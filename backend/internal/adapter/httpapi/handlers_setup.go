package httpapi

import (
	"net/http"

	"github.com/labstack/echo/v4"
)

type pageView struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	URL   string `json:"url"`
}

// setupPages serves GET /v1/setup/pages (spec §2.6).
func (h *handlers) setupPages(c echo.Context) error {
	pages, err := h.Setup.SearchPages(c.Request().Context(), principal(c).InstallationID(), c.QueryParam("q"))
	if err != nil {
		return err
	}
	views := make([]pageView, len(pages))
	for i, p := range pages {
		views[i] = pageView{ID: p.ID, Title: p.Title, URL: p.URL}
	}
	return c.JSON(http.StatusOK, map[string]any{"pages": views})
}

type setupRequest struct {
	ParentPageID string `json:"parent_page_id"`
}

// postSetup serves POST /v1/setup (spec §2.7).
func (h *handlers) postSetup(c echo.Context) error {
	var req setupRequest
	if err := decodeJSON(c, &req); err != nil {
		return err
	}
	inst, err := h.Setup.Setup(c.Request().Context(), principal(c).InstallationID(), req.ParentPageID)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, viewInstallation(inst))
}
