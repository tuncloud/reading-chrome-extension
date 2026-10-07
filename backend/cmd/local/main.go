// Command local runs the API on :8080 against DynamoDB Local with an in-process queue.
package main

import (
	"context"
	"errors"
	"net/http"
	"os"
	"strconv"
	"time"

	"reading-tracker/backend/internal/app"
	"reading-tracker/backend/internal/config"
	"reading-tracker/backend/internal/httpapi"
	"reading-tracker/backend/internal/queue"
	"reading-tracker/backend/internal/store"
)

func main() {
	log := app.Logger()
	cfg, err := config.Load(config.RoleAPI, os.Getenv)
	if err != nil {
		log.Error("config", "err", err)
		os.Exit(1)
	}
	ctx := context.Background()
	awsCfg, err := app.AWSConfig(ctx)
	if err != nil {
		log.Error("aws config", "err", err)
		os.Exit(1)
	}
	ddb := app.DynamoDB(awsCfg)
	if err := store.CreateTable(ctx, ddb, cfg.TableName); err != nil {
		log.Error("create table (is `make ddb-up` running?)", "err", err)
		os.Exit(1)
	}
	st := store.New(ddb, cfg.TableName)
	q := &queue.Local{Delay: localDelay(cfg), Log: log}
	q.Handle = app.Worker(cfg, st, q, q, log).HandleSQS
	e := httpapi.New(app.APIDeps(cfg, st, q, log))
	addr := os.Getenv("LISTEN_ADDR")
	if addr == "" {
		addr = ":8080"
	}
	log.Info("listening", "addr", addr)
	if err := e.Start(addr); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Error("server", "err", err)
		os.Exit(1)
	}
}

// localDelay is LOCAL_QUEUE_DELAY_SECONDS, defaulting to the debounce bucket (D = B).
func localDelay(cfg *config.Config) time.Duration {
	if secs, err := strconv.Atoi(os.Getenv("LOCAL_QUEUE_DELAY_SECONDS")); err == nil && secs >= 0 {
		return time.Duration(secs) * time.Second
	}
	return cfg.DebounceBucket
}
