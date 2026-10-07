package notionsync

import (
	"reflect"
	"strings"
	"testing"

	"reading-tracker/backend/internal/adapter/notion"
	"reading-tracker/backend/internal/adapter/notion/notiontest"
	"reading-tracker/backend/internal/adapter/queue"
	"reading-tracker/backend/internal/domain"
)

func articleMsg() queue.Message { return queue.Article(testBot, domain.URLHash(artURL)) }

func (e *wenv) libraryPages() []*notiontest.Page { return e.ns.Pages(e.tpl.LibraryDS) }

func (e *wenv) onlyLibraryPage() *notiontest.Page {
	e.t.Helper()
	pages := e.libraryPages()
	if len(pages) != 1 {
		e.t.Fatalf("%d library pages, want 1", len(pages))
	}
	return pages[0]
}

func (e *wenv) article(url string) *domain.Article {
	e.t.Helper()
	a, err := e.db.Store.GetArticle(ctx, testBot, domain.URLHash(url))
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
	for _, p := range notion.LibraryProps {
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

// An adoptable page that vanished between the URL query and the PATCH is not a
// missing database: fall through and create a fresh page in the same call.
func TestArticleAdoptedPageGoneIsRecreated(t *testing.T) {
	e := newEnv(t)
	e.seedInstallation("tok", "rt")
	existing := e.ns.AddPage(e.tpl.LibraryDS, map[string]any{pid("URL"): map[string]any{"url": artURL}})
	e.ns.FailNext("PATCH", "/v1/pages/"+existing.ID, notiontest.Fail{Status: 404, Code: "object_not_found", Message: "gone"})
	e.read(artURL, 0, 120, 0.5)
	if f := e.handle(articleMsg()); len(f) != 0 {
		t.Fatalf("failures %+v", f)
	}
	inst, err := e.db.Store.GetInstallation(ctx, testBot)
	must(t, err)
	if inst.Status != domain.StatusActive {
		t.Fatalf("installation %+v", inst)
	}
	pages := e.libraryPages()
	if len(pages) != 2 || len(e.ns.Calls("POST", "/v1/pages")) != 1 {
		t.Fatalf("%d library pages, %d creates", len(pages), len(e.ns.Calls("POST", "/v1/pages")))
	}
	if a := e.article(artURL); a.NotionPageID != pages[1].ID || a.NotionPageID == existing.ID {
		t.Fatalf("article %+v", a)
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
	if inst.Status != domain.StatusNeedsSetup || inst.SetupError != "database_missing:Reading Library" {
		t.Fatalf("installation %+v", inst)
	}
}

func TestArticleSkippedWhenInstallationNotActive(t *testing.T) {
	e := newEnv(t)
	e.seedInstallation("tok", "rt")
	must(t, e.db.Store.SetInstallationStatus(ctx, testBot, domain.StatusRevoked, "", e.now))
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
	e.readMeta(domain.ArticleMeta{CanonicalURL: long, Source: "generic", SiteName: "Better Programming, Inc", WordCount: 0}, 0, 120, 0.5)
	if f := e.handle(queue.Article(testBot, domain.URLHash(long))); len(f) != 0 {
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

// I1 / scenario 10: a page still in a previous Library (the databases were replaced by
// setup) counts as gone: the id is cleared, the message requeued, and the page re-created
// in the current Library.
func TestArticlePageInOldLibraryIsRecreated(t *testing.T) {
	e := newEnv(t)
	e.seedInstallation("tok", "rt")
	old := e.ns.AddTemplate("old-tpl")
	stale := e.ns.AddPage(old.LibraryDS, map[string]any{pid("URL"): map[string]any{"url": artURL}})
	e.ns.DeleteDataSource(old.LibraryDS)
	e.read(artURL, 0, 120, 0.5)
	must(t, e.db.Store.SetArticlePage(ctx, testBot, domain.URLHash(artURL), stale.ID, stale.URL, old.LibraryDS))

	if f := e.handle(articleMsg()); len(f) != 0 {
		t.Fatalf("failures %+v", f)
	}
	if a := e.article(artURL); a.NotionPageID != "" || a.NotionSyncedVersion != 0 {
		t.Fatalf("page in the old Library kept: %+v", a)
	}
	sent := e.q.Drain()
	if len(sent) != 1 || sent[0].Message != articleMsg() {
		t.Fatalf("requeue %+v", sent)
	}
	e.handle(sent[0].Message)
	pg := e.onlyLibraryPage()
	if a := e.article(artURL); a.NotionPageID != pg.ID || a.NotionURL != pg.URL || a.NotionSyncedVersion != 1 {
		t.Fatalf("article %+v, new page %s", a, pg.ID)
	}
}

// switchLibrary points the installation at a second, surviving Library data source,
// as setup on a different parent page would (R1).
func (e *wenv) switchLibrary() string {
	e.t.Helper()
	e.ns.AddDatabase("lib2-db", "lib2-ds", notiontest.LibrarySchema)
	db := e.tpl.DBConfig()
	db.LibraryDBID, db.LibraryDSID = "lib2-db", "lib2-ds"
	must(e.t, e.db.Store.SaveInstallationDatabases(ctx, testBot, db, e.now))
	return "lib2-ds"
}

// Review focus 4 / R1: a page in a database the installation no longer targets is
// re-created in the current one without PATCHing the old page (whose property ids
// the new database does not have), even though the old database still exists.
func TestArticleInOldDatabaseIsRecreatedBeforePatch(t *testing.T) {
	e := newEnv(t)
	e.seedInstallation("tok", "rt")
	e.read(artURL, 0, 120, 0.5)
	e.handle(articleMsg())
	old := e.onlyLibraryPage()
	if a := e.article(artURL); a.NotionDataSourceID != e.tpl.LibraryDS {
		t.Fatalf("data source not recorded: %+v", a)
	}

	newDS := e.switchLibrary()
	e.read(artURL, 1, 60, 0.6)
	e.q.Drain()
	if f := e.handle(articleMsg()); len(f) != 0 {
		t.Fatalf("failures %+v", f)
	}
	if n := len(e.ns.Calls("PATCH", "/v1/pages/"+old.ID)); n != 0 {
		t.Fatalf("PATCHed the old page %d times", n)
	}
	sent := e.q.Drain()
	if len(sent) != 1 || sent[0].Message != articleMsg() {
		t.Fatalf("requeue %+v", sent)
	}
	e.handle(sent[0].Message)
	pages := e.ns.Pages(newDS)
	a := e.article(artURL)
	if len(pages) != 1 || a.NotionPageID != pages[0].ID || a.NotionDataSourceID != newDS {
		t.Fatalf("not re-created in the new Library: pages %d, article %+v", len(pages), a)
	}
}

// R1: a row linked to a page but with no recorded data source (written before the id
// was stored) is stale: it is not PATCHed in place, it is requeued, and the requeued job
// adopts the page and records the current data source, so it does not loop.
func TestArticlePageWithoutDataSourceIsRelinked(t *testing.T) {
	e := newEnv(t)
	e.seedInstallation("tok", "rt")
	pg := e.ns.AddPage(e.tpl.LibraryDS, map[string]any{pid("URL"): map[string]any{"url": artURL}})
	e.read(artURL, 0, 120, 0.5)
	must(t, e.db.Store.SetArticlePage(ctx, testBot, domain.URLHash(artURL), pg.ID, pg.URL, ""))

	if f := e.handle(articleMsg()); len(f) != 0 {
		t.Fatalf("failures %+v", f)
	}
	if n := len(e.ns.Calls("PATCH", "/v1/pages/"+pg.ID)); n != 1 {
		t.Fatalf("PATCHed in place %d times, want 1", n)
	}
	if more := e.q.Drain(); len(more) != 0 {
		t.Fatalf("requeued: %+v", more)
	}
	a := e.article(artURL)
	if a.NotionPageID != pg.ID || a.NotionDataSourceID != e.tpl.LibraryDS || a.NotionSyncedVersion != 1 {
		t.Fatalf("article %+v", a)
	}
}

// R1: a legacy row for a URL Notion cannot query (> 2000 chars) must be PATCHed in place, never re-created.
func TestArticleLongURLWithoutDataSourceIsNotDuplicated(t *testing.T) {
	e := newEnv(t)
	e.seedInstallation("tok", "rt")
	long := "https://example.com/" + strings.Repeat("a", 2001-len("https://example.com/"))
	pg := e.ns.AddPage(e.tpl.LibraryDS, map[string]any{pid("URL"): map[string]any{"url": long}})
	e.read(long, 0, 120, 0.5)
	must(t, e.db.Store.SetArticlePage(ctx, testBot, domain.URLHash(long), pg.ID, pg.URL, ""))

	msg := queue.Article(testBot, domain.URLHash(long))
	if f := e.handle(msg); len(f) != 0 {
		t.Fatalf("failures %+v", f)
	}
	if n := len(e.libraryPages()); n != 1 {
		t.Fatalf("%d library pages, want 1", n)
	}
	if n := len(e.ns.Calls("POST", "/v1/pages")); n != 0 {
		t.Fatalf("%d creates", n)
	}
	a := e.article(long)
	if a.NotionPageID != pg.ID || a.NotionDataSourceID != e.tpl.LibraryDS {
		t.Fatalf("article %+v", a)
	}
	if more := e.q.Drain(); len(more) != 0 {
		t.Fatalf("requeued: %+v", more)
	}
}

// R1: no recorded data source and the PATCH reports the page gone, or rejects the property ids (the page sits in an
// old database): forget and requeue.
func TestArticleWithoutDataSourceFallsBackToForget(t *testing.T) {
	for name, f := range map[string]notiontest.Fail{
		"page gone": {Status: 404, Code: "object_not_found", Message: "gone"},
		"400":       {Status: 400, Code: "validation_error", Message: "property does not exist"},
	} {
		t.Run(name, func(t *testing.T) {
			e := newEnv(t)
			e.seedInstallation("tok", "rt")
			pg := e.ns.AddPage(e.tpl.LibraryDS, map[string]any{pid("URL"): map[string]any{"url": artURL}})
			e.read(artURL, 0, 120, 0.5)
			must(t, e.db.Store.SetArticlePage(ctx, testBot, domain.URLHash(artURL), pg.ID, pg.URL, ""))
			e.ns.FailNext("PATCH", "/v1/pages/"+pg.ID, f)

			if fl := e.handle(articleMsg()); len(fl) != 0 {
				t.Fatalf("failures %+v", fl)
			}
			if a := e.article(artURL); a.NotionPageID != "" {
				t.Fatalf("page link not cleared: %+v", a)
			}
			sent := e.q.Drain()
			if len(sent) != 1 || sent[0].Message != articleMsg() {
				t.Fatalf("requeue %+v", sent)
			}
		})
	}
}

// R1: a transient Notion failure on a legacy row keeps the page link; the job fails and is retried.
func TestArticleWithoutDataSourceKeepsLinkOnTransientFailure(t *testing.T) {
	for name, f := range map[string]notiontest.Fail{
		"429": {Status: 429, Code: "rate_limited", Message: "slow down"},
		"503": {Status: 503, Code: "service_unavailable", Message: "down"},
	} {
		t.Run(name, func(t *testing.T) {
			e := newEnv(t)
			e.seedInstallation("tok", "rt")
			pg := e.ns.AddPage(e.tpl.LibraryDS, map[string]any{pid("URL"): map[string]any{"url": artURL}})
			e.read(artURL, 0, 120, 0.5)
			must(t, e.db.Store.SetArticlePage(ctx, testBot, domain.URLHash(artURL), pg.ID, pg.URL, ""))
			e.ns.FailNext("PATCH", "/v1/pages/"+pg.ID, f)

			if fl := e.handle(articleMsg()); len(fl) != 1 {
				t.Fatalf("want the job to fail for retry, failures %+v", fl)
			}
			if a := e.article(artURL); a.NotionPageID != pg.ID {
				t.Fatalf("page link dropped on a transient failure: %+v", a)
			}
			if sent := e.q.Drain(); len(sent) != 0 {
				t.Fatalf("requeued %+v", sent)
			}
		})
	}
}

// R1: a recorded, different data source is stale: forget, requeue, then adopt or create in the current Library.
func TestArticlePageInOtherDataSourceIsRelinked(t *testing.T) {
	e := newEnv(t)
	e.seedInstallation("tok", "rt")
	pg := e.ns.AddPage(e.tpl.LibraryDS, map[string]any{pid("URL"): map[string]any{"url": artURL}})
	e.read(artURL, 0, 120, 0.5)
	must(t, e.db.Store.SetArticlePage(ctx, testBot, domain.URLHash(artURL), pg.ID, pg.URL, "old-ds"))

	if f := e.handle(articleMsg()); len(f) != 0 {
		t.Fatalf("failures %+v", f)
	}
	if n := len(e.ns.Calls("PATCH", "/v1/pages/"+pg.ID)); n != 0 {
		t.Fatalf("PATCHed in place %d times", n)
	}
	sent := e.q.Drain()
	if len(sent) != 1 || sent[0].Message != articleMsg() {
		t.Fatalf("requeue %+v", sent)
	}
	e.handle(sent[0].Message)
	a := e.article(artURL)
	if a.NotionPageID != pg.ID || a.NotionDataSourceID != e.tpl.LibraryDS || a.NotionSyncedVersion != 1 {
		t.Fatalf("article %+v", a)
	}
	if more := e.q.Drain(); len(more) != 0 {
		t.Fatalf("looping: %+v", more)
	}
}
