package setup_test

import (
	"context"
	"reflect"
	"testing"
	"time"

	"reading-tracker/backend/internal/notion"
	"reading-tracker/backend/internal/notion/notiontest"
	"reading-tracker/backend/internal/queue"
	"reading-tracker/backend/internal/setup"
	"reading-tracker/backend/internal/store"
	"reading-tracker/backend/internal/store/storetest"
)

var (
	ctx     = context.Background()
	testNow = time.Date(2026, 10, 6, 3, 0, 0, 0, time.UTC)
)

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

type fixture struct {
	svc *setup.Service
	ns  *notiontest.Server
	db  *storetest.Env
	rec *queue.Recorder
}

func newFixture(t *testing.T) *fixture {
	ns := notiontest.New(t)
	db := storetest.New(t)
	rec := &queue.Recorder{}
	svc := &setup.Service{Notion: &notion.Client{BaseURL: ns.URL, Version: "2025-09-03"}, Store: db.Store, Queue: rec,
		Bucket: 120 * time.Second, Now: func() time.Time { return testNow }}
	return &fixture{svc: svc, ns: ns, db: db, rec: rec}
}

func TestResolveTemplateHappyPath(t *testing.T) {
	f := newFixture(t)
	tpl := f.ns.AddTemplate("tpl-1")
	db, err := f.svc.ResolveTemplate(ctx, "tok", "tpl-1")
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
	_, err := f.svc.ResolveTemplate(ctx, "tok", "tpl-1")
	if setup.Reason(err) != "property_missing:Progress" {
		t.Fatalf("reason %q (err %v)", setup.Reason(err), err)
	}
}

func TestResolveTemplateWrongType(t *testing.T) {
	f := newFixture(t)
	tpl := f.ns.AddTemplate("tpl-1")
	f.ns.SetPropertyType(tpl.LibraryDS, "Progress", "rich_text")
	if _, err := f.svc.ResolveTemplate(ctx, "tok", "tpl-1"); setup.Reason(err) != "property_type:Progress" {
		t.Fatalf("reason %q", setup.Reason(err))
	}
}

func TestResolveTemplateNotFound(t *testing.T) {
	f := newFixture(t)
	if _, err := f.svc.ResolveTemplate(ctx, "tok", "missing"); setup.Reason(err) != "template_not_found" {
		t.Fatalf("reason %q", setup.Reason(err))
	}
}

func TestResolveTemplateMissingDailyLog(t *testing.T) {
	f := newFixture(t)
	tpl := f.ns.AddTemplate("tpl-1")
	f.ns.SetBlocks("tpl-1", notiontest.ChildDatabase(tpl.LibraryDB, setup.LibraryTitle))
	if _, err := f.svc.ResolveTemplate(ctx, "tok", "tpl-1"); setup.Reason(err) != "database_missing:Daily Log" {
		t.Fatalf("reason %q", setup.Reason(err))
	}
}

// Interpretation 1: user-owned columns are not required.
func TestResolveIgnoresUserOwnedColumns(t *testing.T) {
	f := newFixture(t)
	tpl := f.ns.AddTemplate("tpl-1")
	f.ns.RemoveProperty(tpl.LibraryDS, "Tags")
	if _, err := f.svc.ResolveTemplate(ctx, "tok", "tpl-1"); err != nil {
		t.Fatalf("deleting a user-owned column must not break setup: %v", err)
	}
}

func TestReasonForUnexpectedError(t *testing.T) {
	f := newFixture(t)
	f.ns.AddTemplate("tpl-1")
	f.ns.FailNext("GET", "/v1/blocks/tpl-1/children", notiontest.Fail{Status: 502, Code: "bad_gateway"})
	if _, err := f.svc.ResolveTemplate(ctx, "tok", "tpl-1"); setup.Reason(err) != "notion_error" {
		t.Fatalf("reason %q", setup.Reason(err))
	}
}

// T15 AC (service half): both databases created; Daily Log relates to the Library data source.
func TestCreateDatabasesBuildsBothAndLinksRelation(t *testing.T) {
	f := newFixture(t)
	db, err := f.svc.CreateDatabases(ctx, "tok", "parent-1")
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
	first, err := f.svc.CreateDatabases(ctx, "tok", "parent-1")
	must(t, err)
	second, err := f.svc.CreateDatabases(ctx, "tok", "parent-1")
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
	f.ns.SetBlocks("parent-1", notiontest.ChildDatabase("lib-db", setup.LibraryTitle))

	db, err := f.svc.CreateDatabases(ctx, "tok", "parent-1")
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

func TestCreateDatabasesExistingLibraryGone(t *testing.T) {
	f := newFixture(t)
	f.ns.SetBlocks("parent-1", notiontest.ChildDatabase("ghost-db", setup.LibraryTitle))
	_, err := f.svc.CreateDatabases(ctx, "tok", "parent-1")
	if setup.Reason(err) != "database_missing:Reading Library" {
		t.Fatalf("reason %q (err %v)", setup.Reason(err), err)
	}
	if n := len(f.ns.Calls("POST", "/v1/databases")); n != 0 {
		t.Fatalf("%d database creates, want 0", n)
	}
}

func TestCreateDatabasesListingFailureCreatesNothing(t *testing.T) {
	f := newFixture(t)
	f.ns.FailNext("GET", "/v1/blocks/parent-1/children", notiontest.Fail{Status: 502, Code: "bad_gateway"})
	_, err := f.svc.CreateDatabases(ctx, "tok", "parent-1")
	if setup.Reason(err) != "notion_error" {
		t.Fatalf("reason %q (err %v)", setup.Reason(err), err)
	}
	if n := len(f.ns.Calls("POST", "/v1/databases")); n != 0 {
		t.Fatalf("%d database creates, want 0", n)
	}
}

// T22: resync enqueues every article/day whose version is ahead of Notion.
func TestResyncEnqueuesPending(t *testing.T) {
	f := newFixture(t)
	for i, u := range []string{"https://a.example/1", "https://b.example/2"} {
		_, err := f.db.Store.PushSegment(ctx, store.SegmentWrite{
			InstallationID: "bot-1", DeviceID: "d", SegmentID: []string{"00000000-0000-4000-8000-000000000001", "00000000-0000-4000-8000-000000000002"}[i],
			SessionID: "s", URLHash: store.URLHash(u), Date: "2026-10-06", StartedAt: testNow, EndedAt: testNow.Add(time.Minute),
			ActiveSeconds: 60, MaxProgress: 0.5, Article: store.ArticleMeta{CanonicalURL: u, Source: "generic"},
		})
		must(t, err)
	}
	must(t, f.db.Store.MarkArticleSynced(ctx, "bot-1", store.URLHash("https://a.example/1"), 1, false, testNow))

	must(t, f.svc.Resync(ctx, "bot-1"))
	got := f.rec.Messages()
	want := []queue.Message{queue.Article("bot-1", store.URLHash("https://b.example/2")), queue.Day("bot-1", "2026-10-06")}
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}
