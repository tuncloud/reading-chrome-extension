package notion

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

var ctx = context.Background()

func newClient(t *testing.T, h http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return &Client{BaseURL: srv.URL, Version: "2025-09-03", ClientID: "cid", ClientSecret: "csecret"}
}

// T11 AC: Notion-Version is on every request.
func TestEveryRequestSendsNotionVersion(t *testing.T) {
	var mu sync.Mutex
	var versions []string
	c := newClient(t, func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		versions = append(versions, r.Header.Get("Notion-Version"))
		mu.Unlock()
		w.Write([]byte(`{"results":[],"has_more":false,"properties":{},"data_sources":[]}`))
	})
	c.SearchPages(ctx, "tok", "q")
	c.ListBlockChildren(ctx, "tok", "b")
	c.GetDatabase(ctx, "tok", "d")
	c.CreateDatabase(ctx, "tok", "parent", "T", map[string]any{})
	c.GetDataSource(ctx, "tok", "ds")
	c.QueryDataSource(ctx, "tok", "ds", map[string]any{})
	c.CreatePage(ctx, "tok", map[string]any{})
	c.UpdatePage(ctx, "tok", "p", map[string]any{})
	c.ExchangeCode(ctx, "code", "https://x/cb")
	c.RefreshToken(ctx, "rt")
	if len(versions) != 10 {
		t.Fatalf("made %d requests, want 10", len(versions))
	}
	for i, v := range versions {
		if v != "2025-09-03" {
			t.Errorf("request %d Notion-Version = %q", i, v)
		}
	}
}

// T11 AC: 429 with Retry-After: 2 → APIError.RetryAfter = 2s.
func TestRateLimitedErrorCarriesRetryAfter(t *testing.T) {
	c := newClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "2")
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte(`{"object":"error","status":429,"code":"rate_limited","message":"slow down"}`))
	})
	_, err := c.UpdatePage(ctx, "tok", "p", map[string]any{})
	e, ok := AsAPIError(err)
	if !ok || e.Status != 429 || e.Code != "rate_limited" || e.RetryAfter != 2*time.Second {
		t.Fatalf("got %#v", err)
	}
}

func TestExchangeCodeUsesBasicAuthAndDecodesOwner(t *testing.T) {
	c := newClient(t, func(w http.ResponseWriter, r *http.Request) {
		id, secret, ok := r.BasicAuth()
		var body map[string]string
		json.NewDecoder(r.Body).Decode(&body)
		if !ok || id != "cid" || secret != "csecret" || body["grant_type"] != "authorization_code" || body["redirect_uri"] != "https://x/cb" {
			t.Errorf("bad token request: auth=%v %q %q body=%v", ok, id, secret, body)
		}
		w.Write([]byte(`{"access_token":"at","refresh_token":"rt","bot_id":"bot-1","workspace_id":"ws","workspace_name":"WS",
			"workspace_icon":null,"duplicated_template_id":"tpl","owner":{"type":"user","user":{"id":"nu","name":"Tuan","person":{"email":"t@x"}}}}`))
	})
	res, err := c.ExchangeCode(ctx, "code", "https://x/cb")
	if err != nil {
		t.Fatal(err)
	}
	if res.AccessToken != "at" || res.BotID != "bot-1" || res.DuplicatedTemplateID != "tpl" || res.Owner.User.ID != "nu" || res.Owner.User.Person.Email != "t@x" {
		t.Fatalf("decoded %+v", res)
	}
}

func TestOAuthErrorBodyParsed(t *testing.T) {
	c := newClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"error":"invalid_grant","error_description":"refresh token is invalid"}`))
	})
	_, err := c.RefreshToken(ctx, "rt")
	if e, ok := AsAPIError(err); !ok || e.Status != 400 || e.Code != "invalid_grant" {
		t.Fatalf("got %#v", err)
	}
}

func TestListBlockChildrenPaginates(t *testing.T) {
	c := newClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("start_cursor") == "" {
			w.Write([]byte(`{"results":[{"id":"a","type":"paragraph"},{"id":"b","type":"child_database","child_database":{"title":"Reading Library"}}],"has_more":true,"next_cursor":"c2"}`))
			return
		}
		w.Write([]byte(`{"results":[{"id":"c","type":"child_database","child_database":{"title":"Daily Log"}}],"has_more":false,"next_cursor":null}`))
	})
	blocks, err := c.ListBlockChildren(ctx, "tok", "tpl")
	if err != nil {
		t.Fatal(err)
	}
	if len(blocks) != 3 || blocks[1].ChildDatabase.Title != "Reading Library" || blocks[2].ID != "c" {
		t.Fatalf("got %+v", blocks)
	}
}

func TestSearchPagesExtractsTitle(t *testing.T) {
	c := newClient(t, func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		if f := body["filter"].(map[string]any); f["value"] != "page" || body["page_size"].(float64) != 25 {
			t.Errorf("bad search body %v", body)
		}
		w.Write([]byte(`{"results":[{"id":"p1","url":"https://notion.so/p1","properties":{"Name":{"id":"title","type":"title","title":[{"plain_text":"Read"},{"plain_text":"ing"}]}}}]}`))
	})
	pages, err := c.SearchPages(ctx, "tok", "Read")
	if err != nil {
		t.Fatal(err)
	}
	if len(pages) != 1 || pages[0].Title != "Reading" || pages[0].URL != "https://notion.so/p1" {
		t.Fatalf("got %+v", pages)
	}
}

func TestBearerTokenSent(t *testing.T) {
	c := newClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer secret-token" {
			t.Errorf("Authorization = %q", r.Header.Get("Authorization"))
		}
		w.Write([]byte(`{"id":"p","url":"u"}`))
	})
	if _, err := c.UpdatePage(ctx, "secret-token", "p", map[string]any{}); err != nil {
		t.Fatal(err)
	}
}

func TestMinIntervalPacer(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	var slept []time.Duration
	p := &MinInterval{Interval: 350 * time.Millisecond, now: func() time.Time { return now },
		sleep: func(_ context.Context, d time.Duration) error { slept = append(slept, d); now = now.Add(d); return nil }}
	p.Wait(ctx)
	now = now.Add(100 * time.Millisecond)
	p.Wait(ctx)
	now = now.Add(time.Second)
	p.Wait(ctx)
	if len(slept) != 1 || slept[0] != 250*time.Millisecond {
		t.Fatalf("slept %v, want [250ms]", slept)
	}
}

// I1: the worker compares a page's parent data source with the installation's.
func TestUpdatePageDecodesParentDataSource(t *testing.T) {
	c := newClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"object":"page","id":"p","parent":{"type":"data_source_id","data_source_id":"1f2e3d4c-5b6a-4789-8abc-def012345678","database_id":"db"}}`))
	})
	p, err := c.UpdatePage(ctx, "tok", "p", map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	if p.Parent.Type != "data_source_id" || p.Parent.DataSourceID != "1f2e3d4c-5b6a-4789-8abc-def012345678" {
		t.Fatalf("parent %+v", p.Parent)
	}
}
