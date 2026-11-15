package pgmigrate_test

import (
	"context"
	"fmt"
	"io/fs"
	"math/rand/v2"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"reading-tracker/backend/internal/platform/pgmigrate"
	"reading-tracker/backend/internal/platform/pgtest"
)

var tables = []string{
	"users", "notion_user_links", "user_installations", "devices", "device_tokens", "oauth_states",
	"auth_codes", "installations", "articles", "segments", "days", "sync_jobs",
}

func TestUpCreatesEveryTable(t *testing.T) {
	env := pgtest.New(t)
	for _, tbl := range tables {
		var ok bool
		if err := env.Pool.QueryRow(context.Background(), "SELECT to_regclass($1) IS NOT NULL", tbl).Scan(&ok); err != nil {
			t.Fatal(err)
		}
		if !ok {
			t.Errorf("table %s missing", tbl)
		}
	}
}

func TestUpIsIdempotent(t *testing.T) {
	env := pgtest.New(t)
	ctx := context.Background()
	for range 2 {
		if err := pgmigrate.Up(ctx, env.Pool); err != nil {
			t.Fatal(err)
		}
	}
	assertVersions(t, env.Pool, 3)
}

func TestConcurrentUpIsSafe(t *testing.T) {
	ctx := context.Background()
	pool := bareDB(t)

	var wg sync.WaitGroup
	errs := make([]error, 4)
	for i := range errs {
		wg.Go(func() { errs[i] = pgmigrate.Up(ctx, pool) })
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Errorf("Up #%d: %v", i, err)
		}
	}
	assertVersions(t, pool, 3)
}

// Final review M3: two files sharing a prefix would silently skip the second one.
func TestMigrationPrefixesAreUnique(t *testing.T) {
	names, err := fs.Glob(pgmigrate.Files, "migrations/*.sql")
	if err != nil {
		t.Fatal(err)
	}
	seen := map[int]string{}
	for _, name := range names {
		base := strings.TrimPrefix(name, "migrations/")
		prefix, _, _ := strings.Cut(base, "_")
		v, err := strconv.Atoi(prefix)
		if err != nil {
			t.Fatalf("%s: non-numeric prefix", base)
		}
		if prev, dup := seen[v]; dup {
			t.Fatalf("%s and %s share version %d", prev, base, v)
		}
		seen[v] = base
	}
	if len(seen) == 0 {
		t.Fatal("no embedded migrations")
	}
}

func TestPlanRejectsDuplicatePrefix(t *testing.T) {
	fsys := fstest.MapFS{
		"migrations/0001_a.sql":  {Data: []byte("SELECT 1")},
		"migrations/0002_b.sql":  {Data: []byte("SELECT 1")},
		"migrations/002_dup.sql": {Data: []byte("SELECT 1")},
	}
	if _, err := pgmigrate.Plan(fsys); err == nil || !strings.Contains(err.Error(), "share version 2") {
		t.Fatalf("Plan = %v, want a duplicate-version error", err)
	}
	ok := fstest.MapFS{
		"migrations/0002_b.sql": {Data: []byte("SELECT 1")},
		"migrations/0001_a.sql": {Data: []byte("SELECT 1")},
	}
	got, err := pgmigrate.Plan(ok)
	if err != nil || len(got) != 2 || got[0].Version != 1 || got[1].Version != 2 {
		t.Fatalf("Plan = %+v, %v; want versions 1, 2 in order", got, err)
	}
}

// bareDB creates an empty database, with no migration applied, dropped when the test ends.
func bareDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	env := pgtest.New(t)
	ctx := context.Background()
	name := fmt.Sprintf("reading_bare_%d", rand.Int64())
	if _, err := env.Pool.Exec(ctx, "CREATE DATABASE "+name+" TEMPLATE template0"); err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(env.URL)
	if err != nil {
		t.Fatal(err)
	}
	u.Path = "/" + name
	pool, err := pgxpool.New(ctx, u.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		pool.Close()
		_, _ = env.Pool.Exec(context.Background(), "DROP DATABASE IF EXISTS "+name+" WITH (FORCE)")
	})
	return pool
}

func assertVersions(t *testing.T, pool *pgxpool.Pool, want int) {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), "SELECT count(*) FROM schema_migrations").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != want {
		t.Fatalf("schema_migrations rows = %d, want %d", n, want)
	}
}

// readingFixture is the reading data of four users' installations before 0003 (all
// times on 2026-10-06 UTC). u1 has bot-a and the active bot-b, u2 the active bot-c,
// u3 bot-d but no active installation; bot-gone no longer exists.
const readingFixture = `
INSERT INTO users (id, notion_user_id, name, active_installation_id, created_at) VALUES
  ('u1', 'n1', 'One', 'bot-b', '2026-10-01T00:00:00Z'),
  ('u2', 'n2', 'Two', 'bot-c', '2026-10-01T00:00:00Z'),
  ('u3', 'n3', 'Three', NULL, '2026-10-01T00:00:00Z');
INSERT INTO installations (id, user_id, workspace_id, workspace_name, status, access_token_enc,
  refresh_token_enc, token_version, created_at, updated_at)
SELECT b, u, 'ws-' || b, 'WS ' || b, 'active', 'x', 'x', 1, '2026-10-01T00:00:00Z', '2026-10-01T00:00:00Z'
FROM (VALUES ('bot-a', 'u1'), ('bot-b', 'u1'), ('bot-c', 'u2'), ('bot-d', 'u3')) AS v(b, u);
INSERT INTO articles (installation_id, url_hash, canonical_url, source, title, author, site_name, word_count,
  published_at, first_read_at, last_read_at, total_seconds, sessions, max_progress, completed, version,
  notion_page_id, notion_url, notion_ds_id, notion_synced_version, notion_synced_at) VALUES
  ('bot-a', 'h1', 'https://x/1', 'web', 'title-a', 'au', 'site', 10, '', '2026-10-06T10:00:00Z', '2026-10-06T11:00:00Z',
    100, 2, 0.4, false, 3, 'pa', 'url-pa', 'ds-a', 3, '2026-10-06T11:30:00Z'),
  ('bot-b', 'h1', 'https://x/1', 'web', 'title-b', 'au', 'site', 10, '', '2026-10-06T09:00:00Z', '2026-10-06T12:00:00Z',
    50, 1, 0.9, true, 2, 'pb', 'url-pb', 'ds-b', 2, '2026-10-06T12:30:00Z'),
  ('bot-a', 'h2', 'https://x/2', 'web', 'title-a2', 'au', 'site', 10, '', '2026-10-06T10:00:00Z', '2026-10-06T10:30:00Z',
    30, 1, 0.5, false, 1, 'pa2', 'url-pa2', 'ds-a', 1, '2026-10-06T10:45:00Z'),
  ('bot-c', 'h1', 'https://x/1', 'web', 'title-c', 'au', 'site', 10, '', '2026-10-06T10:00:00Z', '2026-10-06T10:30:00Z',
    7, 1, 0.1, false, 1, 'pc', 'url-pc', 'ds-c', 1, '2026-10-06T10:45:00Z'),
  ('bot-d', 'h3', 'https://x/3', 'web', 'title-d', 'au', 'site', 10, '', '2026-10-06T10:00:00Z', '2026-10-06T10:30:00Z',
    9, 1, 0.1, false, 1, 'pd', 'url-pd', NULL, 1, '2026-10-06T10:45:00Z'),
  ('bot-gone', 'h9', 'https://x/9', 'web', 'title-g', 'au', 'site', 10, '', '2026-10-06T10:00:00Z', '2026-10-06T10:30:00Z',
    1, 1, 0.1, false, 1, NULL, NULL, NULL, NULL, NULL);
INSERT INTO days (installation_id, date, seconds, article_hashes, version, notion_page_id, notion_ds_id,
  notion_synced_version) VALUES
  ('bot-a', '2026-10-06', 100, '{h1,h2}', 2, 'dpa', NULL, 2),
  ('bot-b', '2026-10-06', 50, '{h1}', 1, 'dpb', 'dds-b', 1),
  ('bot-gone', '2026-10-06', 1, '{h9}', 1, NULL, NULL, NULL);
INSERT INTO segments (installation_id, id, session_id, seq, url_hash, device_id, started_at, ended_at,
  active_seconds, max_progress, expires_at)
SELECT b, s, 'sess', 0, 'h1', 'd1', '2026-10-06T10:00:00Z', '2026-10-06T10:01:00Z', 60, 0.5, '2027-01-04T10:00:00Z'
FROM (VALUES ('bot-a', 's1'), ('bot-b', 's1'), ('bot-a', 's2'), ('bot-gone', 's3')) AS v(b, s);
`

type migratedArticle struct {
	Title                         string
	Total, Sessions, Version      int64
	MaxProgress                   *float64
	FirstRead, LastRead, SyncedAt *time.Time
	Completed                     bool
	PageID, NotionURL, DataSource *string
	SyncedVersion                 *int64
}

func TestReadingByUserMigration(t *testing.T) {
	ctx := context.Background()
	pool := bareDB(t)
	if err := pgmigrate.UpThrough(ctx, pool, 2); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, readingFixture); err != nil {
		t.Fatalf("fixture: %v", err)
	}
	if err := pgmigrate.Up(ctx, pool); err != nil {
		t.Fatal(err)
	}

	article := func(uid, hash string) migratedArticle {
		t.Helper()
		var a migratedArticle
		if err := pool.QueryRow(ctx, `SELECT title, total_seconds, sessions, version, max_progress, first_read_at,
			last_read_at, notion_synced_at, completed, notion_page_id, notion_url, notion_ds_id, notion_synced_version
			FROM articles WHERE user_id = $1 AND url_hash = $2`, uid, hash).
			Scan(&a.Title, &a.Total, &a.Sessions, &a.Version, &a.MaxProgress, &a.FirstRead, &a.LastRead, &a.SyncedAt,
				&a.Completed, &a.PageID, &a.NotionURL, &a.DataSource, &a.SyncedVersion); err != nil {
			t.Fatalf("article (%s, %s): %v", uid, hash, err)
		}
		return a
	}
	at := func(clock string) time.Time {
		v, err := time.Parse(time.RFC3339, "2026-10-06T"+clock+":00Z")
		if err != nil {
			t.Fatal(err)
		}
		return v
	}

	a := article("u1", "h1")
	if a.Total != 150 || a.Sessions != 3 || a.MaxProgress == nil || *a.MaxProgress != 0.9 || !a.Completed || a.Version != 5 {
		t.Errorf("article (u1, h1) counters = %+v; want total 150, sessions 3, max_progress 0.9, completed, version 5", a)
	}
	if a.FirstRead == nil || !a.FirstRead.Equal(at("09:00")) || a.LastRead == nil || !a.LastRead.Equal(at("12:00")) {
		t.Errorf("article (u1, h1) first/last = %v/%v; want 09:00/12:00", a.FirstRead, a.LastRead)
	}
	if !eqStr(a.PageID, "pb") || !eqStr(a.NotionURL, "url-pb") || !eqStr(a.DataSource, "ds-b") ||
		a.SyncedVersion == nil || *a.SyncedVersion != 2 || a.SyncedAt == nil || !a.SyncedAt.Equal(at("12:30")) {
		t.Errorf("article (u1, h1) Notion = %+v; want the active bot-b's pb, url-pb, ds-b, 2, 12:30", a)
	}
	if a.Title != "title-b" {
		t.Errorf("article (u1, h1) title = %q; want the earliest-read row's title-b", a.Title)
	}

	a = article("u1", "h2")
	if a.Total != 30 || a.Version != 1 || a.PageID != nil || a.NotionURL != nil || a.DataSource != nil ||
		a.SyncedVersion != nil || a.SyncedAt != nil {
		t.Errorf("article (u1, h2) = %+v; want total 30, version 1 and no Notion state", a)
	}

	a = article("u2", "h1")
	if a.Total != 7 || a.Version != 1 || !eqStr(a.PageID, "pc") || !eqStr(a.DataSource, "ds-c") ||
		a.SyncedVersion == nil || *a.SyncedVersion != 1 {
		t.Errorf("article (u2, h1) = %+v; want total 7, version 1, page pc, ds ds-c, synced 1", a)
	}

	a = article("u3", "h3")
	if a.Total != 9 || a.PageID != nil || a.SyncedVersion != nil {
		t.Errorf("article (u3, h3) = %+v; want total 9 and no Notion state", a)
	}

	if n := count(t, pool, `SELECT count(*) FROM articles`); n != 4 {
		t.Errorf("articles = %d rows, want 4", n)
	}
	if n := count(t, pool, `SELECT count(*) FROM articles WHERE url_hash = 'h9'`); n != 0 {
		t.Errorf("articles with h9 = %d, want 0", n)
	}

	var (
		seconds, version int64
		hashes           []string
		dayPage, dayDS   *string
		daySynced        *int64
	)
	if err := pool.QueryRow(ctx, `SELECT seconds, article_hashes, version, notion_page_id, notion_ds_id,
		notion_synced_version FROM days WHERE user_id = 'u1' AND date = '2026-10-06'`).
		Scan(&seconds, &hashes, &version, &dayPage, &dayDS, &daySynced); err != nil {
		t.Fatalf("day (u1, 2026-10-06): %v", err)
	}
	slices.Sort(hashes)
	if seconds != 150 || !slices.Equal(hashes, []string{"h1", "h2"}) || version != 3 {
		t.Errorf("day (u1) = seconds %d, hashes %v, version %d; want 150, [h1 h2], 3", seconds, hashes, version)
	}
	if !eqStr(dayPage, "dpb") || !eqStr(dayDS, "dds-b") || daySynced == nil || *daySynced != 1 {
		t.Errorf("day (u1) Notion = %v, %v, %v; want dpb, dds-b, 1", dayPage, dayDS, daySynced)
	}
	if n := count(t, pool, `SELECT count(*) FROM days`); n != 1 {
		t.Errorf("days = %d rows, want 1", n)
	}

	for _, id := range []string{"s1", "s2"} {
		if n := count(t, pool, `SELECT count(*) FROM segments WHERE user_id = 'u1' AND id = '`+id+`'`); n != 1 {
			t.Errorf("segment (u1, %s) = %d rows, want 1", id, n)
		}
	}
	if n := count(t, pool, `SELECT count(*) FROM segments`); n != 2 {
		t.Errorf("segments = %d rows, want 2", n)
	}

	if n := count(t, pool, `SELECT count(*) FROM information_schema.columns
		WHERE table_name IN ('articles', 'segments', 'days') AND column_name = 'installation_id'`); n != 0 {
		t.Errorf("%d reading tables still have installation_id", n)
	}
	for _, idx := range []string{"articles_user_last_read", "segments_expiry"} {
		if n := count(t, pool, `SELECT count(*) FROM pg_indexes WHERE indexname = $1`, idx); n != 1 {
			t.Errorf("index %s missing", idx)
		}
	}
}

func eqStr(p *string, want string) bool { return p != nil && *p == want }

func count(t *testing.T, pool *pgxpool.Pool, sql string, args ...any) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), sql, args...).Scan(&n); err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
	return n
}
