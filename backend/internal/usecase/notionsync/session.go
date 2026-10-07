package notionsync

import (
	"context"
	"errors"
	"fmt"
	"time"

	"reading-tracker/backend/internal/domain"
	"reading-tracker/backend/internal/usecase/port"
)

// session runs Notion calls for one installation, refreshing the token once on 401.
type session struct {
	svc       *Service
	inst      *domain.Installation
	token     string
	refreshed bool
}

func (s *Service) openSession(inst *domain.Installation) (*session, error) {
	tok, err := s.Sealer.Open(inst.AccessTokenEnc, []byte(inst.ID))
	if err != nil {
		return nil, fmt.Errorf("notionsync: decrypt access token: %w", err)
	}
	return &session{svc: s, inst: inst, token: string(tok)}, nil
}

// db is the installation's current Notion target.
func (s *session) db() domain.DBConfig { return s.inst.Databases() }

// call runs fn with the current token. On 401 it refreshes once and retries. A 401
// with a freshly refreshed token means the integration was removed → revoked.
func (s *session) call(ctx context.Context, fn func(token string) error) error {
	err := fn(s.token)
	if !port.IsNotionKind(err, port.NotionUnauthorized) {
		return err
	}
	if s.refreshed {
		return s.revoke(ctx)
	}
	if err := s.refresh(ctx); err != nil {
		return err
	}
	if err = fn(s.token); port.IsNotionKind(err, port.NotionUnauthorized) {
		return s.revoke(ctx)
	}
	return err
}

// refresh rotates tokens and persists both under the token_version guard (spec §2.16, C7).
func (s *session) refresh(ctx context.Context) error {
	s.refreshed = true
	if s.inst.RefreshTokenEnc == "" {
		return s.revoke(ctx)
	}
	aad := []byte(s.inst.ID)
	rt, err := s.svc.Sealer.Open(s.inst.RefreshTokenEnc, aad)
	if err != nil {
		return fmt.Errorf("notionsync: decrypt refresh token: %w", err)
	}
	res, err := s.svc.Auth.RefreshToken(ctx, string(rt))
	if port.IsNotionKind(err, port.NotionInvalidGrant) {
		return s.revoke(ctx)
	}
	if err != nil {
		return err
	}
	access, err := s.svc.Sealer.Seal([]byte(res.AccessToken), aad)
	if err != nil {
		return err
	}
	refresh := s.inst.RefreshTokenEnc
	if res.RefreshToken != "" {
		if refresh, err = s.svc.Sealer.Seal([]byte(res.RefreshToken), aad); err != nil {
			return err
		}
	}
	err = s.persistTokens(ctx, access, refresh)
	if errors.Is(err, domain.ErrConditionFailed) {
		// Someone rotated first; use what they stored.
		fresh, gerr := s.svc.Installations.GetInstallation(ctx, s.inst.ID)
		if gerr != nil {
			return gerr
		}
		tok, oerr := s.svc.Sealer.Open(fresh.AccessTokenEnc, aad)
		if oerr != nil {
			return oerr
		}
		s.inst, s.token = fresh, string(tok)
		return nil
	}
	if err != nil {
		return err
	}
	s.inst.AccessTokenEnc, s.inst.RefreshTokenEnc = access, refresh
	s.inst.TokenVersion++
	s.token = res.AccessToken
	return nil
}

const (
	tokenWriteAttempts = 3
	tokenWriteBackoff  = 100 * time.Millisecond
)

// persistTokens stores the rotated tokens, retrying transient failures in-process:
// Notion has already rotated the refresh token, so losing this write would force a
// reconnect (C7). A condition failure is returned at once.
func (s *session) persistTokens(ctx context.Context, access, refresh string) error {
	var err error
	for attempt := 1; attempt <= tokenWriteAttempts; attempt++ {
		err = s.svc.Installations.UpdateInstallationTokens(ctx, s.inst.ID, access, refresh, s.inst.TokenVersion, s.svc.Now())
		if err == nil || errors.Is(err, domain.ErrConditionFailed) {
			return err
		}
		if attempt < tokenWriteAttempts {
			s.svc.sleep(time.Duration(attempt) * tokenWriteBackoff)
		}
	}
	s.svc.Log.ErrorContext(ctx, "notionsync: persisting rotated tokens failed", "installation_id", s.inst.ID, "err", err)
	return err
}

func (s *session) revoke(ctx context.Context) error {
	if err := s.svc.Installations.SetInstallationStatus(ctx, s.inst.ID, domain.StatusRevoked, "", s.svc.Now()); err != nil {
		return err
	}
	s.svc.Log.WarnContext(ctx, "notionsync: installation revoked", "installation_id", s.inst.ID)
	return errAck
}
