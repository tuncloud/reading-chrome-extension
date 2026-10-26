package postgres

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"

	"reading-tracker/backend/internal/domain"
)

func (s *Store) GetProfile(ctx context.Context, uid string) (*domain.User, error) {
	var (
		email, active    *string
		created, deleted *time.Time
		u                = domain.User{ID: uid}
	)
	err := s.pool.QueryRow(ctx, `SELECT notion_user_id, name, email, active_installation_id, created_at, deleted_at
		FROM users WHERE id = $1`, uid).Scan(&u.NotionUserID, &u.Name, &email, &active, &created, &deleted)
	if err != nil {
		return nil, notFound(err)
	}
	u.Email, u.ActiveInstallationID = strOut(email), strOut(active)
	u.CreatedAt, u.DeletedAt = tsOut(created), tsOut(deleted)
	return &u, nil
}

// UserIDForNotionUser resolves a Notion user to our user (spec §2.2 step 4).
func (s *Store) UserIDForNotionUser(ctx context.Context, notionUserID string) (string, error) {
	var uid string
	err := s.pool.QueryRow(ctx, `SELECT user_id FROM notion_user_links WHERE notion_user_id = $1`, notionUserID).Scan(&uid)
	if err != nil {
		return "", notFound(err)
	}
	return uid, nil
}

// CreateUser inserts the user and points the Notion user at it, in one transaction.
func (s *Store) CreateUser(ctx context.Context, uid string, u domain.User) error {
	created, err := tsArg(u.CreatedAt)
	if err != nil {
		return err
	}
	deleted, err := tsArg(u.DeletedAt)
	if err != nil {
		return err
	}
	return s.tx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO users (id, notion_user_id, name, email, active_installation_id, created_at, deleted_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7)`,
			uid, u.NotionUserID, u.Name, strArg(u.Email), strArg(u.ActiveInstallationID), created, deleted); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO notion_user_links (notion_user_id, user_id) VALUES ($1, $2)
			ON CONFLICT (notion_user_id) DO UPDATE SET user_id = EXCLUDED.user_id`, u.NotionUserID, uid)
		return err
	})
}

func (s *Store) SetActiveInstallation(ctx context.Context, uid, botID string) error {
	return s.guarded(ctx, `UPDATE users SET active_installation_id = $2 WHERE id = $1`, uid, botID)
}

func (s *Store) MarkProfileDeleted(ctx context.Context, uid string, now time.Time) error {
	return s.guarded(ctx, `UPDATE users SET deleted_at = $2 WHERE id = $1`, uid, now)
}

// DeleteNotionUserLink removes the link only while it still points at uid.
func (s *Store) DeleteNotionUserLink(ctx context.Context, notionUserID, uid string) error {
	return s.exec(ctx, `DELETE FROM notion_user_links WHERE notion_user_id = $1 AND user_id = $2`, notionUserID, uid)
}

// CreateDevice inserts the device and its token lookup together (spec §2.3 step 4);
// a token that already exists fails the whole transaction.
func (s *Store) CreateDevice(ctx context.Context, uid, deviceID string, d domain.Device) error {
	var ts [3]any
	for i, v := range []string{d.CreatedAt, d.LastSeenAt, d.RevokedAt} {
		t, err := tsArg(v)
		if err != nil {
			return err
		}
		ts[i] = t
	}
	return s.tx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO devices (user_id, id, name, token_hmac, created_at, last_seen_at, revoked_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7)`, uid, deviceID, d.Name, d.TokenHMAC, ts[0], ts[1], ts[2]); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO device_tokens (token_hmac, user_id, device_id) VALUES ($1, $2, $3)`,
			d.TokenHMAC, uid, deviceID)
		return err
	})
}

func (s *Store) GetDeviceToken(ctx context.Context, tokenHMAC string) (*domain.DeviceToken, error) {
	var r domain.DeviceToken
	err := s.pool.QueryRow(ctx, `SELECT user_id, device_id FROM device_tokens WHERE token_hmac = $1`, tokenHMAC).
		Scan(&r.UserID, &r.DeviceID)
	if err != nil {
		return nil, notFound(err)
	}
	return &r, nil
}

const deviceCols = `user_id, id, name, token_hmac, created_at, last_seen_at, revoked_at`

func scanDevice(row pgx.Row) (domain.Device, error) {
	var (
		d                      domain.Device
		created, seen, revoked *time.Time
	)
	if err := row.Scan(&d.UserID, &d.ID, &d.Name, &d.TokenHMAC, &created, &seen, &revoked); err != nil {
		return d, err
	}
	d.CreatedAt, d.LastSeenAt, d.RevokedAt = tsOut(created), tsOut(seen), tsOut(revoked)
	return d, nil
}

func (s *Store) GetDevice(ctx context.Context, uid, deviceID string) (*domain.Device, error) {
	d, err := scanDevice(s.pool.QueryRow(ctx, `SELECT `+deviceCols+` FROM devices WHERE user_id = $1 AND id = $2`, uid, deviceID))
	if err != nil {
		return nil, notFound(err)
	}
	return &d, nil
}

// TouchDevice bumps last_seen_at only when it is over an hour old (spec §2.4 step 3).
func (s *Store) TouchDevice(ctx context.Context, uid, deviceID string, now time.Time) error {
	return s.exec(ctx, `UPDATE devices SET last_seen_at = $3 WHERE user_id = $1 AND id = $2
		AND (last_seen_at IS NULL OR last_seen_at < $3::timestamptz - interval '1 hour')`, uid, deviceID, now)
}

// RevokeDevice sets revoked_at and deletes the token lookup atomically (spec §2.17).
func (s *Store) RevokeDevice(ctx context.Context, uid, deviceID, tokenHMAC string, now time.Time) error {
	return s.tx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `UPDATE devices SET revoked_at = $3 WHERE user_id = $1 AND id = $2`, uid, deviceID, now); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `DELETE FROM device_tokens WHERE token_hmac = $1`, tokenHMAC)
		return err
	})
}

func (s *Store) ListDevices(ctx context.Context, uid string) ([]domain.Device, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+deviceCols+` FROM devices WHERE user_id = $1 ORDER BY id`, uid)
	if err != nil {
		return nil, tagged(err)
	}
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (domain.Device, error) { return scanDevice(r) })
	if err != nil {
		return nil, tagged(err)
	}
	return out, nil
}

func (s *Store) DeleteDeviceToken(ctx context.Context, tokenHMAC string) error {
	return s.exec(ctx, `DELETE FROM device_tokens WHERE token_hmac = $1`, tokenHMAC)
}
