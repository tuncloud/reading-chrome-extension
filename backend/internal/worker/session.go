package worker

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"reading-tracker/backend/internal/notion"
	"reading-tracker/backend/internal/store"
)

// session runs Notion calls for one installation, refreshing the token once on 401.
// FIFO grouping guarantees no concurrent refresh for the same installation (C8).
type session struct {
	w         *Worker
	inst      *store.Installation
	token     string
	refreshed bool
}

func (w *Worker) openSession(inst *store.Installation) (*session, error) {
	tok, err := w.Keys.Open(inst.AccessTokenEnc, []byte(inst.ID()))
	if err != nil {
		return nil, fmt.Errorf("worker: decrypt access token: %w", err)
	}
	return &session{w: w, inst: inst, token: string(tok)}, nil
}

// call runs fn with the current token. On 401 it refreshes once and retries. A 401
// with a freshly refreshed token means the integration was removed → revoked.
func (s *session) call(ctx context.Context, fn func(token string) error) error {
	err := fn(s.token)
	if !isStatus(err, http.StatusUnauthorized) {
		return err
	}
	if s.refreshed {
		return s.revoke(ctx)
	}
	if err := s.refresh(ctx); err != nil {
		return err
	}
	if err = fn(s.token); isStatus(err, http.StatusUnauthorized) {
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
	aad := []byte(s.inst.ID())
	rt, err := s.w.Keys.Open(s.inst.RefreshTokenEnc, aad)
	if err != nil {
		return fmt.Errorf("worker: decrypt refresh token: %w", err)
	}
	res, err := s.w.Notion.RefreshToken(ctx, string(rt))
	if e, ok := notion.AsAPIError(err); ok && e.Code == "invalid_grant" {
		return s.revoke(ctx)
	}
	if err != nil {
		return err
	}
	access, err := s.w.Keys.Seal([]byte(res.AccessToken), aad)
	if err != nil {
		return err
	}
	refresh := s.inst.RefreshTokenEnc
	if res.RefreshToken != "" {
		if refresh, err = s.w.Keys.Seal([]byte(res.RefreshToken), aad); err != nil {
			return err
		}
	}
	err = s.w.Store.UpdateInstallationTokens(ctx, s.inst.ID(), access, refresh, s.inst.TokenVersion, s.w.Now())
	if errors.Is(err, store.ErrConditionFailed) {
		// Someone rotated first; use what they stored.
		fresh, gerr := s.w.Store.GetInstallation(ctx, s.inst.ID())
		if gerr != nil {
			return gerr
		}
		tok, oerr := s.w.Keys.Open(fresh.AccessTokenEnc, aad)
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

func (s *session) revoke(ctx context.Context) error {
	if err := s.w.Store.SetInstallationStatus(ctx, s.inst.ID(), store.StatusRevoked, "", s.w.Now()); err != nil {
		return err
	}
	s.w.Log.WarnContext(ctx, "worker: installation revoked", "installation_id", s.inst.ID())
	return errAck
}
