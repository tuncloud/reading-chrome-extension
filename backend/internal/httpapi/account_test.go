package httpapi_test

import (
	"context"
	"testing"

	"reading-tracker/backend/internal/queue"
	"reading-tracker/backend/internal/store"
)

// T17 AC: logout, then any call with the same token → 401.
func TestLogoutRevokesToken(t *testing.T) {
	env := newTestEnv(t)
	_, tok := env.seedUser()
	if rec := env.do("POST", "/v1/auth/logout", nil, tok); rec.Code != 204 {
		t.Fatalf("logout %d %s", rec.Code, rec.Body)
	}
	if rec := env.do("GET", "/v1/me", nil, tok); rec.Code != 401 {
		t.Fatalf("after logout %d", rec.Code)
	}
}

func TestDeleteMeRevokesAllDevicesAndEnqueuesPurge(t *testing.T) {
	env := newTestEnv(t)
	uid, tok := env.seedUser()
	env.seedInstallation(uid, store.StatusActive)
	if rec := env.do("DELETE", "/v1/me", nil, tok); rec.Code != 202 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if rec := env.do("GET", "/v1/me", nil, tok); rec.Code != 401 {
		t.Fatalf("after delete %d", rec.Code)
	}
	msgs := env.queue.Messages()
	if len(msgs) == 0 || msgs[len(msgs)-1] != queue.Purge(uid, testBotID) {
		t.Fatalf("messages %+v", msgs)
	}
	prof, err := env.db.Store.GetProfile(context.Background(), uid)
	if err != nil || prof.DeletedAt == "" {
		t.Fatalf("profile %+v %v", prof, err)
	}
}
