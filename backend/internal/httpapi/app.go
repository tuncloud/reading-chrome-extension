// Package httpapi is the Echo application behind the api Lambda's Function URL (spec §2).
package httpapi

import (
	"log/slog"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"

	"reading-tracker/backend/internal/config"
	"reading-tracker/backend/internal/notion"
	"reading-tracker/backend/internal/queue"
	"reading-tracker/backend/internal/setup"
	"reading-tracker/backend/internal/store"
	"reading-tracker/backend/internal/syncsvc"
)

// Deps are the handlers' collaborators.
type Deps struct {
	Config *config.Config
	Store  *store.Store
	Notion *notion.Client
	Queue  queue.Sender
	Now    func() time.Time
	Log    *slog.Logger
}

type handlers struct {
	Deps
	setup *setup.Service
	sync  *syncsvc.Service
}

// New builds the app: /v1 routes, §2.0 JSON errors, 256 KB body limit and
// path-only request logs. CORS is configured on the Function URL, not here.
func New(d Deps) *echo.Echo {
	e := echo.New()
	e.HideBanner, e.HidePort = true, true
	e.HTTPErrorHandler = errorHandler(d.Log)
	e.Use(middleware.Recover())
	e.Use(requestLogger(d.Log))
	e.Use(middleware.BodyLimit("256K"))

	h := &handlers{
		Deps:  d,
		setup: &setup.Service{Notion: d.Notion, Store: d.Store, Queue: d.Queue, Bucket: d.Config.DebounceBucket, Now: d.Now},
		sync:  &syncsvc.Service{Store: d.Store, Queue: d.Queue, Bucket: d.Config.DebounceBucket, Now: d.Now},
	}
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
