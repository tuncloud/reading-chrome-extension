// Command poller is the Kubernetes Notion-sync process (STORE=postgres): it drains sync_jobs
// and serves /healthz on HEALTH_ADDR.
package main

import (
	"context"
	"errors"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"reading-tracker/backend/internal/adapter/queue/pgqueue"
	"reading-tracker/backend/internal/platform/config"
	"reading-tracker/backend/internal/wire"
)

func main() {
	log := wire.Logger()
	cfg, err := config.Load(config.RoleWorker, os.Getenv)
	if err != nil {
		log.Error("config", "err", err)
		os.Exit(1)
	}
	if cfg.Store != config.StorePostgres {
		log.Error("cmd/poller runs only with STORE=postgres")
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
	p := &pgqueue.Poller{
		Pool:         pg.Pool,
		Handler:      wire.JobHandler(cfg, pg.Store, pg.Queue, log),
		Idle:         pollInterval(),
		Housekeeping: func(ctx context.Context) error { return pg.Store.Sweep(ctx, time.Now()) },
		Log:          log,
	}
	addr := os.Getenv("HEALTH_ADDR")
	if addr == "" {
		addr = ":8081"
	}
	mux := http.NewServeMux()
	mux.Handle("/healthz", wire.Healthz(pg.Pool))
	health := &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		if err := health.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("health server", "err", err)
		}
	}()
	log.Info("polling", "health_addr", addr)
	if err := p.Run(ctx); err != nil {
		log.Error("poller", "err", err)
	}
	sctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := health.Shutdown(sctx); err != nil {
		log.Error("health shutdown", "err", err)
	}
	log.Info("stopped")
}

// pollInterval is POLL_INTERVAL_SECONDS, defaulting to 1 s.
func pollInterval() time.Duration {
	if secs, err := strconv.Atoi(os.Getenv("POLL_INTERVAL_SECONDS")); err == nil && secs >= 0 {
		return time.Duration(secs) * time.Second
	}
	return time.Second
}
