package worker

import (
	"reflect"
	"strings"
	"testing"

	"reading-tracker/backend/internal/config"
	"reading-tracker/backend/internal/notion/notiontest"
	"reading-tracker/backend/internal/queue"
	"reading-tracker/backend/internal/setup"
	"reading-tracker/backend/internal/store"
)

func articleMsg() queue.Message { return queue.Article(testBot, store.URLHash(artURL)) }

func (e *wenv) libraryPages() []*notiontest.Page { return e.ns.Pages(e.tpl.LibraryDS) }

func (e *wenv) onlyLibraryPage() *notiontest.Page {
	e.t.Helper()
	pages := e.libraryPages()
	if len(pages) != 1 {
		e.t.Fatalf("%d library pages, want 1", len(pages))
	}
	return pages[0]
}

func (e *wenv) article(url string) *store.Article {
	e.t.Helper()
	a, err := e.db.Store.GetArticle(ctx, testBot, store.URLHash(url))
	must(e.t, err)
	return a
}

// T19 AC + scenario 5: below min_create_seconds → no Notion call; crossing it creates the page.
func TestArticleBelowThresholdThenCreated(t *testing.T) {
	e := newEnv(t)
	e.seedInstallation("tok", "rt")
	e.read(artURL, 0, 20, 0.1)
	if f := e.handle(articleMsg()); len(f) != 0 {
		t.Fatalf("failures %+v", f)
	}
	if n := len(e.ns.Requests()); n != 0 {
		t.Fatalf("%d Notion calls below threshold", n)
	}
	e.read(artURL, 1, 15, 0.2)
	e.handle(articleMsg())
	pg := e.onlyLibraryPage()
	a := e.article(artURL)
	if a.NotionPageID != pg.ID || a.NotionURL != pg.URL || a.NotionSyncedVersion != 2 {
		t.Fatalf("article %+v", a)
	}
}

// C1 + C9: creation writes server-owned properties only, keyed by property id.
func TestArticleCreateWritesServerOwnedPropsOnly(t *testing.T) {
	e := newEnv(t)
	e.seedInstallation("tok", "rt")
	e.read(artURL, 0, 120, 0.5)
	e.handle(articleMsg())
	pg := e.onlyLibraryPage()
	allowed := map[string]bool{}
	for _, p := range setup.LibraryProps {
		allowed[pid(p.Name)] = true
	}
	for k := range pg.Properties {
		if !allowed[k] {
			t.Errorf("wrote non-server-owned property %q", k)
		}
	}
	if num(pg, "Reading time (min)") != 2 || num(pg, "Progress") != 0.5 || num(pg, "Sessions") != 1 || num(pg, "Word count") != 1000 {
		t.Fatalf("metrics %+v", pg.Properties)
	}
	if pg.Properties[pid("URL")].(map[string]any)["url"] != artURL {
		t.Fatal("URL not written")
	}
	title := pg.Properties[pid("Name")].(map[string]any)["title"].([]any)[0].(map[string]any)["text"].(map[string]any)["content"]
	if title != "Designing Idempotent APIs" || len(pg.Children) != 1 {
		t.Fatalf("title %v children %v", title, pg.Children)
	}
}

// T19 AC: an existing page found by URL is adopted (and brought up to date), not duplicated.
func TestArticleAdoptsExistingPageByURL(t *testing.T) {
	e := newEnv(t)
	e.seedInstallation("tok", "rt")
	existing := e.ns.AddPage(e.tpl.LibraryDS, map[string]any{pid("URL"): map[string]any{"url": artURL}})
	e.read(artURL, 0, 120, 0.5)
	e.handle(articleMsg())
	if n := len(e.ns.Calls("POST", "/v1/pages")); n != 0 {
		t.Fatalf("created %d pages instead of adopting", n)
	}
	if e.article(artURL).NotionPageID != existing.ID || num(existing, "Reading time (min)") != 2 {
		t.Fatal("existing page not adopted/updated")
	}
}

// T19 AC: the PATCH body contains only server-owned metric property ids.
func TestArticlePatchContainsOnlyMetricProps(t *testing.T) {
	e := newEnv(t)
	e.seedInstallation("tok", "rt")
	e.read(artURL, 0, 120, 0.5)
	e.handle(articleMsg())
	e.read(artURL, 1, 60, 0.8)
	e.handle(articleMsg())
	patches := e.ns.Calls("PATCH", "/v1/pages/")
	if len(patches) != 1 {
		t.Fatalf("%d patches", len(patches))
	}
	props := patches[0].Body["properties"].(map[string]any)
	want := map[string]bool{pid("Last read"): true, pid("Reading time (min)"): true, pid("Progress"): true, pid("Sessions"): true, pid("Completed"): true}
	if len(props) != len(want) {
		t.Fatalf("patched %v", props)
	}
	for k := range props {
		if !want[k] {
			t.Errorf("patched %q", k)
		}
	}
	if num(e.onlyLibraryPage(), "Reading time (min)") != 3 {
		t.Fatal("minutes not updated")
	}
}

// Scenario 7: user edits survive; Completed is its own column.
func TestUserEditsPreserved(t *testing.T) {
	e := newEnv(t)
	e.seedInstallation("tok", "rt")
	e.read(artURL, 0, 120, 0.5)
	e.handle(articleMsg())
	pg := e.onlyLibraryPage()
	abandoned := map[string]any{"status": map[string]any{"name": "Abandoned"}}
	tags := map[string]any{"multi_select": []any{map[string]any{"name": "go"}}}
	e.ns.SetPageProperty(pg.ID, pid("Status"), abandoned)
	e.ns.SetPageProperty(pg.ID, pid("Tags"), tags)

	e.read(artURL, 1, 300, 1.0)
	e.handle(articleMsg())
	if !reflect.DeepEqual(pg.Properties[pid("Status")], abandoned) || !reflect.DeepEqual(pg.Properties[pid("Tags")], tags) {
		t.Fatal("user-owned properties changed")
	}
	if pg.Properties[pid("Completed")].(map[string]any)["checkbox"] != true || !e.article(artURL).Completed {
		t.Fatal("Completed not set")
	}
}

// Scenario 9 / T21 AC: archived page → id cleared, re-enqueued with a requeue dedup id, then re-created.
func TestArticleArchivedPageIsRecreated(t *testing.T) {
	e := newEnv(t)
	e.seedInstallation("tok", "rt")
	e.read(artURL, 0, 120, 0.5)
	e.handle(articleMsg())
	first := e.onlyLibraryPage()
	e.ns.ArchivePage(first.ID)
	e.read(artURL, 1, 60, 0.6)
	e.q.Drain()

	if f := e.handle(articleMsg()); len(f) != 0 {
		t.Fatalf("failures %+v", f)
	}
	if e.article(artURL).NotionPageID != "" {
		t.Fatal("page id not cleared")
	}
	sent := e.q.Drain()
	if len(sent) != 1 || sent[0].Message != articleMsg() || !strings.Contains(sent[0].DedupID, "#r") {
		t.Fatalf("requeue %+v", sent)
	}
	e.handle(sent[0].Message)
	pages := e.libraryPages()
	if len(pages) != 2 || e.article(artURL).NotionPageID != pages[1].ID || num(pages[1], "Reading time (min)") != 3 {
		t.Fatalf("not re-created: %d pages", len(pages))
	}
}

// Scenario 10: Library database deleted → needs_setup, message acked.
func TestArticleDeletedLibraryNeedsSetup(t *testing.T) {
	e := newEnv(t)
	e.seedInstallation("tok", "rt")
	e.ns.DeleteDataSource(e.tpl.LibraryDS)
	e.read(artURL, 0, 120, 0.5)
	if f := e.handle(articleMsg()); len(f) != 0 {
		t.Fatalf("failures %+v", f)
	}
	inst, _ := e.db.Store.GetInstallation(ctx, testBot)
	if inst.Status != store.StatusNeedsSetup || inst.SetupError != "database_missing:Reading Library" {
		t.Fatalf("installation %+v", inst)
	}
}

func TestArticleSkippedWhenInstallationNotActive(t *testing.T) {
	e := newEnv(t)
	e.seedInstallation("tok", "rt")
	must(t, e.db.Store.SetInstallationStatus(ctx, testBot, store.StatusRevoked, "", e.now))
	e.read(artURL, 0, 120, 0.5)
	if f := e.handle(articleMsg()); len(f) != 0 || len(e.ns.Requests()) != 0 {
		t.Fatalf("failures %+v requests %d", f, len(e.ns.Requests()))
	}
}

// Review focus 4: over-long URL and comma'd site name must not poison the message.
func TestArticleLongURLAndCommaSiteStillCreate(t *testing.T) {
	e := newEnv(t)
	e.seedInstallation("tok", "rt")
	long := "https://example.com/" + strings.Repeat("a", 2001)
	e.readMeta(store.ArticleMeta{CanonicalURL: long, Source: "generic", SiteName: "Better Programming, Inc", WordCount: 0}, 0, 120, 0.5)
	if f := e.handle(queue.Article(testBot, store.URLHash(long))); len(f) != 0 {
		t.Fatalf("failures %+v", f)
	}
	pg := e.onlyLibraryPage()
	if _, ok := pg.Properties[pid("URL")]; ok || len(pg.Children) != 0 {
		t.Fatal("URL over 2000 chars must be omitted")
	}
	if site := pg.Properties[pid("Site")].(map[string]any)["select"].(map[string]any)["name"]; site != "Better Programming Inc" {
		t.Fatalf("site %v", site)
	}
	if n := len(e.ns.Calls("POST", "/v1/data_sources/")); n != 0 {
		t.Fatalf("queried by an over-long URL %d times", n)
	}
}

func TestCompletedRule(t *testing.T) {
	tr := config.Tracking{CompleteProgress: 0.9, CompleteTimeRatio: 0.3, WordsPerMinute: 230}
	cases := []struct {
		a    store.Article
		want bool
	}{
		{store.Article{MaxProgress: 0.95, WordCount: 1000, TotalSeconds: 100}, true}, // 100 ≥ 0.3 × 260.9
		{store.Article{MaxProgress: 0.95, WordCount: 1000, TotalSeconds: 50}, false}, // skimmed
		{store.Article{MaxProgress: 0.95, WordCount: 0, TotalSeconds: 5}, true},      // unknown length
		{store.Article{MaxProgress: 0.5, WordCount: 0, TotalSeconds: 999}, false},
		{store.Article{MaxProgress: 0.1, Completed: true}, true}, // sticky
	}
	for i, c := range cases {
		if got := isCompleted(&c.a, tr); got != c.want {
			t.Errorf("case %d: got %v", i, got)
		}
	}
}
