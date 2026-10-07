package httpapi

import (
	"errors"
	"net/http"
	"net/url"

	"github.com/labstack/echo/v4"

	"reading-tracker/backend/internal/usecase/connect"
)

const linkExpired = "Link expired, try again"

// oauthStart serves GET /v1/oauth/notion/start (spec §2.1).
func (h *handlers) oauthStart(c echo.Context) error {
	q := c.QueryParams()
	to, err := h.Connect.Start(c.Request().Context(), q.Get("ext_redirect"), q.Get("code_challenge"),
		q.Get("code_challenge_method"), h.redirectURI(c))
	if err != nil {
		return err
	}
	return c.Redirect(http.StatusFound, to)
}

// oauthCallback serves GET /v1/oauth/notion/callback (spec §2.2).
func (h *handlers) oauthCallback(c echo.Context) error {
	q := c.QueryParams()
	r, err := h.Connect.Complete(c.Request().Context(), q.Get("state"), q.Get("code"), q.Get("error"), h.redirectURI(c))
	if errors.Is(err, connect.ErrLinkExpired) {
		return c.String(http.StatusBadRequest, linkExpired)
	}
	if err != nil {
		return err
	}
	u, err := url.Parse(r.URL)
	if err != nil {
		return err
	}
	v := u.Query()
	v.Set(r.Param, r.Value)
	u.RawQuery = v.Encode()
	return c.Redirect(http.StatusFound, u.String())
}

// redirectURI is PUBLIC_BASE_URL or the request's own origin, plus the callback path (C14).
func (h *handlers) redirectURI(c echo.Context) string {
	base := h.Config.PublicBaseURL
	if base == "" {
		base = c.Scheme() + "://" + c.Request().Host
	}
	return base + "/v1/oauth/notion/callback"
}
