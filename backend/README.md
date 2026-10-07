# Reading Tracker backend

Go service behind the Reading Tracker Chrome extension: an API Lambda (Echo) and a
notion-writer worker Lambda (SQS FIFO), sharing one DynamoDB table.
Spec: [`../docs/reading-tracker-agent-spec.md`](../docs/reading-tracker-agent-spec.md).

For a single end-to-end checklist (local development and first deploy), see [`docs/runbook.md`](../docs/runbook.md).

## Prerequisites

- Go 1.26
- Podman with a running machine (`podman machine start`) for DynamoDB Local
- `openssl` (used by `make .env.local` to generate secrets)

## Common tasks

```sh
make ddb-up        # start DynamoDB Local on :8000 (container reading-ddb, in-memory)
make test          # go test ./... (needs DynamoDB Local)
make ddb-down      # stop it
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
| Adapters | `internal/adapter/dynamo` (DynamoDB), `adapter/notion` (Notion API), `adapter/queue` (SQS/local queue, consumer), `adapter/httpapi` (Echo) | domain, use cases, platform — never another adapter |
| Platform | `internal/platform/{crypto,config,textutil}` | platform only |
| Composition | `internal/wire` (the composition root), used by `cmd/{api,worker,local}` (Lambda API, Lambda worker, and the all-in-one local server) | everything |
| Test-only | `internal/archtest` (enforces this table), `internal/e2e` (API + worker against DynamoDB Local and a fake Notion) | — |
| Test helpers | `adapter/dynamo/dynamotest` (DynamoDB Local store), `adapter/notion/notiontest` (fake Notion server) | live inside their adapter |

Flow of a read: extension → `adapter/httpapi` → `usecase/readsync` → `port.Reading` (`adapter/dynamo`) +
`port.SyncQueue` (`adapter/queue`) → SQS → `adapter/queue.Consumer` → `usecase/notionsync` →
`port.Reading` (`adapter/dynamo`) + `port.NotionPages` (`adapter/notion`).

Adding a feature: put rules in `domain`, orchestration in a `usecase` package that depends on the
ports in `usecase/port` (add a method there if one is missing), implement those ports in an adapter,
and connect them in `wire`.
