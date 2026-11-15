# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this is

Reading Tracker: a Chrome MV3 extension (`extension/`) measures active reading time on article pages and a Go backend (`backend/`) records it in the user's Notion workspace. The backend runs on Kubernetes (tuncloud cluster) as two processes, `cmd/server` (Echo HTTP API) and `cmd/poller` (Notion sync), both on one Postgres database that also holds the job queue. There is no other deployment mode.

`docs/reading-tracker-agent-spec.md` is the behaviour authority. Code comments cite it by number (`C2`, `§2.12`, `scenario 4`, `T22`): never renumber its sections, scenarios, tasks or constraints; new items take the next free number. Designs and plans for past changes live in `docs/superpowers/{specs,plans}/` and are kept as history, not updated.

## Commands

Container runtime is Podman, never Docker. The extension uses pnpm.

Backend (`cd backend`; tests need local Postgres from `make pg-up`):

```sh
make pg-up                      # Postgres 17 on :5432 (container reading-pg)
make test                       # go test ./...
gofmt -l . ; go vet ./... && go test -race ./...   # what CI runs
go test ./internal/adapter/httpapi/ -run TestHistory -v   # one package / test
make .env.local && make run     # server on :8080 + poller (needs Notion client id/secret in .env.local)
make pg-migrate                 # apply migrations only
make image                      # podman build, linux/arm64
```

Tests fail (not skip) when Postgres is unreachable. `PG_TEST_URL` overrides the default `postgres://reading:reading@localhost:5432/reading`.

Extension (`cd extension`):

```sh
pnpm install
pnpm test                       # Vitest, includes test/arch.test.ts
pnpm test src/background/restore.test.ts   # one file
pnpm typecheck
pnpm build                      # two Vite builds: SW + React pages, then the content script as one IIFE
pnpm check                      # typecheck, test, build
```

`VITE_API_BASE` (in `.env.local`, or `.env.production.local` which wins for `pnpm build`/`pnpm dev`) picks the backend; `EXTENSION_KEY` in `.env.local` pins the extension ID.

## Backend architecture

Clean layers, enforced by `internal/archtest`:

- `internal/domain`: entities and rules, stdlib only.
- `internal/usecase/*` (`auth`, `connect`, `setup`, `readsync`, `library`, `account`, `notionsync`): depend only on interfaces in `internal/usecase/port`; error kinds in `usecase/apperr`.
- `internal/adapter/*`: `postgres` (store), `notion` (API client), `queue/pgqueue` (Postgres queue sender + poller), `queue/msg` (message types, dedup ids, the `SyncQueue` implementation, a test `Recorder`), `httpapi` (Echo). An adapter never imports another adapter root.
- `internal/platform/*`: `config`, `crypto`, `pgmigrate`, `pgtest`, `textutil`.
- `internal/wire`: the composition root; only `cmd/*` and tests import it.

Data ownership: reading data (`articles`, `segments`, `days`) is keyed by **`user_id`**. An **installation** is one Notion authorization (id = Notion `bot_id`) and holds only sealed tokens, status and the Notion database ids; a user has one active installation (`users.active_installation_id`). Notion page links on article/day rows refer to the active installation's databases; a row whose `notion_ds_id` differs from the active installation's data source forgets its page and is re-created there.

A read's path: `POST /v1/sync/push` → `readsync` writes each segment exactly once (C2, one transaction per segment in `postgres/push.go`) → enqueues debounced article/day jobs (`msg.SyncQueue`, dedup per bucket, C5) → `pgqueue.Poller` claims jobs one at a time per group `user#<user_id>` (C4) → `notionsync` resolves the user's active installation, writes absolute values to Notion (C6), retries/back-offs (§2.15), refreshes tokens under a `token_version` guard (C7).

Migrations are embedded SQL in `internal/platform/pgmigrate/migrations/`, forward-only, one transaction per file, applied by every binary on start. Never edit a migration that has shipped; add the next number. Test a data migration with `pgmigrate.UpThrough` (test export) to stop at the previous version, insert fixtures, then `Up`.

Test helpers: `adapter/postgres/postgrestest` (fresh migrated DB + store, `Count`, `Dump`), `adapter/storetest` (store conformance suite), `adapter/notion/notiontest` (fake Notion), `internal/e2e` (API + sync end to end). Test-only seams use `export_test.go` (e.g. `postgres.WithTxFault`).

## Extension architecture

Five folders under `src/`, import rules enforced by `test/arch.test.ts`: `shared` (imports only `shared`), `content` (page; only `content` + `shared`, no Dexie, no React), `background` (service worker; Dexie only here), `popup` and `options` (React only here).

`background/services.ts` builds every service; `background/index.ts` is the only place that registers chrome listeners and runs startup/sync. Segments are stored in IndexedDB (`background/db.ts`) and pushed by `background/sync.ts`; only unsynced segments and their articles are ever pushed. After each connect, and once on extension update, `background/restore.ts` pulls `GET /v1/history` into IndexedDB (articles only, merged by larger-of, plus a `today_carry` KV for today's time); restored data is never pushed. The popup (`popup/model.ts`) reads only local storage plus `POST /v1/articles/status`.

Tests use fake-indexeddb, `test/helpers.ts` (fresh DB, clock, API stub) and `test/chrome.ts` (a `chrome` mock).

## Deploy

Pushing to `main` with changes under `backend/` runs `.github/workflows/backend-image.yml`: tests, arm64 image to GHCR, then rollout through the deploy gateway, poller first, then server. Old and new pods overlap during a rollout, so migrations and queue-message changes must tolerate old code running against the new schema. Manifest: `backend/deploy/manifest.yaml`; secrets come from AWS Parameter Store through the cluster's `aws-parameter-store` ClusterSecretStore. Operator steps: `backend/README.md` → "Deploy to tuncloud" and `docs/runbook.md`.
