package httpapi_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/labstack/echo/v4"

	"reading-tracker/backend/internal/adapter/dynamo"
	"reading-tracker/backend/internal/adapter/dynamo/dynamotest"
	"reading-tracker/backend/internal/adapter/notion"
	"reading-tracker/backend/internal/adapter/notion/notiontest"
	"reading-tracker/backend/internal/adapter/queue"
	"reading-tracker/backend/internal/domain"
	"reading-tracker/backend/internal/httpapi"
	"reading-tracker/backend/internal/platform/config"
	"reading-tracker/backend/internal/platform/crypto"
)

const (
	testExtID = "abcdefghijklmnopabcdefghijklmnop"
	testBotID = "b7c1e0f2-0000-4000-8000-000000000001"
)

var testVerifier = strings.Repeat("v", 64)

type testEnv struct {
	t      *testing.T
	db     *dynamotest.Env
	notion *notiontest.Server
	queue  *queue.Recorder
	cfg    *config.Config
	now    time.Time
	logs   *bytes.Buffer
	sleeps []time.Duration
	e      *echo.Echo
}

func newTestEnv(t *testing.T) *testEnv {
	t.Helper()
	env := &testEnv{t: t, db: dynamotest.New(t), notion: notiontest.New(t), queue: &queue.Recorder{}, logs: &bytes.Buffer{},
		now: time.Date(2026, 10, 6, 3, 12, 0, 0, time.UTC)}
	vars := map[string]string{
		"APP_ENV": "test", "TABLE_NAME": env.db.Table, "QUEUE_URL": "local", "NOTION_CLIENT_ID": "cid",
		"NOTION_CLIENT_SECRET": "csecret", "NOTION_VERSION": "2025-09-03", "NOTION_API_BASE": env.notion.URL,
		"TOKEN_ENC_KEYS":          "v1:" + base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{1}, 32)),
		"DEVICE_TOKEN_PEPPER":     base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{2}, 32)),
		"ALLOWED_EXTENSION_IDS":   testExtID,
		"DEBOUNCE_BUCKET_SECONDS": "120",
	}
	cfg, err := config.Load(config.RoleAPI, func(k string) string { return vars[k] })
	must(t, err)
	env.cfg = cfg
	nc := &notion.Client{BaseURL: cfg.NotionAPIBase, Version: cfg.NotionVersion, ClientID: cfg.NotionClientID, ClientSecret: cfg.NotionClientSecret}
	env.e = httpapi.New(httpapi.Deps{Config: cfg, Store: env.db.Store, Notion: nc, Queue: env.queue,
		Now: func() time.Time { return env.now }, Log: slog.New(slog.NewTextHandler(env.logs, nil)),
		Sleep: func(d time.Duration) { env.sleeps = append(env.sleeps, d) }})
	return env
}

// do sends a request. body may be nil, a raw string, or any JSON-encodable value.
func (env *testEnv) do(method, target string, body any, token string) *httptest.ResponseRecorder {
	env.t.Helper()
	var rdr io.Reader
	switch b := body.(type) {
	case nil:
	case string:
		rdr = strings.NewReader(b)
	default:
		raw, err := json.Marshal(b)
		must(env.t, err)
		rdr = bytes.NewReader(raw)
	}
	req := httptest.NewRequest(method, target, rdr)
	if rdr != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	env.e.ServeHTTP(rec, req)
	return rec
}

func decode[T any](t *testing.T, rec *httptest.ResponseRecorder) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatalf("decode %q: %v", rec.Body.String(), err)
	}
	return v
}

func errorCode(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var body struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("not a §2.0 error body: %q", rec.Body.String())
	}
	return body.Error.Code
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// seedUser creates a user with one device and returns its bearer token.
func (env *testEnv) seedUser() (uid, token string) {
	t, ctx := env.t, context.Background()
	uid = "01J9ZK5USER00000000000001"
	must(t, env.db.Store.CreateUser(ctx, uid, domain.User{NotionUserID: "nuser-1", Name: "Tuan Do", CreatedAt: domain.Timestamp(env.now)}))
	token, err := crypto.NewDeviceToken()
	must(t, err)
	must(t, env.db.Store.CreateDevice(ctx, uid, "dev-1", domain.Device{Name: "MacBook Chrome",
		TokenHMAC: crypto.TokenHMAC(env.cfg.DeviceTokenPepper, token), CreatedAt: domain.Timestamp(env.now), LastSeenAt: domain.Timestamp(env.now)}))
	return uid, token
}

// seedInstallation stores an installation for uid with tokens sealed the way the callback seals them.
func (env *testEnv) seedInstallation(uid, status string) *domain.Installation {
	t, ctx := env.t, context.Background()
	tpl := env.notion.AddTemplate("tpl-1")
	access, err := env.cfg.Keys.Seal([]byte("secret-access-token"), []byte(testBotID))
	must(t, err)
	refresh, err := env.cfg.Keys.Seal([]byte("secret-refresh-token"), []byte(testBotID))
	must(t, err)
	inst := &domain.Installation{UserID: uid, WorkspaceID: "ws-1", WorkspaceName: "Tuan's Notion", Status: status,
		AccessTokenEnc: access, RefreshTokenEnc: refresh, TokenVersion: 1, CreatedAt: domain.Timestamp(env.now), UpdatedAt: domain.Timestamp(env.now)}
	if status == domain.StatusActive {
		inst.ApplyDatabases(tpl.DBConfig())
	}
	must(t, env.db.Store.PutInstallation(ctx, testBotID, inst))
	must(t, env.db.Store.PutUserInstallation(ctx, uid, testBotID, env.now))
	must(t, env.db.Store.SetActiveInstallation(ctx, uid, testBotID))
	return inst
}

// setDeviceAttr overwrites one attribute of the device item in place, leaving every other
// item (notably the DEVTOKEN lookup) untouched.
func (env *testEnv) setDeviceAttr(uid, deviceID, attr, value string) {
	env.t.Helper()
	_, err := env.db.DB.UpdateItem(context.Background(), &dynamodb.UpdateItemInput{
		TableName:                 aws.String(env.db.Table),
		Key:                       map[string]types.AttributeValue{"pk": &types.AttributeValueMemberS{Value: dynamo.UserPK(uid)}, "sk": &types.AttributeValueMemberS{Value: dynamo.DeviceSK(deviceID)}},
		UpdateExpression:          aws.String("SET #a = :v"),
		ExpressionAttributeNames:  map[string]string{"#a": attr},
		ExpressionAttributeValues: map[string]types.AttributeValue{":v": &types.AttributeValueMemberS{Value: value}},
	})
	must(env.t, err)
}

func (env *testEnv) lastSeen(uid, deviceID string) string {
	env.t.Helper()
	dev, err := env.db.Store.GetDevice(context.Background(), uid, deviceID)
	must(env.t, err)
	return dev.LastSeenAt
}
