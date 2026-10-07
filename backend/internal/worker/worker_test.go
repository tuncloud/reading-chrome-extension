package worker

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"

	"reading-tracker/backend/internal/adapter/dynamo"
	"reading-tracker/backend/internal/adapter/notion"
	"reading-tracker/backend/internal/domain"
	"reading-tracker/backend/internal/platform/crypto"
	"reading-tracker/backend/internal/queue"
)

// C21: after the first failure, it and every later message are reported failed.
func TestFirstFailureFailsRestOfBatch(t *testing.T) {
	e := newEnv(t)
	var ran []string
	e.w.handlers[queue.TypeArticle] = func(_ context.Context, m queue.Message) error {
		ran = append(ran, m.URLHash)
		if m.URLHash == "bad" {
			return errors.New("boom")
		}
		return nil
	}
	fails := e.handle(queue.Article(testBot, "ok"), queue.Article(testBot, "bad"), queue.Article(testBot, "later"))
	if len(fails) != 2 || fails[0].ItemIdentifier != "m1" || fails[1].ItemIdentifier != "m2" {
		t.Fatalf("failures %+v", fails)
	}
	if len(ran) != 2 {
		t.Fatalf("ran %v; messages after a failure must not run", ran)
	}
}

// T21 AC / scenario 12: 429 → visibility ≥ Retry-After (+ jitter), message failed.
func TestRateLimitChangesVisibility(t *testing.T) {
	e := newEnv(t)
	e.w.handlers[queue.TypeArticle] = func(context.Context, queue.Message) error {
		return &notion.APIError{Status: 429, Code: "rate_limited", RetryAfter: 5 * time.Second}
	}
	fails := e.handle(queue.Article(testBot, "h"))
	if len(fails) != 1 || len(e.vis.calls) != 1 || e.vis.calls[0] != 7*time.Second {
		t.Fatalf("fails %+v visibility %v", fails, e.vis.calls)
	}
}

func TestOverloadWithoutRetryAfterUsesDefault(t *testing.T) {
	e := newEnv(t)
	e.w.handlers[queue.TypeArticle] = func(context.Context, queue.Message) error {
		return &notion.APIError{Status: 529, Code: "service_overload"}
	}
	e.handle(queue.Article(testBot, "h"))
	if len(e.vis.calls) != 1 || e.vis.calls[0] != defaultBackoff+2*time.Second {
		t.Fatalf("visibility %v", e.vis.calls)
	}
}

func TestServerErrorIsRetriedWithoutVisibilityChange(t *testing.T) {
	e := newEnv(t)
	e.w.handlers[queue.TypeArticle] = func(context.Context, queue.Message) error { return &notion.APIError{Status: 502} }
	if fails := e.handle(queue.Article(testBot, "h")); len(fails) != 1 || len(e.vis.calls) != 0 {
		t.Fatalf("fails %+v vis %v", fails, e.vis.calls)
	}
}

// Notion 409 conflict_error is documented as retryable: fail the message, no back-off.
func TestConflictIsRetriedWithoutVisibilityChange(t *testing.T) {
	e := newEnv(t)
	e.w.handlers[queue.TypeArticle] = func(context.Context, queue.Message) error {
		return &notion.APIError{Status: 409, Code: "conflict_error"}
	}
	if fails := e.handle(queue.Article(testBot, "h")); len(fails) != 1 || len(e.vis.calls) != 0 {
		t.Fatalf("fails %+v vis %v", fails, e.vis.calls)
	}
}

// A 401 that escapes session.call means the refresh endpoint itself rejected us
// (e.g. invalid_client): retry so the message reaches the DLQ instead of vanishing.
func TestUnauthorizedIsRetried(t *testing.T) {
	e := newEnv(t)
	e.w.handlers[queue.TypeArticle] = func(context.Context, queue.Message) error {
		return &notion.APIError{Status: 401, Code: "unauthorized"}
	}
	if fails := e.handle(queue.Article(testBot, "h")); len(fails) != 1 || len(e.vis.calls) != 0 {
		t.Fatalf("fails %+v vis %v", fails, e.vis.calls)
	}
}

// Other 4xx (here 403 restricted_resource) are poison: log and ack.
func TestForbiddenIsAcked(t *testing.T) {
	e := newEnv(t)
	e.w.handlers[queue.TypeArticle] = func(context.Context, queue.Message) error {
		return &notion.APIError{Status: 403, Code: "restricted_resource"}
	}
	if fails := e.handle(queue.Article(testBot, "h")); len(fails) != 0 {
		t.Fatalf("fails %+v", fails)
	}
	if !strings.Contains(e.logs.String(), "restricted_resource") {
		t.Fatal("dropped message not logged")
	}
}

// §2.15: 400 validation_error is a poison message: log and ack.
func TestValidationErrorIsAcked(t *testing.T) {
	e := newEnv(t)
	e.w.handlers[queue.TypeArticle] = func(context.Context, queue.Message) error {
		return &notion.APIError{Status: 400, Code: "validation_error", Message: "body failed validation"}
	}
	if fails := e.handle(queue.Article(testBot, "h")); len(fails) != 0 {
		t.Fatalf("fails %+v", fails)
	}
	if !strings.Contains(e.logs.String(), "validation_error") {
		t.Fatal("poison message not logged")
	}
}

func TestUndecodableAndUnknownMessagesAreAcked(t *testing.T) {
	e := newEnv(t)
	resp, err := e.w.HandleSQS(ctx, events.SQSEvent{Records: []events.SQSMessage{
		{MessageId: "m0", Body: "not json"}, {MessageId: "m1", Body: `{"type":"mystery"}`},
	}})
	if err != nil || len(resp.BatchItemFailures) != 0 {
		t.Fatalf("resp %+v err %v", resp, err)
	}
}

func searchCall(e *wenv) func(string) error {
	return func(tok string) error {
		_, err := e.w.Notion.SearchPages(ctx, tok, "")
		return err
	}
}

// T21 AC / C7: refresh stores both new tokens and bumps token_version.
func TestSessionRefreshPersistsBothTokens(t *testing.T) {
	e := newEnv(t)
	inst := e.seedInstallation("old-access", "old-refresh")
	e.ns.RejectToken("old-access")
	e.ns.SetRefreshResponse(map[string]any{"access_token": "new-access", "refresh_token": "new-refresh", "bot_id": testBot})

	s, err := e.w.openSession(inst)
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
	s, _ := e.w.openSession(inst)
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
	s, _ := e.w.openSession(inst)
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
	s, err := e.w.openSession(inst)
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
	e.w.Store = dynamo.New(&tokenWriteFailDB{API: e.db.DB, n: 2}, e.db.Table)
	e.ns.RejectToken("old-access")
	e.ns.SetRefreshResponse(map[string]any{"access_token": "new-access", "refresh_token": "new-refresh"})

	s, err := e.w.openSession(inst)
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
	e.w.Store = dynamo.New(&tokenWriteFailDB{API: e.db.DB, n: 3}, e.db.Table)
	e.ns.RejectToken("old-access")
	e.ns.SetRefreshResponse(map[string]any{"access_token": "new-access", "refresh_token": "new-refresh"})

	s, err := e.w.openSession(inst)
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
