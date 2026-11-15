# Reading Tracker architecture (C4 model)

This document describes the Reading Tracker Chrome extension and Go backend using
the [C4 model](https://c4model.com/). Diagrams use Mermaid's C4 diagram syntax.

The backend runs on Kubernetes as two processes, `cmd/server` and
`cmd/poller`, backed by Postgres, which also holds the sync queue.

The extension records active reading time locally and the backend eventually
updates the user's Notion workspace.

## 1. System context

The Reading Tracker system consists of the browser extension and its backend.
The user interacts with the extension while reading articles; Notion remains the
system of record for the user's library and daily reading log.

```mermaid
C4Context
    title System Context - Reading Tracker

    Person(reader, "Reader", "Reads articles and manages tracking settings")
    System_Boundary(readingTracker, "Reading Tracker") {
        System(extension, "Reading Tracker Chrome extension", "Measures active reading time, stores pending records locally, and syncs them")
        System(backend, "Reading Tracker backend", "Authenticates the extension, stores reading data, and synchronizes it to Notion")
    }
    System_Ext(browser, "Chrome browser", "Hosts the MV3 extension, content scripts, popup, and options page")
    System_Ext(notion, "Notion", "User workspace containing Library pages and Daily Log records")

    Person(reader) --> browser
    browser --> extension : Runs
    extension --> backend : HTTPS API\n(device token)
    backend --> notion : OAuth and HTTPS API
```

### Main flow

1. A content script observes supported article pages and measures active reading.
2. The service worker stores flushed reading segments in IndexedDB.
3. The service worker pushes pending records to the backend.
4. The backend persists the record and enqueues a debounced synchronization job.
5. The poller consumes the job and updates the user's Notion pages.

## 2. Container view

The browser-side containers are bundled from `extension/src`:

- **Content script:** page-local DOM and visibility tracking; it does not
  access the API or IndexedDB directly.
- **Popup and options UI:** React pages for connection, library, and domain
  configuration.
- **Background service worker:** message routing, IndexedDB persistence,
  authentication, API calls, alarms, and synchronization scheduling.

The backend containers are implemented in `backend` and share the same use cases
and ports. The store and the sync queue live in the same Postgres database.

```mermaid
C4Container
    title Container View - Reading Tracker

    Person(reader, "Reader", "Reads articles and configures the extension")
    System_Ext(notion, "Notion API", "OAuth provider and workspace API")

    System_Boundary(browser, "Chrome browser") {
        Container(content, "Content script", "TypeScript", "Detects supported articles and measures active reading")
        Container(ui, "Popup and options pages", "React + TypeScript", "Connection, library, and domain configuration")
        Container(worker, "Background service worker", "TypeScript + MV3", "Routes messages, persists records, calls the backend, and schedules sync")
        ContainerDb(indexedDb, "IndexedDB", "Browser storage", "Pending reading segments, device/session state, and cached configuration")
    }

    System_Boundary(backend, "Reading Tracker backend") {
        Container(api, "HTTP API", "Go + Echo", "Auth, Notion connection, setup, reading sync, library, and account endpoints")
        Container(sync, "Notion sync processor", "Go", "Consumes sync jobs, applies reading policy, and updates Notion")
        ContainerDb(store, "Application store", "Postgres", "Users, devices, OAuth grants, installations, reading records, and sync state")
        ContainerDb(queue, "Sync queue", "Postgres table sync_jobs", "Debounced, ordered Notion synchronization jobs")
    }

    Person(reader) --> content : Reads an article
    Person(reader) --> ui : Uses popup/options
    content --> worker : Chrome runtime messages
    ui --> worker : Chrome runtime messages
    worker --> indexedDb : Reads and writes
    worker --> api : HTTPS JSON API
    api --> store : Repository ports
    api --> queue : Enqueues debounced jobs
    sync --> queue : Consumes jobs
    sync --> store : Reads and writes sync state
    sync --> notion : Updates pages and daily logs
    api --> notion : OAuth and workspace setup
```

The API and sync processor do not call each other directly. The queue is the
boundary that makes Notion writes asynchronous and allows retry handling; a job
that fails 10 times is kept as `dead` for 14 days.

## 3. Backend component view

The backend follows clean architecture. Dependencies point inward:
domain rules are independent, use cases depend on ports, and adapters implement
those ports. `internal/wire` is the composition root used by the command
packages.

```mermaid
C4Component
    title Component View - Backend API and synchronization

    Container_Boundary(apiContainer, "HTTP API container") {
        Component(http, "HTTP adapter", "Echo", "Routes requests, validates DTOs, and applies auth middleware")
        Component(auth, "Auth use case", "Go", "Creates and validates device sessions")
        Component(connect, "Connect and setup use cases", "Go", "Completes Notion OAuth and creates workspace pages")
        Component(readsync, "Read sync use case", "Go", "Accepts reading segments and schedules debounced jobs")
        Component(library, "Library use case", "Go", "Reads the user's synchronized articles and daily totals")
        Component(account, "Account use case", "Go", "Disconnects and purges account data")
        Component(storePorts, "Repository ports", "Go interfaces", "Users, devices, installations, OAuth, reading, and purge contracts")
        Component(queuePort, "Queue port", "Go interface", "Sync job sender contract")
    }

    Container_Boundary(syncContainer, "Notion sync processor") {
        Component(syncService, "Notion sync use case", "Go", "Loads a job, applies reading policy, and updates Notion")
        Component(notionPort, "Notion ports", "Go interfaces", "OAuth, workspace, and page operations")
    }

    Container_Boundary(adapterContainer, "Adapters") {
        Component(httpStore, "Postgres adapter", "Go", "Implements repository ports")
        Component(queueAdapter, "Postgres queue adapter", "Go", "Implements queue sending and polling")
        Component(notionAdapter, "Notion gateway", "Go", "Calls Notion API with pacing and token handling")
    }

    Container_Boundary(coreContainer, "Core") {
        Component(domain, "Domain", "Go", "Reading entities and synchronization rules")
        Component(wire, "Composition root", "Go", "Builds adapters and injects dependencies")
    }

    Rel(http, auth, "Invokes")
    Rel(http, connect, "Invokes")
    Rel(http, readsync, "Invokes")
    Rel(http, library, "Invokes")
    Rel(http, account, "Invokes")
    Rel(auth, storePorts, "Uses")
    Rel(connect, storePorts, "Uses")
    Rel(connect, notionPort, "Uses")
    Rel(readsync, storePorts, "Uses")
    Rel(readsync, queuePort, "Uses")
    Rel(library, storePorts, "Uses")
    Rel(account, storePorts, "Uses")
    Rel(syncService, storePorts, "Uses")
    Rel(syncService, queuePort, "Uses")
    Rel(syncService, notionPort, "Uses")
    Rel(auth, domain, "Applies rules")
    Rel(readsync, domain, "Applies rules")
    Rel(syncService, domain, "Applies rules")
    Rel(storePorts, httpStore, "Implemented by")
    Rel(queuePort, queueAdapter, "Implemented by")
    Rel(notionPort, notionAdapter, "Implemented by")
    Rel(wire, http, "Composes")
    Rel(wire, syncService, "Composes")
    Rel(wire, httpStore, "Injects")
    Rel(wire, queueAdapter, "Injects")
    Rel(wire, notionAdapter, "Injects")
```

The containers map to code as follows:

| Concern | Implementation |
| --- | --- |
| API process | `cmd/server` |
| Sync process | `cmd/poller` |
| Store adapter | `internal/adapter/postgres` |
| Queue adapter | `internal/adapter/queue/pgqueue` (table `sync_jobs`) |
| Infrastructure | `backend/deploy/manifest.yaml` |

## 4. Deployment view

```mermaid
C4Deployment
    title Deployment View - Kubernetes with Postgres

    Deployment_Node(cluster, "Kubernetes cluster", "tuncloud") {
        Deployment_Node(namespace, "reading-tracker namespace", "Kubernetes namespace") {
            Container_Instance(server, "reading-tracker-server", "Go + Echo", "HTTP API on port 8080")
            Container_Instance(poller, "reading-tracker-poller", "Go", "Polls and processes the sync queue on port 8081")
        }
        Deployment_Node(database, "Postgres host", "Private network") {
            ContainerDb(postgres, "Postgres database", "Postgres", "Application data, migrations, and sync queue")
        }
    }
    System_Ext(ingress, "Ingress", "reading.tuando.app")
    System_Ext(extension, "Chrome extension", "User-installed MV3 extension")
    System_Ext(notion, "Notion API", "OAuth and page API")

    Rel(extension, ingress, "HTTPS")
    Rel(ingress, server, "Routes API traffic")
    Rel(server, postgres, "Reads/writes")
    Rel(server, poller, "Shares database-backed queue")
    Rel(poller, postgres, "Polls and updates")
    Rel(server, notion, "OAuth/setup HTTPS")
    Rel(poller, notion, "HTTPS")
```

The two processes share the same Postgres database, but the API and poller
remain separate processes so API latency is independent of Notion API work.
Each process applies pending migrations at startup, before it serves or polls.
The Ingress restricts CORS to the configured extension IDs; application
authentication uses the device token.

## 5. Code map

| Area | Source |
| --- | --- |
| Extension entry points | `extension/src/content`, `extension/src/background`, `extension/src/popup`, `extension/src/options` |
| Extension persistence and API | `extension/src/background/db.ts`, `extension/src/background/api.ts`, `extension/src/background/sync.ts` |
| Backend HTTP adapter | `backend/internal/adapter/httpapi` |
| Backend use cases | `backend/internal/usecase` |
| Backend domain | `backend/internal/domain` |
| Storage adapter | `backend/internal/adapter/postgres` |
| Queue adapter | `backend/internal/adapter/queue/pgqueue`, `backend/internal/adapter/queue/msg` |
| Notion adapter | `backend/internal/adapter/notion` |
| Composition roots | `backend/internal/wire`, `backend/cmd` |
| Kubernetes deployment | `backend/deploy/manifest.yaml` |

For behavior and operational procedures, see:

- [`extension/README.md`](../extension/README.md)
- [`backend/README.md`](../backend/README.md)
- [`runbook.md`](runbook.md)
