// Package postgres is the relational repository: one table per DynamoDB item type
// (schema in platform/pgmigrate), same semantics as adapter/dynamo.
package postgres

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"reading-tracker/backend/internal/domain"
	"reading-tracker/backend/internal/usecase/port"
)

// Store reads and writes the reading database.
type Store struct {
	pool  *pgxpool.Pool
	sleep func(time.Duration)
}

// Option configures a Store.
type Option func(*Store)

// WithSleep replaces time.Sleep in retry back-offs (tests).
func WithSleep(f func(time.Duration)) Option { return func(s *Store) { s.sleep = f } }

func New(pool *pgxpool.Pool, opts ...Option) *Store {
	s := &Store{pool: pool, sleep: time.Sleep}
	for _, o := range opts {
		o(s)
	}
	return s
}

// exec runs one statement and tags its error.
func (s *Store) exec(ctx context.Context, sql string, args ...any) error {
	_, err := s.pool.Exec(ctx, sql, args...)
	return tagged(err)
}

// guarded runs one guarded UPDATE; 0 rows affected becomes domain.ErrConditionFailed.
func (s *Store) guarded(ctx context.Context, sql string, args ...any) error {
	tag, err := s.pool.Exec(ctx, sql, args...)
	if err != nil {
		return tagged(err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrConditionFailed
	}
	return nil
}

// tx runs f in one transaction.
func (s *Store) tx(ctx context.Context, f func(pgx.Tx) error) error {
	return tagged(pgx.BeginFunc(ctx, s.pool, f))
}

// tsArg turns a domain timestamp into a timestamptz parameter; "" is NULL.
func tsArg(v string) (any, error) {
	if v == "" {
		return nil, nil
	}
	return time.Parse(time.RFC3339, v)
}

// tsOut turns a nullable timestamptz into a domain timestamp; NULL is "".
func tsOut(t *time.Time) string {
	if t == nil {
		return ""
	}
	return domain.Timestamp(*t)
}

// strArg turns "" into NULL for optional text columns.
func strArg(v string) any {
	if v == "" {
		return nil
	}
	return v
}

// strOut turns a NULL text column into "".
func strOut(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

var (
	_ port.Users         = (*Store)(nil)
	_ port.Devices       = (*Store)(nil)
	_ port.OAuthGrants   = (*Store)(nil)
	_ port.Installations = (*Store)(nil)
	_ port.Reading       = (*Store)(nil)
	_ port.DataPurger    = (*Store)(nil)
)
