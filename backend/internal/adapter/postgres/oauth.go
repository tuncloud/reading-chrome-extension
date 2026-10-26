package postgres

import (
	"context"
	"time"

	"reading-tracker/backend/internal/domain"
)

func (s *Store) PutOAuthState(ctx context.Context, state string, st domain.OAuthState, expires time.Time) error {
	return s.exec(ctx, `INSERT INTO oauth_states (state, ext_redirect, code_challenge, expires_at) VALUES ($1, $2, $3, $4)
		ON CONFLICT (state) DO UPDATE SET ext_redirect = EXCLUDED.ext_redirect, code_challenge = EXCLUDED.code_challenge,
		expires_at = EXCLUDED.expires_at`, state, st.ExtRedirect, st.CodeChallenge, expires)
}

// ConsumeOAuthState deletes and returns a live state; a missing or expired one is
// domain.ErrNotFound (C12).
func (s *Store) ConsumeOAuthState(ctx context.Context, state string, now time.Time) (*domain.OAuthState, error) {
	var st domain.OAuthState
	err := s.pool.QueryRow(ctx, `DELETE FROM oauth_states WHERE state = $1 AND expires_at > $2
		RETURNING ext_redirect, code_challenge`, state, now).Scan(&st.ExtRedirect, &st.CodeChallenge)
	if err != nil {
		return nil, notFound(err)
	}
	return &st, nil
}

func (s *Store) PutAuthCode(ctx context.Context, code string, ac domain.AuthCode, expires time.Time) error {
	return s.exec(ctx, `INSERT INTO auth_codes (code, user_id, installation_id, code_challenge, ext_redirect, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (code) DO UPDATE SET user_id = EXCLUDED.user_id, installation_id = EXCLUDED.installation_id,
		code_challenge = EXCLUDED.code_challenge, ext_redirect = EXCLUDED.ext_redirect, expires_at = EXCLUDED.expires_at`,
		code, ac.UserID, ac.InstallationID, ac.CodeChallenge, ac.ExtRedirect, expires)
}

// ConsumeAuthCode deletes and returns a live code; a missing or expired one is
// domain.ErrNotFound (C12).
func (s *Store) ConsumeAuthCode(ctx context.Context, code string, now time.Time) (*domain.AuthCode, error) {
	var ac domain.AuthCode
	err := s.pool.QueryRow(ctx, `DELETE FROM auth_codes WHERE code = $1 AND expires_at > $2
		RETURNING user_id, installation_id, code_challenge, ext_redirect`, code, now).
		Scan(&ac.UserID, &ac.InstallationID, &ac.CodeChallenge, &ac.ExtRedirect)
	if err != nil {
		return nil, notFound(err)
	}
	return &ac, nil
}
