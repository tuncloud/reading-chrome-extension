// Package e2e_test drives API → queue → worker → fake Notion, the local stand-in for T32.
package e2e_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-lambda-go/events"
	"github.com/labstack/echo/v4"

	"reading-tracker/backend/internal/config"
	"reading-tracker/backend/internal/crypto"
	"reading-tracker/backend/internal/httpapi"
	"reading-tracker/backend/internal/notion"
	"reading-tracker/backend/internal/notion/notiontest"
	"reading-tracker/backend/internal/queue"
	"reading-tracker/backend/internal/store/storetest"
	"reading-tracker/backend/internal/worker"
)

const extID = "abcdefghijklmnopabcdefghijklmnop"

type noVisibility struct{}

func (noVisibility) ChangeVisibility(context.Context, string, time.Duration) error { return nil }

func call(t *testing.T, e *echo.Echo, method, target string, body any, token string) *httptest.ResponseRecorder {
	t.Helper()
	var rdr io.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		rdr = bytes.NewReader(raw)
	}
	req := httptest.NewRequest(method, target, rdr)
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	return rec
}

// deliver feeds every queued message to the worker until the queue is empty.
func deliver(t *testing.T, wk *worker.Worker, q *queue.Recorder) {
	t.Helper()
	for round := 0; round < 5; round++ {
		sent := q.Drain()
		if len(sent) == 0 {
			return
		}
		for i, o := range sent {
			body, _ := json.Marshal(o.Message)
			resp, err := wk.HandleSQS(context.Background(), events.SQSEvent{Records: []events.SQSMessage{{MessageId: strconv.Itoa(i), Body: string(body)}}})
			if err != nil || len(resp.BatchItemFailures) > 0 {
				t.Fatalf("worker failed on %s: %v %+v", body, err, resp)
			}
		}
	}
	t.Fatal("queue did not drain")
}

func segment(id string, start time.Time, secs int, progress float64, seq int) map[string]any {
	return map[string]any{"id": id, "session_id": "sess-" + strconv.Itoa(seq/10), "seq": seq % 10,
		"canonical_url": "https://medium.com/p/8f3c2a1b9d4e", "started_at": start.Format(time.RFC3339),
		"ended_at": start.Add(time.Duration(secs) * time.Second).Format(time.RFC3339), "active_seconds": secs, "max_progress": progress}
}

func TestReadArticleEndToEnd(t *testing.T) {
	db := storetest.New(t)
	ns := notiontest.New(t)
	tpl := ns.AddTemplate("tpl-1")
	q := &queue.Recorder{}
	now := time.Date(2026, 10, 6, 3, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	vars := map[string]string{
		"APP_ENV": "test", "TABLE_NAME": db.Table, "QUEUE_URL": "local", "NOTION_CLIENT_ID": "cid", "NOTION_CLIENT_SECRET": "cs",
		"NOTION_VERSION": "2025-09-03", "NOTION_API_BASE": ns.URL, "ALLOWED_EXTENSION_IDS": extID, "DEBOUNCE_BUCKET_SECONDS": "120",
		"TOKEN_ENC_KEYS":      "v1:" + base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{1}, 32)),
		"DEVICE_TOKEN_PEPPER": base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{2}, 32)),
	}
	cfg, err := config.Load(config.RoleAPI, func(k string) string { return vars[k] })
	if err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	nc := &notion.Client{BaseURL: ns.URL, Version: cfg.NotionVersion, ClientID: cfg.NotionClientID, ClientSecret: cfg.NotionClientSecret}
	api := httpapi.New(httpapi.Deps{Config: cfg, Store: db.Store, Notion: nc, Queue: q, Now: clock, Log: log})
	wk := worker.New(worker.Deps{Store: db.Store, Notion: nc, Keys: cfg.Keys, Queue: q, Visibility: noVisibility{},
		Tracking: cfg.Public.Tracking, Now: clock, Log: log})

	// 1. Connect Notion, accepting the template (scenario 1).
	ns.SetTokenResponse(map[string]any{"access_token": "at", "refresh_token": "rt", "bot_id": "bot-e2e", "workspace_id": "ws",
		"workspace_name": "WS", "duplicated_template_id": "tpl-1", "owner": map[string]any{"type": "user", "user": map[string]any{"id": "nu", "name": "Tuan"}}})
	verifier := strings.Repeat("v", 64)
	ext := "https://" + extID + ".chromiumapp.org/"
	start := call(t, api, "GET", "/v1/oauth/notion/start?"+url.Values{"ext_redirect": {ext}, "code_challenge": {crypto.S256Challenge(verifier)}, "code_challenge_method": {"S256"}}.Encode(), nil, "")
	loc, _ := url.Parse(start.Header().Get("Location"))
	cb := call(t, api, "GET", "/v1/oauth/notion/callback?code=c&state="+loc.Query().Get("state"), nil, "")
	loc, _ = url.Parse(cb.Header().Get("Location"))
	tokRec := call(t, api, "POST", "/v1/auth/token", map[string]string{"code": loc.Query().Get("code"), "code_verifier": verifier, "device_name": "e2e"}, "")
	var tok struct {
		DeviceToken string `json:"device_token"`
	}
	json.Unmarshal(tokRec.Body.Bytes(), &tok)
	if tok.DeviceToken == "" {
		t.Fatalf("no device token: %d %s", tokRec.Code, tokRec.Body)
	}

	// 2. Read for 3 minutes (three 60 s segments).
	article := map[string]any{"canonical_url": "https://medium.com/p/8f3c2a1b9d4e", "source": "medium", "title": "Designing Idempotent APIs",
		"author": "Jane Doe", "site_name": "Better Programming", "word_count": 1820}
	t0 := now.Add(-5 * time.Minute)
	push := map[string]any{"tz": "Asia/Ho_Chi_Minh", "articles": []any{article}, "segments": []any{
		segment("00000000-0000-4000-8000-000000000001", t0, 60, 0.2, 0),
		segment("00000000-0000-4000-8000-000000000002", t0.Add(time.Minute), 60, 0.4, 1),
		segment("00000000-0000-4000-8000-000000000003", t0.Add(2*time.Minute), 60, 0.6, 2),
	}}
	if rec := call(t, api, "POST", "/v1/sync/push", push, tok.DeviceToken); rec.Code != 200 || !strings.Contains(rec.Body.String(), `"retry":[]`) {
		t.Fatalf("push %d %s", rec.Code, rec.Body)
	}
	deliver(t, wk, q)

	lib := ns.Pages(tpl.LibraryDS)
	daily := ns.Pages(tpl.DailyDS)
	minutes := func(p *notiontest.Page, col string) any {
		return p.Properties[notiontest.PropID(col)].(map[string]any)["number"]
	}
	if len(lib) != 1 || minutes(lib[0], "Reading time (min)") != 3.0 || minutes(lib[0], "Progress") != 0.6 {
		t.Fatalf("library pages %+v", lib)
	}
	if len(daily) != 1 || minutes(daily[0], "Minutes") != 3.0 {
		t.Fatalf("daily pages %+v", daily)
	}
	rel := daily[0].Properties[notiontest.PropID("Articles")].(map[string]any)["relation"].([]any)
	if len(rel) != 1 || rel[0].(map[string]any)["id"] != lib[0].ID {
		t.Fatalf("relation %v", rel)
	}

	// 3. The user curates in Notion, then reads again: curation kept, metrics updated (T32 AC).
	curated := map[string]any{"status": map[string]any{"name": "Reading"}}
	ns.SetPageProperty(lib[0].ID, notiontest.PropID("Status"), curated)
	now = now.Add(time.Hour)
	push["segments"] = []any{segment("00000000-0000-4000-8000-000000000004", now.Add(-2*time.Minute), 60, 0.7, 10)}
	call(t, api, "POST", "/v1/sync/push", push, tok.DeviceToken)
	deliver(t, wk, q)
	if minutes(lib[0], "Reading time (min)") != 4.0 || minutes(lib[0], "Sessions") != 2.0 {
		t.Fatalf("metrics not updated: %+v", lib[0].Properties)
	}
	if !reflect.DeepEqual(lib[0].Properties[notiontest.PropID("Status")], curated) {
		t.Fatal("user-owned Status changed")
	}
	if n := len(ns.Pages(tpl.LibraryDS)); n != 1 {
		t.Fatalf("%d library pages after second read", n)
	}

	// 4. The status endpoint exposes the Notion URL.
	st := call(t, api, "POST", "/v1/articles/status", map[string]any{"canonical_urls": []string{"https://medium.com/p/8f3c2a1b9d4e"}}, tok.DeviceToken)
	if !strings.Contains(st.Body.String(), lib[0].URL) {
		t.Fatalf("status %s", st.Body)
	}
}
