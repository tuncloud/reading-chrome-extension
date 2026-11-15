package httpapi_test

import (
	"context"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"reading-tracker/backend/internal/domain"
	"reading-tracker/backend/internal/platform/crypto"
)

const (
	historyURLA = "https://medium.com/p/8f3c2a1b9d4e"
	historyURLB = "https://medium.com/p/1a2b3c4d5e6f"
	historyURLC = "https://medium.com/p/aabbccddeeff"
)

type historyArticle struct {
	CanonicalURL string  `json:"canonical_url"`
	Source       string  `json:"source"`
	Title        string  `json:"title"`
	Author       string  `json:"author"`
	SiteName     string  `json:"site_name"`
	WordCount    int     `json:"word_count"`
	PublishedAt  string  `json:"published_at"`
	TotalSeconds int64   `json:"total_seconds"`
	MaxProgress  float64 `json:"max_progress"`
	LastReadAt   string  `json:"last_read_at"`
	Completed    bool    `json:"completed"`
	NotionURL    *string `json:"notion_url"`
}

type historyResp struct {
	Articles []historyArticle `json:"articles"`
	Today    struct {
		Date    string `json:"date"`
		Seconds int64  `json:"seconds"`
	} `json:"today"`
}

// historySegment builds a push segment of url that ended ago before env.now and lasted secs.
func historySegment(env *testEnv, n int, url string, secs int, progress float64, ago time.Duration) map[string]any {
	end := env.now.Add(-ago)
	start := end.Add(-time.Duration(secs) * time.Second)
	return map[string]any{
		"id": fmt.Sprintf("00000000-0000-4000-8000-%012d", n), "session_id": fmt.Sprintf("s%d", n), "seq": 0,
		"canonical_url": url, "started_at": start.Format(time.RFC3339), "ended_at": end.Format(time.RFC3339),
		"active_seconds": secs, "max_progress": progress,
	}
}

func historyMeta(url, title string) map[string]any {
	return map[string]any{"canonical_url": url, "source": "medium", "title": title, "author": "Jane Doe",
		"site_name": "Better Programming", "word_count": 1000, "published_at": "2026-09-30"}
}

// pushReading pushes one segment per (url, secs, progress, ago) through the API as tok's user.
func pushReading(t *testing.T, env *testEnv, tok string, reads ...struct {
	URL      string
	Secs     int
	Progress float64
	Ago      time.Duration
}) {
	t.Helper()
	var arts, segs []map[string]any
	for i, r := range reads {
		arts = append(arts, historyMeta(r.URL, "Title of "+r.URL))
		segs = append(segs, historySegment(env, i+1, r.URL, r.Secs, r.Progress, r.Ago))
	}
	body := map[string]any{"client_time": env.now.Format(time.RFC3339), "tz": "Asia/Ho_Chi_Minh", "articles": arts, "segments": segs}
	if rec := env.do("POST", "/v1/sync/push", body, tok); rec.Code != 200 || strings.Contains(rec.Body.String(), `"reason"`) {
		t.Fatalf("push: %d %s", rec.Code, rec.Body)
	}
}

type read = struct {
	URL      string
	Secs     int
	Progress float64
	Ago      time.Duration
}

func seedTwoReads(t *testing.T, env *testEnv) string {
	t.Helper()
	uid, tok := env.seedUser()
	env.seedInstallation(uid, domain.StatusActive)
	pushReading(t, env, tok,
		read{historyURLA, 180, 0.6, 30 * time.Minute},
		read{historyURLB, 60, 0.2, 5 * time.Minute})
	return tok
}

func TestHistoryReturnsRecentAndToday(t *testing.T) {
	env := newTestEnv(t)
	tok := seedTwoReads(t, env)

	rec := env.do("GET", "/v1/history?tz=Asia/Ho_Chi_Minh", nil, tok)
	if rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	t.Logf("body: %s", rec.Body)
	got := decode[historyResp](t, rec)
	if len(got.Articles) != 2 {
		t.Fatalf("articles = %+v", got.Articles)
	}
	if got.Articles[0].CanonicalURL != historyURLB || got.Articles[1].CanonicalURL != historyURLA {
		t.Fatalf("order = %s, %s; want the later-read article first", got.Articles[0].CanonicalURL, got.Articles[1].CanonicalURL)
	}
	a := got.Articles[1]
	if a.TotalSeconds != 180 || a.MaxProgress != 0.6 || a.Completed || a.NotionURL != nil ||
		a.Title != "Title of "+historyURLA || a.Source != "medium" || a.Author != "Jane Doe" ||
		a.SiteName != "Better Programming" || a.WordCount != 1000 || a.PublishedAt != "2026-09-30" ||
		a.LastReadAt != env.now.Add(-30*time.Minute).Format(time.RFC3339) {
		t.Fatalf("article A = %+v", a)
	}
	if !strings.Contains(rec.Body.String(), `"notion_url":null`) {
		t.Fatalf("notion_url must be null: %s", rec.Body)
	}
	if got.Today.Date != "2026-10-06" || got.Today.Seconds != 240 {
		t.Fatalf("today = %+v, want 2026-10-06 / 240", got.Today)
	}
}

func TestHistoryShowsNotionURLOnceAPageExists(t *testing.T) {
	env := newTestEnv(t)
	tok := seedTwoReads(t, env)
	must(t, env.db.Store.SetArticlePage(context.Background(), env.readerID(), domain.URLHash(historyURLA), "page-1", "https://notion.so/page-1", "ds-1"))

	got := decode[historyResp](t, env.do("GET", "/v1/history?tz=UTC", nil, tok))
	if len(got.Articles) != 2 {
		t.Fatalf("articles = %+v", got.Articles)
	}
	if n := got.Articles[1].NotionURL; got.Articles[1].CanonicalURL != historyURLA || n == nil || *n != "https://notion.so/page-1" {
		t.Fatalf("articles = %+v", got.Articles)
	}
	if got.Articles[0].NotionURL != nil {
		t.Fatalf("article without a page has notion_url %v", *got.Articles[0].NotionURL)
	}
}

func TestHistoryTodayFollowsTimeZone(t *testing.T) {
	env := newTestEnv(t)
	tok := seedTwoReads(t, env)

	got := decode[historyResp](t, env.do("GET", "/v1/history?tz=America/Los_Angeles", nil, tok))
	if got.Today.Date != "2026-10-05" || got.Today.Seconds != 0 {
		t.Fatalf("today = %+v, want 2026-10-05 / 0", got.Today)
	}
	if len(got.Articles) != 2 {
		t.Fatalf("articles = %+v", got.Articles)
	}
}

func TestHistoryWorksWithoutActiveInstallation(t *testing.T) {
	for _, status := range []string{domain.StatusNeedsSetup, domain.StatusRevoked, ""} {
		t.Run("installation="+status, func(t *testing.T) {
			env := newTestEnv(t)
			uid, tok := env.seedUser()
			if status != "" {
				env.seedInstallation(uid, status)
			}
			start := env.now.Add(-10 * time.Minute)
			w := domain.SegmentWrite{
				UserID: uid, DeviceID: "dev-1", SegmentID: "00000000-0000-4000-8000-000000000001", SessionID: "s1",
				URLHash: domain.URLHash(historyURLA), Date: "2026-10-06", StartedAt: start, EndedAt: start.Add(time.Minute),
				ActiveSeconds: 60, MaxProgress: 0.5,
				Article: domain.ArticleMeta{CanonicalURL: historyURLA, Source: "medium", Title: "Stored", WordCount: 10},
			}
			if _, err := env.db.Store.PushSegment(context.Background(), w); err != nil {
				t.Fatal(err)
			}
			rec := env.do("GET", "/v1/history?tz=UTC", nil, tok)
			if rec.Code != 200 {
				t.Fatalf("%d %s", rec.Code, rec.Body)
			}
			got := decode[historyResp](t, rec)
			if len(got.Articles) != 1 || got.Articles[0].CanonicalURL != historyURLA || got.Articles[0].Title != "Stored" {
				t.Fatalf("articles = %+v", got.Articles)
			}
			if got.Today.Date != "2026-10-06" || got.Today.Seconds != 60 {
				t.Fatalf("today = %+v", got.Today)
			}
		})
	}
}

// seedSecondUser creates another user with one device and returns its bearer token.
func (env *testEnv) seedSecondUser() (uid, token string) {
	t, ctx := env.t, context.Background()
	uid = "01J9ZK5USER00000000000002"
	must(t, env.db.Store.CreateUser(ctx, uid, domain.User{NotionUserID: "nuser-2", Name: "Other Reader", CreatedAt: domain.Timestamp(env.now)}))
	token, err := crypto.NewDeviceToken()
	must(t, err)
	must(t, env.db.Store.CreateDevice(ctx, uid, "dev-2", domain.Device{Name: "Other Chrome",
		TokenHMAC: crypto.TokenHMAC(env.cfg.DeviceTokenPepper, token), CreatedAt: domain.Timestamp(env.now), LastSeenAt: domain.Timestamp(env.now)}))
	return uid, token
}

func TestHistoryIsPerUser(t *testing.T) {
	env := newTestEnv(t)
	seedTwoReads(t, env)
	_, tok2 := env.seedSecondUser()

	rec := env.do("GET", "/v1/history?tz=Asia/Ho_Chi_Minh", nil, tok2)
	if rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if !strings.Contains(rec.Body.String(), `"articles":[]`) {
		t.Fatalf("second user sees %s", rec.Body)
	}
	if got := decode[historyResp](t, rec); got.Today.Seconds != 0 {
		t.Fatalf("today = %+v, want 0 seconds", got.Today)
	}
}

func TestHistoryEmptyIsArrayNotNull(t *testing.T) {
	env := newTestEnv(t)
	_, tok := env.seedUser()
	rec := env.do("GET", "/v1/history?tz=UTC", nil, tok)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"articles":[]`) {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if got := decode[historyResp](t, rec); got.Today.Date != "2026-10-06" || got.Today.Seconds != 0 {
		t.Fatalf("today = %+v", got.Today)
	}
}

func TestHistoryLimit(t *testing.T) {
	env := newTestEnv(t)
	uid, tok := env.seedUser()
	env.seedInstallation(uid, domain.StatusActive)
	pushReading(t, env, tok,
		read{historyURLA, 60, 0.1, 30 * time.Minute},
		read{historyURLB, 60, 0.1, 20 * time.Minute},
		read{historyURLC, 60, 0.1, 10 * time.Minute})

	count := func(query string) int {
		rec := env.do("GET", "/v1/history?tz=UTC"+query, nil, tok)
		if rec.Code != 200 {
			t.Fatalf("%q: %d %s", query, rec.Code, rec.Body)
		}
		return len(decode[historyResp](t, rec).Articles)
	}
	for query, want := range map[string]int{"&limit=2": 2, "": 3, "&limit=500": 3, "&limit=1": 1} {
		if n := count(query); n != want {
			t.Errorf("%q: %d articles, want %d", query, n, want)
		}
	}
	for _, bad := range []string{"0", "-1", "abc", "", "1.5"} {
		rec := env.do("GET", "/v1/history?tz=UTC&limit="+bad, nil, tok)
		if rec.Code != 400 || errorCode(t, rec) != "invalid_request" {
			t.Errorf("limit=%q: %d %s", bad, rec.Code, rec.Body)
		}
	}
}

func TestHistoryRejectsBadTimeZone(t *testing.T) {
	env := newTestEnv(t)
	_, tok := env.seedUser()
	for _, target := range []string{"/v1/history", "/v1/history?tz=", "/v1/history?tz=Local", "/v1/history?tz=Not/AZone"} {
		rec := env.do("GET", target, nil, tok)
		if rec.Code != 400 || errorCode(t, rec) != "invalid_request" {
			t.Errorf("%s: %d %s", target, rec.Code, rec.Body)
		}
	}
}

func TestHistoryRequiresDevice(t *testing.T) {
	env := newTestEnv(t)
	var rec *httptest.ResponseRecorder = env.do("GET", "/v1/history?tz=UTC", nil, "")
	if rec.Code != 401 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
}
