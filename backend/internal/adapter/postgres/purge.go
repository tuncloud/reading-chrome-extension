package postgres

import (
	"context"

	"github.com/jackc/pgx/v5"
)

// DeleteInstallationData removes an installation and its reading data (spec §2.17).
func (s *Store) DeleteInstallationData(ctx context.Context, botID string) error {
	return s.deleteAll(ctx, botID,
		`DELETE FROM segments WHERE installation_id = $1`,
		`DELETE FROM articles WHERE installation_id = $1`,
		`DELETE FROM days WHERE installation_id = $1`,
		`DELETE FROM installations WHERE id = $1`)
}

// DeleteUserData removes a user's profile, devices and installation pointers. Device
// tokens and the Notion user link are removed by the purge use case itself.
func (s *Store) DeleteUserData(ctx context.Context, uid string) error {
	return s.deleteAll(ctx, uid,
		`DELETE FROM devices WHERE user_id = $1`,
		`DELETE FROM user_installations WHERE user_id = $1`,
		`DELETE FROM users WHERE id = $1`)
}

// deleteAll runs each statement with id in one transaction.
func (s *Store) deleteAll(ctx context.Context, id string, stmts ...string) error {
	return s.tx(ctx, func(tx pgx.Tx) error {
		for _, sql := range stmts {
			if _, err := tx.Exec(ctx, sql, id); err != nil {
				return err
			}
		}
		return nil
	})
}
