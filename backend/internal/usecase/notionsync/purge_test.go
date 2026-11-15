package notionsync

import (
	"testing"

	"reading-tracker/backend/internal/adapter/queue"
	"reading-tracker/backend/internal/domain"
)

// seedUser creates user u1 (Notion user n1) with device d1 and the test installation.
func (e *wenv) seedUser(uid string) {
	e.t.Helper()
	s := e.db.Store
	must(e.t, s.CreateUser(ctx, uid, domain.User{NotionUserID: "n-" + uid, Name: "T", CreatedAt: domain.Timestamp(e.now)}))
	must(e.t, s.CreateDevice(ctx, uid, "d1", domain.Device{Name: "Mac", TokenHMAC: "h-" + uid, CreatedAt: domain.Timestamp(e.now), LastSeenAt: domain.Timestamp(e.now)}))
	must(e.t, s.PutUserInstallation(ctx, uid, testBot, e.now))
}

// T23 AC / scenario 22: every item of the user is gone; Notion untouched.
func TestPurgeRemovesAllUserData(t *testing.T) {
	e := newEnv(t)
	e.seedInstallation("tok", "rt") // META.user_id = u1
	e.seedUser("u1")
	e.read(artURL, 0, 60, 0.5)
	must(t, e.db.Store.MarkProfileDeleted(ctx, "u1", e.now))

	if f := e.handle(queue.Purge("u1", testBot)); len(f) != 0 {
		t.Fatalf("failures %+v", f)
	}
	for _, c := range []struct {
		table, where string
		arg          any
	}{
		{"users", "id = $1", "u1"},
		{"user_installations", "user_id = $1", "u1"},
		{"devices", "user_id = $1", "u1"},
		{"device_tokens", "token_hmac = $1", "h-u1"},
		{"notion_user_links", "notion_user_id = $1", "n-u1"},
		{"installations", "id = $1", testBot},
		{"articles", "installation_id = $1", testBot},
		{"segments", "installation_id = $1", testBot},
		{"days", "installation_id = $1", testBot},
	} {
		if n := e.db.Count(t, c.table, c.where, c.arg); n != 0 {
			t.Errorf("%s still has %d rows", c.table, n)
		}
	}
	if n := len(e.ns.Requests()); n != 0 {
		t.Fatalf("purge called Notion %d times", n)
	}
}

func TestPurgeIgnoresLiveUser(t *testing.T) {
	e := newEnv(t)
	e.seedInstallation("tok", "rt")
	e.seedUser("u1")
	e.handle(queue.Purge("u1", testBot))
	if e.db.Count(t, "users", "id = $1", "u1") != 1 || e.db.Count(t, "installations", "id = $1", testBot) != 1 {
		t.Fatal("purged a user that is not marked deleted")
	}
}

// Interpretation 5: an installation now owned by another user is not purged.
func TestPurgeSkipsInstallationOwnedByAnotherUser(t *testing.T) {
	e := newEnv(t)
	e.seedInstallation("tok", "rt") // owned by u1
	e.seedUser("u0")
	must(t, e.db.Store.MarkProfileDeleted(ctx, "u0", e.now))
	e.handle(queue.Purge("u0", testBot))
	if e.db.Count(t, "installations", "id = $1", testBot) != 1 {
		t.Fatal("deleted another user's installation")
	}
	if e.db.Count(t, "users", "id = $1", "u0") != 0 {
		t.Fatal("u0 not purged")
	}
}
