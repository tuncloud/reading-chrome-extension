package httpapi

import (
	"net/http"

	"github.com/labstack/echo/v4"

	"reading-tracker/backend/internal/store"
)

const maxStatusURLs = 100

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

// articlesStatus serves POST /v1/articles/status (spec §2.9). Unknown URLs are omitted.
func (h *handlers) articlesStatus(c echo.Context) error {
	var req statusRequest
	if err := decodeJSON(c, &req); err != nil {
		return err
	}
	if len(req.CanonicalURLs) > maxStatusURLs {
		return errTooLarge("canonical_urls exceeds 100")
	}
	inst := principal(c).InstallationID()
	if inst == "" {
		return errNoInstallation()
	}
	urlByHash := map[string]string{}
	hashes := make([]string, 0, len(req.CanonicalURLs))
	for _, u := range req.CanonicalURLs {
		hash := store.URLHash(u)
		if _, dup := urlByHash[hash]; !dup {
			urlByHash[hash] = u
			hashes = append(hashes, hash)
		}
	}
	arts, err := h.Store.BatchGetArticles(c.Request().Context(), inst, hashes)
	if err != nil {
		return err
	}
	items := make(map[string]articleStatus, len(arts))
	for hash, a := range arts {
		st := articleStatus{TotalSeconds: a.TotalSeconds, MaxProgress: a.MaxProgress, Sessions: a.Sessions,
			Completed: a.Completed, LastReadAt: a.LastReadAt}
		if a.NotionURL != "" {
			u := a.NotionURL
			st.NotionURL = &u
		}
		items[urlByHash[hash]] = st
	}
	return c.JSON(http.StatusOK, map[string]any{"items": items})
}
