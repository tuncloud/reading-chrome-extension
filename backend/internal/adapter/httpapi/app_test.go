package httpapi_test

import (
	"context"
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"reading-tracker/backend/internal/adapter/dynamo"
	"reading-tracker/backend/internal/adapter/notion"
	"reading-tracker/backend/internal/domain"
	"reading-tracker/backend/internal/platform/config"
	"reading-tracker/backend/internal/platform/crypto"
	"reading-tracker/backend/internal/wire"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

func TestConfigEndpoint(t *testing.T) {
	env := newTestEnv(t)
	rec := env.do("GET", "/v1/config", nil, "")
	if rec.Code != 200 || rec.Header().Get("Cache-Control") != "max-age=3600" {
		t.Fatalf("%d %v", rec.Code, rec.Header())
	}
	body := decode[config.PublicConfig](t, rec)
	if body.Version != 1 || body.Tracking.IdleSeconds != 60 || len(body.StripQueryParams) == 0 {
		t.Fatalf("body %+v", body)
	}
}

// T12 AC: unauthenticated GET /v1/me → 401 with the §2.0 error shape.
func TestMeRequiresDeviceToken(t *testing.T) {
	env := newTestEnv(t)
	for _, tok := range []string{"", "not-a-token", "rt_" + strings.Repeat("A", 43)} {
		rec := env.do("GET", "/v1/me", nil, tok)
		if rec.Code != 401 || errorCode(t, rec) != "unauthorized" {
			t.Fatalf("token %q: %d %s", tok, rec.Code, rec.Body)
		}
	}
}

// §2.4: a revoked device is rejected even if its DEVTOKEN lookup still exists (the
// RevokeDevice transaction deletes both, so this isolates the revoked_at check).
func TestMeRejectsRevokedDeviceWithTokenLookupIntact(t *testing.T) {
	env := newTestEnv(t)
	uid, tok := env.seedUser()
	if rec := env.do("GET", "/v1/me", nil, tok); rec.Code != 200 {
		t.Fatalf("before revoke: %d %s", rec.Code, rec.Body)
	}
	env.setDeviceAttr(uid, "dev-1", "revoked_at", domain.Timestamp(env.now))
	rec := env.do("GET", "/v1/me", nil, tok)
	if rec.Code != 401 || errorCode(t, rec) != "unauthorized" {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
}

func TestMeRejectsRevokedDeviceAfterRevokeDevice(t *testing.T) {
	env := newTestEnv(t)
	uid, tok := env.seedUser()
	must(t, env.db.Store.RevokeDevice(t.Context(), uid, "dev-1", crypto.TokenHMAC(env.cfg.DeviceTokenPepper, tok), env.now))
	rec := env.do("GET", "/v1/me", nil, tok)
	if rec.Code != 401 || errorCode(t, rec) != "unauthorized" {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
}

func TestMeRejectsDeletedProfile(t *testing.T) {
	env := newTestEnv(t)
	uid, tok := env.seedUser()
	must(t, env.db.Store.MarkProfileDeleted(t.Context(), uid, env.now))
	rec := env.do("GET", "/v1/me", nil, tok)
	if rec.Code != 401 || errorCode(t, rec) != "unauthorized" {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
}

// §2.4 step 3: last_seen_at is refreshed at most hourly.
func TestAuthTouchesLastSeenOnlyWhenOverAnHourOld(t *testing.T) {
	env := newTestEnv(t)
	uid, tok := env.seedUser()

	stale := domain.Timestamp(env.now.Add(-2 * time.Hour))
	env.setDeviceAttr(uid, "dev-1", "last_seen_at", stale)
	if rec := env.do("GET", "/v1/me", nil, tok); rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if got := env.lastSeen(uid, "dev-1"); got != domain.Timestamp(env.now) {
		t.Fatalf("2h-old last_seen_at not refreshed: %s", got)
	}

	recent := domain.Timestamp(env.now.Add(-30 * time.Minute))
	env.setDeviceAttr(uid, "dev-1", "last_seen_at", recent)
	if rec := env.do("GET", "/v1/me", nil, tok); rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if got := env.lastSeen(uid, "dev-1"); got != recent {
		t.Fatalf("30m-old last_seen_at must be left alone: %s", got)
	}
}

func TestUnknownRouteIsJSON404(t *testing.T) {
	env := newTestEnv(t)
	rec := env.do("GET", "/v1/nope", nil, "")
	if rec.Code != 404 || errorCode(t, rec) != "not_found" {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
}

type meResponse struct {
	User struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"user"`
	Device struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"device"`
	Installation *struct {
		ID            string  `json:"id"`
		WorkspaceName string  `json:"workspace_name"`
		Status        string  `json:"status"`
		SetupError    string  `json:"setup_error"`
		LibraryURL    *string `json:"library_url"`
	} `json:"installation"`
}

func TestMeReturnsUserDeviceAndInstallation(t *testing.T) {
	env := newTestEnv(t)
	uid, tok := env.seedUser()
	env.seedInstallation(uid, domain.StatusActive)
	rec := env.do("GET", "/v1/me", nil, tok)
	if rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	body := decode[meResponse](t, rec)
	if body.User.ID != uid || body.User.Name != "Tuan Do" || body.Device.ID != "dev-1" || body.Device.Name != "MacBook Chrome" {
		t.Fatalf("body %+v", body)
	}
	if body.Installation == nil || body.Installation.ID != testBotID || body.Installation.Status != "active" ||
		body.Installation.LibraryURL == nil || body.Installation.WorkspaceName != "Tuan's Notion" {
		t.Fatalf("installation %+v", body.Installation)
	}
}

func TestMeWithoutInstallationReturnsNull(t *testing.T) {
	env := newTestEnv(t)
	_, tok := env.seedUser()
	rec := env.do("GET", "/v1/me", nil, tok)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"installation":null`) {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
}

func TestNeedsSetupInstallationShowsErrorAndNullLibrary(t *testing.T) {
	env := newTestEnv(t)
	uid, tok := env.seedUser()
	inst := env.seedInstallation(uid, domain.StatusNeedsSetup)
	inst.SetupError = "template_not_found"
	must(t, env.db.Store.PutInstallation(t.Context(), testBotID, inst))
	rec := env.do("GET", "/v1/me", nil, tok)
	if !strings.Contains(rec.Body.String(), `"setup_error":"template_not_found"`) || !strings.Contains(rec.Body.String(), `"library_url":null`) {
		t.Fatalf("%s", rec.Body)
	}
}

// C13: the request log has the path but never the query string or bearer token.
func TestRequestLogOmitsQueryAndToken(t *testing.T) {
	env := newTestEnv(t)
	_, tok := env.seedUser()
	env.do("GET", "/v1/me?code=abc123secret", nil, tok)
	logs := env.logs.String()
	if !strings.Contains(logs, "/v1/me") || strings.Contains(logs, "abc123secret") || strings.Contains(logs, tok) {
		t.Fatalf("log leak: %s", logs)
	}
}

type throttledReadsDB struct{ dynamo.API }

func (throttledReadsDB) GetItem(context.Context, *dynamodb.GetItemInput, ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error) {
	return nil, &types.ProvisionedThroughputExceededException{Message: aws.String("slow down")}
}

// Review focus 2: a DynamoDB throttle on the API path (auth reads the device token
// first) is still 503 capacity_exceeded now that errors cross the use-case boundary.
func TestThrottledReadIs503(t *testing.T) {
	env := newTestEnv(t)
	_, tok := env.seedUser()
	e := wire.NewAPI(wire.APIDeps{Config: env.cfg, Store: dynamo.New(throttledReadsDB{env.db.DB}, env.db.Table),
		Notion: &notion.Client{BaseURL: env.notion.URL, Version: "2025-09-03"}, Queue: env.queue,
		Now: func() time.Time { return env.now }, Log: slog.New(slog.NewTextHandler(io.Discard, nil))})
	req := httptest.NewRequest("GET", "/v1/me", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != 503 || errorCode(t, rec) != "capacity_exceeded" {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
}
