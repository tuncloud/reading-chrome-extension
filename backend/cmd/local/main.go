// Command local runs the API on :8080 against DynamoDB Local with an in-process queue.
package main

import (
	"context"
	"errors"
	"net/http"
	"os"
	"strconv"
	"time"

	"reading-tracker/backend/internal/adapter/dynamo"
	"reading-tracker/backend/internal/adapter/queue"
	"reading-tracker/backend/internal/platform/config"
	"reading-tracker/backend/internal/wire"
	"reading-tracker/backend/internal/wire/awswire"
)

func main() {
	log := wire.Logger()
	cfg, err := config.Load(config.RoleAPI, os.Getenv)
	if err != nil {
		log.Error("config", "err", err)
		os.Exit(1)
	}
	ctx := context.Background()
	awsCfg, err := awswire.AWSConfig(ctx)
	if err != nil {
		log.Error("aws config", "err", err)
		os.Exit(1)
	}
	ddb := awswire.DynamoDB(awsCfg)
	if err := dynamo.CreateTable(ctx, ddb, cfg.TableName); err != nil {
		log.Error("create table (is `make ddb-up` running?)", "err", err)
		os.Exit(1)
	}
	st := dynamo.New(ddb, cfg.TableName)
	delay := localDelay(cfg)
	if delay < cfg.DebounceBucket {
		log.Warn("LOCAL_QUEUE_DELAY_SECONDS is below DEBOUNCE_BUCKET_SECONDS; the last updates of a bucket may not reach Notion until the next read (C5: D >= B)",
			"delay", delay.String(), "bucket", cfg.DebounceBucket.String())
	}
	q := &queue.Local{Delay: delay, Log: log}
	q.Handle = awswire.Worker(cfg, st, q, q, log).HandleSQS
	e := wire.API(cfg, st, q, log)
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
