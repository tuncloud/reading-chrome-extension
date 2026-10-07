package httpapi_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"reading-tracker/backend/internal/adapter/notion"
	"reading-tracker/backend/internal/adapter/notion/notiontest"
	"reading-tracker/backend/internal/adapter/queue"
	"reading-tracker/backend/internal/domain"
)

const readURL = "https://a.example/1"

// seedSyncedRead stores one read of readURL on 2026-10-06 whose article and day are
// already synced to Notion, as if the worker had processed them.
func (env *testEnv) seedSyncedRead() {
	t, ctx := env.t, context.Background()
	_, err := env.db.Store.PushSegment(ctx, domain.SegmentWrite{InstallationID: testBotID, DeviceID: "d",
		SegmentID: "00000000-0000-4000-8000-000000000001", SessionID: "s", URLHash: domain.URLHash(readURL), Date: "2026-10-06",
		StartedAt: env.now, EndedAt: env.now.Add(time.Minute), ActiveSeconds: 60, MaxProgress: 0.5,
		Article: domain.ArticleMeta{CanonicalURL: readURL, Source: "generic"}})
	must(t, err)
	must(t, env.db.Store.SetArticlePage(ctx, testBotID, domain.URLHash(readURL), "page-old", "https://www.notion.so/page-old"))
	must(t, env.db.Store.MarkArticleSynced(ctx, testBotID, domain.URLHash(readURL), 1, false, env.now))
	must(t, env.db.Store.MarkDaySynced(ctx, testBotID, "2026-10-06", 1))
}

// fullResync is what a resync must enqueue for seedSyncedRead once the databases changed.
func fullResync() []queue.Message {
	return []queue.Message{queue.Article(testBotID, domain.URLHash(readURL)), queue.Day(testBotID, "2026-10-06")}
}

func TestSetupPagesSearchesNotionWithDecryptedToken(t *testing.T) {
	env := newTestEnv(t)
	uid, tok := env.seedUser()
	env.seedInstallation(uid, domain.StatusNeedsSetup)
	env.notion.SetSearchResults(map[string]any{"object": "page", "id": "p1", "url": "https://www.notion.so/p1",
		"properties": map[string]any{"title": map[string]any{"id": "title", "type": "title", "title": []any{map[string]any{"plain_text": "Reading"}}}}})

	rec := env.do("GET", "/v1/setup/pages?q=Read", nil, tok)
	if rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	body := decode[struct {
		Pages []struct{ ID, Title, URL string } `json:"pages"`
	}](t, rec)
	if len(body.Pages) != 1 || body.Pages[0].Title != "Reading" || body.Pages[0].ID != "p1" {
		t.Fatalf("pages %+v", body.Pages)
	}
	calls := env.notion.Calls("POST", "/v1/search")
	if len(calls) != 1 || calls[0].Auth != "Bearer secret-access-token" || calls[0].Body["query"] != "Read" {
		t.Fatalf("search calls %+v", calls)
	}
}

func TestSetupPagesNotionUnauthorizedIsConflict(t *testing.T) {
	env := newTestEnv(t)
	uid, tok := env.seedUser()
	env.seedInstallation(uid, domain.StatusNeedsSetup)
	env.notion.RejectToken("secret-access-token")
	if rec := env.do("GET", "/v1/setup/pages", nil, tok); rec.Code != 409 || errorCode(t, rec) != "conflict" {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
}

// T15 AC: POST /v1/setup creates both DBs and flips the installation to active.
func TestPostSetupCreatesDatabases(t *testing.T) {
	env := newTestEnv(t)
	uid, tok := env.seedUser()
	env.seedInstallation(uid, domain.StatusNeedsSetup)
	rec := env.do("POST", "/v1/setup", map[string]string{"parent_page_id": "parent-1"}, tok)
	if rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	body := decode[struct {
		Status     string  `json:"status"`
		LibraryURL *string `json:"library_url"`
	}](t, rec)
	if body.Status != "active" || body.LibraryURL == nil {
		t.Fatalf("body %+v", body)
	}
	if n := len(env.notion.Calls("POST", "/v1/databases")); n != 2 {
		t.Fatalf("%d database creates", n)
	}
	inst, _ := env.db.Store.GetInstallation(context.Background(), testBotID)
	if inst.Status != domain.StatusActive || len(inst.Props) != 16 || inst.SetupError != "" {
		t.Fatalf("installation %+v", inst)
	}
}

func TestPostSetupConflictWhenActive(t *testing.T) {
	env := newTestEnv(t)
	uid, tok := env.seedUser()
	env.seedInstallation(uid, domain.StatusActive)
	if rec := env.do("POST", "/v1/setup", map[string]string{"parent_page_id": "p"}, tok); rec.Code != 409 {
		t.Fatalf("%d", rec.Code)
	}
}

func TestPostSetupUnknownParentIsNotFound(t *testing.T) {
	env := newTestEnv(t)
	uid, tok := env.seedUser()
	env.seedInstallation(uid, domain.StatusNeedsSetup)
	env.notion.FailNext("POST", "/v1/databases", notiontest.Fail{Status: 404, Code: "object_not_found", Message: "no page"})
	if rec := env.do("POST", "/v1/setup", map[string]string{"parent_page_id": "nope"}, tok); rec.Code != 404 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
}

func TestPostSetupRequiresParent(t *testing.T) {
	env := newTestEnv(t)
	uid, tok := env.seedUser()
	env.seedInstallation(uid, domain.StatusNeedsSetup)
	if rec := env.do("POST", "/v1/setup", map[string]string{}, tok); rec.Code != 400 {
		t.Fatalf("%d", rec.Code)
	}
}

// An existing same-titled database with a broken schema is reused (idempotent setup), and its
// schema error must reach the user as an actionable 409, not an opaque 500.
func TestPostSetupBrokenExistingDatabaseIsConflict(t *testing.T) {
	env := newTestEnv(t)
	uid, tok := env.seedUser()
	env.seedInstallation(uid, domain.StatusNeedsSetup)
	broken := map[string]string{}
	for name, typ := range notiontest.LibrarySchema {
		if name != "Progress" {
			broken[name] = typ
		}
	}
	env.notion.AddDatabase("bad-lib-db", "bad-lib-ds", broken)
	env.notion.SetBlocks("parent-1", notiontest.ChildDatabase("bad-lib-db", notion.LibraryTitle))

	rec := env.do("POST", "/v1/setup", map[string]string{"parent_page_id": "parent-1"}, tok)
	if rec.Code != 409 || errorCode(t, rec) != "conflict" || !strings.Contains(rec.Body.String(), "property_missing:Progress") {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	inst, err := env.db.Store.GetInstallation(context.Background(), testBotID)
	must(t, err)
	if inst.Status != domain.StatusNeedsSetup {
		t.Fatalf("installation status %q", inst.Status)
	}
}

// I1 / scenario 10: the Library was deleted, so setup runs again; the resync re-sends every
// article and day, including ones already synced into the deleted database.
func TestPostSetupAfterDeletedLibraryResyncsEverything(t *testing.T) {
	env := newTestEnv(t)
	uid, tok := env.seedUser()
	inst := env.seedInstallation(uid, domain.StatusActive)
	env.seedSyncedRead()
	env.notion.DeleteDataSource(inst.LibraryDSID)
	must(t, env.db.Store.SetInstallationStatus(context.Background(), testBotID, domain.StatusNeedsSetup, "database_missing:Reading Library", env.now))

	rec := env.do("POST", "/v1/setup", map[string]string{"parent_page_id": "parent-2"}, tok)
	if rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if got := env.queue.Messages(); !reflect.DeepEqual(got, fullResync()) {
		t.Fatalf("resync enqueued %+v, want %+v", got, fullResync())
	}
}

// I3: when the backlog can't be enqueued even after retries, the installation goes back to
// needs_setup (resync_failed) instead of staying active with a stranded backlog; running
// setup again (idempotent) re-runs the resync.
func TestPostSetupResyncFailureNeedsSetupUntilRetried(t *testing.T) {
	env := newTestEnv(t)
	uid, tok := env.seedUser()
	env.seedInstallation(uid, domain.StatusNeedsSetup)
	env.seedSyncedRead()
	env.queue.Err = errors.New("sqs down")

	rec := env.do("POST", "/v1/setup", map[string]string{"parent_page_id": "parent-1"}, tok)
	type view struct {
		Status     string `json:"status"`
		SetupError string `json:"setup_error"`
	}
	if body := decode[view](t, rec); rec.Code != 200 || body.Status != "needs_setup" || body.SetupError != "resync_failed" {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	inst, err := env.db.Store.GetInstallation(context.Background(), testBotID)
	must(t, err)
	if inst.Status != domain.StatusNeedsSetup || inst.SetupError != "resync_failed" {
		t.Fatalf("installation %+v", inst)
	}
	if !strings.Contains(env.logs.String(), "level=ERROR") {
		t.Fatalf("no error logged:\n%s", env.logs)
	}
	if want := []time.Duration{100 * time.Millisecond, 200 * time.Millisecond}; !reflect.DeepEqual(env.sleeps, want) {
		t.Fatalf("back-offs %v, want %v (3 attempts)", env.sleeps, want)
	}

	env.queue.Err = nil
	rec = env.do("POST", "/v1/setup", map[string]string{"parent_page_id": "parent-1"}, tok)
	if body := decode[view](t, rec); rec.Code != 200 || body.Status != "active" || body.SetupError != "" {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if got := env.queue.Messages(); !reflect.DeepEqual(got, fullResync()) {
		t.Fatalf("resync enqueued %+v, want %+v", got, fullResync())
	}
	if n := len(env.notion.Calls("POST", "/v1/databases")); n != 2 {
		t.Fatalf("%d database creates across both attempts, want 2", n)
	}
}
