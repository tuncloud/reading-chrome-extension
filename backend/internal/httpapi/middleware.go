package httpapi

import (
	"log/slog"
	"reading-tracker/backend/internal/usecase/auth"
	"strings"
	"time"

	"github.com/labstack/echo/v4"
)

const principalKey = "principal"

func principal(c echo.Context) *auth.Principal { return c.Get(principalKey).(*auth.Principal) }

// requireDevice authenticates "Authorization: Bearer rt_…" (spec §2.4).
func (h *handlers) requireDevice(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c echo.Context) error {
		tok, ok := strings.CutPrefix(c.Request().Header.Get("Authorization"), "Bearer ")
		if !ok {
			return errUnauthorized()
		}
		p, err := h.Auth.Authenticate(c.Request().Context(), tok)
		if err != nil {
			return err
		}
		c.Set(principalKey, p)
		return next(c)
	}
}

// requestLogger logs method, path, status and latency. Never the query string
// (OAuth codes/state), headers or bodies (C13).
func requestLogger(log *slog.Logger) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			start := time.Now()
			err := next(c)
			status := c.Response().Status
			if err != nil {
				status = toAPIError(err).status
			}
			log.InfoContext(c.Request().Context(), "request", "method", c.Request().Method,
				"path", c.Request().URL.Path, "status", status, "ms", time.Since(start).Milliseconds())
			return err
		}
	}
}
