package worker

import (
	"testing"

	"reading-tracker/backend/internal/notion/notiontest"
	"reading-tracker/backend/internal/queue"
	"reading-tracker/backend/internal/store"
)

const today = "2026-10-06"

func dayMsg() queue.Message { return queue.Day(testBot, today) }

func (e *wenv) onlyDayPage() *notiontest.Page {
	e.t.Helper()
	pages := e.ns.Pages(e.tpl.DailyDS)
	if len(pages) != 1 {
		e.t.Fatalf("%d daily pages, want 1", len(pages))
	}
	return pages[0]
}

// T20 AC: Minutes = round(seconds/60); the relation only includes articles that have a page.
func TestDayMinutesAndRelation(t *testing.T) {
	e := newEnv(t)
	e.seedInstallation("tok", "rt")
	short := "https://example.com/short"
	e.read(artURL, 0, 120, 0.5)
	e.read(short, 0, 20, 0.1)  // below min_create_seconds → no page
	e.read(artURL, 1, 10, 0.6) // day total 150 s → round(2.5) = 3
	e.handle(queue.Article(testBot, store.URLHash(artURL)), queue.Article(testBot, store.URLHash(short)), dayMsg())

	pg := e.onlyDayPage()
	if num(pg, "Minutes") != 3 {
		t.Fatalf("minutes %v", num(pg, "Minutes"))
	}
	rel := pg.Properties[pid("Articles")].(map[string]any)["relation"].([]any)
	art := e.onlyLibraryPage()
	if len(rel) != 1 || rel[0].(map[string]any)["id"] != art.ID {
		t.Fatalf("relation %v", rel)
	}
	if pg.Properties[pid("Date")].(map[string]any)["date"].(map[string]any)["start"] != today {
		t.Fatal("date not set")
	}
	d, _ := e.db.Store.GetDay(ctx, testBot, today)
	if d.NotionPageID != pg.ID || d.NotionSyncedVersion != 3 {
		t.Fatalf("day %+v", d)
	}
}

func TestDayAdoptsExistingPageByDate(t *testing.T) {
	e := newEnv(t)
	e.seedInstallation("tok", "rt")
	existing := e.ns.AddPage(e.tpl.DailyDS, map[string]any{pid("Date"): map[string]any{"date": map[string]any{"start": today}}})
	e.read(artURL, 0, 60, 0.5)
	e.handle(dayMsg())
	if n := len(e.ns.Calls("POST", "/v1/pages")); n != 0 {
		t.Fatalf("created %d pages instead of adopting", n)
	}
	if num(existing, "Minutes") != 1 {
		t.Fatal("adopted day page not updated")
	}
}

// An adoptable day page that vanished between the Date query and the PATCH is not a
// missing database: fall through and create a fresh page in the same call.
func TestDayAdoptedPageGoneIsRecreated(t *testing.T) {
	e := newEnv(t)
	e.seedInstallation("tok", "rt")
	existing := e.ns.AddPage(e.tpl.DailyDS, map[string]any{pid("Date"): map[string]any{"date": map[string]any{"start": today}}})
	e.ns.FailNext("PATCH", "/v1/pages/"+existing.ID, notiontest.Fail{Status: 404, Code: "object_not_found", Message: "gone"})
	e.read(artURL, 0, 60, 0.5)
	if f := e.handle(dayMsg()); len(f) != 0 {
		t.Fatalf("failures %+v", f)
	}
	inst, err := e.db.Store.GetInstallation(ctx, testBot)
	must(t, err)
	if inst.Status != store.StatusActive {
		t.Fatalf("installation %+v", inst)
	}
	pages := e.ns.Pages(e.tpl.DailyDS)
	if len(pages) != 2 || len(e.ns.Calls("POST", "/v1/pages")) != 1 {
		t.Fatalf("%d daily pages, %d creates", len(pages), len(e.ns.Calls("POST", "/v1/pages")))
	}
	d, err := e.db.Store.GetDay(ctx, testBot, today)
	must(t, err)
	if d.NotionPageID != pages[1].ID || d.NotionPageID == existing.ID {
		t.Fatalf("day %+v", d)
	}
}

func TestDayPageGoneIsRequeuedAndRecreated(t *testing.T) {
	e := newEnv(t)
	e.seedInstallation("tok", "rt")
	e.read(artURL, 0, 60, 0.5)
	e.handle(dayMsg())
	e.ns.ArchivePage(e.onlyDayPage().ID)
	e.read(artURL, 1, 60, 0.6)
	e.handle(dayMsg())
	sent := e.q.Drain()
	if len(sent) != 1 || sent[0].Message != dayMsg() {
		t.Fatalf("requeue %+v", sent)
	}
	e.handle(sent[0].Message)
	if pages := e.ns.Pages(e.tpl.DailyDS); len(pages) != 2 || num(pages[1], "Minutes") != 2 {
		t.Fatalf("day page not re-created: %d", len(pages))
	}
}

func TestDayDeletedDailyLogNeedsSetup(t *testing.T) {
	e := newEnv(t)
	e.seedInstallation("tok", "rt")
	e.ns.DeleteDataSource(e.tpl.DailyDS)
	e.read(artURL, 0, 60, 0.5)
	if f := e.handle(dayMsg()); len(f) != 0 {
		t.Fatalf("failures %+v", f)
	}
	inst, _ := e.db.Store.GetInstallation(ctx, testBot)
	if inst.Status != store.StatusNeedsSetup || inst.SetupError != "database_missing:Daily Log" {
		t.Fatalf("installation %+v", inst)
	}
}

// I1: a day page still in a previous Daily Log is re-created in the current one.
func TestDayPageInOldDailyLogIsRecreated(t *testing.T) {
	e := newEnv(t)
	e.seedInstallation("tok", "rt")
	old := e.ns.AddTemplate("old-tpl")
	stale := e.ns.AddPage(old.DailyDS, map[string]any{pid("Date"): map[string]any{"date": map[string]any{"start": today}}})
	e.read(artURL, 0, 60, 0.5)
	must(t, e.db.Store.SetDayPage(ctx, testBot, today, stale.ID))

	if f := e.handle(dayMsg()); len(f) != 0 {
		t.Fatalf("failures %+v", f)
	}
	d, err := e.db.Store.GetDay(ctx, testBot, today)
	must(t, err)
	if d.NotionPageID != "" || d.NotionSyncedVersion != 0 {
		t.Fatalf("page in the old Daily Log kept: %+v", d)
	}
	sent := e.q.Drain()
	if len(sent) != 1 || sent[0].Message != dayMsg() {
		t.Fatalf("requeue %+v", sent)
	}
	e.handle(sent[0].Message)
	pg := e.onlyDayPage()
	if d, _ = e.db.Store.GetDay(ctx, testBot, today); d.NotionPageID != pg.ID || num(pg, "Minutes") != 1 {
		t.Fatalf("day %+v, new page %s", d, pg.ID)
	}
}
