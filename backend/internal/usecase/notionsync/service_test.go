package notionsync

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb"

	"reading-tracker/backend/internal/adapter/dynamo"
	"reading-tracker/backend/internal/domain"
	"reading-tracker/backend/internal/platform/crypto"
	"reading-tracker/backend/internal/usecase/port"
)

func TestRateLimitBacksOffRetryAfterPlusJitter(t *testing.T) {
	e := newEnv(t)
	err := e.s.classify(ctx, &port.NotionError{Kind: port.NotionRateLimited, Status: 429, RetryAfter: 5 * time.Second})
	var ra *port.RetryAfterError
	if !errors.As(err, &ra) || ra.Delay != 7*time.Second {
		t.Fatalf("err %v", err)
	}
}

func TestOverloadWithoutRetryAfterUsesDefault(t *testing.T) {
	e := newEnv(t)
	var ra *port.RetryAfterError
	if err := e.s.classify(ctx, &port.NotionError{Kind: port.NotionRateLimited, Status: 529}); !errors.As(err, &ra) || ra.Delay != defaultBackoff+2*time.Second {
		t.Fatalf("err %v", err)
	}
}

func TestRetryableKindsFailWithoutBackoff(t *testing.T) {
	e := newEnv(t)
	for _, k := range []port.NotionErrorKind{port.NotionTransient, port.NotionConflict, port.NotionUnauthorized} {
		err := e.s.classify(ctx, &port.NotionError{Kind: k})
		var ra *port.RetryAfterError
		if err == nil || errors.As(err, &ra) {
			t.Errorf("%s: err %v", k, err)
		}
	}
	if err := e.s.classify(ctx, errors.New("dynamodb: connection reset")); err == nil {
		t.Error("storage errors must be retried")
	}
}

func TestPoisonKindsAreAckedAndLogged(t *testing.T) {
	e := newEnv(t)
	for _, k := range []port.NotionErrorKind{port.NotionRejected, port.NotionNotFound} {
		if err := e.s.classify(ctx, &port.NotionError{Kind: k, Status: 400, Code: "validation_error", Message: "body failed validation"}); err != nil {
			t.Errorf("%s: err %v", k, err)
		}
	}
	if !strings.Contains(e.logs.String(), "validation_error") {
		t.Fatal("poison message not logged")
	}
}

func TestUnknownJobTypeIsAcked(t *testing.T) {
	e := newEnv(t)
	if err := e.s.HandleJob(ctx, port.Job{Type: "mystery"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(e.logs.String(), "unknown job type dropped") {
		t.Fatalf("drop not logged: %s", e.logs)
	}
}

func searchCall(e *wenv) func(string) error {
	return func(tok string) error {
		_, err := e.gw.SearchPages(ctx, tok, "")
		return err
	}
}

// T21 AC / C7: refresh stores both new tokens and bumps token_version.
func TestSessionRefreshPersistsBothTokens(t *testing.T) {
	e := newEnv(t)
	inst := e.seedInstallation("old-access", "old-refresh")
	e.ns.RejectToken("old-access")
	e.ns.SetRefreshResponse(map[string]any{"access_token": "new-access", "refresh_token": "new-refresh", "bot_id": testBot})

	s, err := e.s.openSession(inst)
	must(t, err)
	must(t, s.call(ctx, searchCall(e)))

	got, _ := e.db.Store.GetInstallation(ctx, testBot)
	access, _ := e.keys.Open(got.AccessTokenEnc, []byte(testBot))
	refresh, _ := e.keys.Open(got.RefreshTokenEnc, []byte(testBot))
	if got.TokenVersion != 2 || string(access) != "new-access" || string(refresh) != "new-refresh" {
		t.Fatalf("installation %+v", got)
	}
	refreshes := e.ns.Calls("POST", "/v1/oauth/token")
	if len(refreshes) != 1 || refreshes[0].Body["refresh_token"] != "old-refresh" {
		t.Fatalf("refresh calls %+v", refreshes)
	}
}

// T21 AC / scenario 11: refresh invalid_grant → revoked, message acked.
func TestSessionRefreshInvalidGrantRevokes(t *testing.T) {
	e := newEnv(t)
	inst := e.seedInstallation("old-access", "old-refresh")
	e.ns.RejectToken("old-access") // no refresh response configured → 400 invalid_grant
	s, _ := e.s.openSession(inst)
	if err := s.call(ctx, searchCall(e)); !errors.Is(err, errAck) {
		t.Fatalf("err %v", err)
	}
	if got, _ := e.db.Store.GetInstallation(ctx, testBot); got.Status != domain.StatusRevoked {
		t.Fatalf("status %s", got.Status)
	}
}

// Integration removed from the workspace: even a fresh token is rejected.
func TestSessionStillUnauthorizedAfterRefreshRevokes(t *testing.T) {
	e := newEnv(t)
	inst := e.seedInstallation("old-access", "old-refresh")
	e.ns.RejectToken("old-access")
	e.ns.RejectToken("new-access")
	e.ns.SetRefreshResponse(map[string]any{"access_token": "new-access", "refresh_token": "new-refresh"})
	s, _ := e.s.openSession(inst)
	if err := s.call(ctx, searchCall(e)); !errors.Is(err, errAck) {
		t.Fatalf("err %v", err)
	}
	if got, _ := e.db.Store.GetInstallation(ctx, testBot); got.Status != domain.StatusRevoked {
		t.Fatalf("status %s", got.Status)
	}
}

// Scenario 19: tokens sealed with v1 still open after v2 is prepended, and a refresh re-seals with v2.
func TestRefreshReencryptsWithCurrentKey(t *testing.T) {
	e := newEnvWithKeys(t, "v2:"+b64(2)+",v1:"+b64(1))
	old, err := crypto.ParseKeyring("v1:" + b64(1))
	must(t, err)
	inst := e.seedInstallationWith(old, "old-access", "old-refresh")
	e.ns.RejectToken("old-access")
	e.ns.SetRefreshResponse(map[string]any{"access_token": "new-access", "refresh_token": "new-refresh"})
	s, err := e.s.openSession(inst)
	must(t, err)
	must(t, s.call(ctx, searchCall(e)))
	got, _ := e.db.Store.GetInstallation(ctx, testBot)
	if !strings.HasPrefix(got.AccessTokenEnc, "v2:") || !strings.HasPrefix(got.RefreshTokenEnc, "v2:") {
		t.Fatalf("not re-sealed with v2: %s / %s", got.AccessTokenEnc[:3], got.RefreshTokenEnc[:3])
	}
}

// tokenWriteFailDB fails the first n token-rotation writes with a transient error.
type tokenWriteFailDB struct {
	dynamo.API
	n int
}

func (d *tokenWriteFailDB) UpdateItem(ctx context.Context, in *dynamodb.UpdateItemInput, opts ...func(*dynamodb.Options)) (*dynamodb.UpdateItemOutput, error) {
	if d.n > 0 && in.ExpressionAttributeNames["#a"] == "access_token_enc" {
		d.n--
		return nil, errors.New("dynamodb: connection reset")
	}
	return d.API.UpdateItem(ctx, in, opts...)
}

// C7: Notion has already rotated the refresh token, so a transient failure persisting
// it is retried in-process instead of losing the only valid refresh token.
func TestSessionRefreshRetriesTokenWrite(t *testing.T) {
	e := newEnv(t)
	inst := e.seedInstallation("old-access", "old-refresh")
	e.s.Installations = dynamo.New(&tokenWriteFailDB{API: e.db.DB, n: 2}, e.db.Table)
	e.ns.RejectToken("old-access")
	e.ns.SetRefreshResponse(map[string]any{"access_token": "new-access", "refresh_token": "new-refresh"})

	s, err := e.s.openSession(inst)
	must(t, err)
	must(t, s.call(ctx, searchCall(e)))
	got, _ := e.db.Store.GetInstallation(ctx, testBot)
	refresh, _ := e.keys.Open(got.RefreshTokenEnc, []byte(testBot))
	if got.TokenVersion != 2 || string(refresh) != "new-refresh" {
		t.Fatalf("rotated tokens not persisted: version %d", got.TokenVersion)
	}
	if want := []time.Duration{100 * time.Millisecond, 200 * time.Millisecond}; !reflect.DeepEqual(e.sleeps, want) {
		t.Fatalf("back-offs %v, want %v", e.sleeps, want)
	}
}

// After three failed writes the error surfaces (message retried later) and is logged.
func TestSessionRefreshTokenWriteGivesUpAfterThreeAttempts(t *testing.T) {
	e := newEnv(t)
	inst := e.seedInstallation("old-access", "old-refresh")
	e.s.Installations = dynamo.New(&tokenWriteFailDB{API: e.db.DB, n: 3}, e.db.Table)
	e.ns.RejectToken("old-access")
	e.ns.SetRefreshResponse(map[string]any{"access_token": "new-access", "refresh_token": "new-refresh"})

	s, err := e.s.openSession(inst)
	must(t, err)
	if err := s.call(ctx, searchCall(e)); err == nil || errors.Is(err, errAck) {
		t.Fatalf("err %v", err)
	}
	if got, _ := e.db.Store.GetInstallation(ctx, testBot); got.TokenVersion != 1 {
		t.Fatalf("token_version %d", got.TokenVersion)
	}
	if !strings.Contains(e.logs.String(), "level=ERROR") || strings.Contains(e.logs.String(), "new-refresh") {
		t.Fatalf("logs:\n%s", e.logs)
	}
}
