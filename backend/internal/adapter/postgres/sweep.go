package postgres

import (
	"context"
	"time"
)

// Sweep deletes expired segments, OAuth states and auth codes (DynamoDB's TTL job).
func (s *Store) Sweep(ctx context.Context, now time.Time) error {
	for _, sql := range []string{
		`DELETE FROM segments WHERE expires_at < $1`,
		`DELETE FROM oauth_states WHERE expires_at < $1`,
		`DELETE FROM auth_codes WHERE expires_at < $1`,
	} {
		if err := s.exec(ctx, sql, now); err != nil {
			return err
		}
	}
	return nil
}
