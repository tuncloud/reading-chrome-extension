package pgmigrate_test

import (
	"context"
	"fmt"
	"io/fs"
	"math/rand/v2"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"testing/fstest"

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
	assertVersions(t, env.Pool, 2)
}

func TestConcurrentUpIsSafe(t *testing.T) {
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
	assertVersions(t, pool, 2)
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
