# Remove the AWS deployment path

Status: draft for approval · 2026-11-15

## Intent

The backend can run two ways: on AWS (DynamoDB + SQS FIFO + Lambda) and on Kubernetes
(Postgres store + Postgres job queue). Only the Kubernetes deployment is live; the AWS stack
is not deployed and holds no data.

We are about to change how reading data is keyed (per user instead of per Notion
installation, so history survives reinstalling the extension — a separate design that
follows this one). Doing that against two stores means two adapters, two data layouts and a
one-off DynamoDB copy script for a deployment nobody runs. Removing the AWS path first makes
that change roughly half the size and leaves one store to reason about.

This project is a pure removal. Nothing the extension or a Notion user can observe changes.

## Goals

- The backend has one store (Postgres) and one queue (`pgqueue`), with no AWS code, AWS
  dependency, or AWS tooling left in the repo.
- Every behaviour that is tested today is still tested, against Postgres.
- Docs describe the system as it is: one deployment mode.

## Non-goals

- No change to HTTP routes, status codes, JSON bodies, the Postgres schema, `sync_jobs` row
  format, or the extension.
- No re-keying of reading data and no history endpoint. That is the next project.
- No change to how the cluster delivers secrets. `backend/deploy/manifest.yaml` reads them
  through the cluster's `aws-parameter-store` ClusterSecretStore; that is cluster
  infrastructure, not this app's AWS path, and stays as is.
- No rewrite of historical documents under `docs/superpowers/plans/` and
  `docs/superpowers/specs/`. They record what was decided at the time.

## Anti-goals

- Losing test coverage by deleting a test because it was written against DynamoDB, when the
  behaviour it checks still exists on Postgres.
- A half-removal: AWS modules still in `go.mod`, a `STORE` switch with one legal value, a
  README that still tells you to start DynamoDB Local.

## Constraints

- The AWS stack is confirmed not deployed, so there is nothing to `terraform destroy` and no
  data to export. Deleting `infra/terraform` strands no resources.
- `internal/archtest` rules keep holding (adapters do not import other adapter roots, only
  `cmd/*` imports `internal/wire/...`). Rules that name removed packages are removed or
  rewritten, not left pointing at nothing.
- Podman, never Docker, in any script, Makefile target, CI step or doc.
- The suite is green at the end: `cd backend && gofmt -l . ; go vet ./... && go test -race ./...`
  with only Postgres running (`make pg-up`).
- The image built by `.github/workflows/backend-image.yml` and the deployed manifest keep
  working without a config change being required on the cluster.

## Approaches

1. **Re-key both stores, migrate only Postgres.** Keeps the AWS path alive in code. Rejected:
   it keeps paying for a second adapter on every future change, for a deployment with no
   users.
2. **Re-key both stores and migrate both.** Same cost as 1 plus a one-off DynamoDB copy
   script. Rejected for the same reason, more so.
3. **Remove the AWS path, then re-key Postgres only.** Chosen by the owner. One-way, but the
   stack is not deployed, and git history keeps the code if it is ever wanted again.

Within approach 3 there is one sensible shape: delete everything AWS in one project, ahead
of the re-keying, rather than leaving the DynamoDB adapter behind as unreferenced code or
mixing the removal into the re-keying diff.

## What gets removed

- **Store:** `backend/internal/adapter/dynamo/` (including `dynamotest`).
- **Queue transport:** `backend/internal/adapter/queue/sqs.go`, and the SQS-shaped
  `consumer.go` (it speaks `events.SQSBatchItemFailure`).
- **Composition:** `backend/internal/wire/awswire/`.
- **Entrypoints:** `backend/cmd/api`, `backend/cmd/worker`, `backend/cmd/local`.
- **Infra and samples:** `infra/` (Terraform and its README), `backend/events/`.
- **Build and tooling:** Makefile targets `ddb-up`, `ddb-down`, `run-local`, `build`,
  `build-api`, `build-worker` and the Lambda build variables; the DynamoDB service
  container, `DYNAMODB_ENDPOINT`, and the "no AWS linked into k8s binaries" step in
  `backend-image.yml`; the DynamoDB and `AWS_*` lines in `backend/.env.local.example`.
- **Config:** the `STORE` setting, `TABLE_NAME`, `QUEUE_URL` and the dynamo branch in
  `internal/platform/config`. `DATABASE_URL` becomes unconditionally required.
- **Dependencies:** every `github.com/aws/*` and `github.com/awslabs/*` module, via
  `go mod tidy`.

## What changes

- **Tests move to Postgres.** These suites run on `dynamotest` today and must run on
  `platform/pgtest` instead, keeping the meaning of every assertion:
  `adapter/httpapi` (its test env also pokes raw DynamoDB items to set up state),
  `usecase/notionsync`, `usecase/setup`, and `internal/e2e` (drops its `"dynamo"` leg).
  `adapter/storetest` stays as the conformance suite, now with one implementation.
- **Tests that drove the worker through `queue.Consumer`** (notionsync, e2e) need another
  way to deliver recorded messages to the sync handler. The properties they assert —
  per-group ordering, a failed message blocking the ones behind it, retry-after handling —
  are already `pgqueue`'s job on the live path, so each such assertion must end up covered
  either there or by calling the handler directly.
- **Local development** is `make pg-up` plus `make run-pg` (server and poller). `cmd/local`
  has no replacement.
- **Docs** stop presenting AWS as a current option: root `README.md`, `backend/README.md`,
  `extension/README.md` (its local-backend instructions), `docs/c4-architecture.md`,
  `docs/runbook.md`, `docs/reading-tracker-onboarding.md`, and
  `docs/reading-tracker-agent-spec.md`.

## Decisions made here (author's calls, not the owner's)

- **`STORE` is removed, not kept with one value.** The deployed manifest sets
  `STORE: "postgres"`; the binary simply stops reading it, so the existing config keeps
  working, and the manifest line is deleted in the same change.
- **Only tests of properties Postgres does not have are deleted:** strongly consistent
  reads and DynamoDB record/attribute shapes.
- **Throttling and retry coverage is ported, not deleted.** Postgres still produces this
  behaviour: `adapter/postgres/errors.go` maps SQLSTATE 40001, 40P01, 53300 and 57P03 to
  `domain.ErrThrottled`, `push.go` returns `domain.PushRetry` on it and after three conflict
  attempts, and `httpapi/errors.go` maps `ErrThrottled` to 503 `capacity_exceeded`. So the
  `PushRetry` producer and the `retry` outcome in the API stay, and these tests need
  Postgres-side equivalents with the same assertions:
  - `TestThrottledReadIs503` (`httpapi/app_test.go`): a store throttle surfaces as 503.
  - `TestPushThrottleIsRetry` and `TestPushRetriesTransactionConflict`
    (`adapter/dynamo/push_test.go`): deterministic, not merely tolerated under concurrency.
  - `TestSessionRefreshRetriesTokenWrite` and its give-up twin
    (`notionsync/service_test.go`): the C7 token-write retry.

  Fault injection should not add a seam to production code if a test-side decorator over
  the `port` interfaces can express the fault; the adapter-level push tests are the one
  place where a small seam in `adapter/postgres` is acceptable if nothing else is
  deterministic. Spec scenario 14 is reworded for Postgres, not removed.
- **`docs/reading-tracker-agent-spec.md` is edited, not rewritten, and stays the behaviour
  authority** — including for the data model, because the Postgres design doc defines its
  tables by reference back to §3.1 and is frozen history. So:
  - §3.1–3.2 are re-expressed in Postgres terms: the tables of `0001_store.sql` and
    `0002_sync_jobs.sql`, with the field meanings and write rules that §3.1–3.2 carry today
    (version counters, monotonic max progress, exactly-once segments, expiry checks,
    conditional writes). This is the text the re-keying design will be written against.
  - SQS/Lambda sections and Terraform tasks are removed; queue rules are stated in
    `pgqueue` terms where they still bind (ordering per group, a failed job blocking those
    behind it, retry-after).
  - Constraints that are purely AWS (C11, C16, C17, C26, C27 and similar) are removed.
    Constraints that still bind are kept and reworded, in particular C14 (redirect URI
    derived from `Host` when `PUBLIC_BASE_URL` is empty) and C15 (`time/tzdata` import,
    checked by `TestTimezoneDataEmbedded`). Their code stays.
- **`docs/superpowers/plans/2026-10-07-infra-terraform.md` and other past plans stay**
  untouched as history.

## Left to the builder

- Whether `adapter/queue` keeps its alias file and in-process `local.go`, or collapses into
  `adapter/queue/msg` and `pgqueue`. Delete what ends up unused.
- How test environments seed state that was previously written as raw DynamoDB items
  (store methods where they exist, SQL where they do not).
- Task order, as long as the suite is green at the end of each task.
- Exact wording of the doc edits.

## Done when

- `grep -rniE 'dynamo|sqs|lambda|terraform|awswire' backend .github README.md extension/README.md`
  finds nothing that describes a current code path or instruction (comments explaining
  history are fine).
- `go list -deps ./... | grep -E 'aws-sdk-go|aws-lambda-go|smithy-go'` prints nothing.
- The full suite passes with only Postgres running, and CI passes without a DynamoDB service.
- `make image` builds, and `make run-pg` serves the extension end to end locally.
