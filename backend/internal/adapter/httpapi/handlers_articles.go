package httpapi

import (
	"net/http"

	"github.com/labstack/echo/v4"
)

type statusRequest struct {
	CanonicalURLs []string `json:"canonical_urls"`
}

type articleStatus struct {
	TotalSeconds int64   `json:"total_seconds"`
	MaxProgress  float64 `json:"max_progress"`
	Sessions     int64   `json:"sessions"`
	Completed    bool    `json:"completed"`
	LastReadAt   string  `json:"last_read_at"`
	NotionURL    *string `json:"notion_url"`
}

// articlesStatus serves POST /v1/articles/status (spec §2.9).
func (h *handlers) articlesStatus(c echo.Context) error {
	var req statusRequest
	if err := decodeJSON(c, &req); err != nil {
		return err
	}
	got, err := h.Library.Status(c.Request().Context(), principal(c).InstallationID(), req.CanonicalURLs)
	if err != nil {
		return err
	}
	items := make(map[string]articleStatus, len(got))
	for u, a := range got {
		st := articleStatus{TotalSeconds: a.TotalSeconds, MaxProgress: a.MaxProgress, Sessions: a.Sessions,
			Completed: a.Completed, LastReadAt: a.LastReadAt}
		if a.NotionURL != "" {
			n := a.NotionURL
			st.NotionURL = &n
		}
		items[u] = st
	}
	return c.JSON(http.StatusOK, map[string]any{"items": items})
}
