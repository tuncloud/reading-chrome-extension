package httpapi

import (
	"context"
	"net/http"
	"strings"

	"github.com/labstack/echo/v4"

	"reading-tracker/backend/internal/notion"
	"reading-tracker/backend/internal/store"
)

// installationToken loads the active installation and decrypts its access token.
// The API never refreshes (C8): a Notion 401 here surfaces as 409 for the client to reconnect.
func (h *handlers) installationToken(ctx context.Context, p *Principal) (*store.Installation, string, error) {
	inst, err := h.activeInstallation(ctx, p)
	if err != nil {
		return nil, "", err
	}
	if inst == nil {
		return nil, "", errNoInstallation()
	}
	if inst.Status == store.StatusRevoked {
		return nil, "", errConflict("Notion connection revoked; reconnect")
	}
	tok, err := h.Config.Keys.Open(inst.AccessTokenEnc, []byte(inst.ID()))
	if err != nil {
		return nil, "", err
	}
	return inst, string(tok), nil
}

// notionErr maps Notion failures in API calls to §2.0 errors.
func notionErr(err error) error {
	e, ok := notion.AsAPIError(err)
	if !ok {
		return err
	}
	switch {
	case e.Status == http.StatusUnauthorized:
		return errConflict("Notion authorization expired; reconnect")
	case e.Status == http.StatusNotFound:
		return errNotFound("Notion object not found or not shared with the integration")
	case e.Status == http.StatusBadRequest:
		return errInvalid("Notion rejected the request: %s", e.Message)
	}
	return err
}

// setupPages serves GET /v1/setup/pages (spec §2.6).
func (h *handlers) setupPages(c echo.Context) error {
	ctx := c.Request().Context()
	_, token, err := h.installationToken(ctx, principal(c))
	if err != nil {
		return err
	}
	pages, err := h.Notion.SearchPages(ctx, token, c.QueryParam("q"))
	if err != nil {
		return notionErr(err)
	}
	return c.JSON(http.StatusOK, map[string]any{"pages": pages})
}

type setupRequest struct {
	ParentPageID string `json:"parent_page_id"`
}

// postSetup serves POST /v1/setup (spec §2.7).
func (h *handlers) postSetup(c echo.Context) error {
	ctx := c.Request().Context()
	var req setupRequest
	if err := decodeJSON(c, &req); err != nil {
		return err
	}
	if strings.TrimSpace(req.ParentPageID) == "" {
		return errInvalid("parent_page_id is required")
	}
	inst, token, err := h.installationToken(ctx, principal(c))
	if err != nil {
		return err
	}
	if inst.Status == store.StatusActive {
		return errConflict("installation is already set up")
	}
	db, err := h.setup.CreateDatabases(ctx, token, req.ParentPageID)
	if err != nil {
		return notionErr(err)
	}
	if err := h.Store.SaveInstallationDatabases(ctx, inst.ID(), db, h.Now()); err != nil {
		return err
	}
	inst.ApplyDatabases(db)
	inst.Status, inst.SetupError = store.StatusActive, ""
	if err := h.setup.Resync(ctx, inst.ID()); err != nil {
		h.Log.WarnContext(ctx, "setup: resync enqueue failed", "err", err)
	}
	return c.JSON(http.StatusOK, viewInstallation(inst))
}
