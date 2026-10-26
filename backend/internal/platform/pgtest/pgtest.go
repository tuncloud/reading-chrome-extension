// Package pgtest provisions throwaway, fully migrated databases on local Postgres (`make pg-up`).
package pgtest

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"net/url"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"reading-tracker/backend/internal/platform/pgmigrate"
)

// Env is one fresh database plus a pool bound to it.
type Env struct {
	Pool *pgxpool.Pool
	URL  string
}

const template = "reading_tpl"

// tplLock guards reading_tpl across test binaries: rebuilding takes it
// exclusively, cloning takes it shared.
const tplLock = 727275

var (
	once     sync.Once
	admin    *pgxpool.Pool
	setupErr error
)

// URL is PG_TEST_URL or the `make pg-up` default.
func URL() string {
	if v := os.Getenv("PG_TEST_URL"); v != "" {
		return v
	}
	return "postgres://reading:reading@localhost:5432/reading?sslmode=disable"
}

// New creates a database cloned from the migrated template, dropped when the
// test ends. It fails the test (never skips) when Postgres is unreachable.
func New(t testing.TB) *Env {
	t.Helper()
	once.Do(func() { setupErr = setup() })
	if setupErr != nil {
		t.Fatal(setupErr)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	name := fmt.Sprintf("reading_test_%d", rand.Int64())
	if err := withLock(ctx, "pg_advisory_lock_shared", "pg_advisory_unlock_shared",
		"CREATE DATABASE "+name+" TEMPLATE "+template); err != nil {
		t.Fatalf("create %s: %v", name, err)
	}
	t.Cleanup(func() {
		_, _ = admin.Exec(context.Background(), "DROP DATABASE IF EXISTS "+name+" WITH (FORCE)")
	})
	dbURL, err := withDB(URL(), name)
	if err != nil {
		t.Fatal(err)
	}
	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		t.Fatalf("open %s: %v", name, err)
	}
	t.Cleanup(pool.Close)
	return &Env{Pool: pool, URL: dbURL}
}

// setup opens the admin pool and rebuilds the template so a stale schema never survives.
func setup() error {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, URL())
	if err == nil {
		err = pool.Ping(ctx)
	}
	if err != nil {
		return fmt.Errorf("Postgres not reachable at %s (run `make pg-up`): %w", URL(), err)
	}
	admin = pool
	return withLock(ctx, "pg_advisory_lock", "pg_advisory_unlock",
		"DROP DATABASE IF EXISTS "+template+" WITH (FORCE)",
		"CREATE DATABASE "+template,
		migrate)
}

// migrate runs pgmigrate.Up on the template and closes its pool, since
// cloning requires no open connections to it.
func migrate(ctx context.Context) error {
	tplURL, err := withDB(URL(), template)
	if err != nil {
		return err
	}
	pool, err := pgxpool.New(ctx, tplURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	return pgmigrate.Up(ctx, pool)
}

// withLock runs steps (SQL strings or funcs) on one admin connection while
// holding tplLock via the given lock/unlock functions.
func withLock(ctx context.Context, lock, unlock string, steps ...any) (err error) {
	conn, err := admin.Acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx, "SELECT "+lock+"($1)", tplLock); err != nil {
		return err
	}
	defer func() {
		_, uerr := conn.Exec(context.WithoutCancel(ctx), "SELECT "+unlock+"($1)", tplLock)
		err = errors.Join(err, uerr)
	}()
	for _, s := range steps {
		switch s := s.(type) {
		case string:
			_, err = conn.Exec(ctx, s)
		case func(context.Context) error:
			err = s(ctx)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func withDB(raw, db string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", err
	}
	u.Path = "/" + db
	return u.String(), nil
}
