# User-Keyed Reading History Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Reading history belongs to the Notion user, not to one Notion authorization, and the extension restores the popup's history from the server after connecting.

**Architecture:** Four vertical steps, each leaving the suite green. (1) `articles`, `segments`, `days` are re-keyed from `installation_id` to `user_id` by a data migration, and every reader and writer passes the user id; sync jobs still name the installation. (2) Sync jobs name the user and the worker resolves the user's active installation when it runs. (3) A new `GET /v1/history` returns recent articles and today's total. (4) The extension calls it once after connect and merges the result into IndexedDB; the popup keeps reading local storage. A last task brings the behaviour spec in line.

**Tech Stack:** Go 1.26 (module `reading-tracker/backend`), Echo v4, `pgx/v5`, PostgreSQL 17 (Podman), embedded SQL migrations; Chrome MV3 extension in TypeScript with Dexie, Vite, Vitest, pnpm.

**Spec:** [`docs/superpowers/specs/2026-11-15-user-keyed-reading-history-design.md`](../specs/2026-11-15-user-keyed-reading-history-design.md) (approved). Read it first. Behaviour authority: [`docs/reading-tracker-agent-spec.md`](../../reading-tracker-agent-spec.md).

## Global Constraints

- **One active installation per user stays the rule.** Notion sync state (`notion_page_id`, `notion_url`, `notion_ds_id`, `notion_synced_version`, `notion_synced_at`) stays on the article and day rows.
- **Standing spec rules bind:** each segment counted exactly once (C2); a group's jobs run in order, one at a time (C4); the worker writes absolute values (C6); the extension holds no Notion credentials (C19). Notion content is never deleted.
- **No double counting**, in the migration or in the extension's restore. Restore merges by "larger of the two", never by adding.
- **Restored data is never pushed.** The extension creates no segments for restored articles.
- **Push still requires the user to have an installation** (`apperr.NewNoInstallation()` otherwise), as today. `GET /v1/history` requires only an authenticated device.
- **History limits:** default 50, maximum 100.
- **Migrations are forward-only embedded SQL** in `backend/internal/platform/pgmigrate/migrations/`, applied by the binaries on start. Existing migration files are not edited.
- **`internal/archtest` holds:** use cases depend on `internal/usecase/port` only; an adapter never imports another adapter root.
- **Suite green after every task.** Backend: `cd backend && gofmt -l . ; go vet ./... && go test -race ./...` (gofmt prints nothing, every package `ok`; needs `make pg-up`). Extension: `cd extension && pnpm test && pnpm typecheck`.
- **Tooling:** Podman, never Docker. pnpm in `extension/`. `perl -pi -e` for in-place rewrites; `go run golang.org/x/tools/cmd/goimports@v0.34.0 -w <files>` for imports.

## Calls this plan makes where the design was silent

1. **Two migration files, not one.** `0003` re-keys the reading tables (Task 1); `0004` rewrites queued jobs (Task 2). Both apply in the same startup, so a deploy still sees one step, and each task stays green on its own.
2. **`0004` also enqueues a job for every article and day that is pending after the merge** (`version` ahead of `notion_synced_version`). The design says merged rows are "synced afresh"; nothing else would enqueue them until the article is read again.
3. **A reading row that belonged to an installation other than the user's active one loses its Notion link in `0003`,** merged or not, so it is written into the active installation's database on its next sync instead of pointing at a page in a database nobody writes to.
4. **All of a user's jobs, purge included, share the group `user#<user_id>`.**
5. **`limit` on `GET /v1/history`:** absent → 50; above 100 → 100; not an integer or below 1 → 400 `invalid_request`.

## Review Focus

1. **A first connect that lands in `needs_setup`, or a user whose installation is `revoked`:** history is still returned and restored. Test `TestHistoryWorksWithoutActiveInstallation` (Task 3).
2. **Restore fails (offline, 5xx, malformed body):** connecting still succeeds, the popup shows local data, nothing is half-written. Test `restore failure leaves local data untouched and does not throw` (Task 4).
3. **A device that already holds reading for an article restores it:** the total is the larger of local and server, and one more minute read afterwards adds exactly one minute. Tests `restoreArticle keeps the larger total` and `reading after restore adds on top` (Task 4).
4. **The user's active installation changes while a job is queued:** the job writes to the installation that is active when it runs. Test `TestJobUsesUsersActiveInstallation` (Task 2).
5. **Odd rows in the live database at migration time** — a user with no active installation, rows whose installation is gone, a job queued in the old shape: nothing fails, attributable rows are kept, the rest is dropped. Tests `TestReadingByUserMigration` (Task 1) and `TestSyncJobsByUserMigration` (Task 2).

---

### Task 1: Key reading data by user

**Files:**
- Create: `backend/internal/platform/pgmigrate/migrations/0003_reading_by_user.sql`
- Modify: `backend/internal/platform/pgmigrate/pgmigrate.go`, `export_test.go`; Test: `pgmigrate_test.go`
- Modify: `backend/internal/domain/reading.go` (`SegmentWrite`), `backend/internal/usecase/port/repositories.go` (doc comments, parameter names)
- Modify: `backend/internal/adapter/postgres/articles.go`, `days.go`, `push.go`, `purge.go`
- Modify: `backend/internal/adapter/storetest/storetest.go`, `reading.go`
- Modify: `backend/internal/usecase/readsync/service.go`, `library/library.go`, `setup/activator.go`, `notionsync/article.go`, `day.go`; `backend/internal/adapter/httpapi/handlers_sync.go`, `handlers_articles.go`
- Modify: the `_test.go` files of those packages and `internal/e2e` where they name the installation for reading data

**Interfaces:**
- Produces, schema after `0003`: `articles (user_id, url_hash, …)` PK `(user_id, url_hash)`; `segments (user_id, id, …)` PK `(user_id, id)`; `days (user_id, date, …)` PK `(user_id, date)`; no `installation_id` column on any of the three; every other column unchanged; index `articles_user_last_read ON articles (user_id, last_read_at DESC)`; `segments_expiry` kept.
- Produces: `domain.SegmentWrite.UserID string` replaces `InstallationID`.
- Produces: every `port.Reading` method keeps its shape; its first string argument is now the user id (rename the parameter `inst` → `uid`). `port.DataPurger`: `DeleteInstallationData(ctx, botID)` deletes only the installation row; `DeleteUserData(ctx, uid)` also deletes the user's `segments`, `articles` and `days`.
- Produces: `readsync.Service.Push(ctx, userID, installationID, deviceID string, req PushRequest)`; `library.Service.Status(ctx, userID, installationID string, urls []string)`. Both still return `apperr.NewNoInstallation()` when `installationID == ""`. Jobs are still enqueued with `installationID` (Task 2 changes that).
- Produces: `storetest.UserID = "u1"` used by `storetest.Segment` and the conformance cases in place of `"bot-1"` for reading calls.
- Produces (test-only, `pgmigrate/export_test.go`): `UpThrough(ctx, pool, version int) error` — applies embedded migrations up to and including `version`.

- [ ] **Step 1: Make migrations testable one at a time.** In `pgmigrate.go` move the body of `Up` into `up(ctx, pool, through int) error`, which skips migrations whose version is above `through`; `Up` calls it with `math.MaxInt`. Export `UpThrough = up` in `export_test.go`. Run `go test ./internal/platform/pgmigrate/` — still `ok`.

- [ ] **Step 2: Write the failing migration test** `TestReadingByUserMigration` in `pgmigrate_test.go`. Start from an empty database the way the existing tests in that file do, `UpThrough(ctx, pool, 2)`, insert the fixture with SQL, then `Up`, then assert.

  Fixture (timestamps on 2026-10-06 UTC; unlisted columns take any valid value):

  | table | rows |
  |---|---|
  | `users` | `u1` (active `bot-b`), `u2` (active `bot-c`), `u3` (active `NULL`) |
  | `installations` | `bot-a`→`u1`, `bot-b`→`u1`, `bot-c`→`u2`, `bot-d`→`u3` |
  | `articles` | (`bot-a`,`h1`): total 100, sessions 2, max_progress 0.4, first 10:00, last 11:00, completed false, version 3, page `pa`, ds `ds-a`, synced_version 3 · (`bot-b`,`h1`): total 50, sessions 1, max_progress 0.9, first 09:00, last 12:00, completed true, version 2, page `pb`, ds `ds-b`, synced_version 2 · (`bot-a`,`h2`): total 30, version 1, page `pa2`, ds `ds-a`, synced_version 1 · (`bot-c`,`h1`): total 7, version 1, page `pc`, ds `ds-c`, synced_version 1 · (`bot-d`,`h3`): total 9, version 1, page `pd`, synced_version 1 · (`bot-gone`,`h9`): total 1 |
  | `days` | (`bot-a`,`2026-10-06`): seconds 100, hashes `{h1,h2}`, version 2, page `dpa`, synced_version 2 · (`bot-b`,`2026-10-06`): seconds 50, hashes `{h1}`, version 1, page `dpb`, ds `dds-b`, synced_version 1 · (`bot-gone`,`2026-10-06`): seconds 1 |
  | `segments` | (`bot-a`,`s1`), (`bot-b`,`s1`), (`bot-a`,`s2`), (`bot-gone`,`s3`) |

  Assertions after `Up`:

  | row | expected |
  |---|---|
  | article (`u1`,`h1`) | total 150, sessions 3, max_progress 0.9, first 09:00, last 12:00, completed true, version 5, page `pb`, ds `ds-b`, synced_version 2 |
  | article (`u1`,`h2`) | total 30, version 1, page NULL, url NULL, ds NULL, synced_version NULL, synced_at NULL |
  | article (`u2`,`h1`) | total 7, version 1, page `pc`, ds `ds-c`, synced_version 1 |
  | article (`u3`,`h3`) | total 9, page NULL, synced_version NULL |
  | articles overall | exactly 4 rows; none with `url_hash = 'h9'` |
  | day (`u1`,`2026-10-06`) | seconds 150, hashes equal as a set to `{h1,h2}` with no repeats, version 3, page `dpb`, ds `dds-b`, synced_version 1 |
  | days overall | exactly 1 row |
  | segments | (`u1`,`s1`) once, (`u1`,`s2`) once; 2 rows in all |
  | schema | none of the three tables has a column `installation_id`; index `articles_user_last_read` exists |

- [ ] **Step 3: Run it to verify it fails**

  Run: `cd backend && go test ./internal/platform/pgmigrate/ -run TestReadingByUserMigration -v`
  Expected: FAIL — after `Up` the tables still have `installation_id`.

- [ ] **Step 4: Write `0003_reading_by_user.sql`** to the rules behind those assertions:
  - A row's user is `installations.user_id` of its `installation_id`; rows with no such installation are dropped.
  - Rows of one user with the same `url_hash` (or the same `date`) become one: `total_seconds`, `sessions`, `seconds` and `version` are summed; `max_progress` and `last_read_at` take the maximum; `first_read_at` the minimum; `completed` is true if any was; `article_hashes` is the union without repeats; descriptive article columns come from the row with the earliest `first_read_at`.
  - The Notion columns are those of the row whose installation is the user's `active_installation_id`; if there is none (the user has no active installation, or it had no such row), all of them are NULL.
  - A segment id appearing under several installations of one user is kept once.
  - Build each new table beside the old one, then drop the old and rename, so the file is safe to run inside pgmigrate's single transaction. Recreate `segments_expiry`; create `articles_user_last_read`.

- [ ] **Step 5: Run the migration test to verify it passes**, and that the other pgmigrate tests still pass (`TestUpCreatesEveryTable` may list columns — update it for the new key column only).

- [ ] **Step 6: Re-key the adapter, ports, domain and conformance suite.** `articles.go`, `days.go`, `push.go`: `installation_id` → `user_id` in every statement, `w.InstallationID` → `w.UserID`. `purge.go` per the Interfaces block. In `storetest`, replace the installation id with `storetest.UserID` for reading calls, and change `purgeRemovesInstallationAndUserData` so that: after `DeleteInstallationData("bot-1")` the installation is gone **and the article and day are still readable**; after `DeleteUserData("u1")` `GetArticle` and `GetDay` return `domain.ErrNotFound` and `PendingSync(ctx, "u1", true)` is empty. Add one conformance case:

```go
// readingIsPerUser: one user's reading is invisible to another.
func readingIsPerUser(t *testing.T, s ReadingStore) {
	// Push(t, s, Segment("…0001", ArticleURL, 0, 60, 0.5, TestNow))        // UserID "u1"
	// want: GetArticle(ctx, "u2", URLHash(ArticleURL)) is domain.ErrNotFound
	// want: BatchGetArticles(ctx, "u2", …) is empty; PendingSync(ctx, "u2", true) is empty
}
```

- [ ] **Step 7: Update the callers.**
  - `readsync.Push` and `library.Status`: signatures from the Interfaces block; reading calls use `userID`.
  - `httpapi/handlers_sync.go`, `handlers_articles.go`: pass `p.UserID` then `p.InstallationID()`.
  - `setup/activator.go`: `PendingSync(ctx, inst.UserID, full)`; the enqueue call is unchanged.
  - `notionsync/article.go`, `day.go`: every `s.Reading.*` call passes `inst.UserID` (also inside `forgetArticlePage`, `forgetDayPage`, `dayArticlePages` and the create helpers, which take the user id where they took the installation id).
  - Tests: seed helpers write `SegmentWrite.UserID` equal to the seeded installation's `UserID`; an installation seeded for reading tests must have its `UserID` set. Purge assertions that counted reading rows by `installation_id` count by `user_id`. No assertion's meaning changes.

- [ ] **Step 8: Run the full backend suite, then commit**

  Run: `cd backend && gofmt -l . ; go vet ./... && go test -race ./...`
  Expected: no gofmt output, every package `ok`.
  Run: `cd backend && grep -rn 'installation_id' internal/adapter/postgres/{articles,days,push}.go`
  Expected: no output.

```bash
git add backend
git commit -m "feat(backend): key reading data by user"
```

---

### Task 2: Sync jobs name the user

**Files:**
- Create: `backend/internal/platform/pgmigrate/migrations/0004_sync_jobs_by_user.sql`; Test: `pgmigrate_test.go`
- Modify: `backend/internal/usecase/port/queue.go`, `backend/internal/adapter/queue/msg/message.go`, `syncqueue.go`; Test: `msg/msg_test.go`
- Modify: `backend/internal/usecase/notionsync/service.go`, `article.go`, `day.go`; `readsync/service.go`; `setup/activator.go`
- Test: `backend/internal/usecase/notionsync/*_test.go`, `readsync/service_test.go`, `setup/activator_test.go`, `backend/internal/adapter/queue/pgqueue/pgqueue_test.go`, `backend/internal/e2e/e2e_test.go`

**Interfaces:**
- Consumes: Task 1's user-keyed `port.Reading`; `pgmigrate.UpThrough`.
- Produces: `port.Job{Type, UserID, URLHash, Date, InstallationID}` — article and day jobs set `UserID` and leave `InstallationID` empty; purge jobs set `UserID` and may set `InstallationID`.
- Produces: `msg.Article(uid, urlHash string) Message`, `msg.Day(uid, date string) Message`, `msg.Purge(uid, inst string) Message`. JSON: `{"type":"article","user_id":"…","url_hash":"…"}`, `{"type":"day","user_id":"…","date":"…"}`, purge unchanged. `GroupID()` is `"user#" + UserID` for every type. Dedup keys: `article#<uid>#<hash>`, `day#<uid>#<date>`, `purge#<uid>`.
- Produces: `port.SyncQueue.EnqueueSync(ctx, uid string, hashes, dates []string)` and `EnqueueResync(ctx, uid string, …)`; `Requeue` and `EnqueuePurge` unchanged.

- [ ] **Step 1: Write the failing message tests** in `msg_test.go` (replace the cases they supersede):

```go
func TestMessageJSONShape(t *testing.T) {
	// json.Marshal(msg.Article("u1", "h1")) == `{"type":"article","user_id":"u1","url_hash":"h1"}`
	// json.Marshal(msg.Day("u1", "2026-10-06")) == `{"type":"day","user_id":"u1","date":"2026-10-06"}`
	// json.Marshal(msg.Purge("u1", "bot-1")) == `{"type":"purge","installation_id":"bot-1","user_id":"u1"}`
}

func TestGroupIDs(t *testing.T) {
	// msg.Article("u1","h1").GroupID(), msg.Day("u1","d").GroupID(), msg.Purge("u1","bot-1").GroupID(),
	// msg.Purge("u1","").GroupID() are all "user#u1"
}
```

  `TestJobCarriesEveryField` stays as is. Run `go test ./internal/adapter/queue/msg/` — FAIL (article JSON still has `installation_id`).

- [ ] **Step 2: Change `msg` and `port`** per the Interfaces block; `SyncQueue.EnqueueSync`/`EnqueueResync` build messages from the user id. Run the `msg` tests — PASS. Fix `pgqueue_test.go` call sites (argument meaning only).

- [ ] **Step 3: Write the failing worker tests** in `notionsync`:

```go
// The job writes to whichever installation is the user's active one when it runs.
func TestJobUsesUsersActiveInstallation(t *testing.T) {
	// seed user "u1"; installation A (testBot, template "tpl-1") active for u1; read artURL 60 s at 0.5
	// handle(msg.Article("u1", hash)) → one page in tpl-1's Library; article.NotionDataSourceID == A's LibraryDSID
	// seed installation B (id "bot-2", template "tpl-2", UserID "u1", status active); SetActiveInstallation("u1","bot-2")
	// handle(msg.Article("u1", hash)) and deliver the requeue it produces
	// want: a page for artURL exists in tpl-2's Library with Reading time 1 (minute)
	// want: article.NotionDataSourceID == B's LibraryDSID; the page in tpl-1's Library is unchanged (not archived)
}

func TestJobForUserWithoutActiveInstallationIsAcked(t *testing.T) {
	// cases: user does not exist · user has ActiveInstallationID "" · active installation is needs_setup ·
	//        active installation is revoked · active installation's UserID is another user
	// each: handle(msg.Article("u1", hash)) returns no failures and Notion received 0 requests
}
```

  Run them — FAIL (the worker still looks the job's installation id up).

- [ ] **Step 4: Resolve the installation from the user.** In `notionsync/service.go`, `activeInstallation(ctx, uid string) (*domain.Installation, error)`: load the profile; a missing or deleted profile, an empty `ActiveInstallationID`, a missing installation, a status other than `active`, or an installation whose `UserID != uid` all return `nil, nil` (acknowledge). `syncArticle` and `syncDay` call it with `job.UserID`. `purge` is unchanged.

- [ ] **Step 5: Enqueue by user.** `readsync.Push`: `s.Queue.EnqueueSync(ctx, userID, …)`. `setup.Activator.resync`: `EnqueueResync(ctx, inst.UserID, …)`. Update `readsync/service_test.go` and `setup/activator_test.go` expectations from `msg.Article(bot, …)` to `msg.Article(<user id>, …)`; every existing notionsync test that handled `msg.Article(testBot, …)` / `msg.Day(testBot, …)` passes the seeded user id instead. Run `go test ./internal/usecase/...` — PASS, including Step 3's tests.

- [ ] **Step 6: Write the failing queue-migration test** `TestSyncJobsByUserMigration` in `pgmigrate_test.go`: empty database, `UpThrough(ctx, pool, 3)`, insert, `Up`, assert.

  Insert: users `u1` (active `bot-b`), installations `bot-a`→`u1`, `bot-b`→`u1`; article (`u1`,`h1`) version 5 synced_version 2; article (`u1`,`h2`) version 1 synced_version 1; day (`u1`,`2026-10-06`) version 3 synced_version NULL; and these `sync_jobs` rows (`group_id` = the installation id, as the old code wrote it):

  | id | state | body |
  |---|---|---|
  | 1 | ready | `{"type":"article","installation_id":"bot-a","url_hash":"h1"}` |
  | 2 | leased | `{"type":"article","installation_id":"bot-b","url_hash":"h1"}` |
  | 3 | ready | `{"type":"day","installation_id":"bot-gone","date":"2026-10-06"}` |
  | 4 | dead | `{"type":"article","installation_id":"bot-a","url_hash":"h2"}` |
  | 5 | ready | `{"type":"purge","installation_id":"bot-a","user_id":"u1"}` |

  Assertions after `Up`:
  - jobs 1 and 2: body equals `{"type":"article","user_id":"u1","url_hash":"h1"}` (as jsonb), `group_id = 'user#u1'`, state and `leased_until` unchanged, and their `dedup_id`s differ from each other;
  - job 3 no longer exists;
  - job 4 is unchanged in every column;
  - job 5: body unchanged, `group_id = 'user#u1'`;
  - two new `ready` jobs exist with `group_id = 'user#u1'`: `dedup_id = 'mig4#article#u1#h1'` with body `{"type":"article","user_id":"u1","url_hash":"h1"}`, and `dedup_id = 'mig4#day#u1#2026-10-06'` with body `{"type":"day","user_id":"u1","date":"2026-10-06"}`; no job other than job 4 names `h2`; the table holds 6 rows in all;
  - every non-dead article or day job's body has a `user_id` key and no `installation_id` key.

  Run it — FAIL (`0004` does not exist).

- [ ] **Step 7: Write `0004_sync_jobs_by_user.sql`.** For non-dead article and day jobs: replace `installation_id` in the body with the installation's `user_id`, set `group_id` to `user#<uid>`, and set `dedup_id` to `mig4#<id>` (unique by construction); delete those whose installation does not exist. For non-dead purge jobs set `group_id` to `user#<user_id>`. Then insert one `ready` job, `run_at = now()`, for every article and every day whose `version > COALESCE(notion_synced_version, 0)`, with `dedup_id` `mig4#article#<uid>#<hash>` / `mig4#day#<uid>#<date>`. Run the test — PASS.

- [ ] **Step 8: End-to-end: a new authorization keeps the history.** Add to `e2e_test.go`:

```go
func TestReconnectWithNewBotKeepsHistory(t *testing.T) {
	// connect() (bot "bot-e2e", template "tpl-1"); push one 180 s segment at progress 0.6; deliver
	// → one page in tpl-1's Library
	// ns.AddTemplate("tpl-2"); SetTokenResponse with bot_id "bot-e2e-2", duplicated_template_id "tpl-2",
	//   the same owner user id; run the connect flow again; deliver
	// want: POST /v1/articles/status with the new token returns the article with total_seconds 180
	// want: tpl-2's Library holds one page for the article with Reading time 3; tpl-1's page still exists
	// want: no reading rows were added — articles has exactly 1 row for the user
}
```

  `connect()` currently hard-codes the token response; give it parameters (bot id, template id) with the existing values as the defaults used by the other tests.

- [ ] **Step 9: Run the full backend suite, then commit**

  Run: `cd backend && gofmt -l . ; go vet ./... && go test -race ./...`
  Expected: no gofmt output, every package `ok`.

```bash
git add backend
git commit -m "feat(backend): sync jobs name the user and use their active installation"
```

---

### Task 3: History endpoint

**Files:**
- Modify: `backend/internal/usecase/port/repositories.go`, `backend/internal/adapter/postgres/articles.go`, `backend/internal/adapter/storetest/reading.go`
- Modify: `backend/internal/usecase/library/library.go`; Create: `backend/internal/usecase/library/library_test.go` if the package has none
- Create: `backend/internal/adapter/httpapi/handlers_history.go`; Test: `backend/internal/adapter/httpapi/history_test.go`
- Modify: `backend/internal/adapter/httpapi/app.go` (route), `backend/internal/wire/app.go` (`library.Service` gets `Now`)

**Interfaces:**
- Consumes: Task 1's user-keyed store.
- Produces: `port.Reading.RecentArticles(ctx context.Context, uid string, limit int) ([]domain.Article, error)` — the user's articles, `last_read_at` descending (rows with no `last_read_at` last), ties by `url_hash` ascending.
- Produces: `library.Service{Reading port.Reading; Now func() time.Time}` and

```go
type History struct {
	Articles     []domain.Article
	TodayDate    string // YYYY-MM-DD in the caller's time zone
	TodaySeconds int64
}

// History returns the user's most recently read articles and today's total (design §History endpoint).
func (s *Service) History(ctx context.Context, userID, tz string, limit int) (*History, error)
```

  `tz` empty or not loadable → `apperr.NewInvalid(…)`. `limit` 0 means the default 50; above 100 it is 100. Today's seconds are `GetDay(ctx, userID, <today in tz>).Seconds`, 0 when there is no row.
- Produces: `GET /v1/history?tz=<IANA zone>&limit=<n>`, behind `requireDevice`:

```json
{
  "articles": [
    { "canonical_url": "https://medium.com/p/8f3c2a1b9d4e", "source": "medium", "title": "…", "author": "…",
      "site_name": "…", "word_count": 1000, "published_at": "", "total_seconds": 180, "max_progress": 0.6,
      "last_read_at": "2026-10-06T03:15:00Z", "completed": false, "notion_url": null }
  ],
  "today": { "date": "2026-10-06", "seconds": 180 }
}
```

  `articles` is `[]`, never `null`. `notion_url` is `null` until a page exists. `limit` present but not an integer, or below 1 → 400 `invalid_request`.

- [ ] **Step 1: Write the failing store test** — conformance case `recentArticlesNewestFirst` in `storetest/reading.go`: push segments for three URLs ending at `TestNow+1m`, `+3m`, `+2m` as `u1` and one as `u2`; `RecentArticles(ctx, "u1", 2)` returns the `+3m` then the `+2m` article with their totals; `RecentArticles(ctx, "u1", 10)` returns three; `RecentArticles(ctx, "nobody", 10)` returns an empty slice and no error. Run `go test ./internal/adapter/postgres/ -run TestConformanceReading` — FAIL to compile.

- [ ] **Step 2: Implement `RecentArticles`** in `postgres/articles.go` with `articleColumns` and `scanArticle`; add it to `port.Reading`. Run — PASS.

- [ ] **Step 3: Write the failing API tests** in `history_test.go`, using the package's `newTestEnv`, `seedUser`, `seedInstallation` and a push through `POST /v1/sync/push` to create data (`env.now` is 2026-10-06T03:12:00Z):

```go
func TestHistoryReturnsRecentAndToday(t *testing.T) {
	// push 180 s on artURL (progress 0.6) and 60 s on a second URL that ends later, tz "Asia/Ho_Chi_Minh"
	// GET /v1/history?tz=Asia/Ho_Chi_Minh → 200
	// want: articles has 2 items, the later-read one first; artURL's item has total_seconds 180,
	//       max_progress 0.6, completed false, notion_url null, title and source as pushed
	// want: today == {"date":"2026-10-06","seconds":240}
}

func TestHistoryTodayFollowsTimeZone(t *testing.T) {
	// same data; GET /v1/history?tz=America/Los_Angeles → today.date "2026-10-05", today.seconds 0
}

func TestHistoryWorksWithoutActiveInstallation(t *testing.T) {
	// seed data for the user directly through the store, then for each of: installation needs_setup,
	// installation revoked, no installation at all → GET /v1/history?tz=UTC is 200 with the article listed
}

func TestHistoryIsPerUser(t *testing.T)          { /* a second user's token gets "articles":[] and seconds 0 */ }
func TestHistoryEmptyIsArrayNotNull(t *testing.T) { /* body contains `"articles":[]` */ }
func TestHistoryLimit(t *testing.T) {
	// 3 articles: limit=2 → 2 items; no limit → 3; limit=500 → 3 (capped, not an error)
	// limit=0, limit=-1, limit=abc → 400 invalid_request
}
func TestHistoryRejectsBadTimeZone(t *testing.T) { /* tz missing, tz=Not/AZone → 400 invalid_request */ }
func TestHistoryRequiresDevice(t *testing.T)     { /* no bearer token → 401 */ }
```

  Run `go test ./internal/adapter/httpapi/ -run TestHistory` — FAIL (404).

- [ ] **Step 4: Implement `library.History`, the handler and the route.** Register `v1.GET("/history", h.getHistory, h.requireDevice)` beside the other device routes; the handler passes `principal(c).UserID`. In `wire.NewAPI` build `&library.Service{Reading: d.Store, Now: d.Now}`. Follow `handlers_articles.go` for the nullable `notion_url`.

- [ ] **Step 5: Run the tests, the full suite, then commit**

  Run: `cd backend && gofmt -l . ; go vet ./... && go test -race ./...`
  Expected: no gofmt output, every package `ok`.

```bash
git add backend
git commit -m "feat(backend): add GET /v1/history"
```

---

### Task 4: Extension restores history on connect

**Files:**
- Modify: `extension/src/shared/types.ts`, `extension/src/shared/reading.ts`; Test: `extension/src/shared/reading.test.ts`
- Modify: `extension/src/background/api-wire.ts`, `api.ts`; Test: `api.test.ts`
- Modify: `extension/src/background/db.ts`; Test: `db.test.ts`
- Create: `extension/src/background/restore.ts`; Test: `extension/src/background/restore.test.ts`
- Modify: `extension/src/background/services.ts`, `session.ts`; Test: `services.test.ts`, `session.test.ts`
- Modify: `extension/src/popup/model.ts`; Test: `extension/src/popup/model.test.ts`

**Interfaces:**
- Consumes: `GET /v1/history?tz=…&limit=…` with the JSON of Task 3.
- Produces, `shared/types.ts`:

```ts
export interface HistoryArticle {
  canonicalUrl: string; source: string; title: string; author: string; siteName: string
  wordCount: number; publishedAt: string; totalSeconds: number; maxProgress: number
  lastReadAt: string; completed: boolean; notionUrl: string | null
}
export interface History { articles: HistoryArticle[]; today: { date: string; seconds: number } }
```

- Produces, `shared/reading.ts`:

```ts
// restoreArticle merges a server article into the local one: larger total, larger progress, later last-read.
export function restoreArticle(local: LocalArticle | undefined, server: HistoryArticle): LocalArticle
// todayCarry is how far the server's total for today exceeds what local segments add up to; never below 0.
export function todayCarry(serverSeconds: number, localSeconds: number): number
```

- Produces: `Api.history(token: string, p: { timeZone: string; limit?: number }): Promise<History>`, added to the `ApiClient` pick.
- Produces: `Db.restoreArticles(items: readonly HistoryArticle[], merge: typeof restoreArticle): Promise<void>` (one transaction on `articles`), and KV key `today_carry: { date: string; seconds: number }`.
- Produces: `class RestoreService { constructor(deps: { db: Db; api: Pick<ApiClient, 'history'>; clock: { now(): Date; timeZone(): string }; log: Log }); run(): Promise<boolean> }` — resolves `true` when history was stored, `false` otherwise; never rejects.

- [ ] **Step 1: Write the failing merge tests** in `reading.test.ts`:

```ts
describe('restoreArticle', () => {
  it('creates a local article from the server one', () => {
    // local undefined → totalSeconds/maxProgress/lastReadAt and all descriptive fields from server;
    // originalUrl === server.canonicalUrl
  })
  it('keeps the larger total', () => {
    // local 300 s / 0.4 / 10:00, server 180 s / 0.9 / 12:00 → 300 s, 0.9, 12:00
    // local 100 s, server 180 s → 180 s (never 280)
  })
  it('keeps local descriptive fields and fills empty ones', () => {
    // local title 'L', author '' ; server title 'S', author 'A' → title 'L', author 'A'; local originalUrl kept
  })
})
describe('todayCarry', () => {
  it('is the excess of server over local, never negative', () => {
    // todayCarry(1800, 0) === 1800; todayCarry(1800, 300) === 1500; todayCarry(100, 300) === 0
  })
})
```

  Run `cd extension && pnpm test src/shared/reading.test.ts` — FAIL (not exported).

- [ ] **Step 2: Implement `restoreArticle` and `todayCarry`**, and add the two types. Run — PASS.

- [ ] **Step 3: API client.** Test in `api.test.ts`: `history('tok', { timeZone: 'Asia/Ho_Chi_Minh' })` requests `GET <base>/v1/history?tz=Asia%2FHo_Chi_Minh` with the bearer token and maps the snake_case body (including `notion_url: null`) to `History`; with `limit: 20` the query also has `limit=20`. Implement with a `WireHistory` mapper in `api-wire.ts`, following `articlesStatus`.

- [ ] **Step 4: Local store.** Test in `db.test.ts`: with one existing local article (300 s), `restoreArticles([serverSame180s, serverNew60s], restoreArticle)` leaves the first at 300 s, adds the second with 60 s, and adds no segments (`countUnsynced()` stays 0, `segmentsSince('')` is empty). Add `today_carry` to `KvSchema`. Implement `restoreArticles`.

- [ ] **Step 5: Write the failing restore-service tests** in `restore.test.ts` (fake `api.history`, real `Db` on the test IndexedDB, clock fixed at 2026-10-06T03:12:00Z, zone `Asia/Ho_Chi_Minh`):

```ts
it('stores history and today carry after connect', async () => {
  // token set; history → 2 articles, today { date: '2026-10-06', seconds: 1800 }; no local segments
  // run() resolves true; recentArticles(5) has both; getKv('today_carry') equals { date: '2026-10-06', seconds: 1800 }
  // history was called with { timeZone: 'Asia/Ho_Chi_Minh', limit: 50 }
})
it('subtracts what local segments already count for today', async () => {
  // one local segment today of 300 s; server today 1800 → today_carry.seconds === 1500
  // server today 100 → today_carry.seconds === 0
})
it('ignores a today total for another date', async () => {
  // history.today.date '2026-10-05' → today_carry is { date: '2026-10-05', seconds: … } and the popup test below ignores it
})
it('restore failure leaves local data untouched and does not throw', async () => {
  // history rejects with NetworkError, with an ApiError 500, and resolves with a malformed body:
  // run() resolves false each time; articles and today_carry are unchanged; nothing is thrown
})
it('does nothing without a device token', async () => {
  // run() resolves false; history not called
})
```

  Run — FAIL (module missing).

- [ ] **Step 6: Implement `RestoreService`** and wire it. In `services.ts` add `restore` to `Services` and make the session's `afterConnect` run the restore and then the sync, whatever the restore's outcome: `() => { void restore.run().finally(syncSoon) }`. In `session.ts`, `deleteAccount` also deletes `today_carry`. Update `services.test.ts` / `session.test.ts` for the new wiring: after a successful `connect()`, `history` was requested once and a sync was started; when `history` fails, `connect()` still resolves `{ ok: true }`.

- [ ] **Step 7: Popup shows the restored total.** Tests in `model.test.ts`:

```ts
it('adds the restored carry to today', async () => {
  // today_carry { date: <today in the clock's zone>, seconds: 1500 }, one local segment today of 300 s
  // → todayMinutes === 30
})
it('ignores a carry from another date', async () => {
  // today_carry { date: yesterday, seconds: 1500 }, one local segment today of 300 s → todayMinutes === 5
})
it('reading after restore adds on top', async () => {
  // restored article 180 s; applyRead of a 60 s segment merged with mergeArticle → local article 240 s;
  // with today_carry 180 and that segment today → todayMinutes === 4
})
```

  In `buildPopupModel`, read `today_carry` and add its seconds when its date equals `localDate(now, clock.timeZone())`.

- [ ] **Step 8: Run the extension checks, then commit**

  Run: `cd extension && pnpm test && pnpm typecheck && pnpm build`
  Expected: all tests pass, no type errors, build succeeds.
  Run: `cd extension && pnpm test test/arch.test.ts`
  Expected: PASS (the new file respects the folder rules).

```bash
git add extension
git commit -m "feat(extension): restore reading history after connect"
```

---

### Task 5: Bring the behaviour spec and docs in line

**Files:**
- Modify: `docs/reading-tracker-agent-spec.md`, `docs/reading-tracker-onboarding.md`, `docs/c4-architecture.md`, `extension/README.md`

No test cycle; the gate is Step 3's checks and a reviewer reading the diff. Write what the code does now — read the merged code of Tasks 1–4 first.

- [ ] **Step 1: `docs/reading-tracker-agent-spec.md`.** Do not renumber existing sections, scenarios, tasks or constraints.
  - §3.1–3.2: the three reading tables are keyed by `user_id`; state the ownership rule (reading history belongs to the user; tokens, status and database ids belong to the installation; Notion link columns refer to the page in the active installation's database) and the index.
  - §2.8 push: writes under the caller's user; still requires an installation; jobs are enqueued for the user.
  - §2.9 status: looked up by user.
  - §2.12 queue messages: the new bodies, group `user#<user_id>`, dedup keys.
  - §2.13–2.14 worker: step 1 resolves the user's active installation; list the cases that are acknowledged.
  - §2.2 connect and the setup section: becoming `active` with databases other than the ones the user's rows were written to enqueues every article and day of the user; state that connecting another workspace brings the history into that workspace and leaves the old pages in place.
  - §2.17 account deletion: reading data is deleted by user; deleting an installation deletes only the installation.
  - New endpoint section for `GET /v1/history`, placed after the status endpoint with the next free number in §2: request, response JSON, limit rules, time-zone rule and its accepted drift, "needs a device, not an installation".
  - §3.4 (extension local store): the `today_carry` key and the restore merge rule; the extension task list and scenarios gain: restore after reinstall; new authorization keeps history; restore never pushes; restore failure does not block connect.
  - Migrations: one paragraph on what `0003` and `0004` do to existing data (attribution, merge rule, dropped rows, rewritten and added jobs).

- [ ] **Step 2: The other documents.** `docs/reading-tracker-onboarding.md`: the glossary and data-model passages that say reading data belongs to an installation, and the connect sequence diagram if it implies an empty library on reconnect. `docs/c4-architecture.md`: the API and extension component descriptions gain the history endpoint and the restore step; keep the Mermaid valid. `extension/README.md`: one sentence that history is restored from the server after connecting.

- [ ] **Step 3: Checks**

  Run: `grep -n 'installation_id' docs/reading-tracker-agent-spec.md`
  Expected: only places where the installation is really meant (`auth_codes`, `user_installations`, `active_installation_id`, purge job body).
  Run: `grep -rhoE '\bC[0-9]{1,2}\b' backend --include='*.go' | sort -u` and confirm each id still has a row in the spec.
  Run: `git status --short docs/superpowers`
  Expected: only this plan file, if its checkboxes were ticked.

- [ ] **Step 4: Commit**

```bash
git add docs extension/README.md
git commit -m "docs: reading history is keyed by user and restored on connect"
```

---

## After the last task

Owner's check with real Notion credentials (`cd backend && make pg-up && make run`, extension built against it): connect, read an article until it syncs, remove and reload the extension, connect again — the popup should list the article and today's minutes before anything new is read. Report it as not run if credentials are unavailable.

Deploy note: the first pods of the new version apply `0003` and `0004` on start. Pods of the old version still running at that moment fail their queries until they are replaced; pushes are retried by the extension and jobs by the queue.
