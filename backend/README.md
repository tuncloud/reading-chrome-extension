# Reading Tracker backend

Go service behind the Reading Tracker Chrome extension: an API Lambda (Echo) and a
notion-writer worker Lambda (SQS FIFO), sharing one DynamoDB table.
Spec: [`../docs/reading-tracker-agent-spec.md`](../docs/reading-tracker-agent-spec.md).

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
base URL (the in-memory fake in `internal/notion/notiontest` is what the tests use).

## Build

```sh
make build         # build/api/bootstrap and build/worker/bootstrap (linux/arm64, lambda.norpc)
```

`events/` holds a sample SQS event for invoking the worker locally.

## Package map

| Package | Role |
|---|---|
| `cmd/api`, `cmd/worker` | Lambda entrypoints |
| `cmd/local` | local API + worker + in-process queue |
| `internal/app` | production wiring (AWS clients, Notion client, deps) |
| `internal/config` | env config, tracking config (`/v1/config`) |
| `internal/httpapi` | Echo routes: OAuth, auth, setup, sync push, status, account |
| `internal/usecase/readsync` | push validation and segment writes |
| `internal/setup` | template resolution, database creation, resync |
| `internal/worker` | Notion page writes, token refresh, purge |
| `internal/notion` (+ `notiontest`) | Notion client (+ in-memory fake) |
| `internal/store` (+ `storetest`) | DynamoDB single-table repository (+ test tables) |
| `internal/queue` | SQS FIFO sender, local queue, test recorder |
| `internal/crypto` | token sealing, device tokens, PKCE |
| `internal/e2e` | API → queue → worker → fake Notion tests |
