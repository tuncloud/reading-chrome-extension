# Reading Tracker

Reading Tracker is a Chrome extension and Go backend that measures active
reading time and records it in a user's Notion workspace.

## How it works

1. The extension content script observes supported article pages.
2. The background service worker measures and stores reading segments in
   IndexedDB.
3. Pending segments are sent to the backend over the HTTP API.
4. The backend persists the data and queues a debounced Notion synchronization
   job.
5. A worker or poller updates the user's Notion Library and Daily Log.

The backend supports two deployment modes:

- **AWS:** API Lambda, worker Lambda, DynamoDB, and SQS FIFO.
- **Kubernetes:** API and poller processes backed by Postgres.

See the [C4 architecture documentation](docs/c4-architecture.md) for Mermaid
diagrams and system details.

## Repository layout

| Directory | Description |
| --- | --- |
| [`extension/`](extension/) | Chrome MV3 extension |
| [`backend/`](backend/) | Go API, sync worker, domain, use cases, and adapters |
| [`infra/`](infra/) | Terraform configuration for AWS deployment |
| [`docs/`](docs/) | Architecture, onboarding, specifications, and runbooks |

## Local development

### Extension

Requirements: Node.js 22 or newer and pnpm.

```sh
cd extension
pnpm install
cp .env.example .env.local
pnpm check
pnpm build
```

Load the generated `extension/dist/` directory as an unpacked extension from
`chrome://extensions`.

### Backend

Requirements: Go 1.26 and Podman.

```sh
cd backend
make ddb-up
make run-local
```

The local API listens on `http://localhost:8080`. Configure the extension with
`VITE_API_BASE=http://localhost:8080`.

For Postgres development, use `make pg-up` followed by `make run-pg`.

## Useful documentation

- [C4 architecture](docs/c4-architecture.md)
- [Extension guide](extension/README.md)
- [Backend guide](backend/README.md)
- [AWS infrastructure guide](infra/README.md)
- [End-to-end runbook](docs/runbook.md)
- [Product and implementation specification](docs/reading-tracker-agent-spec.md)

## License

No license has been specified for this repository.
