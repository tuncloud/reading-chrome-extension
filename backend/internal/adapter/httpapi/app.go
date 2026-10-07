// Package httpapi is the Echo adapter behind the api Lambda's Function URL (spec §2).
// Handlers decode requests, call use cases and encode responses; nothing else.
package httpapi

import (
	"log/slog"

	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"

	"reading-tracker/backend/internal/platform/config"
	"reading-tracker/backend/internal/usecase/account"
	"reading-tracker/backend/internal/usecase/auth"
	"reading-tracker/backend/internal/usecase/connect"
	"reading-tracker/backend/internal/usecase/library"
	"reading-tracker/backend/internal/usecase/readsync"
	"reading-tracker/backend/internal/usecase/setup"
)

// Deps are the use cases behind the routes.
type Deps struct {
	Config   *config.Config
	Auth     *auth.Service
	Connect  *connect.Service
	Setup    *setup.Service
	ReadSync *readsync.Service
	Library  *library.Service
	Account  *account.Service
	Log      *slog.Logger
}

type handlers struct{ Deps }

// New builds the app: /v1 routes, §2.0 JSON errors, 256 KB body limit and
// path-only request logs. CORS is configured on the Function URL, not here.
func New(d Deps) *echo.Echo {
	e := echo.New()
	e.HideBanner, e.HidePort = true, true
	e.HTTPErrorHandler = errorHandler(d.Log)
	e.Use(middleware.Recover())
	e.Use(requestLogger(d.Log))
	e.Use(middleware.BodyLimit("256K"))

	h := &handlers{Deps: d}
	v1 := e.Group("/v1")
	v1.GET("/config", h.getConfig)
	v1.GET("/me", h.getMe, h.requireDevice)
	v1.GET("/oauth/notion/start", h.oauthStart)
	v1.GET("/oauth/notion/callback", h.oauthCallback)
	v1.POST("/auth/token", h.authToken)
	v1.POST("/auth/logout", h.logout, h.requireDevice)
	v1.GET("/setup/pages", h.setupPages, h.requireDevice)
	v1.POST("/setup", h.postSetup, h.requireDevice)
	v1.POST("/articles/status", h.articlesStatus, h.requireDevice)
	v1.POST("/sync/push", h.syncPush, h.requireDevice)
	v1.DELETE("/me", h.deleteMe, h.requireDevice)
	return e
}
