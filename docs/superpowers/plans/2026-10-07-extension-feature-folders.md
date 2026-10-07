# Extension Feature-Folder Restructure Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Reorganize `extension/src` from clean-architecture layers into feature folders (`shared/`, `content/`, `background/`, `popup/`, `options/`). Remove the ports, the adapter classes, the composition root and the per-port test fakes. Change no behaviour.

**Architecture:** Code is grouped by the Chrome context it runs in.
- Modules import Dexie, `fetch` and `chrome.*` directly.
- Pure logic keeps its own small files: the tracker state machine, URL and Medium rules, sync batching and backoff, and article merging.
- Services keep constructor dependencies only where tests need to substitute them. Those deps are the concrete `Db`, a `Pick<Api, …>` of the API client, `now` and `log`.
- The layer test shrinks to two rules: content imports only `content/` and `shared/`, and React never enters the content or background import graph.

**Tech Stack:** TypeScript 7, React 19 (popup/options only), Vite 8 (unchanged two-build setup), Dexie 4, Vitest 5 with jsdom, fake-indexeddb and React Testing Library, pnpm.

**Spec:** `docs/reading-tracker-agent-spec.md` (behaviour is unchanged). The previous plan, `docs/superpowers/plans/2026-10-07-extension.md`, documents what each module does. This plan only moves code and swaps abstractions.

## Global Constraints

- **Behaviour-preserving.** Every user-visible behaviour, message shape, IndexedDB schema (§3.4 tables, snake_case fields, DB name `reading-tracker`), wire JSON and log line stays the same.
  - Each test moves with its code and keeps its assertions. A test may be dropped only when it tested ceremony that no longer exists:
    - the `LocalStore` contract running twice;
    - `MemoryStore`;
    - adapter pass-through tests whose adapter was inlined.
  - Every dropped or changed assertion must be listed in the task report with the reason.
- **Kept fixes.** These must survive, each with its test:
  - focus/blur listeners without capture;
  - `releasePage` when there is no plan;
  - hash-only SPA changes keep the session;
  - 413 splits the batch and drops a single segment, while a 400 keeps the data and backs off;
  - a 401 clears the auth and backoff keys;
  - `connect` clears the backoff keys;
  - the router re-checks the plan on `record`;
  - `deleteAccount` on 401 forgets and clears;
  - Medium hosts refuse `enable` synchronously before the prompt;
  - `disable` tolerates a failed `permissions.remove`;
  - only https server URLs are opened;
  - the permission prompt is reached synchronously.
- **Packages and runtime.** pnpm only (never npm/npx/yarn, no `package-lock.json`). Containers use podman (the backend local server for the smoke test).
- **Thresholds are never inlined** (spec §7). `API_BASE` comes from `import.meta.env.VITE_API_BASE`, and only `background/services.ts` reads it.
- **Every commit is green.** `cd extension && pnpm typecheck && pnpm test` passes after every task. `pnpm build` passes at the end of Tasks 1, 4, 5 and 6. `pnpm smoke` passes 8/8 at the end of Task 6.
- **Dependency rules after the restructure:**
  - `content/**` imports only `content/**` and `shared/**`.
  - `shared/**` imports only `shared/**`.
  - `background/**` imports `background/**` and `shared/**`.
  - `popup/**` and `options/**` may import `shared/**`, `background/**` and their own folder. `options/` may also import `popup/messages.ts`.
  - `react` / `react-dom` are allowed only in `popup/` and `options/`.
  - The import closure of `content/index.ts` and `background/index.ts` never reaches React.

## Review Focus

1. **Content bundle stays lean.** A stray `background/` import from `content/` would bundle Dexie and the API client into every page. The arch rule must catch it (Task 1 table case `content → background` forbidden).
2. **Service worker vs pages.** Popup and options construct services through `createServices()`. They must not register alarms or message listeners. Only `background/index.ts` adds `chrome.*` listeners (Task 4 test: `createServices` touches no `chrome.*.addListener`).
3. **Fake IndexedDB isolation.** Every test gets a fresh DB name, so tests cannot leak data into one another (Task 2 helper `testDb()` uses `crypto.randomUUID()`).
4. **Permission gesture.** After `DomainService` calls `chrome.permissions.request` directly, the prompt must still be reached synchronously from the click (Task 4 keeps the synchronous `events[0]` tests against the chrome mock).
5. **Popup/options under the real build.** The pages now import `background/*` directly. The real-browser smoke test must still pass (Task 6).

## Old → New Map

| Old | New |
|---|---|
| `src/domain/config.ts` `url.ts` `medium.ts` `domains.ts` `reading.ts` | `src/shared/config.ts` `url.ts` `medium.ts` `domains.ts` `reading.ts` |
| `src/domain/tracker.ts` `progress.ts` | `src/content/tracker.ts` `progress.ts` |
| `src/application/types.ts` `errors.ts` | `src/shared/types.ts` `errors.ts` |
| `src/application/protocol.ts` | `src/shared/messages.ts` (now also holds `ConnectResult` and `SyncOutcome`) |
| `src/adapters/chrome/messaging.ts` | `src/shared/messaging.ts` |
| `src/adapters/system/system.ts` (`consoleLog`) | `src/shared/log.ts` (`consoleLog`, `type Log`). `SystemClock` is dropped. |
| `src/adapters/site/*` | `src/content/sites/*` |
| `src/adapters/page/tracker-driver.ts` | `src/content/driver.ts` |
| `src/entry/content.ts` | `src/content/index.ts` |
| `src/adapters/storage/dexie-store.ts` (`DexieStore`) | `src/background/db.ts` (`Db`, `ReadingDb`, `KvSchema`, `KvKey`) |
| `src/adapters/http/api-client.ts` (`HttpApi`) + `wire.ts` | `src/background/api.ts` (`Api`) + `src/background/api-wire.ts` |
| `src/adapters/crypto/pkce.ts` | `src/background/pkce.ts` |
| `src/adapters/chrome/device.ts` | `src/background/device.ts` |
| `src/application/{sync,record,setup,session,domains,router}.ts` | `src/background/{sync,record,setup,session,domains,router}.ts` |
| `src/application/config-service.ts` | `src/background/config.ts` |
| `src/entry/compose.ts` | `src/background/services.ts` (`createServices`) |
| `src/entry/background.ts` | `src/background/index.ts` |
| `src/application/popup-model.ts` | `src/popup/model.ts` |
| `src/ui/popup/*` + `src/adapters/chrome/tabs.ts` + `src/entry/popup.tsx` | `src/popup/*` + `src/popup/tabs.ts` + `src/popup/main.tsx` |
| `src/ui/options/*` + `src/entry/options.tsx` | `src/options/*` + `src/options/main.tsx` |
| `src/application/ports.ts` | deleted: `KvSchema` moves to `db.ts`; `Clock`, `Scheduler`, `AuthFlow`, `Pkce`, `Permissions`, `ContentScripts` are gone |
| `src/adapters/chrome/{identity,permissions,scripting,alarms}.ts` | deleted (inlined into `session.ts`, `domains.ts`, `config.ts`, `index.ts`) |
| `test/fakes.ts`, `test/store-contract.ts`, `test/memory-store.test.ts` | `test/helpers.ts`, `test/chrome.ts` (and `background/db.test.ts`) |

**Moving files:** `git mv` every file and its test, then fix imports. Use `pnpm typecheck` to find each broken import. The shell is zsh, so quote globs.

---

### Task 1: `shared/` and `content/`, plus the new layer rule

**Files:**
- Move (`git mv`, with tests):
  - `src/domain/{config,url,medium,domains,reading}.ts` → `src/shared/`
  - `src/domain/{tracker,progress}.ts` → `src/content/`
  - `src/application/types.ts` and `src/application/errors.ts` → `src/shared/`
  - `src/application/protocol.ts` → `src/shared/messages.ts`
  - `src/adapters/chrome/messaging.ts` → `src/shared/messaging.ts`
  - `src/adapters/site/*` → `src/content/sites/*`
  - `src/adapters/page/tracker-driver.ts` → `src/content/driver.ts`
  - `src/entry/content.ts` → `src/content/index.ts`
- Modify:
  - every importer of a moved file;
  - `src/shared/types.ts` (add `ConnectResult`, `SyncOutcome`, `SessionState`);
  - `src/application/session.ts` and `sync.ts` (import those types from shared);
  - `vite.content.config.ts` (entry path);
  - `test/arch.test.ts` (replaced).

**Interfaces:**
- **Produces:**
  - `shared/messages.ts` exports what `protocol.ts` exported. `shared/types.ts` additionally exports:
    - `type SyncOutcome = 'disconnected' | 'backoff' | 'synced' | 'partial' | 'failed'`;
    - `type ConnectResult = { ok: true; installation: Installation | null } | { ok: false; reason: string }`;
    - `type SessionState = { kind: 'disconnected' } | { kind: 'connected'; user: User | null; installation: Installation | null }`.
  - The application files re-export these names for now. For example, `session.ts` does `export type { ConnectResult, SessionState } from '../shared/types'`, so existing importers keep compiling.

- [ ] **Step 1: Move the files and update imports**

```bash
cd /Users/tuandoquoc/personal/reading-chrome-extension/extension
mkdir -p src/shared src/content/sites
for f in config url medium domains reading; do git mv src/domain/$f.ts src/shared/$f.ts; git mv src/domain/$f.test.ts src/shared/$f.test.ts; done
for f in tracker progress; do git mv src/domain/$f.ts src/content/$f.ts; git mv src/domain/$f.test.ts src/content/$f.test.ts; done
git mv src/application/types.ts src/shared/types.ts
git mv src/application/errors.ts src/shared/errors.ts
git mv src/application/protocol.ts src/shared/messages.ts
git mv src/adapters/chrome/messaging.ts src/shared/messaging.ts
git mv src/adapters/chrome/messaging.test.ts src/shared/messaging.test.ts
git mv src/adapters/site src/content/sites-tmp && git mv src/content/sites-tmp/* src/content/sites/ && rmdir src/content/sites-tmp
git mv src/adapters/page/tracker-driver.ts src/content/driver.ts
git mv src/adapters/page/tracker-driver.test.ts src/content/driver.test.ts
git mv src/entry/content.ts src/content/index.ts
pnpm typecheck 2>&1 | head -60
```

Create `src/shared/log.ts` holding only the `Log` type, moved verbatim from `application/ports.ts`:

```ts
export interface Log {
  info(message: string, ...details: unknown[]): void
  warn(message: string, ...details: unknown[]): void
}
```

`ports.ts` re-exports it with `export type { Log } from '../shared/log'`, and `shared/messaging.ts` imports it from `./log`. Shared code then imports only shared code.

Fix every reported import path. Move `SyncOutcome` (from `application/sync.ts`), `ConnectResult` and `SessionState` (from `application/session.ts`) into `shared/types.ts`. Where they used to be declared, re-export them with `export type { … } from '../shared/types'`. This also removes the `shared/messages.ts → application/*` import.

In `vite.content.config.ts`, change the entry to `resolve(root, 'src/content/index.ts')`.

- [ ] **Step 2: Replace the layer test**

Overwrite `extension/test/arch.test.ts` with the rules below. They apply to whatever folders exist, so the test stays valid while old folders are still being emptied in Tasks 2–6.

```ts
// Dependency rules for the feature-folder layout (plan Global Constraints).
import { readdirSync, readFileSync, statSync } from 'node:fs'
import { join, posix, resolve } from 'node:path'
import { describe, expect, test } from 'vitest'

const SRC = resolve(import.meta.dirname, '../src')

function walk(dir: string): string[] {
  return readdirSync(dir).flatMap((name) => {
    const full = join(dir, name)
    return statSync(full).isDirectory() ? walk(full) : [full]
  })
}

export function sourceFiles(): string[] {
  return walk(SRC)
    .map((f) => f.slice(SRC.length + 1).split('\\').join('/'))
    .filter((f) => /\.tsx?$/.test(f) && !/\.test\.tsx?$/.test(f))
}

// stripComments drops // and /* */ comments but keeps string and template literals intact.
export function stripComments(code: string): string {
  let out = ''
  let i = 0
  while (i < code.length) {
    const c = code[i]!
    const next = code[i + 1]
    if (c === '"' || c === "'" || c === '`') {
      let j = i + 1
      while (j < code.length && code[j] !== c) j += code[j] === '\\' ? 2 : 1
      out += code.slice(i, j + 1)
      i = j + 1
    } else if (c === '/' && next === '/') {
      while (i < code.length && code[i] !== '\n') i++
    } else if (c === '/' && next === '*') {
      const end = code.indexOf('*/', i + 2)
      i = end === -1 ? code.length : end + 2
    } else {
      out += c
      i++
    }
  }
  return out
}

export function importsOf(code: string): string[] {
  const re = /(?:import|export)\s+(?:type\s+)?(?:[^'"]*?\s+from\s+)?['"]([^'"]+)['"]|import\(\s*['"]([^'"]+)['"]\s*\)/g
  return [...stripComments(code).matchAll(re)].map((m) => (m[1] ?? m[2]) as string)
}

export type Target = { rel: string } | { pkg: string }

function resolveImport(fromRel: string, spec: string): Target {
  if (!spec.startsWith('.')) return { pkg: spec.startsWith('@') ? spec.split('/').slice(0, 2).join('/') : spec.split('/')[0]! }
  return { rel: posix.normalize(posix.join(posix.dirname(fromRel), spec)) }
}

const top = (rel: string) => rel.split('/')[0] ?? ''

// violation explains why fromRel may not import target, or returns null.
export function violation(fromRel: string, target: Target): string | null {
  const from = top(fromRel)
  if ('pkg' in target) {
    if ((target.pkg === 'react' || target.pkg === 'react-dom') && from !== 'popup' && from !== 'options') {
      return `${fromRel}: react is only for popup/ and options/`
    }
    return null
  }
  const to = top(target.rel)
  if (from === 'content' && to !== 'content' && to !== 'shared') return `${fromRel}: content may import only content/ and shared/ (${target.rel})`
  if (from === 'shared' && to !== 'shared') return `${fromRel}: shared may import only shared/ (${target.rel})`
  if (from === 'background' && (to === 'popup' || to === 'options' || to === 'content')) return `${fromRel}: background may not import ${to}/`
  return null
}

function candidates(rel: string): string[] {
  return /\.tsx?$/.test(rel) ? [rel] : [`${rel}.ts`, `${rel}.tsx`, `${rel}/index.ts`]
}

// reactImporters walks the relative-import closure of entry over files and returns the files that import react.
export function reactImporters(entry: string, files: Map<string, string>): string[] {
  const seen = new Set<string>()
  const hits: string[] = []
  const queue = [entry]
  while (queue.length > 0) {
    const f = queue.pop()!
    if (seen.has(f) || !files.has(f)) continue
    seen.add(f)
    for (const spec of importsOf(files.get(f)!)) {
      const t = resolveImport(f, spec)
      if ('pkg' in t) {
        if (t.pkg === 'react' || t.pkg === 'react-dom') hits.push(`${f} imports ${t.pkg}`)
      } else {
        const found = candidates(t.rel).find((c) => files.has(c))
        if (found) queue.push(found)
      }
    }
  }
  return hits
}

describe('dependency rules', () => {
  const files = sourceFiles()
  const sources = new Map(files.map((f) => [f, readFileSync(join(SRC, f), 'utf8')]))

  test('the walk finds the source tree (not vacuous)', () => {
    expect(files).toContain('content/index.ts')
    expect(files).toContain('shared/messages.ts')
  })

  test('every import respects the folder rules', () => {
    const problems = files.flatMap((f) => importsOf(sources.get(f)!).map((s) => violation(f, resolveImport(f, s))).filter(Boolean))
    expect(problems).toEqual([])
  })

  test.each(['content/index.ts', 'background/index.ts', 'entry/background.ts'])('%s import closure never reaches react', (entry) => {
    expect(reactImporters(entry, sources)).toEqual([])
  })
})

describe('rule table', () => {
  test.each([
    ['content/driver.ts', { rel: 'content/tracker' }, false],
    ['content/driver.ts', { rel: 'shared/reading' }, false],
    ['content/index.ts', { rel: 'background/db' }, true],
    ['content/index.ts', { rel: 'popup/model' }, true],
    ['shared/url.ts', { rel: 'background/api' }, true],
    ['shared/url.ts', { rel: 'shared/medium' }, false],
    ['background/sync.ts', { rel: 'shared/types' }, false],
    ['background/sync.ts', { rel: 'popup/model' }, true],
    ['popup/model.ts', { rel: 'background/db' }, false],
    ['options/controller.ts', { rel: 'popup/messages' }, false],
    ['popup/App.tsx', { pkg: 'react' }, false],
    ['background/index.ts', { pkg: 'react' }, true],
    ['content/index.ts', { pkg: 'react-dom' }, true],
    ['background/db.ts', { pkg: 'dexie' }, false],
  ] as const)('%s → %j forbidden=%s', (from, target, forbidden) => {
    expect(violation(from, target) !== null).toBe(forbidden)
  })

  test('the closure walker follows .ts/.tsx/index and reports react', () => {
    const files = new Map([
      ['a/entry.ts', "import { b } from './b'\nimport x from './dir'"],
      ['a/b.tsx', "import React from 'react'"],
      ['a/dir/index.ts', "export const y = 1"],
    ])
    expect(reactImporters('a/entry.ts', files)).toEqual(['a/b.tsx imports react'])
  })

  test('comment markers inside strings do not hide imports', () => {
    expect(importsOf("const p = ['https://*/*']\nimport x from '../y'")).toEqual(['../y'])
  })
})
```

During Tasks 1–3, the old `application/`, `adapters/`, `ui/` and `entry/` folders still exist. The rules above put no restrictions on them, and the closure test checks `entry/background.ts` until Task 4 deletes it. Task 6 removes that entry from the list.

- [ ] **Step 3: Verify**

Run: `cd extension && pnpm typecheck && pnpm test && pnpm build && grep -c "import " dist/content.js`
Expected: typecheck clean, every test passes (the count may change only by the arch-test rewrite, which goes into the report), the build succeeds, and the grep prints `0`.

- [ ] **Step 4: Commit**

```bash
cd /Users/tuandoquoc/personal/reading-chrome-extension
git add -A extension
git commit -m "refactor(extension): shared/ and content/ feature folders; folder-based dependency rules"
```

---

### Task 2: `background/db.ts`, `background/api.ts` and the test helpers

**Files:**
- Move:
  - `src/adapters/storage/dexie-store.ts` → `src/background/db.ts`, and its test → `src/background/db.test.ts`
  - `src/adapters/http/api-client.ts` → `src/background/api.ts`
  - `src/adapters/http/wire.ts` → `src/background/api-wire.ts`
  - `src/adapters/http/api-client.test.ts` → `src/background/api.test.ts`
  - `src/adapters/crypto/pkce.ts` (+ test) → `src/background/pkce.ts`
  - `src/adapters/chrome/device.ts` → `src/background/device.ts`
  - `consoleLog` from `src/adapters/system/system.ts` → `src/shared/log.ts`
- Create: `test/setup.ts`, `test/helpers.ts`
- Delete: `test/store-contract.ts`, `test/memory-store.test.ts`. Their cases move into `background/db.test.ts`, which runs once against the real `Db`.
- Modify: `vitest.config.ts` (`setupFiles`)

**Interfaces:**
- **Produces:**
  - `background/db.ts`:
    - `class ReadingDb` (unchanged);
    - `class Db`, renamed from `DexieStore`. It keeps the same methods and drops `implements LocalStore` at the end of Task 3;
    - `KvSchema` and `KvKey`, moved here from `ports.ts`. `ports.ts` re-exports them until Task 4 deletes it.
  - `background/api.ts`:
    - `class Api` (renamed from `HttpApi`, same constructor `{ baseUrl, fetch?, timeoutMs? }`);
    - `type Fetch`;
    - `type ApiClient = Pick<Api, 'oauthStartUrl' | 'config' | 'exchangeCode' | 'me' | 'logout' | 'deleteAccount' | 'push' | 'articlesStatus' | 'setupPages' | 'setup'>`. This is the type services depend on, so tests can pass plain objects.
  - `shared/log.ts`:
    - `type Log = { info(message: string, ...details: unknown[]): void; warn(message: string, ...details: unknown[]): void }`;
    - `consoleLog(scope)`.
    - `SystemClock` is deleted. Callers use `() => new Date()` and `() => Intl.DateTimeFormat().resolvedOptions().timeZone`.
  - `test/helpers.ts`:
    - `testDb(): Db`;
    - `clock(start?: string, zone?: string)` returning `{ now(): Date; timeZone(): string; advance(ms: number): void; current: Date }`;
    - `captureLog()` returning `Log & { lines: string[] }`;
    - `apiStub(over?: Partial<ApiClient>)` returning `ApiClient & { calls: { method: keyof ApiClient; args: unknown[] }[]; count(m: keyof ApiClient): number }`, which answers like a healthy backend;
    - `INSTALLATION`;
    - `META`, `segment(id, over?)` and `record(db, seg, meta?)`, moved from `store-contract.ts`.

- [ ] **Step 1: Move the files and rename the classes**

```bash
cd /Users/tuandoquoc/personal/reading-chrome-extension/extension
mkdir -p src/background
git mv src/adapters/storage/dexie-store.ts src/background/db.ts
git mv src/adapters/storage/dexie-store.test.ts src/background/db.test.ts
git mv src/adapters/http/api-client.ts src/background/api.ts
git mv src/adapters/http/wire.ts src/background/api-wire.ts
git mv src/adapters/http/api-client.test.ts src/background/api.test.ts
git mv src/adapters/crypto/pkce.ts src/background/pkce.ts
git mv src/adapters/crypto/pkce.test.ts src/background/pkce.test.ts
git mv src/adapters/chrome/device.ts src/background/device.ts
git mv src/adapters/system/system.test.ts src/shared/log.test.ts
```

Append `consoleLog` from `src/adapters/system/system.ts` to `src/shared/log.ts`, which already holds the `Log` type from Task 1. Then `git rm src/adapters/system/system.ts`.

Then make these changes:
- **Renames:** `DexieStore` → `Db` and `HttpApi` → `Api` (all uses).
- **`KvSchema`/`KvKey`:** move them from `application/ports.ts` into `background/db.ts`. Add `export type { KvSchema, KvKey } from '../background/db'` in `ports.ts`.
- **`ApiClient`:** add it to `api.ts` as specified above.
- **Clock:** delete `SystemClock` from `shared/log.ts`. In `entry/compose.ts`, replace it with an inline object: `{ now: () => new Date(), timeZone: () => Intl.DateTimeFormat().resolvedOptions().timeZone }`. Drop the SystemClock test from `log.test.ts` and keep the `consoleLog` test.
- **Chrome device test:** the device-name cases currently in `adapters/chrome/chrome-adapters.test.ts` move to `background/device.test.ts`.

- [ ] **Step 2: Test setup and helpers**

`extension/test/setup.ts`:

```ts
// Every test file gets an in-memory IndexedDB.
import 'fake-indexeddb/auto'
```

In `extension/vitest.config.ts`, add `setupFiles: ['test/setup.ts'],` inside `test`.

`extension/test/helpers.ts`:

```ts
// Shared test helpers: a fresh IndexedDB, a fixed clock, a log capture and an API stub.
import { parseRemoteConfig } from '../src/shared/config'
import { mergeArticle, type ArticleMeta, type Segment, type StoredSegment } from '../src/shared/reading'
import type { Installation } from '../src/shared/types'
import type { Log } from '../src/shared/log'
import { Db, ReadingDb } from '../src/background/db'
import type { ApiClient } from '../src/background/api'
import { wireConfig } from './fixtures'

export function testDb(): Db {
  return new Db(new ReadingDb(`test-${crypto.randomUUID()}`))
}

export function clock(start = '2026-10-06T03:00:00.000Z', zone = 'Asia/Ho_Chi_Minh') {
  const c = {
    current: new Date(start),
    now: () => new Date(c.current),
    timeZone: () => zone,
    advance: (ms: number) => {
      c.current = new Date(c.current.getTime() + ms)
    },
  }
  return c
}

export function captureLog(): Log & { lines: string[] } {
  const lines: string[] = []
  return { lines, info: (m: string) => lines.push(`info ${m}`), warn: (m: string) => lines.push(`warn ${m}`) }
}

export const INSTALLATION: Installation = {
  id: 'b1', workspaceName: "Tuan's Notion", status: 'active', setupError: '', libraryUrl: 'https://www.notion.so/lib',
}

export const META: ArticleMeta = {
  canonicalUrl: 'https://medium.com/p/8f3c2a1b9d4e', source: 'medium', title: 'Designing Idempotent APIs',
  author: 'Jane Doe', siteName: 'Better Programming', wordCount: 1820, publishedAt: '2026-09-30',
  originalUrl: 'https://freedium-mirror.cfd/https://medium.com/x-8f3c2a1b9d4e',
}

export function segment(id: string, over: Partial<StoredSegment> = {}): StoredSegment {
  return {
    id, sessionId: 'sess', seq: 0, canonicalUrl: META.canonicalUrl,
    startedAt: '2026-10-06T03:00:00.000Z', endedAt: '2026-10-06T03:01:00.000Z', activeSeconds: 60, maxProgress: 0.5,
    synced: 0, createdAt: '2026-10-06T03:01:00.000Z', ...over,
  }
}

export async function record(db: Db, seg: StoredSegment, meta: ArticleMeta = META): Promise<boolean> {
  const plain: Segment = { ...seg }
  return db.applyRead(seg, (prev) => mergeArticle(prev, { ...meta, canonicalUrl: seg.canonicalUrl }, plain))
}

type Calls = { method: keyof ApiClient; args: unknown[] }[]

// apiStub answers like a healthy backend unless a method is overridden; calls are recorded.
export function apiStub(over: Partial<ApiClient> = {}): ApiClient & { calls: Calls; count(m: keyof ApiClient): number } {
  const calls: Calls = []
  const rec = <K extends keyof ApiClient>(m: K, args: unknown[]) => {
    calls.push({ method: m, args })
  }
  return {
    calls,
    count: (m) => calls.filter((c) => c.method === m).length,
    oauthStartUrl: (p) => {
      rec('oauthStartUrl', [p])
      return over.oauthStartUrl?.(p) ?? `https://api.example.com/v1/oauth/notion/start?code_challenge=${p.codeChallenge}`
    },
    config: async () => {
      rec('config', [])
      return over.config ? over.config() : parseRemoteConfig(wireConfig)
    },
    exchangeCode: async (p) => {
      rec('exchangeCode', [p])
      return over.exchangeCode
        ? over.exchangeCode(p)
        : { deviceToken: 'rt_new', deviceId: 'd1', user: { id: 'u1', name: 'Tuan Do' }, installation: INSTALLATION }
    },
    me: async (t) => {
      rec('me', [t])
      return over.me ? over.me(t) : { user: { id: 'u1', name: 'Tuan Do' }, device: { id: 'd1', name: 'Mac' }, installation: INSTALLATION }
    },
    logout: async (t) => {
      rec('logout', [t])
      if (over.logout) await over.logout(t)
    },
    deleteAccount: async (t) => {
      rec('deleteAccount', [t])
      if (over.deleteAccount) await over.deleteAccount(t)
    },
    push: async (t, body) => {
      rec('push', [t, body])
      return over.push ? over.push(t, body) : { accepted: body.segments.map((s) => s.id), duplicate: [], retry: [], rejected: [] }
    },
    articlesStatus: async (t, urls) => {
      rec('articlesStatus', [t, urls])
      return over.articlesStatus ? over.articlesStatus(t, urls) : {}
    },
    setupPages: async (t, q) => {
      rec('setupPages', [t, q])
      return over.setupPages ? over.setupPages(t, q) : []
    },
    setup: async (t, id) => {
      rec('setup', [t, id])
      return over.setup ? over.setup(t, id) : INSTALLATION
    },
  }
}
```

- [ ] **Step 3: Merge the store contract into `background/db.test.ts`**

Copy every test case from the `storeContract` describe block in `test/store-contract.ts` into `background/db.test.ts`, in a `describe('Db', …)` block:
- Use `let db: Db` with `beforeEach(() => { db = testDb() })` in place of `make()`.
- Use `record(db, …)` and `segment(…)` from `test/helpers.ts`.
- Keep the existing snake_case row test.
- Remove the `import 'fake-indexeddb/auto'` line, since the setup file now provides it.

Then `git rm test/store-contract.ts test/memory-store.test.ts`. Do not delete `test/fakes.ts` yet: the application tests still use `MemoryStore` until Task 3.

- [ ] **Step 4: Verify and commit**

Run: `cd extension && pnpm typecheck && pnpm test`.
Expected: PASS. In the report, record the test count before and after: the 10 contract cases now run once instead of twice, and the `SystemClock` test is gone.

```bash
cd /Users/tuandoquoc/personal/reading-chrome-extension
git add -A extension
git commit -m "refactor(extension): background/db and background/api as concrete modules; shared test helpers"
```

---

### Task 3: Sync, record, config and setup move to `background/` with concrete dependencies

**Files:**
- Move (with tests):
  - `src/application/sync.ts` → `src/background/sync.ts`
  - `src/application/record.ts` → `src/background/record.ts`
  - `src/application/config-service.ts` → `src/background/config.ts` (test → `config.test.ts`)
  - `src/application/setup.ts` → `src/background/setup.ts`
- Create: `test/chrome.ts` (chrome mock)
- Modify: `src/entry/compose.ts` and every importer

**Interfaces:**
- **Produces:**
  - `SyncService({ db: Db; api: Pick<ApiClient, 'push'>; clock: { now(): Date; timeZone(): string }; log: Log })`. The methods `run()` and `prune()` are unchanged, and `PUSH_LIMIT` and `RETENTION_DAYS` are unchanged.
  - `RecordService({ db: Db; now: () => Date; config: () => Promise<RemoteConfig | null>; syncSoon: () => void; log: Log })`.
  - `ConfigService({ db: Db; api: Pick<ApiClient, 'config'>; now: () => Date; log: Log })`. The methods `current()`, `refresh()`, `refreshIfStale()` and `ensureSchedule()` are unchanged. It schedules the sync alarm directly with `chrome.alarms.create(SYNC_ALARM, { periodInMinutes: n, delayInMinutes: n })`, so the `Scheduler` port is gone. `SYNC_ALARM` and `CONFIG_MAX_AGE_MS` are unchanged.
  - `SetupService({ db: Db; api: Pick<ApiClient, 'setupPages' | 'setup'> })`.
  - `test/chrome.ts` exports `installChrome(over?)`. It stubs `globalThis.chrome` with `vi.stubGlobal` and returns handles for assertions.

- [ ] **Step 1: The chrome mock**

`extension/test/chrome.ts`:

```ts
// A minimal chrome.* mock for tests. installChrome() stubs the global and returns handles.
import { vi } from 'vitest'

type Listener = (...args: any[]) => unknown

export interface ChromeMock {
  events: string[]
  alarms: Map<string, { periodInMinutes: number; delayInMinutes: number }>
  scripts: Map<string, string[]>
  granted: Set<string>
  allowPermission: boolean
  redirect: (url: string) => Promise<string | undefined>
  listeners: Record<string, Listener[]>
  chrome: any
}

export function installChrome(): ChromeMock {
  const m: ChromeMock = {
    events: [], alarms: new Map(), scripts: new Map(), granted: new Set(), allowPermission: true,
    redirect: async () => 'https://ext.chromiumapp.org/?code=one-time', listeners: {}, chrome: null,
  }
  const listen = (name: string) => ({ addListener: (l: Listener) => (m.listeners[name] ??= []).push(l) })
  m.chrome = {
    runtime: { id: 'ext-id', onMessage: listen('runtime.onMessage'), onInstalled: listen('runtime.onInstalled'), onStartup: listen('runtime.onStartup'),
      sendMessage: vi.fn(async () => ({ ok: true, value: null })), openOptionsPage: vi.fn(async () => {}) },
    alarms: {
      create: vi.fn(async (name: string, info: { periodInMinutes: number; delayInMinutes: number }) => { m.alarms.set(name, info) }),
      onAlarm: listen('alarms.onAlarm'),
    },
    identity: {
      getRedirectURL: () => 'https://ext.chromiumapp.org/',
      launchWebAuthFlow: vi.fn(async ({ url }: { url: string }) => {
        m.events.push(`launch ${url}`)
        return m.redirect(url)
      }),
    },
    permissions: {
      request: vi.fn(async ({ origins }: { origins: string[] }) => {
        m.events.push(`request ${origins.join(' ')}`)
        if (m.allowPermission) for (const o of origins) m.granted.add(o)
        return m.allowPermission
      }),
      remove: vi.fn(async ({ origins }: { origins: string[] }) => {
        m.events.push(`remove ${origins.join(' ')}`)
        for (const o of origins) m.granted.delete(o)
        return true
      }),
    },
    scripting: {
      registerContentScripts: vi.fn(async (list: { id: string; matches: string[] }[]) => { for (const s of list) m.scripts.set(s.id, s.matches) }),
      updateContentScripts: vi.fn(async (list: { id: string; matches: string[] }[]) => { for (const s of list) m.scripts.set(s.id, s.matches) }),
      unregisterContentScripts: vi.fn(async ({ ids }: { ids: string[] }) => { for (const id of ids) m.scripts.delete(id) }),
      getRegisteredContentScripts: vi.fn(async () => [...m.scripts.keys()].map((id) => ({ id }))),
    },
    tabs: { query: vi.fn(async () => [{ id: 7 }]), create: vi.fn(async () => ({})), sendMessage: vi.fn(async () => undefined) },
  }
  vi.stubGlobal('chrome', m.chrome)
  return m
}
```

Test files that use it call `installChrome()` in `beforeEach`. The suite already restores stubs through `restoreMocks`. Add `afterEach(() => vi.unstubAllGlobals())` in those files.

- [ ] **Step 2: Move the services and change their dependencies**

```bash
cd /Users/tuandoquoc/personal/reading-chrome-extension/extension
for f in sync record setup; do git mv src/application/$f.ts src/background/$f.ts; git mv src/application/$f.test.ts src/background/$f.test.ts; done
git mv src/application/config-service.ts src/background/config.ts
git mv src/application/config-service.test.ts src/background/config.test.ts
```

Edit each service:

1. **Dependency types:** `store: LocalStore` becomes `db: Db`, and every `this.deps.store` / `store.` use becomes `db`. `api: Api` (the port) becomes `Pick<ApiClient, …>`, naming the methods the service calls. `Log` comes from `shared/log`. The service no longer imports anything from `ports.ts`.
2. **Clock:**
   - `SyncService` keeps a `clock: { now(): Date; timeZone(): string }` field.
   - `RecordService` and `ConfigService` take `now: () => Date` in place of `clock`, and their `clock.now()` calls become `now()`.
3. **`ConfigService` scheduler:** remove the `scheduler` dependency. Add a module function `async function scheduleSync(minutes: number): Promise<void> { await chrome.alarms.create(SYNC_ALARM, { periodInMinutes: minutes, delayInMinutes: minutes }) }` and call it where `scheduler.every(SYNC_ALARM, …)` was.
4. **`SyncService` auth keys:** `fail()` still deletes `[...AUTH_KEYS, 'sync_failures', 'sync_backoff_until']`. `AUTH_KEYS` stays in `application/session.ts` until Task 4 moves it, so keep the import path working.
5. **`Db`:** remove `implements LocalStore` from `Db` once nothing imports `LocalStore` from these services.

`entry/compose.ts` builds each service with the new dependency names, for example `new SyncService({ db: store, api, clock, log })`. Keep the variable names, and pass `now: () => clock.now()` where a service now takes `now`.

- [ ] **Step 3: Convert the tests**

Apply these rules to `background/{sync,record,config,setup}.test.ts`:

| Before | After |
|---|---|
| `new MemoryStore()` | `testDb()` |
| `new FakeApi({...})` | `apiStub({...})` (same overrides, same `calls` / `count`) |
| `new FixedClock(date, zone)` | `clock(iso, zone)` (`advance`, `current`, `now`, `timeZone` are unchanged) |
| `new MemoryLog()` | `captureLog()` (same `lines`) |
| `new FakeScheduler()` + `scheduler.alarms.get('sync')` | `const ch = installChrome()` + `ch.alarms.get('sync')?.periodInMinutes` |
| `store.segmentRows.has(id)` | `(await db.segmentsSince('')).some((s) => s.id === id)` |
| `store.segmentRows.get(id)?.synced` | `(await db.segmentsSince('')).find((s) => s.id === id)?.synced` |
| `[...store.segmentRows.keys()]` | `(await db.segmentsSince('')).map((s) => s.id)` |
| `store.articleRows.get(url)` | `(await db.articles([url]))[0]` |
| `store.articleRows.set(url, a)` | `await db.applyRead(segment('seed', { canonicalUrl: url, activeSeconds: a.totalSeconds }), () => a)` |
| `record`, `segment`, `META` from `test/store-contract` | the same names from `test/helpers` |

Keep every assertion's meaning. `segmentsSince('')` returns every segment, because every ISO string is ≥ `''`. If a test asserted on Map iteration order, sort the ids before comparing.

- [ ] **Step 4: Verify and commit**

Run: `cd extension && pnpm typecheck && pnpm test`
Expected: PASS. In the report, list any assertion whose wording changed. There must be none whose meaning changed.

```bash
cd /Users/tuandoquoc/personal/reading-chrome-extension
git add -A extension
git commit -m "refactor(extension): sync, record, config and setup in background/ with concrete dependencies"
```

---

### Task 4: Session, domains and router in `background/`, with `services.ts` and `index.ts`

**Files:**
- Move (with tests): `src/application/{session,domains,router}.ts` → `src/background/`
- Create: `src/background/services.ts`, `src/background/services.test.ts`
- Move: `src/entry/background.ts` → `src/background/index.ts`
- Delete:
  - `src/entry/compose.ts`;
  - `src/application/ports.ts`;
  - `src/adapters/chrome/{identity,permissions,scripting,alarms}.ts` and `src/adapters/chrome/chrome-adapters.test.ts`. Keep `tabs.ts`, which moves in Task 5;
  - `test/fakes.ts`.
- Modify: `vite.config.ts` (background input), `test/arch.test.ts` (drop `entry/background.ts` from the closure list)

**Interfaces:**
- **Produces:**
  - `SessionService({ db: Db; api: Pick<ApiClient, 'oauthStartUrl' | 'exchangeCode' | 'me' | 'logout' | 'deleteAccount'>; log: Log; afterConnect?: () => void })`. It uses `chrome.identity`, the PKCE helpers from `./pkce` and `deviceName(navigator)` directly. Its methods (`state`, `connect`, `refresh`, `logout`, `deleteAccount`) and `AUTH_KEYS` are unchanged.
  - `DomainService({ db: Db })`. It calls `chrome.permissions.*` and `chrome.scripting.*` directly. Its methods and `SCRIPT_PREFIX` are unchanged. `enable` still reaches `chrome.permissions.request` before any `await`.
  - `createRouter(services)`, unchanged.
  - `background/services.ts`:
    - `export interface Services { db: Db; api: Api; log: Log; config: ConfigService; sync: SyncService; recorder: RecordService; session: SessionService; setup: SetupService; domains: DomainService }`;
    - `export function createServices(scope: string): Services`. It adds no `chrome.*` listeners.

- [ ] **Step 1: Inline the chrome adapters into the services**

```bash
cd /Users/tuandoquoc/personal/reading-chrome-extension/extension
for f in session domains router; do git mv src/application/$f.ts src/background/$f.ts; git mv src/application/$f.test.ts src/background/$f.test.ts; done
git mv src/entry/background.ts src/background/index.ts
```

**In `background/session.ts`:**
- Remove the `authFlow`, `pkce` and `deviceName` dependencies.
- Inside `connect()`, replace the adapter calls:

```ts
    const verifier = randomVerifier()
    const challenge = await challengeFor(verifier)
    const url = api.oauthStartUrl({ extRedirect: chrome.identity.getRedirectURL(), codeChallenge: challenge })
    let redirect: string
    try {
      const r = await chrome.identity.launchWebAuthFlow({ url, interactive: true })
      if (!r) throw new Error('auth flow ended without a redirect')
      redirect = r
    } catch (e) {
```

- Import `randomVerifier` and `challengeFor` from `./pkce`. Pass `deviceName: deviceName(navigator)` (from `./device`) in the `exchangeCode` call.
- Keep everything else, including the post-exchange refresh and the clearing of the backoff keys.

**In `background/domains.ts`:**
- Remove the `permissions` and `scripts` dependencies, and inline the old adapter bodies.
- Permission prompt:
  - `chrome.permissions.request({ origins })`. Keep it the first awaited call in `enable`, after the synchronous `normalizeDomain` and Medium checks.
  - In `disable`, `chrome.permissions.remove({ origins })` is wrapped in the existing try/catch.
- Content scripts:
  - **Register:** check `(await chrome.scripting.getRegisteredContentScripts()).map((s) => s.id)`. If the id is already there, call `updateContentScripts([script])`; otherwise call `registerContentScripts([script])`. Use `{ id, matches, js: ['content.js'], runAt: 'document_idle', persistAcrossSessions: true }`.
  - **Unregister:** pass only ids Chrome reports as registered.
  - Put these in private module functions `registerScript(id, matches)`, `unregisterScripts(ids)` and `registeredIds()` at the bottom of the file.

**In `background/config.ts`:** `scheduleSync` from Task 3 stays.

**Move `deviceName` and `pkce` tests:** they already live in `background/` since Task 2. Move the scripting, alarms, identity and permissions test cases from `chrome-adapters.test.ts` into `session.test.ts` and `domains.test.ts`, as cases against `installChrome()`. Then `git rm` the adapter files.

- [ ] **Step 2: `services.ts` and `index.ts`**

`extension/src/background/services.ts`:

```ts
// createServices builds the background services. The service worker, popup and options each
// call it; they share one IndexedDB. It registers no chrome listeners (only index.ts does).
import { consoleLog, type Log } from '../shared/log'
import { Api } from './api'
import { ConfigService } from './config'
import { Db, ReadingDb } from './db'
import { DomainService } from './domains'
import { RecordService } from './record'
import { SessionService } from './session'
import { SetupService } from './setup'
import { SyncService } from './sync'

export interface Services {
  db: Db
  api: Api
  log: Log
  config: ConfigService
  sync: SyncService
  recorder: RecordService
  session: SessionService
  setup: SetupService
  domains: DomainService
}

const now = () => new Date()
const timeZone = () => Intl.DateTimeFormat().resolvedOptions().timeZone

export function createServices(scope: string): Services {
  const db = new Db(new ReadingDb())
  const api = new Api({ baseUrl: import.meta.env.VITE_API_BASE as string })
  const log = consoleLog(scope)
  const config = new ConfigService({ db, api, now, log })
  const sync = new SyncService({ db, api, clock: { now, timeZone }, log })
  const syncSoon = () => {
    sync.run().catch((e: unknown) => log.warn(`sync failed: ${e instanceof Error ? e.message : String(e)}`))
  }
  return {
    db, api, log, config, sync,
    recorder: new RecordService({ db, now, config: () => config.current(), syncSoon, log }),
    session: new SessionService({ db, api, log, afterConnect: syncSoon }),
    setup: new SetupService({ db, api }),
    domains: new DomainService({ db }),
  }
}
```

`extension/src/background/services.test.ts` (Review Focus 2):

```ts
import { afterEach, expect, test, vi } from 'vitest'
import { installChrome } from '../../test/chrome'
import { createServices } from './services'

afterEach(() => vi.unstubAllGlobals())

test('createServices registers no chrome listeners and creates no alarms', () => {
  const ch = installChrome()
  vi.stubEnv('VITE_API_BASE', 'http://localhost:8080')
  createServices('test')
  expect(Object.values(ch.listeners).flat()).toHaveLength(0)
  expect(ch.chrome.alarms.create).not.toHaveBeenCalled()
})
```

In `background/index.ts`, replace the imports:
- `compose` becomes `createServices` from `./services`;
- `onAlarm` and `ChromeScheduler` become direct `chrome.alarms.onAlarm.addListener((a) => …)` and `chrome.alarms.create(DAILY_ALARM, { periodInMinutes: DAY_MINUTES, delayInMinutes: DAY_MINUTES })`;
- `serveBackground` now comes from `../shared/messaging`;
- `SYNC_ALARM` comes from `./config`;
- `createRouter` comes from `./router`.

`startup()` keeps the same steps in the same order, and every listener stays at module top level.

In `vite.config.ts`, set `background: resolve(root, 'src/background/index.ts')`. In `test/arch.test.ts`, remove `'entry/background.ts'` from the closure list.

- [ ] **Step 3: Convert the tests and delete the fakes**

Convert the tests:
- **`session.test.ts`:**
  - `installChrome()` replaces `FakeAuthFlow`. Set `ch.redirect` where a test used `flow.respond`, and read launched URLs from `ch.events` (`launch …`).
  - `FakePkce` is gone, so the verifier and challenge are now real. Assert `codeVerifier` matches `/^[A-Za-z0-9\-._~]{64}$/`, and that the challenge in the `oauthStartUrl` call equals `await challengeFor(codeVerifier)`.
- **`domains.test.ts`:**
  - `installChrome()` replaces `FakePermissions` and `FakeContentScripts`. Use `ch.allowPermission`, `ch.granted`, `ch.scripts` and `ch.events`.
  - Keep the synchronous gesture test: `const p = svc.enable('example.com'); expect(ch.events[0]).toBe('request https://example.com/* https://*.example.com/*'); await p`.
  - The `EventStore` subclass that logged storage reads becomes unnecessary. Assert that `ch.events[0]` is the request, so no storage read can come first.
- **`router.test.ts`:** unchanged apart from imports.

Then:
- `git rm test/fakes.ts src/application/ports.ts src/entry/compose.ts src/adapters/chrome/identity.ts src/adapters/chrome/permissions.ts src/adapters/chrome/scripting.ts src/adapters/chrome/alarms.ts src/adapters/chrome/chrome-adapters.test.ts`;
- update the popup and options entries that imported `compose` to import `createServices` from `../background/services` (still in `entry/` until Task 5);
- the options controller test and `OptionsApp` test switch from the fakes to `testDb()`, `apiStub()` and `installChrome()` in the same way.

- [ ] **Step 4: Verify and commit**

Run: `cd extension && pnpm typecheck && pnpm test && pnpm build && grep -c react dist/background.js dist/content.js`
Expected:
- typecheck and tests PASS;
- the build succeeds;
- both grep counts are `0`.

```bash
cd /Users/tuandoquoc/personal/reading-chrome-extension
git add -A extension
git commit -m "refactor(extension): session, domains and router call chrome directly; background/services and index replace compose"
```

---

### Task 5: `popup/` and `options/` feature folders

**Files:**
- Move:
  - `src/ui/popup/*` → `src/popup/`
  - `src/application/popup-model.ts` (+ test) → `src/popup/model.ts`
  - `src/adapters/chrome/tabs.ts` → `src/popup/tabs.ts`
  - `src/entry/popup.tsx` → `src/popup/main.tsx`
  - `src/ui/options/*` → `src/options/`, renaming `options-controller.ts` (+ test) → `controller.ts`
  - `src/entry/options.tsx` → `src/options/main.tsx`
- Modify:
  - `popup.html` and `options.html`: the script src becomes `/src/popup/main.tsx` and `/src/options/main.tsx`, and the stylesheet hrefs become `/src/popup/popup.css` and `/src/options/options.css`;
  - `src/popup/model.ts`: deps `{ db: Db; api: Pick<ApiClient, 'articlesStatus'>; clock: { now(): Date; timeZone(): string } }`.

**Interfaces:**
- **Produces:** `buildPopupModel({ db, api, clock }, session, currentUrl)`. Popup `main.tsx` passes `{ db: s.db, api: s.api, clock: { now: () => new Date(), timeZone: () => Intl.DateTimeFormat().resolvedOptions().timeZone } }`.
- **Unchanged:** all component props and the controller API.

- [ ] **Step 1: Move the files**

```bash
cd /Users/tuandoquoc/personal/reading-chrome-extension/extension
mkdir -p src/popup src/options
git mv src/ui/popup/* src/popup/
git mv src/application/popup-model.ts src/popup/model.ts
git mv src/application/popup-model.test.ts src/popup/model.test.ts
git mv src/adapters/chrome/tabs.ts src/popup/tabs.ts
git mv src/entry/popup.tsx src/popup/main.tsx
git mv src/ui/options/* src/options/
git mv src/options/options-controller.ts src/options/controller.ts
git mv src/options/options-controller.test.ts src/options/controller.test.ts
git mv src/entry/options.tsx src/options/main.tsx
pnpm typecheck 2>&1 | head -60
```

Fix the imports. `options/controller.ts` keeps importing `connectMessage` from `../popup/messages`. In `popup/model.ts`, the `store` dependency becomes `db` (the same mechanical change as Task 3), and its test converts with the same rules: `testDb()`, `apiStub()`, `clock()`. Update the two HTML files as listed above.

- [ ] **Step 2: Verify and commit**

Run: `cd extension && pnpm typecheck && pnpm test && pnpm build && grep -o 'assets/[a-z]*-[^"]*\.css' dist/popup.html dist/options.html`
Expected: everything PASSES, and each page links its hashed CSS.

```bash
cd /Users/tuandoquoc/personal/reading-chrome-extension
git add -A extension
git commit -m "refactor(extension): popup/ and options/ feature folders"
```

---

### Task 6: Remove the old folders, add `pnpm dev`, update the README, run the smoke test

**Files:**
- Delete: whatever remains under `src/domain`, `src/application`, `src/adapters`, `src/ui` and `src/entry`. These should be empty.
- Modify:
  - `package.json`: add the `dev` script;
  - `test/arch.test.ts`: the non-vacuity check names one file per folder;
  - `README.md`: the Architecture section and the Commands table;
  - `scripts/smoke.mjs`: only if a path inside it changed. It reads `dist/`, so no change is expected.

- [ ] **Step 1: Delete the empty folders and tighten the non-vacuity check**

```bash
cd /Users/tuandoquoc/personal/reading-chrome-extension/extension
find src/domain src/application src/adapters src/ui src/entry -type f 2>/dev/null   # must print nothing
rm -rf src/domain src/application src/adapters src/ui src/entry
```

In `test/arch.test.ts`, change the non-vacuity test to:

```ts
  test('the walk finds every feature folder (not vacuous)', () => {
    for (const f of ['shared/messages.ts', 'content/index.ts', 'content/tracker.ts', 'background/index.ts',
      'background/sync.ts', 'background/db.ts', 'popup/main.tsx', 'options/main.tsx']) {
      expect(files).toContain(f)
    }
  })
```

Also check that no `src/` path outside the five feature folders exists. Add this test:

```ts
  test('only the five feature folders exist under src/', () => {
    const tops = new Set(files.map((f) => f.split('/')[0]))
    expect([...tops].sort()).toEqual(['background', 'content', 'options', 'popup', 'shared'])
  })
```

- [ ] **Step 2: `pnpm dev`**

In `extension/package.json` `"scripts"`, add:

```json
    "dev": "vite build --watch & vite build -c vite.content.config.ts --watch"
```

This rebuilds `dist/` on every save; then click reload in `chrome://extensions`. Check it: run `pnpm dev`, touch `src/content/tracker.ts`, and confirm that `dist/content.js` is rewritten (the timestamp changes). Then stop it with Ctrl-C.

- [ ] **Step 3: README**

In `extension/README.md`, replace the **Architecture** section with:

````markdown
## Architecture

Code is organized by the Chrome context it runs in:

| Folder | Runs in | Holds |
|---|---|---|
| `src/shared` | everywhere | types, message protocol + messaging helpers, URL/Medium/domain rules, config parsing, errors, log |
| `src/content` | the page | `index.ts` (entry), `driver.ts` (DOM events → tracker), `tracker.ts` (state machine), `sites/` (Medium/Freedium, generic) |
| `src/background` | service worker (and imported by popup/options) | `index.ts` (listeners, router), `services.ts`, `db.ts` (Dexie), `api.ts` (backend), `sync.ts`, `record.ts`, `config.ts`, `session.ts`, `setup.ts`, `domains.ts` |
| `src/popup` | popup page | React UI + `model.ts` |
| `src/options` | options page | React UI + `controller.ts` |

Two rules are enforced by `test/arch.test.ts`:
- `content/` imports only `content/` and `shared/`, so pages never bundle Dexie or the API client.
- React stays out of the content script and the service worker.

Modules call Dexie, `fetch` and `chrome.*` directly. Tests use fake-indexeddb, `test/helpers.ts` (fresh DB, clock, API stub) and `test/chrome.ts` (a `chrome` mock).
````

In the Commands table, add a row:

```markdown
| `pnpm dev` | rebuild `dist/` on every save (then reload the extension in `chrome://extensions`) |
```

- [ ] **Step 4: Full verification, including the real browser**

```bash
cd /Users/tuandoquoc/personal/reading-chrome-extension/backend
make ddb-up
(make run-local > /private/tmp/reading-local.log 2>&1 &) ; sleep 4
cd ../extension
pnpm check
export CHROME_BIN="$(ls -d "$HOME"/.cache/puppeteer/chrome/*/chrome-mac*/Google\ Chrome\ for\ Testing.app/Contents/MacOS/Google\ Chrome\ for\ Testing | tail -1)"
pnpm smoke
grep -c react dist/background.js dist/content.js
lsof -ti tcp:8080 | xargs kill
```

If `/private/tmp` is not writable, write the log to a scratch directory.

Expected:
- `pnpm check` passes.
- `pnpm smoke` prints eight PASS lines, then `smoke: all checks passed`.
- Both grep counts are `0`.

- [ ] **Step 5: Commit**

```bash
cd /Users/tuandoquoc/personal/reading-chrome-extension
git add -A extension
git commit -m "refactor(extension): remove the layer folders; pnpm dev; README describes the feature folders"
```

---

## Coverage

| Requirement | Task |
|---|---|
| Feature folders `shared/ content/ background/ popup/ options/` | 1–6 |
| No ports, adapter classes, `compose`, or per-port fakes | 2, 3, 4 |
| Direct Dexie, `fetch` and `chrome.*` use | 2, 3, 4 |
| Tests kept, using fake-indexeddb, `apiStub` and the chrome mock | 2, 3, 4, 5 |
| Layer rule shrunk to the content-isolation and React-closure rules | 1, 6 |
| `pnpm dev` rebuild-on-save | 6 |
| Behaviour unchanged (smoke 8/8, all kept fixes tested) | every task, 6 |
