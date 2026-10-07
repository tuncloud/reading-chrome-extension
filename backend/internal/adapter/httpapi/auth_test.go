package httpapi_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"reading-tracker/backend/internal/domain"
	"reading-tracker/backend/internal/platform/crypto"
)

type tokenResp struct {
	DeviceToken string `json:"device_token"`
	DeviceID    string `json:"device_id"`
	User        struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"user"`
	Installation struct {
		ID         string  `json:"id"`
		Status     string  `json:"status"`
		LibraryURL *string `json:"library_url"`
	} `json:"installation"`
}

func redeem(env *testEnv, code, verifier string) *tokenResp {
	rec := env.do("POST", "/v1/auth/token", map[string]string{"code": code, "code_verifier": verifier, "device_name": "MacBook Chrome"}, "")
	if rec.Code != 200 {
		return nil
	}
	r := decode[tokenResp](env.t, rec)
	return &r
}

func TestAuthTokenHappyPath(t *testing.T) {
	env := newTestEnv(t)
	env.notion.AddTemplate("tpl-1")
	code := env.connect(t, tokenResponse("tpl-1"))

	r := redeem(env, code, testVerifier)
	if r == nil || !crypto.ValidDeviceTokenFormat(r.DeviceToken) || r.DeviceID == "" || r.User.Name != "Tuan Do" ||
		r.Installation.ID != testBotID || r.Installation.Status != "active" || r.Installation.LibraryURL == nil {
		t.Fatalf("response %+v", r)
	}
	// T14 AC: token stored only as HMAC.
	ref, err := env.db.Store.GetDeviceToken(context.Background(), crypto.TokenHMAC(env.cfg.DeviceTokenPepper, r.DeviceToken))
	must(t, err)
	if ref.DeviceID != r.DeviceID || ref.UserID != r.User.ID {
		t.Fatalf("token ref %+v", ref)
	}
	for _, item := range env.db.Scan(t) {
		for _, v := range item {
			if strings.Contains(fmtAV(v), r.DeviceToken) {
				t.Fatal("plaintext device token stored")
			}
		}
	}
	if rec := env.do("GET", "/v1/me", nil, r.DeviceToken); rec.Code != 200 {
		t.Fatalf("new token rejected: %d", rec.Code)
	}
}

// T14 AC / scenario 17: wrong verifier → invalid_grant, and the code is burned.
func TestAuthTokenWrongVerifier(t *testing.T) {
	env := newTestEnv(t)
	code := env.connect(t, tokenResponse(nil))
	rec := env.do("POST", "/v1/auth/token", map[string]string{"code": code, "code_verifier": strings.Repeat("w", 64)}, "")
	if rec.Code != 400 || errorCode(t, rec) != "invalid_grant" {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if redeem(env, code, testVerifier) != nil {
		t.Fatal("code must be single-use even after a failed verifier")
	}
}

// T14 AC: code usable once.
func TestAuthTokenCodeUsableOnce(t *testing.T) {
	env := newTestEnv(t)
	code := env.connect(t, tokenResponse(nil))
	if redeem(env, code, testVerifier) == nil {
		t.Fatal("first redeem failed")
	}
	rec := env.do("POST", "/v1/auth/token", map[string]string{"code": code, "code_verifier": testVerifier}, "")
	if rec.Code != 400 || errorCode(t, rec) != "invalid_grant" {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
}

// Scenario 18 at the API level.
func TestAuthTokenExpiredCode(t *testing.T) {
	env := newTestEnv(t)
	code := env.connect(t, tokenResponse(nil))
	env.now = env.now.Add(3 * time.Minute)
	if rec := env.do("POST", "/v1/auth/token", map[string]string{"code": code, "code_verifier": testVerifier}, ""); rec.Code != 400 {
		t.Fatalf("%d", rec.Code)
	}
}

func TestAuthTokenDefaultDeviceName(t *testing.T) {
	env := newTestEnv(t)
	code := env.connect(t, tokenResponse(nil))
	rec := env.do("POST", "/v1/auth/token", map[string]string{"code": code, "code_verifier": testVerifier, "device_name": "   "}, "")
	r := decode[tokenResp](t, rec)
	dev, err := env.db.Store.GetDevice(context.Background(), r.User.ID, r.DeviceID)
	must(t, err)
	if dev.Name != "Unknown device" || dev.RevokedAt != "" || dev.LastSeenAt != domain.Timestamp(env.now) {
		t.Fatalf("device %+v", dev)
	}
}

func TestAuthTokenMalformedBody(t *testing.T) {
	env := newTestEnv(t)
	if rec := env.do("POST", "/v1/auth/token", "{not json", ""); rec.Code != 400 || errorCode(t, rec) != "invalid_request" {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
}

// Oversized or empty codes never reach DynamoDB (keys are ≤ 2 KB): plain invalid_grant.
func TestAuthTokenOversizedOrEmptyCodeIsInvalidGrant(t *testing.T) {
	env := newTestEnv(t)
	for _, code := range []string{strings.Repeat("c", 3000), strings.Repeat("c", 129), ""} {
		rec := env.do("POST", "/v1/auth/token", map[string]string{"code": code, "code_verifier": testVerifier}, "")
		if rec.Code != 400 || errorCode(t, rec) != "invalid_grant" {
			t.Errorf("code of %d chars: %d %s", len(code), rec.Code, rec.Body)
		}
	}
}
