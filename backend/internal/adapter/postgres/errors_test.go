package postgres

import (
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"

	"reading-tracker/backend/internal/domain"
)

func TestTaggedMapsCapacityErrors(t *testing.T) {
	for _, c := range []struct {
		name      string
		err       error
		throttled bool
	}{
		{"serialization_failure", &pgconn.PgError{Code: "40001"}, true},
		{"deadlock_detected", &pgconn.PgError{Code: "40P01"}, true},
		{"too_many_connections", &pgconn.PgError{Code: "53300"}, true},
		{"cannot_connect_now", &pgconn.PgError{Code: "57P03"}, true},
		{"unique_violation", &pgconn.PgError{Code: "23505"}, false},
		{"nil", nil, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			err := tagged(c.err)
			if got := errors.Is(err, domain.ErrThrottled); got != c.throttled {
				t.Fatalf("errors.Is(ErrThrottled) = %v, want %v", got, c.throttled)
			}
			if c.err == nil {
				if err != nil {
					t.Fatalf("tagged(nil) = %v", err)
				}
				return
			}
			var pe *pgconn.PgError
			if !errors.As(err, &pe) || pe != c.err {
				t.Fatalf("original *pgconn.PgError lost: %v", err)
			}
		})
	}
}
