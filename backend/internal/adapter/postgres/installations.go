package postgres

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"

	"reading-tracker/backend/internal/domain"
)

func (s *Store) GetInstallation(ctx context.Context, botID string) (*domain.Installation, error) {
	var (
		i                                      = domain.Installation{ID: botID}
		icon, setupErr, expires, tmpl          *string
		libDB, libDS, libURL, dailyDB, dailyDS *string
		created, updated                       time.Time
	)
	err := s.pool.QueryRow(ctx, `SELECT user_id, workspace_id, workspace_name, workspace_icon, status, setup_error,
		access_token_enc, refresh_token_enc, token_expires_at, token_version, duplicated_template_id,
		library_db_id, library_ds_id, library_url, daily_db_id, daily_ds_id, props, created_at, updated_at
		FROM installations WHERE id = $1`, botID).Scan(&i.UserID, &i.WorkspaceID, &i.WorkspaceName, &icon, &i.Status, &setupErr,
		&i.AccessTokenEnc, &i.RefreshTokenEnc, &expires, &i.TokenVersion, &tmpl,
		&libDB, &libDS, &libURL, &dailyDB, &dailyDS, &i.Props, &created, &updated)
	if err != nil {
		return nil, notFound(err)
	}
	i.WorkspaceIcon, i.SetupError, i.TokenExpiresAt, i.DuplicatedTemplateID = strOut(icon), strOut(setupErr), strOut(expires), strOut(tmpl)
	i.LibraryDBID, i.LibraryDSID, i.LibraryURL = strOut(libDB), strOut(libDS), strOut(libURL)
	i.DailyDBID, i.DailyDSID = strOut(dailyDB), strOut(dailyDS)
	i.CreatedAt, i.UpdatedAt = domain.Timestamp(created), domain.Timestamp(updated)
	return &i, nil
}

// PutInstallation writes the whole row (used by the OAuth callback upsert).
func (s *Store) PutInstallation(ctx context.Context, botID string, inst *domain.Installation) error {
	created, err := tsArg(inst.CreatedAt)
	if err != nil {
		return err
	}
	updated, err := tsArg(inst.UpdatedAt)
	if err != nil {
		return err
	}
	return s.exec(ctx, `INSERT INTO installations (id, user_id, workspace_id, workspace_name, workspace_icon, status,
		setup_error, access_token_enc, refresh_token_enc, token_expires_at, token_version, duplicated_template_id,
		library_db_id, library_ds_id, library_url, daily_db_id, daily_ds_id, props, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20)
		ON CONFLICT (id) DO UPDATE SET user_id = EXCLUDED.user_id, workspace_id = EXCLUDED.workspace_id,
		workspace_name = EXCLUDED.workspace_name, workspace_icon = EXCLUDED.workspace_icon, status = EXCLUDED.status,
		setup_error = EXCLUDED.setup_error, access_token_enc = EXCLUDED.access_token_enc,
		refresh_token_enc = EXCLUDED.refresh_token_enc, token_expires_at = EXCLUDED.token_expires_at,
		token_version = EXCLUDED.token_version, duplicated_template_id = EXCLUDED.duplicated_template_id,
		library_db_id = EXCLUDED.library_db_id, library_ds_id = EXCLUDED.library_ds_id, library_url = EXCLUDED.library_url,
		daily_db_id = EXCLUDED.daily_db_id, daily_ds_id = EXCLUDED.daily_ds_id, props = EXCLUDED.props,
		created_at = EXCLUDED.created_at, updated_at = EXCLUDED.updated_at`,
		botID, inst.UserID, inst.WorkspaceID, inst.WorkspaceName, strArg(inst.WorkspaceIcon), inst.Status,
		strArg(inst.SetupError), inst.AccessTokenEnc, inst.RefreshTokenEnc, strArg(inst.TokenExpiresAt), inst.TokenVersion,
		strArg(inst.DuplicatedTemplateID), strArg(inst.LibraryDBID), strArg(inst.LibraryDSID), strArg(inst.LibraryURL),
		strArg(inst.DailyDBID), strArg(inst.DailyDSID), inst.Props, created, updated)
}

// PutUserInstallation records the user → installation pointer, keeping the first created_at.
func (s *Store) PutUserInstallation(ctx context.Context, uid, botID string, now time.Time) error {
	return s.exec(ctx, `INSERT INTO user_installations (user_id, installation_id, created_at) VALUES ($1, $2, $3)
		ON CONFLICT DO NOTHING`, uid, botID, now)
}

func (s *Store) ListUserInstallationIDs(ctx context.Context, uid string) ([]string, error) {
	rows, err := s.pool.Query(ctx, `SELECT installation_id FROM user_installations WHERE user_id = $1
		ORDER BY installation_id`, uid)
	if err != nil {
		return nil, tagged(err)
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, tagged(err)
	}
	return ids, nil
}

// SetInstallationStatus sets status; an empty setupError clears setup_error.
func (s *Store) SetInstallationStatus(ctx context.Context, botID, status, setupError string, now time.Time) error {
	return s.guarded(ctx, `UPDATE installations SET status = $2, setup_error = $3, updated_at = $4 WHERE id = $1`,
		botID, status, strArg(setupError), now)
}

// SaveInstallationDatabases stores the resolved Notion IDs and marks the installation active.
func (s *Store) SaveInstallationDatabases(ctx context.Context, botID string, db domain.DBConfig, now time.Time) error {
	return s.guarded(ctx, `UPDATE installations SET library_db_id = $2, library_ds_id = $3, library_url = $4,
		daily_db_id = $5, daily_ds_id = $6, props = $7, status = $8, setup_error = NULL, updated_at = $9 WHERE id = $1`,
		botID, db.LibraryDBID, db.LibraryDSID, db.LibraryURL, db.DailyDBID, db.DailyDSID, db.Props, domain.StatusActive, now)
}

// UpdateInstallationTokens persists both rotated tokens, guarded by token_version (C7).
func (s *Store) UpdateInstallationTokens(ctx context.Context, botID, accessEnc, refreshEnc string, oldVersion int, now time.Time) error {
	return s.guarded(ctx, `UPDATE installations SET access_token_enc = $2, refresh_token_enc = $3,
		token_version = $4 + 1, updated_at = $5 WHERE id = $1 AND token_version = $4`,
		botID, accessEnc, refreshEnc, oldVersion, now)
}
