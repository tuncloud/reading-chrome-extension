package httpapi_test

import (
	"context"
	"fmt"
	"reading-tracker/backend/internal/domain"
	"testing"
	"time"
)

type statusResp struct {
	Items map[string]struct {
		TotalSeconds int64   `json:"total_seconds"`
		MaxProgress  float64 `json:"max_progress"`
		Sessions     int64   `json:"sessions"`
		Completed    bool    `json:"completed"`
		LastReadAt   string  `json:"last_read_at"`
		NotionURL    *string `json:"notion_url"`
	} `json:"items"`
}

func TestArticlesStatus(t *testing.T) {
	env := newTestEnv(t)
	uid, tok := env.seedUser()
	env.seedInstallation(uid, domain.StatusActive)
	ctx := context.Background()
	u := "https://medium.com/p/8f3c2a1b9d4e"
	_, err := env.db.Store.PushSegment(ctx, domain.SegmentWrite{InstallationID: testBotID, DeviceID: "dev-1", SegmentID: "00000000-0000-4000-8000-000000000001",
		SessionID: "s", URLHash: domain.URLHash(u), Date: "2026-10-06", StartedAt: env.now, EndedAt: env.now.Add(time.Minute),
		ActiveSeconds: 60, MaxProgress: 0.97, Article: domain.ArticleMeta{CanonicalURL: u, Source: "medium"}})
	must(t, err)
	must(t, env.db.Store.SetArticlePage(ctx, testBotID, domain.URLHash(u), "page-1", "https://www.notion.so/page-1"))

	rec := env.do("POST", "/v1/articles/status", map[string]any{"canonical_urls": []string{u, "https://unknown.example/x"}}, tok)
	if rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	body := decode[statusResp](t, rec)
	got, ok := body.Items[u]
	if len(body.Items) != 1 || !ok || got.TotalSeconds != 60 || got.MaxProgress != 0.97 || got.Sessions != 1 ||
		got.NotionURL == nil || *got.NotionURL != "https://www.notion.so/page-1" || got.LastReadAt != domain.Timestamp(env.now.Add(time.Minute)) {
		t.Fatalf("items %+v", body.Items)
	}
}

func TestArticlesStatusLimit(t *testing.T) {
	env := newTestEnv(t)
	uid, tok := env.seedUser()
	env.seedInstallation(uid, domain.StatusActive)
	urls := make([]string, 101)
	for i := range urls {
		urls[i] = fmt.Sprintf("https://example.com/%d", i)
	}
	rec := env.do("POST", "/v1/articles/status", map[string]any{"canonical_urls": urls}, tok)
	if rec.Code != 413 || errorCode(t, rec) != "payload_too_large" {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
}

func TestArticlesStatusNoInstallation(t *testing.T) {
	env := newTestEnv(t)
	_, tok := env.seedUser()
	rec := env.do("POST", "/v1/articles/status", map[string]any{"canonical_urls": []string{"https://a.example"}}, tok)
	if rec.Code != 409 || errorCode(t, rec) != "no_installation" {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
}
