package httpapi_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"reading-tracker/backend/internal/store"
)

func pushBody(env *testEnv) map[string]any {
	start := env.now.Add(-10 * time.Minute)
	return map[string]any{
		"client_time": env.now.Format(time.RFC3339), "tz": "Asia/Ho_Chi_Minh",
		"articles": []map[string]any{{
			"canonical_url": "https://medium.com/p/8f3c2a1b9d4e", "source": "medium", "title": "Designing Idempotent APIs",
			"author": "Jane Doe", "site_name": "Better Programming", "word_count": 1820, "published_at": "2026-09-30",
			"original_url": "https://freedium-mirror.cfd/https://medium.com/better-programming/designing-idempotent-apis-8f3c2a1b9d4e",
		}},
		"segments": []map[string]any{{
			"id": "6f1d2c9e-6a43-4b1e-9a0e-2f7c1d5b8e11", "session_id": "c3a9", "seq": 0,
			"canonical_url": "https://medium.com/p/8f3c2a1b9d4e", "started_at": start.Format(time.RFC3339),
			"ended_at": start.Add(65 * time.Second).Format(time.RFC3339), "active_seconds": 58, "max_progress": 0.42,
		}},
	}
}

type pushResp struct {
	Accepted  []string                      `json:"accepted"`
	Duplicate []string                      `json:"duplicate"`
	Retry     []string                      `json:"retry"`
	Rejected  []struct{ ID, Reason string } `json:"rejected"`
}

// Scenario 2 end-to-end through the API and DynamoDB Local.
func TestSyncPushTwiceIsDuplicate(t *testing.T) {
	env := newTestEnv(t)
	uid, tok := env.seedUser()
	env.seedInstallation(uid, store.StatusActive)

	first := env.do("POST", "/v1/sync/push", pushBody(env), tok)
	if first.Code != 200 {
		t.Fatalf("%d %s", first.Code, first.Body)
	}
	if r := decode[pushResp](t, first); len(r.Accepted) != 1 || r.Duplicate == nil || r.Retry == nil || r.Rejected == nil {
		t.Fatalf("first %s", first.Body)
	}
	second := decode[pushResp](t, env.do("POST", "/v1/sync/push", pushBody(env), tok))
	if len(second.Duplicate) != 1 || len(second.Accepted) != 0 {
		t.Fatalf("second %+v", second)
	}
	a, err := env.db.Store.GetArticle(context.Background(), testBotID, store.URLHash("https://medium.com/p/8f3c2a1b9d4e"))
	must(t, err)
	if a.TotalSeconds != 58 {
		t.Fatalf("total %d", a.TotalSeconds)
	}
}

func TestSyncPushNoInstallation(t *testing.T) {
	env := newTestEnv(t)
	_, tok := env.seedUser()
	if rec := env.do("POST", "/v1/sync/push", pushBody(env), tok); rec.Code != 409 || errorCode(t, rec) != "no_installation" {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
}

// Designated test for the 256 KB BodyLimit (Task 8 ruling): the body must exceed 262144 bytes.
func TestSyncPushBodyTooLarge(t *testing.T) {
	env := newTestEnv(t)
	uid, tok := env.seedUser()
	env.seedInstallation(uid, store.StatusActive)
	huge := `{"tz":"UTC","articles":[],"segments":[],"pad":"` + strings.Repeat("x", 300*1024) + `"}`
	if len(huge) <= 256*1024 {
		t.Fatalf("test body is %d bytes; it must exceed the 256 KB limit", len(huge))
	}
	if rec := env.do("POST", "/v1/sync/push", huge, tok); rec.Code != 413 || errorCode(t, rec) != "payload_too_large" {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
}

func TestSyncPushBadTZ(t *testing.T) {
	env := newTestEnv(t)
	uid, tok := env.seedUser()
	env.seedInstallation(uid, store.StatusActive)
	body := pushBody(env)
	body["tz"] = "Mars/Olympus"
	if rec := env.do("POST", "/v1/sync/push", body, tok); rec.Code != 400 || errorCode(t, rec) != "invalid_request" {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
}

// Scenario 10: pushes are accepted while the installation needs setup.
func TestSyncPushAcceptedWhileNeedsSetup(t *testing.T) {
	env := newTestEnv(t)
	uid, tok := env.seedUser()
	env.seedInstallation(uid, store.StatusNeedsSetup)
	if r := decode[pushResp](t, env.do("POST", "/v1/sync/push", pushBody(env), tok)); len(r.Accepted) != 1 {
		t.Fatalf("%+v", r)
	}
}
