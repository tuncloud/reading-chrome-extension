# Reading history that survives reinstalling the extension

Status: draft for approval · 2026-11-15

## Intent

After the extension is removed and installed again, the popup shows no reading history, even
though the user reconnects the same Notion account. The owner wants the history to belong to
the Notion user, so a reinstall (or a new browser) gets it back.

Two things cause the loss today:

1. **The popup reads only local storage.** "Recent articles" and "today's minutes" come from
   the extension's IndexedDB, which Chrome deletes on uninstall. The backend has the data but
   no endpoint returns it; `POST /v1/articles/status` only answers for URLs the extension
   already knows.
2. **Server data hangs off the Notion authorization, not the user.** `articles`, `segments`
   and `days` are keyed by `installation_id` (Notion's `bot_id`). The user is already
   identified by `notion_user_id`, but if a reconnect ever yields a different `bot_id`, the
   user's active installation becomes a new, empty one and the old data is orphaned.

This project fixes both: reading data is keyed by user, and the extension restores the
popup's history from the server after connecting.

## Goals

- A user's reading data is stored per user. Connecting again — same or different `bot_id`,
  same or different workspace — reaches the same data.
- After connecting, the popup shows the user's recent articles and today's reading time
  without the user having to read anything first.
- Existing data in the deployed Postgres database is carried over, with nothing lost.

## Non-goals

- Multiple active installations per user. One user still has one active installation
  (the v1 rule); Notion sync state stays single-valued.
- Restoring more than the popup shows. No full history browser, no per-segment restore.
- Changing what Notion pages contain or how sync decides to create or update them.
- Cross-device live sync of the popup. Restore happens when a device connects.
- Deleting or migrating Notion content. Pages in an old database are never touched.

## Anti-goals

- Double-counting reading time: in the migration, or when a device that already holds local
  data connects.
- A restore that makes the extension push restored data back to the server as if it were new.
- A deploy that needs manual database surgery.

## Constraints

- Postgres is the only store; schema changes are embedded SQL migrations applied by the
  binaries on start (`internal/platform/pgmigrate`). The next migration is `0003`.
- The spec's standing rules keep binding: each segment counted exactly once (C2), jobs of one
  group run in order one at a time (C4), absolute values written to Notion (C6), the
  extension holds no Notion credentials (C19).
- `docs/reading-tracker-agent-spec.md` is the behaviour authority and is updated with this
  change (data model §3.1–3.2, push §2.8, queue messages §2.12, worker §2.13–2.14, account
  deletion §2.17, plus the new endpoint).
- Architecture rules in `internal/archtest` hold. Use cases depend on ports only.
- The extension uses pnpm; local containers use Podman.

## Approaches

1. **Add a history endpoint only.** The extension restores from the active installation's
   data; no schema change. Smallest change, but a reconnect with a new `bot_id` still starts
   empty.
2. **History endpoint, plus move data when a user gets a new installation.** Handles a
   changed `bot_id` by copying rows at connect time. Keeps the misleading model (data
   belongs to an authorization) and adds a copy step that must stay correct forever.
3. **Key reading data by user, plus the history endpoint.** Chosen by the owner. The model
   says what is true — reading history belongs to a person, Notion credentials belong to an
   authorization — and nothing has to be moved at connect time. It costs a schema migration
   and a change to queue messages.

## Design

### Data model

- `articles`, `segments` and `days` replace `installation_id` with `user_id`. Primary keys
  become `(user_id, url_hash)`, `(user_id, id)` and `(user_id, date)`.
- `installations` keeps what belongs to an authorization: sealed tokens, status, the Notion
  database ids and property map. `users.active_installation_id` and `user_installations`
  stay.
- Notion sync state (`notion_page_id`, `notion_url`, `notion_ds_id`,
  `notion_synced_version`, `notion_synced_at`) stays on the article and day rows. It refers
  to the page in whichever database the user's active installation writes to. The existing
  rule already covers a change of database: a row whose `notion_ds_id` differs from the
  installation's current data source forgets its page and gets a new one.
- A new index supports "most recently read first" per user.

### What follows from that

- **Push** writes under the caller's user id. It still requires the user to have an
  installation, as today.
- **Queue jobs** identify the user, not the installation: article and day jobs carry
  `user_id`, and a user's jobs form one ordered group. The worker loads the user's active
  installation to get tokens and the target databases. If the user has no active
  installation, or it is not `active`, the job is acknowledged and the data waits, exactly
  as now.
- **Connecting** needs no data handling. A new installation for an existing user becomes the
  active one. Whenever an installation becomes `active` with databases other than the ones
  the user's rows were last written to, **every** article and day of the user is enqueued,
  not only rows with unsynced changes. Both existing activation paths already ask for a
  full resync in that situation (`connect` when the Library or Daily data source changed —
  which includes a brand-new installation — and `POST /v1/setup` always); the requirement
  is that this stays true once the rows are per user. The worker's "different data source →
  forget the page and re-create" rule then writes each row into the new database.
  This is a behaviour change worth stating: **connecting a different workspace now brings
  the history along into that workspace's Notion.** Pages already written to the previous
  database stay there.
- **Account deletion** removes reading data by user. Deleting an installation removes only
  the installation.

### Migration `0003`

One SQL migration, run automatically on start:

- Each reading row gets the `user_id` of its installation (`installations.user_id`).
- Rows whose installation no longer exists cannot be attributed and are deleted. (A purged
  installation's rows are deleted with it today, so none are expected.)
- If one user has rows for the same article or the same day under several installations,
  they are merged: time and session counts are added, progress and last-read take the
  maximum, first-read the minimum, completed is true if any was, a day's article list is the
  union. The merged row keeps the Notion link of the row that belonged to the user's active
  installation, if there is one; otherwise it has none and is synced afresh. Its `version`
  ends up higher than any `notion_synced_version` it keeps, so the next sync writes the
  merged totals.
- Segments keep their ids; if the same segment id exists under two installations of one
  user, one copy is kept.
- Article and day jobs waiting in `sync_jobs` are rewritten to the new message shape and
  group in the same migration, so nothing queued is left undeliverable. Two old jobs may
  now name the same user and key; both may stay (sync writes absolute values, so an extra
  run is harmless), as long as the rewrite cannot violate the unique dedup index. A job
  whose installation no longer exists is dropped, like its rows. `dead` jobs are left as
  they are. A job that is leased while the migration runs is rewritten like any other.

Merging by addition can over-count if the same reading was pushed to two installations. That
needs the same segments to have been accepted twice under different installations, which the
extension does not do in normal use; it is accepted, not engineered around.

### History endpoint

`GET /v1/history`, authenticated like the other device endpoints, takes the caller's time
zone (as push does) and an optional limit. It returns:

- the user's most recently read articles, newest first — the descriptive fields the
  extension stores locally, plus total seconds, max progress, last-read time, completed, and
  the Notion URL when a page exists;
- the user's total reading seconds for today in that time zone.

Default limit 50, maximum 100. It never returns segments.

It needs only an authenticated device, not an installation: it answers the same way whether
the user's installation is `active`, `needs_setup`, `revoked` or absent, so a restore works
right after a first connect.

"Today" is the stored daily total (`days`) for today's date in the requested time zone. Daily
totals are dated in the time zone of the push that recorded them, so after the user changes
time zone the figure can be off until the next day. That drift is accepted.

### Extension

- After a successful connect, the background worker fetches the history once and writes it
  into local storage, then the popup renders from local storage as it does now. The popup
  itself does not change.
- Restored articles are stored as articles only. No segments are created for them, so
  nothing restored is ever pushed.
- The server does not keep an article's original (pre-canonical) URL. A restored article
  uses its canonical URL in that field; nothing reads it except the next push, and a later
  read of the article fills it in as usual.
- If the device already has local data for an article (it read before connecting, or it
  reconnected without a reinstall), restore never lowers and never adds to what both sides
  already count: the article's total becomes the larger of the local total and the server's
  total, progress the larger of the two, last-read the later. This rule cannot double-count,
  including when a push was accepted but its response was lost. Its cost is that reading
  not yet synced from this device is briefly under-counted when other devices also
  contributed; that is accepted.
- Today's time follows the same rule. At restore the extension keeps, for today's date,
  how far the server's total for today exceeds what local segments already add up to (never
  less than zero). The popup shows that amount plus the local segments of today, so it
  starts at the larger of the two totals and keeps growing as the user reads. The kept
  amount applies only to that date.
- A failed restore (offline, server error) never blocks connecting. The popup simply shows
  what is local, and the restore is tried again on the next connect.

## Decisions made here (author's calls, not the owner's)

- **Notion sync state stays on the reading rows** instead of moving to a per-installation
  table. With one active installation per user it is single-valued, and the existing
  "different data source → re-create" rule handles a switch.
- **Jobs carry `user_id` and resolve the installation at run time,** instead of carrying
  both. One source of truth for "which installation is active".
- **One migration, no expand-then-contract.** While new pods start and old ones are still
  running, old pods will fail their queries for a few seconds. Pushes come back as errors
  the extension already retries, and jobs are retried by the queue. For a single-user
  deployment that is cheaper than a two-step migration.
- **Queued jobs are rewritten by the migration** rather than the worker understanding both
  message shapes.
- **Restore covers 50 articles.** The popup shows five; fifty leaves room for the popup's
  "current tab" lookup to hit recently read articles without a round trip.
- **Restore runs on connect only,** not on every startup.
- **Restore merges by "larger of the two", not "server plus unsynced local".** The second is
  more exact but double-counts when a push was accepted and its response lost; avoiding
  that would need the server to report which segment ids it holds.
- **Today's total comes from the stored daily totals,** accepting drift after a time-zone
  change, rather than re-adding segments by start time.
- **The history endpoint needs a user, not an installation.**
- **Push still requires an installation,** although data is now per user. Lifting that is a
  separate decision.

## Left to the builder

- The exact SQL of the migration and of the merge, and the index definition.
- The JSON field names of `GET /v1/history`, following the existing endpoints' conventions,
  and where its use case lives (the `library` use case is the natural home).
- How the extension stores what it needs to show today's restored total (the requirement is
  the behaviour under "Today's time" above).
- Port method names and signatures after the re-keying.
- Task order, as long as the suite is green at the end of each task.

## How we will know it works

- **Reinstall:** connect, read an article, sync; clear the extension's storage (as an
  uninstall does); connect again → the popup lists the article with its time and shows
  today's minutes, and nothing is pushed.
- **New authorization:** the same Notion user connects with a different `bot_id` → the
  history is there, and once the new installation is active the articles appear in its
  Library database.
- **Migration:** a database with two installations of one user holding the same article →
  after migration one row with summed time, the active installation's page link, and a
  pending sync; a queued job from before the migration is delivered after it.
- **No double count:** a device with local reading for an article connects → after restore
  the article's local total is the larger of the two totals, never their sum; the same for
  today's time; and reading one more minute afterwards adds exactly one minute to both.
- **Account deletion** leaves no reading rows for the user.
- The conformance, API, sync and end-to-end suites pass on Postgres; the extension's unit
  tests pass.
