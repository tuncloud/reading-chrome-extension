// Package pgmigrate applies the embedded, forward-only SQL migrations.
package pgmigrate

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"sort"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed migrations/*.sql
var files embed.FS

// lockKey serialises concurrent Up calls across processes.
const lockKey = 727274

type migration struct {
	Name, Base string // embedded path and file name
	Version    int
}

// plan lists the migrations in fsys in version order. A non-numeric or duplicate
// version prefix is an error, so a clashing file fails loudly instead of being skipped.
func plan(fsys fs.FS) ([]migration, error) {
	names, err := fs.Glob(fsys, "migrations/*.sql")
	if err != nil {
		return nil, err
	}
	seen := map[int]string{}
	out := make([]migration, 0, len(names))
	for _, name := range names {
		base := strings.TrimPrefix(name, "migrations/")
		prefix, _, _ := strings.Cut(base, "_")
		version, err := strconv.Atoi(prefix)
		if err != nil {
			return nil, fmt.Errorf("pgmigrate: %s: non-numeric version prefix", base)
		}
		if prev, dup := seen[version]; dup {
			return nil, fmt.Errorf("pgmigrate: %s and %s share version %d", prev, base, version)
		}
		seen[version] = base
		out = append(out, migration{Name: name, Base: base, Version: version})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Version < out[j].Version })
	return out, nil
}

// Up applies every embedded migration not yet recorded in schema_migrations.
// Each file runs in its own transaction together with its version insert.
func Up(ctx context.Context, pool *pgxpool.Pool) (err error) {
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("pgmigrate: acquire: %w", err)
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx, "SELECT pg_advisory_lock($1)", lockKey); err != nil {
		return fmt.Errorf("pgmigrate: lock: %w", err)
	}
	defer func() {
		if _, uerr := conn.Exec(context.WithoutCancel(ctx), "SELECT pg_advisory_unlock($1)", lockKey); uerr != nil && err == nil {
			err = fmt.Errorf("pgmigrate: unlock: %w", uerr)
		}
	}()
	if _, err := conn.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version int PRIMARY KEY, applied_at timestamptz NOT NULL DEFAULT now())`); err != nil {
		return fmt.Errorf("pgmigrate: schema_migrations: %w", err)
	}
	migrations, err := plan(files)
	if err != nil {
		return err
	}
	for _, mg := range migrations {
		name, base, version := mg.Name, mg.Base, mg.Version
		var applied bool
		if err := conn.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM schema_migrations WHERE version = $1)", version).Scan(&applied); err != nil {
			return fmt.Errorf("pgmigrate: check %s: %w", base, err)
		}
		if applied {
			continue
		}
		sql, err := files.ReadFile(name)
		if err != nil {
			return err
		}
		tx, err := conn.Begin(ctx)
		if err != nil {
			return fmt.Errorf("pgmigrate: begin %s: %w", base, err)
		}
		if _, err := tx.Exec(ctx, string(sql)); err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("pgmigrate: apply %s: %w", base, err)
		}
		if _, err := tx.Exec(ctx, "INSERT INTO schema_migrations (version) VALUES ($1)", version); err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("pgmigrate: record %s: %w", base, err)
		}
		if err := tx.Commit(ctx); err != nil {
			return fmt.Errorf("pgmigrate: commit %s: %w", base, err)
		}
	}
	return nil
}
