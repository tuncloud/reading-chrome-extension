# Remove the AWS Deployment Path Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Leave the backend with one store (Postgres) and one queue (`pgqueue`), with no AWS code, dependency, tooling or documentation, and with every behaviour that is tested today still tested.

**Architecture:** Tests move first, deletion second. Tasks 1–4 give every suite that runs on DynamoDB Local today a Postgres home (and add the Postgres-side tests that replace DynamoDB fault injection and the SQS consumer tests) while the AWS code still compiles. Task 5 then deletes the AWS code in one cut, Task 6 the tooling and Terraform, Task 7 the docs. The suite is green after every task.

**Tech Stack:** Go 1.26 (module `reading-tracker/backend`), Echo v4, `pgx/v5` (`pgxpool`), PostgreSQL 17 via Podman, GitHub Actions.

**Spec:** [`docs/superpowers/specs/2026-11-15-remove-aws-path-design.md`](../specs/2026-11-15-remove-aws-path-design.md) (approved). Read it first. Behaviour authority stays [`docs/reading-tracker-agent-spec.md`](../../reading-tracker-agent-spec.md).

## Global Constraints

- **Nothing observable changes.** No change to HTTP routes, status codes, JSON bodies, the Postgres schema (`0001_store.sql`, `0002_sync_jobs.sql`), the `sync_jobs` row format, or anything under `extension/src`.
- **No production code changes under `internal/usecase/**` or `internal/domain/**`.** Only their `_test.go` files change.
- **A moved test keeps the meaning of every assertion.** Only tests of strongly consistent reads and DynamoDB record/attribute shapes are deleted. Any other assertion that has to change goes in the task report with a reason.
- **The `retry` segment outcome and `domain.PushRetry` stay.** Postgres produces them (`adapter/postgres/push.go`).
- **The suite is green after every task:** `cd backend && gofmt -l . ; go vet ./... && go test -race ./...` — gofmt prints nothing, every package `ok`. Tasks 1–4 need `make ddb-up pg-up`; from Task 5 on only `make pg-up`.
- **Podman only, never Docker,** in any script, Makefile target, CI step or doc.
- **`internal/archtest` holds:** an adapter never imports another adapter root; only `cmd/*` (and tests) import `internal/wire/...`; platform imports only platform and third-party code.
- **The deployed manifest keeps working unchanged:** a binary built from this branch must start with `STORE=postgres` still set in its environment.
- **Secrets delivery is out of scope:** the `aws-parameter-store` ClusterSecretStore reference in `backend/deploy/manifest.yaml` stays.
- **History stays:** nothing under `docs/superpowers/plans/` or `docs/superpowers/specs/` (other than this plan's checkboxes) is edited.
- **Tooling:** `perl -pi -e` for in-place rewrites (macOS `sed` has no `\b`); `go run golang.org/x/tools/cmd/goimports@v0.34.0 -w <files>` to fix imports.

## Review Focus

1. **A serialization conflict or deadlock in the middle of a push:** the segment is counted exactly once after the retry, and a push that keeps conflicting comes back `retry`, never an error. Tests `TestPushRetriesTransactionConflict`, `TestPushGivesUpAfterThreeConflicts` (Task 1).
2. **Postgres out of connections while the API authenticates a request:** the caller gets 503 `capacity_exceeded`, not 500. Test `TestThrottledReadIs503` (Task 3).
3. **The database write fails right after Notion rotated the refresh token:** the write is retried in-process (100 ms, 200 ms) so the only valid refresh token is not lost. Tests `TestSessionRefreshRetriesTokenWrite`, `TestSessionRefreshTokenWriteGivesUpAfterThreeAttempts` (Task 2).
4. **A stale environment:** the cluster still sets `STORE=postgres`, and an old `.env.local` may still set `STORE=dynamo`, `TABLE_NAME`, `QUEUE_URL`. The binary starts and uses Postgres in every case. Test `TestStoreEnvIsIgnored` (Task 5).
5. **`DATABASE_URL` missing:** startup fails with a message that names it, in both roles. Test `TestLoadNeedsDatabaseURL` (Task 5).

---

### Task 1: Postgres test helper and deterministic push-retry tests

**Files:**
- Create: `backend/internal/adapter/postgres/postgrestest/postgrestest.go`
- Create: `backend/internal/adapter/postgres/export_test.go`
- Modify: `backend/internal/adapter/postgres/store.go` (the `Store` struct and `tx`)
- Test: `backend/internal/adapter/postgres/push_test.go`

**Interfaces:**
- Produces, package `postgrestest` (replaces `dynamotest` for every other suite):
  - `type Env struct { Store *postgres.Store; Pool *pgxpool.Pool; URL string }`
  - `func New(t testing.TB, opts ...postgres.Option) *Env` — a fresh migrated database from `pgtest.New(t)`, `Store` built with `opts`.
  - `func (e *Env) Count(t testing.TB, table, where string, args ...any) int` — `SELECT count(*) FROM <table> WHERE <where>`; `where` may be `"true"`.
  - `func (e *Env) Dump(t testing.TB) []string` — every row of every table in schema `public`, each as its `row_to_json` text. Used by tests that assert a secret appears nowhere in the database.
- Produces, package `postgres` (test-only, in `export_test.go`): `func WithTxFault(f func() error) Option`.

- [ ] **Step 1: Write the failing tests** in `push_test.go`. `pgErr(code)` returns `&pgconn.PgError{Code: code}`; each store is `postgres.New(pgtest.New(t).Pool, postgres.WithSleep(record), postgres.WithTxFault(fault))`; `w` is `storetest.Segment("00000000-0000-4000-8000-000000000001", storetest.ArticleURL, 0, 60, 0.5, storetest.TestNow)`.

```go
// Scenario 14: a capacity error is reported as retry, and nothing is written.
func TestPushThrottleIsRetry(t *testing.T) {
	// fault always returns pgErr("53300")
	out, err := s.PushSegment(ctx, w)
	// want: out == domain.PushRetry, err == nil, no sleeps recorded
	// want: GetArticle(ctx, "bot-1", hash) returns domain.ErrNotFound
}

// A conflict is retried and the segment is counted exactly once (C2).
func TestPushRetriesTransactionConflict(t *testing.T) {
	// fault returns pgErr("40001") on calls 1 and 2, then nil
	out, err := s.PushSegment(ctx, w)
	// want: out == domain.PushAccepted, err == nil
	// want: sleeps == []time.Duration{50 * time.Millisecond, 100 * time.Millisecond}
	// want: article TotalSeconds == 60, Version == 1, Sessions == 1
}

func TestPushGivesUpAfterThreeConflicts(t *testing.T) {
	// fault always returns pgErr("40P01")
	out, err := s.PushSegment(ctx, w)
	// want: out == domain.PushRetry, err == nil
	// want: sleeps == []time.Duration{50 * time.Millisecond, 100 * time.Millisecond}
}
```

- [ ] **Step 2: Run them to verify they fail**

Run: `cd backend && go test ./internal/adapter/postgres/ -run 'TestPush(Throttle|Retries|GivesUp)' -v`
Expected: FAIL to compile, `undefined: postgres.WithTxFault`.

- [ ] **Step 3: Add the seam.** In `store.go` add the unexported field `txFault func() error` to `Store`; `tx` calls it first when non-nil and returns `tagged(err)` for a non-nil result without opening a transaction. In `export_test.go` (package `postgres`) add `WithTxFault(f func() error) Option`, which sets the field. No exported production API changes.

- [ ] **Step 4: Run them to verify they pass**

Run: `cd backend && go test -race ./internal/adapter/postgres/ -v -run 'TestPush|TestConcurrent|TestConformance'`
Expected: PASS.

- [ ] **Step 5: Write `postgrestest`** with the three functions above. `Dump` lists tables from `information_schema.tables WHERE table_schema = 'public'`. Add `postgrestest_test.go` with one test: after `CreateUser(ctx, "u1", …)`, `Count(t, "users", "id = $1", "u1") == 1` and some line of `Dump(t)` contains `"u1"`.

- [ ] **Step 6: Run the full suite, then commit**

Run: `cd backend && gofmt -l . ; go vet ./... && go test -race ./...`
Expected: no gofmt output, all `ok`.

```bash
git add backend/internal/adapter/postgres
git commit -m "test(postgres): add postgrestest helper and deterministic push-retry tests"
```

---

### Task 2: Move the use-case suites to Postgres

**Files:**
- Modify: `backend/internal/usecase/notionsync/env_test.go`, `purge_test.go`, `service_test.go` (and any other `_test.go` there that names `dynamo`, `events` or `queue.Consumer`)
- Modify: `backend/internal/usecase/setup/activator_test.go`

**Interfaces:**
- Consumes: `postgrestest.New`, `(*Env).Count` from Task 1.
- Produces: nothing other tasks use.

- [ ] **Step 1: Rewrite the notionsync test environment.** In `env_test.go`:
  - `wenv.db` becomes `*postgrestest.Env`, built with `postgrestest.New(t)`. Every `e.db.Store` use keeps working.
  - Remove the `c *queue.Consumer` field and the `aws-lambda-go/events` import.
  - `handle` keeps its name, argument list and call sites, and returns `[]failure` where `type failure struct{ ItemIdentifier string }`. It reproduces what the consumer did, without SQS: call `e.s.HandleJob(ctx, m.Job())` for each message in order; on the first error, append the delay of a `*port.RetryAfterError` (via `errors.As`) to `e.vis.calls`, report that message and every later one as `failure{ItemIdentifier: fmt.Sprintf("m%d", i)}`, and stop.
  - `fakeVisibility` shrinks to `struct{ calls []time.Duration }` (its `ChangeVisibility` method goes).

- [ ] **Step 2: Rewrite the purge assertions.** In `purge_test.go` delete `remaining` and assert with `e.db.Count`. "Nothing left for user `u` / installation `testBot`" means all of these are 0:

| table | where |
|---|---|
| `users` | `id = $1` (uid) |
| `user_installations` | `user_id = $1` |
| `devices` | `user_id = $1` |
| `device_tokens` | `token_hmac = $1` (`"h-"+uid`) |
| `notion_user_links` | `notion_user_id = $1` (`"n-"+uid`) |
| `installations` | `id = $1` (`testBot`) |
| `articles`, `segments`, `days` | `installation_id = $1` (`testBot`) |

  `TestPurgeRemovesAllUserData` checks every row of the table. `TestPurgeIgnoresLiveUser` checks `users` and `installations` are still 1. `TestPurgeSkipsInstallationOwnedByAnotherUser` checks `installations` for `testBot` is still 1 and `users` for `u0` is 0.

- [ ] **Step 3: Replace the token-write fault injection.** In `service_test.go` delete `tokenWriteFailDB` and add:

```go
// tokenWriteFail fails the first n token-rotation writes with a transient error.
type tokenWriteFail struct {
	port.Installations
	n int
}

func (d *tokenWriteFail) UpdateInstallationTokens(ctx context.Context, botID, accessEnc, refreshEnc string, oldVersion int, now time.Time) error
// while n > 0: n--, return errors.New("postgres: connection reset"); else delegate
```

  The two tests set `e.s.Installations = &tokenWriteFail{Installations: e.db.Store, n: 2}` (and `n: 3`). Every assertion stays as written: `TokenVersion == 2`, refresh token `"new-refresh"`, sleeps `[100ms, 200ms]`; and for three failures a non-`errAck` error with `TokenVersion == 1`.

- [ ] **Step 4: Move `setup/activator_test.go`** from `dynamotest.New(t)` / `*dynamotest.Env` to `postgrestest.New(t)` / `*postgrestest.Env`. No assertion changes.

- [ ] **Step 5: Verify nothing here names DynamoDB or SQS any more**

Run: `cd backend && grep -rnE 'dynamo|aws-|events\.SQS|queue\.Consumer' internal/usecase`
Expected: no output.

- [ ] **Step 6: Run the suites, then commit**

Run: `cd backend && go test -race ./internal/usecase/... && gofmt -l . ; go vet ./...`
Expected: all `ok`, no gofmt output. If an assertion fails only because Postgres orders or formats something differently, fix the test helper, not the assertion, and note it in the task report.

```bash
git add backend/internal/usecase
git commit -m "test(usecase): run notionsync and setup suites on Postgres"
```

---

### Task 3: Move the HTTP API suite to Postgres

**Files:**
- Modify: `backend/internal/adapter/httpapi/testenv_test.go`, `app_test.go`, `auth_test.go`, `oauth_test.go` (and any other `_test.go` there that names `dynamo`)

**Interfaces:**
- Consumes: `postgrestest.New`, `(*Env).Dump`, `(*Env).Pool` from Task 1.

- [ ] **Step 1: Rewrite the test environment.** In `testenv_test.go`:
  - `testEnv.db` becomes `*postgrestest.Env`.
  - The `vars` map drops `TABLE_NAME` and `QUEUE_URL` and gains `"STORE": "postgres"` and `"DATABASE_URL": env.db.URL`. (`STORE` is removed again in Task 5.)
  - `setDeviceAttr(uid, deviceID, attr, value string)` keeps its signature and call sites and becomes one `UPDATE devices SET <attr> = $1::timestamptz WHERE user_id = $2 AND id = $3` on `env.db.Pool`. Its only callers pass `revoked_at` and `last_seen_at`; reject any other `attr` with `t.Fatalf`.

- [ ] **Step 2: Replace the raw-item scans.** `auth_test.go:52` and `oauth_test.go:111` loop over `env.db.Scan(t)` to assert that a secret appears in no stored item. Loop over `env.db.Dump(t)` instead and keep the same substring assertions.

- [ ] **Step 3: Replace the throttle injection.** In `app_test.go` delete `throttledReadsDB` and add:

```go
// throttledAuth fails the first read the API makes (the device-token lookup) the
// way the Postgres adapter reports capacity errors.
type throttledAuth struct{ wire.Store }

func (throttledAuth) GetDeviceToken(context.Context, string) (*domain.DeviceToken, error) {
	return nil, fmt.Errorf("postgres: too many connections: %w", domain.ErrThrottled)
}
```

  `TestThrottledReadIs503` builds its API with `Store: throttledAuth{env.db.Store}` and keeps its assertion: status 503, error code `capacity_exceeded`. Update its comment to say Postgres.

- [ ] **Step 4: Verify and run**

Run: `cd backend && grep -rnE 'dynamo|aws-' internal/adapter/httpapi ; go test -race ./internal/adapter/httpapi/`
Expected: no grep output; `ok`.

- [ ] **Step 5: Commit**

```bash
git add backend/internal/adapter/httpapi
git commit -m "test(httpapi): run the API suite on Postgres"
```

---

### Task 4: End-to-end on Postgres only, and cover what the SQS consumer tests covered

**Files:**
- Modify: `backend/internal/e2e/e2e_test.go`
- Test: `backend/internal/adapter/queue/pgqueue/pgqueue_test.go`, `backend/internal/adapter/queue/msg/msg_test.go`

**Interfaces:**
- Consumes: `wire.NewJobHandler(wire.SyncDeps) port.JobHandler` (exists), `postgrestest.New`.

- [ ] **Step 1: Make e2e Postgres-only.** In `e2e_test.go`:
  - Delete `stores`, the `kind` parameter of `newSystem`, the `switch`, `noVisibility`, and the per-store `for` loops and subtests around each test body (the body runs once).
  - `system.wk` becomes `port.JobHandler`, built with `wire.NewJobHandler(wire.SyncDeps{…})` using the same field values as today.
  - `deliver(t, wk port.JobHandler, q *queue.Recorder)` calls `wk.HandleJob(ctx, o.Message.Job())` for each drained message and fails the test on any error. The five-round loop stays.
  - Store is `postgrestest.New(t).Store`; `vars` has `"STORE": "postgres"` and `"DATABASE_URL"`.

- [ ] **Step 2: Run e2e**

Run: `cd backend && go test -race ./internal/e2e/ && grep -nE 'dynamo|awswire|events\.' internal/e2e/e2e_test.go`
Expected: `ok`; no grep output.

- [ ] **Step 3: Add the queue tests that replace the consumer and local-queue tests.** The tests being deleted in Task 5 and where their property lives afterwards:

| Deleted test (`adapter/queue`) | Property | Covered by |
|---|---|---|
| `TestConsumerFailsRestOfBatch` | a failed job blocks later jobs of its group (C21) | existing `TestGroupIsStrictFIFOAcrossPollers` |
| `TestConsumerBacksOffRetryAfter` | a `RetryAfterError` hides the job for its `Delay` | existing `TestOtherGroupsKeepFlowingWhileOneIsParked` |
| `TestConsumerAcksUndecodable` | an undecodable body is dropped | existing `TestUndecodableBodyIsDropped` |
| `TestConsumerPassesJobFields` | every message field reaches the handler | **new** `TestJobCarriesEveryField` |
| `TestResyncAfterPushIsDeliveredByLocalQueue` | a resync right after a push in the same bucket is not deduplicated away (R2) | **new** `TestResyncAfterPushInSameBucketIsQueued` |
| `TestLocalQueueDedupsAndDelivers` | dedup inside one bucket | existing `TestSendDedupsWithinBucket` |
| `TestSQS*` | SQS wire format | deleted with SQS |

  Read each "existing" test and confirm it asserts the property in its row; if one does not, add the missing assertion to it and say so in the task report.

```go
// msg_test.go
func TestJobCarriesEveryField(t *testing.T) {
	m := msg.Message{Type: msg.TypePurge, InstallationID: "bot-1", URLHash: "h1", Date: "2026-10-06", UserID: "u1"}
	want := port.Job{Type: port.JobPurge, InstallationID: "bot-1", URLHash: "h1", Date: "2026-10-06", UserID: "u1"}
	// want: m.Job() == want
}

// pgqueue_test.go — R2
func TestResyncAfterPushInSameBucketIsQueued(t *testing.T) {
	// q := &msg.SyncQueue{Sender: &pgqueue.Sender{Pool: pool}, Bucket: 120 * time.Second, Now: fixed clock}
	// q.EnqueueSync(ctx, "bot-1", []string{"h1"}, nil); q.EnqueueSync again; q.EnqueueResync(ctx, "bot-1", []string{"h1"}, nil)
	// want: SELECT count(*) FROM sync_jobs == 2 (the two pushes collapsed, the resync did not)
}
```

- [ ] **Step 4: Run, then commit**

Run: `cd backend && go test -race ./internal/adapter/queue/... ./internal/e2e/ && gofmt -l . ; go vet ./...`
Expected: all `ok`.

```bash
git add backend/internal/e2e backend/internal/adapter/queue
git commit -m "test: run e2e on Postgres only and cover consumer properties in pgqueue"
```

---

### Task 5: Delete the AWS code

**Files:**
- Delete: `backend/internal/adapter/dynamo/` (whole tree), `backend/internal/wire/awswire/`, `backend/cmd/api/`, `backend/cmd/worker/`, `backend/cmd/local/`, `backend/events/`
- Delete: `backend/internal/adapter/queue/sqs.go`, `consumer.go`, `consumer_test.go`, `local.go`, `queue_test.go`, `aliases.go`
- Modify: `backend/internal/platform/config/config.go`, `config_test.go`
- Modify: `backend/cmd/server/main.go`, `backend/cmd/poller/main.go`
- Modify: `backend/internal/archtest/arch_test.go`
- Modify: every `_test.go` that imports `internal/adapter/queue` (use `msg` instead), `backend/internal/wire/app.go`, `backend/internal/wire/postgres_test.go`, `backend/internal/adapter/postgres/store.go` (comments)
- Modify: `backend/go.mod`, `backend/go.sum`

**Interfaces:**
- Produces: `config.Config` without `Store`, `TableName`, `QueueURL`; no `config.StoreDynamo` / `config.StorePostgres`. `config.Load(role, getenv)` keeps its signature and both roles.

- [ ] **Step 1: Write the failing config tests** in `config_test.go` (`base()` here means the file's existing helper for a complete valid environment, adjusted to carry `DATABASE_URL` and no `TABLE_NAME`/`QUEUE_URL`):

```go
// The cluster manifest still sets STORE=postgres and an old .env.local may say dynamo.
func TestStoreEnvIsIgnored(t *testing.T) {
	for _, v := range []string{"", "postgres", "dynamo", "anything"} {
		// vars = base() + STORE=v, TABLE_NAME="t", QUEUE_URL="q"
		// want: config.Load(config.RoleAPI, …) succeeds and cfg.DatabaseURL == base DATABASE_URL
	}
}

func TestLoadNeedsDatabaseURL(t *testing.T) {
	for _, role := range []config.Role{config.RoleAPI, config.RoleWorker} {
		// vars = base() without DATABASE_URL
		// want: error whose text contains "missing env" and "DATABASE_URL"
	}
}
```

- [ ] **Step 2: Run them to verify they fail**

Run: `cd backend && go test ./internal/platform/config/ -run 'TestStoreEnvIsIgnored|TestLoadNeedsDatabaseURL' -v`
Expected: FAIL (`STORE must be dynamo or postgres, got "anything"`, and missing `TABLE_NAME` rather than `DATABASE_URL`).

- [ ] **Step 3: Simplify `config`.** Remove the `Store`, `TableName`, `QueueURL` fields and the two `Store*` constants. `Load` no longer reads `STORE`, `TABLE_NAME` or `QUEUE_URL`. `DATABASE_URL` is required in the position `TABLE_NAME` held (right after `APP_ENV`), so the multi-variable "missing env" message keeps its order. `DEBOUNCE_BUCKET_SECONDS` is required for both roles and `QUEUE_DELAY_SECONDS` is always parsed with its existing rules (`>=` bucket, `<= 900`, integer). Then fix the rest of `config_test.go`: delete `TestStoreDefaultsToDynamo`, `TestDynamoModeStillNeedsTableAndQueue`, `TestUnknownStoreRejected`, `TestLoadMissingTableName`; rename `TestPostgresModeDoesNotNeedTableOrQueue` → `TestLoadDoesNotNeedTableOrQueue`, `TestPostgresModeNeedsDatabaseURL` is replaced by `TestLoadNeedsDatabaseURL`, `TestPostgresWorkerReadsBucket` → `TestWorkerReadsBucket`; keep every other test's assertions.

- [ ] **Step 4: Delete the AWS packages and entrypoints**

```bash
cd backend
git rm -r -q internal/adapter/dynamo internal/wire/awswire cmd/api cmd/worker cmd/local events
git rm -q internal/adapter/queue/{sqs,consumer,consumer_test,local,queue_test,aliases}.go
```

- [ ] **Step 5: Repair what referenced them.**
  - `cmd/server/main.go`, `cmd/poller/main.go`: drop the `cfg.Store != config.StorePostgres` guard and "(STORE=postgres)" from the doc comments.
  - Tests that used the `queue` aliases: `grep -rl '"reading-tracker/backend/internal/adapter/queue"' internal cmd | xargs perl -pi -e 's{internal/adapter/queue"}{internal/adapter/queue/msg"}; s/\bqueue\.(Message|Outgoing|Sender|SyncQueue|Recorder|Type\w*|Article|Day|Purge|Bucketed)\b/msg.$1/g'`, then run goimports on those files.
  - Remove `"STORE": "postgres"` from the `vars` maps in `httpapi/testenv_test.go`, `e2e/e2e_test.go` and `wire/postgres_test.go`.
  - `internal/archtest/arch_test.go`, `TestViolationRules`: replace `internal/adapter/dynamo` with `internal/adapter/postgres` in its three cases; change `{"internal/adapter/queue", module + "/internal/wire", true}` to `{"internal/adapter/queue/pgqueue", module + "/internal/wire", true}`; keep the `aws-sdk-go-v2` case (it still proves the domain rejects third-party imports).
  - Comments: `wire/app.go` (package doc mentions `wire/awswire`, `Logger` says CloudWatch), `adapter/postgres/store.go` (package doc says "same semantics as adapter/dynamo"), `config.go` ("shared by cmd/api, cmd/worker and cmd/local"). State what is true now.

- [ ] **Step 6: Drop the dependencies**

Run: `cd backend && go mod tidy && go list -deps ./... | grep -E 'aws-sdk-go|aws-lambda-go|smithy-go|awslabs'`
Expected: no output. `grep -c aws go.mod` prints `0`.

- [ ] **Step 7: Run the full suite with only Postgres up**

Run: `cd backend && make ddb-down ; gofmt -l . ; go vet ./... && go test -race ./...`
Expected: no gofmt output, every package `ok`.

Run: `cd backend && grep -rniE 'dynamo|sqs|lambda|awswire' --include='*.go' . | grep -v 'arch_test.go'`
Expected: nothing that describes a current code path. A comment explaining why a rule exists historically may stay; rewrite anything that reads as present tense.

- [ ] **Step 8: Commit**

```bash
git add -A backend
git commit -m "refactor(backend)!: remove the DynamoDB, SQS and Lambda path"
```

---

### Task 6: Remove AWS tooling, CI steps and Terraform

**Files:**
- Delete: `infra/` (whole tree)
- Modify: `backend/Makefile`, `backend/.env.local.example`, `backend/deploy/manifest.yaml`, `.github/workflows/backend-image.yml`, `extension/.env.example`
- Check: `backend/Containerfile`, `backend/.containerignore`, `.gitignore`, `backend/.gitignore` if present

- [ ] **Step 1: Makefile.** Remove `ddb-up`, `ddb-down`, `run-local`, `build`, `build-api`, `build-worker`, `DDB_CONTAINER`, `LAMBDA_ENV`, `LAMBDA_FLAGS`, and their `.PHONY` entries. Rename `run-pg` to `run` and keep `run-pg` as an alias target that depends on it, so existing habits and docs written before Task 7 keep working. `run` no longer exports `STORE`. Keep `test`, `pg-up`, `pg-down`, `.env.local`, `pg-migrate`, `image`.

- [ ] **Step 2: `.env.local.example`.** Remove `TABLE_NAME`, `QUEUE_URL`, `LOCAL_QUEUE_DELAY_SECONDS`, `DYNAMODB_ENDPOINT`, the three `AWS_*` lines and the commented "Postgres mode" block. Add, uncommented:

```
# Keep QUEUE_DELAY_SECONDS >= DEBOUNCE_BUCKET_SECONDS (C5: D >= B).
DEBOUNCE_BUCKET_SECONDS=5
QUEUE_DELAY_SECONDS=5
DATABASE_URL=postgres://reading:reading@localhost:5432/reading?sslmode=disable
```

  `make run` still passes `DATABASE_URL=$(PG_URL)` explicitly.

- [ ] **Step 3: Manifest.** Delete the `STORE: "postgres"` line and reword the "CORS mirrors the Lambda Function URL" comment. Change nothing else — in particular not the `aws-parameter-store` secret store reference.

- [ ] **Step 4: CI.** In `backend-image.yml` remove the `dynamodb` service, the `DYNAMODB_ENDPOINT` env line, and the "no AWS in cmd/server, cmd/poller, cmd/migrate" step.

- [ ] **Step 5: Terraform and leftovers.** `git rm -r -q infra`. Update `extension/.env.example`'s comment from `make run-local` to `make run`. Remove ignore entries that only served deleted paths (`build/`, `.terraform`, SAM output) from the ignore files listed above; leave entries that still match something.

- [ ] **Step 6: Verify**

Run: `cd backend && make pg-up && make test && make image`
Expected: tests `ok`; the image builds.

Run: `cd backend && rm -f .env.local && make .env.local && grep -c . .env.local`
Expected: a file with the two generated secrets and no AWS variable. (Then fill in real Notion values only if you go on to run the server.)

Run: `grep -rniE 'dynamo|sqs|lambda|terraform|ddb' backend/Makefile backend/.env.local.example backend/deploy .github extension/.env.example`
Expected: no output.

- [ ] **Step 7: Commit**

```bash
git add -A
git commit -m "chore: remove AWS tooling, CI steps and Terraform"
```

---

### Task 7: Make the docs describe one deployment

**Files:**
- Modify: `README.md`, `backend/README.md`, `extension/README.md`, `docs/runbook.md`, `docs/c4-architecture.md`, `docs/reading-tracker-onboarding.md`, `docs/reading-tracker-agent-spec.md`

This task has no test cycle of its own; its gate is the checks in Step 5 and a reviewer reading the diff. Before editing, read the Postgres schema (`backend/internal/platform/pgmigrate/migrations/*.sql`), `adapter/postgres/push.go`, and `adapter/queue/pgqueue/poller.go` — they are the facts the docs must match.

- [ ] **Step 1: READMEs.**
  - Root `README.md`: one deployment (Kubernetes: API server, poller, Postgres); remove the AWS bullet, the `infra/` row and the infra guide link.
  - `backend/README.md`: rewrite the intro, the local-development commands (`make pg-up`, `make run`, `make test`, `make pg-down`), and the layer table (no `adapter/dynamo`, `awswire`, `cmd/{api,worker,local}`, `dynamotest`; add `adapter/postgres/postgrestest`). Rewrite "flow of a read" as: extension → `adapter/httpapi` → `usecase/readsync` → `port.Reading` (`adapter/postgres`) + `port.SyncQueue` (`pgqueue.Sender`) → `sync_jobs` → `pgqueue.Poller` → `usecase/notionsync` → `port.Reading` + `port.NotionPages`.
  - `extension/README.md`: the backend prerequisite becomes `cd ../backend && make pg-up && make run`.

- [ ] **Step 2: `docs/reading-tracker-agent-spec.md`** — edited, not rewritten; it stays the behaviour authority.
  - §3.1–3.2 are re-expressed in Postgres terms: one subsection per table of `0001_store.sql` and `0002_sync_jobs.sql`, carrying over the field meanings and write rules the current text states — segments applied exactly once, `version` counters, monotonic `max_progress` and `last_read_at`, sticky `completed`, article metadata written only on creation, segment expiry at `started_at + 90 d`, single-use and expiring OAuth states and auth codes, the `token_version` guard. Where the current text describes a DynamoDB mechanism (conditional expressions, the post-commit raise, `BatchWriteItem`, lazy TTL deletion), describe what Postgres does instead (one transaction, `ON CONFLICT`, guarded `UPDATE`, the expiry sweep).
  - Queue sections: remove SQS/Lambda mechanics; state the rules that still bind in `pgqueue` terms — group ordering one at a time (C4), dedup per bucket with `D ≥ B`, `B ≤ 300` (C5), a failed job blocks those behind it (C21), `Retry-After` handling (§2.15), 10 attempts then `dead`.
  - Remove the Terraform/Lambda task entries and constraints that are purely AWS: C11, C16, C17, C26, C27, plus any other whose only subject is DynamoDB capacity, Lambda packaging or SSM. Keep and reword C14 (redirect URI derived from `Host` when `PUBLIC_BASE_URL` is empty) and C15 (`time/tzdata` import). Do not renumber the constraints that remain: code comments cite them by number.
  - Scenario 14 is reworded for Postgres: a serialization failure, deadlock or connection exhaustion → segments come back `retry` (or the request 503 `capacity_exceeded`); the extension retries later; nothing is double-counted. Scenarios whose only subject is AWS are removed.
  - Every `PK#…/SK` key reference elsewhere in the file (handler steps in §2, acceptance criteria in §4) is restated against tables.

- [ ] **Step 3: Onboarding, C4, runbook.** `docs/reading-tracker-onboarding.md`, `docs/c4-architecture.md` (diagrams included) and `docs/runbook.md` show one deployment. Remove AWS deploy, Terraform and SAM steps; local development is `make pg-up` + `make run`. Keep the runbook's Kubernetes deploy section as is, including its secrets instructions.

- [ ] **Step 4: Check constraint citations still resolve**

Run: `grep -rhoE '\bC[0-9]{1,2}\b' backend --include='*.go' | sort -u` and, for each id printed, `grep -c "| <id> |" docs/reading-tracker-agent-spec.md`.
Expected: every id cited in code still has a row in the spec. If a removed constraint is still cited by live code, the constraint was not purely AWS — restore and reword it.

- [ ] **Step 5: Final checks (the spec's "done when")**

Run: `grep -rniE 'dynamo|sqs|lambda|terraform|awswire' backend .github README.md extension/README.md docs/*.md`
Expected: nothing that describes a current code path or instruction.

Run: `cd backend && go list -deps ./... | grep -E 'aws-sdk-go|aws-lambda-go|smithy-go' ; gofmt -l . ; go vet ./... && go test -race ./...`
Expected: no dependency output, no gofmt output, every package `ok`.

Run: `git status --short docs/superpowers`
Expected: only this plan file.

- [ ] **Step 6: Commit**

```bash
git add -A
git commit -m "docs: describe the single Postgres deployment"
```

---

## After the last task

Run the extension against the local backend once (`make pg-up && make run`, then `pnpm dev` or the smoke script in `extension/`) and confirm connect, a tracked read, and the Notion page appearing. This needs real Notion credentials in `backend/.env.local`, so it is the owner's check, not an automated step. Report it as not run if credentials are unavailable.
