package storetest

import (
	"errors"
	"slices"
	"testing"
	"time"

	"reading-tracker/backend/internal/domain"
)

var readingChecks = []struct {
	name string
	run  func(t *testing.T, s ReadingStore)
}{
	{"PushSegmentTwiceCountsOnce", pushSegmentTwiceCountsOnce},
	{"MaxProgressNeverDecreases", maxProgressNeverDecreases},
	{"OutOfOrderSessionCountedOnce", outOfOrderSessionCountedOnce},
	{"MarkArticleSyncedIsMonotonic", markArticleSyncedIsMonotonic},
	{"ArticleAndDayPageLinks", articleAndDayPageLinks},
	{"PendingSync", pendingSync},
	{"PendingSyncAllIncludesSynced", pendingSyncAllIncludesSynced},
	{"CreateUserRelinksNotionUser", createUserRelinksNotionUser},
	{"EmptyOptionalFieldsRoundTrip", emptyOptionalFieldsRoundTrip},
	{"PurgeRemovesInstallationAndUserData", purgeRemovesInstallationAndUserData},
	{"BatchGetArticlesReturnsOnlyExisting", batchGetArticlesReturnsOnlyExisting},
	{"ReadingIsPerUser", readingIsPerUser},
	{"RecentArticlesNewestFirst", recentArticlesNewestFirst},
}

// T10 AC + scenario 2: the same segment twice increments aggregates once (C2).
func pushSegmentTwiceCountsOnce(t *testing.T, s ReadingStore) {
	w := Segment("6f1d2c9e-6a43-4b1e-9a0e-2f7c1d5b8e11", ArticleURL, 0, 58, 0.42, TestNow)

	if out := Push(t, s, w); out != domain.PushAccepted {
		t.Fatalf("first push = %v", out)
	}
	if out := Push(t, s, w); out != domain.PushDuplicate {
		t.Fatalf("second push = %v, want duplicate", out)
	}
	a, err := s.GetArticle(ctx, UserID, domain.URLHash(ArticleURL))
	Must(t, err)
	if a.TotalSeconds != 58 || a.Sessions != 1 || a.Version != 1 || a.MaxProgress != 0.42 || a.CanonicalURL != ArticleURL || a.Title != "Designing Idempotent APIs" {
		t.Fatalf("aggregate wrong: %+v", a)
	}
	if a.FirstReadAt != domain.Timestamp(TestNow) || a.LastReadAt != domain.Timestamp(TestNow.Add(58*time.Second)) {
		t.Fatalf("read times wrong: first=%s last=%s", a.FirstReadAt, a.LastReadAt)
	}
	d, err := s.GetDay(ctx, UserID, "2026-10-06")
	Must(t, err)
	if d.Seconds != 58 || d.Version != 1 || len(d.ArticleHashes) != 1 || d.ArticleHashes[0] != domain.URLHash(ArticleURL) {
		t.Fatalf("day wrong: %+v", d)
	}
}

// T10 AC: max_progress (and last_read_at) never decrease.
func maxProgressNeverDecreases(t *testing.T, s ReadingStore) {
	Push(t, s, Segment("00000000-0000-4000-8000-000000000001", ArticleURL, 0, 60, 0.6, TestNow.Add(time.Hour)))
	Push(t, s, Segment("00000000-0000-4000-8000-000000000002", ArticleURL, 1, 60, 0.3, TestNow))
	a, _ := s.GetArticle(ctx, UserID, domain.URLHash(ArticleURL))
	if a.MaxProgress != 0.6 {
		t.Fatalf("max_progress = %v, want 0.6", a.MaxProgress)
	}
	if a.LastReadAt != domain.Timestamp(TestNow.Add(time.Hour+60*time.Second)) {
		t.Fatalf("last_read_at went backwards: %s", a.LastReadAt)
	}
	if a.TotalSeconds != 120 || a.Version != 2 {
		t.Fatalf("totals wrong: %+v", a)
	}
}

// Scenario 3: seq 1 before seq 0 still counts one session.
func outOfOrderSessionCountedOnce(t *testing.T, s ReadingStore) {
	Push(t, s, Segment("00000000-0000-4000-8000-000000000002", ArticleURL, 1, 60, 0.5, TestNow.Add(time.Minute)))
	Push(t, s, Segment("00000000-0000-4000-8000-000000000001", ArticleURL, 0, 60, 0.2, TestNow))
	a, _ := s.GetArticle(ctx, UserID, domain.URLHash(ArticleURL))
	if a.Sessions != 1 || a.TotalSeconds != 120 {
		t.Fatalf("sessions=%d total=%d", a.Sessions, a.TotalSeconds)
	}
}

func markArticleSyncedIsMonotonic(t *testing.T, s ReadingStore) {
	h := domain.URLHash(ArticleURL)
	Push(t, s, Segment("00000000-0000-4000-8000-000000000001", ArticleURL, 0, 60, 0.5, TestNow))
	Must(t, s.MarkArticleSynced(ctx, UserID, h, 2, true, TestNow))
	Must(t, s.MarkArticleSynced(ctx, UserID, h, 1, false, TestNow))
	a, _ := s.GetArticle(ctx, UserID, h)
	if a.NotionSyncedVersion != 2 || !a.Completed {
		t.Fatalf("older version overwrote newer: %+v", a)
	}
}

func articleAndDayPageLinks(t *testing.T, s ReadingStore) {
	h := domain.URLHash(ArticleURL)
	Push(t, s, Segment("00000000-0000-4000-8000-000000000001", ArticleURL, 0, 60, 0.5, TestNow))
	Must(t, s.SetArticlePage(ctx, UserID, h, "page-1", "https://notion.so/page-1", "ds-1"))
	Must(t, s.SetDayPage(ctx, UserID, "2026-10-06", "day-page-1", "ds-2"))
	a, _ := s.GetArticle(ctx, UserID, h)
	d, _ := s.GetDay(ctx, UserID, "2026-10-06")
	if a.NotionPageID != "page-1" || a.NotionURL != "https://notion.so/page-1" || d.NotionPageID != "day-page-1" ||
		a.NotionDataSourceID != "ds-1" || d.NotionDataSourceID != "ds-2" {
		t.Fatalf("links not saved: %+v %+v", a, d)
	}
	Must(t, s.ClearArticlePage(ctx, UserID, h))
	Must(t, s.ClearDayPage(ctx, UserID, "2026-10-06"))
	a, _ = s.GetArticle(ctx, UserID, h)
	d, _ = s.GetDay(ctx, UserID, "2026-10-06")
	if a.NotionPageID != "" || a.NotionURL != "" || d.NotionPageID != "" ||
		a.NotionDataSourceID != "" || d.NotionDataSourceID != "" {
		t.Fatalf("links not cleared: %+v %+v", a, d)
	}
}

// T22: only items whose version is ahead of notion_synced_version are pending.
func pendingSync(t *testing.T, s ReadingStore) {
	other := "https://example.com/post"
	Push(t, s, Segment("00000000-0000-4000-8000-000000000001", ArticleURL, 0, 60, 0.5, TestNow))
	Push(t, s, Segment("00000000-0000-4000-8000-000000000002", other, 0, 60, 0.5, TestNow))
	Must(t, s.MarkArticleSynced(ctx, UserID, domain.URLHash(ArticleURL), 1, false, TestNow))

	hashes, dates, err := s.PendingSync(ctx, UserID, false)
	Must(t, err)
	if len(hashes) != 1 || hashes[0] != domain.URLHash(other) {
		t.Fatalf("hashes = %v", hashes)
	}
	if len(dates) != 1 || dates[0] != "2026-10-06" {
		t.Fatalf("dates = %v", dates)
	}
	Must(t, s.MarkDaySynced(ctx, UserID, "2026-10-06", 2))
	if _, dates, _ = s.PendingSync(ctx, UserID, false); len(dates) != 0 {
		t.Fatalf("synced day still pending: %v", dates)
	}
}

// I1: after the target databases change, the "all" mode lists synced items too.
func pendingSyncAllIncludesSynced(t *testing.T, s ReadingStore) {
	other := "https://example.com/post"
	Push(t, s, Segment("00000000-0000-4000-8000-000000000001", ArticleURL, 0, 60, 0.5, TestNow))
	Push(t, s, Segment("00000000-0000-4000-8000-000000000002", other, 0, 60, 0.5, TestNow))
	Must(t, s.MarkArticleSynced(ctx, UserID, domain.URLHash(ArticleURL), 1, false, TestNow))
	Must(t, s.MarkDaySynced(ctx, UserID, "2026-10-06", 2))

	hashes, dates, err := s.PendingSync(ctx, UserID, true)
	Must(t, err)
	want := []string{domain.URLHash(ArticleURL), domain.URLHash(other)}
	slices.Sort(want)
	slices.Sort(hashes)
	if !slices.Equal(hashes, want) {
		t.Fatalf("hashes = %v, want %v", hashes, want)
	}
	if len(dates) != 1 || dates[0] != "2026-10-06" {
		t.Fatalf("dates = %v", dates)
	}
}

// Review focus 3: a new user re-links the Notion user; purging the old one keeps the new link.
func createUserRelinksNotionUser(t *testing.T, s ReadingStore) {
	Must(t, s.CreateUser(ctx, "u1", domain.User{NotionUserID: "n1", Name: "old", CreatedAt: domain.Timestamp(TestNow)}))
	Must(t, s.CreateUser(ctx, "u2", domain.User{NotionUserID: "n1", Name: "new", CreatedAt: domain.Timestamp(TestNow)}))
	if uid, err := s.UserIDForNotionUser(ctx, "n1"); err != nil || uid != "u2" {
		t.Fatalf("after relink: %q %v, want u2", uid, err)
	}
	Must(t, s.DeleteUserData(ctx, "u1"))
	Must(t, s.DeleteNotionUserLink(ctx, "n1", "u1"))
	if uid, err := s.UserIDForNotionUser(ctx, "n1"); err != nil || uid != "u2" {
		t.Fatalf("after purging u1: %q %v, want u2", uid, err)
	}
}

// Review focus 5: absent optional fields read back as zero values.
func emptyOptionalFieldsRoundTrip(t *testing.T, s ReadingStore) {
	Must(t, s.CreateUser(ctx, "u1", domain.User{NotionUserID: "n1", Name: "Tuan Do", CreatedAt: domain.Timestamp(TestNow)}))
	p, err := s.GetProfile(ctx, "u1")
	Must(t, err)
	if p.Email != "" || p.ActiveInstallationID != "" || p.DeletedAt != "" {
		t.Fatalf("user optional fields not empty: %+v", p)
	}

	Must(t, s.PutInstallation(ctx, "bot-1", &domain.Installation{ID: "bot-1", UserID: "u1", WorkspaceID: "ws", WorkspaceName: "WS",
		Status: domain.StatusNeedsSetup, AccessTokenEnc: "v1:a", RefreshTokenEnc: "v1:r", TokenVersion: 1,
		CreatedAt: domain.Timestamp(TestNow), UpdatedAt: domain.Timestamp(TestNow)}))
	inst, err := s.GetInstallation(ctx, "bot-1")
	Must(t, err)
	if inst.SetupError != "" || inst.TokenExpiresAt != "" || inst.WorkspaceIcon != "" || len(inst.Props) != 0 {
		t.Fatalf("installation optional fields not empty: %+v", inst)
	}

	Push(t, s, Segment("00000000-0000-4000-8000-000000000001", ArticleURL, 0, 60, 0, TestNow))
	a, err := s.GetArticle(ctx, UserID, domain.URLHash(ArticleURL))
	Must(t, err)
	if a.MaxProgress != 0 || a.NotionPageID != "" || a.NotionSyncedAt != "" {
		t.Fatalf("article optional fields not empty: %+v", a)
	}
}

// Spec §2.17: purging an installation removes only the installation; reading data
// belongs to the user, and purging the user leaves nothing readable behind.
func purgeRemovesInstallationAndUserData(t *testing.T, s ReadingStore) {
	Push(t, s, Segment("00000000-0000-4000-8000-000000000001", ArticleURL, 0, 60, 0.5, TestNow))
	Push(t, s, Segment("00000000-0000-4000-8000-000000000002", ArticleURL, 1, 60, 0.6, TestNow.Add(time.Minute)))
	Must(t, s.CreateUser(ctx, "u1", domain.User{NotionUserID: "n1", Name: "Tuan Do", CreatedAt: domain.Timestamp(TestNow)}))
	Must(t, s.CreateDevice(ctx, "u1", "d1", domain.Device{Name: "Mac", TokenHMAC: "h1", CreatedAt: domain.Timestamp(TestNow), LastSeenAt: domain.Timestamp(TestNow)}))
	Must(t, s.PutUserInstallation(ctx, "u1", "bot-1", TestNow))
	Must(t, s.PutInstallation(ctx, "bot-1", newInstallation()))

	Must(t, s.DeleteInstallationData(ctx, "bot-1"))
	if _, err := s.GetInstallation(ctx, "bot-1"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("GetInstallation after purge: %v", err)
	}
	if _, err := s.GetArticle(ctx, "u1", domain.URLHash(ArticleURL)); err != nil {
		t.Fatalf("GetArticle after installation purge: %v, want the user's article kept", err)
	}
	if _, err := s.GetDay(ctx, "u1", "2026-10-06"); err != nil {
		t.Fatalf("GetDay after installation purge: %v, want the user's day kept", err)
	}

	Must(t, s.DeleteUserData(ctx, "u1"))
	if _, err := s.GetArticle(ctx, "u1", domain.URLHash(ArticleURL)); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("GetArticle after user purge: %v", err)
	}
	if _, err := s.GetDay(ctx, "u1", "2026-10-06"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("GetDay after user purge: %v", err)
	}
	hashes, dates, err := s.PendingSync(ctx, "u1", true)
	Must(t, err)
	if len(hashes) != 0 || len(dates) != 0 {
		t.Fatalf("PendingSync after user purge: %v %v", hashes, dates)
	}
	Must(t, s.DeleteUserData(ctx, "u1"))
	if _, err := s.GetProfile(ctx, "u1"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("GetProfile after purge: %v", err)
	}
	if _, err := s.GetDevice(ctx, "u1", "d1"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("GetDevice after purge: %v", err)
	}
	ids, err := s.ListUserInstallationIDs(ctx, "u1")
	Must(t, err)
	devs, err := s.ListDevices(ctx, "u1")
	Must(t, err)
	if len(ids) != 0 || len(devs) != 0 {
		t.Fatalf("after purge: installations=%v devices=%+v", ids, devs)
	}
}

// T17: unknown hashes are absent from the result, duplicates in the input are harmless.
func batchGetArticlesReturnsOnlyExisting(t *testing.T, s ReadingStore) {
	Push(t, s, Segment("00000000-0000-4000-8000-000000000001", ArticleURL, 0, 60, 0.5, TestNow))
	unknown1, unknown2 := domain.URLHash("https://example.com/a"), domain.URLHash("https://example.com/b")
	got, err := s.BatchGetArticles(ctx, UserID, []string{domain.URLHash(ArticleURL), unknown1, unknown2, unknown1})
	Must(t, err)
	if len(got) != 1 || got[domain.URLHash(ArticleURL)] == nil {
		t.Fatalf("got %d entries: %+v", len(got), got)
	}
}

// readingIsPerUser: one user's reading is invisible to another.
func readingIsPerUser(t *testing.T, s ReadingStore) {
	Push(t, s, Segment("00000000-0000-4000-8000-000000000001", ArticleURL, 0, 60, 0.5, TestNow))
	h := domain.URLHash(ArticleURL)
	if _, err := s.GetArticle(ctx, "u2", h); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("GetArticle as u2: %v, want ErrNotFound", err)
	}
	got, err := s.BatchGetArticles(ctx, "u2", []string{h})
	Must(t, err)
	if len(got) != 0 {
		t.Fatalf("BatchGetArticles as u2 = %+v, want empty", got)
	}
	hashes, dates, err := s.PendingSync(ctx, "u2", true)
	Must(t, err)
	if len(hashes) != 0 || len(dates) != 0 {
		t.Fatalf("PendingSync as u2: %v %v, want empty", hashes, dates)
	}
}

// recentArticlesNewestFirst: RecentArticles lists one user's articles by last_read_at descending, capped by limit.
func recentArticlesNewestFirst(t *testing.T, s ReadingStore) {
	const (
		urlA = "https://medium.com/p/aaaaaaaaaaaa"
		urlB = "https://medium.com/p/bbbbbbbbbbbb"
		urlC = "https://medium.com/p/cccccccccccc"
	)
	// Each segment lasts 60 s and ends at the given offset from TestNow.
	endingAt := func(id, url string, offset time.Duration) domain.SegmentWrite {
		return Segment(id, url, 0, 60, 0.5, TestNow.Add(offset-time.Minute))
	}
	Push(t, s, endingAt("00000000-0000-4000-8000-0000000000a1", urlA, time.Minute))
	Push(t, s, endingAt("00000000-0000-4000-8000-0000000000b1", urlB, 3*time.Minute))
	Push(t, s, endingAt("00000000-0000-4000-8000-0000000000c1", urlC, 2*time.Minute))
	other := endingAt("00000000-0000-4000-8000-0000000000d1", ArticleURL, 10*time.Minute)
	other.UserID = "u2"
	Push(t, s, other)

	got, err := s.RecentArticles(ctx, UserID, 2)
	Must(t, err)
	if len(got) != 2 || got[0].CanonicalURL != urlB || got[1].CanonicalURL != urlC ||
		got[0].TotalSeconds != 60 || got[1].TotalSeconds != 60 {
		t.Fatalf("RecentArticles(u1, 2) = %+v, want B then C", got)
	}
	got, err = s.RecentArticles(ctx, UserID, 10)
	Must(t, err)
	if len(got) != 3 || got[0].CanonicalURL != urlB || got[1].CanonicalURL != urlC || got[2].CanonicalURL != urlA {
		t.Fatalf("RecentArticles(u1, 10) = %+v, want B, C, A", got)
	}
	got, err = s.RecentArticles(ctx, "nobody", 10)
	Must(t, err)
	if got == nil || len(got) != 0 {
		t.Fatalf("RecentArticles(nobody) = %#v, want empty non-nil slice", got)
	}
}
