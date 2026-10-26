// Command migrate applies pending Postgres migrations to DATABASE_URL and exits.
package main

import (
	"context"
	"os"

	"github.com/jackc/pgx/v5/pgxpool"

	"reading-tracker/backend/internal/platform/pgmigrate"
	"reading-tracker/backend/internal/wire"
)

func main() {
	log := wire.Logger()
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		log.Error("DATABASE_URL is required")
		os.Exit(1)
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		log.Error("postgres", "err", err)
		os.Exit(1)
	}
	err = pgmigrate.Up(ctx, pool)
	pool.Close()
	if err != nil {
		log.Error("migrate", "err", err)
		os.Exit(1)
	}
	log.Info("migrations applied")
}
