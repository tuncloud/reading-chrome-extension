package httpapi_test

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"testing"
	"time"

	"reading-tracker/backend/internal/crypto"
	"reading-tracker/backend/internal/store"
)

// fmtAV renders an attribute value (e.g. &{secret {}}) so scans can look for plaintext.
func fmtAV(v any) string { return fmt.Sprintf("%v", v) }

func startURL(ext string) string {
	q := url.Values{"ext_redirect": {ext}, "code_challenge": {crypto.S256Challenge(testVerifier)}, "code_challenge_method": {"S256"}}
	return "/v1/oauth/notion/start?" + q.Encode()
}

func extRedirect() string { return "https://" + testExtID + ".chromiumapp.org/" }

func tokenResponse(templateID any) map[string]any {
	return map[string]any{
		"access_token": "secret-access-token", "refresh_token": "secret-refresh-token", "token_type": "bearer",
		"bot_id": testBotID, "workspace_id": "ws-1", "workspace_name": "Tuan's Notion", "workspace_icon": nil,
		"owner":                  map[string]any{"type": "user", "user": map[string]any{"id": "nuser-1", "name": "Tuan Do", "person": map[string]any{"email": "tuan@example.com"}}},
		"duplicated_template_id": templateID,
	}
}

// start runs /start and returns the state Notion would echo back.
func (env *testEnv) start(t *testing.T) string {
	t.Helper()
	rec := env.do("GET", startURL(extRedirect()), nil, "")
	if rec.Code != 302 {
		t.Fatalf("start: %d %s", rec.Code, rec.Body)
	}
	loc, _ := url.Parse(rec.Header().Get("Location"))
	return loc.Query().Get("state")
}

// connect runs start + callback with the given Notion token response and returns the one-time code.
func (env *testEnv) connect(t *testing.T, tokenResp map[string]any) string {
	t.Helper()
	env.notion.SetTokenResponse(tokenResp)
	state := env.start(t)
	rec := env.do("GET", "/v1/oauth/notion/callback?code=notion-code&state="+state, nil, "")
	if rec.Code != 302 {
		t.Fatalf("callback: %d %s", rec.Code, rec.Body)
	}
	loc, _ := url.Parse(rec.Header().Get("Location"))
	code := loc.Query().Get("code")
	if code == "" {
		t.Fatalf("no code in %s", loc)
	}
	return code
}

// T13 AC: unknown extension id → 400.
func TestOAuthStartRejectsUnknownExtension(t *testing.T) {
	env := newTestEnv(t)
	for _, ext := range []string{
		"https://" + strings.Repeat("z", 32) + ".chromiumapp.org/",
		"http://" + testExtID + ".chromiumapp.org/",
		"https://" + testExtID + ".chromiumapp.org.evil.com/",
		"https://evil.com/?x=" + testExtID + ".chromiumapp.org",
	} {
		if rec := env.do("GET", startURL(ext), nil, ""); rec.Code != 400 || errorCode(t, rec) != "invalid_request" {
			t.Errorf("%s: %d %s", ext, rec.Code, rec.Body)
		}
	}
}

func TestOAuthStartRequiresS256Challenge(t *testing.T) {
	env := newTestEnv(t)
	q := url.Values{"ext_redirect": {extRedirect()}, "code_challenge": {"short"}, "code_challenge_method": {"plain"}}
	if rec := env.do("GET", "/v1/oauth/notion/start?"+q.Encode(), nil, ""); rec.Code != 400 {
		t.Fatalf("%d", rec.Code)
	}
}

func TestOAuthStartRedirectsToNotion(t *testing.T) {
	env := newTestEnv(t)
	rec := env.do("GET", startURL(extRedirect()), nil, "")
	loc, _ := url.Parse(rec.Header().Get("Location"))
	q := loc.Query()
	if rec.Code != 302 || loc.Path != "/v1/oauth/authorize" || q.Get("client_id") != "cid" || q.Get("owner") != "user" ||
		q.Get("response_type") != "code" || q.Get("redirect_uri") != "http://example.com/v1/oauth/notion/callback" || len(q.Get("state")) != 43 {
		t.Fatalf("%d %s", rec.Code, loc)
	}
}

// T13 AC: happy path → 302 to the extension with a code; INST stored with encrypted tokens only.
func TestOAuthCallbackHappyPathWithTemplate(t *testing.T) {
	env := newTestEnv(t)
	tpl := env.notion.AddTemplate("tpl-1")
	code := env.connect(t, tokenResponse("tpl-1"))
	if len(code) != 43 {
		t.Fatalf("code %q", code)
	}
	inst, err := env.db.Store.GetInstallation(context.Background(), testBotID)
	must(t, err)
	if inst.Status != store.StatusActive || inst.LibraryDSID != tpl.LibraryDS || inst.DuplicatedTemplateID != "tpl-1" || inst.TokenVersion != 1 {
		t.Fatalf("installation %+v", inst)
	}
	for _, item := range env.db.Scan(t) {
		for k, v := range item {
			if s := fmtAV(v); strings.Contains(s, "secret-access-token") || strings.Contains(s, "secret-refresh-token") {
				t.Fatalf("plaintext token stored in %s", k)
			}
		}
	}
	plain, err := env.cfg.Keys.Open(inst.AccessTokenEnc, []byte(testBotID))
	if err != nil || string(plain) != "secret-access-token" {
		t.Fatalf("access token does not decrypt: %v", err)
	}
	ex := env.notion.Calls("POST", "/v1/oauth/token")
	if len(ex) != 1 || ex[0].Body["redirect_uri"] != "http://example.com/v1/oauth/notion/callback" || ex[0].Body["code"] != "notion-code" {
		t.Fatalf("token exchange %+v", ex)
	}
	uid, err := env.db.Store.UserIDForNotionUser(context.Background(), "nuser-1")
	must(t, err)
	prof, _ := env.db.Store.GetProfile(context.Background(), uid)
	if prof.ActiveInstallationID != testBotID || prof.Email != "tuan@example.com" {
		t.Fatalf("profile %+v", prof)
	}
}

func TestOAuthCallbackWithoutTemplateNeedsSetup(t *testing.T) {
	env := newTestEnv(t)
	env.connect(t, tokenResponse(nil))
	inst, err := env.db.Store.GetInstallation(context.Background(), testBotID)
	must(t, err)
	if inst.Status != store.StatusNeedsSetup || inst.SetupError != "" {
		t.Fatalf("installation %+v", inst)
	}
}

func TestOAuthCallbackBrokenTemplateRecordsReason(t *testing.T) {
	env := newTestEnv(t)
	tpl := env.notion.AddTemplate("tpl-1")
	env.notion.RemoveProperty(tpl.LibraryDS, "Progress")
	env.connect(t, tokenResponse("tpl-1"))
	inst, _ := env.db.Store.GetInstallation(context.Background(), testBotID)
	if inst.Status != store.StatusNeedsSetup || inst.SetupError != "property_missing:Progress" {
		t.Fatalf("installation %+v", inst)
	}
}

// T13 AC / scenario 17: a replayed state → 400.
func TestOAuthCallbackReusedStateRejected(t *testing.T) {
	env := newTestEnv(t)
	env.notion.SetTokenResponse(tokenResponse(nil))
	state := env.start(t)
	if rec := env.do("GET", "/v1/oauth/notion/callback?code=c&state="+state, nil, ""); rec.Code != 302 {
		t.Fatalf("first callback %d", rec.Code)
	}
	rec := env.do("GET", "/v1/oauth/notion/callback?code=c&state="+state, nil, "")
	if rec.Code != 400 || !strings.Contains(rec.Body.String(), "Link expired") {
		t.Fatalf("replay: %d %s", rec.Code, rec.Body)
	}
}

func TestOAuthCallbackExpiredStateRejected(t *testing.T) {
	env := newTestEnv(t)
	state := env.start(t)
	env.now = env.now.Add(11 * time.Minute)
	if rec := env.do("GET", "/v1/oauth/notion/callback?code=c&state="+state, nil, ""); rec.Code != 400 {
		t.Fatalf("%d", rec.Code)
	}
}

func TestOAuthCallbackAccessDenied(t *testing.T) {
	env := newTestEnv(t)
	state := env.start(t)
	rec := env.do("GET", "/v1/oauth/notion/callback?error=access_denied&state="+state, nil, "")
	if rec.Code != 302 || rec.Header().Get("Location") != extRedirect()+"?error=access_denied" {
		t.Fatalf("%d %s", rec.Code, rec.Header().Get("Location"))
	}
	if _, err := env.db.Store.GetInstallation(context.Background(), testBotID); err == nil {
		t.Fatal("denied flow must store nothing")
	}
}

// Review focus 5 / scenario 11: reconnect after revoke, user skips template → reuse DBs, active, resync.
func TestOAuthReconnectWithoutTemplateKeepsDatabases(t *testing.T) {
	env := newTestEnv(t)
	tpl := env.notion.AddTemplate("tpl-1")
	env.connect(t, tokenResponse("tpl-1"))
	ctx := context.Background()
	must(t, env.db.Store.SetInstallationStatus(ctx, testBotID, store.StatusRevoked, "", env.now))
	_, err := env.db.Store.PushSegment(ctx, store.SegmentWrite{InstallationID: testBotID, DeviceID: "d", SegmentID: "00000000-0000-4000-8000-000000000001",
		SessionID: "s", URLHash: store.URLHash("https://a.example/x"), Date: "2026-10-06", StartedAt: env.now, EndedAt: env.now.Add(time.Minute),
		ActiveSeconds: 60, Article: store.ArticleMeta{CanonicalURL: "https://a.example/x", Source: "generic"}})
	must(t, err)
	env.queue.Drain()

	env.connect(t, tokenResponse(nil))
	inst, _ := env.db.Store.GetInstallation(ctx, testBotID)
	if inst.Status != store.StatusActive || inst.LibraryDSID != tpl.LibraryDS || inst.TokenVersion != 2 {
		t.Fatalf("installation %+v", inst)
	}
	if msgs := env.queue.Messages(); len(msgs) != 2 {
		t.Fatalf("resync enqueued %+v", msgs)
	}
}
