package wire_test

import (
	"context"
	"encoding/base64"
	"fmt"
	"math/rand/v2"
	"net/url"
	"testing"

	"reading-tracker/backend/internal/platform/config"
	"reading-tracker/backend/internal/platform/pgtest"
	"reading-tracker/backend/internal/wire"
)

func pgConfig(t *testing.T, dbURL string) *config.Config {
	t.Helper()
	env := map[string]string{
		"APP_ENV":                 "test",
		"STORE":                   "postgres",
		"DATABASE_URL":            dbURL,
		"NOTION_CLIENT_ID":        "cid",
		"NOTION_CLIENT_SECRET":    "csecret",
		"NOTION_VERSION":          "2025-09-03",
		"TOKEN_ENC_KEYS":          "v1:" + base64.StdEncoding.EncodeToString(make([]byte, 32)),
		"DEBOUNCE_BUCKET_SECONDS": "120",
	}
	cfg, err := config.Load(config.RoleWorker, func(k string) string { return env[k] })
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func TestPostgresDefaultsPoolToFive(t *testing.T) {
	env := pgtest.New(t)
	for _, tc := range []struct {
		url  string
		want int32
	}{{env.URL, 5}, {env.URL + "&pool_max_conns=3", 3}} {
		pg, err := wire.Postgres(context.Background(), pgConfig(t, tc.url))
		if err != nil {
			t.Fatal(err)
		}
		got := pg.Pool.Config().MaxConns
		pg.Pool.Close()
		if got != tc.want {
			t.Errorf("MaxConns = %d, want %d", got, tc.want)
		}
	}
}

func TestPostgresMigratesOnStart(t *testing.T) {
	ctx := context.Background()
	env := pgtest.New(t)
	name := fmt.Sprintf("reading_unmigrated_%d", rand.Int64())
	if _, err := env.Pool.Exec(ctx, "CREATE DATABASE "+name+" TEMPLATE template0"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = env.Pool.Exec(context.Background(), "DROP DATABASE IF EXISTS "+name+" WITH (FORCE)")
	})
	u, err := url.Parse(env.URL)
	if err != nil {
		t.Fatal(err)
	}
	u.Path = "/" + name
	pg, err := wire.Postgres(ctx, pgConfig(t, u.String()))
	if err != nil {
		t.Fatal(err)
	}
	defer pg.Pool.Close()
	var reg *string
	if err := pg.Pool.QueryRow(ctx, "SELECT to_regclass('sync_jobs')::text").Scan(&reg); err != nil {
		t.Fatal(err)
	}
	if reg == nil {
		t.Fatal("sync_jobs missing after wire.Postgres")
	}
}
