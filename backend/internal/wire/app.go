// Package wire is the composition root: it builds adapters, injects them into use
// cases, and hands cmd/* a ready API handler or job handler. wire/awswire adds the
// AWS clients and the SQS consumer. Only cmd/* (and integration tests) import it.
package wire

import (
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/labstack/echo/v4"

	"reading-tracker/backend/internal/adapter/httpapi"
	"reading-tracker/backend/internal/adapter/notion"
	"reading-tracker/backend/internal/adapter/queue/msg"
	"reading-tracker/backend/internal/domain"
	"reading-tracker/backend/internal/platform/config"
	"reading-tracker/backend/internal/platform/crypto"
	"reading-tracker/backend/internal/usecase/account"
	"reading-tracker/backend/internal/usecase/auth"
	"reading-tracker/backend/internal/usecase/connect"
	"reading-tracker/backend/internal/usecase/library"
	"reading-tracker/backend/internal/usecase/notionsync"
	"reading-tracker/backend/internal/usecase/port"
	"reading-tracker/backend/internal/usecase/readsync"
	"reading-tracker/backend/internal/usecase/setup"
)

// Logger writes JSON logs to stdout (CloudWatch).
func Logger() *slog.Logger { return slog.New(slog.NewJSONHandler(os.Stdout, nil)) }

func NotionClient(cfg *config.Config, pacer notion.Pacer) *notion.Client {
	return &notion.Client{BaseURL: cfg.NotionAPIBase, Version: cfg.NotionVersion, ClientID: cfg.NotionClientID,
		ClientSecret: cfg.NotionClientSecret, HTTP: &http.Client{Timeout: 15 * time.Second}, Pacer: pacer}
}

// Store is every repository port the API and the Notion sync use.
type Store interface {
	port.Users
	port.Devices
	port.OAuthGrants
	port.Installations
	port.Reading
	port.DataPurger
}

// APIDeps are the API's infrastructure; NewAPI builds the use cases on top of them.
type APIDeps struct {
	Config *config.Config
	Store  Store
	Notion *notion.Client
	Queue  msg.Sender
	Now    func() time.Time
	Log    *slog.Logger
	Sleep  func(time.Duration)
}

// NewAPI composes the HTTP adapter and its use cases. The API never paces or
// refreshes Notion tokens (C3, C8).
func NewAPI(d APIDeps) *echo.Echo {
	gw := &notion.Gateway{Client: d.Notion}
	sq := &msg.SyncQueue{Sender: d.Queue, Bucket: d.Config.DebounceBucket, Now: d.Now}
	activator := &setup.Activator{Reading: d.Store, Installations: d.Store, Queue: sq, Now: d.Now, Sleep: d.Sleep, Log: d.Log}
	return httpapi.New(httpapi.Deps{
		Config: d.Config,
		Auth: &auth.Service{Users: d.Store, Devices: d.Store, Grants: d.Store, Installations: d.Store,
			Pepper: d.Config.DeviceTokenPepper, Now: d.Now, Log: d.Log},
		Connect: &connect.Service{Users: d.Store, Grants: d.Store, Installations: d.Store, Auth: gw, Workspace: gw,
			Sealer: d.Config.Keys, Activator: activator, AllowedExtensionIDs: d.Config.AllowedExtensionIDs, Now: d.Now, Log: d.Log},
		Setup:    &setup.Service{Installations: d.Store, Workspace: gw, Sealer: d.Config.Keys, Activator: activator, Now: d.Now},
		ReadSync: &readsync.Service{Reading: d.Store, Queue: sq, Now: d.Now},
		Library:  &library.Service{Reading: d.Store},
		Account:  &account.Service{Users: d.Store, Devices: d.Store, Installations: d.Store, Queue: sq, Now: d.Now},
		Log:      d.Log,
	})
}

// API is the production API: unpaced Notion client, real clock.
func API(cfg *config.Config, st Store, q msg.Sender, log *slog.Logger) *echo.Echo {
	return NewAPI(APIDeps{Config: cfg, Store: st, Notion: NotionClient(cfg, nil), Queue: q, Now: time.Now, Log: log})
}

// SyncDeps are the Notion sync's collaborators.
type SyncDeps struct {
	Store    Store
	Notion   *notion.Client
	Keys     *crypto.Keyring
	Queue    msg.Sender
	Tracking config.Tracking
	// DebounceBucket sizes the requeue dedup bucket; the API uses the same value.
	DebounceBucket time.Duration
	Now            func() time.Time
	Jitter         func() time.Duration
	Sleep          func(time.Duration)
	Log            *slog.Logger
}

// NewJobHandler assembles the Notion sync that handles one queue job.
func NewJobHandler(d SyncDeps) port.JobHandler {
	gw := &notion.Gateway{Client: d.Notion}
	return &notionsync.Service{
		Installations: d.Store, Reading: d.Store, Users: d.Store, Devices: d.Store, Purger: d.Store,
		Pages: gw, Auth: gw, Sealer: d.Keys,
		Queue: &msg.SyncQueue{Sender: d.Queue, Bucket: d.DebounceBucket, Now: d.Now},
		Policy: domain.SyncPolicy{MinCreateSeconds: d.Tracking.MinCreateSeconds, CompleteProgress: d.Tracking.CompleteProgress,
			CompleteTimeRatio: d.Tracking.CompleteTimeRatio, WordsPerMinute: d.Tracking.WordsPerMinute},
		Now: d.Now, Jitter: d.Jitter, Sleep: d.Sleep, Log: d.Log,
	}
}

// JobHandler is the production Notion sync: paced Notion client (C22).
func JobHandler(cfg *config.Config, st Store, q msg.Sender, log *slog.Logger) port.JobHandler {
	return NewJobHandler(SyncDeps{Store: st, Notion: NotionClient(cfg, notion.NewMinInterval(350*time.Millisecond)),
		Keys: cfg.Keys, Queue: q, Tracking: cfg.Public.Tracking, DebounceBucket: cfg.DebounceBucket, Now: time.Now, Log: log})
}
