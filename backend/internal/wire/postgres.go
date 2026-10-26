package wire

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"reading-tracker/backend/internal/adapter/postgres"
	"reading-tracker/backend/internal/adapter/queue/pgqueue"
	"reading-tracker/backend/internal/platform/config"
	"reading-tracker/backend/internal/platform/pgmigrate"
)

// defaultMaxConns caps each pod's pool unless DATABASE_URL sets pool_max_conns.
const defaultMaxConns = 5

// PG is the Postgres half of the composition: one pool shared by store and queue.
type PG struct {
	Pool  *pgxpool.Pool
	Store *postgres.Store
	Queue *pgqueue.Sender
}

// Postgres connects to cfg.DatabaseURL, pings, applies pending migrations and
// returns the store and queue sender. The pool is closed on any failure.
func Postgres(ctx context.Context, cfg *config.Config) (*PG, error) {
	poolCfg, err := pgxpool.ParseConfig(cfg.DatabaseURL)
	if err != nil {
		return nil, err
	}
	if !strings.Contains(cfg.DatabaseURL, "pool_max_conns=") {
		poolCfg.MaxConns = defaultMaxConns
	}
	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		return nil, err
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	if err := pgmigrate.Up(ctx, pool); err != nil {
		pool.Close()
		return nil, err
	}
	return &PG{Pool: pool, Store: postgres.New(pool), Queue: &pgqueue.Sender{Pool: pool, Delay: cfg.QueueDelay}}, nil
}

// Livez answers 200 "ok" unconditionally: a process-only liveness check, so a database
// outage fails readiness (Healthz) without restart-storming the pods.
func Livez() http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte("ok"))
	}
}

// Healthz answers 200 "ok" when the database pings within 2 s, else 503 "unavailable".
func Healthz(pinger interface{ Ping(context.Context) error }) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		if err := pinger.Ping(ctx); err != nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte("unavailable"))
			return
		}
		_, _ = w.Write([]byte("ok"))
	}
}
