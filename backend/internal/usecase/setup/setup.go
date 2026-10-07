package setup

import (
	"context"
	"errors"
	"strings"
	"time"

	"reading-tracker/backend/internal/domain"
	"reading-tracker/backend/internal/usecase/apperr"
	"reading-tracker/backend/internal/usecase/port"
)

// Service lets a connected user pick a parent page and create the databases.
type Service struct {
	Installations port.Installations
	Workspace     port.NotionWorkspace
	Sealer        port.Sealer
	Activator     *Activator
	Now           func() time.Time
}

// SearchPages lists pages the integration can see (spec §2.6).
func (s *Service) SearchPages(ctx context.Context, instID, query string) ([]port.PageRef, error) {
	_, token, err := s.installationToken(ctx, instID)
	if err != nil {
		return nil, err
	}
	pages, err := s.Workspace.SearchPages(ctx, token, query)
	if err != nil {
		return nil, notionErr(err)
	}
	return pages, nil
}

// Setup creates (or reuses) both databases under parentPageID and activates the
// installation with a full resync (spec §2.7, scenario 10).
func (s *Service) Setup(ctx context.Context, instID, parentPageID string) (*domain.Installation, error) {
	if strings.TrimSpace(parentPageID) == "" {
		return nil, apperr.NewInvalid("parent_page_id is required")
	}
	inst, token, err := s.installationToken(ctx, instID)
	if err != nil {
		return nil, err
	}
	if inst.Status == domain.StatusActive {
		return nil, apperr.NewConflict("installation is already set up")
	}
	db, err := s.Workspace.ProvisionDatabases(ctx, token, parentPageID)
	if err != nil {
		// A reused same-titled database may have a missing or wrong-typed property.
		var se *domain.SetupError
		if errors.As(err, &se) {
			return nil, apperr.NewConflict("setup failed: " + se.Reason)
		}
		return nil, notionErr(err)
	}
	if err := s.Installations.SaveInstallationDatabases(ctx, inst.ID, db, s.Now()); err != nil {
		return nil, err
	}
	inst.ApplyDatabases(db)
	inst.Status, inst.SetupError = domain.StatusActive, ""
	if err := s.Activator.Activate(ctx, inst, true); err != nil {
		return nil, err
	}
	return inst, nil
}

// installationToken loads the active installation and decrypts its access token.
// The API never refreshes (C8): a Notion 401 surfaces as a conflict ("reconnect").
func (s *Service) installationToken(ctx context.Context, instID string) (*domain.Installation, string, error) {
	if instID == "" {
		return nil, "", apperr.NewNoInstallation()
	}
	inst, err := s.Installations.GetInstallation(ctx, instID)
	if errors.Is(err, domain.ErrNotFound) {
		return nil, "", apperr.NewNoInstallation()
	}
	if err != nil {
		return nil, "", err
	}
	if inst.Status == domain.StatusRevoked {
		return nil, "", apperr.NewConflict("Notion connection revoked; reconnect")
	}
	tok, err := s.Sealer.Open(inst.AccessTokenEnc, []byte(inst.ID))
	if err != nil {
		return nil, "", err
	}
	return inst, string(tok), nil
}

// notionErr maps Notion failures in API calls to application errors (spec §2.0).
func notionErr(err error) error {
	e, ok := port.NotionErrorOf(err)
	if !ok {
		return err
	}
	switch {
	case e.Kind == port.NotionUnauthorized:
		return apperr.NewConflict("Notion authorization expired; reconnect")
	case e.Kind == port.NotionNotFound:
		return apperr.NewNotFound("Notion object not found or not shared with the integration")
	case e.Kind == port.NotionRejected && e.Status == 400:
		return apperr.NewInvalid("Notion rejected the request: %s", e.Message)
	}
	return err
}
