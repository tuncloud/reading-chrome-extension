package httpapi_test

import (
	"context"
	"strings"
	"testing"

	"reading-tracker/backend/internal/notion/notiontest"
	"reading-tracker/backend/internal/setup"
	"reading-tracker/backend/internal/store"
)

func TestSetupPagesSearchesNotionWithDecryptedToken(t *testing.T) {
	env := newTestEnv(t)
	uid, tok := env.seedUser()
	env.seedInstallation(uid, store.StatusNeedsSetup)
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
	env.seedInstallation(uid, store.StatusNeedsSetup)
	env.notion.RejectToken("secret-access-token")
	if rec := env.do("GET", "/v1/setup/pages", nil, tok); rec.Code != 409 || errorCode(t, rec) != "conflict" {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
}

// T15 AC: POST /v1/setup creates both DBs and flips the installation to active.
func TestPostSetupCreatesDatabases(t *testing.T) {
	env := newTestEnv(t)
	uid, tok := env.seedUser()
	env.seedInstallation(uid, store.StatusNeedsSetup)
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
	if inst.Status != store.StatusActive || len(inst.Props) != 16 || inst.SetupError != "" {
		t.Fatalf("installation %+v", inst)
	}
}

func TestPostSetupConflictWhenActive(t *testing.T) {
	env := newTestEnv(t)
	uid, tok := env.seedUser()
	env.seedInstallation(uid, store.StatusActive)
	if rec := env.do("POST", "/v1/setup", map[string]string{"parent_page_id": "p"}, tok); rec.Code != 409 {
		t.Fatalf("%d", rec.Code)
	}
}

func TestPostSetupUnknownParentIsNotFound(t *testing.T) {
	env := newTestEnv(t)
	uid, tok := env.seedUser()
	env.seedInstallation(uid, store.StatusNeedsSetup)
	env.notion.FailNext("POST", "/v1/databases", notiontest.Fail{Status: 404, Code: "object_not_found", Message: "no page"})
	if rec := env.do("POST", "/v1/setup", map[string]string{"parent_page_id": "nope"}, tok); rec.Code != 404 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
}

func TestPostSetupRequiresParent(t *testing.T) {
	env := newTestEnv(t)
	uid, tok := env.seedUser()
	env.seedInstallation(uid, store.StatusNeedsSetup)
	if rec := env.do("POST", "/v1/setup", map[string]string{}, tok); rec.Code != 400 {
		t.Fatalf("%d", rec.Code)
	}
}

// An existing same-titled database with a broken schema is reused (idempotent setup), and its
// schema error must reach the user as an actionable 409, not an opaque 500.
func TestPostSetupBrokenExistingDatabaseIsConflict(t *testing.T) {
	env := newTestEnv(t)
	uid, tok := env.seedUser()
	env.seedInstallation(uid, store.StatusNeedsSetup)
	broken := map[string]string{}
	for name, typ := range notiontest.LibrarySchema {
		if name != "Progress" {
			broken[name] = typ
		}
	}
	env.notion.AddDatabase("bad-lib-db", "bad-lib-ds", broken)
	env.notion.SetBlocks("parent-1", notiontest.ChildDatabase("bad-lib-db", setup.LibraryTitle))

	rec := env.do("POST", "/v1/setup", map[string]string{"parent_page_id": "parent-1"}, tok)
	if rec.Code != 409 || errorCode(t, rec) != "conflict" || !strings.Contains(rec.Body.String(), "property_missing:Progress") {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	inst, err := env.db.Store.GetInstallation(context.Background(), testBotID)
	must(t, err)
	if inst.Status != store.StatusNeedsSetup {
		t.Fatalf("installation status %q", inst.Status)
	}
}
