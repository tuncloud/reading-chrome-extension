# Reading Tracker backend

Go service behind the Reading Tracker Chrome extension, with two deployment modes: on AWS
it runs as an API Lambda (Echo) and a notion-writer worker Lambda (SQS FIFO) sharing one
DynamoDB table; on Kubernetes it runs as two long-running processes, `cmd/server` and
`cmd/poller`, sharing one Postgres database (`STORE=postgres`).
Spec: [`../docs/reading-tracker-agent-spec.md`](../docs/reading-tracker-agent-spec.md).

For a single end-to-end checklist (local development and first deploy), see [`docs/runbook.md`](../docs/runbook.md).

## Prerequisites

- Go 1.26
- Podman with a running machine (`podman machine start`) for DynamoDB Local
- `openssl` (used by `make .env.local` to generate secrets)

## Common tasks

```sh
make ddb-up        # start DynamoDB Local on :8000 (container reading-ddb, in-memory)
make pg-up         # start Postgres on :5432 (container reading-pg)
make test          # go test ./... (needs both DynamoDB Local and Postgres running)
make ddb-down      # stop DynamoDB Local
make pg-down       # stop Postgres
```

Before committing: `gofmt -l . ; go vet ./... && go test -race ./...`.

## Run locally

```sh
make .env.local    # copies .env.local.example and generates TOKEN_ENC_KEYS / DEVICE_TOKEN_PEPPER
# edit .env.local: NOTION_CLIENT_ID, NOTION_CLIENT_SECRET, ALLOWED_EXTENSION_IDS
make run-local     # API on :8080, worker in-process, DynamoDB Local
```

`cmd/local` creates the table if needed and runs the worker behind an in-process
queue (`LOCAL_QUEUE_DELAY_SECONDS`). Keep that delay ≥ `DEBOUNCE_BUCKET_SECONDS`
(C5); it logs a WARN at startup otherwise. Register
`http://localhost:8080/v1/oauth/notion/callback` (or `PUBLIC_BASE_URL` + that path)
as a redirect URI of the Notion integration.

To run against a fake Notion instead of api.notion.com, set `NOTION_API_BASE` to its
base URL (the in-memory fake in `internal/adapter/notion/notiontest` is what the tests use).

## Run locally on Postgres

```sh
make pg-up         # start Postgres on :5432 (container reading-pg)
make .env.local    # if you haven't already (see "Run locally" above)
make run-pg        # runs cmd/server and cmd/poller together, STORE=postgres
```

`make run-pg` applies pending migrations at startup (same as the binaries do in Kubernetes)
and starts `cmd/server` and `cmd/poller` against the local Postgres. To apply migrations on
their own, without starting either process: `make pg-migrate`.

Check it from another terminal: `curl -s localhost:8080/healthz` (the server) and
`curl -s localhost:8081/healthz` (the poller) both return `ok`.

## Build

```sh
make build         # build/api/bootstrap and build/worker/bootstrap (linux/arm64, lambda.norpc)
```

`events/` holds a sample SQS event for invoking the worker locally.

## Architecture

Clean architecture: dependencies point inward, enforced by `internal/archtest`.

| Layer | Packages | May import |
|---|---|---|
| Domain | `internal/domain` — entities and business rules | standard library only |
| Use cases | `internal/usecase/{auth,connect,setup,readsync,library,account,notionsync}`; all ports (the interfaces use cases need) live centrally in `usecase/port`, owned by this layer; error kinds in `usecase/apperr` | domain, other use cases, `platform/{crypto,textutil}`, stdlib, `oklog/ulid` (id generation) |
| Adapters | `internal/adapter/dynamo` (DynamoDB), `adapter/postgres` (Postgres store), `adapter/notion` (Notion API), `adapter/queue` (SQS/local queue, consumer), `adapter/queue/pgqueue` (Postgres-backed sync queue + poller), `adapter/queue/msg` (queue message/dedup-id types shared by both queues), `adapter/httpapi` (Echo) | domain, use cases, platform — never another adapter |
| Platform | `internal/platform/{crypto,config,textutil}`, `platform/pgmigrate` (applies SQL migrations) | platform only |
| Composition | `internal/wire` (the composition root), `wire/awswire` (AWS clients + SQS consumer), used by `cmd/{api,worker,local}` (Lambda API, Lambda worker, the all-in-one local server) and `cmd/{server,poller,migrate}` (Kubernetes API process, Kubernetes poller, standalone migration runner) | everything |
| Test-only | `internal/archtest` (enforces this table), `internal/e2e` (API + worker against DynamoDB Local and a fake Notion) | — |
| Test helpers | `adapter/dynamo/dynamotest` (DynamoDB Local store), `adapter/notion/notiontest` (fake Notion server), `adapter/storetest` (store-agnostic conformance suite run by both `adapter/dynamo` and `adapter/postgres`), `platform/pgtest` (throwaway, fully migrated Postgres databases) | live inside their adapter |

Flow of a read: extension → `adapter/httpapi` → `usecase/readsync` → `port.Reading` (`adapter/dynamo`) +
`port.SyncQueue` (`adapter/queue`) → SQS → `adapter/queue.Consumer` → `usecase/notionsync` →
`port.Reading` (`adapter/dynamo`) + `port.NotionPages` (`adapter/notion`).

Adding a feature: put rules in `domain`, orchestration in a `usecase` package that depends on the
ports in `usecase/port` (add a method there if one is missing), implement those ports in an adapter,
and connect them in `wire`.

## Deploy to tuncloud

The Kubernetes manifest is [`deploy/manifest.yaml`](deploy/manifest.yaml); the image build and
rollout are [`.github/workflows/backend-image.yml`](../.github/workflows/backend-image.yml). In order:

1. On `worker`: `CREATE ROLE reading_tracker LOGIN PASSWORD '…'; CREATE DATABASE reading_tracker OWNER reading_tracker;`
2. Parameter Store (`ap-southeast-1`, SecureString, JSON object): the two params and their key
   names from §9. Values never appear in the repo.
3. DNS: `reading.tuando.app` → the ingress IP (same as `gateway.tuando.app`).
4. Notion integration: add `https://reading.tuando.app/v1/oauth/notion/callback` as a redirect URI.
5. Replace `REPLACE_WITH_EXTENSION_ID` in both the ConfigMap and the Ingress CORS annotation of
   `deploy/manifest.yaml` with the store extension id, then
   `ssh master 'sudo kubectl --kubeconfig=/etc/kubernetes/admin.conf apply -f -' < backend/deploy/manifest.yaml`.
6. Push to `main` → first image; make the `reading-tracker` GHCR package public.
7. In `tuncloud-deployment-config/deploy-gateway/manifest.yaml`, add
   `tuncloud/reading-chrome-extension` → `deployment.rollout` on namespace `reading-tracker`,
   deployments `reading-tracker-server`, `reading-tracker-poller`; apply and restart the gateway.
8. Re-run the workflow; verify `externalsecret` is `SecretSynced` and
   `curl https://reading.tuando.app/healthz`.
9. Build the extension with `VITE_API_BASE=https://reading.tuando.app`.
