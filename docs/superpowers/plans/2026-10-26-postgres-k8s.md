# Postgres Store + Queue and Kubernetes Deployment Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a second way to run the backend: Postgres as both store and job queue, running as two long-lived processes on the tuncloud Kubernetes cluster, while the AWS (DynamoDB + SQS + Lambda) deployment keeps working unchanged.

**Architecture:** The use cases already depend only on `internal/usecase/port`. We add a Postgres adapter for every storage port (`internal/adapter/postgres`) and a Postgres job queue (`internal/adapter/queue/pgqueue`), both checked against shared conformance tests. The composition root is split so the k8s binaries (`cmd/server`, `cmd/poller`) link no AWS code at all. Migrations are embedded SQL that each binary applies on start.

**Tech Stack:** Go 1.26 (module `reading-tracker/backend`), Echo v4, `github.com/jackc/pgx/v5` v5.11.0 (`pgxpool`), PostgreSQL 17 (Podman locally, `postgres-17.tuando.app` in the cluster), GitHub Actions with the `tuncloud/workflow` reusable workflows, distroless arm64 image.

**Spec:** [`docs/superpowers/specs/2026-10-25-postgres-k8s-design.md`](../specs/2026-10-25-postgres-k8s-design.md) (approved). Behaviour authority stays [`docs/reading-tracker-agent-spec.md`](../../reading-tracker-agent-spec.md). Cluster conventions: `../tuncloud-deployment-config/.claude/skills/deploying-to-tuncloud/` (SKILL.md and templates). Read all three.

## Global Constraints

- **The AWS path does not change.** Every HTTP route, status code, JSON body, SQS message body, DynamoDB key and attribute name, Lambda entrypoint (`cmd/api`, `cmd/worker`), `cmd/local`, and Terraform file stays byte-for-byte compatible. `STORE` unset means `dynamo`.
- **Use cases and domain do not change.** No edits under `internal/usecase/**` or `internal/domain/**`, except that tests may change imports.
- **The suite is green after every task.** `cd backend && gofmt -l . ; go vet ./... && go test -race ./...`: gofmt prints nothing and every package is `ok`. Needs `make ddb-up` (from Task 3 on, also `make pg-up`). Podman only, never Docker.
- **Existing tests are moved, not weakened.** A moved test keeps the meaning of its assertions. Any assertion that has to change goes in the task report with a reason.
- **Architecture rules (`internal/archtest`) hold.** An adapter never imports another adapter root (`internal/adapter/<root>`). Only `cmd/*` imports `internal/wire/...`. Platform imports only platform and third-party code.
- **k8s binaries link no AWS code:** `go list -deps ./cmd/server ./cmd/poller ./cmd/migrate | grep -E 'aws-sdk-go|aws-lambda-go|smithy-go'` prints nothing.
- **Spec constraints that bind in Postgres mode:** C2 (each segment counted exactly once), C4 (one installation's jobs run in order, one at a time), C5 (`D ≥ B`, `B ≤ 300`), C7 (`token_version` guard), C12 (OAuth state and auth codes are single-use and expire), C21 (a failed job blocks the jobs behind it), §2.15 (429/529 → hide for `Retry-After + rand(0..3) s`). Logs never contain tokens or query strings.
- **Queue values:** lease 6 min; max 10 attempts, then `state='dead'`; dead rows kept 14 days; backoff for plain errors `min(30 s × attempts, 5 min)`; a `RetryAfterError` waits exactly its `Delay`; 2 claim goroutines per poller.
- **Pool:** at most 5 connections per pod unless `DATABASE_URL` sets `pool_max_conns`.
- **Image and deploy:** `ghcr.io/tuncloud/reading-tracker`, `linux/arm64`, UID 65532, read-only root filesystem. Namespace `reading-tracker`. Deployments `reading-tracker-server` (container `server`) and `reading-tracker-poller` (container `poller`). Host `reading.tuando.app`. No init containers.
- **Tooling:** use `perl -pi -e` for in-place rewrites (macOS `sed` has no `\b`). For imports: `go run golang.org/x/tools/cmd/goimports@v0.34.0 -w <files>`.

## Calls this plan makes where the design was silent or wrong

1. **The Postgres schema keeps DynamoDB's pointer items as tables** (`notion_user_links`, `device_tokens`, `user_installations`). It does not use unique indexes on `users.notion_user_id` or `devices.token_hmac` as design §6.1 says. Two flows need the pointers: `CreateUser` overwrites the Notion-user link when someone reconnects after deleting their account, and `RevokeDevice` and `DeleteDeviceToken` remove the token lookup while the device row stays. A unique index would break both.
2. **`PushSegment` is one transaction.** The max_progress/last_read_at "raise" runs inside that transaction, on both the accepted and the duplicate path. Spec §3.2 only splits it out because DynamoDB can't express it. The behaviour stays the same: a duplicate still heals.
3. **Migrations live at `internal/platform/pgmigrate/migrations/*.sql`, not `backend/migrations/`,** because `embed` can't reach a parent directory.
4. **`pgqueue` lives under `internal/adapter/queue/`, and the transport-free queue code moves to `internal/adapter/queue/msg`.** The archtest only allows imports inside one adapter root. `queue` keeps type and function aliases, so callers don't change.
5. **The composition root is split.** `internal/wire` is AWS-free. `internal/wire/awswire` holds the AWS clients, SQS and the Lambda consumer. This is what makes the "links no AWS code" constraint true.
6. **The Postgres tests fail, rather than skip, when Postgres is unreachable,** the same as `dynamotest`. Design §6.4 says the dynamo tests skip, but they don't.
7. **In Postgres mode both roles read `DEBOUNCE_BUCKET_SECONDS`.** The poller's requeues need `D` too, and `QUEUE_DELAY_SECONDS` defaults to the bucket.
8. **The httpapi tests stay dynamo-only.** They inspect raw DynamoDB items. The `internal/e2e` suite runs on both stores, which is the parity check the design asks for.

## Review Focus

1. **A poller dies mid-job:** after the 6-minute lease expires, the job is claimable again and still blocks its group until then. Test `TestExpiredLeaseIsReclaimed` (Task 7).
2. **SIGTERM while a job runs:** the in-flight job finishes and is acked. It must not sit leased for 6 minutes, and no new job is claimed. Test `TestRunFinishesInFlightJobOnShutdown` (Task 7).
3. **Someone reconnects Notion after deleting their account,** so a second `CreateUser` for the same Notion user arrives: the lookup returns the newest user, and purging the old user doesn't remove the new link. Test `TestCreateUserRelinksNotionUser` (Task 4, conformance, so both stores must pass).
4. **Postgres is unreachable at startup or drops later:** the binary exits non-zero if migration fails, `/healthz` returns 503 while the pool can't ping, and the poller backs off without crashing. Tests `TestHealthzReports503WhenDatabaseDown` and `TestPollerSurvivesClaimError` (Tasks 8 and 7).
5. **A record with every optional field empty:** `""` timestamps and empty `props` round-trip as `""` and `nil`, not as `"0001-01-01T00:00:00Z"`. Test `TestEmptyOptionalFieldsRoundTrip` (Task 4, conformance).

---

### Task 1: Make the composition root AWS-free

**Files:**
- Create: `backend/internal/adapter/queue/msg/message.go` (moved from `queue/message.go`), `msg/syncqueue.go` (moved from `queue/syncqueue.go`), `msg/recorder.go` (moved from `queue/recorder.go`), `msg/msg_test.go` (the transport-free tests moved from `queue/queue_test.go` and `queue/syncqueue_test.go`)
- Create: `backend/internal/adapter/queue/aliases.go`
- Create: `backend/internal/wire/awswire/awswire.go`
- Modify: `backend/internal/wire/app.go`, `backend/internal/adapter/queue/consumer.go`, `backend/cmd/{api,worker,local}/main.go`, `backend/internal/e2e/e2e_test.go`, `backend/internal/adapter/httpapi/*_test.go` (imports only)

**Interfaces:**
- Produces, in package `msg`: `Message`, `Type`, `TypeArticle|TypeDay|TypePurge`, `Article(inst, hash) Message`, `Day(inst, date) Message`, `Purge(uid, inst) Message`, `(Message) GroupID() string`, `(Message) DedupID(now, bucket) string`, `(Message) RequeueDedupID(now) string`, `(Message) Job() port.Job` (exported; it was `job()`), `Outgoing`, `Bucketed(...)`, `Sender`, `SyncQueue{Sender, Bucket, Now}`, `Recorder`.
- Produces, in package `queue`: aliases `type Message = msg.Message`, `Outgoing`, `Sender`, `SyncQueue`, `Recorder`, `Type`. Constants and function aliases (`var Article = msg.Article`, etc.) so every existing caller compiles unchanged.
- Produces, in package `wire`:
  - `type Store interface { port.Users; port.Devices; port.OAuthGrants; port.Installations; port.Reading; port.DataPurger }`
  - `APIDeps.Store Store`, `APIDeps.Queue msg.Sender`
  - `func NewAPI(APIDeps) *echo.Echo` and `func API(cfg, Store, msg.Sender, *slog.Logger) *echo.Echo`
  - `type SyncDeps struct { Store Store; Notion *notion.Client; Keys *crypto.Keyring; Queue msg.Sender; Tracking config.Tracking; DebounceBucket time.Duration; Now func() time.Time; Jitter func() time.Duration; Sleep func(time.Duration); Log *slog.Logger }`
  - `func NewJobHandler(SyncDeps) port.JobHandler`, which builds today's `notionsync.Service`
  - `func JobHandler(cfg *config.Config, st Store, q msg.Sender, log *slog.Logger) port.JobHandler`, the production one with the 350 ms paced Notion client
  - `Logger()`, `NotionClient(...)`
- Produces, in package `awswire`:
  - `AWSConfig(ctx)`, `DynamoDB(aws.Config)`, `SQS(aws.Config)`
  - `type WorkerDeps struct { SyncDeps wire.SyncDeps; Visibility queue.VisibilityChanger }`
  - `func NewWorker(WorkerDeps) *queue.Consumer`
  - `func Worker(cfg, st wire.Store, q queue.Sender, vis queue.VisibilityChanger, log) *queue.Consumer`

- [ ] **Step 1: Move the three files and their pure tests into `msg`.** Use `git mv` so history follows. Change `package queue` to `package msg`. Export `job()` as `Job()`; `messageFor` stays unexported in `msg`. The tests that move to `msg/msg_test.go`: `TestDedupIDBuckets`, `TestRequeueDedupIDNeverCollides`, `TestGroupIDs`, `TestMessageJSONShape`, `TestRecorder`, `TestEnqueueSyncDebouncesArticlesThenDays`, `TestResyncIDsNeverCollideWithPushIDs`, `TestRequeueAndPurge`. `TestResyncAfterPushIsDeliveredByLocalQueue` and every SQS, Local and Consumer test stay in `queue`.
- [ ] **Step 2: Add `queue/aliases.go`** so `queue.Article`, `queue.Bucketed`, `queue.SyncQueue{...}`, `&queue.Recorder{}` and the other moved names keep working. `consumer.go` calls `m.Job()`.
- [ ] **Step 3: Split `wire`.** `app.go` keeps `Logger`, `NotionClient`, `Store`, `APIDeps`/`NewAPI`/`API` and `SyncDeps`/`NewJobHandler`/`JobHandler`, and imports neither `dynamo` nor `queue`. `awswire.go` gets `AWSConfig`, `DynamoDB`, `SQS`, `WorkerDeps`, `NewWorker` and `Worker`. `NewWorker` wraps `wire.NewJobHandler(d.SyncDeps)` in `&queue.Consumer{Handler, Visibility, Log}`. Update `cmd/api`, `cmd/worker` and `cmd/local`, plus the e2e `newSystem` (it builds the worker through `awswire.NewWorker`).
- [ ] **Step 4: Verify.**
  - `cd backend && gofmt -l . ; go vet ./... && go test -race ./...`: all `ok`.
  - `go list -deps ./internal/wire | grep -E 'aws-sdk-go|aws-lambda-go|smithy-go|adapter/dynamo|adapter/queue$'` prints nothing.
  - `git diff --stat -- internal/usecase internal/domain` is empty.
- [ ] **Step 5: Commit** `refactor(backend): AWS-free composition root; transport-free queue code in queue/msg`

### Task 2: `STORE`, `DATABASE_URL`, `QUEUE_DELAY_SECONDS` in config

**Files:**
- Modify: `backend/internal/platform/config/config.go`, `config_test.go`

**Interfaces:**
- Produces: `const StoreDynamo = "dynamo"`, `const StorePostgres = "postgres"`; new `Config` fields `Store string`, `DatabaseURL string`, `QueueDelay time.Duration` (zero in dynamo mode).

- [ ] **Step 1: Write the failing tests** in `config_test.go`, using a helper `pgEnv(role)` that drops `TABLE_NAME`/`QUEUE_URL` from `apiEnv()` and adds `STORE=postgres`, `DATABASE_URL=postgres://u:p@h:5432/d`:
  - `TestStoreDefaultsToDynamo`: `apiEnv()` → `c.Store == "dynamo"`.
  - `TestPostgresModeDoesNotNeedTableOrQueue`: `pgEnv(RoleAPI)` loads. `c.Store == "postgres"`, `c.DatabaseURL` is set, `c.QueueDelay == 120*time.Second` (defaults to the bucket).
  - `TestPostgresModeNeedsDatabaseURL`: no `DATABASE_URL` → the error contains `missing env DATABASE_URL`.
  - `TestDynamoModeStillNeedsTableAndQueue`: `apiEnv()` minus `TABLE_NAME` → the error contains `TABLE_NAME`.
  - `TestUnknownStoreRejected`: `STORE=mysql` → the error contains `STORE must be dynamo or postgres`.
  - `TestQueueDelayMustCoverBucket`: `QUEUE_DELAY_SECONDS=60` with bucket 120 → the error contains `QUEUE_DELAY_SECONDS must be >= DEBOUNCE_BUCKET_SECONDS (C5)`. `QUEUE_DELAY_SECONDS=901` → the error contains `<= 900`.
  - `TestPostgresWorkerReadsBucket`: in `pgEnv(RoleWorker)`, `DEBOUNCE_BUCKET_SECONDS` is required and `c.DebounceBucket == 120*time.Second`.
- [ ] **Step 2: Run** `go test ./internal/platform/config/` → the new tests FAIL.
- [ ] **Step 3: Implement it in `Load`.**
  - Read `STORE`, trimmed and lowercased; empty means `dynamo`.
  - `TABLE_NAME` and `QUEUE_URL` are required only for dynamo; `DATABASE_URL` only for postgres.
  - In postgres mode, read and validate `DEBOUNCE_BUCKET_SECONDS` for both roles (1..300, same message as today).
  - `QUEUE_DELAY_SECONDS` defaults to the bucket and must be `>= bucket` and `<= 900`.
  - The dynamo-mode behaviour and messages stay exactly as they are.
  - Update the package doc ("Lambda environment" → "process environment").
- [ ] **Step 4: Run** `go test ./internal/platform/config/` → PASS.
- [ ] **Step 5: Commit** `feat(config): STORE=postgres mode with DATABASE_URL and QUEUE_DELAY_SECONDS`

### Task 3: Embedded migrations, schema, local Postgres and the test helper

**Files:**
- Create: `backend/internal/platform/pgmigrate/pgmigrate.go`, `pgmigrate_test.go`, `migrations/0001_store.sql`, `migrations/0002_sync_jobs.sql`
- Create: `backend/internal/platform/pgtest/pgtest.go`
- Modify: `backend/Makefile` (`pg-up`, `pg-down`), `backend/go.mod`, `go.sum` (`github.com/jackc/pgx/v5 v5.11.0`)

**Interfaces:**
- Produces: `pgmigrate.Up(ctx context.Context, pool *pgxpool.Pool) error`. It takes `pg_advisory_lock(727274)` (any fixed constant, named `lockKey`), creates `schema_migrations(version int primary key, applied_at timestamptz not null default now())` if missing, then applies each embedded file whose `NNNN` prefix isn't recorded. Each file runs in its own transaction together with its `schema_migrations` insert. A file with a non-numeric prefix is an error.
- Produces:
  - `pgtest.URL() string`: `PG_TEST_URL`, or `postgres://reading:reading@localhost:5432/reading?sslmode=disable`.
  - `pgtest.New(t testing.TB) *Env`, with `type Env struct { Pool *pgxpool.Pool; URL string }`. `New` creates a fresh, fully migrated database per test and drops it (`WITH (FORCE)`) on cleanup. It fails the test, without skipping, when Postgres is unreachable, with the message `Postgres not reachable at <url> (run `make pg-up`)`. Recommended: migrate one template database per test binary (`sync.Once`), then `CREATE DATABASE … TEMPLATE …` for each test.

Schema (`0001_store.sql`). Column names equal the DynamoDB attribute names. Every `*_at` column is `timestamptz` except `published_at`, which is `text` because it is source-supplied. Nullable means "absent in DynamoDB".

```sql
CREATE TABLE users (id text PRIMARY KEY, notion_user_id text NOT NULL, name text NOT NULL, email text,
  active_installation_id text, created_at timestamptz NOT NULL, deleted_at timestamptz);
CREATE TABLE notion_user_links (notion_user_id text PRIMARY KEY, user_id text NOT NULL);
CREATE TABLE user_installations (user_id text, installation_id text, created_at timestamptz NOT NULL,
  PRIMARY KEY (user_id, installation_id));
CREATE TABLE devices (user_id text, id text, name text NOT NULL, token_hmac text NOT NULL,
  created_at timestamptz NOT NULL, last_seen_at timestamptz, revoked_at timestamptz, PRIMARY KEY (user_id, id));
CREATE TABLE device_tokens (token_hmac text PRIMARY KEY, user_id text NOT NULL, device_id text NOT NULL);
CREATE TABLE oauth_states (state text PRIMARY KEY, ext_redirect text NOT NULL, code_challenge text NOT NULL,
  expires_at timestamptz NOT NULL);
CREATE TABLE auth_codes (code text PRIMARY KEY, user_id text NOT NULL, installation_id text NOT NULL,
  code_challenge text NOT NULL, ext_redirect text NOT NULL, expires_at timestamptz NOT NULL);
CREATE TABLE installations (id text PRIMARY KEY, user_id text NOT NULL, workspace_id text NOT NULL,
  workspace_name text NOT NULL, workspace_icon text, status text NOT NULL
    CHECK (status IN ('active','needs_setup','revoked')), setup_error text,
  access_token_enc text NOT NULL, refresh_token_enc text NOT NULL, token_expires_at text,
  token_version int NOT NULL, duplicated_template_id text, library_db_id text, library_ds_id text,
  library_url text, daily_db_id text, daily_ds_id text, props jsonb,
  created_at timestamptz NOT NULL, updated_at timestamptz NOT NULL);
CREATE TABLE articles (installation_id text, url_hash text, canonical_url text NOT NULL, source text NOT NULL,
  title text NOT NULL, author text NOT NULL, site_name text NOT NULL, word_count int NOT NULL,
  published_at text NOT NULL, first_read_at timestamptz, last_read_at timestamptz,
  total_seconds bigint NOT NULL DEFAULT 0, sessions bigint NOT NULL DEFAULT 0,
  max_progress double precision, completed boolean NOT NULL DEFAULT false, version bigint NOT NULL DEFAULT 0,
  notion_page_id text, notion_url text, notion_ds_id text, notion_synced_version bigint,
  notion_synced_at timestamptz, PRIMARY KEY (installation_id, url_hash));
CREATE TABLE segments (installation_id text, id text, session_id text NOT NULL, seq int NOT NULL,
  url_hash text NOT NULL, device_id text NOT NULL, started_at timestamptz NOT NULL, ended_at timestamptz NOT NULL,
  active_seconds int NOT NULL, max_progress double precision NOT NULL, expires_at timestamptz NOT NULL,
  PRIMARY KEY (installation_id, id));
CREATE TABLE days (installation_id text, date text, seconds bigint NOT NULL DEFAULT 0,
  article_hashes text[] NOT NULL DEFAULT '{}', version bigint NOT NULL DEFAULT 0, notion_page_id text,
  notion_ds_id text, notion_synced_version bigint, PRIMARY KEY (installation_id, date));
CREATE INDEX segments_expiry ON segments (expires_at);
```

`0002_sync_jobs.sql` is design §5.2 verbatim: the table, `sync_jobs_dedup` (unique), `sync_jobs_head`, `sync_jobs_ready`.

- [ ] **Step 1: Add `pg-up` and `pg-down` to the Makefile,** mirroring `ddb-up`:
  - `podman container exists reading-pg || podman run -d --rm --name reading-pg -p 5432:5432 -e POSTGRES_USER=reading -e POSTGRES_PASSWORD=reading -e POSTGRES_DB=reading docker.io/library/postgres:17`
  - then a loop of up to 30 one-second tries of `podman exec reading-pg pg_isready -U reading`.
  - Add both targets to `.PHONY`. Run `make pg-up`.
- [ ] **Step 2: Write the failing tests** in `pgmigrate_test.go`, using `pgtest.New`, whose template step already migrated:
  - `TestUpCreatesEveryTable`: `to_regclass` is non-null for all 11 tables plus `sync_jobs`.
  - `TestUpIsIdempotent`: calling `pgmigrate.Up` twice more returns nil, and `schema_migrations` has exactly 2 rows.
  - `TestConcurrentUpIsSafe`: 4 goroutines run `Up` on a fresh, unmigrated database (create it directly in the test); all return nil and 2 rows are recorded.
- [ ] **Step 3: Run** `go test ./internal/platform/pgmigrate/` → FAIL (package missing).
- [ ] **Step 4: Implement** `pgmigrate.Up` and `pgtest`, and write the two SQL files.
- [ ] **Step 5: Run** `go test ./internal/platform/pgmigrate/` → PASS. Run the full suite → all `ok`; `archtest` passes (platform imports only platform and pgx).
- [ ] **Step 6: Commit** `feat(backend): embedded Postgres migrations, schema, pgtest helper, make pg-up`

### Task 4: Shared store conformance suite (dynamo first)

**Files:**
- Create: `backend/internal/adapter/storetest/storetest.go`, `identity.go`, `reading.go`
- Modify: `backend/internal/adapter/dynamo/{identity,installations,oauth,push,maintenance}_test.go` (the moved tests are removed), plus a new `backend/internal/adapter/dynamo/conformance_test.go`

**Interfaces:**
- Produces:
  - `type IdentityStore interface { port.Users; port.Devices; port.OAuthGrants; port.Installations }`
  - `type ReadingStore interface { IdentityStore; port.Reading; port.DataPurger }`
  - `func RunIdentity(t *testing.T, newStore func(t *testing.T) IdentityStore)`
  - `func RunReading(t *testing.T, newStore func(t *testing.T) ReadingStore)`
  - Each runs every check as a `t.Run` subtest with a fresh store, and both live in non-`_test` files so other packages' tests can import them.

- [ ] **Step 1: Move these tests into `storetest`,** unchanged in meaning, with `dynamotest.New(t).Store` replaced by `newStore(t)`:
  - Into `RunIdentity`: `CreateUserAndResolveByNotionUser`, `DeviceLifecycle`, `TouchDeviceWritesAtMostHourly`, `InstallationTokensVersionGuard`, `SaveInstallationDatabasesActivates`, `UserInstallationPointers`, `OAuthStateConsumedOnce`, `ExpiredAuthCodeRejected`, `AuthCodeRoundTrip`, `DeleteNotionUserLinkOnlyForOwner`.
  - Into `RunReading`: `PushSegmentTwiceCountsOnce`, `MaxProgressNeverDecreases`, `OutOfOrderSessionCountedOnce`, `MarkArticleSyncedIsMonotonic`, `ArticleAndDayPageLinks`, `PendingSync`, `PendingSyncAllIncludesSynced`.
  - Also move the helpers `segment`, `push`, `must`, `articleURL` and `testNow`.
  - These stay in `dynamo`: `consistency_test`, `throttle_test`, `records_internal_test`, `TestBatchDeleteRemovesWholePartition`, `TestPushThrottleIsRetry`, `TestPushRetriesTransactionConflict`, `TestPostCommit*`, `TestDuplicateReportsRetryWhenHealFails`, `TestPushConflictRetryBuildsFreshInput`, `TestBatchGetArticlesOneRoundTripFor100`.
- [ ] **Step 2: Add the new conformance checks** (these pin Review Focus 3 and 5, plus purge). All four go in `RunReading`, because they need `port.Reading` or `port.DataPurger`:
  - `CreateUserRelinksNotionUser`: `CreateUser("u1", n1)`, then `CreateUser("u2", n1)` → `UserIDForNotionUser(n1) == "u2"`. Then `DeleteUserData("u1")` + `DeleteNotionUserLink(n1, "u1")` → still `"u2"`.
  - `EmptyOptionalFieldsRoundTrip`:
    - a user with no `Email`, `ActiveInstallationID` or `DeletedAt` reads back with those as `""`;
    - an installation with empty `SetupError`, `TokenExpiresAt` and `WorkspaceIcon`, and `Props == nil`, reads back with `""`, `""`, `""` and `len(Props) == 0`;
    - an article pushed with `MaxProgress 0` has `MaxProgress == 0`, `NotionPageID == ""` and `NotionSyncedAt == ""`.
  - `PurgeRemovesInstallationAndUserData`:
    - push 2 segments for `bot-1`, create user `u1` with device `d1`, `PutUserInstallation(u1, bot-1)`, `PutInstallation(bot-1)`;
    - `DeleteInstallationData(bot-1)` → `GetInstallation`, `GetArticle` and `GetDay` all return `domain.ErrNotFound`, and `PendingSync(bot-1, true)` returns empty;
    - `DeleteUserData(u1)` → `GetProfile` and `GetDevice` return `ErrNotFound`, and `ListUserInstallationIDs` and `ListDevices` return empty.
  - `BatchGetArticlesReturnsOnlyExisting`: 1 pushed hash plus 2 unknown hashes, one of them duplicated in the input → a map of exactly 1 entry.
- [ ] **Step 3: Add `dynamo/conformance_test.go`** with `TestConformanceIdentity` and `TestConformanceReading`, each calling `storetest.Run*` with `func(t) … { return dynamotest.New(t).Store }`.
- [ ] **Step 4: Run** `go test ./internal/adapter/dynamo/ ./internal/adapter/storetest/` → PASS. The new checks pass on dynamo. If one doesn't, that is a finding about the dynamo store: stop and report it rather than weakening the check.
- [ ] **Step 5: Commit** `test(backend): shared store conformance suite, run on DynamoDB`

### Task 5: Postgres store, identity half

**Files:**
- Create: `backend/internal/adapter/postgres/store.go`, `identity.go`, `oauth.go`, `installations.go`, `errors.go`, `conformance_test.go`, `errors_test.go`

**Interfaces:**
- Consumes: `pgtest.New`, `storetest.RunIdentity`, the schema from Task 3.
- Produces:
  - `type Store struct{ … }` and `func New(pool *pgxpool.Pool) *Store`
  - every method of `port.Users`, `port.Devices`, `port.OAuthGrants` and `port.Installations`
  - `func tagged(err error) error`: wraps SQLSTATE `40001`, `40P01`, `53300` and `57P03` so that `errors.Is(err, domain.ErrThrottled)` holds and the original error is kept
  - `func isUniqueViolation(err error) bool` (SQLSTATE `23505`)

Semantics (each must match the dynamo adapter, and the conformance suite decides):
- **Not found** (`pgx.ErrNoRows`) → `domain.ErrNotFound`. **A guarded update that affects 0 rows** → `domain.ErrConditionFailed`. That applies to `SetActiveInstallation`, `MarkProfileDeleted`, `SetInstallationStatus` and `SaveInstallationDatabases` on a missing row, and to `UpdateInstallationTokens` on a stale version.
- `CreateUser` inserts the user and **upserts** `notion_user_links`, in one transaction.
- `CreateDevice` inserts the device and the `device_tokens` row in one transaction; a token conflict returns an error.
- `RevokeDevice` sets `revoked_at` and deletes the token row in one transaction.
- `TouchDevice` runs `UPDATE … WHERE last_seen_at IS NULL OR last_seen_at < $now - interval '1 hour'`. 0 rows → nil.
- `PutUserInstallation` uses `ON CONFLICT DO NOTHING`, so the first `created_at` stays. `PutInstallation` is an upsert that replaces the whole row.
- `ListUserInstallationIDs` orders by `installation_id`; `ListDevices` orders by `id`.
- `Consume*` runs `DELETE … WHERE key = $1 AND expires_at > $now RETURNING …`. No row → `domain.ErrNotFound`.
- `DeleteNotionUserLink` runs `DELETE FROM notion_user_links WHERE notion_user_id = $1 AND user_id = $2`, which is a no-op for anyone but the owner.
- Timestamps: domain strings are RFC3339 UTC (`domain.Timestamp`). `""` ↔ `NULL`. Read back with `domain.Timestamp(t)`. `props` is JSON; `NULL` ↔ `nil` map.
- Every returned error passes through `tagged`.

- [ ] **Step 1: Write the failing tests.**
  - `conformance_test.go`: `TestConformanceIdentity` calls `storetest.RunIdentity` with `func(t) … { return postgres.New(pgtest.New(t).Pool) }`.
  - `errors_test.go`: `TestTaggedMapsCapacityErrors` is table-driven over `&pgconn.PgError{Code: …}`. For `40001`, `40P01`, `53300` and `57P03`, `errors.Is(tagged(e), domain.ErrThrottled)` is true and `errors.As` still finds the `*pgconn.PgError`. For `23505` and `nil` it is false.
- [ ] **Step 2: Run** `go test ./internal/adapter/postgres/` → FAIL (package missing).
- [ ] **Step 3: Implement** the files listed above.
- [ ] **Step 4: Run** `go test -race ./internal/adapter/postgres/` → PASS, then the full suite → all `ok`.
- [ ] **Step 5: Commit** `feat(postgres): identity, OAuth and installation stores`

### Task 6: Postgres store, reading half, purge, sweep

**Files:**
- Create: `backend/internal/adapter/postgres/push.go`, `articles.go`, `days.go`, `purge.go`, `sweep.go`, `push_test.go`
- Modify: `backend/internal/adapter/postgres/conformance_test.go`, `store.go` (compile-time port assertions for all six ports)

**Interfaces:**
- Produces:
  - every method of `port.Reading` and `port.DataPurger`
  - `func (s *Store) Sweep(ctx context.Context, now time.Time) error`, which deletes `segments`, `oauth_states` and `auth_codes` rows whose `expires_at < now`
  - `var _ wire.Store`-equivalent assertions: `var _ port.Reading = (*Store)(nil)` etc. Don't import `wire` from the adapter.

Semantics:
- **`PushSegment` is one transaction, retried up to 3 times with sleeps of `attempt × 50 ms`** on `40001`/`40P01`. If the retries run out, or the error is tagged `ErrThrottled`, it returns `(domain.PushRetry, nil)`. Any other error is returned as is. The transaction runs, in this order (which avoids deadlocks):
  1. `INSERT INTO segments … ON CONFLICT DO NOTHING`. `expires_at = started_at + 90 days`. If no row is inserted, the outcome is `PushDuplicate`, else `PushAccepted`.
  2. Only when the segment was inserted:
     - upsert the article. A new row takes the meta and `first_read_at = started_at`. On conflict, add only `total_seconds += active_seconds`, `sessions += (seq == 0 ? 1 : 0)` and `version += 1`.
     - upsert the day: `seconds += active_seconds`, `version += 1`, and `array_append` the hash unless `= ANY(article_hashes)`.
  3. Always: `UPDATE articles SET max_progress = GREATEST(max_progress, $p)` (skipped when `$p == 0`) `, last_read_at = GREATEST(last_read_at, $ended)`. This is what lets the duplicate path heal (Plan call 2).
- **Monotonic writes:** `MarkArticleSynced` and `MarkDaySynced` add `WHERE notion_synced_version IS NULL OR notion_synced_version < $v`; 0 rows → nil.
- **Page links:** `SetArticlePage`/`SetDayPage` → `ErrConditionFailed` when the row is missing. `Clear*Page` sets the three (or two) columns to NULL; a missing row is a no-op.
- **Reads:** `BatchGetArticles` uses `url_hash = ANY($2)`. `PendingSync` lists `url_hash`/`date` where `all OR version > COALESCE(notion_synced_version, 0)`, ordered ascending.
- **Purge:**
  - `DeleteInstallationData` deletes from `segments`, `articles`, `days` and `installations` in one transaction.
  - `DeleteUserData` deletes from `devices`, `user_installations` and `users` in one transaction. It leaves `device_tokens` and `notion_user_links` alone: the purge use case removes those itself, the same as with the dynamo partition.

- [ ] **Step 1: Write the failing tests.**
  - `conformance_test.go` gains `TestConformanceReading`, calling `storetest.RunReading`.
  - `push_test.go`:
    - `TestConcurrentPushesCountEverySegmentOnce`: 20 goroutines each push a distinct segment (60 s) for the same article and day → `TotalSeconds == 1200`, `Version == 20`, day `Seconds == 1200`, and every outcome is `PushAccepted` or `PushRetry`. Any `PushRetry` is pushed again until it is accepted.
    - `TestSweepRemovesOnlyExpired`: a segment started 91 days ago, a fresh segment, an expired OAuth state and a live auth code. After `Sweep(now)`, the expired two are gone and the live two remain.
- [ ] **Step 2: Run** `go test ./internal/adapter/postgres/` → FAIL.
- [ ] **Step 3: Implement** the files listed above.
- [ ] **Step 4: Run** `go test -race ./internal/adapter/postgres/` → PASS, then the full suite → all `ok`.
- [ ] **Step 5: Commit** `feat(postgres): reading store, purge and expiry sweep`

### Task 7: Postgres job queue

**Files:**
- Create: `backend/internal/adapter/queue/pgqueue/sender.go`, `poller.go`, `sql.go`, `pgqueue_test.go`

**Interfaces:**
- Consumes: `msg.Sender`, `msg.Outgoing`, `msg.Message.Job()`, `port.JobHandler`, `port.RetryAfterError`, `pgtest.New`.
- Produces:
  - `type Sender struct { Pool *pgxpool.Pool; Delay time.Duration }`, which implements `msg.Sender`. Each `Outgoing` is inserted with `group_id = Message.GroupID()`, `dedup_id`, `body` = the JSON of the `Message` (the same shape as the SQS body), and `run_at = now() + Delay`. It uses `ON CONFLICT (dedup_id) DO NOTHING`, and sends the whole batch in one transaction, so a send is all-or-nothing like `SendMessageBatch`.
  - `type Poller struct { Pool *pgxpool.Pool; Handler port.JobHandler; Workers int; Idle time.Duration; Housekeeping func(context.Context) error; HousekeepEvery time.Duration; Log *slog.Logger }`. Defaults: `Workers` 2, `Idle` 1 s, `HousekeepEvery` 1 h.
  - `func (p *Poller) Once(ctx context.Context) (handled bool, err error)`: claims one job (design §5.2 claim query, verbatim), runs the handler, and settles the job.
  - `func (p *Poller) Run(ctx context.Context) error`: runs `Workers` loops of `Once`, sleeping `Idle` when there is nothing to claim or the claim fails (claim errors are logged at WARN and never returned). Every `HousekeepEvery` it calls `Housekeeping` and then `SweepDead`. It returns once `ctx` is done **and** in-flight jobs have settled.
  - `func SweepDead(ctx context.Context, pool *pgxpool.Pool) error`: deletes `state='dead'` rows whose `created_at < now() - 14 days`.

How `Once` settles a job:
- Handler `nil` → `DELETE`.
- `*port.RetryAfterError` → `state='ready', leased_until=NULL, run_at = now() + Delay, last_error`.
- Any other error → the same, but `run_at = now() + min(30 s × attempts, 5 min)`.
- In either error case, at `attempts >= 10` → `state='dead'`.
- An undecodable body → logged at ERROR with the job id and deleted (this matches `Consumer`).
- The handler runs with `context.WithoutCancel(ctx)`, so shutdown never aborts a job mid-write.
- Logs: job id, group, type, attempt, outcome; never the body.

- [ ] **Step 1: Write the failing tests** (`pgqueue_test.go`). `Delay: 0` unless stated. A recording handler appends `job.Type + ":" + key` under a mutex and returns scripted errors.
  - `TestSendDedupsWithinBucket`: send the same `msg.Bucketed` article twice, then a `RequeueDedupID` copy → 2 rows.
  - `TestSendIsAllOrNothing`: a second connection holds `LOCK TABLE sync_jobs IN ACCESS EXCLUSIVE MODE` in an open transaction; `Send` of 3 messages with a 200 ms context deadline returns an error; after the lock is released, the table has 0 rows.
  - `TestDelayHidesJob`: with `Delay: time.Hour`, `Once` → `handled == false`.
  - `TestGroupIsStrictFIFOAcrossPollers`: in one group, send article A, then day D, then article B. Two `Poller`s call `Once` concurrently until the queue is empty. The handler returns `RetryAfterError{Delay: 200 ms}` the first time it sees A. Recorded order: `A` (failed), `A`, `D`, `B`, with D never before A succeeds and no two jobs of the group running at once (assert this with an atomic in-flight counter that must never exceed 1).
  - `TestOtherGroupsKeepFlowingWhileOneIsParked`: group 1's head returns `RetryAfterError{1 h}`; group 2's job is handled by the next `Once`.
  - `TestPlainErrorBacksOffAndDiesAfterTen`: the handler always errors. After one `Once`, `run_at - now() ≈ 30 s` (within 2 s). Then force `run_at = now()` before each `Once`: after the 10th attempt, the row has `state='dead'`, `Once` returns false, and the group's next job can't be claimed. Dead heads are excluded from `min(id)`, so the follower **is** claimable; assert it is.
  - `TestExpiredLeaseIsReclaimed`: claim (handler blocks on a channel), set `leased_until = now() - 1 s` in SQL, and a second poller's `Once` claims the same id. Review Focus 1.
  - `TestRunFinishesInFlightJobOnShutdown`: the handler blocks until released. Start `Run`, wait until the job is in the handler, cancel `ctx`, release the handler → `Run` returns, the row is deleted, and a second queued job was never started. Review Focus 2.
  - `TestPollerSurvivesClaimError`: start `Run` against a `pgtest` database, then drop that database `WITH (FORCE)` from a second `pgtest` env's pool. `Run` keeps going (no panic, WARN logs) for 3 × `Idle`; cancelling `ctx` makes it return `nil`. Review Focus 4.
  - `TestSweepDeadKeepsRecentDead`: dead rows aged 15 days and 1 day → `SweepDead` removes only the old one.
- [ ] **Step 2: Run** `go test ./internal/adapter/queue/pgqueue/` → FAIL.
- [ ] **Step 3: Implement** the files listed above.
- [ ] **Step 4: Run** `go test -race -count=3 ./internal/adapter/queue/pgqueue/` → PASS three times (the ordering test must not be flaky). Then the full suite, and confirm `archtest` passes (`pgqueue` imports `queue/msg`, which is inside the same adapter root).
- [ ] **Step 5: Commit** `feat(queue): Postgres job queue with strict per-installation FIFO`

### Task 8: Postgres composition and the three k8s binaries

**Files:**
- Create: `backend/internal/wire/postgres.go`, `backend/internal/wire/postgres_test.go`
- Create: `backend/cmd/server/main.go`, `backend/cmd/poller/main.go`, `backend/cmd/migrate/main.go`, `backend/cmd/server/main_test.go`

**Interfaces:**
- Consumes: `config.Load`, `postgres.New`, `pgqueue.Sender`/`Poller`/`SweepDead`, `pgmigrate.Up`, `wire.API`, `wire.JobHandler`.
- Produces:
  - `type PG struct { Pool *pgxpool.Pool; Store *postgres.Store; Queue *pgqueue.Sender }`
  - `func Postgres(ctx context.Context, cfg *config.Config) (*PG, error)`: parses `cfg.DatabaseURL`; sets `MaxConns = 5` unless the URL has `pool_max_conns`; connects and pings; runs `pgmigrate.Up`; and returns the pool, store and sender. The sender has `Delay = cfg.QueueDelay`.
  - `func Healthz(pool interface{ Ping(context.Context) error }) http.HandlerFunc`: pings with a 2 s timeout and returns `200 ok` or `503 unavailable`.
  - `cmd/server`:
    - `config.Load(RoleAPI)`; exits 1 with `cmd/server runs only with STORE=postgres` unless `cfg.Store == postgres`.
    - `wire.Postgres`; then `e := wire.API(cfg, pg.Store, pg.Queue, log)` (`wire.API` wraps the sender in a `SyncQueue`, exactly as it does for SQS).
    - `e.GET("/healthz", echo.WrapHandler(wire.Healthz(pg.Pool)))`, listening on `LISTEN_ADDR` (default `:8080`).
    - On SIGTERM/SIGINT: `e.Shutdown` with a 10 s timeout, then `pool.Close()`.
  - `cmd/poller`:
    - `config.Load(RoleWorker)`, with the same postgres-only guard.
    - `wire.Postgres`; `wire.JobHandler(cfg, pg.Store, pg.Queue, log)`.
    - A `Poller` with `Idle` taken from `POLL_INTERVAL_SECONDS` (default 1) and `Housekeeping = func(ctx) error { return pg.Store.Sweep(ctx, time.Now()) }`.
    - A plain `net/http` server on `HEALTH_ADDR` (default `:8081`) serving `/healthz`.
    - On SIGTERM: cancel `Run`'s context, then shut down health.
  - `cmd/migrate`: `config` isn't needed; it reads `DATABASE_URL`, connects, runs `pgmigrate.Up`, and exits 0 or 1.

- [ ] **Step 1: Write the failing tests.**
  - `wire/postgres_test.go`:
    - `TestPostgresDefaultsPoolToFive`: a cfg with `pgtest` URL → `pg.Pool.Config().MaxConns == 5`. With `&pool_max_conns=3` appended → 3.
    - `TestPostgresMigratesOnStart`: a fresh unmigrated database → after `wire.Postgres`, `to_regclass('sync_jobs')` is non-null.
  - `cmd/server/main_test.go`, `TestHealthzReports503WhenDatabaseDown`: `wire.Healthz` with a stub whose `Ping` returns an error → 503; a nil error → 200 with body `ok`. Review Focus 4.
- [ ] **Step 2: Run** them → FAIL.
- [ ] **Step 3: Implement.** Keep `main` thin: parse, compose, run, and handle signals with `signal.NotifyContext`. Startup failures log with `wire.Logger()` and `os.Exit(1)`, like `cmd/local`.
- [ ] **Step 4: Verify.**
  - The full suite → all `ok`.
  - `go list -deps ./cmd/server ./cmd/poller ./cmd/migrate | grep -E 'aws-sdk-go|aws-lambda-go|smithy-go'` prints nothing.
  - Manual smoke run: `make pg-up`. In one shell, run `STORE=postgres DATABASE_URL=postgres://reading:reading@localhost:5432/reading?sslmode=disable` plus the `.env.local` values, then `go run ./cmd/server`. Run `go run ./cmd/poller` in another shell. `curl -s localhost:8080/healthz` → `ok`; `curl -s localhost:8080/v1/config` → JSON starting `{"version":1`; `curl -s localhost:8081/healthz` → `ok`.
- [ ] **Step 5: Commit** `feat(backend): cmd/server, cmd/poller, cmd/migrate for STORE=postgres`

### Task 9: End-to-end suite on both stores

**Files:**
- Modify: `backend/internal/e2e/e2e_test.go`

**Interfaces:**
- Consumes: `dynamotest.New`, `pgtest.New`, `postgres.New`, `wire.NewAPI`, `awswire.NewWorker`.

- [ ] **Step 1: Run each e2e test once per store.**
  - `newSystem(t, kind string)` takes `"dynamo"` or `"postgres"` and holds a `store wire.Store`, replacing `db *dynamotest.Env`.
  - For postgres, the config vars are `STORE=postgres` and `DATABASE_URL=<pgtest env URL>`, with no `TABLE_NAME`/`QUEUE_URL`.
  - `TestReadArticleEndToEnd` and `TestDeletedLibraryRecoversAfterSetup` each wrap their body in `for _, kind := range []string{"dynamo", "postgres"} { t.Run(kind, …) }`.
  - Messages still flow through `queue.Recorder` plus `deliver`. Queue mechanics are covered by Task 7; this suite pins **store** parity.
- [ ] **Step 2: Run** `go test -race ./internal/e2e/ -v` → 4 subtests PASS (2 tests × 2 stores).
- [ ] **Step 3: Commit** `test(e2e): run the end-to-end suite on DynamoDB and Postgres`

### Task 10: Image, local loop, manifest, CI/CD

**Files:**
- Create: `backend/Containerfile`, `backend/.containerignore`, `backend/deploy/manifest.yaml`, `.github/workflows/backend-image.yml`
- Modify: `backend/Makefile` (`run-pg`, `pg-migrate`, `image`; `test` unchanged), `backend/.env.local.example`

**Interfaces:**
- Consumes: the skill's `Containerfile.template`, `manifest.template.yaml` and `workflow.template.yml` (`tuncloud-deployment-config/.claude/skills/deploying-to-tuncloud/`), and design §8–§9.

- [ ] **Step 1: Write `backend/Containerfile`** from the template. It builds `/server` and `/poller` only (`./cmd/server`, `./cmd/poller`) and uses `ENTRYPOINT ["/server"]`. `.containerignore` excludes `build/`, `.env*` and `events/`.
- [ ] **Step 2: Add the Makefile targets.**
  - `run-pg: .env.local`: sources `.env.local`, exports `STORE=postgres` and `DATABASE_URL=postgres://reading:reading@localhost:5432/reading?sslmode=disable`, then runs `go run ./cmd/poller & go run ./cmd/server; kill %1`.
  - `pg-migrate`: `go run ./cmd/migrate` with the same URL.
  - `image`: `podman build --platform linux/arm64 -t ghcr.io/tuncloud/reading-tracker:dev -f Containerfile .`.
  - Add all three to `.PHONY`.
- [ ] **Step 3: Extend `.env.local.example`.** Below the DynamoDB block, add a commented `# --- Postgres mode (make run-pg) ---` block holding `STORE`, `DATABASE_URL` and `QUEUE_DELAY_SECONDS=5`. Leave the existing lines unchanged.
- [ ] **Step 4: Write `backend/deploy/manifest.yaml`** from `manifest.template.yaml`, using `<APP>=reading-tracker` and `<NS>=reading-tracker`. Use the exact objects and values from design §9, with these specifics:
  - The Deployments are `reading-tracker-server` (container `server`, `command: ["/server"]`, port 8080, readiness and liveness on `/healthz`) and `reading-tracker-poller` (container `poller`, `command: ["/poller"]`, port `health` 8081, liveness on `/healthz`). They have no init containers, and `image: ghcr.io/tuncloud/reading-tracker:v0.1.0`.
  - The ExternalSecrets use `/reading-tracker/reading-tracker` and `/reading-tracker/reading-tracker-db`.
  - The Ingress host is `reading.tuando.app`. Its CORS annotations are `nginx.ingress.kubernetes.io/enable-cors: "true"`, `cors-allow-origin: "chrome-extension://REPLACE_WITH_EXTENSION_ID"`, `cors-allow-methods: "GET, POST, DELETE"`, `cors-allow-headers: "authorization, content-type"` and `cors-max-age: "3600"`.
  - `ALLOWED_EXTENSION_IDS` in the ConfigMap uses the same `REPLACE_WITH_EXTENSION_ID` placeholder (operator step in Task 11).
  - The header comment is the template's re-apply warning.
- [ ] **Step 5: Write `.github/workflows/backend-image.yml`** from `workflow.template.yml`.
  - `<APP>=reading-tracker` and `<CONTEXT>=backend`; `tag-prefix: backend/`; `commit-path: backend`.
  - The deploy jobs are `deploy-poller` (container `poller`, deployment `reading-tracker-poller`), then `deploy-server`.
  - The `test` job adds two services: `postgres:17` with `POSTGRES_USER`/`POSTGRES_PASSWORD`/`POSTGRES_DB` all set to `reading`, port 5432 and a `pg_isready` health check; and `amazon/dynamodb-local:latest` on port 8000.
  - Its steps run `test -z "$(gofmt -l .)"`, `go vet ./...` and `go test -race ./...`, with `PG_TEST_URL` and `DYNAMODB_ENDPOINT=http://localhost:8000` set.
  - It also runs the no-AWS check from Global Constraints as a step that fails when `grep` prints anything.
- [ ] **Step 6: Verify.**
  - `make image` succeeds.
  - `podman run --rm --entrypoint /poller ghcr.io/tuncloud/reading-tracker:dev` exits 1 with `cmd/poller runs only with STORE=postgres`.
  - `podman run --rm -e STORE=postgres ghcr.io/tuncloud/reading-tracker:dev` exits 1 listing the missing env vars.
  - `podman image inspect --format '{{.Architecture}} {{.Config.User}}'` prints `arm64 65532:65532`.
  - `ssh master 'sudo kubectl --kubeconfig=/etc/kubernetes/admin.conf apply --dry-run=server -f -' < deploy/manifest.yaml` succeeds. If the namespace doesn't exist yet, use `--dry-run=client` and say so in the report.
  - The workflow YAML parses: `python3 -c 'import yaml,sys; yaml.safe_load(open(sys.argv[1]))' .github/workflows/backend-image.yml`.
- [ ] **Step 7: Commit** `feat(deploy): container image, k8s manifest and CI/CD for the tuncloud cluster`

### Task 11: Documentation

**Files:**
- Modify: `backend/README.md`, `docs/runbook.md`, `docs/reading-tracker-agent-spec.md` (§3.5 only), `infra/README.md`

- [ ] **Step 1: Update `backend/README.md`.**
  - The intro describes two modes, AWS (Lambda + DynamoDB + SQS) and Kubernetes (`cmd/server` + `cmd/poller` on Postgres).
  - "Common tasks" adds `make pg-up` / `make pg-down` and notes that `make test` needs both containers.
  - A new "Run locally on Postgres" section covers `make run-pg`.
  - The Architecture table adds `adapter/postgres`, `adapter/queue/pgqueue`, `adapter/queue/msg`, `platform/pgmigrate`, `platform/pgtest`, `wire/awswire` and `cmd/{server,poller,migrate}`.
  - A new section, "Deploy to tuncloud", holds the 9 operator steps from design §10 verbatim. Step 5 also says to replace `REPLACE_WITH_EXTENSION_ID` in `deploy/manifest.yaml` first.
- [ ] **Step 2: Update `docs/runbook.md`.**
  - Add "Part 3 — Kubernetes (Postgres)", which points to the README's "Deploy to tuncloud" section and adds the smoke test: `curl https://reading.tuando.app/healthz`, then build the extension with `VITE_API_BASE=https://reading.tuando.app` and repeat Part 2 step 9 against it.
  - In the step-7 test table, the backend row's "Needs" becomes `make ddb-up` and `make pg-up`.
- [ ] **Step 3: Update spec §3.5,** retitled "Environment variables". Add rows for `STORE`, `DATABASE_URL`, `QUEUE_DELAY_SECONDS`, `LISTEN_ADDR`, `HEALTH_ADDR` and `POLL_INTERVAL_SECONDS`, each marked "k8s/postgres". Add one sentence saying that §3.1–§3.2 describe the DynamoDB mapping and that design `2026-10-25-postgres-k8s-design.md` §6 plus migration `0001_store.sql` is the Postgres mapping.
- [ ] **Step 4: Add one line to `infra/README.md`:** "Running on Kubernetes + Postgres instead? See `backend/README.md` → Deploy to tuncloud."
- [ ] **Step 5: Verify that every command in the new sections is copy-pasteable:** `make pg-up && make run-pg` starts both processes, and `curl localhost:8080/healthz` returns `ok`.
- [ ] **Step 6: Commit** `docs: Postgres mode and Kubernetes deployment`
- [ ] **Step 7: Gateway policy, in the sibling repo `../tuncloud-deployment-config`.** In `deploy-gateway/manifest.yaml`, ConfigMap `deploy-gateway-policy`, add after the `tuncloud/deploy-gateway` entry:
  ```yaml
  - repository: tuncloud/reading-chrome-extension
    permissions:
      - action: deployment.rollout
        namespaces: ["reading-tracker"]
        deployments: ["reading-tracker-server", "reading-tracker-poller"]
  ```
  Commit there: `deploy-gateway: allow tuncloud/reading-chrome-extension to roll out reading-tracker`. Applying it to the cluster stays an operator step (README step 7).
