# Reading Tracker backend

Go service behind the Reading Tracker Chrome extension. It runs on Kubernetes as two long-running
processes, `cmd/server` (the HTTP API, Echo) and `cmd/poller` (the Notion sync), sharing one
Postgres database that holds both the data and the sync queue (`sync_jobs`).
Spec: [`../docs/reading-tracker-agent-spec.md`](../docs/reading-tracker-agent-spec.md).

For the project-wide C4 architecture diagrams, see [`../docs/c4-architecture.md`](../docs/c4-architecture.md).

For a single end-to-end checklist (local development and first deploy), see [`docs/runbook.md`](../docs/runbook.md).

## Prerequisites

- Go 1.26
- Podman with a running machine (`podman machine start`) for the local Postgres
- `openssl` (used by `make .env.local` to generate secrets)

## Common tasks

```sh
make pg-up         # start Postgres on :5432 (container reading-pg)
make test          # go test ./... (needs Postgres running)
make pg-down       # stop Postgres
make image         # build the container image with Podman (linux/arm64)
```

Before committing: `gofmt -l . ; go vet ./... && go test -race ./...`.

## Run locally

```sh
make pg-up         # start Postgres on :5432 (container reading-pg)
make .env.local    # copies .env.local.example and generates TOKEN_ENC_KEYS / DEVICE_TOKEN_PEPPER
# edit .env.local: NOTION_CLIENT_ID, NOTION_CLIENT_SECRET, ALLOWED_EXTENSION_IDS
make run           # cmd/server (API on :8080) and cmd/poller together
```

`make run-pg` is an alias of `make run`.

`make run` applies pending migrations at startup (same as the binaries do in Kubernetes)
and starts `cmd/server` and `cmd/poller` against the local Postgres. To apply migrations on
their own, without starting either process: `make pg-migrate`.

`QUEUE_DELAY_SECONDS` defaults to `DEBOUNCE_BUCKET_SECONDS`; keep it ≥ the bucket and ≤ 900
(C5). Both processes refuse to start otherwise. Register
`http://localhost:8080/v1/oauth/notion/callback` (or `PUBLIC_BASE_URL` + that path)
as a redirect URI of the Notion integration.

Check it from another terminal: `curl -s localhost:8080/healthz` (the server) and
`curl -s localhost:8081/healthz` (the poller) both return `ok`.

To run against a fake Notion instead of api.notion.com, set `NOTION_API_BASE` to its
base URL (the in-memory fake in `internal/adapter/notion/notiontest` is what the tests use).

## Architecture

Clean architecture: dependencies point inward, enforced by `internal/archtest`.

| Layer | Packages | May import |
|---|---|---|
| Domain | `internal/domain` — entities and business rules | standard library only |
| Use cases | `internal/usecase/{auth,connect,setup,readsync,library,account,notionsync}`; all ports (the interfaces use cases need) live centrally in `usecase/port`, owned by this layer; error kinds in `usecase/apperr` | domain, other use cases, `platform/{crypto,textutil}`, stdlib, `oklog/ulid` (id generation) |
| Adapters | `internal/adapter/postgres` (Postgres store), `adapter/notion` (Notion API), `adapter/queue/pgqueue` (Postgres-backed sync queue + poller), `adapter/queue/msg` (queue message/dedup-id types, the `SyncQueue` port implementation, a test `Recorder`), `adapter/httpapi` (Echo) | domain, use cases, platform — never another adapter |
| Platform | `internal/platform/{crypto,config,textutil}`, `platform/pgmigrate` (applies SQL migrations) | platform only |
| Composition | `internal/wire` (the composition root), used by `cmd/{server,poller,migrate}` (API process, Notion-sync poller, standalone migration runner) | everything |
| Test-only | `internal/archtest` (enforces this table), `internal/e2e` (API + Notion sync against Postgres and a fake Notion) | — |
| Test helpers | `adapter/postgres/postgrestest` (fresh migrated database + store), `adapter/notion/notiontest` (fake Notion server), `adapter/storetest` (conformance suite for the store, run by `adapter/postgres`), `platform/pgtest` (throwaway, fully migrated Postgres databases) | live inside their adapter |

Flow of a read: extension → `adapter/httpapi` → `usecase/readsync` → `port.Reading` (`adapter/postgres`) +
`port.SyncQueue` (`pgqueue.Sender`) → `sync_jobs` → `pgqueue.Poller` → `usecase/notionsync` →
`port.Reading` (`adapter/postgres`) + `port.NotionPages` (`adapter/notion`).

Adding a feature: put rules in `domain`, orchestration in a `usecase` package that depends on the
ports in `usecase/port` (add a method there if one is missing), implement those ports in an adapter,
and connect them in `wire`.

## Deploy to tuncloud

The Kubernetes manifest is [`deploy/manifest.yaml`](deploy/manifest.yaml); the image build and
rollout are [`.github/workflows/backend-image.yml`](../.github/workflows/backend-image.yml). In order:

1. On `worker`: `CREATE ROLE reading_tracker LOGIN PASSWORD '…'; CREATE DATABASE reading_tracker OWNER reading_tracker;`
2. Parameter Store, region `ap-southeast-1`: create two **SecureString** parameters, each holding
   one JSON object whose keys become env vars. Create the values by hand; never commit them.

   | Parameter | JSON keys |
   |---|---|
   | `/reading-tracker/reading-tracker` | `NOTION_CLIENT_ID`, `NOTION_CLIENT_SECRET`, `TOKEN_ENC_KEYS`, `DEVICE_TOKEN_PEPPER` |
   | `/reading-tracker/reading-tracker-db` | `DATABASE_URL` |

   `DATABASE_URL` has this shape (placeholders only):
   `postgres://reading_tracker:<password>@postgres-17.tuando.app:5432/reading_tracker?sslmode=<require|disable>&pool_max_conns=5`.
   Use `sslmode=require` if the Postgres server has TLS enabled, otherwise `disable` over the
   private network; confirm which with the operator.
3. DNS: `reading.tuando.app` → the ingress IP (same as `gateway.tuando.app`).
4. Notion integration: add `https://reading.tuando.app/v1/oauth/notion/callback` as a redirect URI.
5. Replace `REPLACE_WITH_EXTENSION_ID` in both the ConfigMap and the Ingress CORS annotation of
   `deploy/manifest.yaml` with the store extension id, then validate the manifest against the
   cluster (this checks the ExternalSecret CRD shape, which offline checks cannot) and expect no
   errors:
   `ssh master 'sudo kubectl --kubeconfig=/etc/kubernetes/admin.conf apply --dry-run=server -f -' < backend/deploy/manifest.yaml`.
6. Apply it:
   `ssh master 'sudo kubectl --kubeconfig=/etc/kubernetes/admin.conf apply -f -' < backend/deploy/manifest.yaml`.
7. Push to `main` → first image; make the `reading-tracker` GHCR package public.
8. In `tuncloud-deployment-config/deploy-gateway/manifest.yaml`, add
   `tuncloud/reading-chrome-extension` → `deployment.rollout` on namespace `reading-tracker`,
   deployments `reading-tracker-server`, `reading-tracker-poller`; apply and restart the gateway.
9. Re-run the workflow; verify `externalsecret` is `SecretSynced` and
   `curl https://reading.tuando.app/healthz`.
10. Build the extension with `VITE_API_BASE=https://reading.tuando.app`.
