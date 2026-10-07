package notiontest_test

import (
	"context"
	"strings"
	"testing"

	"reading-tracker/backend/internal/notion"
	"reading-tracker/backend/internal/notion/notiontest"
)

var ctx = context.Background()

func newClient(ns *notiontest.Server) *notion.Client {
	return &notion.Client{BaseURL: ns.URL, Version: "2025-09-03"}
}

func createBody(dsID string, props map[string]any) map[string]any {
	return map[string]any{
		"parent":     map[string]any{"type": "data_source_id", "data_source_id": dsID},
		"properties": props,
	}
}

func progress(v float64) map[string]any { return map[string]any{"number": v} }

func requireValidationError(t *testing.T, err error, key string) {
	t.Helper()
	e, ok := notion.AsAPIError(err)
	if !ok || e.Status != 400 || e.Code != "validation_error" || !strings.Contains(e.Message, key) {
		t.Fatalf("want 400 validation_error naming %q, got %v", key, err)
	}
}

func TestCreatePageRejectsUnknownPropertyAndStoresNothing(t *testing.T) {
	ns := notiontest.New(t)
	tpl := ns.AddTemplate("tpl")
	c := newClient(ns)

	_, err := c.CreatePage(ctx, "tok", createBody(tpl.LibraryDS, map[string]any{
		notiontest.PropID("Progress"): progress(0.5),
		"id-stale":                    progress(1),
	}))
	requireValidationError(t, err, "id-stale")
	if n := len(ns.Pages(tpl.LibraryDS)); n != 0 {
		t.Fatalf("%d pages stored after a rejected create", n)
	}

	pg, err := c.CreatePage(ctx, "tok", createBody(tpl.LibraryDS, map[string]any{notiontest.PropID("Progress"): progress(0.5)}))
	if err != nil {
		t.Fatalf("known property id rejected: %v", err)
	}
	if got := ns.Page(pg.ID).Properties[notiontest.PropID("Progress")]; got == nil {
		t.Fatal("accepted property not stored as sent")
	}
}

func TestPropertyNameIsAcceptedToo(t *testing.T) {
	ns := notiontest.New(t)
	tpl := ns.AddTemplate("tpl")
	if _, err := newClient(ns).CreatePage(ctx, "tok", createBody(tpl.LibraryDS, map[string]any{"Progress": progress(0.5)})); err != nil {
		t.Fatalf("known property name rejected: %v", err)
	}
}

func TestUpdatePageRejectsUnknownPropertyAndKeepsPage(t *testing.T) {
	ns := notiontest.New(t)
	tpl := ns.AddTemplate("tpl")
	pg := ns.AddPage(tpl.LibraryDS, map[string]any{notiontest.PropID("Progress"): progress(0.1)})
	c := newClient(ns)

	_, err := c.UpdatePage(ctx, "tok", pg.ID, map[string]any{
		notiontest.PropID("Progress"): progress(0.9),
		"id-stale":                    progress(1),
	})
	requireValidationError(t, err, "id-stale")
	if got := ns.Page(pg.ID).Properties[notiontest.PropID("Progress")]; got.(map[string]any)["number"] != 0.1 {
		t.Fatalf("rejected update changed the page: %v", got)
	}

	if _, err := c.UpdatePage(ctx, "tok", pg.ID, map[string]any{notiontest.PropID("Progress"): progress(0.9)}); err != nil {
		t.Fatalf("known property id rejected: %v", err)
	}
	if got := ns.Page(pg.ID).Properties[notiontest.PropID("Progress")]; got.(map[string]any)["number"] != 0.9 {
		t.Fatalf("accepted update not applied: %v", got)
	}
}

// A property deleted from the schema is rejected like any unknown key.
func TestRemovedPropertyIsRejected(t *testing.T) {
	ns := notiontest.New(t)
	tpl := ns.AddTemplate("tpl")
	ns.RemoveProperty(tpl.LibraryDS, "Progress")
	_, err := newClient(ns).CreatePage(ctx, "tok", createBody(tpl.LibraryDS, map[string]any{notiontest.PropID("Progress"): progress(0.5)}))
	requireValidationError(t, err, notiontest.PropID("Progress"))
}

// With the data source deleted there is no schema to validate against.
func TestUpdatePageSkipsValidationWhenDataSourceDeleted(t *testing.T) {
	ns := notiontest.New(t)
	tpl := ns.AddTemplate("tpl")
	pg := ns.AddPage(tpl.LibraryDS, nil)
	ns.DeleteDataSource(tpl.LibraryDS)
	if _, err := newClient(ns).UpdatePage(ctx, "tok", pg.ID, map[string]any{"anything": progress(1)}); err != nil {
		t.Fatalf("update on a page of a deleted data source: %v", err)
	}
}
