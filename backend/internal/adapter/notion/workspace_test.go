package notion_test

import (
	"context"
	"reflect"
	"testing"

	"reading-tracker/backend/internal/adapter/notion"
	"reading-tracker/backend/internal/adapter/notion/notiontest"
	"reading-tracker/backend/internal/domain"
)

var (
	ctx = context.Background()
)

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

type fixture struct {
	gw *notion.Gateway
	ns *notiontest.Server
}

func newFixture(t *testing.T) *fixture {
	ns := notiontest.New(t)
	return &fixture{gw: &notion.Gateway{Client: &notion.Client{BaseURL: ns.URL, Version: "2025-09-03"}}, ns: ns}
}

func TestResolveTemplateHappyPath(t *testing.T) {
	f := newFixture(t)
	tpl := f.ns.AddTemplate("tpl-1")
	db, err := f.gw.ResolveTemplate(ctx, "tok", "tpl-1")
	must(t, err)
	want := tpl.DBConfig()
	if db.LibraryDSID != want.LibraryDSID || db.DailyDSID != want.DailyDSID || db.LibraryURL != want.LibraryURL ||
		len(db.Props) != 16 || db.Props["progress"] != notiontest.PropID("Progress") || db.Props["day_articles"] != notiontest.PropID("Articles") {
		t.Fatalf("got %+v", db)
	}
}

// T15 AC: template missing Progress → property_missing:Progress.
func TestResolveTemplateMissingProgress(t *testing.T) {
	f := newFixture(t)
	tpl := f.ns.AddTemplate("tpl-1")
	f.ns.RemoveProperty(tpl.LibraryDS, "Progress")
	_, err := f.gw.ResolveTemplate(ctx, "tok", "tpl-1")
	if domain.SetupReason(err) != "property_missing:Progress" {
		t.Fatalf("reason %q (err %v)", domain.SetupReason(err), err)
	}
}

func TestResolveTemplateWrongType(t *testing.T) {
	f := newFixture(t)
	tpl := f.ns.AddTemplate("tpl-1")
	f.ns.SetPropertyType(tpl.LibraryDS, "Progress", "rich_text")
	if _, err := f.gw.ResolveTemplate(ctx, "tok", "tpl-1"); domain.SetupReason(err) != "property_type:Progress" {
		t.Fatalf("reason %q", domain.SetupReason(err))
	}
}

func TestResolveTemplateNotFound(t *testing.T) {
	f := newFixture(t)
	if _, err := f.gw.ResolveTemplate(ctx, "tok", "missing"); domain.SetupReason(err) != "template_not_found" {
		t.Fatalf("reason %q", domain.SetupReason(err))
	}
}

func TestResolveTemplateMissingDailyLog(t *testing.T) {
	f := newFixture(t)
	tpl := f.ns.AddTemplate("tpl-1")
	f.ns.SetBlocks("tpl-1", notiontest.ChildDatabase(tpl.LibraryDB, notion.LibraryTitle))
	if _, err := f.gw.ResolveTemplate(ctx, "tok", "tpl-1"); domain.SetupReason(err) != "database_missing:Daily Log" {
		t.Fatalf("reason %q", domain.SetupReason(err))
	}
}

// I2: a Daily Log whose Articles relation targets another data source is rejected.
func TestResolveTemplateRelationToOtherLibrary(t *testing.T) {
	f := newFixture(t)
	tpl := f.ns.AddTemplate("tpl-1")
	f.ns.SetRelationTarget(tpl.DailyDS, "Articles", "some-other-ds")
	if _, err := f.gw.ResolveTemplate(ctx, "tok", "tpl-1"); domain.SetupReason(err) != "property_type:Articles" {
		t.Fatalf("reason %q (err %v)", domain.SetupReason(err), err)
	}
}

// Interpretation 1: user-owned columns are not required.
func TestResolveIgnoresUserOwnedColumns(t *testing.T) {
	f := newFixture(t)
	tpl := f.ns.AddTemplate("tpl-1")
	f.ns.RemoveProperty(tpl.LibraryDS, "Tags")
	if _, err := f.gw.ResolveTemplate(ctx, "tok", "tpl-1"); err != nil {
		t.Fatalf("deleting a user-owned column must not break setup: %v", err)
	}
}

func TestReasonForUnexpectedError(t *testing.T) {
	f := newFixture(t)
	f.ns.AddTemplate("tpl-1")
	f.ns.FailNext("GET", "/v1/blocks/tpl-1/children", notiontest.Fail{Status: 502, Code: "bad_gateway"})
	if _, err := f.gw.ResolveTemplate(ctx, "tok", "tpl-1"); domain.SetupReason(err) != "notion_error" {
		t.Fatalf("reason %q", domain.SetupReason(err))
	}
}

// T15 AC (service half): both databases created; Daily Log relates to the Library data source.
func TestCreateDatabasesBuildsBothAndLinksRelation(t *testing.T) {
	f := newFixture(t)
	db, err := f.gw.ProvisionDatabases(ctx, "tok", "parent-1")
	must(t, err)
	calls := f.ns.Calls("POST", "/v1/databases")
	if len(calls) != 2 {
		t.Fatalf("%d database creates", len(calls))
	}
	libProps := calls[0].Body["initial_data_source"].(map[string]any)["properties"].(map[string]any)
	for _, name := range []string{"Tags", "Status", "Rating", "Notes", "Progress"} {
		if _, ok := libProps[name]; !ok {
			t.Errorf("library schema lacks %s", name)
		}
	}
	if libProps["Progress"].(map[string]any)["number"].(map[string]any)["format"] != "percent" {
		t.Error("Progress must be a percent number")
	}
	dailyProps := calls[1].Body["initial_data_source"].(map[string]any)["properties"].(map[string]any)
	rel := dailyProps["Articles"].(map[string]any)["relation"].(map[string]any)
	if rel["data_source_id"] != db.LibraryDSID || db.LibraryDSID == "" {
		t.Fatalf("relation points at %v, library ds %q", rel["data_source_id"], db.LibraryDSID)
	}
	if len(db.Props) != 16 {
		t.Fatalf("props %v", db.Props)
	}
}

// A retry of "Finish setup" must not create a second pair of databases.
func TestCreateDatabasesIsIdempotent(t *testing.T) {
	f := newFixture(t)
	first, err := f.gw.ProvisionDatabases(ctx, "tok", "parent-1")
	must(t, err)
	second, err := f.gw.ProvisionDatabases(ctx, "tok", "parent-1")
	must(t, err)
	if n := len(f.ns.Calls("POST", "/v1/databases")); n != 2 {
		t.Fatalf("%d database creates across two calls, want 2", n)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("second call differs:\n first %+v\nsecond %+v", first, second)
	}
}

// A prior run that created only the Library (Daily Log failed) is completed, not duplicated.
func TestCreateDatabasesReusesExistingLibrary(t *testing.T) {
	f := newFixture(t)
	f.ns.AddDatabase("lib-db", "lib-ds", notiontest.LibrarySchema)
	f.ns.SetBlocks("parent-1", notiontest.ChildDatabase("lib-db", notion.LibraryTitle))

	db, err := f.gw.ProvisionDatabases(ctx, "tok", "parent-1")
	must(t, err)
	calls := f.ns.Calls("POST", "/v1/databases")
	if len(calls) != 1 {
		t.Fatalf("%d database creates, want only the Daily Log", len(calls))
	}
	dailyProps := calls[0].Body["initial_data_source"].(map[string]any)["properties"].(map[string]any)
	rel := dailyProps["Articles"].(map[string]any)["relation"].(map[string]any)
	if rel["data_source_id"] != "lib-ds" {
		t.Fatalf("relation points at %v, want the pre-seeded library data source", rel["data_source_id"])
	}
	if db.LibraryDBID != "lib-db" || db.LibraryDSID != "lib-ds" || db.DailyDSID == "" || len(db.Props) != 16 {
		t.Fatalf("got %+v", db)
	}
}

// I2 / scenario 10: the user deleted the Library; the Daily Log left under the parent
// still relates to it, so setup creates a new Library and a new Daily Log related to it.
func TestCreateDatabasesSkipsDailyLogRelatedToDeletedLibrary(t *testing.T) {
	f := newFixture(t)
	f.ns.AddDatabase("old-daily-db", "old-daily-ds", notiontest.DailySchema)
	f.ns.SetRelationTarget("old-daily-ds", "Articles", "deleted-lib-ds")
	f.ns.SetBlocks("parent-1", notiontest.ChildDatabase("old-daily-db", notion.DailyTitle))

	db, err := f.gw.ProvisionDatabases(ctx, "tok", "parent-1")
	must(t, err)
	calls := f.ns.Calls("POST", "/v1/databases")
	if len(calls) != 2 {
		t.Fatalf("%d database creates, want a new Library and a new Daily Log", len(calls))
	}
	rel := calls[1].Body["initial_data_source"].(map[string]any)["properties"].(map[string]any)["Articles"].(map[string]any)["relation"].(map[string]any)
	if db.DailyDBID == "old-daily-db" || db.LibraryDSID == "" || rel["data_source_id"] != db.LibraryDSID {
		t.Fatalf("got %+v, new Daily Log relation targets %v", db, rel["data_source_id"])
	}
}

// I2: among several Daily Logs under the parent, the first one related to the Library is reused.
func TestCreateDatabasesReusesDailyLogRelatedToLibrary(t *testing.T) {
	f := newFixture(t)
	f.ns.AddDatabase("lib-db", "lib-ds", notiontest.LibrarySchema)
	f.ns.AddDatabase("stale-daily-db", "stale-daily-ds", notiontest.DailySchema)
	f.ns.SetRelationTarget("stale-daily-ds", "Articles", "deleted-lib-ds")
	f.ns.AddDatabase("daily-db", "daily-ds", notiontest.DailySchema)
	f.ns.SetRelationTarget("daily-ds", "Articles", "lib-ds")
	f.ns.SetBlocks("parent-1", notiontest.ChildDatabase("lib-db", notion.LibraryTitle),
		notiontest.ChildDatabase("stale-daily-db", notion.DailyTitle), notiontest.ChildDatabase("daily-db", notion.DailyTitle))

	db, err := f.gw.ProvisionDatabases(ctx, "tok", "parent-1")
	must(t, err)
	if n := len(f.ns.Calls("POST", "/v1/databases")); n != 0 {
		t.Fatalf("%d database creates, want 0", n)
	}
	if db.LibraryDSID != "lib-ds" || db.DailyDBID != "daily-db" || db.DailyDSID != "daily-ds" {
		t.Fatalf("got %+v", db)
	}
}

func TestCreateDatabasesExistingLibraryGone(t *testing.T) {
	f := newFixture(t)
	f.ns.SetBlocks("parent-1", notiontest.ChildDatabase("ghost-db", notion.LibraryTitle))
	_, err := f.gw.ProvisionDatabases(ctx, "tok", "parent-1")
	if domain.SetupReason(err) != "database_missing:Reading Library" {
		t.Fatalf("reason %q (err %v)", domain.SetupReason(err), err)
	}
	if n := len(f.ns.Calls("POST", "/v1/databases")); n != 0 {
		t.Fatalf("%d database creates, want 0", n)
	}
}

func TestCreateDatabasesListingFailureCreatesNothing(t *testing.T) {
	f := newFixture(t)
	f.ns.FailNext("GET", "/v1/blocks/parent-1/children", notiontest.Fail{Status: 502, Code: "bad_gateway"})
	_, err := f.gw.ProvisionDatabases(ctx, "tok", "parent-1")
	if domain.SetupReason(err) != "notion_error" {
		t.Fatalf("reason %q (err %v)", domain.SetupReason(err), err)
	}
	if n := len(f.ns.Calls("POST", "/v1/databases")); n != 0 {
		t.Fatalf("%d database creates, want 0", n)
	}
}
