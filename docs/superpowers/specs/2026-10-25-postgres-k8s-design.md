# Postgres storage + Kubernetes deployment — design

**Date:** 2026-10-25
**Status:** approved 2026-10-25
**Spec authority:** [`docs/reading-tracker-agent-spec.md`](../../reading-tracker-agent-spec.md) still defines behaviour. This document only changes *where the data lives* and *how the processes run*.

## 1. Intent

Run the reading-tracker backend on infrastructure the operator already owns: the tuncloud
Kubernetes cluster and its host-level PostgreSQL 17 (`postgres-17.tuando.app` on `worker`).
The motive is consolidation: one more app on hardware and a database that already run, instead of
paying for a separate AWS stack.

The AWS deployment (DynamoDB + SQS FIFO + Lambda, via Terraform/SAM) **remains a fully supported
option**. Postgres-on-Kubernetes is a second way to run the same application, not a replacement.

## 2. Goals, non-goals, anti-goals

**Goals**
- A Postgres implementation of every storage port, selectable at startup.
- A Postgres-backed job queue that gives the Notion-sync worker the same guarantees SQS FIFO gives
  today (§5).
- Long-running API and worker processes that run on Kubernetes, with **no AWS SDK calls** at
  runtime when the Postgres mode is selected.
- CI/CD and manifests that follow the tuncloud conventions (the `deploying-to-tuncloud` skill in
  `tuncloud-deployment-config/.claude/skills/`).
- One conformance test suite that runs against both storage adapters, so their behaviour cannot
  drift apart.

**Non-goals**
- Migrating existing data from DynamoDB to Postgres. The two deployments are independent
  installations; an installation lives in one or the other.
- Any change to the Chrome extension or to the HTTP API contract.
- Creating the cluster, the Postgres server, DNS, or TLS. All exist.
- Replacing the AWS deployment, or changing how it is configured and deployed.

**Anti-goals** (a build that does these has failed even if it works)
- Weakening the queue guarantees (per-installation ordering, debounce, retry-after back-off,
  fail-rest-of-batch) in the Postgres mode.
- Breaking byte-level compatibility of the AWS deployment: DynamoDB keys/attributes, SQS message
  bodies, Lambda entrypoints and Terraform must keep working unchanged.
- Introducing an ORM or a schema-migration tool with its own runtime service.
- The Kubernetes pods needing AWS credentials for anything the *application* does. (The cluster's
  External Secrets Operator reads AWS Parameter Store to materialise k8s Secrets; that is cluster
  plumbing outside the app and is acceptable.)

## 3. Constraints

- Go 1.26, module `reading-tracker/backend`. The clean-architecture rules enforced by
  `internal/archtest` stay in force: use cases import only `domain`, `usecase/...`,
  `platform/{crypto,textutil}`; adapters never import other adapters; only `cmd/*` imports `wire`.
- The ports in `internal/usecase/port` are the contract. **Use cases and domain do not change.**
- All spec constraints bind in both modes. The queue-relevant ones: C4 (one installation's jobs are
  processed in order, one at a time), C5 (debounce: `D ≥ B`, `B ≤ 300`), C21 (first failure in a
  batch fails the rest), §2.15 (429/529 → hide the job for `Retry-After + rand(0..3)s`).
- `max_connections=100` on `postgres-17` is shared with n8n and Temporal. **Pool cap: 5 per pod.**
- All cluster nodes are arm64. Images are built for `linux/arm64`.
- Secrets reach pods via Parameter Store → ExternalSecret → Secret → `envFrom`. The operator
  creates parameter values by hand; the repo documents paths and key names, never values.
- Container runs as UID 65532, read-only root filesystem, all capabilities dropped.
- Local development uses Podman, never Docker.

## 4. Mode selection and process layout

### 4.1 Config

One new variable selects the storage/queue backend:

| Var | Values | Required when |
|---|---|---|
| `STORE` | `dynamo` (default when unset, so existing deployments need no change) or `postgres` | always |
| `DATABASE_URL` | `postgres://user:pass@host:5432/db?sslmode=…` | `STORE=postgres` |
| `TABLE_NAME`, `QUEUE_URL` | as today | `STORE=dynamo` only |
| `LISTEN_ADDR` | default `:8080` | `cmd/server` |
| `POLL_INTERVAL_SECONDS` | default `1` | `cmd/poller` |
| `QUEUE_DELAY_SECONDS` | `D` in C5; default = `DEBOUNCE_BUCKET_SECONDS` | `STORE=postgres` |

`config.Load` validates per mode: in `postgres` mode `TABLE_NAME`/`QUEUE_URL` are not required and
`DATABASE_URL` is; and vice versa. `STORE` must be one of the two values. Everything else
(`NOTION_*`, `TOKEN_ENC_KEYS`, `DEVICE_TOKEN_PEPPER`, `ALLOWED_EXTENSION_IDS`, `PUBLIC_BASE_URL`,
`DEBOUNCE_BUCKET_SECONDS`, `TRACKING_CONFIG_JSON`) is unchanged.

### 4.2 Binaries

| Binary | Runtime | Role | Status |
|---|---|---|---|
| `cmd/api` | Lambda (Function URL) | API | unchanged |
| `cmd/worker` | Lambda (SQS event source) | worker | unchanged |
| `cmd/local` | laptop | API + in-process queue on DynamoDB Local | unchanged; still dynamo-only |
| `cmd/server` | k8s Deployment | long-running Echo HTTP server | **new** |
| `cmd/poller` | k8s Deployment | long-running loop claiming jobs from Postgres | **new** |
| `cmd/migrate` | laptop / one-off | applies pending SQL migrations, exits | **new** |

`cmd/server` and `cmd/poller` are **Postgres-only**: they fail at startup with a clear error unless
`STORE=postgres`. There is no SQS receive loop outside Lambda and none is added; the dynamo path is
served by `cmd/api`, `cmd/worker` and `cmd/local` exactly as today. Names are the designer's call;
a builder may rename them if the Containerfile and manifest follow.

**Migrations run at process start** of both `cmd/server` and `cmd/poller` (§6.3), not in an init
container. Reason: the deploy gateway updates a Deployment by strategic-merge-patching one named
entry in `spec.containers`; an init container's image would stay frozen at the manifest's tag
forever. `cmd/migrate` exists for laptops and manual runs only.

Both new binaries handle `SIGTERM`: the server drains in-flight requests; the poller finishes the
job it holds (or releases it) and exits. Both expose `GET /healthz` → `200 ok` (the server on
`LISTEN_ADDR`; the poller on a small internal listener, port `8081`, so Kubernetes can probe it).
`/healthz` is unauthenticated and sits outside `/v1`; it checks `SELECT 1` against the pool.

### 4.3 Composition root

`wire.APIDeps`/`WorkerDeps` currently take `*dynamo.Store`. They change to take the **port
interfaces** (`port.Users`, `port.Devices`, …) so either adapter plugs in. `wire` gains two
constructors:

- `wire.Dynamo(ctx, cfg)` → stores + SQS sender + visibility changer (existing code, moved)
- `wire.Postgres(ctx, cfg)` → `*pgxpool.Pool`, the Postgres store, the Postgres queue

Use cases see no difference.

## 5. Queue on Postgres

### 5.1 What SQS FIFO gives today, and the Postgres equivalent

| Guarantee | SQS today | Postgres |
|---|---|---|
| Ordering per group (C4) | `MessageGroupId`; a hidden/in-flight head blocks the whole group | **strict FIFO per group**: a worker may claim only the group's *head* (lowest `id` among its live jobs), and only when that head is ready. A backed-off head parks the group, exactly as SQS does |
| One group processed at a time | FIFO group semantics | follows from head-only claiming; parallelism is across groups |
| Debounce (C5) | `delay_seconds = D` + `MessageDeduplicationId` per bucket | `run_at = now + D`; unique index on `dedup_id` for 5 minutes |
| Requeue inside a bucket not swallowed (R2) | `RequeueDedupID` is unique | same ids, same uniqueness |
| Retry-after on 429/529 (§2.15) | `ChangeMessageVisibility` | `run_at = now + delay`, lease released |
| Transient failure | redelivered after visibility timeout | `run_at = now + backoff(attempts)`, lease released |
| Fail rest of batch (C21) | partial batch response | no batches: one job per claim, and the next job of a group is unreachable until the head is acked, so a failed head blocks its followers by construction |
| Poison / exhausted | DLQ after 10 receives, 14-day retention | `attempts ≥ 10` → `state='dead'`, kept 14 days, then deleted |
| Lost worker | visibility timeout expires | `leased_until` expires (6 minutes, matching today's 360 s) and the job becomes claimable again |

### 5.2 Schema

```sql
CREATE TABLE sync_jobs (
  id           bigserial PRIMARY KEY,
  group_id     text        NOT NULL,            -- Message.GroupID()
  dedup_id     text        NOT NULL,            -- Outgoing.DedupID
  body         jsonb       NOT NULL,            -- queue.Message, same JSON as the SQS body
  state        text        NOT NULL DEFAULT 'ready',  -- ready | leased | dead
  run_at       timestamptz NOT NULL,
  attempts     int         NOT NULL DEFAULT 0,
  leased_until timestamptz,
  created_at   timestamptz NOT NULL DEFAULT now(),
  last_error   text
);
CREATE UNIQUE INDEX sync_jobs_dedup ON sync_jobs (dedup_id);
CREATE INDEX sync_jobs_head  ON sync_jobs (group_id, id) WHERE state <> 'dead';
CREATE INDEX sync_jobs_ready ON sync_jobs (run_at) WHERE state = 'ready';
```

- **Send:** `INSERT … ON CONFLICT (dedup_id) DO NOTHING`. Rows older than 5 minutes that have
  finished (deleted on ack) no longer block the dedup id, which reproduces SQS's 5-minute window:
  completed jobs are **deleted**, not kept, and a job cannot complete before `D` has elapsed, so
  within one bucket the id is always present. A background sweep in the poller deletes `dead` rows
  older than 14 days.
- **Claim** (one statement, in a transaction). Only a group's head is ever a candidate:
  ```sql
  WITH candidate AS (
    SELECT id FROM sync_jobs j
    WHERE (state = 'ready' OR (state = 'leased' AND leased_until <= now()))
      AND run_at <= now()
      AND id = (SELECT min(id) FROM sync_jobs h
                WHERE h.group_id = j.group_id AND h.state <> 'dead')
    ORDER BY run_at, id
    FOR UPDATE SKIP LOCKED
    LIMIT 1)
  UPDATE sync_jobs SET state='leased', leased_until = now() + interval '6 minutes', attempts = attempts + 1
  FROM candidate WHERE sync_jobs.id = candidate.id
  RETURNING sync_jobs.*;
  ```
  Two pollers racing on one group both compute the same head; `FOR UPDATE SKIP LOCKED` lets exactly
  one take it and the other finds no candidate in that group. No advisory lock is needed. Because a
  head that is leased, or backed off to a future `run_at`, is not claimable, every later job of the
  group waits behind it: that is the SQS FIFO behaviour C4/C21 depend on (e.g. the `day` job queued
  after an `article` job never runs before it). The conformance test for C4 runs two pollers against
  one group with a 429 on the head and asserts strict *ordered* processing.
- **Ack:** `DELETE WHERE id = $1`.
- **Fail:** `UPDATE SET state='ready', leased_until=NULL, run_at = now() + $backoff, last_error=$2`.
  Backoff for plain errors: `min(30s × attempts, 5m)`. For `RetryAfterError`: exactly the requested
  delay (already includes the jitter the use case adds). At `attempts ≥ 10`: `state='dead'`.
- Expired leases need no sweeper: the claim query above already treats a `leased` row with
  `leased_until <= now()` as claimable, and the claim's own `UPDATE` renews the lease.

### 5.3 Code shape

- `internal/adapter/queue/message.go`, `syncqueue.go`: **unchanged**; the dedup/group policy is
  reused as-is.
- New `internal/adapter/pgqueue` (an adapter; must not import `adapter/queue`'s SQS or Lambda
  types). It needs `Message`/`Outgoing`/`Sender`, which today live in `adapter/queue` next to the
  SQS code. Move those three plus `GroupID`/`DedupID` into a small leaf package
  `internal/adapter/queue/msg` (or keep them in `queue` and have `pgqueue` import `queue` — the
  archtest forbids cross-adapter imports, so the leaf-package move is the way). `queue.SQS`,
  `queue.Local`, `queue.Consumer` keep working with a type alias.
- `pgqueue.Sender` implements `msg.Sender`. `pgqueue.Poller` runs the loop:
  claim → `port.JobHandler.HandleJob` → ack/fail. Concurrency: `N` goroutines per pod (default 2,
  mirroring today's `maximum_concurrency = 2`), each its own claim loop; group exclusivity comes
  from the database, not from the process.
- The poller sleeps `POLL_INTERVAL_SECONDS` when a claim returns no row. `LISTEN/NOTIFY` is an
  optional later improvement, not part of this design.

## 6. Storage on Postgres

### 6.1 Schema

One table per DynamoDB item type; the row shapes follow §3.1 of the spec one-to-one, with the
same column names as the DynamoDB attributes (snake_case). All timestamps are `timestamptz`.

| Table | Key | Notes |
|---|---|---|
| `users` | `id` | `notion_user_id` unique (replaces `NUSER#` pointer); `active_installation_id`, `deleted_at` |
| `user_installations` | `(user_id, installation_id)` | the `USER#/INST#` pointer |
| `devices` | `(user_id, id)` | `token_hmac` unique (replaces `DEVTOKEN#` pointer) |
| `installations` | `id` (bot id) | `props jsonb`, `token_version int`, status check constraint |
| `articles` | `(installation_id, url_hash)` | counters as `bigint`/`double precision`; `version bigint` |
| `segments` | `(installation_id, id)` | `expires_at` = `started_at + 90 d`; sweep deletes expired rows |
| `days` | `(installation_id, date)` | `article_hashes text[]`; `version bigint` |
| `oauth_states` | `state` | `expires_at`; consume = `DELETE … WHERE expires_at > now() RETURNING` |
| `auth_codes` | `code` | same |

Pointers that DynamoDB modelled as separate items (`NUSER#`, `DEVTOKEN#`) become unique indexes;
the port methods that read them (`UserIDForNotionUser`, `GetDeviceToken`) query those indexes.
`DeleteNotionUserLink(notionUserID, uid)` becomes a conditional `UPDATE users SET notion_user_id = NULL
WHERE id = $uid AND notion_user_id = $nuid` — same "only the owner can unlink" rule the dynamo test
checks.

### 6.2 Write semantics that must match DynamoDB

- **`PushSegment`** is one transaction: insert segment (`ON CONFLICT DO NOTHING` → if no row inserted,
  return `PushDuplicate`); upsert article with `COALESCE(existing, new)` for the `if_not_exists`
  columns and `+=` for `total_seconds`, `sessions` (`+1` only when `seq = 0`), `version`; upsert day
  with `+= seconds`, `version + 1`, `array_append` if not present. Then, **outside** that
  transaction, `UPDATE articles SET max_progress = GREATEST(max_progress, $p)` and
  `last_read_at = GREATEST(last_read_at, $e)`, skipping values that cannot win, exactly as §3.2.
- **`UpdateInstallationTokens(…, oldVersion, …)`**: `UPDATE … WHERE token_version = $old` → 0 rows
  affected maps to the same error the dynamo store returns for a failed condition.
- **`MarkArticleSynced` / `MarkDaySynced`**: `WHERE notion_synced_version IS NULL OR notion_synced_version < $v`.
- **`ConsumeOAuthState` / `ConsumeAuthCode`**: single `DELETE … RETURNING` with the expiry check in
  the `WHERE`, so a code is consumed at most once (C12).
- **`DeleteInstallationData` / `DeleteUserData`**: cascading deletes in one transaction.
- **TTL-style expiry** (segments 90 d, oauth states 10 min, auth codes 2 min) is enforced on read
  (as today) and swept by the poller's housekeeping tick (hourly `DELETE … WHERE expires_at < now()`).
- **Throttle tagging:** the dynamo store wraps capacity errors so the use case can map them to
  `PushRetry`. The Postgres store does the same for `serialization_failure` (`40001`),
  `deadlock_detected` (`40P01`) and `too_many_connections` (`53300`), after one internal retry.
- Error mapping to `domain`/`apperr` sentinel errors (not found, conflict) must match the dynamo
  adapter; the conformance suite is the arbiter.

### 6.3 Migrations

- Plain SQL files under `backend/migrations/NNNN_name.sql`, embedded with `embed.FS`, applied
  forward-only by a small `platform/pgmigrate` package using a
  `schema_migrations(version int primary key, applied_at)` table and `pg_advisory_lock`, so the
  server and poller pods starting at once do not race.
- `cmd/server` and `cmd/poller` call it before serving/polling and exit non-zero if it fails, so a
  bad migration surfaces as a failed rollout (the gateway reports it) rather than a half-upgraded
  pod. `cmd/migrate` wraps the same package for laptops (`make pg-migrate`).
- No down migrations. A mistaken migration is fixed by a new one.

### 6.4 Conformance tests

- The dynamo adapter's store tests (`identity_test`, `installations_test`, `oauth_test`,
  `push_test`, `maintenance_test`) move into a shared package
  `internal/adapter/storetest` as functions `func TestXxx(t *testing.T, newStore func(t) Store)`
  where `Store` is the union of the port interfaces plus the purger. Each adapter's `_test.go` calls
  them with its own constructor. Assertions keep their meaning (the clean-architecture plan's "moved,
  not weakened" rule applies).
- Dynamo-specific tests (`consistency_test`, `throttle_test`, `records_internal_test`, the
  `BatchDelete` partition test) stay where they are.
- Postgres tests run against a throwaway database created per test (`CREATE DATABASE` from a
  template, dropped at cleanup) on a local Postgres started by `make pg-up` (Podman,
  `docker.io/library/postgres:17`). `PG_TEST_URL` overrides the default URL. The tests `t.Skip`
  when Postgres is not reachable, matching how the dynamo tests behave without DynamoDB Local.
- The queue gets its own tests in `pgqueue`: debounce collapses within a bucket, requeue inside a
  bucket is delivered, retry-after delays, exhausted → dead, and the two-poller C4 serial test.
- The httpapi and e2e suites currently construct `dynamotest.Env` + `queue.Recorder`. They gain a
  build-tag or env switch so the **same** e2e suite runs once on dynamo and once on Postgres in CI.

## 7. Local development

- `make pg-up` / `make pg-down`: Postgres 17 in Podman on `:5432`, user/db `reading`.
- `make run-pg`: `STORE=postgres` + `DATABASE_URL=postgres://reading:reading@localhost:5432/reading?sslmode=disable`,
  runs `cmd/server` and `cmd/poller` in one shell (two processes; each migrates on start).
- `.env.local.example` gains the Postgres block, commented, under the DynamoDB block.
- `make ddb-up` / `make run-local` keep working untouched.

## 8. Container image and CI/CD

Follows the `deploying-to-tuncloud` skill. Concretely:

- **`backend/Containerfile`**: multi-stage from the skill's template; builds `/server` and
  `/poller` into `gcr.io/distroless/static-debian12:nonroot`. `ENTRYPOINT ["/server"]`. Only the
  two k8s binaries are in the image; the Lambda binaries and `cmd/migrate` are not.
- **`.github/workflows/backend-image.yml`** (new; the repo has no workflows yet):
  - `test` job on `ubuntu-24.04-arm`: `gofmt -l`, `go vet`, `go test -race ./...` with a Postgres 17
    service container (so the Postgres conformance tests run, not skip). DynamoDB Local also runs as
    a service so the dynamo tests run too.
  - `build` job: `tuncloud/workflow/.github/workflows/ci-build-push-ghcr.yml@main`, `image-name:
    reading-tracker`, `context: backend`, `dockerfile: backend/Containerfile`,
    `platforms: linux/arm64`, `tag-prefix: backend/`, `commit-path: backend`. Image:
    `ghcr.io/tuncloud/reading-tracker:vX.Y.Z`.
  - `deploy-poller` then `deploy-server`: `tuncloud/workflow/.github/workflows/rollout-deployment.yml@main`,
    namespace `reading-tracker`, deployments `reading-tracker-poller` / `reading-tracker-server`,
    containers `poller` / `server`. Poller first, so new job-handling code is live before the API
    enqueues for it.
  - Triggers: push to `main` touching `backend/**` or the workflow file; `workflow_dispatch`.
- The repo is published under **`github.com/tuncloud/reading-chrome-extension`**.

## 9. Kubernetes manifest (`backend/deploy/manifest.yaml`)

From the skill's `manifest.template.yaml` with these specifics:

| Object | Specifics |
|---|---|
| Namespace | `reading-tracker`, no `istio-injection` label |
| ConfigMap `reading-tracker-config` | `APP_ENV=prod`, `STORE=postgres`, `LISTEN_ADDR=:8080`, `NOTION_VERSION=2025-09-03`, `PUBLIC_BASE_URL=https://reading.tuando.app`, `DEBOUNCE_BUCKET_SECONDS=120`, `QUEUE_DELAY_SECONDS=120` (the AWS production values; `account.Delete` relies on `D ≥ 120 s`), `ALLOWED_EXTENSION_IDS=<store extension id>`, `TRACKING_CONFIG_JSON` (optional) |
| ExternalSecret `reading-tracker-secret` | param `/reading-tracker/reading-tracker` → keys `NOTION_CLIENT_ID`, `NOTION_CLIENT_SECRET`, `TOKEN_ENC_KEYS`, `DEVICE_TOKEN_PEPPER` |
| ExternalSecret `reading-tracker-db-secret` | param `/reading-tracker/reading-tracker-db` → key `DATABASE_URL` (host `postgres-17.tuando.app`, `sslmode` per the server's setting, `pool_max_conns=5`) |
| Deployment `reading-tracker-server` | `command: ["/server"]`, single container (migrates on start), port 8080, readiness+liveness `/healthz`, 1 replica, requests 10m/32Mi, limits 200m/128Mi |
| Deployment `reading-tracker-poller` | `command: ["/poller"]`, single container, liveness `/healthz` on 8081, 1 replica (may be raised; the queue is safe for N pollers), same resources |
| Service `reading-tracker-server` | 80 → http |
| Ingress `reading-tracker-server` | class `nginx`, host `reading.tuando.app`, no `tls:` block; CORS via nginx annotations mirroring the Function URL: `enable-cors: "true"`, `cors-allow-origin: chrome-extension://<id>` (one per allowed id), `cors-allow-methods: GET, POST, DELETE`, `cors-allow-headers: authorization, content-type`, `cors-max-age: "3600"` |

No init containers anywhere (see §4.2). CORS stays outside the app in both modes: the Function URL
serves it on AWS, the Ingress serves it on Kubernetes; `httpapi` keeps doing none. (An MV3 service
worker with `host_permissions` is exempt from CORS anyway; the annotations keep parity, not
correctness.)

## 10. Operator hand-off (documented in `backend/README.md`, "Deploy to tuncloud")

In order:
1. On `worker`: `CREATE ROLE reading_tracker LOGIN PASSWORD '…'; CREATE DATABASE reading_tracker OWNER reading_tracker;`
2. Parameter Store (`ap-southeast-1`, SecureString, JSON object): the two params and their key
   names from §9. Values never appear in the repo.
3. DNS: `reading.tuando.app` → the ingress IP (same as `gateway.tuando.app`).
4. Notion integration: add `https://reading.tuando.app/v1/oauth/notion/callback` as a redirect URI.
5. `ssh master 'sudo kubectl --kubeconfig=/etc/kubernetes/admin.conf apply -f -' < backend/deploy/manifest.yaml`.
6. Push to `main` → first image; make the `reading-tracker` GHCR package public.
7. In `tuncloud-deployment-config/deploy-gateway/manifest.yaml`, add
   `tuncloud/reading-chrome-extension` → `deployment.rollout` on namespace `reading-tracker`,
   deployments `reading-tracker-server`, `reading-tracker-poller`; apply and restart the gateway.
8. Re-run the workflow; verify `externalsecret` is `SecretSynced` and
   `curl https://reading.tuando.app/healthz`.
9. Build the extension with `VITE_API_BASE=https://reading.tuando.app`.

## 11. Documentation updates

- `backend/README.md`: the two modes, local Postgres loop, deploy section.
- `docs/runbook.md`: a "Kubernetes (Postgres)" section next to the AWS one.
- `docs/reading-tracker-agent-spec.md` §3.5: add the new variables; note that §3.1/§3.2 describe
  the DynamoDB mapping and that §6 of this design is the Postgres mapping.
- `infra/README.md`: one line pointing at this design for the non-AWS path.

## 12. Decisions already made (and by whom)

| Decision | Who |
|---|---|
| Postgres as both store and queue; zero AWS calls in the app in k8s mode | user |
| AWS deployment stays supported | user |
| Existing cluster + `postgres-17` on `worker` | user |
| GitHub owner `tuncloud` | user |
| Skill lives in `tuncloud-deployment-config/.claude/skills/` (done) | user |
| Hostname `reading.tuando.app`, namespace `reading-tracker`, binary names `server`/`poller`/`migrate` | designer (rename freely) |
| `STORE` env var with `dynamo` default; `cmd/server`/`cmd/poller` are Postgres-only | designer |
| Strict FIFO per group (head-only claiming); a backed-off head parks its group | spec C4/C21 — the SQS behaviour the use cases assume |
| Job table design in §5: 6-minute lease, 10 attempts, 14-day dead retention | designer, mirroring today's SQS/DLQ settings |
| Migrations at process start, not an init container | forced by the deploy gateway (patches only the named app container) |
| `DEBOUNCE_BUCKET_SECONDS=120`, `QUEUE_DELAY_SECONDS=120` | designer, matching AWS production and `account.Delete`'s assumption |
| CORS at the Ingress, not in `httpapi` | designer, keeping parity with the Function URL and the "no API change" non-goal |
| pgx + hand-written SQL; embedded forward-only migrations; no ORM | designer |
| Shared conformance suite in `internal/adapter/storetest` | designer |
| Poller concurrency 2 per pod; 1 replica to start | designer, mirroring `maximum_concurrency = 2` |

## 13. Left to the builder

- Exact column types and indexes beyond those named; `sslmode` choice once the server's TLS setup
  is known (ask the operator; default to `require` if the server supports it, else `disable` over
  the private network).
- Whether `healthz` on the poller is a tiny `net/http` server or reuses Echo.
- pgx version and whether to use `pgxpool` directly or `database/sql`; `pgxpool` is recommended.
- The order of tasks in the implementation plan. A sensible order: ports in `wire` → `msg` leaf
  package → Postgres store + conformance suite → `pgqueue` → `cmd/*` → Containerfile/workflow/manifest
  → docs.
- Log fields for the poller (job id, group, attempt, outcome) — keep the existing rule that logs
  never contain tokens or query strings.
