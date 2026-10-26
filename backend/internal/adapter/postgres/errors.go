package postgres

import (
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"reading-tracker/backend/internal/domain"
)

// throttledError marks Postgres capacity errors so callers outside this adapter can
// recognise them with errors.Is(err, domain.ErrThrottled) (spec §2.0 → 503), while
// errors.As still reaches the *pgconn.PgError.
type throttledError struct{ err error }

func (e throttledError) Error() string   { return e.err.Error() }
func (e throttledError) Unwrap() []error { return []error{domain.ErrThrottled, e.err} }

// tagged wraps serialization failures, deadlocks and connection exhaustion as throttled.
func tagged(err error) error {
	var pe *pgconn.PgError
	if errors.As(err, &pe) {
		switch pe.Code {
		case "40001", "40P01", "53300", "57P03":
			return throttledError{err}
		}
	}
	return err
}

// notFound maps pgx.ErrNoRows to domain.ErrNotFound and tags everything else.
func notFound(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ErrNotFound
	}
	return tagged(err)
}
