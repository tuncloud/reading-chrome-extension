// Package postgrestest provisions a fresh migrated database and a Store bound to it
// (needs local Postgres, `make pg-up`).
package postgrestest

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"reading-tracker/backend/internal/adapter/postgres"
	"reading-tracker/backend/internal/platform/pgtest"
)

// Env is one fresh database plus a Store bound to it.
type Env struct {
	Store *postgres.Store
	Pool  *pgxpool.Pool
	URL   string
}

// New creates a fresh migrated database (dropped when the test ends) and a Store built with opts.
func New(t testing.TB, opts ...postgres.Option) *Env {
	t.Helper()
	e := pgtest.New(t)
	return &Env{Store: postgres.New(e.Pool, opts...), Pool: e.Pool, URL: e.URL}
}

// Count returns SELECT count(*) FROM table WHERE where; where may be "true".
func (e *Env) Count(t testing.TB, table, where string, args ...any) int {
	t.Helper()
	var n int
	if err := e.Pool.QueryRow(context.Background(), "SELECT count(*) FROM "+table+" WHERE "+where, args...).Scan(&n); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return n
}

// Dump returns every row of every table in schema public as its row_to_json text.
func (e *Env) Dump(t testing.TB) []string {
	t.Helper()
	ctx := context.Background()
	rows, err := e.Pool.Query(ctx, `SELECT table_name FROM information_schema.tables WHERE table_schema = 'public'`)
	if err != nil {
		t.Fatalf("list tables: %v", err)
	}
	tables, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		t.Fatalf("list tables: %v", err)
	}
	var out []string
	for _, tbl := range tables {
		rs, err := e.Pool.Query(ctx, `SELECT row_to_json(x)::text FROM public."`+tbl+`" x`)
		if err != nil {
			t.Fatalf("dump %s: %v", tbl, err)
		}
		lines, err := pgx.CollectRows(rs, pgx.RowTo[string])
		if err != nil {
			t.Fatalf("dump %s: %v", tbl, err)
		}
		out = append(out, lines...)
	}
	return out
}
