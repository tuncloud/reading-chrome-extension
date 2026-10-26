// Command server is the Kubernetes API process (STORE=postgres): Echo on LISTEN_ADDR plus /healthz.
package main

import (
	"context"
	"errors"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/labstack/echo/v4"

	"reading-tracker/backend/internal/platform/config"
	"reading-tracker/backend/internal/wire"
)

func main() {
	log := wire.Logger()
	cfg, err := config.Load(config.RoleAPI, os.Getenv)
	if err != nil {
		log.Error("config", "err", err)
		os.Exit(1)
	}
	if cfg.Store != config.StorePostgres {
		log.Error("cmd/server runs only with STORE=postgres")
		os.Exit(1)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	pg, err := wire.Postgres(ctx, cfg)
	if err != nil {
		log.Error("postgres", "err", err)
		os.Exit(1)
	}
	defer pg.Pool.Close()
	e := wire.API(cfg, pg.Store, pg.Queue, log)
	e.GET("/healthz", echo.WrapHandler(wire.Healthz(pg.Pool)))
	addr := os.Getenv("LISTEN_ADDR")
	if addr == "" {
		addr = ":8080"
	}
	errc := make(chan error, 1)
	go func() { errc <- e.Start(addr) }()
	log.Info("listening", "addr", addr)
	select {
	case err := <-errc:
		if !errors.Is(err, http.ErrServerClosed) {
			log.Error("server", "err", err)
			pg.Pool.Close()
			os.Exit(1)
		}
	case <-ctx.Done():
	}
	sctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := e.Shutdown(sctx); err != nil {
		log.Error("shutdown", "err", err)
	}
	log.Info("stopped")
}
