package worker

import (
	"testing"

	"reading-tracker/backend/internal/queue"
	"reading-tracker/backend/internal/store"
)

// seedUser creates user u1 (Notion user n1) with device d1 and the test installation.
func (e *wenv) seedUser(uid string) {
	e.t.Helper()
	s := e.db.Store
	must(e.t, s.CreateUser(ctx, uid, store.Profile{NotionUserID: "n-" + uid, Name: "T", CreatedAt: store.Timestamp(e.now)}))
	must(e.t, s.CreateDevice(ctx, uid, "d1", store.Device{Name: "Mac", TokenHMAC: "h-" + uid, CreatedAt: store.Timestamp(e.now), LastSeenAt: store.Timestamp(e.now)}))
	must(e.t, s.PutUserInstallation(ctx, uid, testBot, e.now))
}

func (e *wenv) remaining(pk string) int {
	keys, err := e.db.Store.PartitionKeys(ctx, pk)
	must(e.t, err)
	return len(keys)
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
	for _, pk := range []string{store.UserPK("u1"), store.InstPK(testBot), store.NUserPK("n-u1"), store.DevTokenPK("h-u1")} {
		if n := e.remaining(pk); n != 0 {
			t.Errorf("%s still has %d items", pk, n)
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
	if e.remaining(store.UserPK("u1")) == 0 || e.remaining(store.InstPK(testBot)) == 0 {
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
	if e.remaining(store.InstPK(testBot)) == 0 {
		t.Fatal("deleted another user's installation")
	}
	if e.remaining(store.UserPK("u0")) != 0 {
		t.Fatal("u0 not purged")
	}
}
