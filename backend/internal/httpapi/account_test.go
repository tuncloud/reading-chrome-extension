package httpapi_test

import (
	"context"
	"errors"
	"testing"

	"reading-tracker/backend/internal/crypto"
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
	ctx := context.Background()
	uid, tok := env.seedUser()
	env.seedInstallation(uid, store.StatusActive)
	tok2, err := crypto.NewDeviceToken()
	must(t, err)
	must(t, env.db.Store.CreateDevice(ctx, uid, "dev-2", store.Device{Name: "Firefox", TokenHMAC: crypto.TokenHMAC(env.cfg.DeviceTokenPepper, tok2),
		CreatedAt: store.Timestamp(env.now), LastSeenAt: store.Timestamp(env.now)}))

	if rec := env.do("DELETE", "/v1/me", nil, tok); rec.Code != 202 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if rec := env.do("GET", "/v1/me", nil, tok); rec.Code != 401 {
		t.Fatalf("after delete %d", rec.Code)
	}
	// Every device is revoked, not just the caller's: the token lookups are gone and revoked_at is set.
	for id, token := range map[string]string{"dev-1": tok, "dev-2": tok2} {
		if _, err := env.db.Store.GetDeviceToken(ctx, crypto.TokenHMAC(env.cfg.DeviceTokenPepper, token)); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("%s token lookup survived: %v", id, err)
		}
		dev, err := env.db.Store.GetDevice(ctx, uid, id)
		if err != nil || dev.RevokedAt == "" {
			t.Errorf("%s not revoked: %+v %v", id, dev, err)
		}
	}
	msgs := env.queue.Messages()
	if len(msgs) == 0 || msgs[len(msgs)-1] != queue.Purge(uid, testBotID) {
		t.Fatalf("messages %+v", msgs)
	}
	prof, err := env.db.Store.GetProfile(ctx, uid)
	if err != nil || prof.DeletedAt == "" {
		t.Fatalf("profile %+v %v", prof, err)
	}
}

// If the purge cannot be enqueued nothing irreversible has happened: the caller can
// retry with the same token.
func TestDeleteMeLeavesAccountIntactWhenEnqueueFails(t *testing.T) {
	env := newTestEnv(t)
	uid, tok := env.seedUser()
	env.seedInstallation(uid, store.StatusActive)
	env.queue.Err = errors.New("sqs down")

	if rec := env.do("DELETE", "/v1/me", nil, tok); rec.Code != 500 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	prof, err := env.db.Store.GetProfile(context.Background(), uid)
	if err != nil || prof.DeletedAt != "" {
		t.Fatalf("profile %+v %v", prof, err)
	}
	if rec := env.do("GET", "/v1/me", nil, tok); rec.Code != 200 {
		t.Fatalf("token no longer works: %d %s", rec.Code, rec.Body)
	}
}
