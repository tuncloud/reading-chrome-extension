# Reading Tracker Extension Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build the Chrome MV3 extension (spec T25–T31) that measures active reading time, stores it in IndexedDB, pushes it to the Go backend, and lets the user connect Notion, finish setup and manage tracked domains.

**Architecture:** Clean architecture, the same rule as the backend: dependencies point inward and a test enforces it.
- `src/domain` holds pure TypeScript rules: URL normalization, Medium canonicals, the tracker state machine and article merging. It has no browser APIs.
- `src/application` holds use cases (record, sync, config, session, setup, domains, popup model) behind ports it defines.
- `src/adapters/*` implement those ports: Dexie, fetch, chrome.*, WebCrypto, site DOM extractors, the DOM tracker driver.
- `src/ui` holds the React components for the popup and options page. They render application models and call use cases; no React reaches the service worker or the content script.
- `src/entry/*` are the four composition roots: background service worker, content script, popup, options.

**Tech Stack:** TypeScript 7 (strict), React 19 for the popup and options page only, Vite 8 with `@vitejs/plugin-react` (two builds: ES-module service worker + React pages, and an IIFE content script without React), Dexie 4, Vitest 5 with jsdom, fake-indexeddb and React Testing Library, pnpm. Real-browser smoke test on Chrome for Testing over the DevTools protocol (no extra npm dependency).

**Pre-validated:** every code block in this plan was extracted into a scratch project and run before the plan was written: 288 tests, a clean `tsc`, both builds, and the Task 13 smoke test in Chrome for Testing 152 against the local backend. Remaining slips should be small; fix compile slips in place and note them in the report.

**Spec:** `docs/reading-tracker-agent-spec.md` (§2 API contracts, §3.4 local storage, §4.2 T25–T31, §5 scenarios, §6 constraints, §7 open questions) and `docs/reading-tracker-onboarding.md` §5 (flows, tracker lifecycle). The backend in `backend/` is the live contract: `backend/internal/adapter/httpapi` is the source of truth for JSON shapes.

## Global Constraints

- Package manager is **pnpm**. Never `npm`, `npx` or `yarn`, and never create `package-lock.json`. One-off binaries run with `pnpm exec` or `pnpm dlx`.
- Container commands, if any are ever needed, use **podman** (backend local runs: `make ddb-up`, `make run-local`).
- Node ≥ 22 (dev machine has 24); Chrome ≥ 120 (`minimum_chrome_version: "120"`).
- Manifest: `permissions: ["storage","alarms","identity","scripting"]`; `optional_host_permissions: ["https://*/*"]`; `"incognito": "not_allowed"`; **no `<all_urls>`** in required permissions (T25, C24).
- `API_BASE` comes from `VITE_API_BASE` at build time and is never hardcoded (🔶 §7). The build fails when it is missing.
- Tracking thresholds (`idle_seconds`, `segment_seconds`, `sync_interval_minutes`, `sync_threshold_segments`, …), `freedium_hosts` and `strip_query_params` come from `GET /v1/config` (cached in `kv.config`). **Never inline them in tracker or sync code** (🔶 §7). If no config is available yet, the content script does not track.
- The extension holds **no** Notion credentials; only the `rt_` device token (C19).
- Never send a segment with `active_seconds = 0`; flush on `pagehide`, never `unload` (C23).
- The generic adapter runs **only** on user-enabled domains; a denied domain is never tracked, even if enabled or a Medium/Freedium host (C24, T31).
- Max 50 segments per push, with only those segments' articles (C25, §2.8).
- Client marks `accepted ∪ duplicate` synced, keeps `retry`, drops `rejected` with a local log line (§2.8).
- Backoff on network error, 5xx, 503 and other non-401 failures: 1, 2, 4 … minutes, capped at 30 (T28). On 401, clear the device token and show "disconnected" (T28).
- Synced segments older than 30 days are deleted locally (§3.4). Unsynced segments are never deleted by pruning.
- IndexedDB (Dexie) schema and field names are exactly §3.4: tables `articles`, `segments`, `kv`, snake_case fields.
- Wire JSON is snake_case exactly as §2; the HTTP adapter maps to camelCase domain/application types.
- Layer rule (enforced by `test/arch.test.ts`):
  - `domain` imports only `domain`, and uses no `chrome`, `window` or `document`.
  - `application` imports only `domain` and `application`, and uses no `chrome`, `window` or `document`.
  - `adapters/<x>` imports `domain`, `application` and its own `adapters/<x>` only.
  - `ui` imports `domain`, `application`, `ui`, and the packages `react` / `react-dom`.
  - `entry` may import anything.
  - npm packages imported from `src/`: `dexie` only in `adapters/storage`; `react` and `react-dom` only in `ui` and `entry`. The content script and the service worker never bundle React.
  - `chrome.` may appear only in `adapters/chrome` and `entry`.
- Times on the wire are RFC 3339 UTC (`Date.prototype.toISOString()`).

## Review Focus

1. **Extension reloaded while a tab is open.** `chrome.runtime.sendMessage` then throws "Extension context invalidated" on every flush. The content script must stop its timers quietly after the first such failure, not throw every second (Task 9 test `stops after the extension context is invalidated`).
2. **The same page gets the content script twice.** A static match and a dynamic registration can overlap, and so can a re-injection. A second injection must not start a second tracker, or every second is counted twice (Task 9 test `a second injection does not start a second tracker`).
3. **A segment message is delivered twice** (retry after the service worker restarts). The local article totals must not double: `applyRead` is idempotent by segment id (Task 4 store contract test `recording the same segment twice counts it once`).
4. **Alarm sync and threshold sync fire together.** Two concurrent `SyncService.run()` calls must share one push loop, not send the same segments twice (Task 6 test `concurrent runs share one push`).
5. **Pruning while offline for over 30 days.** Prune deletes only *synced* old segments; unsynced old segments survive until they sync (Task 4 store contract test `prune keeps unsynced segments`).

## File Structure

```
extension/
├── package.json  tsconfig.json  vitest.config.ts  vite.config.ts  vite.content.config.ts
├── manifest.config.ts          # buildManifest(env) → MV3 manifest object (tested)
├── popup.html  options.html    # page shells, scripts from src/entry/*.tsx
├── .env.example  .gitignore  README.md
├── scripts/smoke.mjs           # real-browser smoke test (Chrome for Testing + local backend)
├── test/
│   ├── arch.test.ts            # layer/dependency rule
│   ├── fixtures.ts             # wire fixtures (the /v1/config body)
│   ├── fakes.ts                # MemoryStore, FakeApi, FixedClock, … (test-only)
│   ├── store-contract.ts       # LocalStore contract run against MemoryStore and DexieStore
│   └── memory-store.test.ts
└── src/
    ├── domain/
    │   ├── config.ts           # RemoteConfig/TrackingConfig + parseRemoteConfig
    │   ├── url.ts              # globMatch, normalizeUrl
    │   ├── medium.ts           # isMediumHost, isFreediumHost, mediumCanonical
    │   ├── domains.ts          # normalizeDomain, originPatterns, chooseAdapter
    │   ├── reading.ts          # ArticleMeta, Segment, LocalArticle, mergeArticle, localDate, todaySeconds, roundMinutes
    │   ├── progress.ts         # readingProgress
    │   └── tracker.ts          # Tracker state machine (pure, time injected)
    ├── application/
    │   ├── types.ts            # Installation, User, Me, TokenGrant, PushBody, PushResult, ArticleStatus, NotionPage
    │   ├── errors.ts           # ApiError, NetworkError
    │   ├── ports.ts            # LocalStore, KvSchema, Api, Clock, AuthFlow, Pkce, Permissions, ContentScripts, Scheduler, Log
    │   ├── protocol.ts         # message types between content/popup and background
    │   ├── record.ts           # RecordService
    │   ├── config-service.ts   # ConfigService
    │   ├── sync.ts             # SyncService
    │   ├── session.ts          # SessionService (connect, me, logout, delete)
    │   ├── setup.ts            # SetupService
    │   ├── domains.ts          # DomainService
    │   ├── router.ts           # createRouter(services) → (msg) => response
    │   └── popup-model.ts      # buildPopupModel
    ├── adapters/
    │   ├── storage/dexie-store.ts
    │   ├── http/api-client.ts
    │   ├── crypto/pkce.ts
    │   ├── chrome/{identity,permissions,scripting,alarms,messaging,tabs,device}.ts
    │   ├── system/system.ts    # SystemClock, consoleLog
    │   ├── site/{types,freedium-medium,generic,metadata,registry}.ts
    │   └── page/tracker-driver.ts
    ├── ui/
    │   ├── popup/
    │   │   ├── Popup.tsx       # <Popup model actions note />: pure view
    │   │   ├── PopupApp.tsx    # loads the model, refreshes /v1/me, handles connect
    │   │   ├── messages.ts     # connectMessage(reason)
    │   │   └── popup.css
    │   └── options/
    │       ├── OptionsView.tsx         # <OptionsView state actions />: pure view
    │       ├── OptionsApp.tsx          # wires the controller to React state
    │       ├── options-controller.ts   # options state machine (plain TS, no React)
    │       └── options.css
    └── entry/
        ├── compose.ts          # builds every service from the real adapters
        ├── background.ts  content.ts  popup.tsx  options.tsx
```

Tests sit next to the code they test (`*.test.ts`); shared fakes live in `extension/test/`.

**Message protocol (background is the only writer of sync state):**
- **Content → background:** `tracking-plan` (which adapter and config for this host) and `record` (one segment and its article).
- **Popup → background:** `connect`, because `launchWebAuthFlow` must outlive the popup, and `sync-now`.
- **Popup → content (active tab):** `current-page` (canonical URL of the article being read).
- **Direct service use:** options and popup compose their own services (Dexie, fetch and chrome adapters) for everything else. `chrome.permissions.request` needs the page's user gesture.

---
### Task 1: Scaffold, manifest and the layer rule

**Files:**
- Create: `extension/package.json`, `extension/tsconfig.json`, `extension/vitest.config.ts`, `extension/vite.config.ts`, `extension/vite.content.config.ts`
- Create: `extension/manifest.config.ts`, `extension/manifest.config.test.ts`
- Create: `extension/popup.html`, `extension/options.html`, `extension/.env.example`, `extension/.gitignore`
- Create: `extension/src/entry/background.ts`, `content.ts`, `popup.tsx`, `options.tsx` (stubs)
- Create: `extension/test/arch.test.ts`

**Interfaces:**
- Consumes: nothing.
- Produces:
  - `buildManifest(env: ManifestEnv): Manifest`, `apiHostPattern(apiBase: string): string` and `STATIC_SITES: readonly string[]` from `manifest.config.ts`.
  - The `pnpm build | test | typecheck | check` scripts.
  - The `dist/` layout: `manifest.json`, `background.js` (ES module service worker), `content.js` (IIFE), `popup.html`, `options.html`, `chunks/*`, `assets/*`.
  - The layer rule test `test/arch.test.ts`, with exported `violation(fromRel, target)` and `globalViolation(fromRel, code)`. It covers `.ts` and `.tsx` files, and allows `react` / `react-dom` only in `ui` (and `entry`).
  - React 19 with JSX (`"jsx": "react-jsx"`) for the popup and options pages only.

- [ ] **Step 1: Create the package and install tooling**

```bash
mkdir -p /Users/tuandoquoc/personal/reading-chrome-extension/extension
cd /Users/tuandoquoc/personal/reading-chrome-extension/extension
cat > package.json <<'EOF'
{
  "name": "reading-tracker-extension",
  "version": "0.1.0",
  "private": true,
  "type": "module",
  "scripts": {
    "build": "vite build && vite build -c vite.content.config.ts",
    "test": "vitest run",
    "test:watch": "vitest",
    "typecheck": "tsc --noEmit",
    "check": "pnpm typecheck && pnpm test && pnpm build"
  }
}
EOF
pnpm add dexie@^4 react@^19 react-dom@^19
pnpm add -D typescript@^7 vite@^8 vitest@^5 jsdom@^30 fake-indexeddb@^6 @types/chrome @types/node \
  @vitejs/plugin-react@^6 @testing-library/react@^16 @testing-library/dom@^10 @types/react@^19 @types/react-dom@^19
test ! -e package-lock.json && echo "no package-lock.json"
```

Expected: `pnpm-lock.yaml` exists and `no package-lock.json` prints.

- [ ] **Step 2: Write the config files**

`extension/tsconfig.json`:

```json
{
  "compilerOptions": {
    "target": "ES2022",
    "module": "ESNext",
    "moduleResolution": "Bundler",
    "lib": ["ES2022", "DOM", "DOM.Iterable"],
    "jsx": "react-jsx",
    "types": ["chrome", "vite/client", "node"],
    "strict": true,
    "noUncheckedIndexedAccess": true,
    "noImplicitOverride": true,
    "noEmit": true,
    "skipLibCheck": true,
    "isolatedModules": true,
    "verbatimModuleSyntax": true,
    "allowImportingTsExtensions": true
  },
  "include": ["src", "test", "*.ts"]
}
```

`extension/vitest.config.ts`. It exists so Vitest does not load `vite.config.ts`, which requires `VITE_API_BASE`:

```ts
import { defineConfig } from 'vitest/config'

export default defineConfig({
  test: {
    include: ['src/**/*.test.{ts,tsx}', 'test/**/*.test.ts', '*.test.ts'],
    environment: 'node',
    restoreMocks: true,
  },
})
```

`extension/.gitignore`:

```
node_modules/
dist/
.env.local
.env.*.local
```

`extension/.env.example`:

```
# Copy to .env.local. The backend's local server (backend: make run-local) listens on :8080.
VITE_API_BASE=http://localhost:8080
# Optional: the manifest "key" (base64 public key) that pins the unpacked extension ID (spec §7).
# EXTENSION_KEY=
```

`extension/popup.html`:

```html
<!doctype html>
<html lang="en">
  <head>
    <meta charset="utf-8" />
    <title>Reading Tracker</title>
  </head>
  <body>
    <main id="app"></main>
    <script type="module" src="/src/entry/popup.tsx"></script>
  </body>
</html>
```

`extension/options.html`:

```html
<!doctype html>
<html lang="en">
  <head>
    <meta charset="utf-8" />
    <title>Reading Tracker — Options</title>
  </head>
  <body>
    <main id="app"></main>
    <script type="module" src="/src/entry/options.tsx"></script>
  </body>
</html>
```

The entry stubs are replaced in later tasks. `src/entry/background.ts`, `src/entry/popup.tsx` and `src/entry/options.tsx` each contain:

```ts
export {}
```

`src/entry/content.ts`:

```ts
// Replaced in Task 9.
export {}
```

- [ ] **Step 3: Write the failing manifest test**

`extension/manifest.config.test.ts`:

```ts
import { describe, expect, test } from 'vitest'
import { apiHostPattern, buildManifest, STATIC_SITES } from './manifest.config'

describe('buildManifest', () => {
  const m = buildManifest({ apiBase: 'https://abc.lambda-url.ap-southeast-1.on.aws', version: '0.1.0' })

  test('requests only the T25 permissions and never <all_urls>', () => {
    expect(m.permissions).toEqual(['storage', 'alarms', 'identity', 'scripting'])
    expect(JSON.stringify(m)).not.toContain('<all_urls>')
    expect(m.optional_host_permissions).toEqual(['https://*/*'])
  })

  test('is not allowed in incognito (C24)', () => {
    expect(m.incognito).toBe('not_allowed')
  })

  test('host permissions are the static sites plus the API origin', () => {
    expect(m.host_permissions).toEqual([...STATIC_SITES, 'https://abc.lambda-url.ap-southeast-1.on.aws/*'])
  })

  test('content script runs on the static sites only', () => {
    expect(m.content_scripts).toEqual([{ matches: [...STATIC_SITES], js: ['content.js'], run_at: 'document_idle' }])
  })

  test('module service worker, popup and options page', () => {
    expect(m.background).toEqual({ service_worker: 'background.js', type: 'module' })
    expect(m.action).toEqual({ default_popup: 'popup.html' })
    expect(m.options_ui).toEqual({ page: 'options.html', open_in_tab: true })
  })

  test('key is included only when given', () => {
    expect(m.key).toBeUndefined()
    expect(buildManifest({ apiBase: 'http://localhost:8080', version: '1.0.0', key: 'MIIB' }).key).toBe('MIIB')
  })
})

describe('apiHostPattern', () => {
  test('drops the port and path: Chrome match patterns ignore ports', () => {
    expect(apiHostPattern('http://localhost:8080')).toBe('http://localhost/*')
    expect(apiHostPattern('https://api.example.com/base/')).toBe('https://api.example.com/*')
  })

  test('rejects non-http(s) and garbage', () => {
    expect(() => apiHostPattern('ftp://x.example')).toThrow(/VITE_API_BASE/)
    expect(() => apiHostPattern('not a url')).toThrow(/VITE_API_BASE/)
  })
})
```

- [ ] **Step 4: Run it to see it fail**

Run: `cd extension && pnpm test manifest`
Expected: FAIL, `Cannot find module './manifest.config'` or `Failed to load url ./manifest.config`.

- [ ] **Step 5: Implement the manifest builder**

`extension/manifest.config.ts`:

```ts
// The MV3 manifest, built at bundle time so API_BASE and the optional key come from env.

// Sites the extension tracks without the user enabling them. New Freedium mirrors from
// /v1/config are not listed here: the user enables them as domains in Options (T31).
export const STATIC_SITES = [
  'https://freedium-mirror.cfd/*',
  'https://freedium.cfd/*',
  'https://medium.com/*',
  'https://*.medium.com/*',
] as const

export interface ManifestEnv {
  apiBase: string
  version: string
  key?: string
}

export interface Manifest {
  manifest_version: 3
  name: string
  description: string
  version: string
  minimum_chrome_version: string
  permissions: string[]
  host_permissions: string[]
  optional_host_permissions: string[]
  background: { service_worker: string; type: 'module' }
  content_scripts: { matches: string[]; js: string[]; run_at: 'document_idle' }[]
  action: { default_popup: string }
  options_ui: { page: string; open_in_tab: boolean }
  incognito: 'not_allowed'
  key?: string
}

// apiHostPattern turns VITE_API_BASE into a host permission. Chrome match patterns
// ignore ports, so http://localhost:8080 becomes http://localhost/*.
export function apiHostPattern(apiBase: string): string {
  let u: URL
  try {
    u = new URL(apiBase)
  } catch {
    throw new Error(`VITE_API_BASE is not a URL: ${apiBase}`)
  }
  if (u.protocol !== 'https:' && u.protocol !== 'http:') {
    throw new Error(`VITE_API_BASE must be http(s): ${apiBase}`)
  }
  return `${u.protocol}//${u.hostname}/*`
}

export function buildManifest(env: ManifestEnv): Manifest {
  const m: Manifest = {
    manifest_version: 3,
    name: 'Reading Tracker',
    description: 'Measures active reading time and logs it to your Notion.',
    version: env.version,
    minimum_chrome_version: '120',
    permissions: ['storage', 'alarms', 'identity', 'scripting'],
    host_permissions: [...STATIC_SITES, apiHostPattern(env.apiBase)],
    optional_host_permissions: ['https://*/*'],
    background: { service_worker: 'background.js', type: 'module' },
    content_scripts: [{ matches: [...STATIC_SITES], js: ['content.js'], run_at: 'document_idle' }],
    action: { default_popup: 'popup.html' },
    options_ui: { page: 'options.html', open_in_tab: true },
    incognito: 'not_allowed',
  }
  if (env.key) m.key = env.key
  return m
}
```

- [ ] **Step 6: Run the manifest test**

Run: `cd extension && pnpm test manifest`
Expected: PASS, 8 tests.

- [ ] **Step 7: Write the two Vite configs**

`extension/vite.config.ts` builds the service worker and the two React pages, and emits `manifest.json`:

```ts
import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import react from '@vitejs/plugin-react'
import { defineConfig, loadEnv, type Plugin } from 'vite'
import { buildManifest } from './manifest.config.ts'

const root = import.meta.dirname

function manifestPlugin(apiBase: string, key: string | undefined): Plugin {
  const pkg = JSON.parse(readFileSync(resolve(root, 'package.json'), 'utf8')) as { version: string }
  return {
    name: 'reading-tracker-manifest',
    generateBundle() {
      const manifest = buildManifest({ apiBase, version: pkg.version, key })
      this.emitFile({ type: 'asset', fileName: 'manifest.json', source: JSON.stringify(manifest, null, 2) })
    },
  }
}

export default defineConfig(({ mode }) => {
  const env = loadEnv(mode, root, '')
  const apiBase = env.VITE_API_BASE
  if (!apiBase) throw new Error('VITE_API_BASE is required: copy .env.example to .env.local')
  return {
    build: {
      outDir: 'dist',
      emptyOutDir: true,
      target: 'chrome120',
      // Chrome supports modulepreload natively; the polyfill would only add a chunk.
      modulePreload: { polyfill: false },
      rollupOptions: {
        input: {
          background: resolve(root, 'src/entry/background.ts'),
          popup: resolve(root, 'popup.html'),
          options: resolve(root, 'options.html'),
        },
        output: {
          entryFileNames: '[name].js',
          chunkFileNames: 'chunks/[name]-[hash].js',
          assetFileNames: 'assets/[name]-[hash][extname]',
        },
      },
    },
    plugins: [react(), manifestPlugin(apiBase, env.EXTENSION_KEY || undefined)],
  }
})
```

`extension/vite.content.config.ts`. Content scripts are classic scripts, so the content script is built as one self-contained IIFE, without shared chunks. It renders no UI, so it has no React plugin and React never reaches the pages you read:

```ts
import { resolve } from 'node:path'
import { defineConfig } from 'vite'

const root = import.meta.dirname

export default defineConfig({
  build: {
    outDir: 'dist',
    emptyOutDir: false,
    target: 'chrome120',
    lib: {
      entry: resolve(root, 'src/entry/content.ts'),
      formats: ['iife'],
      name: 'readingTrackerContent',
      fileName: () => 'content.js',
    },
  },
})
```

- [ ] **Step 8: Write the failing layer-rule test**

`extension/test/arch.test.ts`:

```ts
// The dependency rule (plan Global Constraints): dependencies point inward.
import { readdirSync, readFileSync, statSync } from 'node:fs'
import { join, posix, resolve } from 'node:path'
import { describe, expect, test } from 'vitest'

const SRC = resolve(import.meta.dirname, '../src')
const LAYERS = ['domain', 'application', 'adapters', 'ui', 'entry'] as const
type Layer = (typeof LAYERS)[number]

export type Target = { rel: string } | { pkg: string }

function walk(dir: string): string[] {
  return readdirSync(dir).flatMap((name) => {
    const full = join(dir, name)
    return statSync(full).isDirectory() ? walk(full) : [full]
  })
}

// sourceFiles lists production .ts/.tsx files under src/ as posix paths relative to src/.
export function sourceFiles(): string[] {
  return walk(SRC)
    .map((f) => f.slice(SRC.length + 1).split('\\').join('/'))
    .filter((f) => /\.tsx?$/.test(f) && !/\.test\.tsx?$/.test(f))
}

function layerOf(rel: string): { layer: Layer | null; sub: string } {
  const [first, second = ''] = rel.split('/')
  const layer = (LAYERS as readonly string[]).includes(first ?? '') ? (first as Layer) : null
  return { layer, sub: layer === 'adapters' ? second : '' }
}

function stripComments(code: string): string {
  return code.replace(/\/\*[\s\S]*?\*\//g, '').replace(/(^|[^:])\/\/.*$/gm, '$1')
}

export function importsOf(code: string): string[] {
  const out: string[] = []
  const re = /(?:import|export)\s+(?:type\s+)?(?:[^'"]*?\s+from\s+)?['"]([^'"]+)['"]|import\(\s*['"]([^'"]+)['"]\s*\)/g
  for (const m of stripComments(code).matchAll(re)) out.push((m[1] ?? m[2]) as string)
  return out
}

function resolveImport(fromRel: string, spec: string): Target {
  if (!spec.startsWith('.')) return { pkg: spec.startsWith('@') ? spec.split('/').slice(0, 2).join('/') : spec.split('/')[0]! }
  const joined = posix.normalize(posix.join(posix.dirname(fromRel), spec))
  return { rel: /\.tsx?$/.test(joined) ? joined : `${joined}.ts` }
}

// violation explains why fromRel may not import target, or returns null.
export function violation(fromRel: string, target: Target): string | null {
  const from = layerOf(fromRel)
  if (from.layer === null) return `${fromRel} is outside the layer directories ${LAYERS.join(', ')}`
  if (from.layer === 'entry') return null
  if ('pkg' in target) {
    if (target.pkg === 'dexie' && from.layer === 'adapters' && from.sub === 'storage') return null
    if ((target.pkg === 'react' || target.pkg === 'react-dom') && from.layer === 'ui') return null
    return `${from.layer} may not import the package ${target.pkg}`
  }
  const to = layerOf(target.rel)
  if (to.layer === null) return `${target.rel} is outside src/ layers`
  const allowed: Record<Layer, Layer[]> = {
    domain: ['domain'],
    application: ['domain', 'application'],
    adapters: ['domain', 'application', 'adapters'],
    ui: ['domain', 'application', 'ui'],
    entry: [...LAYERS],
  }
  if (!allowed[from.layer].includes(to.layer)) return `${from.layer} may not import ${to.layer} (${target.rel})`
  if (from.layer === 'adapters' && to.layer === 'adapters' && from.sub !== to.sub) {
    return `adapters/${from.sub} may not import adapters/${to.sub}`
  }
  return null
}

// globalViolation flags browser globals used outside the layers allowed to touch them.
export function globalViolation(fromRel: string, code: string): string | null {
  const { layer, sub } = layerOf(fromRel)
  const body = stripComments(code)
  if (/\bchrome\s*\.\s*[a-zA-Z]/.test(body) && layer !== 'entry' && !(layer === 'adapters' && sub === 'chrome')) {
    return `${fromRel} uses chrome.* outside adapters/chrome and entry`
  }
  if ((layer === 'domain' || layer === 'application') && /\b(window|document)\s*\./.test(body)) {
    return `${fromRel} touches window/document in ${layer}`
  }
  return null
}

describe('layer rule', () => {
  const files = sourceFiles()

  test('the walk finds the composition roots (not vacuous)', () => {
    expect(files).toContain('entry/background.ts')
    expect(files).toContain('entry/content.ts')
  })

  test('every import points inward', () => {
    const problems: string[] = []
    for (const f of files) {
      const code = readFileSync(join(SRC, f), 'utf8')
      for (const spec of importsOf(code)) {
        const v = violation(f, resolveImport(f, spec))
        if (v) problems.push(v)
      }
      const g = globalViolation(f, code)
      if (g) problems.push(g)
    }
    expect(problems).toEqual([])
  })
})

describe('violation rules', () => {
  test.each([
    ['domain/url.ts', { rel: 'domain/medium.ts' }, false],
    ['domain/url.ts', { rel: 'application/ports.ts' }, true],
    ['domain/url.ts', { pkg: 'dexie' }, true],
    ['application/sync.ts', { rel: 'domain/reading.ts' }, false],
    ['application/sync.ts', { rel: 'adapters/http/api-client.ts' }, true],
    ['application/sync.ts', { rel: 'ui/popup/Popup.tsx' }, true],
    ['adapters/http/api-client.ts', { rel: 'application/ports.ts' }, false],
    ['adapters/http/api-client.ts', { rel: 'adapters/http/wire.ts' }, false],
    ['adapters/http/api-client.ts', { rel: 'adapters/storage/dexie-store.ts' }, true],
    ['adapters/storage/dexie-store.ts', { pkg: 'dexie' }, false],
    ['adapters/http/api-client.ts', { pkg: 'dexie' }, true],
    ['ui/popup/Popup.tsx', { rel: 'application/popup-model.ts' }, false],
    ['ui/popup/Popup.tsx', { rel: 'adapters/chrome/tabs.ts' }, true],
    ['ui/popup/Popup.tsx', { pkg: 'react' }, false],
    ['entry/popup.tsx', { pkg: 'react-dom' }, false],
    ['application/popup-model.ts', { pkg: 'react' }, true],
    ['adapters/page/tracker-driver.ts', { pkg: 'react' }, true],
    ['entry/background.ts', { rel: 'adapters/storage/dexie-store.ts' }, false],
    ['lib/x.ts', { rel: 'domain/url.ts' }, true],
  ] as const)('%s → %j forbidden=%s', (from, target, forbidden) => {
    expect(violation(from, target) !== null).toBe(forbidden)
  })

  test.each([
    ['domain/tracker.ts', 'const x = chrome.runtime', true],
    ['application/sync.ts', 'document.title', true],
    ['application/sync.ts', '// a comment about document.title', false],
    ['adapters/chrome/tabs.ts', 'chrome.tabs.query({})', false],
    ['adapters/page/tracker-driver.ts', 'document.addEventListener', false],
    ['adapters/page/tracker-driver.ts', 'chrome.runtime.sendMessage', true],
    ['entry/content.ts', 'chrome.runtime.onMessage', false],
  ] as const)('%s uses %j forbidden=%s', (from, code, forbidden) => {
    expect(globalViolation(from, code) !== null).toBe(forbidden)
  })

  test('importsOf sees static, type-only, re-export and dynamic imports', () => {
    const code = `import a from './a'\nimport type { B } from '../b'\nexport { c } from "./c"\nconst d = import('./d')\nimport './side'`
    expect(importsOf(code)).toEqual(['./a', '../b', './c', './d', './side'])
  })
})
```

- [ ] **Step 9: Run the layer test**

Run: `cd extension && pnpm test arch`
Expected: PASS. Only the entry stubs exist so far, so "every import points inward" holds trivially. The table tests prove the rules bite.

- [ ] **Step 10: Verify the whole scaffold**

```bash
cd /Users/tuandoquoc/personal/reading-chrome-extension/extension
pnpm typecheck
VITE_API_BASE= pnpm build; echo "exit=$?"          # must fail: API base required
cp .env.example .env.local
pnpm build && ls dist && node -e 'const m=require("./dist/manifest.json"); console.log(m.host_permissions.at(-1), m.incognito)'
```

Expected:
- `pnpm typecheck` prints nothing and exits 0.
- The first build prints `VITE_API_BASE is required` and `exit=1`.
- The second build lists `background.js content.js manifest.json options.html popup.html`.
- The node command prints `http://localhost/* not_allowed`.

- [ ] **Step 11: Commit**

```bash
cd /Users/tuandoquoc/personal/reading-chrome-extension
git add extension
git commit -m "feat(extension): scaffold MV3 build, manifest and layer rule"
```

---
### Task 2: Domain rules: config, URLs, Medium canonicals, domains, reading, progress

**Files:**
- Create: `extension/test/fixtures.ts`
- Create: `extension/src/domain/config.ts`, `config.test.ts`
- Create: `extension/src/domain/url.ts`, `url.test.ts`
- Create: `extension/src/domain/medium.ts`, `medium.test.ts`
- Create: `extension/src/domain/domains.ts`, `domains.test.ts`
- Create: `extension/src/domain/reading.ts`, `reading.test.ts`
- Create: `extension/src/domain/progress.ts`, `progress.test.ts`

**Interfaces:**
- Consumes: nothing. The domain is pure TypeScript.
- Produces (all exported from `src/domain/*`):
  - `config.ts`: `TrackingConfig`, `RemoteConfig`, `ConfigError`, `parseRemoteConfig(raw: unknown): RemoteConfig`.
  - `url.ts`: `globMatch(pattern, name): boolean`, `NormalizeOptions`, `normalizeUrl(raw, opts): string | null`.
  - `medium.ts`: `isMediumHost(host)`, `isFreediumHost(host, freediumHosts)`, `mediumPostId(path): string | null`, `mediumCanonical(raw, freediumHosts): string | null`.
  - `domains.ts`: `AdapterName = 'freedium-medium' | 'generic'`, `normalizeDomain(input): string | null`, `originPatterns(domain): string[]`, `hostInDomain(host, domain)`, `DomainLists`, `chooseAdapter(host, freediumHosts, lists): AdapterName | null`.
  - `reading.ts`: `ArticleMeta`, `Segment`, `LocalArticle`, `StoredSegment`, `mergeArticle(prev, meta, seg): LocalArticle`, `roundMinutes(seconds)`, `localDate(at: Date, timeZone): string`, `todaySeconds(segments, now, timeZone)`.
  - `progress.ts`: `ScrollGeometry`, `readingProgress(g): number`.

- [ ] **Step 1: Write the failing tests**

`extension/test/fixtures.ts` holds the shared wire fixtures. Later tasks import it, and must never import another `*.test.ts` file:

```ts
// The exact body of the backend's GET /v1/config defaults (backend/internal/platform/config/default_config.json).
export const wireConfig = {
  version: 1,
  tracking: {
    idle_seconds: 60, segment_seconds: 60, min_create_seconds: 30, complete_progress: 0.9,
    complete_time_ratio: 0.3, words_per_minute: 230, sync_interval_minutes: 2, sync_threshold_segments: 20,
  },
  freedium_hosts: ['freedium-mirror.cfd', 'freedium.cfd'],
  strip_query_params: ['utm_*', 'fbclid', 'gclid', 'mc_cid', 'mc_eid', 'ref', 'source'],
}
```

`extension/src/domain/config.test.ts`:

```ts
import { describe, expect, test } from 'vitest'
import { wireConfig } from '../../test/fixtures'
import { ConfigError, parseRemoteConfig } from './config'

describe('parseRemoteConfig', () => {
  test('maps the §2.10 body to camelCase', () => {
    const c = parseRemoteConfig(wireConfig)
    expect(c.version).toBe(1)
    expect(c.tracking).toEqual({
      idleSeconds: 60, segmentSeconds: 60, minCreateSeconds: 30, completeProgress: 0.9,
      completeTimeRatio: 0.3, wordsPerMinute: 230, syncIntervalMinutes: 2, syncThresholdSegments: 20,
    })
    expect(c.freediumHosts).toEqual(['freedium-mirror.cfd', 'freedium.cfd'])
    expect(c.stripQueryParams).toContain('utm_*')
  })

  test.each([
    ['not an object', null],
    ['no tracking', { ...wireConfig, tracking: undefined }],
    ['zero idle', { ...wireConfig, tracking: { ...wireConfig.tracking, idle_seconds: 0 } }],
    ['string number', { ...wireConfig, tracking: { ...wireConfig.tracking, segment_seconds: '60' } }],
    ['hosts not strings', { ...wireConfig, freedium_hosts: [1] }],
    ['missing strip list', { ...wireConfig, strip_query_params: undefined }],
  ])('rejects %s', (_, raw) => {
    expect(() => parseRemoteConfig(raw)).toThrow(ConfigError)
  })
})
```

`extension/src/domain/url.test.ts`:

```ts
import { describe, expect, test } from 'vitest'
import { globMatch, normalizeUrl } from './url'

const strip = ['utm_*', 'fbclid', 'gclid', 'mc_cid', 'mc_eid', 'ref', 'source']

describe('globMatch', () => {
  test.each([
    ['utm_*', 'utm_source', true],
    ['utm_*', 'UTM_Medium', true],
    ['utm_*', 'xutm_source', false],
    ['ref', 'ref', true],
    ['ref', 'referrer', false],
    ['a.b', 'axb', false],
  ])('%s vs %s → %s', (p, n, want) => {
    expect(globMatch(p, n)).toBe(want)
  })
})

describe('normalizeUrl', () => {
  test('T26: strips tracking params, fragment and trailing slash', () => {
    expect(normalizeUrl('https://example.com/post/?utm_source=x#h', { stripQueryParams: strip })).toBe('https://example.com/post')
  })

  test('lowercases the host and keeps other params in order', () => {
    expect(normalizeUrl('HTTPS://Example.COM/a/?b=1&utm_medium=x&fbclid=y&c=2', { stripQueryParams: strip }))
      .toBe('https://example.com/a?b=1&c=2')
  })

  test('keeps the root slash', () => {
    expect(normalizeUrl('https://example.com/?ref=hn', { stripQueryParams: strip })).toBe('https://example.com/')
  })

  test('upgrades to https only when the site serves https', () => {
    expect(normalizeUrl('http://example.com/a', { stripQueryParams: strip })).toBe('http://example.com/a')
    expect(normalizeUrl('http://example.com/a', { stripQueryParams: strip, preferHttps: true })).toBe('https://example.com/a')
  })

  test('drops credentials', () => {
    expect(normalizeUrl('https://u:p@example.com/a', { stripQueryParams: strip })).toBe('https://example.com/a')
  })

  test.each(['not a url', 'ftp://example.com/a', 'chrome://settings', ''])('rejects %j', (raw) => {
    expect(normalizeUrl(raw, { stripQueryParams: strip })).toBeNull()
  })
})
```

`extension/src/domain/medium.test.ts`:

```ts
import { describe, expect, test } from 'vitest'
import { isFreediumHost, isMediumHost, mediumCanonical, mediumPostId } from './medium'

const hosts = ['freedium-mirror.cfd', 'freedium.cfd']

describe('mediumCanonical (T26)', () => {
  test('Freedium mirror wrapping a Medium URL', () => {
    expect(mediumCanonical(
      'https://freedium-mirror.cfd/https://medium.com/better-programming/designing-idempotent-apis-8f3c2a1b9d4e?source=x', hosts,
    )).toBe('https://medium.com/p/8f3c2a1b9d4e')
  })

  test('Medium author URL gives the same canonical', () => {
    expect(mediumCanonical('https://medium.com/@jane/foo-8f3c2a1b9d4e', hosts)).toBe('https://medium.com/p/8f3c2a1b9d4e')
  })

  test('Medium /p/<id> and subdomain publications', () => {
    expect(mediumCanonical('https://medium.com/p/8f3c2a1b9d4e', hosts)).toBe('https://medium.com/p/8f3c2a1b9d4e')
    expect(mediumCanonical('https://jane.medium.com/foo-8f3c2a1b9d4e?sk=1', hosts)).toBe('https://medium.com/p/8f3c2a1b9d4e')
  })

  test('scenario 23: a new mirror host from config gives the identical canonical', () => {
    const url = 'https://new-mirror.example/https://medium.com/x/designing-idempotent-apis-8f3c2a1b9d4e'
    expect(mediumCanonical(url, hosts)).toBeNull()
    expect(mediumCanonical(url, [...hosts, 'new-mirror.example'])).toBe('https://medium.com/p/8f3c2a1b9d4e')
  })

  test('Freedium wrapping a custom-domain Medium publication', () => {
    expect(mediumCanonical('https://freedium.cfd/https://betterprogramming.pub/some-title-0123456789ab', hosts))
      .toBe('https://medium.com/p/0123456789ab')
  })

  test.each([
    'https://medium.com/@jane',
    'https://medium.com/',
    'https://freedium.cfd/',
    'https://freedium.cfd/about',
    'https://example.com/foo-8f3c2a1b9d4e',
    'garbage',
  ])('no post id → null: %s', (url) => {
    expect(mediumCanonical(url, hosts)).toBeNull()
  })
})

describe('helpers', () => {
  test('isMediumHost', () => {
    expect(isMediumHost('medium.com')).toBe(true)
    expect(isMediumHost('Jane.Medium.com')).toBe(true)
    expect(isMediumHost('notmedium.com')).toBe(false)
  })

  test('isFreediumHost is exact and case-insensitive', () => {
    expect(isFreediumHost('Freedium.cfd', hosts)).toBe(true)
    expect(isFreediumHost('x.freedium.cfd', hosts)).toBe(false)
  })

  test('mediumPostId reads the last segment', () => {
    expect(mediumPostId('/a/b-8f3c2a1b9d4e/')).toBe('8f3c2a1b9d4e')
    expect(mediumPostId('/a/b-xyz')).toBeNull()
  })
})
```

`extension/src/domain/domains.test.ts`:

```ts
import { describe, expect, test } from 'vitest'
import { chooseAdapter, hostInDomain, normalizeDomain, originPatterns } from './domains'

describe('normalizeDomain', () => {
  test.each([
    ['example.com', 'example.com'],
    ['  Example.COM ', 'example.com'],
    ['https://www.example.com/path?q=1', 'example.com'],
    ['blog.example.co.uk', 'blog.example.co.uk'],
  ])('%j → %s', (input, want) => {
    expect(normalizeDomain(input)).toBe(want)
  })

  test.each(['', 'localhost', 'not a domain', 'exa mple.com', '-bad.com', 'chrome://extensions'])('rejects %j', (input) => {
    expect(normalizeDomain(input)).toBeNull()
  })
})

test('originPatterns covers the domain and its subdomains over https', () => {
  expect(originPatterns('example.com')).toEqual(['https://example.com/*', 'https://*.example.com/*'])
})

test('hostInDomain', () => {
  expect(hostInDomain('example.com', 'example.com')).toBe(true)
  expect(hostInDomain('www.example.com', 'example.com')).toBe(true)
  expect(hostInDomain('badexample.com', 'example.com')).toBe(false)
})

describe('chooseAdapter', () => {
  const hosts = ['freedium.cfd']

  test('Medium and Freedium hosts use freedium-medium without being enabled', () => {
    expect(chooseAdapter('medium.com', hosts, { enabled: [], denied: [] })).toBe('freedium-medium')
    expect(chooseAdapter('freedium.cfd', hosts, { enabled: [], denied: [] })).toBe('freedium-medium')
  })

  test('the generic adapter runs only on enabled domains (C24)', () => {
    expect(chooseAdapter('blog.example.com', hosts, { enabled: [], denied: [] })).toBeNull()
    expect(chooseAdapter('blog.example.com', hosts, { enabled: ['example.com'], denied: [] })).toBe('generic')
  })

  test('T31: a denied domain is never tracked, even if enabled or Medium', () => {
    expect(chooseAdapter('blog.example.com', hosts, { enabled: ['example.com'], denied: ['example.com'] })).toBeNull()
    expect(chooseAdapter('medium.com', hosts, { enabled: [], denied: ['medium.com'] })).toBeNull()
  })
})
```

`extension/src/domain/reading.test.ts`:

```ts
import { describe, expect, test } from 'vitest'
import { localDate, mergeArticle, roundMinutes, todaySeconds, type ArticleMeta, type Segment } from './reading'

const meta: ArticleMeta = {
  canonicalUrl: 'https://medium.com/p/8f3c2a1b9d4e', source: 'medium', title: 'Designing Idempotent APIs',
  author: 'Jane Doe', siteName: 'Better Programming', wordCount: 1820, publishedAt: '2026-09-30',
  originalUrl: 'https://freedium-mirror.cfd/https://medium.com/x-8f3c2a1b9d4e',
}

function seg(over: Partial<Segment> = {}): Segment {
  return {
    id: 's1', sessionId: 'sess', seq: 0, canonicalUrl: meta.canonicalUrl,
    startedAt: '2026-10-06T03:01:05.000Z', endedAt: '2026-10-06T03:02:10.000Z', activeSeconds: 58, maxProgress: 0.42, ...over,
  }
}

describe('mergeArticle', () => {
  test('first segment creates the article', () => {
    expect(mergeArticle(undefined, meta, seg())).toEqual({
      ...meta, totalSeconds: 58, maxProgress: 0.42, lastReadAt: '2026-10-06T03:02:10.000Z',
    })
  })

  test('later segments add seconds, keep max progress and latest read time', () => {
    const a = mergeArticle(undefined, meta, seg())
    const b = mergeArticle(a, meta, seg({ id: 's2', activeSeconds: 30, maxProgress: 0.3, endedAt: '2026-10-06T03:05:00.000Z' }))
    expect(b.totalSeconds).toBe(88)
    expect(b.maxProgress).toBe(0.42)
    expect(b.lastReadAt).toBe('2026-10-06T03:05:00.000Z')
    const c = mergeArticle(b, meta, seg({ id: 's0', endedAt: '2026-10-06T02:00:00.000Z' }))
    expect(c.lastReadAt).toBe('2026-10-06T03:05:00.000Z')
  })

  test('empty metadata does not erase what an earlier extraction found', () => {
    const a = mergeArticle(undefined, meta, seg())
    const b = mergeArticle(a, { ...meta, title: '', author: '  ', wordCount: 0 }, seg({ id: 's2' }))
    expect(b.title).toBe('Designing Idempotent APIs')
    expect(b.author).toBe('Jane Doe')
    expect(b.wordCount).toBe(1820)
  })
})

test('roundMinutes rounds half up like the backend', () => {
  expect(roundMinutes(89)).toBe(1)
  expect(roundMinutes(90)).toBe(2)
  expect(roundMinutes(0)).toBe(0)
})

test('localDate uses the given time zone (scenario 16)', () => {
  expect(localDate(new Date('2026-10-05T18:30:00Z'), 'Asia/Ho_Chi_Minh')).toBe('2026-10-06')
  expect(localDate(new Date('2026-10-05T18:30:00Z'), 'UTC')).toBe('2026-10-05')
})

test('todaySeconds counts only segments that started today in the local zone', () => {
  const now = new Date('2026-10-06T10:00:00Z') // 17:00 in Ho Chi Minh
  const segments = [
    seg({ startedAt: '2026-10-05T16:59:00Z', activeSeconds: 60 }), // 23:59 on the 5th, local
    seg({ startedAt: '2026-10-05T17:00:00Z', activeSeconds: 45 }), // 00:00 on the 6th, local
    seg({ startedAt: '2026-10-06T09:00:00Z', activeSeconds: 30 }),
  ]
  expect(todaySeconds(segments, now, 'Asia/Ho_Chi_Minh')).toBe(75)
})
```

`extension/src/domain/progress.test.ts`:

```ts
import { expect, test } from 'vitest'
import { readingProgress } from './progress'

test('progress is the share of the content root above the viewport bottom', () => {
  expect(readingProgress({ scrollY: 0, viewportHeight: 800, rootTop: 200, rootHeight: 3000 })).toBeCloseTo(0.2)
  expect(readingProgress({ scrollY: 1400, viewportHeight: 800, rootTop: 200, rootHeight: 3000 })).toBeCloseTo(0.6667, 3)
})

test('progress is clamped to [0, 1]', () => {
  expect(readingProgress({ scrollY: 0, viewportHeight: 100, rootTop: 500, rootHeight: 3000 })).toBe(0)
  expect(readingProgress({ scrollY: 9000, viewportHeight: 800, rootTop: 0, rootHeight: 3000 })).toBe(1)
})

test('an empty root reads as 0, never NaN', () => {
  expect(readingProgress({ scrollY: 0, viewportHeight: 800, rootTop: 0, rootHeight: 0 })).toBe(0)
})
```

- [ ] **Step 2: Run them to see them fail**

Run: `cd extension && pnpm test src/domain`
Expected: FAIL. Each file reports that it cannot resolve its module (`./config`, `./url` and so on).

- [ ] **Step 3: Implement the domain modules**

`extension/src/domain/config.ts`:

```ts
// RemoteConfig is GET /v1/config (spec §2.10) in camelCase. Every tracking threshold the
// extension uses comes from here; none is inlined in tracker or sync code (spec §7).

export interface TrackingConfig {
  idleSeconds: number
  segmentSeconds: number
  minCreateSeconds: number
  completeProgress: number
  completeTimeRatio: number
  wordsPerMinute: number
  syncIntervalMinutes: number
  syncThresholdSegments: number
}

export interface RemoteConfig {
  version: number
  tracking: TrackingConfig
  freediumHosts: string[]
  stripQueryParams: string[]
}

export class ConfigError extends Error {
  override name = 'ConfigError'
}

const TRACKING_FIELDS: readonly [keyof TrackingConfig, string][] = [
  ['idleSeconds', 'idle_seconds'],
  ['segmentSeconds', 'segment_seconds'],
  ['minCreateSeconds', 'min_create_seconds'],
  ['completeProgress', 'complete_progress'],
  ['completeTimeRatio', 'complete_time_ratio'],
  ['wordsPerMinute', 'words_per_minute'],
  ['syncIntervalMinutes', 'sync_interval_minutes'],
  ['syncThresholdSegments', 'sync_threshold_segments'],
]

function isRecord(v: unknown): v is Record<string, unknown> {
  return typeof v === 'object' && v !== null && !Array.isArray(v)
}

function stringList(v: unknown, field: string): string[] {
  if (!Array.isArray(v) || !v.every((x) => typeof x === 'string')) {
    throw new ConfigError(`config: ${field} must be a list of strings`)
  }
  return [...v]
}

export function parseRemoteConfig(raw: unknown): RemoteConfig {
  if (!isRecord(raw) || !isRecord(raw.tracking)) throw new ConfigError('config: missing tracking')
  const wire = raw.tracking
  const tracking = {} as TrackingConfig
  for (const [key, field] of TRACKING_FIELDS) {
    const v = wire[field]
    if (typeof v !== 'number' || !Number.isFinite(v) || v <= 0) {
      throw new ConfigError(`config: tracking.${field} must be a positive number`)
    }
    tracking[key] = v
  }
  return {
    version: typeof raw.version === 'number' ? raw.version : 0,
    tracking,
    freediumHosts: stringList(raw.freedium_hosts, 'freedium_hosts').map((h) => h.toLowerCase()),
    stripQueryParams: stringList(raw.strip_query_params, 'strip_query_params'),
  }
}
```

`extension/src/domain/url.ts`:

```ts
// URL normalization (spec T26): the canonical URL is the article's identity on every device.

// globMatch matches a query-param name against a pattern where * is any run of characters.
export function globMatch(pattern: string, name: string): boolean {
  const body = pattern.split('*').map((part) => part.replace(/[.+?^${}()|[\]\\]/g, '\\$&')).join('.*')
  return new RegExp(`^${body}$`, 'i').test(name)
}

export interface NormalizeOptions {
  stripQueryParams: readonly string[]
  // preferHttps upgrades http:// when the page itself is served over https.
  preferHttps?: boolean
}

// normalizeUrl lowercases the host, drops the fragment, credentials and matching query
// params, and removes a trailing slash (except the root). Non-http(s) input gives null.
export function normalizeUrl(raw: string, opts: NormalizeOptions): string | null {
  let u: URL
  try {
    u = new URL(raw)
  } catch {
    return null
  }
  if (u.protocol !== 'http:' && u.protocol !== 'https:') return null
  if (opts.preferHttps && u.protocol === 'http:') u.protocol = 'https:'
  u.hash = ''
  u.username = ''
  u.password = ''
  const kept = [...u.searchParams].filter(([name]) => !opts.stripQueryParams.some((p) => globMatch(p, name)))
  u.search = kept.length > 0 ? `?${new URLSearchParams(kept).toString()}` : ''
  if (u.pathname.length > 1 && u.pathname.endsWith('/')) u.pathname = u.pathname.replace(/\/+$/, '')
  return u.toString()
}
```

`extension/src/domain/medium.ts`:

```ts
// Medium identity (spec T26): every Medium URL and every Freedium mirror of it maps to
// https://medium.com/p/<post id>, so reading on any of them counts for one article.

const POST_ID = /-?([0-9a-f]{10,16})$/

export function isMediumHost(host: string): boolean {
  const h = host.toLowerCase()
  return h === 'medium.com' || h.endsWith('.medium.com')
}

export function isFreediumHost(host: string, freediumHosts: readonly string[]): boolean {
  const h = host.toLowerCase()
  return freediumHosts.some((f) => f.toLowerCase() === h)
}

// mediumPostId reads the post id from the last path segment (…-8f3c2a1b9d4e or /p/8f3c2a1b9d4e).
export function mediumPostId(path: string): string | null {
  const last = path.split('/').filter(Boolean).pop() ?? ''
  return POST_ID.exec(last)?.[1] ?? null
}

// mediumCanonical returns https://medium.com/p/<id> for a Medium page or a Freedium page
// wrapping one (/https://…), or null when the URL carries no post id.
export function mediumCanonical(raw: string, freediumHosts: readonly string[]): string | null {
  let u: URL
  try {
    u = new URL(raw)
  } catch {
    return null
  }
  let path: string
  if (isFreediumHost(u.hostname, freediumHosts)) {
    const inner = u.pathname.slice(1)
    if (!/^https?:/i.test(inner)) return null
    try {
      path = new URL(inner).pathname
    } catch {
      return null
    }
  } else if (isMediumHost(u.hostname)) {
    path = u.pathname
  } else {
    return null
  }
  const id = mediumPostId(path)
  return id ? `https://medium.com/p/${id}` : null
}
```

`extension/src/domain/domains.ts`:

```ts
// Which sites are tracked (spec T26, T31, C24): Medium/Freedium always, other sites only
// when the user enabled their domain, and never a denied domain.
import { isFreediumHost, isMediumHost } from './medium'

export type AdapterName = 'freedium-medium' | 'generic'

const HOSTNAME = /^(?=.{1,253}$)[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?(?:\.[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?)+$/

// normalizeDomain turns user input ("https://www.Example.com/x") into "example.com",
// or null when it is not a dotted hostname.
export function normalizeDomain(input: string): string | null {
  let s = input.trim().toLowerCase()
  if (!s) return null
  if (!/^[a-z][a-z0-9+.-]*:\/\//.test(s)) s = `https://${s}`
  let host: string
  try {
    const u = new URL(s)
    if (u.protocol !== 'https:' && u.protocol !== 'http:') return null
    host = u.hostname
  } catch {
    return null
  }
  host = host.replace(/^www\./, '').replace(/\.$/, '')
  return HOSTNAME.test(host) ? host : null
}

// originPatterns are the host permissions and content-script matches for an enabled domain.
export function originPatterns(domain: string): string[] {
  return [`https://${domain}/*`, `https://*.${domain}/*`]
}

export function hostInDomain(host: string, domain: string): boolean {
  const h = host.toLowerCase()
  return h === domain || h.endsWith(`.${domain}`)
}

export interface DomainLists {
  enabled: readonly string[]
  denied: readonly string[]
}

export function chooseAdapter(host: string, freediumHosts: readonly string[], lists: DomainLists): AdapterName | null {
  if (lists.denied.some((d) => hostInDomain(host, d))) return null
  if (isMediumHost(host) || isFreediumHost(host, freediumHosts)) return 'freedium-medium'
  if (lists.enabled.some((d) => hostInDomain(host, d))) return 'generic'
  return null
}
```

`extension/src/domain/reading.ts`:

```ts
// Local reading records (spec §3.4) and the rules that combine them.

export interface ArticleMeta {
  canonicalUrl: string
  source: string
  title: string
  author: string
  siteName: string
  wordCount: number
  publishedAt: string
  originalUrl: string
}

// Segment is one stretch of active reading (spec §2.8 segments[]); times are RFC 3339 UTC.
export interface Segment {
  id: string
  sessionId: string
  seq: number
  canonicalUrl: string
  startedAt: string
  endedAt: string
  activeSeconds: number
  maxProgress: number
}

export interface LocalArticle extends ArticleMeta {
  totalSeconds: number
  maxProgress: number
  lastReadAt: string
}

export interface StoredSegment extends Segment {
  synced: 0 | 1
  createdAt: string
}

function keep(next: string, prev: string | undefined): string {
  return next.trim() ? next : (prev ?? '')
}

// mergeArticle folds one segment into the local article. Metadata found earlier is kept
// when a later extraction comes back empty.
export function mergeArticle(prev: LocalArticle | undefined, meta: ArticleMeta, seg: Segment): LocalArticle {
  return {
    canonicalUrl: meta.canonicalUrl,
    source: keep(meta.source, prev?.source),
    title: keep(meta.title, prev?.title),
    author: keep(meta.author, prev?.author),
    siteName: keep(meta.siteName, prev?.siteName),
    wordCount: meta.wordCount > 0 ? meta.wordCount : (prev?.wordCount ?? 0),
    publishedAt: keep(meta.publishedAt, prev?.publishedAt),
    originalUrl: keep(meta.originalUrl, prev?.originalUrl),
    totalSeconds: (prev?.totalSeconds ?? 0) + seg.activeSeconds,
    maxProgress: Math.max(prev?.maxProgress ?? 0, seg.maxProgress),
    lastReadAt: !prev || seg.endedAt > prev.lastReadAt ? seg.endedAt : prev.lastReadAt,
  }
}

// roundMinutes matches the backend's rounding (half away from zero; inputs are ≥ 0).
export function roundMinutes(seconds: number): number {
  return Math.round(seconds / 60)
}

// localDate is the calendar date (YYYY-MM-DD) of an instant in a time zone.
export function localDate(at: Date, timeZone: string): string {
  return new Intl.DateTimeFormat('en-CA', { timeZone, year: 'numeric', month: '2-digit', day: '2-digit' }).format(at)
}

// todaySeconds sums segments that started on today's local date.
export function todaySeconds(segments: readonly Segment[], now: Date, timeZone: string): number {
  const today = localDate(now, timeZone)
  return segments
    .filter((s) => localDate(new Date(s.startedAt), timeZone) === today)
    .reduce((sum, s) => sum + s.activeSeconds, 0)
}
```

`extension/src/domain/progress.ts`:

```ts
// Reading progress (spec T27): clamp((scrollY + innerHeight − rootTop) / rootHeight, 0, 1).

export interface ScrollGeometry {
  scrollY: number
  viewportHeight: number
  // rootTop is the content root's top edge in document coordinates.
  rootTop: number
  rootHeight: number
}

export function readingProgress(g: ScrollGeometry): number {
  if (!(g.rootHeight > 0)) return 0
  const p = (g.scrollY + g.viewportHeight - g.rootTop) / g.rootHeight
  return Math.min(1, Math.max(0, p))
}
```

- [ ] **Step 4: Run the domain tests**

Run: `cd extension && pnpm test src/domain && pnpm test arch`
Expected: PASS. The arch test now also checks `src/domain/*`; it imports only `./medium`.

- [ ] **Step 5: Commit**

```bash
cd /Users/tuandoquoc/personal/reading-chrome-extension
git add extension/src/domain extension/test/fixtures.ts
git commit -m "feat(extension): domain rules for config, URL normalization, Medium canonicals, domains and reading"
```

---
### Task 3: Tracker state machine

**Files:**
- Create: `extension/src/domain/tracker.ts`
- Test: `extension/src/domain/tracker.test.ts`

**Interfaces:**
- Consumes: nothing.
- Produces:
  - `TrackerState = 'active' | 'idle' | 'hidden' | 'stopped'`.
  - `TrackerOptions { idleSeconds; segmentSeconds; now: number; visible: boolean; focused: boolean; newId: () => string }`, where `now` is epoch milliseconds.
  - `SegmentDraft { id; sessionId; seq; startedAt: number; endedAt: number; activeSeconds; maxProgress }`, with times in epoch ms.
  - `class Tracker`. Every method that can end a segment returns the `SegmentDraft[]` it flushed:
    - `state` and `sessionId`;
    - `input(now)`, `setVisible(visible, now)`, `setFocused(focused, now)` and `progress(p)`;
    - `tick(now)`, `pagehide(now)` and `newSession(now)`.

The machine follows onboarding §5.4 and spec T27.
- **Ticks:** the driver calls `tick` once per second. Only `active` accrues, 1 second per tick.
- **Idle:** `active` becomes `idle` once a tick sees more than `idleSeconds` since the last input. Until then the seconds count, so 2 minutes without input accrue exactly `idleSeconds`.
- **Hidden:** a hidden tab or a blurred window means `hidden`. Becoming visible and focused again returns to `active`, and that counts as input.
- **Flushes:** a segment is flushed when it reaches `segmentSeconds`, when the tracker leaves `active`, and on `pagehide`. It is never flushed with 0 seconds (C23).

- [ ] **Step 1: Write the failing test**

`extension/src/domain/tracker.test.ts`:

```ts
import { describe, expect, test } from 'vitest'
import { Tracker, type SegmentDraft, type TrackerOptions } from './tracker'

const S = 1000

function ids(): () => string {
  let n = 0
  return () => `id-${++n}`
}

function tracker(over: Partial<TrackerOptions> = {}): Tracker {
  return new Tracker({ idleSeconds: 60, segmentSeconds: 60, now: 0, visible: true, focused: true, newId: ids(), ...over })
}

// run ticks once per second over (from, to], optionally with input every `inputEvery` seconds.
function run(t: Tracker, from: number, to: number, inputEvery = 0): SegmentDraft[] {
  const out: SegmentDraft[] = []
  for (let now = from + S; now <= to; now += S) {
    if (inputEvery && (now / S) % inputEvery === 0) out.push(...t.input(now))
    out.push(...t.tick(now))
  }
  return out
}

const seconds = (segs: SegmentDraft[]) => segs.map((s) => s.activeSeconds)

describe('T27 acceptance', () => {
  test('a hidden tab accrues 0 s', () => {
    const t = tracker({ visible: false })
    expect(t.state).toBe('hidden')
    expect(run(t, 0, 120 * S, 5)).toEqual([])
    expect(t.pagehide(120 * S)).toEqual([])
  })

  test('2 min with no input accrues exactly idle_seconds', () => {
    const t = tracker()
    const segs = [...run(t, 0, 120 * S), ...t.pagehide(120 * S)]
    expect(segs.reduce((n, s) => n + s.activeSeconds, 0)).toBe(60)
    expect(t.state).toBe('stopped')
  })

  test('150 s active with segment_seconds=60 → 60, 60, 30 on pagehide', () => {
    const t = tracker()
    const segs = [...run(t, 0, 150 * S, 10), ...t.pagehide(150 * S)]
    expect(seconds(segs)).toEqual([60, 60, 30])
    expect(segs.map((s) => s.seq)).toEqual([0, 1, 2])
  })
})

describe('segments', () => {
  test('carry session, ids, contiguous times and the max progress', () => {
    const t = tracker()
    t.progress(0.3)
    t.progress(0.1)
    const [seg] = [...run(t, 0, 5 * S), ...t.pagehide(5 * S)]
    expect(seg).toEqual({
      id: 'id-2', sessionId: 'id-1', seq: 0, startedAt: 0, endedAt: 5 * S, activeSeconds: 5, maxProgress: 0.3,
    })
  })

  test('active_seconds never exceeds (ended − started) + 5 (backend validation)', () => {
    const t = tracker()
    for (const s of [...run(t, 0, 150 * S, 10), ...t.pagehide(150 * S)]) {
      expect(s.activeSeconds).toBeLessThanOrEqual((s.endedAt - s.startedAt) / S + 5)
      expect(s.activeSeconds).toBeGreaterThan(0)
    }
  })

  test('leaving active flushes what was read so far', () => {
    const t = tracker()
    run(t, 0, 10 * S)
    expect(seconds(t.setFocused(false, 10 * S))).toEqual([10])
    expect(t.state).toBe('hidden')
  })

  test('a transition with nothing accrued sends no segment (C23)', () => {
    const t = tracker()
    expect(t.setVisible(false, 0)).toEqual([])
    expect(t.pagehide(0)).toEqual([])
  })

  test('idle ends the segment; input resumes a new one', () => {
    const t = tracker()
    const first = run(t, 0, 70 * S) // 60 s then idle
    expect(seconds(first)).toEqual([60])
    expect(t.state).toBe('idle')
    t.input(70 * S)
    expect(t.state).toBe('active')
    const second = [...run(t, 70 * S, 80 * S), ...t.pagehide(80 * S)]
    expect(second).toHaveLength(1)
    expect(second[0]).toMatchObject({ activeSeconds: 10, seq: 1, startedAt: 70 * S, endedAt: 80 * S })
  })

  test('idle stays idle without input', () => {
    const t = tracker({ idleSeconds: 5 })
    run(t, 0, 10 * S)
    expect(t.state).toBe('idle')
    expect(run(t, 10 * S, 30 * S)).toEqual([])
  })

  test('hidden → visible and focused is active again and counts as input', () => {
    const t = tracker()
    t.setVisible(false, 0)
    run(t, 0, 100 * S)
    t.setVisible(true, 100 * S)
    expect(t.state).toBe('active')
    expect(seconds([...run(t, 100 * S, 130 * S), ...t.pagehide(130 * S)])).toEqual([30])
  })

  test('visible but blurred stays hidden', () => {
    const t = tracker({ focused: false })
    t.setVisible(true, 0)
    expect(t.state).toBe('hidden')
    t.setFocused(true, S)
    expect(t.state).toBe('active')
  })
})

describe('sessions', () => {
  test('newSession flushes under the old session, then restarts seq and progress', () => {
    const t = tracker()
    t.progress(0.8)
    run(t, 0, 10 * S)
    const old = t.sessionId
    const flushed = t.newSession(10 * S)
    expect(flushed).toHaveLength(1)
    expect(flushed[0]).toMatchObject({ sessionId: old, seq: 0, maxProgress: 0.8 })
    expect(t.sessionId).not.toBe(old)
    const next = [...run(t, 10 * S, 15 * S), ...t.pagehide(15 * S)]
    expect(next[0]).toMatchObject({ sessionId: t.sessionId, seq: 0, maxProgress: 0 })
  })
})

test('stopped ignores every event', () => {
  const t = tracker()
  t.pagehide(0)
  expect(t.input(S)).toEqual([])
  expect(t.setVisible(true, S)).toEqual([])
  expect(run(t, 0, 10 * S, 1)).toEqual([])
  expect(t.pagehide(10 * S)).toEqual([])
  expect(t.state).toBe('stopped')
})
```

- [ ] **Step 2: Run it to see it fail**

Run: `cd extension && pnpm test tracker`
Expected: FAIL, `Cannot find module './tracker'` (or "Failed to resolve import").

- [ ] **Step 3: Implement the tracker**

`extension/src/domain/tracker.ts`:

```ts
// The per-tab reading tracker (onboarding §5.4, spec T27), free of DOM and timers: the
// driver feeds it events and a 1 s tick, and sends whatever segments it flushes.

export type TrackerState = 'active' | 'idle' | 'hidden' | 'stopped'

export interface TrackerOptions {
  idleSeconds: number
  segmentSeconds: number
  // now is epoch milliseconds at page load.
  now: number
  visible: boolean
  focused: boolean
  newId: () => string
}

// SegmentDraft is a flushed segment before it gets its URL; times are epoch ms.
export interface SegmentDraft {
  id: string
  sessionId: string
  seq: number
  startedAt: number
  endedAt: number
  activeSeconds: number
  maxProgress: number
}

const SECOND = 1000

export class Tracker {
  private current: TrackerState
  private visible: boolean
  private focused: boolean
  private lastInput: number
  private session: string
  private seq = 0
  private maxProgress = 0
  private segStart: number | null = null
  private segEnd = 0
  private segSeconds = 0

  constructor(private readonly opts: TrackerOptions) {
    this.visible = opts.visible
    this.focused = opts.focused
    this.lastInput = opts.now
    this.session = opts.newId()
    this.current = this.visible && this.focused ? 'active' : 'hidden'
  }

  get state(): TrackerState {
    return this.current
  }

  get sessionId(): string {
    return this.session
  }

  input(now: number): SegmentDraft[] {
    if (this.current === 'stopped') return []
    this.lastInput = now
    if (this.current === 'idle') this.current = 'active'
    return []
  }

  setVisible(visible: boolean, now: number): SegmentDraft[] {
    this.visible = visible
    return this.reconcile(now)
  }

  setFocused(focused: boolean, now: number): SegmentDraft[] {
    this.focused = focused
    return this.reconcile(now)
  }

  progress(p: number): void {
    if (p > this.maxProgress) this.maxProgress = p
  }

  tick(now: number): SegmentDraft[] {
    if (this.current !== 'active') return []
    if (now - this.lastInput > this.opts.idleSeconds * SECOND) {
      const out = this.flush()
      this.current = 'idle'
      return out
    }
    if (this.segStart === null) this.segStart = now - SECOND
    this.segSeconds += 1
    this.segEnd = now
    return this.segSeconds >= this.opts.segmentSeconds ? this.flush() : []
  }

  pagehide(_now: number): SegmentDraft[] {
    if (this.current === 'stopped') return []
    const out = this.flush()
    this.current = 'stopped'
    return out
  }

  // newSession starts a new reading session (SPA navigation): the open segment is flushed
  // under the old session, then seq and progress restart.
  newSession(now: number): SegmentDraft[] {
    if (this.current === 'stopped') return []
    const out = this.flush()
    this.session = this.opts.newId()
    this.seq = 0
    this.maxProgress = 0
    this.lastInput = now
    if (this.current === 'idle') this.current = 'active'
    return out
  }

  private reconcile(now: number): SegmentDraft[] {
    if (this.current === 'stopped') return []
    const shown = this.visible && this.focused
    if (!shown && this.current !== 'hidden') {
      const out = this.flush()
      this.current = 'hidden'
      return out
    }
    if (shown && this.current === 'hidden') {
      this.current = 'active'
      this.lastInput = now
    }
    return []
  }

  private flush(): SegmentDraft[] {
    const start = this.segStart
    const secs = this.segSeconds
    this.segStart = null
    this.segSeconds = 0
    if (start === null || secs < 1) return []
    return [{
      id: this.opts.newId(),
      sessionId: this.session,
      seq: this.seq++,
      startedAt: start,
      endedAt: this.segEnd,
      activeSeconds: secs,
      maxProgress: this.maxProgress,
    }]
  }
}
```

- [ ] **Step 4: Run the tracker tests**

Run: `cd extension && pnpm test tracker && pnpm test arch`
Expected: PASS. Every tracker test and the arch test pass.

- [ ] **Step 5: Commit**

```bash
cd /Users/tuandoquoc/personal/reading-chrome-extension
git add extension/src/domain/tracker.ts extension/src/domain/tracker.test.ts
git commit -m "feat(extension): pure tracker state machine with idle, hidden and segment flushing"
```

---
### Task 4: Application ports, RecordService, and the IndexedDB store

**Files:**
- Create: `extension/src/application/types.ts`, `extension/src/application/errors.ts`, `extension/src/application/ports.ts`
- Create: `extension/src/application/record.ts`, `record.test.ts`
- Create: `extension/test/fakes.ts`, `extension/test/store-contract.ts`, `extension/test/memory-store.test.ts`
- Create: `extension/src/adapters/storage/dexie-store.ts`, `dexie-store.test.ts`

**Interfaces:**
- Consumes (from Task 2):
  - `ArticleMeta`, `Segment`, `LocalArticle`, `StoredSegment` and `mergeArticle` from `src/domain/reading.ts`;
  - `RemoteConfig` from `src/domain/config.ts`.
- Produces:
  - **`application/types.ts`:**
    - `InstallationStatus`;
    - `Installation { id; workspaceName; status; setupError; libraryUrl: string | null }`;
    - `User { id; name }`;
    - `Me { user; device: { id; name }; installation: Installation | null }`;
    - `TokenGrant { deviceToken; deviceId; user; installation: Installation | null }`;
    - `PushBody { clientTime; tz; articles: ArticleMeta[]; segments: Segment[] }`;
    - `PushResult { accepted; duplicate; retry: string[]; rejected: { id; reason }[] }`;
    - `ArticleStatus { totalSeconds; maxProgress; sessions; completed; lastReadAt; notionUrl: string | null }`;
    - `NotionPage { id; title; url }`;
    - `CachedConfig { fetchedAt: string; value: RemoteConfig }`.
  - **`application/errors.ts`:**
    - `class ApiError { status; code; message }`;
    - `class NetworkError`;
    - `isApiError(e, status?, code?)`.
  - **`application/ports.ts`:**
    - `KvSchema` and `KvKey`;
    - `LocalStore`, `Api`, `Clock`, `AuthFlow`, `Pkce`, `Permissions`, `ContentScripts`, `Scheduler` and `Log` (signatures below).
  - **`application/record.ts`:** `RecordService(deps: RecordDeps)` with `record(meta, segment): Promise<boolean>`.
  - **`adapters/storage/dexie-store.ts`:**
    - `class ReadingDb` (Dexie, schema §3.4);
    - `class DexieStore implements LocalStore`, built as `new DexieStore(new ReadingDb())`.
  - **`test/fakes.ts`:** `MemoryStore`, `FixedClock` and `MemoryLog`.
  - **`test/store-contract.ts`:** `storeContract(name, make)`, which every `LocalStore` must pass.

- [ ] **Step 1: Write the application types, errors and ports**

These files hold only types and two small error classes. They are exercised by the tests in the next steps.

`extension/src/application/types.ts`:

```ts
// What the use cases exchange with the backend (spec §2), in camelCase.
import type { RemoteConfig } from '../domain/config'
import type { ArticleMeta, Segment } from '../domain/reading'

export type InstallationStatus = 'active' | 'needs_setup' | 'revoked'

export interface Installation {
  id: string
  workspaceName: string
  status: InstallationStatus | (string & {})
  setupError: string
  libraryUrl: string | null
}

export interface User {
  id: string
  name: string
}

export interface Me {
  user: User
  device: { id: string; name: string }
  installation: Installation | null
}

export interface TokenGrant {
  deviceToken: string
  deviceId: string
  user: User
  installation: Installation | null
}

export interface PushBody {
  clientTime: string
  tz: string
  articles: ArticleMeta[]
  segments: Segment[]
}

export interface PushResult {
  accepted: string[]
  duplicate: string[]
  retry: string[]
  rejected: { id: string; reason: string }[]
}

export interface ArticleStatus {
  totalSeconds: number
  maxProgress: number
  sessions: number
  completed: boolean
  lastReadAt: string
  notionUrl: string | null
}

export interface NotionPage {
  id: string
  title: string
  url: string
}

export interface CachedConfig {
  fetchedAt: string
  value: RemoteConfig
}
```

`extension/src/application/errors.ts`:

```ts
// ApiError is a non-2xx answer with the §2.0 error body; NetworkError is no answer at all.

export class ApiError extends Error {
  override name = 'ApiError'
  readonly status: number
  readonly code: string

  constructor(status: number, code: string, message: string) {
    super(message)
    this.status = status
    this.code = code
  }
}

export class NetworkError extends Error {
  override name = 'NetworkError'
}

export function isApiError(e: unknown, status?: number, code?: string): e is ApiError {
  return e instanceof ApiError && (status === undefined || e.status === status) && (code === undefined || e.code === code)
}
```

`extension/src/application/ports.ts`:

```ts
// Ports: what the use cases need from the outside world. Adapters implement them.
import type { RemoteConfig } from '../domain/config'
import type { LocalArticle, StoredSegment } from '../domain/reading'
import type {
  ArticleStatus, CachedConfig, Installation, Me, NotionPage, PushBody, PushResult, TokenGrant, User,
} from './types'

// KvSchema is the kv table of spec §3.4.
export interface KvSchema {
  device_token: string
  device_id: string
  user: User
  installation: Installation | null
  config: CachedConfig
  enabled_domains: string[]
  denied_domains: string[]
  sync_backoff_until: string
  sync_failures: number
}

export type KvKey = keyof KvSchema

export interface LocalStore {
  // applyRead stores a new segment and folds it into its article in one transaction.
  // It returns false, changing nothing, when the segment id is already stored.
  applyRead(segment: StoredSegment, merge: (prev: LocalArticle | undefined) => LocalArticle): Promise<boolean>
  // unsyncedSegments returns up to limit unsynced segments, oldest started_at first.
  unsyncedSegments(limit: number): Promise<StoredSegment[]>
  countUnsynced(): Promise<number>
  markSynced(ids: readonly string[]): Promise<void>
  deleteSegments(ids: readonly string[]): Promise<void>
  articles(urls: readonly string[]): Promise<LocalArticle[]>
  // recentArticles returns up to limit articles, latest last_read_at first.
  recentArticles(limit: number): Promise<LocalArticle[]>
  // segmentsSince returns segments with started_at >= iso.
  segmentsSince(iso: string): Promise<StoredSegment[]>
  // pruneSynced deletes synced segments that started before beforeIso and returns how many.
  pruneSynced(beforeIso: string): Promise<number>
  // clearReading deletes every article and segment; kv is kept.
  clearReading(): Promise<void>
  getKv<K extends KvKey>(key: K): Promise<KvSchema[K] | undefined>
  setKv<K extends KvKey>(key: K, value: KvSchema[K]): Promise<void>
  deleteKv(...keys: KvKey[]): Promise<void>
}

// Api is the backend (spec §2). Failures are ApiError (non-2xx) or NetworkError.
export interface Api {
  oauthStartUrl(p: { extRedirect: string; codeChallenge: string }): string
  config(): Promise<RemoteConfig>
  exchangeCode(p: { code: string; codeVerifier: string; deviceName: string }): Promise<TokenGrant>
  me(token: string): Promise<Me>
  logout(token: string): Promise<void>
  deleteAccount(token: string): Promise<void>
  push(token: string, body: PushBody): Promise<PushResult>
  articlesStatus(token: string, urls: readonly string[]): Promise<Record<string, ArticleStatus>>
  setupPages(token: string, query: string): Promise<NotionPage[]>
  setup(token: string, parentPageId: string): Promise<Installation>
}

export interface Clock {
  now(): Date
  // timeZone is the IANA zone the user reads in (sent as "tz" on push).
  timeZone(): string
}

// AuthFlow is chrome.identity's web auth flow.
export interface AuthFlow {
  redirectUrl(): string
  // launch resolves with the final redirect URL; it rejects when the user closes the window.
  launch(url: string): Promise<string>
}

export interface Pkce {
  create(): Promise<{ verifier: string; challenge: string }>
}

export interface Permissions {
  request(origins: string[]): Promise<boolean>
  remove(origins: string[]): Promise<boolean>
}

export interface ContentScripts {
  register(id: string, matches: string[]): Promise<void>
  unregister(ids: string[]): Promise<void>
  registeredIds(): Promise<string[]>
}

export interface Scheduler {
  // every (re)creates a repeating alarm.
  every(name: string, periodMinutes: number): Promise<void>
}

export interface Log {
  info(message: string, ...details: unknown[]): void
  warn(message: string, ...details: unknown[]): void
}
```

- [ ] **Step 2: Write the test fakes and the store contract**

`extension/test/fakes.ts`:

```ts
// Test doubles for the application ports. MemoryStore passes the same contract as DexieStore.
import type { LocalArticle, StoredSegment } from '../src/domain/reading'
import type { Clock, KvKey, KvSchema, LocalStore, Log } from '../src/application/ports'

export class MemoryStore implements LocalStore {
  readonly articleRows = new Map<string, LocalArticle>()
  readonly segmentRows = new Map<string, StoredSegment>()
  readonly kv = new Map<string, unknown>()

  async applyRead(segment: StoredSegment, merge: (prev: LocalArticle | undefined) => LocalArticle): Promise<boolean> {
    if (this.segmentRows.has(segment.id)) return false
    this.articleRows.set(segment.canonicalUrl, merge(this.articleRows.get(segment.canonicalUrl)))
    this.segmentRows.set(segment.id, { ...segment })
    return true
  }

  async unsyncedSegments(limit: number): Promise<StoredSegment[]> {
    return [...this.segmentRows.values()]
      .filter((s) => s.synced === 0)
      .sort((a, b) => (a.startedAt < b.startedAt ? -1 : a.startedAt > b.startedAt ? 1 : 0))
      .slice(0, limit)
  }

  async countUnsynced(): Promise<number> {
    return [...this.segmentRows.values()].filter((s) => s.synced === 0).length
  }

  async markSynced(ids: readonly string[]): Promise<void> {
    for (const id of ids) {
      const s = this.segmentRows.get(id)
      if (s) s.synced = 1
    }
  }

  async deleteSegments(ids: readonly string[]): Promise<void> {
    for (const id of ids) this.segmentRows.delete(id)
  }

  async articles(urls: readonly string[]): Promise<LocalArticle[]> {
    return urls.flatMap((u) => {
      const a = this.articleRows.get(u)
      return a ? [a] : []
    })
  }

  async recentArticles(limit: number): Promise<LocalArticle[]> {
    return [...this.articleRows.values()].sort((a, b) => (a.lastReadAt < b.lastReadAt ? 1 : -1)).slice(0, limit)
  }

  async segmentsSince(iso: string): Promise<StoredSegment[]> {
    return [...this.segmentRows.values()].filter((s) => s.startedAt >= iso)
  }

  async pruneSynced(beforeIso: string): Promise<number> {
    let n = 0
    for (const [id, s] of this.segmentRows) {
      if (s.synced === 1 && s.startedAt < beforeIso) {
        this.segmentRows.delete(id)
        n++
      }
    }
    return n
  }

  async clearReading(): Promise<void> {
    this.articleRows.clear()
    this.segmentRows.clear()
  }

  async getKv<K extends KvKey>(key: K): Promise<KvSchema[K] | undefined> {
    return this.kv.get(key) as KvSchema[K] | undefined
  }

  async setKv<K extends KvKey>(key: K, value: KvSchema[K]): Promise<void> {
    this.kv.set(key, value)
  }

  async deleteKv(...keys: KvKey[]): Promise<void> {
    for (const k of keys) this.kv.delete(k)
  }
}

export class FixedClock implements Clock {
  constructor(public current: Date = new Date('2026-10-06T03:00:00.000Z'), private readonly zone = 'Asia/Ho_Chi_Minh') {}
  now(): Date {
    return new Date(this.current)
  }
  timeZone(): string {
    return this.zone
  }
  advance(ms: number): void {
    this.current = new Date(this.current.getTime() + ms)
  }
}

export class MemoryLog implements Log {
  readonly lines: string[] = []
  info(message: string): void {
    this.lines.push(`info ${message}`)
  }
  warn(message: string): void {
    this.lines.push(`warn ${message}`)
  }
}
```

`extension/test/store-contract.ts`:

```ts
// storeContract is the behaviour every LocalStore must have (spec §3.4).
import { beforeEach, describe, expect, test } from 'vitest'
import { mergeArticle, type ArticleMeta, type Segment, type StoredSegment } from '../src/domain/reading'
import type { LocalStore } from '../src/application/ports'

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

export async function record(store: LocalStore, seg: StoredSegment, meta: ArticleMeta = META): Promise<boolean> {
  const plain: Segment = { ...seg }
  return store.applyRead(seg, (prev) => mergeArticle(prev, { ...meta, canonicalUrl: seg.canonicalUrl }, plain))
}

export function storeContract(name: string, make: () => Promise<LocalStore>): void {
  describe(`${name} (LocalStore contract)`, () => {
    let store: LocalStore
    beforeEach(async () => {
      store = await make()
    })

    test('applyRead stores an unsynced segment and creates its article', async () => {
      expect(await record(store, segment('a'))).toBe(true)
      expect(await store.countUnsynced()).toBe(1)
      const [art] = await store.articles([META.canonicalUrl])
      expect(art).toMatchObject({ title: 'Designing Idempotent APIs', totalSeconds: 60, maxProgress: 0.5 })
    })

    test('recording the same segment twice counts it once', async () => {
      await record(store, segment('a'))
      expect(await record(store, segment('a'))).toBe(false)
      const [art] = await store.articles([META.canonicalUrl])
      expect(art?.totalSeconds).toBe(60)
      expect(await store.countUnsynced()).toBe(1)
    })

    test('unsyncedSegments is oldest first and limited', async () => {
      await record(store, segment('late', { startedAt: '2026-10-06T05:00:00.000Z' }))
      await record(store, segment('early', { startedAt: '2026-10-06T01:00:00.000Z' }))
      await record(store, segment('mid', { startedAt: '2026-10-06T03:00:00.000Z' }))
      expect((await store.unsyncedSegments(2)).map((s) => s.id)).toEqual(['early', 'mid'])
    })

    test('markSynced and deleteSegments', async () => {
      await record(store, segment('a'))
      await record(store, segment('b'))
      await record(store, segment('c'))
      await store.markSynced(['a'])
      await store.deleteSegments(['b'])
      expect((await store.unsyncedSegments(10)).map((s) => s.id)).toEqual(['c'])
      expect(await store.countUnsynced()).toBe(1)
    })

    test('articles skips unknown URLs', async () => {
      await record(store, segment('a'))
      expect(await store.articles(['https://nope.example/', META.canonicalUrl])).toHaveLength(1)
    })

    test('recentArticles is latest first and limited', async () => {
      for (const [i, hour] of ['01', '03', '02'].entries()) {
        await record(store, segment(`s${i}`, {
          canonicalUrl: `https://example.com/${hour}`, endedAt: `2026-10-06T${hour}:00:00.000Z`,
        }))
      }
      expect((await store.recentArticles(2)).map((a) => a.canonicalUrl)).toEqual(['https://example.com/03', 'https://example.com/02'])
    })

    test('segmentsSince filters by started_at', async () => {
      await record(store, segment('old', { startedAt: '2026-10-05T00:00:00.000Z' }))
      await record(store, segment('new', { startedAt: '2026-10-06T00:00:00.000Z' }))
      expect((await store.segmentsSince('2026-10-06T00:00:00.000Z')).map((s) => s.id)).toEqual(['new'])
    })

    test('prune keeps unsynced segments', async () => {
      await record(store, segment('old-synced', { startedAt: '2026-08-01T00:00:00.000Z' }))
      await record(store, segment('old-unsynced', { startedAt: '2026-08-02T00:00:00.000Z' }))
      await record(store, segment('new-synced', { startedAt: '2026-10-01T00:00:00.000Z' }))
      await store.markSynced(['old-synced', 'new-synced'])
      expect(await store.pruneSynced('2026-09-06T00:00:00.000Z')).toBe(1)
      expect((await store.segmentsSince('2000-01-01T00:00:00.000Z')).map((s) => s.id).sort()).toEqual(['new-synced', 'old-unsynced'])
    })

    test('clearReading empties articles and segments but keeps kv', async () => {
      await record(store, segment('a'))
      await store.setKv('device_token', 'rt_x')
      await store.clearReading()
      expect(await store.countUnsynced()).toBe(0)
      expect(await store.recentArticles(5)).toEqual([])
      expect(await store.getKv('device_token')).toBe('rt_x')
    })

    test('kv round-trips structured values and deletes keys', async () => {
      expect(await store.getKv('installation')).toBeUndefined()
      const inst = { id: 'b1', workspaceName: 'W', status: 'active', setupError: '', libraryUrl: null }
      await store.setKv('installation', inst)
      await store.setKv('enabled_domains', ['example.com'])
      expect(await store.getKv('installation')).toEqual(inst)
      await store.deleteKv('installation', 'enabled_domains')
      expect(await store.getKv('installation')).toBeUndefined()
      expect(await store.getKv('enabled_domains')).toBeUndefined()
    })
  })
}
```

`extension/test/memory-store.test.ts`:

```ts
import { MemoryStore } from './fakes'
import { storeContract } from './store-contract'

storeContract('MemoryStore', async () => new MemoryStore())
```

`extension/src/adapters/storage/dexie-store.test.ts`:

```ts
import 'fake-indexeddb/auto'
import { expect, test } from 'vitest'
import { storeContract } from '../../../test/store-contract'
import { DexieStore, ReadingDb } from './dexie-store'

storeContract('DexieStore', async () => new DexieStore(new ReadingDb(`test-${crypto.randomUUID()}`)))

test('rows use the snake_case field names of spec §3.4', async () => {
  const db = new ReadingDb(`test-${crypto.randomUUID()}`)
  const store = new DexieStore(db)
  await store.applyRead(
    { id: 's1', sessionId: 'x', seq: 0, canonicalUrl: 'https://e.com/a', startedAt: '2026-10-06T03:00:00.000Z',
      endedAt: '2026-10-06T03:01:00.000Z', activeSeconds: 60, maxProgress: 0.5, synced: 0, createdAt: '2026-10-06T03:01:00.000Z' },
    () => ({ canonicalUrl: 'https://e.com/a', source: 'web', title: 'T', author: '', siteName: '', wordCount: 0,
      publishedAt: '', originalUrl: 'https://e.com/a', totalSeconds: 60, maxProgress: 0.5, lastReadAt: '2026-10-06T03:01:00.000Z' }),
  )
  expect(Object.keys((await db.segments.get('s1'))!).sort()).toEqual([
    'active_seconds', 'canonical_url', 'created_at', 'ended_at', 'id', 'max_progress', 'seq', 'session_id', 'started_at', 'synced',
  ])
  expect(Object.keys((await db.articles.get('https://e.com/a'))!).sort()).toEqual([
    'author', 'canonical_url', 'last_read_at', 'max_progress', 'original_url', 'published_at', 'site_name', 'source',
    'title', 'total_seconds', 'word_count',
  ])
})
```

- [ ] **Step 3: Run the store tests to see them fail**

Run: `cd extension && pnpm test store`
Expected:
- The `MemoryStore` contract passes, since the fake is written alongside the contract.
- `dexie-store.test.ts` FAILS because it cannot resolve `./dexie-store`.

- [ ] **Step 4: Implement the Dexie store**

`extension/src/adapters/storage/dexie-store.ts`:

```ts
// DexieStore is the LocalStore over IndexedDB (spec §3.4). Rows keep the spec's snake_case
// names; the mapping to domain types lives here only.
import Dexie, { type Table } from 'dexie'
import type { LocalArticle, StoredSegment } from '../../domain/reading'
import type { KvKey, KvSchema, LocalStore } from '../../application/ports'

interface ArticleRow {
  canonical_url: string
  source: string
  title: string
  author: string
  site_name: string
  word_count: number
  published_at: string
  original_url: string
  total_seconds: number
  max_progress: number
  last_read_at: string
}

interface SegmentRow {
  id: string
  session_id: string
  seq: number
  canonical_url: string
  started_at: string
  ended_at: string
  active_seconds: number
  max_progress: number
  synced: 0 | 1
  created_at: string
}

interface KvRow {
  key: string
  value: unknown
}

export class ReadingDb extends Dexie {
  declare articles: Table<ArticleRow, string>
  declare segments: Table<SegmentRow, string>
  declare kv: Table<KvRow, string>

  constructor(name = 'reading-tracker') {
    super(name)
    this.version(1).stores({
      articles: 'canonical_url, last_read_at',
      segments: 'id, synced, started_at, [synced+started_at]',
      kv: 'key',
    })
  }
}

function toArticle(r: ArticleRow): LocalArticle {
  return {
    canonicalUrl: r.canonical_url, source: r.source, title: r.title, author: r.author, siteName: r.site_name,
    wordCount: r.word_count, publishedAt: r.published_at, originalUrl: r.original_url,
    totalSeconds: r.total_seconds, maxProgress: r.max_progress, lastReadAt: r.last_read_at,
  }
}

function fromArticle(a: LocalArticle): ArticleRow {
  return {
    canonical_url: a.canonicalUrl, source: a.source, title: a.title, author: a.author, site_name: a.siteName,
    word_count: a.wordCount, published_at: a.publishedAt, original_url: a.originalUrl,
    total_seconds: a.totalSeconds, max_progress: a.maxProgress, last_read_at: a.lastReadAt,
  }
}

function toSegment(r: SegmentRow): StoredSegment {
  return {
    id: r.id, sessionId: r.session_id, seq: r.seq, canonicalUrl: r.canonical_url, startedAt: r.started_at,
    endedAt: r.ended_at, activeSeconds: r.active_seconds, maxProgress: r.max_progress, synced: r.synced, createdAt: r.created_at,
  }
}

function fromSegment(s: StoredSegment): SegmentRow {
  return {
    id: s.id, session_id: s.sessionId, seq: s.seq, canonical_url: s.canonicalUrl, started_at: s.startedAt,
    ended_at: s.endedAt, active_seconds: s.activeSeconds, max_progress: s.maxProgress, synced: s.synced, created_at: s.createdAt,
  }
}

export class DexieStore implements LocalStore {
  constructor(private readonly db: ReadingDb) {}

  applyRead(segment: StoredSegment, merge: (prev: LocalArticle | undefined) => LocalArticle): Promise<boolean> {
    const { db } = this
    return db.transaction('rw', db.articles, db.segments, async () => {
      if (await db.segments.get(segment.id)) return false
      const prev = await db.articles.get(segment.canonicalUrl)
      await db.articles.put(fromArticle(merge(prev ? toArticle(prev) : undefined)))
      await db.segments.put(fromSegment(segment))
      return true
    })
  }

  async unsyncedSegments(limit: number): Promise<StoredSegment[]> {
    const rows = await this.db.segments
      .where('[synced+started_at]')
      .between([0, Dexie.minKey], [0, Dexie.maxKey])
      .limit(limit)
      .toArray()
    return rows.map(toSegment)
  }

  countUnsynced(): Promise<number> {
    return this.db.segments.where('synced').equals(0).count()
  }

  async markSynced(ids: readonly string[]): Promise<void> {
    if (ids.length === 0) return
    await this.db.segments.where('id').anyOf([...ids]).modify({ synced: 1 })
  }

  async deleteSegments(ids: readonly string[]): Promise<void> {
    await this.db.segments.bulkDelete([...ids])
  }

  async articles(urls: readonly string[]): Promise<LocalArticle[]> {
    const rows = await this.db.articles.bulkGet([...urls])
    return rows.flatMap((r) => (r ? [toArticle(r)] : []))
  }

  async recentArticles(limit: number): Promise<LocalArticle[]> {
    const rows = await this.db.articles.orderBy('last_read_at').reverse().limit(limit).toArray()
    return rows.map(toArticle)
  }

  async segmentsSince(iso: string): Promise<StoredSegment[]> {
    const rows = await this.db.segments.where('started_at').aboveOrEqual(iso).toArray()
    return rows.map(toSegment)
  }

  pruneSynced(beforeIso: string): Promise<number> {
    return this.db.segments.where('[synced+started_at]').between([1, Dexie.minKey], [1, beforeIso]).delete()
  }

  async clearReading(): Promise<void> {
    const { db } = this
    await db.transaction('rw', db.articles, db.segments, async () => {
      await db.articles.clear()
      await db.segments.clear()
    })
  }

  async getKv<K extends KvKey>(key: K): Promise<KvSchema[K] | undefined> {
    return (await this.db.kv.get(key))?.value as KvSchema[K] | undefined
  }

  async setKv<K extends KvKey>(key: K, value: KvSchema[K]): Promise<void> {
    await this.db.kv.put({ key, value })
  }

  async deleteKv(...keys: KvKey[]): Promise<void> {
    await this.db.kv.bulkDelete(keys)
  }
}
```

- [ ] **Step 5: Run the store tests**

Run: `cd extension && pnpm test store`
Expected: PASS. Both `MemoryStore` and `DexieStore` pass the 10 contract tests, and the snake_case row test passes too.

- [ ] **Step 6: Write the failing RecordService test**

`extension/src/application/record.test.ts`:

```ts
import { beforeEach, describe, expect, test, vi, type Mock } from 'vitest'
import { FixedClock, MemoryLog, MemoryStore } from '../../test/fakes'
import { META, segment } from '../../test/store-contract'
import { parseRemoteConfig, type RemoteConfig } from '../domain/config'
import { wireConfig } from '../../test/fixtures'
import { RecordService } from './record'

describe('RecordService', () => {
  let store: MemoryStore
  let syncSoon: Mock<() => void>
  let config: RemoteConfig | null
  let svc: RecordService
  const log = new MemoryLog()

  beforeEach(() => {
    store = new MemoryStore()
    syncSoon = vi.fn<() => void>()
    config = parseRemoteConfig({ ...wireConfig, tracking: { ...wireConfig.tracking, sync_threshold_segments: 2 } })
    svc = new RecordService({ store, clock: new FixedClock(), config: async () => config, syncSoon, log })
  })

  const plain = (id: string) => {
    const { synced: _s, createdAt: _c, ...seg } = segment(id)
    return seg
  }

  test('stores the segment unsynced with created_at and merges the article', async () => {
    expect(await svc.record(META, plain('a'))).toBe(true)
    expect(store.segmentRows.get('a')).toMatchObject({ synced: 0, createdAt: '2026-10-06T03:00:00.000Z' })
    expect(store.articleRows.get(META.canonicalUrl)?.totalSeconds).toBe(60)
  })

  test('C23: a 0-second segment is never stored', async () => {
    expect(await svc.record(META, { ...plain('a'), activeSeconds: 0 })).toBe(false)
    expect(store.segmentRows.size).toBe(0)
  })

  test('a segment whose URL does not match its article is dropped', async () => {
    expect(await svc.record(META, { ...plain('a'), canonicalUrl: 'https://other.example/' })).toBe(false)
    expect(store.segmentRows.size).toBe(0)
  })

  test('asks for a sync once unsynced segments reach sync_threshold_segments', async () => {
    await svc.record(META, plain('a'))
    expect(syncSoon).not.toHaveBeenCalled()
    await svc.record(META, plain('b'))
    expect(syncSoon).toHaveBeenCalledTimes(1)
  })

  test('a duplicate delivery does not trigger a sync or count again', async () => {
    await svc.record(META, plain('a'))
    expect(await svc.record(META, plain('a'))).toBe(false)
    expect(syncSoon).not.toHaveBeenCalled()
  })

  test('without config it still records but never asks to sync', async () => {
    config = null
    await svc.record(META, plain('a'))
    await svc.record(META, plain('b'))
    expect(store.segmentRows.size).toBe(2)
    expect(syncSoon).not.toHaveBeenCalled()
  })
})
```

- [ ] **Step 7: Run it to see it fail**

Run: `cd extension && pnpm test record`
Expected: FAIL, because `./record` cannot be resolved.

- [ ] **Step 8: Implement RecordService**

`extension/src/application/record.ts`:

```ts
// RecordService stores one tracked segment and its article (spec T28: "on segment message
// upsert article + put segment (synced=0)") and asks for a sync past the threshold.
import type { RemoteConfig } from '../domain/config'
import { mergeArticle, type ArticleMeta, type Segment } from '../domain/reading'
import type { Clock, LocalStore, Log } from './ports'

export interface RecordDeps {
  store: LocalStore
  clock: Clock
  config: () => Promise<RemoteConfig | null>
  // syncSoon starts a sync without waiting for it.
  syncSoon: () => void
  log: Log
}

export class RecordService {
  constructor(private readonly deps: RecordDeps) {}

  async record(meta: ArticleMeta, segment: Segment): Promise<boolean> {
    const { store, clock, log } = this.deps
    if (!(segment.activeSeconds >= 1)) {
      log.warn('record: dropped a segment with no active seconds', segment.id)
      return false
    }
    if (segment.canonicalUrl !== meta.canonicalUrl) {
      log.warn('record: segment URL does not match its article', segment.id)
      return false
    }
    const stored = await store.applyRead(
      { ...segment, synced: 0, createdAt: clock.now().toISOString() },
      (prev) => mergeArticle(prev, meta, segment),
    )
    if (!stored) return false
    const config = await this.deps.config()
    if (config && (await store.countUnsynced()) >= config.tracking.syncThresholdSegments) this.deps.syncSoon()
    return true
  }
}
```

- [ ] **Step 9: Run the task's tests and the layer rule**

Run: `cd extension && pnpm test && pnpm typecheck`
Expected: PASS. `test/arch.test.ts` now covers `application/*` and `adapters/storage/*`, and `dexie` is imported only from `adapters/storage`.

- [ ] **Step 10: Commit**

```bash
cd /Users/tuandoquoc/personal/reading-chrome-extension
git add extension/src/application extension/src/adapters/storage extension/test
git commit -m "feat(extension): application ports, RecordService and the IndexedDB store"
```

---
### Task 5: HTTP API client and PKCE

**Files:**
- Create: `extension/src/adapters/http/wire.ts`, `extension/src/adapters/http/api-client.ts`, `api-client.test.ts`
- Create: `extension/src/adapters/crypto/pkce.ts`, `pkce.test.ts`

**Interfaces:**
- Consumes:
  - `Api`, `Pkce` (Task 4, `application/ports.ts`);
  - `ApiError`, `NetworkError` (Task 4, `application/errors.ts`);
  - the types in `application/types.ts`;
  - `parseRemoteConfig` (Task 2).
- Produces:
  - `class HttpApi implements Api`, built as `new HttpApi({ baseUrl, fetch?, timeoutMs? })`. `fetch` defaults to the global `fetch`; `timeoutMs` defaults to 20000.
  - `type Fetch = (url: string, init?: RequestInit) => Promise<Response>`.
  - `class WebCryptoPkce implements Pkce`, `challengeFor(verifier): Promise<string>`, `randomVerifier(length = 64): string`, `base64url(bytes): string`.

Behaviour:
- Every request path is `/v1/...` under `baseUrl`, with any trailing slashes on `baseUrl` dropped.
- An authenticated call sends `Authorization: Bearer <token>`.
- A non-2xx answer becomes `ApiError(status, error.code, error.message)` from the §2.0 body. If the body is not JSON, the code is `internal` for 5xx and `http_<status>` otherwise.
- A rejected or aborted fetch becomes `NetworkError`.
- `202` and `204` resolve to `undefined`.

- [ ] **Step 1: Write the failing tests**

`extension/src/adapters/http/api-client.test.ts`:

```ts
import { describe, expect, test } from 'vitest'
import { wireConfig } from '../../../test/fixtures'
import { ApiError, NetworkError } from '../../application/errors'
import { HttpApi, type Fetch } from './api-client'

interface Call {
  url: string
  method: string
  headers: Record<string, string>
  body: unknown
}

function stub(respond: (call: Call) => Response | Promise<Response>): { api: HttpApi; calls: Call[] } {
  const calls: Call[] = []
  const fetch: Fetch = async (url, init) => {
    const call: Call = {
      url,
      method: init?.method ?? 'GET',
      headers: Object.fromEntries(new Headers(init?.headers).entries()),
      body: init?.body === undefined ? undefined : JSON.parse(String(init.body)),
    }
    calls.push(call)
    return respond(call)
  }
  return { api: new HttpApi({ baseUrl: 'https://api.example.com/', fetch }), calls }
}

const json = (status: number, body: unknown) =>
  new Response(JSON.stringify(body), { status, headers: { 'content-type': 'application/json' } })

const wireInstallation = { id: 'b1', workspace_name: "Tuan's Notion", status: 'active', library_url: 'https://www.notion.so/2a1b' }
const installation = { id: 'b1', workspaceName: "Tuan's Notion", status: 'active', setupError: '', libraryUrl: 'https://www.notion.so/2a1b' }

describe('HttpApi requests', () => {
  test('oauthStartUrl carries ext_redirect and the S256 challenge (§2.1)', () => {
    const { api } = stub(() => json(200, {}))
    const u = new URL(api.oauthStartUrl({ extRedirect: 'https://abc.chromiumapp.org/', codeChallenge: 'E9Mel' }))
    expect(u.origin + u.pathname).toBe('https://api.example.com/v1/oauth/notion/start')
    expect(Object.fromEntries(u.searchParams)).toEqual({
      ext_redirect: 'https://abc.chromiumapp.org/', code_challenge: 'E9Mel', code_challenge_method: 'S256',
    })
  })

  test('config is unauthenticated and parsed (§2.10)', async () => {
    const { api, calls } = stub(() => json(200, wireConfig))
    const cfg = await api.config()
    expect(cfg.tracking.idleSeconds).toBe(60)
    expect(calls[0]).toMatchObject({ url: 'https://api.example.com/v1/config', method: 'GET' })
    expect(calls[0]?.headers.authorization).toBeUndefined()
  })

  test('exchangeCode posts snake_case and maps the grant (§2.3)', async () => {
    const { api, calls } = stub(() => json(200, {
      device_token: 'rt_abc', device_id: 'd1', user: { id: 'u1', name: 'Tuan Do' }, installation: wireInstallation,
    }))
    const grant = await api.exchangeCode({ code: 'c', codeVerifier: 'v', deviceName: 'Chrome on macOS' })
    expect(calls[0]).toMatchObject({
      url: 'https://api.example.com/v1/auth/token', method: 'POST',
      body: { code: 'c', code_verifier: 'v', device_name: 'Chrome on macOS' },
    })
    expect(calls[0]?.headers['content-type']).toBe('application/json')
    expect(grant).toEqual({ deviceToken: 'rt_abc', deviceId: 'd1', user: { id: 'u1', name: 'Tuan Do' }, installation })
  })

  test('me sends the bearer token and maps a needs_setup installation (§2.5)', async () => {
    const { api, calls } = stub(() => json(200, {
      user: { id: 'u1', name: 'T' }, device: { id: 'd1', name: 'Mac' },
      installation: { id: 'b1', workspace_name: 'W', status: 'needs_setup', setup_error: 'template_not_found', library_url: null },
    }))
    const me = await api.me('rt_x')
    expect(calls[0]?.headers.authorization).toBe('Bearer rt_x')
    expect(me.installation).toEqual({ id: 'b1', workspaceName: 'W', status: 'needs_setup', setupError: 'template_not_found', libraryUrl: null })
  })

  test('me maps a null installation', async () => {
    const { api } = stub(() => json(200, { user: { id: 'u1', name: 'T' }, device: { id: 'd1', name: 'Mac' }, installation: null }))
    expect((await api.me('rt_x')).installation).toBeNull()
  })

  test('push sends the exact §2.8 body and maps the result', async () => {
    const { api, calls } = stub(() => json(200, { accepted: ['s1'], duplicate: [], retry: ['s2'], rejected: [{ id: 's3', reason: 'started_at_out_of_range' }] }))
    const res = await api.push('rt_x', {
      clientTime: '2026-10-06T03:12:00.000Z', tz: 'Asia/Ho_Chi_Minh',
      articles: [{ canonicalUrl: 'https://medium.com/p/8f3c2a1b9d4e', source: 'medium', title: 'T', author: 'A', siteName: 'S',
        wordCount: 1820, publishedAt: '2026-09-30', originalUrl: 'https://freedium.cfd/x' }],
      segments: [{ id: 's1', sessionId: 'c3', seq: 0, canonicalUrl: 'https://medium.com/p/8f3c2a1b9d4e',
        startedAt: '2026-10-06T03:01:05.000Z', endedAt: '2026-10-06T03:02:10.000Z', activeSeconds: 58, maxProgress: 0.42 }],
    })
    expect(calls[0]).toMatchObject({ url: 'https://api.example.com/v1/sync/push', method: 'POST' })
    expect(calls[0]?.body).toEqual({
      client_time: '2026-10-06T03:12:00.000Z', tz: 'Asia/Ho_Chi_Minh',
      articles: [{ canonical_url: 'https://medium.com/p/8f3c2a1b9d4e', source: 'medium', title: 'T', author: 'A', site_name: 'S',
        word_count: 1820, published_at: '2026-09-30', original_url: 'https://freedium.cfd/x' }],
      segments: [{ id: 's1', session_id: 'c3', seq: 0, canonical_url: 'https://medium.com/p/8f3c2a1b9d4e',
        started_at: '2026-10-06T03:01:05.000Z', ended_at: '2026-10-06T03:02:10.000Z', active_seconds: 58, max_progress: 0.42 }],
    })
    expect(res).toEqual({ accepted: ['s1'], duplicate: [], retry: ['s2'], rejected: [{ id: 's3', reason: 'started_at_out_of_range' }] })
  })

  test('articlesStatus maps items keyed by URL (§2.9)', async () => {
    const { api, calls } = stub(() => json(200, { items: { 'https://e.com/a': {
      total_seconds: 734, max_progress: 0.97, sessions: 2, completed: true, last_read_at: '2026-10-06T03:20:00Z', notion_url: null,
    } } }))
    const items = await api.articlesStatus('rt_x', ['https://e.com/a'])
    expect(calls[0]?.body).toEqual({ canonical_urls: ['https://e.com/a'] })
    expect(items).toEqual({ 'https://e.com/a': {
      totalSeconds: 734, maxProgress: 0.97, sessions: 2, completed: true, lastReadAt: '2026-10-06T03:20:00Z', notionUrl: null,
    } })
  })

  test('setupPages encodes q; setup posts parent_page_id (§2.6, §2.7)', async () => {
    const { api, calls } = stub((c) => c.url.includes('/pages')
      ? json(200, { pages: [{ id: 'p1', title: 'Reading', url: 'https://www.notion.so/p1' }] })
      : json(200, wireInstallation))
    expect(await api.setupPages('rt_x', 'Read & notes')).toEqual([{ id: 'p1', title: 'Reading', url: 'https://www.notion.so/p1' }])
    expect(calls[0]?.url).toBe('https://api.example.com/v1/setup/pages?q=Read+%26+notes')
    expect(await api.setup('rt_x', 'p1')).toEqual(installation)
    expect(calls[1]).toMatchObject({ url: 'https://api.example.com/v1/setup', method: 'POST', body: { parent_page_id: 'p1' } })
  })

  test('logout (204) and deleteAccount (202) resolve to undefined (§2.17)', async () => {
    const { api, calls } = stub((c) => new Response(null, { status: c.method === 'DELETE' ? 202 : 204 }))
    await expect(api.logout('rt_x')).resolves.toBeUndefined()
    await expect(api.deleteAccount('rt_x')).resolves.toBeUndefined()
    expect(calls.map((c) => `${c.method} ${c.url}`)).toEqual([
      'POST https://api.example.com/v1/auth/logout', 'DELETE https://api.example.com/v1/me',
    ])
  })
})

describe('HttpApi errors', () => {
  test('the §2.0 error body becomes ApiError', async () => {
    const { api } = stub(() => json(409, { error: { code: 'no_installation', message: 'connect Notion first' } }))
    const err = await api.push('rt_x', { clientTime: '', tz: 'UTC', articles: [], segments: [] }).catch((e) => e)
    expect(err).toBeInstanceOf(ApiError)
    expect(err).toMatchObject({ status: 409, code: 'no_installation', message: 'connect Notion first' })
  })

  test('a non-JSON 502 becomes ApiError(502, internal)', async () => {
    const { api } = stub(() => new Response('<html>bad gateway</html>', { status: 502 }))
    await expect(api.me('rt_x')).rejects.toMatchObject({ status: 502, code: 'internal' })
  })

  test('a non-JSON 404 becomes ApiError(404, http_404)', async () => {
    const { api } = stub(() => new Response('nope', { status: 404 }))
    await expect(api.me('rt_x')).rejects.toMatchObject({ status: 404, code: 'http_404' })
  })

  test('a failed fetch becomes NetworkError', async () => {
    const api = new HttpApi({ baseUrl: 'https://api.example.com', fetch: async () => { throw new TypeError('Failed to fetch') } })
    await expect(api.config()).rejects.toBeInstanceOf(NetworkError)
  })

  test('a timeout becomes NetworkError', async () => {
    const api = new HttpApi({
      baseUrl: 'https://api.example.com', timeoutMs: 5,
      fetch: (_url, init) => new Promise((_, reject) => init?.signal?.addEventListener('abort', () => reject(init.signal?.reason))),
    })
    await expect(api.config()).rejects.toBeInstanceOf(NetworkError)
  })
})
```

`extension/src/adapters/crypto/pkce.test.ts`:

```ts
import { expect, test } from 'vitest'
import { base64url, challengeFor, randomVerifier, WebCryptoPkce } from './pkce'

test('RFC 7636 appendix B vector', async () => {
  expect(await challengeFor('dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk')).toBe('E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM')
})

test('T29: verifiers are 64 unreserved characters and differ', () => {
  const a = randomVerifier()
  expect(a).toMatch(/^[A-Za-z0-9\-._~]{64}$/)
  expect(randomVerifier()).not.toBe(a)
})

test('create pairs a verifier with its challenge', async () => {
  const { verifier, challenge } = await new WebCryptoPkce().create()
  expect(challenge).toBe(await challengeFor(verifier))
})

test('base64url has no padding or +/', () => {
  expect(base64url(new Uint8Array([251, 255, 191]))).toBe('-_-_')
})
```

- [ ] **Step 2: Run them to see them fail**

Run: `cd extension && pnpm test adapters/http adapters/crypto`
Expected: FAIL, because neither `./api-client` nor `./pkce` can be resolved.

- [ ] **Step 3: Implement the wire mapping and the client**

`extension/src/adapters/http/wire.ts`:

```ts
// The snake_case JSON of spec §2 and its mapping to application types.
import type { ArticleStatus, Installation, Me, PushBody, PushResult, TokenGrant } from '../../application/types'

export interface WireInstallation {
  id: string
  workspace_name: string
  status: string
  setup_error?: string
  library_url: string | null
}

export interface WireUser {
  id: string
  name: string
}

export interface WireToken {
  device_token: string
  device_id: string
  user: WireUser
  installation: WireInstallation | null
}

export interface WireMe {
  user: WireUser
  device: { id: string; name: string }
  installation: WireInstallation | null
}

export interface WirePushResult {
  accepted: string[] | null
  duplicate: string[] | null
  retry: string[] | null
  rejected: { id: string; reason: string }[] | null
}

export interface WireArticleStatus {
  total_seconds: number
  max_progress: number
  sessions: number
  completed: boolean
  last_read_at: string
  notion_url: string | null
}

export function installation(w: WireInstallation | null): Installation | null {
  if (!w) return null
  return {
    id: w.id, workspaceName: w.workspace_name, status: w.status, setupError: w.setup_error ?? '', libraryUrl: w.library_url ?? null,
  }
}

export function tokenGrant(w: WireToken): TokenGrant {
  return { deviceToken: w.device_token, deviceId: w.device_id, user: { id: w.user.id, name: w.user.name }, installation: installation(w.installation) }
}

export function me(w: WireMe): Me {
  return {
    user: { id: w.user.id, name: w.user.name }, device: { id: w.device.id, name: w.device.name }, installation: installation(w.installation),
  }
}

export function pushBody(b: PushBody): unknown {
  return {
    client_time: b.clientTime,
    tz: b.tz,
    articles: b.articles.map((a) => ({
      canonical_url: a.canonicalUrl, source: a.source, title: a.title, author: a.author, site_name: a.siteName,
      word_count: a.wordCount, published_at: a.publishedAt, original_url: a.originalUrl,
    })),
    segments: b.segments.map((s) => ({
      id: s.id, session_id: s.sessionId, seq: s.seq, canonical_url: s.canonicalUrl, started_at: s.startedAt,
      ended_at: s.endedAt, active_seconds: s.activeSeconds, max_progress: s.maxProgress,
    })),
  }
}

export function pushResult(w: WirePushResult): PushResult {
  return { accepted: w.accepted ?? [], duplicate: w.duplicate ?? [], retry: w.retry ?? [], rejected: w.rejected ?? [] }
}

export function articleStatus(w: WireArticleStatus): ArticleStatus {
  return {
    totalSeconds: w.total_seconds, maxProgress: w.max_progress, sessions: w.sessions, completed: w.completed,
    lastReadAt: w.last_read_at, notionUrl: w.notion_url ?? null,
  }
}
```

`extension/src/adapters/http/api-client.ts`:

```ts
// HttpApi is the backend over fetch (spec §2). It only moves JSON; decisions stay in use cases.
import { parseRemoteConfig, type RemoteConfig } from '../../domain/config'
import { ApiError, NetworkError } from '../../application/errors'
import type { Api } from '../../application/ports'
import type { ArticleStatus, Installation, Me, NotionPage, PushBody, PushResult, TokenGrant } from '../../application/types'
import * as wire from './wire'

export type Fetch = (url: string, init?: RequestInit) => Promise<Response>

export interface HttpApiOptions {
  baseUrl: string
  fetch?: Fetch
  timeoutMs?: number
}

interface RequestOptions {
  token?: string
  body?: unknown
}

async function apiError(res: Response): Promise<ApiError> {
  let code = res.status >= 500 ? 'internal' : `http_${res.status}`
  let message = res.statusText || `HTTP ${res.status}`
  try {
    const body = (await res.json()) as { error?: { code?: string; message?: string } }
    if (body?.error?.code) {
      code = body.error.code
      message = body.error.message ?? message
    }
  } catch {
    // not the §2.0 body; keep the status-derived code
  }
  return new ApiError(res.status, code, message)
}

export class HttpApi implements Api {
  private readonly base: string
  private readonly fetchFn: Fetch
  private readonly timeoutMs: number

  constructor(opts: HttpApiOptions) {
    this.base = opts.baseUrl.replace(/\/+$/, '')
    this.fetchFn = opts.fetch ?? ((url, init) => fetch(url, init))
    this.timeoutMs = opts.timeoutMs ?? 20_000
  }

  oauthStartUrl(p: { extRedirect: string; codeChallenge: string }): string {
    const q = new URLSearchParams({ ext_redirect: p.extRedirect, code_challenge: p.codeChallenge, code_challenge_method: 'S256' })
    return `${this.base}/v1/oauth/notion/start?${q.toString()}`
  }

  async config(): Promise<RemoteConfig> {
    return parseRemoteConfig(await this.request('GET', '/v1/config'))
  }

  async exchangeCode(p: { code: string; codeVerifier: string; deviceName: string }): Promise<TokenGrant> {
    const body = { code: p.code, code_verifier: p.codeVerifier, device_name: p.deviceName }
    return wire.tokenGrant(await this.request<wire.WireToken>('POST', '/v1/auth/token', { body }))
  }

  async me(token: string): Promise<Me> {
    return wire.me(await this.request<wire.WireMe>('GET', '/v1/me', { token }))
  }

  async logout(token: string): Promise<void> {
    await this.request('POST', '/v1/auth/logout', { token })
  }

  async deleteAccount(token: string): Promise<void> {
    await this.request('DELETE', '/v1/me', { token })
  }

  async push(token: string, body: PushBody): Promise<PushResult> {
    return wire.pushResult(await this.request<wire.WirePushResult>('POST', '/v1/sync/push', { token, body: wire.pushBody(body) }))
  }

  async articlesStatus(token: string, urls: readonly string[]): Promise<Record<string, ArticleStatus>> {
    const res = await this.request<{ items: Record<string, wire.WireArticleStatus> | null }>(
      'POST', '/v1/articles/status', { token, body: { canonical_urls: urls } },
    )
    return Object.fromEntries(Object.entries(res.items ?? {}).map(([url, s]) => [url, wire.articleStatus(s)]))
  }

  async setupPages(token: string, query: string): Promise<NotionPage[]> {
    const q = new URLSearchParams({ q: query })
    const res = await this.request<{ pages: NotionPage[] | null }>('GET', `/v1/setup/pages?${q.toString()}`, { token })
    return (res.pages ?? []).map((p) => ({ id: p.id, title: p.title, url: p.url }))
  }

  async setup(token: string, parentPageId: string): Promise<Installation> {
    const res = await this.request<wire.WireInstallation>('POST', '/v1/setup', { token, body: { parent_page_id: parentPageId } })
    const inst = wire.installation(res)
    if (!inst) throw new ApiError(500, 'bad_response', 'POST /v1/setup returned no installation')
    return inst
  }

  private async request<T = unknown>(method: string, path: string, opts: RequestOptions = {}): Promise<T> {
    const headers: Record<string, string> = { accept: 'application/json' }
    if (opts.token) headers.authorization = `Bearer ${opts.token}`
    if (opts.body !== undefined) headers['content-type'] = 'application/json'
    let res: Response
    try {
      res = await this.fetchFn(this.base + path, {
        method,
        headers,
        body: opts.body === undefined ? undefined : JSON.stringify(opts.body),
        signal: AbortSignal.timeout(this.timeoutMs),
      })
    } catch (e) {
      throw new NetworkError(`${method} ${path.split('?')[0]}: ${e instanceof Error ? e.message : String(e)}`)
    }
    if (!res.ok) throw await apiError(res)
    if (res.status === 202 || res.status === 204) return undefined as T
    const text = await res.text()
    if (!text) return undefined as T
    try {
      return JSON.parse(text) as T
    } catch {
      throw new ApiError(res.status, 'bad_response', `${method} ${path.split('?')[0]}: response is not JSON`)
    }
  }
}
```

- [ ] **Step 4: Implement PKCE**

`extension/src/adapters/crypto/pkce.ts`:

```ts
// PKCE S256 over WebCrypto (spec §2.1, T29): a 64-char verifier and base64url(SHA-256(verifier)).
import type { Pkce } from '../../application/ports'

const UNRESERVED = 'ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-._~'
// The largest multiple of 66 below 256; bytes at or above it are redrawn to avoid modulo bias.
const LIMIT = 256 - (256 % UNRESERVED.length)

export function base64url(bytes: Uint8Array): string {
  let s = ''
  for (const b of bytes) s += String.fromCharCode(b)
  return btoa(s).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '')
}

export function randomVerifier(length = 64): string {
  let out = ''
  while (out.length < length) {
    for (const b of crypto.getRandomValues(new Uint8Array(length))) {
      if (b < LIMIT && out.length < length) out += UNRESERVED[b % UNRESERVED.length]
    }
  }
  return out
}

export async function challengeFor(verifier: string): Promise<string> {
  const digest = await crypto.subtle.digest('SHA-256', new TextEncoder().encode(verifier))
  return base64url(new Uint8Array(digest))
}

export class WebCryptoPkce implements Pkce {
  async create(): Promise<{ verifier: string; challenge: string }> {
    const verifier = randomVerifier()
    return { verifier, challenge: await challengeFor(verifier) }
  }
}
```

- [ ] **Step 5: Run the tests**

Run: `cd extension && pnpm test adapters && pnpm test arch && pnpm typecheck`
Expected: PASS. `adapters/http` imports only domain, application and `./wire`, and `adapters/crypto` imports only application.

- [ ] **Step 6: Commit**

```bash
cd /Users/tuandoquoc/personal/reading-chrome-extension
git add extension/src/adapters/http extension/src/adapters/crypto
git commit -m "feat(extension): HTTP API client for the backend contract and PKCE over WebCrypto"
```

---
### Task 6: SyncService and ConfigService

**Files:**
- Modify: `extension/test/fakes.ts`. Append `FakeApi` and `FakeScheduler`.
- Create: `extension/src/application/sync.ts`, `sync.test.ts`
- Create: `extension/src/application/config-service.ts`, `config-service.test.ts`

**Interfaces:**
- Consumes:
  - from Task 4: `LocalStore`, `Api`, `Clock`, `Scheduler`, `Log`, `KvSchema`, `isApiError`, `PushResult`;
  - from Task 2: `RemoteConfig`, `parseRemoteConfig`.
- Produces:
  - `application/sync.ts`:
    - `SyncOutcome = 'disconnected' | 'backoff' | 'synced' | 'partial' | 'failed'`;
    - `PUSH_LIMIT = 50` and `RETENTION_DAYS = 30`;
    - `class SyncService(deps: SyncDeps)` with `run(): Promise<SyncOutcome>` and `prune(): Promise<number>`.
  - `application/config-service.ts`:
    - `CONFIG_MAX_AGE_MS`;
    - `class ConfigService(deps: ConfigDeps)` with `current()`, `refresh()`, `refreshIfStale()` and `ensureSchedule()`. Each returns `Promise<RemoteConfig | null>`, except `ensureSchedule`, which returns `Promise<void>`.
  - `test/fakes.ts`:
    - `FakeApi`, where each `Api` method can be overridden in the constructor, and `calls` records `{ method, args }`;
    - `FakeScheduler`, which records `every` calls.

Sync rules (spec §2.8, T28, C25):
- **Prerequisites:** no device token means `disconnected`, with no request sent. While `now < sync_backoff_until`, the result is `backoff`, also with no request.
- **Batches:** each push sends up to 50 unsynced segments, oldest first, plus exactly the articles those segments reference. Pushes repeat until nothing is unsynced.
- **Response handling:**
  - `accepted ∪ duplicate` are marked synced.
  - `rejected` are deleted and logged.
  - Any `retry` stops the loop with `partial` and leaves those segments unsynced. The next alarm resends them.
- **401:** delete `device_token`, `device_id`, `user` and `installation`, then return `disconnected`. The segments are kept.
- **Any other failure** (network, 5xx, 503, 409, 400, 413):
  - increment `sync_failures`;
  - set `sync_backoff_until = now + min(2^(failures−1), 30)` minutes;
  - return `failed`.
- **Success:** clears both backoff keys.
- **Concurrency:** concurrent `run()` calls share one loop.
- **Pruning:** `prune()` deletes synced segments that started more than 30 days ago (§3.4).

- [ ] **Step 1: Add the fakes**

Append to `extension/test/fakes.ts`:

```ts
import type { Api, Scheduler } from '../src/application/ports'
import type { ArticleStatus, Installation, Me, NotionPage, PushBody, PushResult, TokenGrant } from '../src/application/types'
import { parseRemoteConfig, type RemoteConfig } from '../src/domain/config'
import { wireConfig } from './fixtures'

export const INSTALLATION: Installation = {
  id: 'b1', workspaceName: "Tuan's Notion", status: 'active', setupError: '', libraryUrl: 'https://www.notion.so/lib',
}

// FakeApi answers like a healthy backend unless a method is overridden.
export class FakeApi implements Api {
  readonly calls: { method: keyof Api; args: unknown[] }[] = []

  constructor(private readonly over: Partial<Api> = {}) {}

  private record(method: keyof Api, args: unknown[]): void {
    this.calls.push({ method, args })
  }

  count(method: keyof Api): number {
    return this.calls.filter((c) => c.method === method).length
  }

  oauthStartUrl(p: { extRedirect: string; codeChallenge: string }): string {
    this.record('oauthStartUrl', [p])
    return this.over.oauthStartUrl?.(p) ?? `https://api.example.com/v1/oauth/notion/start?code_challenge=${p.codeChallenge}`
  }

  async config(): Promise<RemoteConfig> {
    this.record('config', [])
    return this.over.config ? this.over.config() : parseRemoteConfig(wireConfig)
  }

  async exchangeCode(p: { code: string; codeVerifier: string; deviceName: string }): Promise<TokenGrant> {
    this.record('exchangeCode', [p])
    return this.over.exchangeCode
      ? this.over.exchangeCode(p)
      : { deviceToken: 'rt_new', deviceId: 'd1', user: { id: 'u1', name: 'Tuan Do' }, installation: INSTALLATION }
  }

  async me(token: string): Promise<Me> {
    this.record('me', [token])
    return this.over.me
      ? this.over.me(token)
      : { user: { id: 'u1', name: 'Tuan Do' }, device: { id: 'd1', name: 'Mac' }, installation: INSTALLATION }
  }

  async logout(token: string): Promise<void> {
    this.record('logout', [token])
    if (this.over.logout) await this.over.logout(token)
  }

  async deleteAccount(token: string): Promise<void> {
    this.record('deleteAccount', [token])
    if (this.over.deleteAccount) await this.over.deleteAccount(token)
  }

  async push(token: string, body: PushBody): Promise<PushResult> {
    this.record('push', [token, body])
    return this.over.push
      ? this.over.push(token, body)
      : { accepted: body.segments.map((s) => s.id), duplicate: [], retry: [], rejected: [] }
  }

  async articlesStatus(token: string, urls: readonly string[]): Promise<Record<string, ArticleStatus>> {
    this.record('articlesStatus', [token, urls])
    return this.over.articlesStatus ? this.over.articlesStatus(token, urls) : {}
  }

  async setupPages(token: string, query: string): Promise<NotionPage[]> {
    this.record('setupPages', [token, query])
    return this.over.setupPages ? this.over.setupPages(token, query) : []
  }

  async setup(token: string, parentPageId: string): Promise<Installation> {
    this.record('setup', [token, parentPageId])
    return this.over.setup ? this.over.setup(token, parentPageId) : INSTALLATION
  }
}

export class FakeScheduler implements Scheduler {
  readonly alarms = new Map<string, number>()
  async every(name: string, periodMinutes: number): Promise<void> {
    this.alarms.set(name, periodMinutes)
  }
}
```

- [ ] **Step 2: Write the failing SyncService tests**

`extension/src/application/sync.test.ts`:

```ts
import { beforeEach, describe, expect, test } from 'vitest'
import { FakeApi, FixedClock, MemoryLog, MemoryStore } from '../../test/fakes'
import { record, segment } from '../../test/store-contract'
import { ApiError, NetworkError } from './errors'
import type { PushBody, PushResult } from './types'
import { SyncService } from './sync'

const MIN = 60_000

async function seed(store: MemoryStore, n: number, urls = ['https://e.com/a', 'https://e.com/b']): Promise<string[]> {
  const ids: string[] = []
  for (let i = 0; i < n; i++) {
    const id = `s${String(i).padStart(3, '0')}`
    ids.push(id)
    const startedAt = new Date(Date.UTC(2026, 9, 6, 0, 0, i)).toISOString()
    await record(store, segment(id, { canonicalUrl: urls[i % urls.length]!, startedAt }))
  }
  return ids
}

describe('SyncService.run', () => {
  let store: MemoryStore
  let clock: FixedClock
  let log: MemoryLog

  beforeEach(async () => {
    store = new MemoryStore()
    clock = new FixedClock()
    log = new MemoryLog()
    await store.setKv('device_token', 'rt_x')
  })

  const service = (api: FakeApi) => new SyncService({ store, api, clock, log })

  test('without a device token it does nothing', async () => {
    await store.deleteKv('device_token')
    await seed(store, 3)
    const api = new FakeApi()
    expect(await service(api).run()).toBe('disconnected')
    expect(api.count('push')).toBe(0)
  })

  test('C25: batches of ≤ 50, oldest first, each with only its own articles', async () => {
    const urls = Array.from({ length: 60 }, (_, i) => `https://e.com/${i}`)
    const ids = await seed(store, 120, urls)
    const api = new FakeApi()
    expect(await service(api).run()).toBe('synced')
    const bodies = api.calls.filter((c) => c.method === 'push').map((c) => c.args[1] as PushBody)
    expect(bodies.map((b) => b.segments.length)).toEqual([50, 50, 20])
    expect(bodies.flatMap((b) => b.segments.map((s) => s.id))).toEqual(ids)
    for (const b of bodies) {
      expect(b.articles.map((a) => a.canonicalUrl).sort()).toEqual([...new Set(b.segments.map((s) => s.canonicalUrl))].sort())
    }
    expect(await store.countUnsynced()).toBe(0)
  })

  test('the envelope carries client_time and the reader time zone; articles carry metadata only', async () => {
    await seed(store, 1)
    const api = new FakeApi()
    await service(api).run()
    const body = api.calls[0]?.args[1] as PushBody
    expect(body.clientTime).toBe('2026-10-06T03:00:00.000Z')
    expect(body.tz).toBe('Asia/Ho_Chi_Minh')
    expect(Object.keys(body.articles[0]!).sort()).toEqual(
      ['author', 'canonicalUrl', 'originalUrl', 'publishedAt', 'siteName', 'source', 'title', 'wordCount'],
    )
    expect(Object.keys(body.segments[0]!)).not.toContain('synced')
  })

  test('§2.8: accepted and duplicate are synced, rejected dropped with a log line, retry kept', async () => {
    await seed(store, 4)
    const api = new FakeApi({
      push: async () => ({ accepted: ['s000'], duplicate: ['s001'], retry: ['s002'], rejected: [{ id: 's003', reason: 'started_at_out_of_range' }] }),
    })
    expect(await service(api).run()).toBe('partial')
    expect((await store.unsyncedSegments(10)).map((s) => s.id)).toEqual(['s002'])
    expect(store.segmentRows.has('s003')).toBe(false)
    expect(log.lines.some((l) => l.includes('s003') && l.includes('started_at_out_of_range'))).toBe(true)
    expect(api.count('push')).toBe(1)
  })

  test('T28: 401 clears the device token and keeps the segments', async () => {
    await seed(store, 2)
    await store.setKv('device_id', 'd1')
    await store.setKv('installation', null)
    const api = new FakeApi({ push: async () => { throw new ApiError(401, 'unauthorized', 'revoked') } })
    expect(await service(api).run()).toBe('disconnected')
    expect(await store.getKv('device_token')).toBeUndefined()
    expect(await store.getKv('device_id')).toBeUndefined()
    expect(await store.getKv('installation')).toBeUndefined()
    expect(await store.countUnsynced()).toBe(2)
  })

  test('T28: failures back off 1, 2, 4 … minutes, capped at 30, and success resets', async () => {
    await seed(store, 1)
    let online = false
    const api = new FakeApi({
      push: async (_t, body) => {
        if (!online) throw new NetworkError('Failed to fetch')
        return { accepted: body.segments.map((s) => s.id), duplicate: [], retry: [], rejected: [] }
      },
    })
    const svc = service(api)
    const waits: number[] = []
    for (let i = 0; i < 7; i++) {
      expect(await svc.run()).toBe('failed')
      const until = Date.parse((await store.getKv('sync_backoff_until'))!)
      waits.push((until - clock.now().getTime()) / MIN)
      expect(await svc.run()).toBe('backoff')
      clock.current = new Date(until)
    }
    expect(waits).toEqual([1, 2, 4, 8, 16, 30, 30])
    online = true
    expect(await svc.run()).toBe('synced')
    expect(await store.getKv('sync_failures')).toBeUndefined()
    expect(await store.getKv('sync_backoff_until')).toBeUndefined()
  })

  test('503 capacity_exceeded backs off too', async () => {
    await seed(store, 1)
    const api = new FakeApi({ push: async () => { throw new ApiError(503, 'capacity_exceeded', 'busy') } })
    expect(await service(api).run()).toBe('failed')
    expect(await store.getKv('sync_failures')).toBe(1)
  })

  test('Review focus 4: concurrent runs share one push', async () => {
    await seed(store, 3)
    let release!: () => void
    const gate = new Promise<void>((r) => { release = r })
    const api = new FakeApi({
      push: async (_t, body) => {
        await gate
        return { accepted: body.segments.map((s) => s.id), duplicate: [], retry: [], rejected: [] }
      },
    })
    const svc = service(api)
    const a = svc.run()
    const b = svc.run()
    release()
    expect(await Promise.all([a, b])).toEqual(['synced', 'synced'])
    expect(api.count('push')).toBe(1)
    expect(await svc.run()).toBe('synced')
    expect(api.count('push')).toBe(1)
  })

  test('T28: offline for an hour, then online → every segment is synced exactly once', async () => {
    const ids = await seed(store, 75)
    const committed = new Map<string, number>()
    let online = false
    const api = new FakeApi({
      push: async (_t, body) => {
        if (!online) throw new NetworkError('offline')
        const res: PushResult = { accepted: [], duplicate: [], retry: [], rejected: [] }
        for (const s of body.segments) {
          if (committed.has(s.id)) res.duplicate.push(s.id)
          else res.accepted.push(s.id)
          committed.set(s.id, (committed.get(s.id) ?? 0) + 1)
        }
        return res
      },
    })
    const svc = service(api)
    for (let t = 0; t < 60; t += 2) {
      await svc.run()
      clock.advance(2 * MIN)
    }
    online = true
    clock.advance(60 * MIN)
    expect(await svc.run()).toBe('synced')
    expect([...committed.keys()].sort()).toEqual(ids)
    expect([...committed.values()].every((n) => n === 1)).toBe(true)
  })

  test('T28: a batch retried after a timeout comes back as duplicate only', async () => {
    const ids = await seed(store, 5)
    const committed = new Set<string>()
    let timeout = true
    const api = new FakeApi({
      push: async (_t, body) => {
        const res: PushResult = { accepted: [], duplicate: [], retry: [], rejected: [] }
        for (const s of body.segments) {
          if (committed.has(s.id)) res.duplicate.push(s.id)
          else { committed.add(s.id); res.accepted.push(s.id) }
        }
        if (timeout) { timeout = false; throw new NetworkError('timeout after commit') }
        return res
      },
    })
    const svc = service(api)
    expect(await svc.run()).toBe('failed')
    clock.advance(MIN)
    expect(await svc.run()).toBe('synced')
    const second = api.calls[1]?.args[1] as PushBody
    expect(second.segments.map((s) => s.id)).toEqual(ids)
    expect(await store.countUnsynced()).toBe(0)
  })
})

describe('SyncService.prune', () => {
  test('§3.4: deletes synced segments older than 30 days only', async () => {
    const store = new MemoryStore()
    const clock = new FixedClock(new Date('2026-10-06T00:00:00.000Z'))
    await record(store, segment('old', { startedAt: '2026-09-05T00:00:00.000Z' }))
    await record(store, segment('recent', { startedAt: '2026-09-07T00:00:00.000Z' }))
    await record(store, segment('old-unsynced', { startedAt: '2026-08-01T00:00:00.000Z' }))
    await store.markSynced(['old', 'recent'])
    const svc = new SyncService({ store, api: new FakeApi(), clock, log: new MemoryLog() })
    expect(await svc.prune()).toBe(1)
    expect([...store.segmentRows.keys()].sort()).toEqual(['old-unsynced', 'recent'])
  })
})
```

- [ ] **Step 3: Write the failing ConfigService tests**

`extension/src/application/config-service.test.ts`:

```ts
import { beforeEach, describe, expect, test } from 'vitest'
import { FakeApi, FakeScheduler, FixedClock, MemoryLog, MemoryStore } from '../../test/fakes'
import { wireConfig } from '../../test/fixtures'
import { parseRemoteConfig } from '../domain/config'
import { ConfigService } from './config-service'
import { NetworkError } from './errors'

const HOUR = 3_600_000

describe('ConfigService', () => {
  let store: MemoryStore
  let clock: FixedClock
  let scheduler: FakeScheduler

  beforeEach(() => {
    store = new MemoryStore()
    clock = new FixedClock()
    scheduler = new FakeScheduler()
  })

  const service = (api: FakeApi) => new ConfigService({ store, api, clock, scheduler, log: new MemoryLog() })
  const cached = (fetchedAt: string, interval = 2) => store.setKv('config', {
    fetchedAt,
    value: parseRemoteConfig({ ...wireConfig, tracking: { ...wireConfig.tracking, sync_interval_minutes: interval } }),
  })

  test('current() answers from the cache without a request', async () => {
    await cached('2026-10-06T02:00:00.000Z')
    const api = new FakeApi()
    expect((await service(api).current())?.tracking.idleSeconds).toBe(60)
    expect(api.count('config')).toBe(0)
  })

  test('current() fetches when nothing is cached, stores it and schedules sync every sync_interval_minutes', async () => {
    const api = new FakeApi()
    const cfg = await service(api).current()
    expect(cfg?.freediumHosts).toEqual(['freedium-mirror.cfd', 'freedium.cfd'])
    expect(await store.getKv('config')).toEqual({ fetchedAt: '2026-10-06T03:00:00.000Z', value: cfg })
    expect(scheduler.alarms.get('sync')).toBe(2)
  })

  test('a failed refresh keeps the cached config, or gives null when there is none', async () => {
    const api = new FakeApi({ config: async () => { throw new NetworkError('offline') } })
    expect(await service(api).refresh()).toBeNull()
    await cached('2026-10-01T00:00:00.000Z')
    expect((await service(api).refresh())?.tracking.segmentSeconds).toBe(60)
    expect((await store.getKv('config'))?.fetchedAt).toBe('2026-10-01T00:00:00.000Z')
  })

  test('T28: refreshIfStale refetches only after a day', async () => {
    await cached(new Date(clock.now().getTime() - 23 * HOUR).toISOString())
    const api = new FakeApi()
    await service(api).refreshIfStale()
    expect(api.count('config')).toBe(0)
    clock.advance(2 * HOUR)
    await service(api).refreshIfStale()
    expect(api.count('config')).toBe(1)
  })

  test('ensureSchedule follows the cached interval and does nothing without config', async () => {
    const api = new FakeApi({ config: async () => { throw new NetworkError('offline') } })
    await service(api).ensureSchedule()
    expect(scheduler.alarms.size).toBe(0)
    await cached('2026-10-06T02:00:00.000Z', 5)
    await service(api).ensureSchedule()
    expect(scheduler.alarms.get('sync')).toBe(5)
  })
})
```

- [ ] **Step 4: Run them to see them fail**

Run: `cd extension && pnpm test application/sync application/config-service`
Expected: FAIL, because `./sync` and `./config-service` cannot be resolved.

- [ ] **Step 5: Implement SyncService**

`extension/src/application/sync.ts`:

```ts
// SyncService pushes the local outbox to POST /v1/sync/push (spec §2.8, T28).
import type { ArticleMeta, LocalArticle, Segment, StoredSegment } from '../domain/reading'
import { isApiError } from './errors'
import type { Api, Clock, LocalStore, Log } from './ports'
import type { PushResult } from './types'

export type SyncOutcome = 'disconnected' | 'backoff' | 'synced' | 'partial' | 'failed'

// PUSH_LIMIT is the backend's per-request segment cap (C25).
export const PUSH_LIMIT = 50
// RETENTION_DAYS is how long synced segments stay local (spec §3.4).
export const RETENTION_DAYS = 30
const MAX_BACKOFF_MINUTES = 30
const MINUTE = 60_000
const DAY = 24 * 60 * MINUTE

export interface SyncDeps {
  store: LocalStore
  api: Api
  clock: Clock
  log: Log
}

function meta(a: LocalArticle): ArticleMeta {
  return {
    canonicalUrl: a.canonicalUrl, source: a.source, title: a.title, author: a.author, siteName: a.siteName,
    wordCount: a.wordCount, publishedAt: a.publishedAt, originalUrl: a.originalUrl,
  }
}

function plain(s: StoredSegment): Segment {
  return {
    id: s.id, sessionId: s.sessionId, seq: s.seq, canonicalUrl: s.canonicalUrl, startedAt: s.startedAt,
    endedAt: s.endedAt, activeSeconds: s.activeSeconds, maxProgress: s.maxProgress,
  }
}

export class SyncService {
  private inflight: Promise<SyncOutcome> | null = null

  constructor(private readonly deps: SyncDeps) {}

  // run pushes until the outbox is empty. Concurrent callers share one run.
  run(): Promise<SyncOutcome> {
    if (!this.inflight) {
      this.inflight = this.loop().finally(() => {
        this.inflight = null
      })
    }
    return this.inflight
  }

  // prune deletes synced segments that started more than RETENTION_DAYS ago.
  prune(): Promise<number> {
    const { store, clock } = this.deps
    return store.pruneSynced(new Date(clock.now().getTime() - RETENTION_DAYS * DAY).toISOString())
  }

  private async loop(): Promise<SyncOutcome> {
    const { store, api, clock, log } = this.deps
    const token = await store.getKv('device_token')
    if (!token) return 'disconnected'
    const until = await store.getKv('sync_backoff_until')
    if (until && clock.now().toISOString() < until) return 'backoff'

    for (;;) {
      const segments = await store.unsyncedSegments(PUSH_LIMIT)
      if (segments.length === 0) break
      const articles = await store.articles([...new Set(segments.map((s) => s.canonicalUrl))])
      let res: PushResult
      try {
        res = await api.push(token, {
          clientTime: clock.now().toISOString(),
          tz: clock.timeZone(),
          articles: articles.map(meta),
          segments: segments.map(plain),
        })
      } catch (e) {
        return this.fail(e)
      }
      await store.markSynced([...res.accepted, ...res.duplicate])
      if (res.rejected.length > 0) {
        await store.deleteSegments(res.rejected.map((r) => r.id))
        for (const r of res.rejected) log.warn(`sync: server rejected segment ${r.id}: ${r.reason}`)
      }
      if (res.retry.length > 0) {
        await this.clearBackoff()
        return 'partial'
      }
      // The server settled nothing: stop rather than resend the same batch in a loop.
      if (res.accepted.length + res.duplicate.length + res.rejected.length === 0) break
    }
    await this.clearBackoff()
    return 'synced'
  }

  private async fail(e: unknown): Promise<SyncOutcome> {
    const { store, clock, log } = this.deps
    if (isApiError(e, 401)) {
      await store.deleteKv('device_token', 'device_id', 'user', 'installation')
      log.warn('sync: device token rejected; disconnected')
      return 'disconnected'
    }
    const failures = ((await store.getKv('sync_failures')) ?? 0) + 1
    const minutes = Math.min(2 ** (failures - 1), MAX_BACKOFF_MINUTES)
    await store.setKv('sync_failures', failures)
    await store.setKv('sync_backoff_until', new Date(clock.now().getTime() + minutes * MINUTE).toISOString())
    log.warn(`sync: push failed (${e instanceof Error ? e.message : String(e)}); retrying in ${minutes} min`)
    return 'failed'
  }

  private async clearBackoff(): Promise<void> {
    await this.deps.store.deleteKv('sync_failures', 'sync_backoff_until')
  }
}
```

- [ ] **Step 6: Implement ConfigService**

`extension/src/application/config-service.ts`:

```ts
// ConfigService keeps GET /v1/config cached in kv.config, refreshes it daily (T28) and keeps
// the sync alarm on its sync_interval_minutes.
import type { RemoteConfig } from '../domain/config'
import type { Api, Clock, LocalStore, Log, Scheduler } from './ports'

export const CONFIG_MAX_AGE_MS = 24 * 60 * 60 * 1000
export const SYNC_ALARM = 'sync'

export interface ConfigDeps {
  store: LocalStore
  api: Api
  clock: Clock
  scheduler: Scheduler
  log: Log
}

export class ConfigService {
  constructor(private readonly deps: ConfigDeps) {}

  // current returns the cached config, fetching it only when there is none.
  async current(): Promise<RemoteConfig | null> {
    const cached = await this.deps.store.getKv('config')
    return cached ? cached.value : this.refresh()
  }

  // refresh fetches the config; on failure it keeps whatever was cached.
  async refresh(): Promise<RemoteConfig | null> {
    const { store, api, clock, scheduler, log } = this.deps
    try {
      const value = await api.config()
      await store.setKv('config', { fetchedAt: clock.now().toISOString(), value })
      await scheduler.every(SYNC_ALARM, value.tracking.syncIntervalMinutes)
      return value
    } catch (e) {
      log.warn(`config: refresh failed (${e instanceof Error ? e.message : String(e)})`)
      return (await store.getKv('config'))?.value ?? null
    }
  }

  async refreshIfStale(): Promise<RemoteConfig | null> {
    const cached = await this.deps.store.getKv('config')
    if (cached && this.deps.clock.now().getTime() - Date.parse(cached.fetchedAt) < CONFIG_MAX_AGE_MS) return cached.value
    return this.refresh()
  }

  // ensureSchedule (re)creates the sync alarm from the cached config, if any.
  async ensureSchedule(): Promise<void> {
    const cached = await this.deps.store.getKv('config')
    if (cached) await this.deps.scheduler.every(SYNC_ALARM, cached.value.tracking.syncIntervalMinutes)
  }
}
```

- [ ] **Step 7: Run the tests**

Run: `cd extension && pnpm test && pnpm typecheck`
Expected: PASS. Every test passes, including `concurrent runs share one push` and `prune keeps unsynced segments`.

- [ ] **Step 8: Commit**

```bash
cd /Users/tuandoquoc/personal/reading-chrome-extension
git add extension/src/application extension/test/fakes.ts
git commit -m "feat(extension): outbox sync with batching, backoff and 401 handling; cached remote config"
```

---
### Task 7: Session, setup and domain use cases, plus the background message router

**Files:**
- Modify: `extension/src/application/errors.ts` (append `NotConnectedError`)
- Modify: `extension/test/fakes.ts` (append `FakeAuthFlow`, `FakePkce`, `FakePermissions`, `FakeContentScripts`)
- Create: `extension/src/application/session.ts`, `session.test.ts`
- Create: `extension/src/application/setup.ts`, `setup.test.ts`
- Create: `extension/src/application/domains.ts`, `domains.test.ts`
- Create: `extension/src/application/protocol.ts`, `extension/src/application/router.ts`, `router.test.ts`

**Interfaces:**
- Consumes:
  - **Task 4:** the ports and types, `ApiError` and `isApiError`.
  - **Task 6:** `SyncOutcome` and `SyncService`, and `ConfigService`.
  - **Task 4:** `RecordService`.
  - **Task 2:** `normalizeDomain`, `originPatterns`, `chooseAdapter`, `AdapterName` and `DomainLists`.
- Produces:
  - **`session.ts`:**
    - `SessionState`: `{ kind: 'disconnected' }`, or `{ kind: 'connected'; user: User | null; installation: Installation | null }`;
    - `ConnectResult`: `{ ok: true; installation } | { ok: false; reason: string }`;
    - `AUTH_KEYS`;
    - `class SessionService(deps: SessionDeps)`, with `state()`, `connect()`, `refresh()`, `logout()` and `deleteAccount()`.
  - **`setup.ts`:** `class SetupService(deps)`, with `searchPages(query): Promise<NotionPage[]>` and `createDatabases(parentPageId): Promise<Installation>`.
  - **`domains.ts`:**
    - `SCRIPT_PREFIX = 'generic:'`;
    - `EnableResult`;
    - `class DomainService(deps)`, with `lists()`, `enable(input)`, `disable(domain)`, `deny(input)`, `undeny(domain)`, `reconcile()` and `plan(host, config): Promise<AdapterName | null>`.
  - **`protocol.ts`:**
    - `TrackingPlan { adapter: AdapterName; config: RemoteConfig }`;
    - `BackgroundRequest`, `BackgroundReply<T>` and `isBackgroundRequest(m)`;
    - `TabRequest = { type: 'current-page' }` and `CurrentPage { canonicalUrl: string | null }`.
  - **`router.ts`:** `RouterServices` and `createRouter(services): (msg: unknown) => Promise<unknown>`.
  - **`errors.ts`:** `NotConnectedError`.

Rules:
- **Connect (T29, §2.1–§2.3).**
  1. Create a PKCE pair.
  2. Build the start URL with `ext_redirect = authFlow.redirectUrl()`.
  3. Call `authFlow.launch(url)`.
  4. Read `code` or `error` from the redirect.
  5. POST `/v1/auth/token`.
  6. Store `device_token`, `device_id`, `user` and `installation`.
  7. Refresh from `GET /v1/me`.

  A cancelled window, `error=…`, a missing code or a failed exchange each store **nothing** and return `{ ok: false, reason }`.
- **Logout** always disconnects locally, even if the request fails.
- **Delete account** disconnects and clears local reading data only after `DELETE /v1/me` succeeds.
- **Enable domain:** `permissions.request` must be the **first** awaited call, because Chrome only grants it inside the click's user gesture.

- [ ] **Step 1: Add the error and the fakes**

Append to `extension/src/application/errors.ts`:

```ts
// NotConnectedError means the use case needs a device token and there is none.
export class NotConnectedError extends Error {
  override name = 'NotConnectedError'
  constructor() {
    super('not connected to Notion')
  }
}
```

Append to `extension/test/fakes.ts`:

```ts
import type { AuthFlow, ContentScripts, Permissions, Pkce } from '../src/application/ports'

export class FakeAuthFlow implements AuthFlow {
  readonly launched: string[] = []
  constructor(public respond: (url: string) => Promise<string> = async () => 'https://ext.chromiumapp.org/?code=one-time') {}
  redirectUrl(): string {
    return 'https://ext.chromiumapp.org/'
  }
  launch(url: string): Promise<string> {
    this.launched.push(url)
    return this.respond(url)
  }
}

export class FakePkce implements Pkce {
  async create(): Promise<{ verifier: string; challenge: string }> {
    return { verifier: 'v'.repeat(64), challenge: 'challenge-of-v' }
  }
}

export class FakePermissions implements Permissions {
  readonly granted = new Set<string>()
  readonly events: string[]
  constructor(public allow = true, events: string[] = []) {
    this.events = events
  }
  async request(origins: string[]): Promise<boolean> {
    this.events.push(`request ${origins.join(' ')}`)
    if (this.allow) for (const o of origins) this.granted.add(o)
    return this.allow
  }
  async remove(origins: string[]): Promise<boolean> {
    this.events.push(`remove ${origins.join(' ')}`)
    for (const o of origins) this.granted.delete(o)
    return true
  }
}

export class FakeContentScripts implements ContentScripts {
  readonly scripts = new Map<string, string[]>()
  async register(id: string, matches: string[]): Promise<void> {
    this.scripts.set(id, matches)
  }
  async unregister(ids: string[]): Promise<void> {
    for (const id of ids) this.scripts.delete(id)
  }
  async registeredIds(): Promise<string[]> {
    return [...this.scripts.keys()]
  }
}
```

- [ ] **Step 2: Write the failing tests**

`extension/src/application/session.test.ts`:

```ts
import { beforeEach, describe, expect, test, vi } from 'vitest'
import { FakeApi, FakeAuthFlow, FakePkce, INSTALLATION, MemoryLog, MemoryStore } from '../../test/fakes'
import { ApiError, NetworkError } from './errors'
import { SessionService } from './session'

describe('SessionService', () => {
  let store: MemoryStore
  let flow: FakeAuthFlow
  let afterConnect: () => void

  beforeEach(() => {
    store = new MemoryStore()
    flow = new FakeAuthFlow()
    afterConnect = vi.fn<() => void>()
  })

  const service = (api: FakeApi) => new SessionService({
    store, api, authFlow: flow, pkce: new FakePkce(), deviceName: () => 'Chrome on macOS', log: new MemoryLog(), afterConnect,
  })

  const nothingStored = async () => {
    for (const k of ['device_token', 'device_id', 'user', 'installation'] as const) expect(await store.getKv(k)).toBeUndefined()
  }

  describe('connect (T29)', () => {
    test('PKCE start → redirect code → token exchange → stored', async () => {
      const api = new FakeApi()
      const res = await service(api).connect()
      expect(res).toEqual({ ok: true, installation: INSTALLATION })
      expect(api.calls[0]).toEqual({
        method: 'oauthStartUrl', args: [{ extRedirect: 'https://ext.chromiumapp.org/', codeChallenge: 'challenge-of-v' }],
      })
      expect(flow.launched).toEqual(['https://api.example.com/v1/oauth/notion/start?code_challenge=challenge-of-v'])
      expect(api.calls.find((c) => c.method === 'exchangeCode')?.args[0]).toEqual({
        code: 'one-time', codeVerifier: 'v'.repeat(64), deviceName: 'Chrome on macOS',
      })
      expect(await store.getKv('device_token')).toBe('rt_new')
      expect(await store.getKv('device_id')).toBe('d1')
      expect(await store.getKv('user')).toEqual({ id: 'u1', name: 'Tuan Do' })
      expect(await store.getKv('installation')).toEqual(INSTALLATION)
      expect(api.calls.find((c) => c.method === 'me')?.args).toEqual(['rt_new'])
      expect(afterConnect).toHaveBeenCalledTimes(1)
    })

    test('user closes the window → cancelled, nothing stored', async () => {
      flow.respond = async () => { throw new Error('The user did not approve access.') }
      const api = new FakeApi()
      expect(await service(api).connect()).toEqual({ ok: false, reason: 'cancelled' })
      expect(api.count('exchangeCode')).toBe(0)
      await nothingStored()
      expect(afterConnect).not.toHaveBeenCalled()
    })

    test('error=access_denied → that reason, nothing stored', async () => {
      flow.respond = async () => 'https://ext.chromiumapp.org/?error=access_denied'
      expect(await service(new FakeApi()).connect()).toEqual({ ok: false, reason: 'access_denied' })
      await nothingStored()
    })

    test('a redirect without a code → invalid_response', async () => {
      flow.respond = async () => 'https://ext.chromiumapp.org/'
      expect(await service(new FakeApi()).connect()).toEqual({ ok: false, reason: 'invalid_response' })
      await nothingStored()
    })

    test('a failed exchange → the API error code, nothing stored', async () => {
      const api = new FakeApi({ exchangeCode: async () => { throw new ApiError(400, 'invalid_grant', 'bad code') } })
      expect(await service(api).connect()).toEqual({ ok: false, reason: 'invalid_grant' })
      await nothingStored()
    })

    test('a network failure during the exchange → network_error', async () => {
      const api = new FakeApi({ exchangeCode: async () => { throw new NetworkError('offline') } })
      expect(await service(api).connect()).toEqual({ ok: false, reason: 'network_error' })
    })
  })

  describe('state and refresh', () => {
    test('state is disconnected without a token, connected with one', async () => {
      const svc = service(new FakeApi())
      expect(await svc.state()).toEqual({ kind: 'disconnected' })
      await store.setKv('device_token', 'rt_x')
      expect(await svc.state()).toEqual({ kind: 'connected', user: null, installation: null })
    })

    test('refresh stores /v1/me', async () => {
      await store.setKv('device_token', 'rt_x')
      const api = new FakeApi({ me: async () => ({
        user: { id: 'u1', name: 'T' }, device: { id: 'd1', name: 'Mac' }, installation: { ...INSTALLATION, status: 'revoked' },
      }) })
      expect(await service(api).refresh()).toEqual({
        kind: 'connected', user: { id: 'u1', name: 'T' }, installation: { ...INSTALLATION, status: 'revoked' },
      })
      expect((await store.getKv('installation'))?.status).toBe('revoked')
    })

    test('refresh on 401 disconnects', async () => {
      await store.setKv('device_token', 'rt_x')
      const api = new FakeApi({ me: async () => { throw new ApiError(401, 'unauthorized', 'revoked') } })
      expect(await service(api).refresh()).toEqual({ kind: 'disconnected' })
      expect(await store.getKv('device_token')).toBeUndefined()
    })

    test('refresh offline keeps the cached state', async () => {
      await store.setKv('device_token', 'rt_x')
      await store.setKv('installation', INSTALLATION)
      const api = new FakeApi({ me: async () => { throw new NetworkError('offline') } })
      expect(await service(api).refresh()).toEqual({ kind: 'connected', user: null, installation: INSTALLATION })
    })
  })

  describe('logout and delete (T31, §2.17)', () => {
    beforeEach(async () => {
      await store.setKv('device_token', 'rt_x')
      await store.setKv('device_id', 'd1')
      await store.setKv('installation', INSTALLATION)
      await store.setKv('enabled_domains', ['example.com'])
      store.articleRows.set('https://e.com/a', {
        canonicalUrl: 'https://e.com/a', source: 'web', title: 'A', author: '', siteName: '', wordCount: 0,
        publishedAt: '', originalUrl: 'https://e.com/a', totalSeconds: 60, maxProgress: 0.5, lastReadAt: '2026-10-06T03:00:00.000Z',
      })
    })

    test('logout calls the API and disconnects locally even if it fails', async () => {
      const api = new FakeApi({ logout: async () => { throw new NetworkError('offline') } })
      await service(api).logout()
      expect(api.calls.map((c) => c.method)).toEqual(['logout'])
      await nothingStored()
      expect(store.articleRows.size).toBe(1)
    })

    test('deleteAccount clears auth and local reading data after the server accepts', async () => {
      const api = new FakeApi()
      await service(api).deleteAccount()
      expect(api.count('deleteAccount')).toBe(1)
      await nothingStored()
      expect(store.articleRows.size).toBe(0)
      expect(await store.getKv('enabled_domains')).toEqual(['example.com'])
    })

    test('deleteAccount keeps everything when the server call fails', async () => {
      const api = new FakeApi({ deleteAccount: async () => { throw new ApiError(500, 'internal', 'boom') } })
      await expect(service(api).deleteAccount()).rejects.toBeInstanceOf(ApiError)
      expect(await store.getKv('device_token')).toBe('rt_x')
      expect(store.articleRows.size).toBe(1)
    })
  })
})
```

`extension/src/application/setup.test.ts`:

```ts
import { expect, test } from 'vitest'
import { FakeApi, INSTALLATION, MemoryStore } from '../../test/fakes'
import { NotConnectedError } from './errors'
import { SetupService } from './setup'

test('searchPages needs a token and passes the query', async () => {
  const store = new MemoryStore()
  const api = new FakeApi({ setupPages: async () => [{ id: 'p1', title: 'Reading', url: 'https://www.notion.so/p1' }] })
  const svc = new SetupService({ store, api })
  await expect(svc.searchPages('Read')).rejects.toBeInstanceOf(NotConnectedError)
  await store.setKv('device_token', 'rt_x')
  expect(await svc.searchPages('Read')).toEqual([{ id: 'p1', title: 'Reading', url: 'https://www.notion.so/p1' }])
  expect(api.calls.at(-1)).toEqual({ method: 'setupPages', args: ['rt_x', 'Read'] })
})

test('createDatabases stores the now-active installation (§2.7)', async () => {
  const store = new MemoryStore()
  await store.setKv('device_token', 'rt_x')
  await store.setKv('installation', { ...INSTALLATION, status: 'needs_setup', setupError: 'template_not_found', libraryUrl: null })
  const api = new FakeApi()
  expect(await new SetupService({ store, api }).createDatabases('p1')).toEqual(INSTALLATION)
  expect(api.calls.at(-1)).toEqual({ method: 'setup', args: ['rt_x', 'p1'] })
  expect(await store.getKv('installation')).toEqual(INSTALLATION)
})
```

`extension/src/application/domains.test.ts`:

```ts
import { beforeEach, describe, expect, test } from 'vitest'
import { FakeContentScripts, FakePermissions, MemoryStore } from '../../test/fakes'
import { parseRemoteConfig } from '../domain/config'
import { wireConfig } from '../../test/fixtures'
import type { KvKey, KvSchema } from './ports'
import { DomainService, SCRIPT_PREFIX } from './domains'

// EventStore logs storage access so tests can check what happened before the permission prompt.
class EventStore extends MemoryStore {
  constructor(readonly events: string[]) {
    super()
  }
  override async getKv<K extends KvKey>(key: K): Promise<KvSchema[K] | undefined> {
    this.events.push(`get ${key}`)
    return super.getKv(key)
  }
}

describe('DomainService', () => {
  let events: string[]
  let store: EventStore
  let permissions: FakePermissions
  let scripts: FakeContentScripts
  let svc: DomainService

  beforeEach(() => {
    events = []
    store = new EventStore(events)
    permissions = new FakePermissions(true, events)
    scripts = new FakeContentScripts()
    svc = new DomainService({ store, permissions, scripts })
  })

  test('T31: enabling asks for permission first, then registers the content script', async () => {
    expect(await svc.enable('https://www.Example.com/post')).toEqual({ ok: true, domain: 'example.com' })
    expect(events[0]).toBe('request https://example.com/* https://*.example.com/*')
    expect(scripts.scripts.get(`${SCRIPT_PREFIX}example.com`)).toEqual(['https://example.com/*', 'https://*.example.com/*'])
    expect(await store.getKv('enabled_domains')).toEqual(['example.com'])
  })

  test('invalid input never prompts', async () => {
    expect(await svc.enable('not a domain')).toEqual({ ok: false, reason: 'invalid' })
    expect(events).toEqual([])
  })

  test('a declined prompt registers and saves nothing', async () => {
    permissions.allow = false
    expect(await svc.enable('example.com')).toEqual({ ok: false, reason: 'permission_denied' })
    expect(scripts.scripts.size).toBe(0)
    expect(await store.getKv('enabled_domains')).toBeUndefined()
  })

  test('enabling twice keeps one entry', async () => {
    await svc.enable('example.com')
    expect(await svc.enable('example.com')).toEqual({ ok: false, reason: 'already_enabled' })
    expect(await store.getKv('enabled_domains')).toEqual(['example.com'])
  })

  test('T31: disabling unregisters the script and drops the permission', async () => {
    await svc.enable('example.com')
    await svc.enable('other.org')
    await svc.disable('example.com')
    expect([...scripts.scripts.keys()]).toEqual([`${SCRIPT_PREFIX}other.org`])
    expect(permissions.granted.has('https://example.com/*')).toBe(false)
    expect(await store.getKv('enabled_domains')).toEqual(['other.org'])
  })

  test('deny and undeny', async () => {
    expect(await svc.deny('Blog.Example.com')).toEqual({ ok: true, domain: 'blog.example.com' })
    expect(await svc.deny('??')).toEqual({ ok: false, reason: 'invalid' })
    expect((await svc.lists()).denied).toEqual(['blog.example.com'])
    await svc.undeny('blog.example.com')
    expect((await svc.lists()).denied).toEqual([])
  })

  test('T31: plan never tracks a denied domain, even if enabled', async () => {
    const cfg = parseRemoteConfig(wireConfig)
    await svc.enable('example.com')
    expect(await svc.plan('blog.example.com', cfg)).toBe('generic')
    await svc.deny('example.com')
    expect(await svc.plan('blog.example.com', cfg)).toBeNull()
    expect(await svc.plan('medium.com', cfg)).toBe('freedium-medium')
    expect(await svc.plan('unrelated.net', cfg)).toBeNull()
  })

  test('reconcile restores missing scripts and removes stale generic ones only', async () => {
    await store.setKv('enabled_domains', ['example.com'])
    await scripts.register(`${SCRIPT_PREFIX}gone.org`, ['https://gone.org/*'])
    await scripts.register('something-else', ['https://x.org/*'])
    await svc.reconcile()
    expect([...scripts.scripts.keys()].sort()).toEqual([`${SCRIPT_PREFIX}example.com`, 'something-else'])
  })
})
```

`extension/src/application/router.test.ts`:

```ts
import { describe, expect, test, vi } from 'vitest'
import { parseRemoteConfig, type RemoteConfig } from '../domain/config'
import { wireConfig } from '../../test/fixtures'
import { META } from '../../test/store-contract'
import { createRouter, type RouterServices } from './router'

const cfg = parseRemoteConfig(wireConfig)

function services(config: RemoteConfig | null = cfg): RouterServices {
  return {
    config: { current: vi.fn(async () => config) },
    domains: { plan: vi.fn(async (host: string) => (host === 'medium.com' ? 'freedium-medium' as const : null)) },
    recorder: { record: vi.fn(async () => true) },
    session: { connect: vi.fn(async () => ({ ok: false as const, reason: 'cancelled' })) },
    sync: { run: vi.fn(async () => 'synced' as const) },
  }
}

describe('router', () => {
  test('tracking-plan returns the adapter with the config', async () => {
    const route = createRouter(services())
    expect(await route({ type: 'tracking-plan', host: 'medium.com' })).toEqual({ adapter: 'freedium-medium', config: cfg })
    expect(await route({ type: 'tracking-plan', host: 'example.com' })).toBeNull()
  })

  test('without config nothing is tracked (no inlined thresholds)', async () => {
    expect(await createRouter(services(null))({ type: 'tracking-plan', host: 'medium.com' })).toBeNull()
  })

  test('record, connect and sync-now delegate', async () => {
    const s = services()
    const route = createRouter(s)
    const segment = { id: 's1', sessionId: 'x', seq: 0, canonicalUrl: META.canonicalUrl, startedAt: '', endedAt: '', activeSeconds: 5, maxProgress: 0 }
    expect(await route({ type: 'record', article: META, segment })).toEqual({ stored: true })
    expect(s.recorder.record).toHaveBeenCalledWith(META, segment)
    expect(await route({ type: 'connect' })).toEqual({ ok: false, reason: 'cancelled' })
    expect(await route({ type: 'sync-now' })).toBe('synced')
  })

  test('unknown messages are rejected', async () => {
    const route = createRouter(services())
    await expect(route({ type: 'nope' })).rejects.toThrow(/unknown message/)
    await expect(route('hello')).rejects.toThrow(/unknown message/)
  })
})
```

- [ ] **Step 3: Run them to see them fail**

Run: `cd extension && pnpm test application`
Expected: FAIL in `session`, `setup`, `domains` and `router`, because their modules cannot be resolved.

- [ ] **Step 4: Implement the use cases**

`extension/src/application/session.ts`:

```ts
// SessionService owns the device's connection to the backend (spec §2.1–§2.5, §2.17, T29).
import { isApiError, NetworkError, NotConnectedError } from './errors'
import type { Api, AuthFlow, KvKey, LocalStore, Log, Pkce } from './ports'
import type { Installation, User } from './types'

export type SessionState =
  | { kind: 'disconnected' }
  | { kind: 'connected'; user: User | null; installation: Installation | null }

export type ConnectResult = { ok: true; installation: Installation | null } | { ok: false; reason: string }

export const AUTH_KEYS: readonly KvKey[] = ['device_token', 'device_id', 'user', 'installation']

export interface SessionDeps {
  store: LocalStore
  api: Api
  authFlow: AuthFlow
  pkce: Pkce
  deviceName: () => string
  log: Log
  // afterConnect runs once a device token is stored (e.g. to push the waiting outbox).
  afterConnect?: () => void
}

export class SessionService {
  constructor(private readonly deps: SessionDeps) {}

  async state(): Promise<SessionState> {
    const { store } = this.deps
    if (!(await store.getKv('device_token'))) return { kind: 'disconnected' }
    return { kind: 'connected', user: (await store.getKv('user')) ?? null, installation: (await store.getKv('installation')) ?? null }
  }

  async connect(): Promise<ConnectResult> {
    const { api, authFlow, pkce, store, log } = this.deps
    const { verifier, challenge } = await pkce.create()
    const url = api.oauthStartUrl({ extRedirect: authFlow.redirectUrl(), codeChallenge: challenge })
    let redirect: string
    try {
      redirect = await authFlow.launch(url)
    } catch (e) {
      log.info(`connect: auth window closed (${e instanceof Error ? e.message : String(e)})`)
      return { ok: false, reason: 'cancelled' }
    }
    const params = new URL(redirect).searchParams
    const error = params.get('error')
    if (error) return { ok: false, reason: error }
    const code = params.get('code')
    if (!code) return { ok: false, reason: 'invalid_response' }
    try {
      const grant = await api.exchangeCode({ code, codeVerifier: verifier, deviceName: this.deps.deviceName() })
      await store.setKv('device_token', grant.deviceToken)
      await store.setKv('device_id', grant.deviceId)
      await store.setKv('user', grant.user)
      await store.setKv('installation', grant.installation)
      await this.refresh() // T29: GET /v1/me after the exchange; best-effort, it never throws
      this.deps.afterConnect?.()
      return { ok: true, installation: grant.installation }
    } catch (e) {
      if (isApiError(e)) return { ok: false, reason: e.code }
      if (e instanceof NetworkError) return { ok: false, reason: 'network_error' }
      throw e
    }
  }

  // refresh reloads GET /v1/me; offline it returns the cached state.
  async refresh(): Promise<SessionState> {
    const { store, api } = this.deps
    const token = await store.getKv('device_token')
    if (!token) return { kind: 'disconnected' }
    try {
      const me = await api.me(token)
      await store.setKv('user', me.user)
      await store.setKv('installation', me.installation)
      return { kind: 'connected', user: me.user, installation: me.installation }
    } catch (e) {
      if (isApiError(e, 401)) {
        await this.forget()
        return { kind: 'disconnected' }
      }
      return this.state()
    }
  }

  // logout revokes the device token on the server and always disconnects locally.
  async logout(): Promise<void> {
    const token = await this.deps.store.getKv('device_token')
    if (token) {
      try {
        await this.deps.api.logout(token)
      } catch (e) {
        this.deps.log.warn(`logout: server call failed (${e instanceof Error ? e.message : String(e)})`)
      }
    }
    await this.forget()
  }

  // deleteAccount asks the server to purge the account (§2.17), then clears local data.
  async deleteAccount(): Promise<void> {
    const { store, api } = this.deps
    const token = await store.getKv('device_token')
    if (!token) throw new NotConnectedError()
    await api.deleteAccount(token)
    await this.forget()
    await store.clearReading()
  }

  private async forget(): Promise<void> {
    await this.deps.store.deleteKv(...AUTH_KEYS)
  }
}
```

`extension/src/application/setup.ts`:

```ts
// SetupService finishes a needs_setup installation (spec §2.6, §2.7, T31).
import { NotConnectedError } from './errors'
import type { Api, LocalStore } from './ports'
import type { Installation, NotionPage } from './types'

export interface SetupDeps {
  store: LocalStore
  api: Api
}

export class SetupService {
  constructor(private readonly deps: SetupDeps) {}

  async searchPages(query: string): Promise<NotionPage[]> {
    return this.deps.api.setupPages(await this.token(), query)
  }

  async createDatabases(parentPageId: string): Promise<Installation> {
    const inst = await this.deps.api.setup(await this.token(), parentPageId)
    await this.deps.store.setKv('installation', inst)
    return inst
  }

  private async token(): Promise<string> {
    const token = await this.deps.store.getKv('device_token')
    if (!token) throw new NotConnectedError()
    return token
  }
}
```

`extension/src/application/domains.ts`:

```ts
// DomainService manages user-enabled and denied domains (spec T31, C24).
import type { RemoteConfig } from '../domain/config'
import { chooseAdapter, normalizeDomain, originPatterns, type AdapterName, type DomainLists } from '../domain/domains'
import type { ContentScripts, LocalStore, Permissions } from './ports'

export const SCRIPT_PREFIX = 'generic:'

export type EnableResult =
  | { ok: true; domain: string }
  | { ok: false; reason: 'invalid' | 'permission_denied' | 'already_enabled' }

export interface DomainDeps {
  store: LocalStore
  permissions: Permissions
  scripts: ContentScripts
}

export class DomainService {
  constructor(private readonly deps: DomainDeps) {}

  async lists(): Promise<DomainLists> {
    const { store } = this.deps
    return { enabled: (await store.getKv('enabled_domains')) ?? [], denied: (await store.getKv('denied_domains')) ?? [] }
  }

  // enable must run from a click handler: the permission prompt is the first awaited call,
  // because Chrome grants permissions.request only inside the user gesture.
  async enable(input: string): Promise<EnableResult> {
    const domain = normalizeDomain(input)
    if (!domain) return { ok: false, reason: 'invalid' }
    const origins = originPatterns(domain)
    if (!(await this.deps.permissions.request(origins))) return { ok: false, reason: 'permission_denied' }
    const { enabled } = await this.lists()
    if (enabled.includes(domain)) return { ok: false, reason: 'already_enabled' }
    await this.deps.scripts.register(SCRIPT_PREFIX + domain, origins)
    await this.deps.store.setKv('enabled_domains', [...enabled, domain].sort())
    return { ok: true, domain }
  }

  async disable(domain: string): Promise<void> {
    await this.deps.scripts.unregister([SCRIPT_PREFIX + domain])
    await this.deps.permissions.remove(originPatterns(domain))
    const { enabled } = await this.lists()
    await this.deps.store.setKv('enabled_domains', enabled.filter((d) => d !== domain))
  }

  async deny(input: string): Promise<{ ok: true; domain: string } | { ok: false; reason: 'invalid' }> {
    const domain = normalizeDomain(input)
    if (!domain) return { ok: false, reason: 'invalid' }
    const { denied } = await this.lists()
    if (!denied.includes(domain)) await this.deps.store.setKv('denied_domains', [...denied, domain].sort())
    return { ok: true, domain }
  }

  async undeny(domain: string): Promise<void> {
    const { denied } = await this.lists()
    await this.deps.store.setKv('denied_domains', denied.filter((d) => d !== domain))
  }

  // reconcile makes registered generic content scripts match enabled_domains.
  async reconcile(): Promise<void> {
    const { scripts } = this.deps
    const { enabled } = await this.lists()
    const registered = (await scripts.registeredIds()).filter((id) => id.startsWith(SCRIPT_PREFIX))
    const wanted = new Set(enabled.map((d) => SCRIPT_PREFIX + d))
    const stale = registered.filter((id) => !wanted.has(id))
    if (stale.length > 0) await scripts.unregister(stale)
    for (const d of enabled) {
      if (!registered.includes(SCRIPT_PREFIX + d)) await scripts.register(SCRIPT_PREFIX + d, originPatterns(d))
    }
  }

  async plan(host: string, config: RemoteConfig): Promise<AdapterName | null> {
    return chooseAdapter(host, config.freediumHosts, await this.lists())
  }
}
```

`extension/src/application/protocol.ts`:

```ts
// Messages between the content script / popup and the background service worker.
import type { RemoteConfig } from '../domain/config'
import type { AdapterName } from '../domain/domains'
import type { ArticleMeta, Segment } from '../domain/reading'
import type { ConnectResult } from './session'
import type { SyncOutcome } from './sync'

export interface TrackingPlan {
  adapter: AdapterName
  config: RemoteConfig
}

export type BackgroundRequest =
  | { type: 'tracking-plan'; host: string }
  | { type: 'record'; article: ArticleMeta; segment: Segment }
  | { type: 'connect' }
  | { type: 'sync-now' }

export type BackgroundReply<T extends BackgroundRequest['type']> =
  T extends 'tracking-plan' ? TrackingPlan | null
    : T extends 'record' ? { stored: boolean }
      : T extends 'connect' ? ConnectResult
        : T extends 'sync-now' ? SyncOutcome
          : never

const TYPES = new Set(['tracking-plan', 'record', 'connect', 'sync-now'])

export function isBackgroundRequest(m: unknown): m is BackgroundRequest {
  return typeof m === 'object' && m !== null && TYPES.has((m as { type?: unknown }).type as string)
}

// TabRequest goes from the popup to the content script of the active tab.
export type TabRequest = { type: 'current-page' }

export interface CurrentPage {
  canonicalUrl: string | null
}
```

`extension/src/application/router.ts`:

```ts
// createRouter maps background messages to use cases; the entry point wires it to chrome.runtime.
import type { ConfigService } from './config-service'
import type { DomainService } from './domains'
import { isBackgroundRequest } from './protocol'
import type { RecordService } from './record'
import type { SessionService } from './session'
import type { SyncService } from './sync'

export interface RouterServices {
  config: Pick<ConfigService, 'current'>
  domains: Pick<DomainService, 'plan'>
  recorder: Pick<RecordService, 'record'>
  session: Pick<SessionService, 'connect'>
  sync: Pick<SyncService, 'run'>
}

export function createRouter(s: RouterServices): (msg: unknown) => Promise<unknown> {
  return async (msg) => {
    if (!isBackgroundRequest(msg)) throw new Error('unknown message')
    switch (msg.type) {
      case 'tracking-plan': {
        const config = await s.config.current()
        if (!config) return null
        const adapter = await s.domains.plan(msg.host, config)
        return adapter ? { adapter, config } : null
      }
      case 'record':
        return { stored: await s.recorder.record(msg.article, msg.segment) }
      case 'connect':
        return s.session.connect()
      case 'sync-now':
        return s.sync.run()
    }
  }
}
```

- [ ] **Step 5: Run the tests**

Run: `cd extension && pnpm test && pnpm typecheck`
Expected: PASS. All tests pass and `pnpm typecheck` is clean.

- [ ] **Step 6: Commit**

```bash
cd /Users/tuandoquoc/personal/reading-chrome-extension
git add extension/src/application extension/test/fakes.ts
git commit -m "feat(extension): connect/logout/delete, setup, domain settings and the background router"
```

---
### Task 8: Site adapters (Freedium/Medium and generic)

**Files:**
- Create: `extension/src/adapters/site/types.ts`, `metadata.ts`, `freedium-medium.ts`, `generic.ts`, `registry.ts`
- Test: `extension/src/adapters/site/metadata.test.ts`, `freedium-medium.test.ts`, `generic.test.ts`

**Interfaces:**
- Consumes:
  - from Task 2: `RemoteConfig`, `normalizeUrl`, `mediumCanonical`, `AdapterName` and `ArticleMeta`.
- Produces:
  - `types.ts`: `PageContext { doc: Document; location: string; config: RemoteConfig }` and `SiteAdapter { name: AdapterName; extract(ctx): ArticleMeta | null; contentRoot(doc): Element }`.
  - `metadata.ts`, the shared DOM readers:
    - `metaContent`, `firstText`, `jsonLdArticle`, `canonicalFromDocument`, `defaultContentRoot`, `countWords`;
    - `finishMeta`, which trims and clamps to the backend limits and returns `null` when the URL is over 2048 characters.
  - `freedium-medium.ts`: `freediumMedium: SiteAdapter`, with source `medium`.
  - `generic.ts`: `generic: SiteAdapter`, with source `web`.
  - `registry.ts`: `adapterFor(name: AdapterName): SiteAdapter`.

Rules (spec T26):
- **Freedium/Medium canonical:** `mediumCanonical(location)` first. If that finds no post id, fall back to `link[rel=canonical]` → `og:url` → the normalized location.
- **Freedium/Medium metadata:**
  - title: `h1` → `og:title` → `document.title`;
  - author: `meta[name=author]` → `a[rel=author]` / `[data-testid=authorName]`;
  - site: `og:site_name`;
  - word count: from the content root's text.
- **Generic canonical:** `link[rel=canonical]` → `og:url` → location, then normalized. It upgrades to https when the page is served over https.
- **Generic metadata:** JSON-LD `Article` / `BlogPosting` / `NewsArticle` first, reading `headline`, `author.name`, `datePublished` and `wordCount`. Then OpenGraph, then `meta[name=author]`.
- **Content root:** `article` → `main` → `body`.
- **Backend limits (backend `readsync/validate.go`):**
  - `word_count` is clamped to 0…200000 and the URL must be at most 2048 characters. Breaking either would make the backend reject the article *and its segments*.
  - The backend truncates title, author and site itself; this adapter truncates too, so local data matches.

- [ ] **Step 1: Write the failing tests**

`extension/src/adapters/site/metadata.test.ts`:

```ts
// @vitest-environment jsdom
import { describe, expect, test } from 'vitest'
import { wireConfig } from '../../../test/fixtures'
import { parseRemoteConfig } from '../../domain/config'
import { canonicalFromDocument, countWords, defaultContentRoot, finishMeta, jsonLdArticle } from './metadata'

const config = parseRemoteConfig(wireConfig)
const page = (html: string) => new DOMParser().parseFromString(html, 'text/html')

describe('canonicalFromDocument', () => {
  test('link rel=canonical wins, resolved against the page and normalized', () => {
    const doc = page('<link rel="canonical" href="/post/?utm_source=x"><meta property="og:url" content="https://e.com/og">')
    expect(canonicalFromDocument(doc, 'https://e.com/post/?ref=hn#top', config)).toBe('https://e.com/post')
  })

  test('og:url next, then the location', () => {
    expect(canonicalFromDocument(page('<meta property="og:url" content="https://e.com/og/">'), 'https://e.com/x', config)).toBe('https://e.com/og')
    expect(canonicalFromDocument(page(''), 'https://E.com/x/?fbclid=1', config)).toBe('https://e.com/x')
  })

  test('an http canonical on an https page is upgraded', () => {
    expect(canonicalFromDocument(page('<link rel="canonical" href="http://e.com/a">'), 'https://e.com/a', config)).toBe('https://e.com/a')
  })

  test('a junk canonical falls through to the next candidate', () => {
    expect(canonicalFromDocument(page('<link rel="canonical" href="javascript:void(0)">'), 'https://e.com/a', config)).toBe('https://e.com/a')
  })
})

describe('jsonLdArticle', () => {
  test('finds an article inside @graph with an author object', () => {
    const doc = page(`<script type="application/ld+json">${JSON.stringify({
      '@context': 'https://schema.org',
      '@graph': [{ '@type': 'WebSite', name: 'x' }, { '@type': ['BlogPosting'], headline: 'H', author: { '@type': 'Person', name: 'Ann' },
        datePublished: '2026-09-30T08:00:00Z', wordCount: '1200' }],
    })}</script>`)
    expect(jsonLdArticle(doc)).toEqual({ headline: 'H', author: 'Ann', datePublished: '2026-09-30T08:00:00Z', wordCount: 1200 })
  })

  test('reads an author array and skips broken JSON', () => {
    const doc = page(`<script type="application/ld+json">{ not json</script>
      <script type="application/ld+json">[{"@type":"NewsArticle","headline":"N","author":[{"name":"A"},{"name":"B"}]}]</script>`)
    expect(jsonLdArticle(doc)).toEqual({ headline: 'N', author: 'A', datePublished: '', wordCount: 0 })
  })

  test('no article → empty fields', () => {
    expect(jsonLdArticle(page('<p>hi</p>'))).toEqual({ headline: '', author: '', datePublished: '', wordCount: 0 })
  })
})

test('content root is article, then main, then body', () => {
  expect(defaultContentRoot(page('<main><article id="a"></article></main>')).id).toBe('a')
  expect(defaultContentRoot(page('<main id="m"></main>')).id).toBe('m')
  expect(defaultContentRoot(page('<p></p>')).tagName).toBe('BODY')
})

test('countWords counts words per text node, across blocks, skipping scripts', () => {
  const doc = page('<article><h1>Two words</h1><p>and  three\nmore</p><script>var a = 1</script></article>')
  expect(countWords(defaultContentRoot(doc))).toBe(5)
})

describe('finishMeta', () => {
  const base = {
    canonicalUrl: 'https://e.com/a', source: 'web', title: '  T  ', author: 'A', siteName: 'S',
    wordCount: 10, publishedAt: '2026-09-30', originalUrl: 'https://e.com/a?x=1',
  }

  test('trims and truncates text to the backend limits', () => {
    const m = finishMeta({ ...base, title: 'x'.repeat(600), author: 'y'.repeat(300) })!
    expect(m.title).toHaveLength(500)
    expect(m.author).toHaveLength(200)
    expect(finishMeta(base)?.title).toBe('T')
  })

  test('clamps word count to 0…200000', () => {
    expect(finishMeta({ ...base, wordCount: 999_999 })?.wordCount).toBe(200_000)
    expect(finishMeta({ ...base, wordCount: -3 })?.wordCount).toBe(0)
    expect(finishMeta({ ...base, wordCount: Number.NaN })?.wordCount).toBe(0)
  })

  test('a canonical URL over 2048 chars cannot be tracked', () => {
    expect(finishMeta({ ...base, canonicalUrl: `https://e.com/${'a'.repeat(2048)}` })).toBeNull()
  })

  test('an over-long original URL is cut back to the canonical', () => {
    expect(finishMeta({ ...base, originalUrl: `https://e.com/${'a'.repeat(3000)}` })?.originalUrl).toBe('https://e.com/a')
  })
})
```

`extension/src/adapters/site/freedium-medium.test.ts`:

```ts
// @vitest-environment jsdom
import { expect, test } from 'vitest'
import { wireConfig } from '../../../test/fixtures'
import { parseRemoteConfig } from '../../domain/config'
import { freediumMedium } from './freedium-medium'

const config = parseRemoteConfig(wireConfig)
const page = (html: string) => new DOMParser().parseFromString(html, 'text/html')
const body = `<article><h1>Designing Idempotent APIs</h1><p>${'word '.repeat(298)}</p></article>`

test('T26: a Freedium page maps to the Medium canonical with its metadata', () => {
  const location = 'https://freedium-mirror.cfd/https://medium.com/better-programming/designing-idempotent-apis-8f3c2a1b9d4e?source=x'
  const doc = page(`<head><title>Freedium</title><meta name="author" content="Jane Doe">
    <meta property="og:site_name" content="Better Programming"><meta property="article:published_time" content="2026-09-30T08:00:00Z"></head>
    <body>${body}</body>`)
  expect(freediumMedium.extract({ doc, location, config })).toEqual({
    canonicalUrl: 'https://medium.com/p/8f3c2a1b9d4e', source: 'medium', title: 'Designing Idempotent APIs', author: 'Jane Doe',
    siteName: 'Better Programming', wordCount: 301, publishedAt: '2026-09-30T08:00:00Z', originalUrl: location,
  })
})

test('T26: the Medium author URL gives the same canonical', () => {
  const meta = freediumMedium.extract({ doc: page(body), location: 'https://medium.com/@jane/foo-8f3c2a1b9d4e', config })
  expect(meta?.canonicalUrl).toBe('https://medium.com/p/8f3c2a1b9d4e')
})

test('no post id falls back to link rel=canonical, og:title and a[rel=author]', () => {
  const doc = page(`<head><link rel="canonical" href="https://medium.com/some-list/?source=feed">
    <meta property="og:title" content="A list"></head><body><a rel="author" href="/@x">Ann</a><main>${'w '.repeat(10)}</main></body>`)
  expect(freediumMedium.extract({ doc, location: 'https://medium.com/some-list', config })).toMatchObject({
    canonicalUrl: 'https://medium.com/some-list', title: 'A list', author: 'Ann', wordCount: 10,
  })
})
```

`extension/src/adapters/site/generic.test.ts`:

```ts
// @vitest-environment jsdom
import { expect, test } from 'vitest'
import { wireConfig } from '../../../test/fixtures'
import { parseRemoteConfig } from '../../domain/config'
import { generic } from './generic'
import { adapterFor } from './registry'

const config = parseRemoteConfig(wireConfig)
const page = (html: string) => new DOMParser().parseFromString(html, 'text/html')

test('T26: JSON-LD BlogPosting wins over OpenGraph', () => {
  const doc = page(`<head><link rel="canonical" href="https://blog.example.com/p/1/">
    <meta property="og:title" content="OG title"><meta property="og:site_name" content="Example Blog">
    <script type="application/ld+json">{"@type":"BlogPosting","headline":"LD title","author":{"name":"Ann"},
      "datePublished":"2026-09-30","wordCount":1234}</script></head><body><article>short</article></body>`)
  expect(generic.extract({ doc, location: 'https://blog.example.com/p/1/?utm_campaign=z', config })).toEqual({
    canonicalUrl: 'https://blog.example.com/p/1', source: 'web', title: 'LD title', author: 'Ann', siteName: 'Example Blog',
    wordCount: 1234, publishedAt: '2026-09-30', originalUrl: 'https://blog.example.com/p/1/?utm_campaign=z',
  })
})

test('without JSON-LD it uses OpenGraph, meta author and counts words; site defaults to the host', () => {
  const doc = page(`<head><title>Doc title</title><meta property="og:title" content="OG title">
    <meta name="author" content="Bob"><meta property="article:published_time" content="2026-09-29T10:00:00Z"></head>
    <body><nav>menu menu</nav><article>${'w '.repeat(42)}</article></body>`)
  expect(generic.extract({ doc, location: 'https://www.example.com/a', config })).toMatchObject({
    canonicalUrl: 'https://www.example.com/a', title: 'OG title', author: 'Bob', siteName: 'example.com',
    wordCount: 42, publishedAt: '2026-09-29T10:00:00Z',
  })
})

test('falls back to document.title', () => {
  expect(generic.extract({ doc: page('<title>Only title</title><p>x</p>'), location: 'https://e.com/a', config })?.title).toBe('Only title')
})

test('registry returns each adapter by name', () => {
  expect(adapterFor('generic')).toBe(generic)
  expect(adapterFor('freedium-medium').name).toBe('freedium-medium')
})
```

- [ ] **Step 2: Run them to see them fail**

Run: `cd extension && pnpm test adapters/site`
Expected: FAIL, because the `./metadata`, `./freedium-medium`, `./generic` and `./registry` modules cannot be resolved.

- [ ] **Step 3: Implement the adapters**

`extension/src/adapters/site/types.ts`:

```ts
import type { RemoteConfig } from '../../domain/config'
import type { AdapterName } from '../../domain/domains'
import type { ArticleMeta } from '../../domain/reading'

export interface PageContext {
  doc: Document
  // location is the page URL as the browser shows it.
  location: string
  config: RemoteConfig
}

// SiteAdapter reads an article's identity and metadata from a page (spec T26).
export interface SiteAdapter {
  name: AdapterName
  extract(ctx: PageContext): ArticleMeta | null
  contentRoot(doc: Document): Element
}
```

`extension/src/adapters/site/metadata.ts`:

```ts
// DOM readers shared by the site adapters.
import type { RemoteConfig } from '../../domain/config'
import type { ArticleMeta } from '../../domain/reading'
import { normalizeUrl } from '../../domain/url'

// Backend limits (backend/internal/usecase/readsync/validate.go).
const MAX_URL = 2048
const MAX_TITLE = 500
const MAX_NAME = 200
const MAX_WORDS = 200_000
const ARTICLE_TYPES = new Set(['Article', 'BlogPosting', 'NewsArticle'])

export function metaContent(doc: Document, selector: string): string {
  return doc.querySelector(selector)?.getAttribute('content')?.trim() ?? ''
}

export function firstText(doc: Document, selector: string): string {
  return doc.querySelector(selector)?.textContent?.replace(/\s+/g, ' ').trim() ?? ''
}

export function defaultContentRoot(doc: Document): Element {
  return doc.querySelector('article') ?? doc.querySelector('main') ?? doc.body ?? doc.documentElement
}

// countWords counts words per text node, so adjacent blocks (<h1>a</h1><p>b</p>) do not
// merge into one word the way textContent would; script and style text is skipped.
export function countWords(root: Element): number {
  const walker = root.ownerDocument.createTreeWalker(root, 4 /* NodeFilter.SHOW_TEXT */)
  let n = 0
  for (let node = walker.nextNode(); node; node = walker.nextNode()) {
    const tag = node.parentElement?.tagName
    if (tag === 'SCRIPT' || tag === 'STYLE' || tag === 'NOSCRIPT') continue
    n += (node.nodeValue ?? '').split(/\s+/).filter(Boolean).length
  }
  return n
}

// canonicalFromDocument tries link[rel=canonical], og:url, then the location, and returns
// the first that normalizes.
export function canonicalFromDocument(doc: Document, location: string, config: RemoteConfig): string | null {
  const preferHttps = location.startsWith('https:')
  const candidates = [doc.querySelector('link[rel="canonical"]')?.getAttribute('href') ?? '', metaContent(doc, 'meta[property="og:url"]'), location]
  for (const raw of candidates) {
    if (!raw.trim()) continue
    let absolute: string
    try {
      absolute = new URL(raw.trim(), location).toString()
    } catch {
      continue
    }
    const url = normalizeUrl(absolute, { stripQueryParams: config.stripQueryParams, preferHttps })
    if (url) return url
  }
  return null
}

export interface LdArticle {
  headline: string
  author: string
  datePublished: string
  wordCount: number
}

function nodes(value: unknown): Record<string, unknown>[] {
  if (Array.isArray(value)) return value.flatMap(nodes)
  if (typeof value !== 'object' || value === null) return []
  const obj = value as Record<string, unknown>
  return [obj, ...nodes(obj['@graph'])]
}

function isArticle(node: Record<string, unknown>): boolean {
  const t = node['@type']
  return (Array.isArray(t) ? t : [t]).some((x) => typeof x === 'string' && ARTICLE_TYPES.has(x))
}

function authorName(a: unknown): string {
  if (typeof a === 'string') return a.trim()
  if (Array.isArray(a)) return a.length > 0 ? authorName(a[0]) : ''
  if (typeof a === 'object' && a !== null && typeof (a as { name?: unknown }).name === 'string') return ((a as { name: string }).name).trim()
  return ''
}

// jsonLdArticle reads the first Article/BlogPosting/NewsArticle in the page's JSON-LD.
export function jsonLdArticle(doc: Document): LdArticle {
  for (const script of doc.querySelectorAll('script[type="application/ld+json"]')) {
    let parsed: unknown
    try {
      parsed = JSON.parse(script.textContent ?? '')
    } catch {
      continue
    }
    const node = nodes(parsed).find(isArticle)
    if (!node) continue
    const words = Number(node.wordCount)
    return {
      headline: typeof node.headline === 'string' ? node.headline.trim() : '',
      author: authorName(node.author),
      datePublished: typeof node.datePublished === 'string' ? node.datePublished.trim() : '',
      wordCount: Number.isFinite(words) && words > 0 ? Math.floor(words) : 0,
    }
  }
  return { headline: '', author: '', datePublished: '', wordCount: 0 }
}

function cut(s: string, max: number): string {
  const t = s.replace(/\s+/g, ' ').trim()
  return [...t].slice(0, max).join('')
}

// finishMeta applies the backend's limits so neither the article nor its segments get rejected.
export function finishMeta(m: ArticleMeta): ArticleMeta | null {
  if (m.canonicalUrl.length > MAX_URL) return null
  const words = Number.isFinite(m.wordCount) ? Math.floor(m.wordCount) : 0
  return {
    canonicalUrl: m.canonicalUrl,
    source: m.source,
    title: cut(m.title, MAX_TITLE),
    author: cut(m.author, MAX_NAME),
    siteName: cut(m.siteName, MAX_NAME),
    wordCount: Math.min(MAX_WORDS, Math.max(0, words)),
    publishedAt: m.publishedAt.trim(),
    originalUrl: m.originalUrl.length > MAX_URL ? m.canonicalUrl : m.originalUrl,
  }
}
```

`extension/src/adapters/site/freedium-medium.ts`:

```ts
// The Freedium/Medium adapter (spec T26): Medium posts and their Freedium mirrors.
import { mediumCanonical } from '../../domain/medium'
import { canonicalFromDocument, countWords, defaultContentRoot, finishMeta, firstText, metaContent } from './metadata'
import type { SiteAdapter } from './types'

export const freediumMedium: SiteAdapter = {
  name: 'freedium-medium',
  contentRoot: defaultContentRoot,
  extract({ doc, location, config }) {
    const canonicalUrl = mediumCanonical(location, config.freediumHosts) ?? canonicalFromDocument(doc, location, config)
    if (!canonicalUrl) return null
    return finishMeta({
      canonicalUrl,
      source: 'medium',
      title: firstText(doc, 'h1') || metaContent(doc, 'meta[property="og:title"]') || doc.title,
      author: metaContent(doc, 'meta[name="author"]') || firstText(doc, 'a[rel="author"], [data-testid="authorName"]'),
      siteName: metaContent(doc, 'meta[property="og:site_name"]'),
      wordCount: countWords(defaultContentRoot(doc)),
      publishedAt: metaContent(doc, 'meta[property="article:published_time"]'),
      originalUrl: location,
    })
  },
}
```

`extension/src/adapters/site/generic.ts`:

```ts
// The generic adapter (spec T26): any user-enabled domain.
import { canonicalFromDocument, countWords, defaultContentRoot, finishMeta, jsonLdArticle, metaContent } from './metadata'
import type { SiteAdapter } from './types'

export const generic: SiteAdapter = {
  name: 'generic',
  contentRoot: defaultContentRoot,
  extract({ doc, location, config }) {
    const canonicalUrl = canonicalFromDocument(doc, location, config)
    if (!canonicalUrl) return null
    const ld = jsonLdArticle(doc)
    return finishMeta({
      canonicalUrl,
      source: 'web',
      title: ld.headline || metaContent(doc, 'meta[property="og:title"]') || doc.title,
      author: ld.author || metaContent(doc, 'meta[name="author"]'),
      siteName: metaContent(doc, 'meta[property="og:site_name"]') || new URL(canonicalUrl).hostname.replace(/^www\./, ''),
      wordCount: ld.wordCount || countWords(defaultContentRoot(doc)),
      publishedAt: ld.datePublished || metaContent(doc, 'meta[property="article:published_time"]'),
      originalUrl: location,
    })
  },
}
```

`extension/src/adapters/site/registry.ts`:

```ts
import type { AdapterName } from '../../domain/domains'
import { freediumMedium } from './freedium-medium'
import { generic } from './generic'
import type { SiteAdapter } from './types'

const ADAPTERS: Record<AdapterName, SiteAdapter> = { 'freedium-medium': freediumMedium, generic }

export function adapterFor(name: AdapterName): SiteAdapter {
  return ADAPTERS[name]
}
```

- [ ] **Step 4: Run the tests**

Run: `cd extension && pnpm test adapters/site && pnpm test arch && pnpm typecheck`
Expected: PASS. `adapters/site` imports only domain and its own folder.

- [ ] **Step 5: Commit**

```bash
cd /Users/tuandoquoc/personal/reading-chrome-extension
git add extension/src/adapters/site
git commit -m "feat(extension): Freedium/Medium and generic site adapters"
```

---
### Task 9: DOM tracker driver, messaging and the content script

**Files:**
- Create: `extension/src/adapters/page/tracker-driver.ts`, `tracker-driver.test.ts`
- Create: `extension/src/adapters/chrome/messaging.ts`, `messaging.test.ts`
- Modify: `extension/src/entry/content.ts` (replace the stub)

**Interfaces:**
- **Consumes:**
  - From Task 3: `Tracker`, `SegmentDraft`.
  - From Task 2: `readingProgress`, `TrackingConfig`, `ArticleMeta`, `Segment`.
  - From Task 7: `BackgroundRequest`, `BackgroundReply`, `TabRequest`, `CurrentPage`.
  - From Task 8: `adapterFor`.
- **Produces:**
  - `adapters/page/tracker-driver.ts`:
    - `DriverDeps { win; config: TrackingConfig; extract; contentRoot; send; newId; now? }`;
    - `Driver { stop(); current(): ArticleMeta | null; readonly stopped: boolean }`;
    - `startTracking(deps): Driver`;
    - `claimPage(win): boolean`.
  - `adapters/chrome/messaging.ts`:
    - `ExtensionGoneError`;
    - `sendToBackground(msg)`;
    - `serveBackground(handler, log)`;
    - `sendToTab(tabId, msg)`;
    - `serveTabRequests(handler)`.
  - Reply envelope: `{ ok: true; value } | { ok: false; error }`.

**Driver behaviour (spec T27, C23, onboarding §5.4):**
- **Events:**
  - `scroll`, `wheel`, `keydown`, `pointerdown` and `touchstart` count as input.
  - `mousemove` counts as input at most once per second.
  - `visibilitychange` maps to `setVisible`; window `focus`/`blur` map to `setFocused`.
- **Tick (every second):**
  - updates progress from the content root's geometry;
  - checks `location.href` for SPA navigation;
  - calls `tracker.tick`.
- **SPA navigation:** the open segment is sent under the *previous* article. A new session then starts and metadata is re-extracted.
- **Normal flushes:** metadata is re-extracted at each flush, so a page that finishes rendering late still gets its title and word count.
- **`pagehide`:** flush, then stop. A bfcache restore (`pageshow` with `persisted`) starts a fresh driver; the content entry does this.
- **Send failure:** a rejected `send` means the extension is gone. The driver stops quietly (Review Focus 1). The content entry turns every other send failure into a warning.
- **Single tracker:** `claimPage` is set once per page and isolated world, so a second injection does not start a second tracker (Review Focus 2).

- [ ] **Step 1: Write the failing driver tests**

`extension/src/adapters/page/tracker-driver.test.ts`:

```ts
// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest'
import type { TrackingConfig } from '../../domain/config'
import type { ArticleMeta, Segment } from '../../domain/reading'
import { claimPage, startTracking, type Driver } from './tracker-driver'

const config: TrackingConfig = {
  idleSeconds: 60, segmentSeconds: 60, minCreateSeconds: 30, completeProgress: 0.9, completeTimeRatio: 0.3,
  wordsPerMinute: 230, syncIntervalMinutes: 2, syncThresholdSegments: 20,
}

function meta(url: string, title = 'Title'): ArticleMeta {
  return { canonicalUrl: url, source: 'web', title, author: '', siteName: '', wordCount: 100, publishedAt: '', originalUrl: url }
}

describe('startTracking', () => {
  let visibility: DocumentVisibilityState
  let sent: { article: ArticleMeta; segment: Segment }[]
  let send: (article: ArticleMeta, segment: Segment) => Promise<unknown>
  let driver: Driver | null
  let n: number

  beforeEach(() => {
    vi.useFakeTimers({ now: new Date('2026-10-06T03:00:00.000Z') })
    visibility = 'visible'
    Object.defineProperty(document, 'visibilityState', { configurable: true, get: () => visibility })
    document.hasFocus = () => true
    history.replaceState(null, '', '/post/a')
    document.body.innerHTML = '<article>text</article>'
    sent = []
    send = async (article, segment) => { sent.push({ article, segment }) }
    driver = null
    n = 0
  })

  afterEach(() => {
    driver?.stop()
    vi.useRealTimers()
  })

  const start = (extract: () => ArticleMeta | null = () => meta(location.href)) => {
    driver = startTracking({
      win: window, config, extract, contentRoot: () => document.querySelector('article')!,
      send: (a, s) => send(a, s), newId: () => `00000000-0000-4000-8000-${String(++n).padStart(12, '0')}`,
    })
    return driver
  }

  const scrollEvery = (seconds: number, total: number) => {
    for (let t = 1; t <= total; t++) {
      if (t % seconds === 0) window.dispatchEvent(new Event('scroll'))
      vi.advanceTimersByTime(1000)
    }
  }

  test('T27: 150 s of reading → segments of 60, 60 and 30 on pagehide, with ISO times', () => {
    start()
    scrollEvery(10, 150)
    window.dispatchEvent(new Event('pagehide'))
    expect(sent.map((s) => s.segment.activeSeconds)).toEqual([60, 60, 30])
    const first = sent[0]!.segment
    expect(first).toMatchObject({ seq: 0, canonicalUrl: 'http://localhost:3000/post/a' })
    expect(first.startedAt).toBe('2026-10-06T03:00:00.000Z')
    expect(first.endedAt).toBe('2026-10-06T03:01:00.000Z')
    expect(driver!.stopped).toBe(true)
  })

  test('a hidden tab accrues nothing; leaving flushes what was read', () => {
    start()
    vi.advanceTimersByTime(10_000)
    visibility = 'hidden'
    document.dispatchEvent(new Event('visibilitychange'))
    expect(sent.map((s) => s.segment.activeSeconds)).toEqual([10])
    vi.advanceTimersByTime(120_000)
    window.dispatchEvent(new Event('pagehide'))
    expect(sent).toHaveLength(1)
  })

  test('window blur pauses; focus resumes', () => {
    start()
    vi.advanceTimersByTime(5_000)
    window.dispatchEvent(new Event('blur'))
    vi.advanceTimersByTime(60_000)
    window.dispatchEvent(new Event('focus'))
    vi.advanceTimersByTime(5_000)
    window.dispatchEvent(new Event('pagehide'))
    expect(sent.map((s) => s.segment.activeSeconds)).toEqual([5, 5])
  })

  test('mousemove keeps the reader active', () => {
    start()
    for (let t = 1; t <= 120; t++) {
      if (t % 30 === 0) window.dispatchEvent(new MouseEvent('mousemove'))
      vi.advanceTimersByTime(1000)
    }
    window.dispatchEvent(new Event('pagehide'))
    expect(sent.reduce((sum, s) => sum + s.segment.activeSeconds, 0)).toBe(120)
  })

  test('progress comes from the content root geometry', () => {
    const root = document.querySelector('article')!
    root.getBoundingClientRect = () => ({ top: -1400, height: 3000 }) as DOMRect
    Object.defineProperty(window, 'scrollY', { configurable: true, value: 1600 })
    Object.defineProperty(window, 'innerHeight', { configurable: true, value: 800 })
    start()
    vi.advanceTimersByTime(3_000)
    window.dispatchEvent(new Event('pagehide'))
    // rootTop = −1400 + 1600 = 200; (1600 + 800 − 200) / 3000
    expect(sent[0]!.segment.maxProgress).toBeCloseTo(0.7333, 3)
  })

  test('SPA navigation sends the open segment under the previous article and starts a new session', () => {
    start((): ArticleMeta => meta(location.href, location.pathname))
    vi.advanceTimersByTime(10_000)
    history.pushState(null, '', '/post/b')
    vi.advanceTimersByTime(1_000)
    expect(sent).toHaveLength(1)
    expect(sent[0]!.article.canonicalUrl).toBe('http://localhost:3000/post/a')
    expect(sent[0]!.segment.canonicalUrl).toBe('http://localhost:3000/post/a')
    vi.advanceTimersByTime(4_000)
    window.dispatchEvent(new Event('pagehide'))
    const second = sent[1]!
    expect(second.article.title).toBe('/post/b')
    expect(second.segment.seq).toBe(0)
    expect(second.segment.sessionId).not.toBe(sent[0]!.segment.sessionId)
    expect(driver!.current()?.canonicalUrl).toBe('http://localhost:3000/post/b')
  })

  test('metadata is re-read at flush time', () => {
    let title = ''
    start(() => meta(location.href, title))
    title = 'Rendered later'
    vi.advanceTimersByTime(3_000)
    window.dispatchEvent(new Event('pagehide'))
    expect(sent[0]!.article.title).toBe('Rendered later')
  })

  test('a page that is not an article sends nothing', () => {
    start(() => null)
    vi.advanceTimersByTime(90_000)
    window.dispatchEvent(new Event('pagehide'))
    expect(sent).toEqual([])
  })

  test('Review focus 1: stops after the extension context is invalidated', async () => {
    let calls = 0
    send = async () => {
      calls++
      throw new Error('Extension context invalidated.')
    }
    start()
    scrollEvery(10, 60)
    await vi.runOnlyPendingTimersAsync()
    expect(driver!.stopped).toBe(true)
    scrollEvery(10, 120)
    expect(calls).toBe(1)
  })

  test('after stop no event or timer does anything', () => {
    const d = start()
    d.stop()
    window.dispatchEvent(new Event('scroll'))
    vi.advanceTimersByTime(120_000)
    window.dispatchEvent(new Event('pagehide'))
    expect(sent).toEqual([])
  })
})

test('Review focus 2: a second injection does not start a second tracker', () => {
  const win = {} as Window
  expect(claimPage(win)).toBe(true)
  expect(claimPage(win)).toBe(false)
})
```

- [ ] **Step 2: Run it to see it fail**

Run: `cd extension && pnpm test tracker-driver`
Expected: FAIL, because `./tracker-driver` cannot be resolved.

- [ ] **Step 3: Implement the driver**

`extension/src/adapters/page/tracker-driver.ts`:

```ts
// The DOM side of the tracker (spec T27): browser events and a 1 s tick in, segments out.
import type { TrackingConfig } from '../../domain/config'
import { readingProgress } from '../../domain/progress'
import type { ArticleMeta, Segment } from '../../domain/reading'
import { Tracker, type SegmentDraft } from '../../domain/tracker'

export interface DriverDeps {
  win: Window
  config: TrackingConfig
  // extract reads the article from the live page; null means "not an article".
  extract: () => ArticleMeta | null
  contentRoot: () => Element
  // send delivers one segment; a rejection means the extension is gone and tracking stops.
  send: (article: ArticleMeta, segment: Segment) => Promise<unknown>
  newId: () => string
  now?: () => number
}

export interface Driver {
  stop(): void
  current(): ArticleMeta | null
  readonly stopped: boolean
}

const TICK_MS = 1000
const MOUSEMOVE_EVERY_MS = 1000
const INPUT_EVENTS = ['wheel', 'keydown', 'pointerdown', 'touchstart'] as const
const CLAIM = Symbol.for('reading-tracker.claimed')

// claimPage returns true the first time it is called for a window (Review Focus 2).
export function claimPage(win: Window): boolean {
  const w = win as Window & { [CLAIM]?: boolean }
  if (w[CLAIM]) return false
  w[CLAIM] = true
  return true
}

export function startTracking(d: DriverDeps): Driver {
  const { win } = d
  const doc = win.document
  const now = d.now ?? (() => Date.now())
  const tracker = new Tracker({
    idleSeconds: d.config.idleSeconds,
    segmentSeconds: d.config.segmentSeconds,
    now: now(),
    visible: doc.visibilityState === 'visible',
    focused: doc.hasFocus(),
    newId: d.newId,
  })
  let meta = d.extract()
  let href = win.location.href
  let lastMove = Number.NEGATIVE_INFINITY
  let stopped = false

  const emit = (drafts: SegmentDraft[], article: ArticleMeta | null) => {
    if (!article) return
    for (const s of drafts) {
      const segment: Segment = {
        id: s.id, sessionId: s.sessionId, seq: s.seq, canonicalUrl: article.canonicalUrl,
        startedAt: new Date(s.startedAt).toISOString(), endedAt: new Date(s.endedAt).toISOString(),
        activeSeconds: s.activeSeconds, maxProgress: s.maxProgress,
      }
      d.send(article, segment).catch(() => stop())
    }
  }

  // flush re-reads metadata first, so late-rendered titles and word counts are captured.
  const flush = (drafts: SegmentDraft[]) => {
    if (drafts.length === 0) return
    meta = d.extract() ?? meta
    emit(drafts, meta)
  }

  const updateProgress = () => {
    const rect = d.contentRoot().getBoundingClientRect()
    tracker.progress(readingProgress({
      scrollY: win.scrollY, viewportHeight: win.innerHeight, rootTop: rect.top + win.scrollY, rootHeight: rect.height,
    }))
  }

  const onInput = () => {
    tracker.input(now())
  }
  const onScroll = () => {
    updateProgress()
    tracker.input(now())
  }
  const onMouseMove = () => {
    const t = now()
    if (t - lastMove >= MOUSEMOVE_EVERY_MS) {
      lastMove = t
      tracker.input(t)
    }
  }
  const onVisibility = () => flush(tracker.setVisible(doc.visibilityState === 'visible', now()))
  const onFocus = () => flush(tracker.setFocused(true, now()))
  const onBlur = () => flush(tracker.setFocused(false, now()))
  const onPageHide = () => {
    updateProgress()
    flush(tracker.pagehide(now()))
    stop()
  }

  const tick = () => {
    if (win.location.href !== href) {
      href = win.location.href
      emit(tracker.newSession(now()), meta) // the open segment belongs to the previous article
      meta = d.extract()
    }
    updateProgress()
    flush(tracker.tick(now()))
  }

  const listen: [EventTarget, string, EventListener][] = [
    ...INPUT_EVENTS.map((type): [EventTarget, string, EventListener] => [win, type, onInput]),
    [win, 'scroll', onScroll],
    [win, 'mousemove', onMouseMove],
    [doc, 'visibilitychange', onVisibility],
    [win, 'focus', onFocus],
    [win, 'blur', onBlur],
    [win, 'pagehide', onPageHide],
  ]
  for (const [target, type, fn] of listen) target.addEventListener(type, fn, { passive: true, capture: true })
  const timer = win.setInterval(tick, TICK_MS)

  function stop() {
    if (stopped) return
    stopped = true
    win.clearInterval(timer)
    for (const [target, type, fn] of listen) target.removeEventListener(type, fn, { capture: true })
  }

  return {
    stop,
    current: () => meta,
    get stopped() {
      return stopped
    },
  }
}
```

- [ ] **Step 4: Run the driver tests**

Run: `cd extension && pnpm test tracker-driver`
Expected: PASS, 11 tests.

- [ ] **Step 5: Write the failing messaging tests**

`extension/src/adapters/chrome/messaging.test.ts`:

```ts
import { afterEach, describe, expect, test, vi } from 'vitest'
import { ExtensionGoneError, sendToBackground, serveBackground, serveTabRequests } from './messaging'

type Listener = (msg: unknown, sender: unknown, reply: (r: unknown) => void) => boolean | undefined

function fakeChrome(over: { id?: string; sendMessage?: (m: unknown) => Promise<unknown> } = {}) {
  const listeners: Listener[] = []
  const chrome = {
    runtime: {
      id: 'id' in over ? over.id : 'ext-id',
      sendMessage: vi.fn(over.sendMessage ?? (async () => ({ ok: true, value: 'pong' }))),
      onMessage: { addListener: (l: Listener) => listeners.push(l) },
    },
  }
  vi.stubGlobal('chrome', chrome)
  return { chrome, listeners }
}

afterEach(() => {
  vi.unstubAllGlobals()
})

describe('sendToBackground', () => {
  test('unwraps the reply envelope', async () => {
    fakeChrome()
    expect(await sendToBackground({ type: 'sync-now' })).toBe('pong')
  })

  test('an error reply rejects with its message', async () => {
    fakeChrome({ sendMessage: async () => ({ ok: false, error: 'boom' }) })
    await expect(sendToBackground({ type: 'sync-now' })).rejects.toThrow('boom')
  })

  test('an invalidated context rejects with ExtensionGoneError', async () => {
    fakeChrome({ id: undefined })
    await expect(sendToBackground({ type: 'sync-now' })).rejects.toBeInstanceOf(ExtensionGoneError)
    fakeChrome({ sendMessage: async () => { throw new Error('Extension context invalidated.') } })
    await expect(sendToBackground({ type: 'sync-now' })).rejects.toBeInstanceOf(ExtensionGoneError)
  })
})

describe('serveBackground', () => {
  test('replies with the handler value, or the error, and keeps the channel open', async () => {
    const { listeners } = fakeChrome()
    const warn = vi.fn()
    serveBackground(async (m) => {
      if ((m as { type: string }).type === 'bad') throw new Error('nope')
      return 42
    }, { info: () => {}, warn })
    const replies: unknown[] = []
    expect(listeners[0]!({ type: 'ok' }, {}, (r) => replies.push(r))).toBe(true)
    listeners[0]!({ type: 'bad' }, {}, (r) => replies.push(r))
    await vi.waitFor(() => expect(replies).toHaveLength(2))
    expect(replies).toEqual([{ ok: true, value: 42 }, { ok: false, error: 'nope' }])
    expect(warn).toHaveBeenCalled()
  })
})

test('serveTabRequests answers current-page only', () => {
  const { listeners } = fakeChrome()
  serveTabRequests(() => ({ canonicalUrl: 'https://e.com/a' }))
  const replies: unknown[] = []
  expect(listeners[0]!({ type: 'current-page' }, {}, (r) => replies.push(r))).toBe(false)
  expect(listeners[0]!({ type: 'other' }, {}, (r) => replies.push(r))).toBe(false)
  expect(replies).toEqual([{ canonicalUrl: 'https://e.com/a' }])
})
```

- [ ] **Step 6: Run it to see it fail**

Run: `cd extension && pnpm test messaging`
Expected: FAIL, because `./messaging` cannot be resolved.

- [ ] **Step 7: Implement messaging**

`extension/src/adapters/chrome/messaging.ts`:

```ts
// chrome.runtime / chrome.tabs messaging with a { ok, value | error } reply envelope.
import type { Log } from '../../application/ports'
import type { BackgroundReply, BackgroundRequest, CurrentPage, TabRequest } from '../../application/protocol'

export type Reply = { ok: true; value: unknown } | { ok: false; error: string }

// ExtensionGoneError: the extension was reloaded or removed under this page.
export class ExtensionGoneError extends Error {
  override name = 'ExtensionGoneError'
}

function gone(e: unknown): boolean {
  return !chrome.runtime?.id || /context invalidated/i.test(e instanceof Error ? e.message : String(e))
}

function unwrap(reply: Reply | undefined): unknown {
  if (!reply) throw new Error('no reply from the background')
  if (!reply.ok) throw new Error(reply.error)
  return reply.value
}

export async function sendToBackground<M extends BackgroundRequest>(msg: M): Promise<BackgroundReply<M['type']>> {
  if (!chrome.runtime?.id) throw new ExtensionGoneError('extension context invalidated')
  let reply: Reply | undefined
  try {
    reply = (await chrome.runtime.sendMessage(msg)) as Reply | undefined
  } catch (e) {
    if (gone(e)) throw new ExtensionGoneError('extension context invalidated')
    throw e
  }
  return unwrap(reply) as BackgroundReply<M['type']>
}

// serveBackground answers runtime messages in the service worker.
export function serveBackground(handler: (msg: unknown) => Promise<unknown>, log: Log): void {
  chrome.runtime.onMessage.addListener((msg, _sender, sendResponse) => {
    handler(msg).then(
      (value) => sendResponse({ ok: true, value } satisfies Reply),
      (e: unknown) => {
        const error = e instanceof Error ? e.message : String(e)
        log.warn(`background: message failed: ${error}`)
        sendResponse({ ok: false, error } satisfies Reply)
      },
    )
    return true // keep the channel open for the async reply
  })
}

// sendToTab asks the content script in a tab; null when the tab has none.
export async function sendToTab(tabId: number, msg: TabRequest): Promise<CurrentPage | null> {
  try {
    return ((await chrome.tabs.sendMessage(tabId, msg)) as CurrentPage | undefined) ?? null
  } catch {
    return null
  }
}

// serveTabRequests answers the popup's questions in the content script.
export function serveTabRequests(handler: (msg: TabRequest) => CurrentPage): void {
  chrome.runtime.onMessage.addListener((msg, _sender, sendResponse) => {
    if ((msg as { type?: unknown })?.type === 'current-page') sendResponse(handler(msg as TabRequest))
    return false
  })
}
```

- [ ] **Step 8: Replace the content entry**

`extension/src/entry/content.ts`:

```ts
// Content script composition root: ask the background whether and how to track this
// page, then run the tracker driver with the chosen site adapter.
import { ExtensionGoneError, sendToBackground, serveTabRequests } from '../adapters/chrome/messaging'
import { claimPage, startTracking, type Driver } from '../adapters/page/tracker-driver'
import { adapterFor } from '../adapters/site/registry'
import type { ArticleMeta, Segment } from '../domain/reading'

async function main(): Promise<void> {
  if (!claimPage(window)) return
  const plan = await sendToBackground({ type: 'tracking-plan', host: location.hostname }).catch(() => null)
  if (!plan) return
  const adapter = adapterFor(plan.adapter)
  let driver: Driver | null = null

  const send = (article: ArticleMeta, segment: Segment) =>
    sendToBackground({ type: 'record', article, segment }).then(
      () => undefined,
      (e: unknown) => {
        if (e instanceof ExtensionGoneError) throw e // stops the driver
        console.warn('reading-tracker: segment not recorded', e)
      },
    )

  const start = () => {
    driver = startTracking({
      win: window,
      config: plan.config.tracking,
      extract: () => adapter.extract({ doc: document, location: location.href, config: plan.config }),
      contentRoot: () => adapter.contentRoot(document),
      send,
      newId: () => crypto.randomUUID(),
    })
  }

  start()
  // A page restored from the back/forward cache gets a fresh tracker.
  window.addEventListener('pageshow', (e) => {
    if (e.persisted && driver?.stopped) start()
  })
  serveTabRequests(() => ({ canonicalUrl: driver?.current()?.canonicalUrl ?? null }))
}

void main()
```

- [ ] **Step 9: Run everything**

Run: `cd extension && pnpm test && pnpm typecheck && pnpm build && grep -c "import " dist/content.js`
Expected:
- All tests PASS, including the arch test. `entry/content.ts` may use `chrome.` and import every layer, while `adapters/page` touches no `chrome.` and imports no other adapter.
- The build succeeds.
- `grep -c` prints `0`, because the content script is one IIFE with no module imports.

- [ ] **Step 10: Commit**

```bash
cd /Users/tuandoquoc/personal/reading-chrome-extension
git add extension/src/adapters/page extension/src/adapters/chrome/messaging.ts extension/src/adapters/chrome/messaging.test.ts extension/src/entry/content.ts
git commit -m "feat(extension): DOM tracker driver, background messaging and the content script"
```

---
### Task 10: chrome.* adapters and the background service worker

**Files:**
- Create: `extension/src/adapters/chrome/identity.ts`, `permissions.ts`, `scripting.ts`, `alarms.ts`, `tabs.ts`, `device.ts`
- Test: `extension/src/adapters/chrome/chrome-adapters.test.ts`
- Create: `extension/src/adapters/system/system.ts`, `system.test.ts`
- Create: `extension/src/entry/compose.ts`
- Modify: `extension/src/entry/background.ts` (replace the stub)

**Interfaces:**
- **Consumes:**
  - every port from Task 4;
  - every service from Tasks 4, 6 and 7;
  - `serveBackground` from Task 9;
  - `DexieStore` / `ReadingDb` (Task 4), `HttpApi` and `WebCryptoPkce` (Task 5).
- **Produces:**
  - Port implementations:
    - `ChromeAuthFlow implements AuthFlow`;
    - `ChromePermissions implements Permissions`;
    - `ChromeContentScripts implements ContentScripts`;
    - `ChromeScheduler implements Scheduler`.
  - Chrome helpers:
    - `onAlarm(handler)`;
    - `activeTabId()`, `openTab(url)`, `openOptions()`;
    - `deviceName(nav)`.
  - System adapters: `SystemClock implements Clock` and `consoleLog(scope): Log`.
  - `entry/compose.ts`:
    - `Services { store, api, clock, log, config, sync, recorder, session, setup, domains }`;
    - `compose(scope: string): Services`.
  - The background service worker, which:
    - routes messages;
    - runs `startup()` on install and on browser start: refresh config if stale, ensure the sync alarm, set the daily alarm, reconcile content scripts, then sync;
    - on the `sync` alarm, runs `sync.run()`;
    - on the `daily` alarm, refreshes config, prunes local segments and refreshes `/v1/me`.

MV3 note: every `chrome.*.addListener` call in `background.ts` happens synchronously at module top level, so Chrome re-attaches the listeners when it wakes the service worker.

- [ ] **Step 1: Write the failing tests**

`extension/src/adapters/chrome/chrome-adapters.test.ts`:

```ts
import { afterEach, describe, expect, test, vi } from 'vitest'
import { ChromeScheduler, onAlarm } from './alarms'
import { deviceName } from './device'
import { ChromeAuthFlow } from './identity'
import { ChromePermissions } from './permissions'
import { ChromeContentScripts } from './scripting'
import { activeTabId } from './tabs'

afterEach(() => {
  vi.unstubAllGlobals()
})

describe('ChromeContentScripts', () => {
  function fake(registered: string[] = []) {
    const scripting = {
      registerContentScripts: vi.fn(async () => {}),
      updateContentScripts: vi.fn(async () => {}),
      unregisterContentScripts: vi.fn(async () => {}),
      getRegisteredContentScripts: vi.fn(async () => registered.map((id) => ({ id }))),
    }
    vi.stubGlobal('chrome', { scripting })
    return scripting
  }

  test('T31: registers content.js for the domain, persisted across sessions', async () => {
    const s = fake()
    await new ChromeContentScripts().register('generic:example.com', ['https://example.com/*'])
    expect(s.registerContentScripts).toHaveBeenCalledWith([{
      id: 'generic:example.com', matches: ['https://example.com/*'], js: ['content.js'], runAt: 'document_idle', persistAcrossSessions: true,
    }])
  })

  test('re-registering an existing id updates it instead of throwing', async () => {
    const s = fake(['generic:example.com'])
    await new ChromeContentScripts().register('generic:example.com', ['https://example.com/*'])
    expect(s.registerContentScripts).not.toHaveBeenCalled()
    expect(s.updateContentScripts).toHaveBeenCalledTimes(1)
  })

  test('unregister skips ids Chrome does not know (it would throw)', async () => {
    const s = fake(['generic:a.com'])
    await new ChromeContentScripts().unregister(['generic:a.com', 'generic:b.com'])
    expect(s.unregisterContentScripts).toHaveBeenCalledWith({ ids: ['generic:a.com'] })
    await new ChromeContentScripts().unregister(['generic:b.com'])
    expect(s.unregisterContentScripts).toHaveBeenCalledTimes(1)
  })

  test('registeredIds lists the ids', async () => {
    fake(['x', 'y'])
    expect(await new ChromeContentScripts().registeredIds()).toEqual(['x', 'y'])
  })
})

test('ChromeScheduler creates a repeating alarm; onAlarm passes the name', async () => {
  const listeners: ((a: { name: string }) => void)[] = []
  const alarms = { create: vi.fn(async () => {}), onAlarm: { addListener: (l: (a: { name: string }) => void) => listeners.push(l) } }
  vi.stubGlobal('chrome', { alarms })
  await new ChromeScheduler().every('sync', 2)
  expect(alarms.create).toHaveBeenCalledWith('sync', { periodInMinutes: 2, delayInMinutes: 2 })
  const seen: string[] = []
  onAlarm((name) => seen.push(name))
  listeners[0]!({ name: 'daily' })
  expect(seen).toEqual(['daily'])
})

test('ChromeAuthFlow uses the chromiumapp redirect and an interactive window', async () => {
  const identity = {
    getRedirectURL: () => 'https://abc.chromiumapp.org/',
    launchWebAuthFlow: vi.fn(async () => 'https://abc.chromiumapp.org/?code=x'),
  }
  vi.stubGlobal('chrome', { identity })
  const flow = new ChromeAuthFlow()
  expect(flow.redirectUrl()).toBe('https://abc.chromiumapp.org/')
  expect(await flow.launch('https://api/start')).toBe('https://abc.chromiumapp.org/?code=x')
  expect(identity.launchWebAuthFlow).toHaveBeenCalledWith({ url: 'https://api/start', interactive: true })
  identity.launchWebAuthFlow.mockResolvedValueOnce(undefined as unknown as string)
  await expect(flow.launch('https://api/start')).rejects.toThrow()
})

test('ChromePermissions passes origins through', async () => {
  const permissions = { request: vi.fn(async () => true), remove: vi.fn(async () => true) }
  vi.stubGlobal('chrome', { permissions })
  expect(await new ChromePermissions().request(['https://e.com/*'])).toBe(true)
  expect(permissions.request).toHaveBeenCalledWith({ origins: ['https://e.com/*'] })
  await new ChromePermissions().remove(['https://e.com/*'])
  expect(permissions.remove).toHaveBeenCalledWith({ origins: ['https://e.com/*'] })
})

test('activeTabId returns the id or null', async () => {
  const tabs = { query: vi.fn(async () => [{ id: 7 }]) }
  vi.stubGlobal('chrome', { tabs })
  expect(await activeTabId()).toBe(7)
  tabs.query.mockResolvedValueOnce([])
  expect(await activeTabId()).toBeNull()
})

describe('deviceName', () => {
  test.each([
    [{ userAgentData: { platform: 'macOS' }, userAgent: '' }, 'Chrome on macOS'],
    [{ userAgent: 'Mozilla/5.0 (Windows NT 10.0; Win64; x64) Chrome/130' }, 'Chrome on Windows'],
    [{ userAgent: 'Mozilla/5.0 (X11; Linux x86_64) Chrome/130' }, 'Chrome on Linux'],
    [{ userAgent: 'Mozilla/5.0 (X11; CrOS x86_64 14541.0.0) Chrome/130' }, 'Chrome on ChromeOS'],
    [{ userAgent: 'something odd' }, 'Chrome'],
  ])('%j → %s', (nav, want) => {
    expect(deviceName(nav as unknown as Navigator)).toBe(want)
  })
})
```

`extension/src/adapters/system/system.test.ts`:

```ts
import { expect, test, vi } from 'vitest'
import { consoleLog, SystemClock } from './system'

test('SystemClock reports now and an IANA time zone', () => {
  const c = new SystemClock()
  expect(Math.abs(c.now().getTime() - Date.now())).toBeLessThan(1000)
  expect(() => new Intl.DateTimeFormat('en', { timeZone: c.timeZone() })).not.toThrow()
})

test('consoleLog prefixes the scope', () => {
  const warn = vi.spyOn(console, 'warn').mockImplementation(() => {})
  consoleLog('background').warn('sync failed', 1)
  expect(warn).toHaveBeenCalledWith('[reading-tracker:background] sync failed', 1)
})
```

- [ ] **Step 2: Run them to see them fail**

Run: `cd extension && pnpm test chrome-adapters system`
Expected: FAIL, because the `./alarms`, `./device`, `./identity`, `./permissions`, `./scripting`, `./tabs` and `./system` modules cannot be resolved.

- [ ] **Step 3: Implement the adapters**

`extension/src/adapters/chrome/identity.ts`:

```ts
import type { AuthFlow } from '../../application/ports'

// ChromeAuthFlow runs OAuth in a Chrome-owned window (spec T29). The redirect is
// https://<extension-id>.chromiumapp.org/, which the backend allow-lists (§2.1).
export class ChromeAuthFlow implements AuthFlow {
  redirectUrl(): string {
    return chrome.identity.getRedirectURL()
  }

  async launch(url: string): Promise<string> {
    const redirect = await chrome.identity.launchWebAuthFlow({ url, interactive: true })
    if (!redirect) throw new Error('auth flow ended without a redirect')
    return redirect
  }
}
```

`extension/src/adapters/chrome/permissions.ts`:

```ts
import type { Permissions } from '../../application/ports'

// ChromePermissions requests optional host permissions; call request() from a user gesture.
export class ChromePermissions implements Permissions {
  request(origins: string[]): Promise<boolean> {
    return chrome.permissions.request({ origins })
  }

  remove(origins: string[]): Promise<boolean> {
    return chrome.permissions.remove({ origins })
  }
}
```

`extension/src/adapters/chrome/scripting.ts`:

```ts
import type { ContentScripts } from '../../application/ports'

// ChromeContentScripts registers the content script on user-enabled domains (spec T31).
export class ChromeContentScripts implements ContentScripts {
  constructor(private readonly file = 'content.js') {}

  async register(id: string, matches: string[]): Promise<void> {
    const script = { id, matches, js: [this.file], runAt: 'document_idle' as const, persistAcrossSessions: true }
    if ((await this.registeredIds()).includes(id)) await chrome.scripting.updateContentScripts([script])
    else await chrome.scripting.registerContentScripts([script])
  }

  async unregister(ids: string[]): Promise<void> {
    const known = new Set(await this.registeredIds())
    const present = ids.filter((id) => known.has(id))
    if (present.length > 0) await chrome.scripting.unregisterContentScripts({ ids: present })
  }

  async registeredIds(): Promise<string[]> {
    return (await chrome.scripting.getRegisteredContentScripts()).map((s) => s.id)
  }
}
```

`extension/src/adapters/chrome/alarms.ts`:

```ts
import type { Scheduler } from '../../application/ports'

export class ChromeScheduler implements Scheduler {
  async every(name: string, periodMinutes: number): Promise<void> {
    await chrome.alarms.create(name, { periodInMinutes: periodMinutes, delayInMinutes: periodMinutes })
  }
}

// onAlarm must be called at service-worker top level so Chrome re-attaches it on wake-up.
export function onAlarm(handler: (name: string) => void): void {
  chrome.alarms.onAlarm.addListener((alarm) => handler(alarm.name))
}
```

`extension/src/adapters/chrome/tabs.ts`:

```ts
// activeTabId is the focused tab of the current window (no "tabs" permission needed for the id).
export async function activeTabId(): Promise<number | null> {
  const [tab] = await chrome.tabs.query({ active: true, currentWindow: true })
  return tab?.id ?? null
}

export async function openTab(url: string): Promise<void> {
  await chrome.tabs.create({ url })
}

export async function openOptions(): Promise<void> {
  await chrome.runtime.openOptionsPage()
}
```

`extension/src/adapters/chrome/device.ts`:

```ts
// deviceName labels this browser in the user's device list (spec §2.3 device_name).
export function deviceName(nav: Navigator): string {
  const hinted = (nav as Navigator & { userAgentData?: { platform?: string } }).userAgentData?.platform
  if (hinted) return `Chrome on ${hinted}`
  const ua = nav.userAgent ?? ''
  const platform = /CrOS/.test(ua) ? 'ChromeOS'
    : /Windows/.test(ua) ? 'Windows'
      : /Mac OS X|Macintosh/.test(ua) ? 'macOS'
        : /Android/.test(ua) ? 'Android'
          : /Linux/.test(ua) ? 'Linux'
            : ''
  return platform ? `Chrome on ${platform}` : 'Chrome'
}
```

`extension/src/adapters/system/system.ts`:

```ts
import type { Clock, Log } from '../../application/ports'

export class SystemClock implements Clock {
  now(): Date {
    return new Date()
  }

  timeZone(): string {
    return Intl.DateTimeFormat().resolvedOptions().timeZone
  }
}

// consoleLog never receives tokens or bodies: callers log ids, reasons and counts only (C13).
export function consoleLog(scope: string): Log {
  const prefix = `[reading-tracker:${scope}]`
  return {
    info: (message, ...details) => console.info(`${prefix} ${message}`, ...details),
    warn: (message, ...details) => console.warn(`${prefix} ${message}`, ...details),
  }
}
```

- [ ] **Step 4: Run the adapter tests**

Run: `cd extension && pnpm test chrome-adapters system && pnpm test arch`
Expected: PASS. Only `adapters/chrome` uses `chrome.`, and `adapters/system` imports only application ports.

- [ ] **Step 5: Write the composition root and the service worker**

`extension/src/entry/compose.ts`:

```ts
// compose builds every service from the real adapters. Background, popup and options each
// call it; they share one IndexedDB, so they see the same data.
import { ChromeAuthFlow } from '../adapters/chrome/identity'
import { ChromePermissions } from '../adapters/chrome/permissions'
import { ChromeContentScripts } from '../adapters/chrome/scripting'
import { ChromeScheduler } from '../adapters/chrome/alarms'
import { deviceName } from '../adapters/chrome/device'
import { WebCryptoPkce } from '../adapters/crypto/pkce'
import { HttpApi } from '../adapters/http/api-client'
import { DexieStore, ReadingDb } from '../adapters/storage/dexie-store'
import { consoleLog, SystemClock } from '../adapters/system/system'
import { ConfigService } from '../application/config-service'
import { DomainService } from '../application/domains'
import type { Api, Clock, LocalStore, Log } from '../application/ports'
import { RecordService } from '../application/record'
import { SessionService } from '../application/session'
import { SetupService } from '../application/setup'
import { SyncService } from '../application/sync'

export interface Services {
  store: LocalStore
  api: Api
  clock: Clock
  log: Log
  config: ConfigService
  sync: SyncService
  recorder: RecordService
  session: SessionService
  setup: SetupService
  domains: DomainService
}

export function compose(scope: string): Services {
  const apiBase = import.meta.env.VITE_API_BASE as string
  const store = new DexieStore(new ReadingDb())
  const api = new HttpApi({ baseUrl: apiBase })
  const clock = new SystemClock()
  const log = consoleLog(scope)
  const config = new ConfigService({ store, api, clock, scheduler: new ChromeScheduler(), log })
  const sync = new SyncService({ store, api, clock, log })
  const syncSoon = () => {
    void sync.run()
  }
  return {
    store, api, clock, log, config, sync,
    recorder: new RecordService({ store, clock, config: () => config.current(), syncSoon, log }),
    session: new SessionService({
      store, api, authFlow: new ChromeAuthFlow(), pkce: new WebCryptoPkce(), deviceName: () => deviceName(navigator), log, afterConnect: syncSoon,
    }),
    setup: new SetupService({ store, api }),
    domains: new DomainService({ store, permissions: new ChromePermissions(), scripts: new ChromeContentScripts() }),
  }
}
```

`extension/src/entry/background.ts`:

```ts
// Background service worker composition root (spec T28): the only place that syncs.
import { ChromeScheduler, onAlarm } from '../adapters/chrome/alarms'
import { serveBackground } from '../adapters/chrome/messaging'
import { SYNC_ALARM } from '../application/config-service'
import { createRouter } from '../application/router'
import { compose } from './compose'

const DAILY_ALARM = 'daily'
const DAY_MINUTES = 24 * 60

const s = compose('background')

serveBackground(createRouter(s), s.log)

async function startup(): Promise<void> {
  await s.config.refreshIfStale()
  await s.config.ensureSchedule()
  await new ChromeScheduler().every(DAILY_ALARM, DAY_MINUTES)
  await s.domains.reconcile()
  await s.sync.run()
}

function run(task: () => Promise<unknown>, what: string): void {
  task().catch((e: unknown) => s.log.warn(`${what} failed: ${e instanceof Error ? e.message : String(e)}`))
}

chrome.runtime.onInstalled.addListener(() => run(startup, 'startup'))
chrome.runtime.onStartup.addListener(() => run(startup, 'startup'))

onAlarm((name) => {
  if (name === SYNC_ALARM) run(() => s.sync.run(), 'sync')
  if (name === DAILY_ALARM) {
    run(async () => {
      await s.config.refreshIfStale()
      await s.sync.prune()
      await s.session.refresh()
    }, 'daily')
  }
})
```

- [ ] **Step 6: Verify the whole extension so far**

Run: `cd extension && pnpm check`
Expected:
- `typecheck` is clean.
- All tests pass.
- Both builds succeed and `dist/background.js` exists.

- [ ] **Step 7: Commit**

```bash
cd /Users/tuandoquoc/personal/reading-chrome-extension
git add extension/src/adapters/chrome extension/src/adapters/system extension/src/entry
git commit -m "feat(extension): chrome adapters and the background service worker"
```

---
### Task 11: Popup

**Files:**
- Create: `extension/src/application/popup-model.ts`, `popup-model.test.ts`
- Create: `extension/src/ui/popup/messages.ts`, `Popup.tsx`, `Popup.test.tsx`, `PopupApp.tsx`, `PopupApp.test.tsx`, `popup.css`
- Modify: `extension/src/entry/popup.tsx` (replace the stub), `extension/popup.html` (add the stylesheet)

**Interfaces:**
- **Consumes:**
  - Task 7: `SessionState`, `ConnectResult`.
  - Task 4: `LocalStore`, `Api`, `Clock`.
  - Task 2: `todaySeconds`, `roundMinutes`, `LocalArticle`.
  - Task 9: `sendToBackground`, `sendToTab`.
  - Task 1: React 19, `@testing-library/react`.
  - Task 10: `activeTabId`, `openTab`, `openOptions`, `compose`.
- **Produces:**
  - `application/popup-model.ts`:
    - `ArticleView { url; title; minutes; progressPercent; completed: boolean | null; notionUrl: string | null }`;
    - `PopupModel`, a union over `kind` (`disconnected | needs_setup | revoked | active`);
    - `buildPopupModel(deps, session, currentUrl): Promise<PopupModel>`.
  - `ui/popup/messages.ts`: `connectMessage(reason): string`.
  - `ui/popup/Popup.tsx`:
    - `PopupActions { connect(); openOptions(); openUrl(url) }`;
    - `<Popup model actions note? />`, a pure view of one `PopupModel`.
  - `ui/popup/PopupApp.tsx`:
    - `PopupAppProps { load(): Promise<PopupModel>; refresh(): Promise<void>; connect(): Promise<ConnectResult>; openOptions(); openUrl(url) }`;
    - `<PopupApp {...props} />`, which loads the model, refreshes `/v1/me`, reloads, and turns a failed connect into a note.

**Popup states (spec T30):**
- `disconnected`, or connected with no installation: "Connect Notion".
- `needs_setup`: "Finish setup", which opens Options.
- `revoked`: "Reconnect".
- `active`, which shows:
  - today's minutes: `roundMinutes(Σ active_seconds of local segments that started today in the local zone)`;
  - the current tab's article: title, minutes, progress, ✓ when completed;
  - the last 5 articles;
  - "Open library in Notion" (`library_url`).

Server totals from `POST /v1/articles/status` (§2.9) enrich the views when the backend is reachable. They include reading on other devices, and supply completed and the Notion link. When the backend is not reachable, the local values are shown.

- [ ] **Step 1: Write the failing model test**

`extension/src/application/popup-model.test.ts`:

```ts
import { beforeEach, describe, expect, test } from 'vitest'
import { FakeApi, FixedClock, INSTALLATION, MemoryStore } from '../../test/fakes'
import { record, segment } from '../../test/store-contract'
import { roundMinutes } from '../domain/reading'
import { NetworkError } from './errors'
import { buildPopupModel } from './popup-model'
import type { SessionState } from './session'

const connected = (status: string, extra: object = {}): SessionState => ({
  kind: 'connected', user: { id: 'u1', name: 'T' }, installation: { ...INSTALLATION, status, ...extra },
})

describe('buildPopupModel', () => {
  let store: MemoryStore
  // 10:00 UTC is 17:00 in Ho Chi Minh, so "today" is 2026-10-06 local.
  const clock = new FixedClock(new Date('2026-10-06T10:00:00.000Z'), 'Asia/Ho_Chi_Minh')

  beforeEach(async () => {
    store = new MemoryStore()
    await store.setKv('device_token', 'rt_x')
  })

  const build = (session: SessionState, current: string | null = null, api = new FakeApi()) =>
    buildPopupModel({ store, api, clock }, session, current)

  test('T30: each installation status has its own state', async () => {
    expect(await build({ kind: 'disconnected' })).toEqual({ kind: 'disconnected' })
    expect(await build({ kind: 'connected', user: null, installation: null })).toEqual({ kind: 'disconnected' })
    expect(await build(connected('needs_setup', { setupError: 'template_not_found' }))).toEqual({
      kind: 'needs_setup', workspaceName: "Tuan's Notion", setupError: 'template_not_found',
    })
    expect(await build(connected('revoked'))).toEqual({ kind: 'revoked', workspaceName: "Tuan's Notion" })
    expect((await build(connected('active'))).kind).toBe('active')
  })

  test("T30: today's minutes are today's local segments / 60", async () => {
    await record(store, segment('yesterday', { startedAt: '2026-10-05T16:59:00.000Z', activeSeconds: 600 })) // 23:59 local, 5th
    await record(store, segment('a', { startedAt: '2026-10-05T17:00:00.000Z', activeSeconds: 125 })) // 00:00 local, 6th
    await record(store, segment('b', { startedAt: '2026-10-06T09:00:00.000Z', activeSeconds: 200 }))
    const m = await build(connected('active'))
    expect(m.kind === 'active' && m.todayMinutes).toBe(roundMinutes(325))
  })

  test('current article, last 5 and the library link; server status enriches them', async () => {
    for (let i = 0; i < 7; i++) {
      await record(store, segment(`s${i}`, {
        canonicalUrl: `https://e.com/${i}`, activeSeconds: 60 * (i + 1), maxProgress: 0.1 * i,
        endedAt: `2026-10-06T0${i}:00:00.000Z`,
      }), { canonicalUrl: '', source: 'web', title: `Article ${i}`, author: '', siteName: '', wordCount: 0, publishedAt: '', originalUrl: '' })
    }
    const api = new FakeApi({
      articlesStatus: async () => ({
        'https://e.com/6': { totalSeconds: 900, maxProgress: 0.95, sessions: 3, completed: true, lastReadAt: '', notionUrl: 'https://www.notion.so/6' },
      }),
    })
    const m = await build(connected('active'), 'https://e.com/6', api)
    if (m.kind !== 'active') throw new Error(m.kind)
    expect(m.current).toEqual({
      url: 'https://e.com/6', title: 'Article 6', minutes: 15, progressPercent: 95, completed: true, notionUrl: 'https://www.notion.so/6',
    })
    expect(m.recent.map((a) => a.title)).toEqual(['Article 6', 'Article 5', 'Article 4', 'Article 3', 'Article 2'])
    expect(m.recent[1]).toMatchObject({ minutes: 6, progressPercent: 50, completed: null, notionUrl: null })
    expect(m.libraryUrl).toBe('https://www.notion.so/lib')
    expect(m.unsynced).toBe(7)
    expect(api.calls.find((c) => c.method === 'articlesStatus')?.args[1]).toEqual([
      'https://e.com/6', 'https://e.com/5', 'https://e.com/4', 'https://e.com/3', 'https://e.com/2',
    ])
  })

  test('offline: local values only', async () => {
    await record(store, segment('a'))
    const api = new FakeApi({ articlesStatus: async () => { throw new NetworkError('offline') } })
    const m = await build(connected('active'), 'https://medium.com/p/8f3c2a1b9d4e', api)
    if (m.kind !== 'active') throw new Error(m.kind)
    expect(m.current).toMatchObject({ minutes: 1, progressPercent: 50, completed: null })
  })

  test('a page read only on another device still shows from the server', async () => {
    const api = new FakeApi({
      articlesStatus: async () => ({ 'https://e.com/x': { totalSeconds: 120, maxProgress: 0.2, sessions: 1, completed: false, lastReadAt: '', notionUrl: null } }),
    })
    const m = await build(connected('active'), 'https://e.com/x', api)
    expect(m.kind === 'active' && m.current).toEqual({
      url: 'https://e.com/x', title: 'https://e.com/x', minutes: 2, progressPercent: 20, completed: false, notionUrl: null,
    })
  })

  test('no current page → no current view, and no status call when nothing is listed', async () => {
    const api = new FakeApi()
    const m = await build(connected('active'), null, api)
    expect(m.kind === 'active' && m.current).toBeNull()
    expect(api.count('articlesStatus')).toBe(0)
  })
})
```

- [ ] **Step 2: Run it to see it fail**

Run: `cd extension && pnpm test popup-model`
Expected: FAIL, because `./popup-model` cannot be resolved.

- [ ] **Step 3: Implement the model**

`extension/src/application/popup-model.ts`:

```ts
// The popup's view model (spec T30), built from local data plus the server's article status.
import { roundMinutes, todaySeconds, type LocalArticle } from '../domain/reading'
import type { Api, Clock, LocalStore } from './ports'
import type { SessionState } from './session'
import type { ArticleStatus } from './types'

export interface ArticleView {
  url: string
  title: string
  minutes: number
  progressPercent: number
  completed: boolean | null
  notionUrl: string | null
}

export type PopupModel =
  | { kind: 'disconnected' }
  | { kind: 'needs_setup'; workspaceName: string; setupError: string }
  | { kind: 'revoked'; workspaceName: string }
  | {
      kind: 'active'
      workspaceName: string
      todayMinutes: number
      current: ArticleView | null
      recent: ArticleView[]
      libraryUrl: string | null
      unsynced: number
    }

export interface PopupDeps {
  store: LocalStore
  api: Api
  clock: Clock
}

const RECENT = 5
// Look back far enough to cover "today" in any time zone.
const TODAY_WINDOW_MS = 36 * 60 * 60 * 1000

function view(url: string, local: LocalArticle | undefined, server: ArticleStatus | undefined): ArticleView {
  const seconds = Math.max(local?.totalSeconds ?? 0, server?.totalSeconds ?? 0)
  const progress = Math.max(local?.maxProgress ?? 0, server?.maxProgress ?? 0)
  return {
    url,
    title: local?.title || url,
    minutes: roundMinutes(seconds),
    progressPercent: Math.round(progress * 100),
    completed: server ? server.completed : null,
    notionUrl: server?.notionUrl ?? null,
  }
}

export async function buildPopupModel(deps: PopupDeps, session: SessionState, currentUrl: string | null): Promise<PopupModel> {
  if (session.kind === 'disconnected' || !session.installation) return { kind: 'disconnected' }
  const inst = session.installation
  if (inst.status === 'revoked') return { kind: 'revoked', workspaceName: inst.workspaceName }
  if (inst.status !== 'active') return { kind: 'needs_setup', workspaceName: inst.workspaceName, setupError: inst.setupError }

  const { store, api, clock } = deps
  const now = clock.now()
  const todays = await store.segmentsSince(new Date(now.getTime() - TODAY_WINDOW_MS).toISOString())
  const recent = await store.recentArticles(RECENT)
  const current = currentUrl ? (await store.articles([currentUrl]))[0] : undefined
  const urls = [...new Set([...(currentUrl ? [currentUrl] : []), ...recent.map((a) => a.canonicalUrl)])]

  let status: Record<string, ArticleStatus> = {}
  const token = await store.getKv('device_token')
  if (token && urls.length > 0) {
    try {
      status = await api.articlesStatus(token, urls)
    } catch {
      // offline or failing backend: local values only
    }
  }

  const currentView = currentUrl && (current || status[currentUrl]) ? view(currentUrl, current, status[currentUrl]) : null
  return {
    kind: 'active',
    workspaceName: inst.workspaceName,
    todayMinutes: roundMinutes(todaySeconds(todays, now, clock.timeZone())),
    current: currentView,
    recent: recent.map((a) => view(a.canonicalUrl, a, status[a.canonicalUrl])),
    libraryUrl: inst.libraryUrl,
    unsynced: await store.countUnsynced(),
  }
}
```

- [ ] **Step 4: Run the model test**

Run: `cd extension && pnpm test popup-model`
Expected: PASS, 6 tests.

- [ ] **Step 5: Write the failing view tests**

`extension/src/ui/popup/Popup.test.tsx`:

```tsx
// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest'
import type { PopupModel } from '../../application/popup-model'
import { connectMessage } from './messages'
import { Popup, type PopupActions } from './Popup'

afterEach(cleanup)

describe('<Popup>', () => {
  let actions: PopupActions

  beforeEach(() => {
    actions = { connect: vi.fn(), openOptions: vi.fn(), openUrl: vi.fn() }
  })

  test.each([
    [{ kind: 'disconnected' }, 'Connect Notion', 'connect'],
    [{ kind: 'needs_setup', workspaceName: 'W', setupError: 'template_not_found' }, 'Finish setup', 'openOptions'],
    [{ kind: 'revoked', workspaceName: 'W' }, 'Reconnect', 'connect'],
  ] as const)('T30: %j shows "%s"', (model, label, action) => {
    render(<Popup model={model as PopupModel} actions={actions} />)
    fireEvent.click(screen.getByRole('button', { name: label }))
    expect(actions[action as keyof PopupActions]).toHaveBeenCalledTimes(1)
  })

  test('needs_setup explains the setup error', () => {
    render(<Popup model={{ kind: 'needs_setup', workspaceName: 'W', setupError: 'database_missing:Daily Log' }} actions={actions} />)
    expect(screen.getByText(/database_missing:Daily Log/)).toBeTruthy()
  })

  const active: PopupModel = {
    kind: 'active', workspaceName: 'W', todayMinutes: 42, libraryUrl: 'https://www.notion.so/lib', unsynced: 3,
    current: { url: 'https://e.com/a', title: 'Current one', minutes: 7, progressPercent: 64, completed: true, notionUrl: 'https://www.notion.so/a' },
    recent: [
      { url: 'https://e.com/a', title: 'Current one', minutes: 7, progressPercent: 64, completed: true, notionUrl: 'https://www.notion.so/a' },
      { url: 'https://e.com/b', title: 'Older', minutes: 2, progressPercent: 10, completed: null, notionUrl: null },
    ],
  }

  test("active shows today's minutes, the current article and recent reads", () => {
    const { container } = render(<Popup model={active} actions={actions} />)
    expect(container.querySelector('[data-role=today]')?.textContent).toBe('42 min')
    const current = container.querySelector('[data-role=current]')!
    expect(current.textContent).toContain('Current one')
    expect(current.textContent).toContain('7 min · 64% · ✓')
    expect(container.querySelectorAll('[data-role=recent] li')).toHaveLength(2)
    expect(screen.getByText('3 segments waiting to sync')).toBeTruthy()
  })

  test('recent items open the Notion page when there is one, else the article', () => {
    const { container } = render(<Popup model={active} actions={actions} />)
    const links = container.querySelectorAll<HTMLButtonElement>('[data-role=recent] li button')
    fireEvent.click(links[0]!)
    fireEvent.click(links[1]!)
    expect(actions.openUrl).toHaveBeenNthCalledWith(1, 'https://www.notion.so/a')
    expect(actions.openUrl).toHaveBeenNthCalledWith(2, 'https://e.com/b')
  })

  test('the library button opens library_url and is hidden without one', () => {
    const { rerender } = render(<Popup model={active} actions={actions} />)
    fireEvent.click(screen.getByRole('button', { name: 'Open library in Notion' }))
    expect(actions.openUrl).toHaveBeenCalledWith('https://www.notion.so/lib')
    rerender(<Popup model={{ ...active, libraryUrl: null, current: null, recent: [], unsynced: 0 }} actions={actions} />)
    expect(screen.queryByRole('button', { name: 'Open library in Notion' })).toBeNull()
    expect(screen.getByText(/Nothing read yet/)).toBeTruthy()
  })

  test('a note is announced', () => {
    render(<Popup model={{ kind: 'disconnected' }} actions={actions} note={connectMessage('access_denied')} />)
    expect(screen.getByRole('status').textContent).toBe('Notion access was not granted.')
  })

  test('page titles render as text, never HTML', () => {
    const evil = { ...active, recent: [{ ...active.recent[1]!, title: '<img src=x onerror=alert(1)>' }] }
    const { container } = render(<Popup model={evil} actions={actions} />)
    expect(container.querySelector('img')).toBeNull()
  })
})

test('connectMessage maps reasons', () => {
  expect(connectMessage('cancelled')).toBe('Connection cancelled.')
  expect(connectMessage('network_error')).toBe("Can't reach the server. Try again.")
  expect(connectMessage('invalid_grant')).toBe('Connection failed (invalid_grant). Try again.')
})
```

`extension/src/ui/popup/PopupApp.test.tsx`:

```tsx
// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, expect, test, vi } from 'vitest'
import type { PopupModel } from '../../application/popup-model'
import type { ConnectResult } from '../../application/session'
import { PopupApp, type PopupAppProps } from './PopupApp'

afterEach(cleanup)

function props(over: Partial<PopupAppProps> = {}): PopupAppProps {
  return {
    load: vi.fn(async (): Promise<PopupModel> => ({ kind: 'disconnected' })),
    refresh: vi.fn(async () => {}),
    connect: vi.fn(async (): Promise<ConnectResult> => ({ ok: false, reason: 'cancelled' })),
    openOptions: vi.fn(),
    openUrl: vi.fn(),
    ...over,
  }
}

test('loads the model, refreshes /v1/me, then reloads', async () => {
  const p = props()
  render(<PopupApp {...p} />)
  expect(await screen.findByRole('button', { name: 'Connect Notion' })).toBeTruthy()
  await vi.waitFor(() => expect(p.load).toHaveBeenCalledTimes(2))
  expect(p.refresh).toHaveBeenCalledTimes(1)
})

test('a failed connect shows why and reloads', async () => {
  const p = props()
  render(<PopupApp {...p} />)
  fireEvent.click(await screen.findByRole('button', { name: 'Connect Notion' }))
  expect((await screen.findByRole('status')).textContent).toBe('Connection cancelled.')
  await vi.waitFor(() => expect(p.load).toHaveBeenCalledTimes(3))
})

test('a successful connect shows the new state', async () => {
  let connected = false
  const p = props({
    load: vi.fn(async (): Promise<PopupModel> => connected
      ? { kind: 'needs_setup', workspaceName: 'W', setupError: '' }
      : { kind: 'disconnected' }),
    connect: vi.fn(async (): Promise<ConnectResult> => {
      connected = true
      return { ok: true, installation: null }
    }),
  })
  render(<PopupApp {...p} />)
  fireEvent.click(await screen.findByRole('button', { name: 'Connect Notion' }))
  expect(await screen.findByRole('button', { name: 'Finish setup' })).toBeTruthy()
  expect(screen.queryByRole('status')).toBeNull()
})

test('a load failure is reported instead of a blank popup', async () => {
  render(<PopupApp {...props({ load: vi.fn(async () => { throw new Error('db closed') }) })} />)
  expect((await screen.findByRole('status')).textContent).toBe("Couldn't load your reading. Close and reopen the popup.")
})
```

- [ ] **Step 6: Run them to see them fail**

Run: `cd extension && pnpm test ui/popup`
Expected: FAIL, because `./messages`, `./Popup` and `./PopupApp` cannot be resolved.

- [ ] **Step 7: Implement the components and the styles**

`extension/src/ui/popup/messages.ts`:

```ts
// connectMessage turns a ConnectResult reason into a sentence for the user.
export function connectMessage(reason: string): string {
  switch (reason) {
    case 'cancelled':
      return 'Connection cancelled.'
    case 'access_denied':
      return 'Notion access was not granted.'
    case 'network_error':
      return "Can't reach the server. Try again."
    default:
      return `Connection failed (${reason}). Try again.`
  }
}
```

`extension/src/ui/popup/Popup.tsx`:

```tsx
// <Popup> draws one PopupModel (spec T30). It holds no state: PopupApp owns loading.
import type { ArticleView, PopupModel } from '../../application/popup-model'

export interface PopupActions {
  connect(): void
  openOptions(): void
  openUrl(url: string): void
}

function articleLine(a: ArticleView): string {
  return `${a.minutes} min · ${a.progressPercent}%${a.completed ? ' · ✓' : ''}`
}

function Body({ model, actions }: { model: PopupModel; actions: PopupActions }) {
  switch (model.kind) {
    case 'disconnected':
      return (
        <>
          <p>Track your reading time and log it to Notion.</p>
          <button className="primary" onClick={actions.connect}>Connect Notion</button>
        </>
      )
    case 'needs_setup':
      return (
        <>
          <p>Almost there: {model.workspaceName} still needs its Reading Library.</p>
          {model.setupError && <p className="muted">Reason: {model.setupError}</p>}
          <button className="primary" onClick={actions.openOptions}>Finish setup</button>
        </>
      )
    case 'revoked':
      return (
        <>
          <p>Notion access for {model.workspaceName} was removed. Your reading is kept and syncs after you reconnect.</p>
          <button className="primary" onClick={actions.connect}>Reconnect</button>
        </>
      )
    case 'active': {
      const library = model.libraryUrl
      return (
        <>
          <section className="today">
            <span className="muted">Today</span>
            <strong data-role="today">{model.todayMinutes} min</strong>
          </section>
          {model.current && (
            <section data-role="current">
              <h2>Reading now</h2>
              <p className="title">{model.current.title}</p>
              <p className="muted">{articleLine(model.current)}</p>
            </section>
          )}
          {model.recent.length === 0 ? (
            <p className="muted">Nothing read yet. Open an article to start.</p>
          ) : (
            <section>
              <h2>Recent</h2>
              <ul data-role="recent">
                {model.recent.map((a) => (
                  <li key={a.url}>
                    <button className="link" title={a.url} onClick={() => actions.openUrl(a.notionUrl ?? a.url)}>{a.title}</button>
                    <span className="muted">{articleLine(a)}</span>
                  </li>
                ))}
              </ul>
            </section>
          )}
          {library && <button onClick={() => actions.openUrl(library)}>Open library in Notion</button>}
          {model.unsynced > 0 && <p className="muted small">{model.unsynced} segments waiting to sync</p>}
        </>
      )
    }
  }
}

export function Popup({ model, actions, note }: { model: PopupModel; actions: PopupActions; note?: string }) {
  return (
    <>
      <header>
        <h1>Reading Tracker</h1>
      </header>
      <Body model={model} actions={actions} />
      {note && <p role="status" className="note">{note}</p>}
    </>
  )
}
```

`extension/src/ui/popup/PopupApp.tsx`:

```tsx
// <PopupApp> loads the popup model, refreshes /v1/me in the background, and handles connect.
// Connect itself runs in the service worker: the popup closes when the Notion window opens.
import { useEffect, useState } from 'react'
import type { PopupModel } from '../../application/popup-model'
import type { ConnectResult } from '../../application/session'
import { connectMessage } from './messages'
import { Popup, type PopupActions } from './Popup'

export interface PopupAppProps {
  load(): Promise<PopupModel>
  refresh(): Promise<void>
  connect(): Promise<ConnectResult>
  openOptions(): void
  openUrl(url: string): void
}

const LOAD_FAILED = "Couldn't load your reading. Close and reopen the popup."

export function PopupApp(props: PopupAppProps) {
  const [model, setModel] = useState<PopupModel | null>(null)
  const [note, setNote] = useState<string | undefined>()

  useEffect(() => {
    let live = true
    const show = async () => {
      const m = await props.load()
      if (live) setModel(m)
    }
    show()
      .then(() => props.refresh())
      .then(show)
      .catch(() => {
        if (live) setNote(LOAD_FAILED)
      })
    return () => {
      live = false
    }
    // Load once per popup opening; the props are fixed for the popup's lifetime.
  }, [])

  const actions: PopupActions = {
    connect: () => {
      props.connect().then(
        async (res) => {
          setNote(res.ok ? undefined : connectMessage(res.reason))
          setModel(await props.load())
        },
        () => setNote(connectMessage('network_error')),
      )
    },
    openOptions: props.openOptions,
    openUrl: props.openUrl,
  }

  if (!model) return note ? <p role="status" className="note">{note}</p> : <p className="muted">Loading…</p>
  return <Popup model={model} actions={actions} note={note} />
}
```

`extension/src/ui/popup/popup.css`:

```css
:root {
  color-scheme: light;
  --fg: #1f2328;
  --muted: #656d76;
  --accent: #2f6feb;
  --line: #d0d7de;
  font: 14px/1.45 system-ui, -apple-system, 'Segoe UI', sans-serif;
  color: var(--fg);
}
body { margin: 0; width: 320px; }
main { padding: 14px 16px; display: grid; gap: 10px; }
h1 { font-size: 15px; margin: 0; }
h2 { font-size: 12px; text-transform: uppercase; letter-spacing: .04em; color: var(--muted); margin: 0 0 4px; }
p { margin: 0; }
ul { list-style: none; margin: 0; padding: 0; display: grid; gap: 6px; }
li { display: grid; }
.muted { color: var(--muted); }
.small { font-size: 12px; }
.title { font-weight: 600; }
.today { display: flex; align-items: baseline; justify-content: space-between; border-bottom: 1px solid var(--line); padding-bottom: 8px; }
.today strong { font-size: 22px; }
button { font: inherit; padding: 6px 10px; border: 1px solid var(--line); border-radius: 6px; background: #f6f8fa; cursor: pointer; }
button.primary { background: var(--accent); border-color: var(--accent); color: #fff; }
button.link { border: 0; background: none; padding: 0; color: var(--accent); text-align: left; }
.note { color: #9a6700; }
```

- [ ] **Step 8: Write the popup entry and link the stylesheet**

`extension/src/entry/popup.tsx`:

```tsx
// Popup composition root (spec T30): real adapters in, <PopupApp> out.
import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import { sendToBackground, sendToTab } from '../adapters/chrome/messaging'
import { activeTabId, openOptions, openTab } from '../adapters/chrome/tabs'
import { buildPopupModel } from '../application/popup-model'
import { PopupApp } from '../ui/popup/PopupApp'
import { compose } from './compose'

const s = compose('popup')

async function currentCanonical(): Promise<string | null> {
  const tabId = await activeTabId()
  return tabId === null ? null : ((await sendToTab(tabId, { type: 'current-page' }))?.canonicalUrl ?? null)
}

createRoot(document.getElementById('app') as HTMLElement).render(
  <StrictMode>
    <PopupApp
      load={async () => buildPopupModel(s, await s.session.state(), await currentCanonical())}
      refresh={async () => {
        await s.session.refresh()
      }}
      connect={() => sendToBackground({ type: 'connect' })}
      openOptions={() => void openOptions()}
      openUrl={(url) => void openTab(url)}
    />
  </StrictMode>,
)
```

Replace `extension/popup.html` with:

```html
<!doctype html>
<html lang="en">
  <head>
    <meta charset="utf-8" />
    <title>Reading Tracker</title>
    <link rel="stylesheet" href="/src/ui/popup/popup.css" />
  </head>
  <body>
    <main id="app"></main>
    <script type="module" src="/src/entry/popup.tsx"></script>
  </body>
</html>
```

- [ ] **Step 9: Run everything**

Run: `cd extension && pnpm check`
Expected:
- All tests PASS.
- The arch test passes:
  - `ui/popup/*` imports only application, ui and `react`;
  - `entry/popup.tsx` composes adapters and `react-dom`;
  - `application/*` imports no React.
- The build emits `popup.html` with a hashed CSS asset under `assets/`. React lands in `popup.js` and its chunks, and `background.js` and `content.js` stay free of React. Check that with `grep -c react dist/content.js`, which should print `0`.

- [ ] **Step 10: Commit**

```bash
cd /Users/tuandoquoc/personal/reading-chrome-extension
git add extension/src/application/popup-model.ts extension/src/application/popup-model.test.ts extension/src/ui extension/src/entry/popup.tsx extension/popup.html
git commit -m "feat(extension): React popup with connect, setup, reconnect and today's reading"
```

---
### Task 12: Options page

**Files:**
- Create: `extension/src/ui/options/OptionsView.tsx`, `OptionsView.test.tsx`
- Create: `extension/src/ui/options/options-controller.ts`, `options-controller.test.ts`
- Create: `extension/src/ui/options/OptionsApp.tsx`, `OptionsApp.test.tsx`, `options.css`
- Modify: `extension/src/entry/options.tsx` (replace the stub), `extension/options.html` (stylesheet)

**Interfaces:**
- **Consumes:**
  - Task 7: `SessionService`, `SetupService`, `DomainService`, `SessionState`, `ConnectResult`.
  - Task 4: `NotionPage`.
  - Task 2: `DomainLists`.
  - Task 11: `connectMessage`.
  - Tasks 9–10: `sendToBackground`, `compose`.
  - Task 1: React 19 and `@testing-library/react`.
- **Produces:**
  - `ui/options/OptionsView.tsx`:
    - `OptionsState { session; domains; pages: NotionPage[] | null; busy: boolean; note: string | null }`;
    - `OptionsActions`;
    - `<OptionsView state actions />`, a pure view.
  - `ui/options/options-controller.ts` (plain TypeScript, no React):
    - `OptionsServices` and `OptionsUi { render(state, actions); confirm(message): boolean }`;
    - `createOptionsController(services, ui)`, which returns `{ load(): Promise<void>; actions: OptionsActions; state(): OptionsState }`.
  - `ui/options/OptionsApp.tsx`: `<OptionsApp services confirm />`. It wires the controller to React state, loads, refreshes `/v1/me`, and reloads.

**Sections** (spec T31):
- **Account:** the connection status.
  - "Connect Notion" when disconnected, "Reconnect" when revoked.
  - "Disconnect" calls `POST /v1/auth/logout`.
  - "Delete account" calls `DELETE /v1/me` after a confirm dialog.
- **Finish setup:** shown only while the installation is `needs_setup`. Search the pages the integration can see (`GET /v1/setup/pages`), then pick a parent (`POST /v1/setup`).
- **Tracked sites:**
  - Enabling a domain shows the optional host-permission prompt, then registers the content script.
  - Enabled domains are listed, each with a remove button.
  - A note says Medium and Freedium are always tracked.
- **Never track:** a denied-domain list that overrides everything.

`enableDomain` runs synchronously inside the form's submit handler, and its first `await` is `permissions.request`. That keeps the prompt inside the user gesture.

- [ ] **Step 1: Write the failing tests**

`extension/src/ui/options/OptionsView.test.tsx`:

```tsx
// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen, within } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest'
import { INSTALLATION } from '../../../test/fakes'
import { OptionsView, type OptionsActions, type OptionsState } from './OptionsView'

afterEach(cleanup)

describe('<OptionsView>', () => {
  let actions: OptionsActions

  beforeEach(() => {
    actions = {
      connect: vi.fn(), disconnect: vi.fn(), deleteAccount: vi.fn(), searchPages: vi.fn(), choosePage: vi.fn(),
      enableDomain: vi.fn(), disableDomain: vi.fn(), denyDomain: vi.fn(), undenyDomain: vi.fn(),
    }
  })

  const state = (over: Partial<OptionsState> = {}): OptionsState => ({
    session: { kind: 'connected', user: { id: 'u1', name: 'Tuan' }, installation: INSTALLATION },
    domains: { enabled: [], denied: [] }, pages: null, busy: false, note: null, ...over,
  })
  const view = (s: OptionsState) => render(<OptionsView state={s} actions={actions} />).container
  const submit = (container: HTMLElement, form: string, value: string) => {
    const f = container.querySelector<HTMLFormElement>(`form[data-form=${form}]`)!
    fireEvent.change(within(f).getByRole('textbox'), { target: { value } })
    fireEvent.submit(f)
  }

  test('disconnected: only Connect in the account section, no setup', () => {
    const c = view(state({ session: { kind: 'disconnected' } }))
    fireEvent.click(screen.getByRole('button', { name: 'Connect Notion' }))
    expect(actions.connect).toHaveBeenCalled()
    expect(screen.queryByRole('button', { name: 'Disconnect' })).toBeNull()
    expect(c.querySelector('[data-section=setup]')).toBeNull()
  })

  test('active: workspace shown, disconnect and delete wired, no setup', () => {
    const c = view(state())
    expect(c.textContent).toContain("Tuan's Notion")
    fireEvent.click(screen.getByRole('button', { name: 'Disconnect' }))
    fireEvent.click(screen.getByRole('button', { name: 'Delete account' }))
    expect(actions.disconnect).toHaveBeenCalled()
    expect(actions.deleteAccount).toHaveBeenCalled()
    expect(c.querySelector('[data-section=setup]')).toBeNull()
  })

  test('revoked: Reconnect', () => {
    view(state({ session: { kind: 'connected', user: null, installation: { ...INSTALLATION, status: 'revoked' } } }))
    fireEvent.click(screen.getByRole('button', { name: 'Reconnect' }))
    expect(actions.connect).toHaveBeenCalled()
  })

  test('T31: needs_setup shows the page picker', () => {
    const needsSetup = { ...INSTALLATION, status: 'needs_setup', setupError: 'template_not_found', libraryUrl: null }
    const c = view(state({
      session: { kind: 'connected', user: null, installation: needsSetup },
      pages: [{ id: 'p1', title: 'Reading', url: 'https://www.notion.so/p1' }],
    }))
    expect(c.querySelector('[data-section=setup]')?.textContent).toContain('template_not_found')
    submit(c, 'search', 'Read')
    expect(actions.searchPages).toHaveBeenCalledWith('Read')
    fireEvent.click(screen.getByRole('button', { name: 'Use this page' }))
    expect(actions.choosePage).toHaveBeenCalledWith('p1')
  })

  test('an empty search result says so', () => {
    view(state({ session: { kind: 'connected', user: null, installation: { ...INSTALLATION, status: 'needs_setup' } }, pages: [] }))
    expect(screen.getByText(/No pages found/)).toBeTruthy()
  })

  test('T31: tracked and denied domains', () => {
    const c = view(state({ domains: { enabled: ['example.com'], denied: ['spam.net'] } }))
    submit(c, 'enable', 'blog.org')
    submit(c, 'deny', 'ads.com')
    expect(actions.enableDomain).toHaveBeenCalledWith('blog.org')
    expect(actions.denyDomain).toHaveBeenCalledWith('ads.com')
    fireEvent.click(c.querySelector<HTMLButtonElement>('[data-list=enabled] button')!)
    fireEvent.click(c.querySelector<HTMLButtonElement>('[data-list=denied] button')!)
    expect(actions.disableDomain).toHaveBeenCalledWith('example.com')
    expect(actions.undenyDomain).toHaveBeenCalledWith('spam.net')
    expect(c.textContent).toContain('Medium and Freedium are always tracked')
  })

  test('busy disables buttons; the note is announced', () => {
    const c = view(state({ busy: true, note: 'Tracking blog.org.' }))
    expect([...c.querySelectorAll('button')].every((b) => b.disabled)).toBe(true)
    expect(screen.getByRole('status').textContent).toBe('Tracking blog.org.')
  })
})
```

Then write the controller and app tests:

`extension/src/ui/options/options-controller.test.ts`:

```ts
// @vitest-environment jsdom
import { beforeEach, describe, expect, test, vi } from 'vitest'
import {
  FakeApi, FakeAuthFlow, FakeContentScripts, FakePermissions, FakePkce, INSTALLATION, MemoryLog, MemoryStore,
} from '../../../test/fakes'
import { ApiError } from '../../application/errors'
import { DomainService } from '../../application/domains'
import { SessionService } from '../../application/session'
import { SetupService } from '../../application/setup'
import type { ConnectResult } from '../../application/session'
import { createOptionsController } from './options-controller'
import type { OptionsState } from './OptionsView'

describe('options controller', () => {
  let store: MemoryStore
  let api: FakeApi
  let events: string[]
  let confirmAnswer: boolean
  let rendered: OptionsState[]
  let connect: () => Promise<ConnectResult>

  beforeEach(async () => {
    store = new MemoryStore()
    await store.setKv('device_token', 'rt_x')
    await store.setKv('installation', { ...INSTALLATION, status: 'needs_setup', libraryUrl: null })
    api = new FakeApi()
    events = []
    confirmAnswer = false
    rendered = []
    connect = async () => ({ ok: false, reason: 'cancelled' })
  })

  function controller() {
    const session = new SessionService({ store, api, authFlow: new FakeAuthFlow(), pkce: new FakePkce(), deviceName: () => 'x', log: new MemoryLog() })
    return createOptionsController(
      {
        session,
        setup: new SetupService({ store, api }),
        domains: new DomainService({ store, permissions: new FakePermissions(true, events), scripts: new FakeContentScripts() }),
        connect: () => connect(),
      },
      { render: (s) => rendered.push(structuredClone(s)), confirm: () => confirmAnswer },
    )
  }

  test('load renders the stored session and domain lists', async () => {
    await store.setKv('enabled_domains', ['example.com'])
    const c = controller()
    await c.load()
    expect(rendered.at(-1)).toMatchObject({ domains: { enabled: ['example.com'], denied: [] }, busy: false })
    expect(rendered.at(-1)?.session).toMatchObject({ kind: 'connected' })
  })

  test('T31: enabling a domain prompts first and lists it', async () => {
    const c = controller()
    await c.load()
    await c.actions.enableDomain('Example.com')
    expect(events[0]).toBe('request https://example.com/* https://*.example.com/*')
    expect(c.state()).toMatchObject({ domains: { enabled: ['example.com'] }, note: 'Tracking example.com.' })
  })

  test('an invalid domain is explained', async () => {
    const c = controller()
    await c.load()
    await c.actions.enableDomain('nope')
    expect(c.state().note).toBe('Enter a domain like example.com.')
  })

  test('T31: delete account needs confirmation', async () => {
    const c = controller()
    await c.load()
    await c.actions.deleteAccount()
    expect(api.count('deleteAccount')).toBe(0)
    confirmAnswer = true
    await c.actions.deleteAccount()
    expect(api.count('deleteAccount')).toBe(1)
    expect(c.state()).toMatchObject({ session: { kind: 'disconnected' }, note: 'Account deleted.' })
  })

  test('a failed delete keeps the session and says so', async () => {
    api = new FakeApi({ deleteAccount: async () => { throw new ApiError(500, 'internal', 'boom') } })
    confirmAnswer = true
    const c = controller()
    await c.load()
    await c.actions.deleteAccount()
    expect(c.state()).toMatchObject({ session: { kind: 'connected' }, note: 'Could not delete the account. Try again.' })
  })

  test('T31: setup search then choose activates the installation', async () => {
    api = new FakeApi({ setupPages: async () => [{ id: 'p1', title: 'Reading', url: 'https://www.notion.so/p1' }] })
    const c = controller()
    await c.load()
    await c.actions.searchPages('Read')
    expect(c.state().pages).toEqual([{ id: 'p1', title: 'Reading', url: 'https://www.notion.so/p1' }])
    await c.actions.choosePage('p1')
    expect(c.state()).toMatchObject({ pages: null, note: 'Reading Library and Daily Log created.' })
    expect(c.state().session).toMatchObject({ installation: { status: 'active' } })
  })

  test('a setup conflict shows the server message', async () => {
    api = new FakeApi({ setup: async () => { throw new ApiError(409, 'conflict', 'setup failed: property_missing:Progress') } })
    const c = controller()
    await c.load()
    await c.actions.choosePage('p1')
    expect(c.state().note).toBe('setup failed: property_missing:Progress')
  })

  test('disconnect logs out; connect failure shows the reason', async () => {
    const c = controller()
    await c.load()
    await c.actions.connect()
    expect(c.state().note).toBe('Connection cancelled.')
    await c.actions.disconnect()
    expect(api.count('logout')).toBe(1)
    expect(c.state()).toMatchObject({ session: { kind: 'disconnected' }, note: 'Disconnected.' })
  })

  test('busy is shown while an action runs', async () => {
    const c = controller()
    await c.load()
    rendered = []
    await c.actions.denyDomain('ads.com')
    expect(rendered[0]?.busy).toBe(true)
    expect(rendered.at(-1)).toMatchObject({ busy: false, domains: { denied: ['ads.com'] } })
  })

  test('render receives the actions object', async () => {
    const render = vi.fn()
    const c = createOptionsController(
      { session: { state: async () => ({ kind: 'disconnected' as const }) } as never, setup: {} as never,
        domains: { lists: async () => ({ enabled: [], denied: [] }) } as never, connect },
      { render, confirm: () => false },
    )
    await c.load()
    expect(render).toHaveBeenLastCalledWith(expect.anything(), c.actions)
  })
})
```

`extension/src/ui/options/OptionsApp.test.tsx`:

```tsx
// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, expect, test, vi } from 'vitest'
import { FakeContentScripts, FakePermissions, MemoryStore } from '../../../test/fakes'
import { DomainService } from '../../application/domains'
import type { SessionState } from '../../application/session'
import { OptionsApp } from './OptionsApp'
import type { OptionsServices } from './options-controller'

afterEach(cleanup)

function services(): OptionsServices & { refreshed: () => number } {
  let refreshes = 0
  const session: SessionState = { kind: 'disconnected' }
  return {
    session: {
      state: async () => session,
      refresh: async () => {
        refreshes++
        return session
      },
      logout: async () => {},
      deleteAccount: async () => {},
    },
    setup: { searchPages: async () => [], createDatabases: vi.fn() },
    domains: new DomainService({ store: new MemoryStore(), permissions: new FakePermissions(), scripts: new FakeContentScripts() }),
    connect: async () => ({ ok: false, reason: 'cancelled' }),
    refreshed: () => refreshes,
  }
}

test('loads, refreshes /v1/me once, and renders the page', async () => {
  const s = services()
  render(<OptionsApp services={s} confirm={() => false} />)
  expect(await screen.findByRole('button', { name: 'Connect Notion' })).toBeTruthy()
  await vi.waitFor(() => expect(s.refreshed()).toBe(1))
})

test('actions re-render through React state', async () => {
  render(<OptionsApp services={services()} confirm={() => false} />)
  const input = (await screen.findAllByPlaceholderText('example.com'))[0]!
  fireEvent.change(input, { target: { value: 'example.com' } })
  fireEvent.submit(input.closest('form')!)
  expect(await screen.findByText('Tracking example.com.')).toBeTruthy()
  expect(screen.getByText('example.com', { selector: 'li span' })).toBeTruthy()
})
```

- [ ] **Step 2: Run them to see them fail**

Run: `cd extension && pnpm test ui/options`
Expected: FAIL. The modules `./OptionsView`, `./options-controller` and `./OptionsApp` cannot be resolved.

- [ ] **Step 3: Implement the view**

`extension/src/ui/options/OptionsView.tsx`:

```tsx
// <OptionsView> draws the options page (spec T31) for one OptionsState. It holds only
// the text typed into its forms; everything else comes from the controller.
import { useState, type FormEvent } from 'react'
import type { DomainLists } from '../../domain/domains'
import type { SessionState } from '../../application/session'
import type { NotionPage } from '../../application/types'

export interface OptionsState {
  session: SessionState
  domains: DomainLists
  pages: NotionPage[] | null
  busy: boolean
  note: string | null
}

export interface OptionsActions {
  connect(): void
  disconnect(): void
  deleteAccount(): void
  searchPages(query: string): void
  choosePage(pageId: string): void
  enableDomain(input: string): void
  disableDomain(domain: string): void
  denyDomain(input: string): void
  undenyDomain(domain: string): void
}

interface TextFormProps {
  name: string
  placeholder: string
  label: string
  busy: boolean
  onSubmit(value: string): void
}

function TextForm({ name, placeholder, label, busy, onSubmit }: TextFormProps) {
  const [value, setValue] = useState('')
  const submit = (e: FormEvent) => {
    e.preventDefault()
    onSubmit(value) // synchronous: enableDomain's permission prompt stays in the gesture
  }
  return (
    <form data-form={name} onSubmit={submit}>
      <input type="text" placeholder={placeholder} value={value} onChange={(e) => setValue(e.target.value)} />
      <button type="submit" disabled={busy}>{label}</button>
    </form>
  )
}

function DomainList({ name, domains, busy, empty, onRemove }: {
  name: string
  domains: readonly string[]
  busy: boolean
  empty: string
  onRemove(domain: string): void
}) {
  if (domains.length === 0) return <p className="muted">{empty}</p>
  return (
    <ul data-list={name}>
      {domains.map((d) => (
        <li key={d}>
          <span>{d}</span>
          <button disabled={busy} onClick={() => onRemove(d)}>Remove</button>
        </li>
      ))}
    </ul>
  )
}

function Account({ state, actions }: { state: OptionsState; actions: OptionsActions }) {
  const { session, busy } = state
  if (session.kind === 'disconnected') {
    return (
      <section data-section="account">
        <h2>Notion</h2>
        <p>Not connected.</p>
        <button className="primary" disabled={busy} onClick={actions.connect}>Connect Notion</button>
      </section>
    )
  }
  const inst = session.installation
  const who = session.user ? ` as ${session.user.name}` : ''
  const status = !inst ? 'No workspace connected.'
    : inst.status === 'active' ? `Connected to ${inst.workspaceName}${who}.`
      : inst.status === 'revoked' ? `Access to ${inst.workspaceName} was removed.`
        : `Connected to ${inst.workspaceName}${who}; setup is not finished.`
  return (
    <section data-section="account">
      <h2>Notion</h2>
      <p>{status}</p>
      <div className="row">
        {(!inst || inst.status === 'revoked') && (
          <button className="primary" disabled={busy} onClick={actions.connect}>Reconnect</button>
        )}
        <button disabled={busy} onClick={actions.disconnect}>Disconnect</button>
        <button className="danger" disabled={busy} onClick={actions.deleteAccount}>Delete account</button>
      </div>
    </section>
  )
}

function Setup({ state, actions }: { state: OptionsState; actions: OptionsActions }) {
  const inst = state.session.kind === 'connected' ? state.session.installation : null
  if (!inst || inst.status !== 'needs_setup') return null
  return (
    <section data-section="setup">
      <h2>Finish setup</h2>
      <p>Pick a Notion page; Reading Library and Daily Log are created inside it.</p>
      {inst.setupError && <p className="muted">Reason: {inst.setupError}</p>}
      <TextForm name="search" placeholder="Search your pages" label="Search" busy={state.busy} onSubmit={actions.searchPages} />
      {state.pages !== null && (state.pages.length === 0 ? (
        <p className="muted">No pages found. Share a page with the integration in Notion, then search again.</p>
      ) : (
        <ul data-list="pages">
          {state.pages.map((p) => (
            <li key={p.id}>
              <span title={p.url}>{p.title || 'Untitled'}</span>
              <button disabled={state.busy} onClick={() => actions.choosePage(p.id)}>Use this page</button>
            </li>
          ))}
        </ul>
      ))}
    </section>
  )
}

export function OptionsView({ state, actions }: { state: OptionsState; actions: OptionsActions }) {
  return (
    <>
      <h1>Reading Tracker</h1>
      <Account state={state} actions={actions} />
      <Setup state={state} actions={actions} />
      <section data-section="enabled">
        <h2>Tracked sites</h2>
        <p className="muted">Medium and Freedium are always tracked. Add other sites to track them too.</p>
        <TextForm name="enable" placeholder="example.com" label="Track site" busy={state.busy} onSubmit={actions.enableDomain} />
        <DomainList name="enabled" domains={state.domains.enabled} busy={state.busy} empty="No other sites yet." onRemove={actions.disableDomain} />
      </section>
      <section data-section="denied">
        <h2>Never track</h2>
        <p className="muted">Sites listed here are never tracked, even if added above.</p>
        <TextForm name="deny" placeholder="example.com" label="Never track" busy={state.busy} onSubmit={actions.denyDomain} />
        <DomainList name="denied" domains={state.domains.denied} busy={state.busy} empty="Nothing blocked." onRemove={actions.undenyDomain} />
      </section>
      {state.note && <p role="status" className="note">{state.note}</p>}
    </>
  )
}
```

- [ ] **Step 4: Run the view test**

Run: `cd extension && pnpm test OptionsView`
Expected: PASS, 7 tests.

- [ ] **Step 5: Implement the controller**

`extension/src/ui/options/options-controller.ts`:

```ts
// The options page's state machine: run an action, keep the page busy meanwhile,
// then re-read what changed and re-render with a short note.
import { isApiError } from '../../application/errors'
import type { DomainService } from '../../application/domains'
import type { ConnectResult, SessionService } from '../../application/session'
import type { SetupService } from '../../application/setup'
import { connectMessage } from '../popup/messages'
import type { OptionsActions, OptionsState } from './OptionsView'

export interface OptionsServices {
  session: Pick<SessionService, 'state' | 'refresh' | 'logout' | 'deleteAccount'>
  setup: Pick<SetupService, 'searchPages' | 'createDatabases'>
  domains: Pick<DomainService, 'lists' | 'enable' | 'disable' | 'deny' | 'undeny'>
  connect: () => Promise<ConnectResult>
}

export interface OptionsUi {
  render(state: OptionsState, actions: OptionsActions): void
  confirm(message: string): boolean
}

const ENABLE_FAILURES = {
  invalid: 'Enter a domain like example.com.',
  permission_denied: 'Chrome permission was not granted.',
  already_enabled: 'That site is already tracked.',
} as const

function errorText(e: unknown): string {
  return isApiError(e) ? e.message : e instanceof Error ? e.message : String(e)
}

export function createOptionsController(s: OptionsServices, ui: OptionsUi) {
  let state: OptionsState = {
    session: { kind: 'disconnected' }, domains: { enabled: [], denied: [] }, pages: null, busy: false, note: null,
  }
  const draw = () => ui.render(state, actions)

  // run marks the page busy, runs the action (whose first await is the action's own), then refreshes.
  async function run(action: () => Promise<string | null>): Promise<void> {
    const pending = action()
    state = { ...state, busy: true, note: null }
    draw()
    let note: string | null
    try {
      note = await pending
    } catch (e) {
      note = errorText(e)
    }
    state = { ...state, session: await s.session.state(), domains: await s.domains.lists(), busy: false, note }
    draw()
  }

  const actions: OptionsActions = {
    connect: () => run(async () => {
      const res = await s.connect()
      return res.ok ? 'Connected.' : connectMessage(res.reason)
    }),
    disconnect: () => run(async () => {
      await s.session.logout()
      return 'Disconnected.'
    }),
    deleteAccount: () => {
      const message = 'Delete your Reading Tracker account? Your Notion pages stay; reading data on this device is erased.'
      if (!ui.confirm(message)) return Promise.resolve()
      return run(async () => {
        try {
          await s.session.deleteAccount()
          return 'Account deleted.'
        } catch {
          return 'Could not delete the account. Try again.'
        }
      })
    },
    searchPages: (query) => run(async () => {
      const pages = await s.setup.searchPages(query)
      state = { ...state, pages }
      return null
    }),
    choosePage: (pageId) => run(async () => {
      await s.setup.createDatabases(pageId)
      state = { ...state, pages: null }
      return 'Reading Library and Daily Log created.'
    }),
    enableDomain: (input) => run(async () => {
      const res = await s.domains.enable(input) // first await: keeps the permission prompt in the gesture
      return res.ok ? `Tracking ${res.domain}.` : ENABLE_FAILURES[res.reason]
    }),
    disableDomain: (domain) => run(async () => {
      await s.domains.disable(domain)
      return `Stopped tracking ${domain}.`
    }),
    denyDomain: (input) => run(async () => {
      const res = await s.domains.deny(input)
      return res.ok ? `${res.domain} will never be tracked.` : ENABLE_FAILURES.invalid
    }),
    undenyDomain: (domain) => run(async () => {
      await s.domains.undeny(domain)
      return `${domain} removed from the block list.`
    }),
  }

  return {
    actions,
    state: () => state,
    async load(): Promise<void> {
      state = { ...state, session: await s.session.state(), domains: await s.domains.lists() }
      draw()
    },
  }
}
```

`OptionsActions` methods are typed `void`, but these implementations return promises. That lets the tests await them, while the view only fires them. The type-check accepts this, because a function returning a promise is assignable to one returning `void`. The controller has no React in it: `OptionsApp` (next step) gives it a `render` callback that sets React state.

- [ ] **Step 6: Implement OptionsApp, the styles, the entry and the HTML**

`extension/src/ui/options/OptionsApp.tsx`:

```tsx
// <OptionsApp> connects the controller to React: the controller's render callback sets
// state, and React draws <OptionsView>. It loads, refreshes /v1/me, then reloads.
import { useEffect, useState } from 'react'
import { createOptionsController, type OptionsServices } from './options-controller'
import { OptionsView, type OptionsActions, type OptionsState } from './OptionsView'

export interface OptionsAppProps {
  services: OptionsServices
  confirm(message: string): boolean
}

export function OptionsApp({ services, confirm }: OptionsAppProps) {
  const [view, setView] = useState<{ state: OptionsState; actions: OptionsActions } | null>(null)

  useEffect(() => {
    let live = true
    const controller = createOptionsController(services, {
      render: (state, actions) => {
        if (live) setView({ state, actions })
      },
      confirm,
    })
    void controller
      .load()
      .then(() => services.session.refresh())
      .then(() => controller.load())
    return () => {
      live = false
    }
    // One controller per page load; services and confirm are fixed for the page's lifetime.
  }, [])

  if (!view) return <p className="muted">Loading…</p>
  return <OptionsView state={view.state} actions={view.actions} />
}
```

`extension/src/ui/options/options.css`:

```css
:root {
  color-scheme: light;
  --fg: #1f2328;
  --muted: #656d76;
  --accent: #2f6feb;
  --danger: #cf222e;
  --line: #d0d7de;
  font: 15px/1.5 system-ui, -apple-system, 'Segoe UI', sans-serif;
  color: var(--fg);
}
body { margin: 0; background: #fff; }
main { max-width: 640px; margin: 40px auto; padding: 0 20px; display: grid; gap: 28px; }
h1 { font-size: 22px; margin: 0; }
h2 { font-size: 16px; margin: 0 0 6px; }
section { display: grid; gap: 8px; padding-bottom: 20px; border-bottom: 1px solid var(--line); }
p { margin: 0; }
.muted { color: var(--muted); }
.row, form { display: flex; gap: 8px; flex-wrap: wrap; }
input { font: inherit; padding: 6px 8px; border: 1px solid var(--line); border-radius: 6px; flex: 1; min-width: 200px; }
ul { list-style: none; margin: 0; padding: 0; display: grid; gap: 6px; }
li { display: flex; align-items: center; justify-content: space-between; gap: 8px; }
button { font: inherit; padding: 6px 12px; border: 1px solid var(--line); border-radius: 6px; background: #f6f8fa; cursor: pointer; }
button:disabled { opacity: .5; cursor: default; }
button.primary { background: var(--accent); border-color: var(--accent); color: #fff; }
button.danger { color: var(--danger); }
.note { color: #9a6700; }
```

`extension/src/entry/options.tsx`:

```tsx
// Options page composition root (spec T31). Connect goes through the background (like the
// popup) so the outbox sync after connecting runs in the service worker.
import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import { sendToBackground } from '../adapters/chrome/messaging'
import { OptionsApp } from '../ui/options/OptionsApp'
import { compose } from './compose'

const s = compose('options')

createRoot(document.getElementById('app') as HTMLElement).render(
  <StrictMode>
    <OptionsApp
      services={{ session: s.session, setup: s.setup, domains: s.domains, connect: () => sendToBackground({ type: 'connect' }) }}
      confirm={(message) => window.confirm(message)}
    />
  </StrictMode>,
)
```

Replace `extension/options.html` with:

```html
<!doctype html>
<html lang="en">
  <head>
    <meta charset="utf-8" />
    <title>Reading Tracker — Options</title>
    <link rel="stylesheet" href="/src/ui/options/options.css" />
  </head>
  <body>
    <main id="app"></main>
    <script type="module" src="/src/entry/options.tsx"></script>
  </body>
</html>
```

- [ ] **Step 7: Run everything**

Run: `cd extension && pnpm check`
Expected:
- All tests PASS.
- The arch test passes: `ui/options/options-controller.ts` imports only application and ui, and the `.tsx` files import only `react` plus inner layers.
- The build emits `options.html` with its CSS.

- [ ] **Step 8: Commit**

```bash
cd /Users/tuandoquoc/personal/reading-chrome-extension
git add extension/src/ui extension/src/entry/options.tsx extension/options.html
git commit -m "feat(extension): React options page for account, setup and tracked domains"
```

---
### Task 13: Browser smoke test, README and final verification

**Files:**
- Create: `extension/scripts/smoke.mjs`
- Modify: `extension/package.json` (add the `smoke` script)
- Modify: `extension/test/arch.test.ts`. Require a file from every layer, so the rule cannot pass vacuously.
- Create: `extension/README.md`

**Interfaces:**
- Consumes: the complete extension (Tasks 1–12), the backend's local server (`backend/`: `make ddb-up`, `make run-local`), and Chrome for Testing.
- Produces: `pnpm smoke`, an automated end-to-end check in a real browser, and the extension README.

**Why Chrome for Testing:** branded Google Chrome ignores `--load-extension` since version 137, so the smoke script needs Chrome for Testing. Install it with `pnpm dlx @puppeteer/browsers install chrome@stable` and point `CHROME_BIN` at its binary.

The smoke script checks what unit tests cannot:
- the built manifest loads;
- the module service worker starts and imports its chunk;
- `onInstalled` fetches `/v1/config` into IndexedDB and creates the `sync` and `daily` alarms;
- the React popup and options page render;
- a real page on `medium.com` is tracked through the content script into a stored segment and article, with the Medium canonical URL and metadata.

`medium.com` is mapped to a local HTTPS server with a throwaway self-signed certificate.

- [ ] **Step 1: Make the layer rule non-vacuous for every layer**

In `extension/test/arch.test.ts`, replace the test `'the walk finds the composition roots (not vacuous)'` with:

```ts
  test('the walk finds every layer (not vacuous)', () => {
    for (const f of [
      'domain/tracker.ts', 'application/sync.ts', 'adapters/storage/dexie-store.ts',
      'adapters/chrome/messaging.ts', 'ui/popup/Popup.tsx', 'ui/options/OptionsView.tsx', 'entry/background.ts', 'entry/content.ts',
    ]) {
      expect(files).toContain(f)
    }
  })
```

Run: `cd extension && pnpm test arch`
Expected: PASS.

- [ ] **Step 2: Write the smoke script**

`extension/scripts/smoke.mjs`:

```js
// End-to-end smoke test in a real browser (plan Task 13).
//
//   CHROME_BIN=<Chrome for Testing binary> pnpm smoke
//
// Needs: `pnpm build` done, the backend's local server running at VITE_API_BASE
// (backend/: make ddb-up && make run-local), and openssl on PATH.
// It never touches Notion: it checks config, alarms, the popup, the options page and local tracking only.
import { execFileSync, spawn } from 'node:child_process'
import { existsSync, mkdtempSync, readFileSync } from 'node:fs'
import { createServer } from 'node:https'
import { tmpdir } from 'node:os'
import { join, resolve } from 'node:path'

const root = resolve(import.meta.dirname, '..')
const dist = join(root, 'dist')
const chromeBin = process.env.CHROME_BIN
const env = Object.fromEntries(
  (existsSync(join(root, '.env.local')) ? readFileSync(join(root, '.env.local'), 'utf8') : '')
    .split('\n').filter((l) => /^\s*[A-Z_]+=/.test(l)).map((l) => l.split('=').map((x) => x.trim())),
)
const apiBase = process.env.VITE_API_BASE ?? env.VITE_API_BASE
const ARTICLE = 'https://medium.com/@jane/designing-idempotent-apis-8f3c2a1b9d4e'
const CANONICAL = 'https://medium.com/p/8f3c2a1b9d4e'
const DEBUG_PORT = 9339
const HTTPS_PORT = 8443

const sleep = (ms) => new Promise((r) => setTimeout(r, ms))
let failures = 0
function check(ok, label, detail = '') {
  console.log(`${ok ? 'PASS' : 'FAIL'}  ${label}${detail ? `: ${detail}` : ''}`)
  if (!ok) failures++
}

function cdp(wsUrl) {
  const ws = new WebSocket(wsUrl)
  let id = 0
  const pending = new Map()
  ws.onmessage = (m) => {
    const msg = JSON.parse(m.data)
    pending.get(msg.id)?.(msg)
    pending.delete(msg.id)
  }
  const ready = new Promise((r) => { ws.onopen = r })
  const send = (method, params = {}) => new Promise((r) => {
    const i = ++id
    pending.set(i, r)
    ws.send(JSON.stringify({ id: i, method, params }))
  })
  const evaluate = async (expression) =>
    (await send('Runtime.evaluate', { expression, awaitPromise: true, returnByValue: true })).result?.result?.value
  return { ready, send, evaluate, close: () => ws.close() }
}

async function targets() {
  return (await fetch(`http://127.0.0.1:${DEBUG_PORT}/json/list`)).json()
}

const readDb = `new Promise((res) => { const r = indexedDB.open('reading-tracker'); r.onsuccess = () => {
  const tx = r.result.transaction(['segments', 'articles', 'kv']); const out = {};
  tx.objectStore('segments').getAll().onsuccess = (e) => { out.segments = e.target.result };
  tx.objectStore('articles').getAll().onsuccess = (e) => { out.articles = e.target.result };
  tx.objectStore('kv').get('config').onsuccess = (e) => { out.config = e.target.result?.value ?? null };
  tx.oncomplete = () => res(JSON.stringify(out)) } })`

async function main() {
  if (!chromeBin || !existsSync(chromeBin)) throw new Error('set CHROME_BIN to a Chrome for Testing binary')
  if (!existsSync(join(dist, 'manifest.json'))) throw new Error('run pnpm build first')
  if (!apiBase) throw new Error('VITE_API_BASE is not set (.env.local)')
  const cfg = await fetch(`${apiBase}/v1/config`).then((r) => r.json()).catch(() => null)
  if (!cfg?.tracking) throw new Error(`backend not reachable at ${apiBase}/v1/config (backend: make ddb-up && make run-local)`)

  const tmp = mkdtempSync(join(tmpdir(), 'rt-smoke-'))
  execFileSync('openssl', ['req', '-x509', '-newkey', 'rsa:2048', '-nodes', '-days', '1', '-subj', '/CN=medium.com',
    '-addext', 'subjectAltName=DNS:medium.com', '-keyout', join(tmp, 'key.pem'), '-out', join(tmp, 'cert.pem')], { stdio: 'ignore' })
  const page = `<!doctype html><html><head><title>Designing Idempotent APIs</title><meta name="author" content="Jane Doe"></head>
    <body><article><h1>Designing Idempotent APIs</h1><p>${'word '.repeat(1500)}</p></article></body></html>`
  const server = createServer({ key: readFileSync(join(tmp, 'key.pem')), cert: readFileSync(join(tmp, 'cert.pem')) }, (req, res) => {
    res.writeHead(200, { 'content-type': 'text/html' })
    res.end(req.url === '/elsewhere' ? '<p>elsewhere</p>' : page)
  }).listen(HTTPS_PORT)
  const chrome = spawn(chromeBin, [
    '--headless=new', `--remote-debugging-port=${DEBUG_PORT}`, `--user-data-dir=${join(tmp, 'profile')}`,
    `--disable-extensions-except=${dist}`, `--load-extension=${dist}`, '--no-first-run', '--ignore-certificate-errors',
    `--host-resolver-rules=MAP medium.com:443 127.0.0.1:${HTTPS_PORT}`, 'about:blank',
  ], { stdio: 'ignore' })

  try {
    let sw = null
    for (let i = 0; i < 60 && !sw; i++) {
      await sleep(250)
      let list = []
      try { list = await targets() } catch { continue }
      for (const t of list.filter((x) => x.type === 'service_worker')) {
        const c = cdp(t.webSocketDebuggerUrl)
        await c.ready
        if ((await c.evaluate('chrome.runtime.getManifest().name')) === 'Reading Tracker') sw = t
        c.close()
      }
    }
    check(sw !== null, 'service worker started')
    if (!sw) return
    const extId = new URL(sw.url).host
    const s = cdp(sw.webSocketDebuggerUrl)
    await s.ready
    check((await s.evaluate('chrome.runtime.getManifest().incognito')) === 'not_allowed', 'manifest incognito not_allowed')

    let alarms = ''
    for (let i = 0; i < 20 && !alarms.includes('sync'); i++) {
      await sleep(250)
      alarms = await s.evaluate('chrome.alarms.getAll().then((a) => a.map((x) => x.name).sort().join(","))')
    }
    check(alarms === 'daily,sync', 'alarms created', alarms)
    const db1 = JSON.parse(await s.evaluate(readDb))
    check(db1.config?.value?.tracking?.idleSeconds === cfg.tracking.idle_seconds, 'config cached from /v1/config')

    const popup = await (await fetch(`http://127.0.0.1:${DEBUG_PORT}/json/new?chrome-extension://${extId}/popup.html`, { method: 'PUT' })).json()
    await sleep(1500)
    const pc = cdp(popup.webSocketDebuggerUrl)
    await pc.ready
    const popupText = await pc.evaluate('document.getElementById("app").innerText')
    check(/Connect Notion/.test(popupText ?? ''), 'popup shows Connect Notion when disconnected')
    pc.close()

    const options = await (await fetch(`http://127.0.0.1:${DEBUG_PORT}/json/new?chrome-extension://${extId}/options.html`, { method: 'PUT' })).json()
    await sleep(1500)
    const oc = cdp(options.webSocketDebuggerUrl)
    await oc.ready
    const optionsText = await oc.evaluate('document.getElementById("app").innerText')
    check(/Tracked sites/.test(optionsText ?? '') && /Never track/.test(optionsText ?? ''), 'options page renders')
    oc.close()

    const tab = (await targets()).find((t) => t.type === 'page' && t.url === 'about:blank')
    const p = cdp(tab.webSocketDebuggerUrl)
    await p.ready
    await p.send('Emulation.setFocusEmulationEnabled', { enabled: true })
    await p.send('Page.enable')
    await p.send('Page.navigate', { url: ARTICLE })
    await sleep(1500)
    for (let i = 0; i < 6; i++) {
      await p.send('Input.dispatchMouseEvent', { type: 'mouseMoved', x: 20 + i, y: 20 })
      await sleep(1000)
    }
    await p.send('Page.navigate', { url: 'https://medium.com/elsewhere' }) // pagehide flushes the segment
    await sleep(1500)
    p.close()

    const db2 = JSON.parse(await s.evaluate(readDb))
    const seg = db2.segments.find((x) => x.canonical_url === CANONICAL)
    const art = db2.articles.find((a) => a.canonical_url === CANONICAL)
    check(Boolean(seg) && seg.active_seconds >= 4 && seg.synced === 0, 'segment stored for the Medium canonical', seg ? `${seg.active_seconds}s` : 'none')
    check(art?.title === 'Designing Idempotent APIs' && art?.author === 'Jane Doe' && art?.word_count === 1503, 'article metadata extracted',
      art ? `"${art.title}" by ${art.author}, ${art.word_count} words` : 'none')
    s.close()
  } finally {
    chrome.kill()
    server.close()
  }
}

main().then(
  () => {
    console.log(failures === 0 ? '\nsmoke: all checks passed' : `\nsmoke: ${failures} check(s) failed`)
    process.exitCode = failures === 0 ? 0 : 1
  },
  (e) => {
    console.error(`smoke: ${e.message}`)
    process.exitCode = 1
  },
)
```

In `extension/package.json`, add to `"scripts"`:

```json
    "smoke": "node scripts/smoke.mjs"
```

- [ ] **Step 3: Run the smoke test against the local backend**

```bash
cd /Users/tuandoquoc/personal/reading-chrome-extension/backend
make ddb-up
(make run-local > /tmp/reading-local.log 2>&1 &) ; sleep 4
cd ../extension
pnpm dlx @puppeteer/browsers install chrome@stable --path "$HOME/.cache/chrome-for-testing"
export CHROME_BIN="$(ls -d "$HOME"/.cache/chrome-for-testing/chrome/*/chrome-mac*/Google\ Chrome\ for\ Testing.app/Contents/MacOS/Google\ Chrome\ for\ Testing | tail -1)"
pnpm build && pnpm smoke
lsof -ti tcp:8080 | xargs kill
```

On Linux, `CHROME_BIN` is `…/chrome-linux64/chrome`. If `/tmp` is not writable for you, write the backend log to a scratch directory instead.

Expected: eight `PASS` lines, then `smoke: all checks passed`:
- `service worker started`;
- `manifest incognito not_allowed`;
- `alarms created: daily,sync`;
- `config cached from /v1/config`;
- `popup shows Connect Notion when disconnected`;
- `options page renders`;
- `segment stored for the Medium canonical: 6s` (any value ≥ 4 s);
- `article metadata extracted: "Designing Idempotent APIs" by Jane Doe, 1503 words`.

- [ ] **Step 4: Write the README**

`extension/README.md`:

````markdown
# Reading Tracker — Chrome extension

Measures active reading time per article. The data is stored in IndexedDB and pushed to the backend, which writes it to the user's Notion. See `docs/reading-tracker-agent-spec.md` (T25–T31) and `docs/reading-tracker-onboarding.md` §5.

## Architecture

Clean architecture. Dependencies point inward, and `test/arch.test.ts` enforces this.

| Layer | Path | Holds | May import |
|---|---|---|---|
| Domain | `src/domain` | URL normalization, Medium canonicals, tracker state machine, reading rules | domain only; no `chrome`, `window` or `document` |
| Application | `src/application` | use cases (record, sync, config, session, setup, domains, popup model), ports, message protocol | domain, application |
| Adapters | `src/adapters/{storage,http,crypto,chrome,site,page,system}` | Dexie, fetch, WebCrypto, `chrome.*`, site DOM extractors, DOM tracker driver | domain, application, and their own folder |
| UI | `src/ui/{popup,options}` | React 19 components for the popup and options page, plus the plain-TypeScript options controller | domain, application, ui, `react` |
| Entry | `src/entry` | composition roots: `background`, `content`, `popup`, `options` | everything |

Only `adapters/storage` imports `dexie`. Only `ui` and `entry` import `react` / `react-dom`, so React ships in the popup and options pages, never in the service worker or the content script. Only `adapters/chrome` and `entry` touch `chrome.*`.

How a read becomes a Notion page:
1. The content script (`entry/content`) asks the background for a tracking plan.
2. `adapters/page/tracker-driver` feeds DOM events to `domain/tracker`.
3. Flushed segments go to the background, where `application/record` stores them.
4. The `sync` alarm runs `application/sync`, which sends them to `POST /v1/sync/push`.
5. The backend queues the Notion write.

## Prerequisites

- Node ≥ 22 and pnpm.
- The backend running locally, using Podman for DynamoDB Local:

  ```bash
  cd ../backend && make ddb-up && make run-local
  ```

- For connecting Notion: a Notion public integration whose redirect URI is `http://localhost:8080/v1/oauth/notion/callback`. The backend's `.env.local` must hold its client id and secret, and `ALLOWED_EXTENSION_IDS` must include this extension's ID (next section).

## Setup

```bash
pnpm install
cp .env.example .env.local      # VITE_API_BASE=http://localhost:8080
pnpm build                      # → dist/
```

Load `dist/` in `chrome://extensions`: turn on Developer mode, click "Load unpacked", and pick `dist`.

### Pin the extension ID (spec §7)

An unpacked extension's ID changes with its folder unless the manifest has a `key`. The backend only redirects to allow-listed IDs. To pin it:

```bash
openssl genrsa 2048 | openssl pkcs8 -topk8 -nocrypt -out extension-key.pem      # keep this file private
echo "EXTENSION_KEY=$(openssl rsa -in extension-key.pem -pubout -outform DER | openssl base64 -A)" >> .env.local
openssl rsa -in extension-key.pem -pubout -outform DER | shasum -a 256 | head -c 32 | tr 0-9a-f a-p; echo   # the extension ID
```

Then:
1. Put that ID in the backend's `ALLOWED_EXTENSION_IDS`.
2. Restart `make run-local`.
3. Run `pnpm build` and reload the extension.

## Commands

| Command | Does |
|---|---|
| `pnpm test` | unit tests (Vitest, jsdom, fake-indexeddb, React Testing Library) including the layer rule |
| `pnpm typecheck` | `tsc --noEmit` |
| `pnpm build` | two Vite builds: service worker and React pages (ES modules), then the content script (one IIFE, no React) |
| `pnpm check` | typecheck, then test, then build |
| `pnpm smoke` | real-browser check of config, alarms, popup and tracking. It needs `CHROME_BIN` (Chrome for Testing) and the local backend. |

## Manual end-to-end check (spec T32, against a real Notion workspace)

1. Open the popup, click **Connect Notion**, and accept the template. The popup should show today's minutes.
2. Read a Freedium article for 3 minutes. Within `D + B + 60 s` (about 5 minutes locally), a Library page exists with Reading time ≈ 3 and Progress > 0.
3. In Notion, set Tags, Status and Rating, then read again. Those values stay unchanged and the metrics update.
4. A Daily Log row for today exists.
5. In Options, enable `example.com`. Accept the permission prompt. A post there is tracked without reinstalling.
6. Remove `example.com`; it is no longer tracked. Add it to **Never track**, then enable it again: it stays untracked.
7. Click **Disconnect**: the popup shows **Connect Notion**. Click **Delete account** and confirm: the backend purges the account, and the Notion pages remain.

## Troubleshooting

- **"VITE_API_BASE is required"**: create `.env.local` from `.env.example`.
- **"Link expired" or a 400 at OAuth start**: the extension ID is not in `ALLOWED_EXTENSION_IDS`. Pin the ID with a key.
- **Nothing is tracked**: open the service worker console from `chrome://extensions`. Without a cached `/v1/config`, tracking stays off, because thresholds are never inlined. Check that the backend is reachable.
````

- [ ] **Step 5: Final verification**

```bash
cd /Users/tuandoquoc/personal/reading-chrome-extension/extension
pnpm check
test ! -e package-lock.json && echo "no package-lock.json"
grep -rnE "(idleSeconds|segmentSeconds|syncIntervalMinutes|syncThresholdSegments)\s*[:=]\s*[0-9]" src --include='*.ts' | grep -v '\.test\.ts' || echo "no inlined thresholds"
node -e 'const m=require("./dist/manifest.json"); console.log(m.manifest_version, m.incognito, m.permissions.join(","), JSON.stringify(m).includes("<all_urls>"))'
```

Expected:
- `pnpm check` passes: typecheck clean, every test passing, both builds succeeding.
- `no package-lock.json` is printed.
- `no inlined thresholds` is printed.
- The node line prints `3 not_allowed storage,alarms,identity,scripting false`.
- Together with Step 3's eight PASS lines, this completes the verification.

- [ ] **Step 6: Commit**

```bash
cd /Users/tuandoquoc/personal/reading-chrome-extension
git add extension
git commit -m "test(extension): real-browser smoke test, README and full verification"
```

---

## Spec coverage

| Spec item | Task |
|---|---|
| T25 manifest: permissions, optional hosts, incognito, `API_BASE` from env, build | 1 |
| T26 `normalizeUrl` and its AC URLs | 2 |
| T26 Freedium/Medium canonical and its AC URLs; scenario 23 (new mirror) | 2, 8 |
| T26 generic adapter (JSON-LD → OG → meta; content root), enabled domains only | 2, 8 |
| T27 tracker state machine and its three fake-timer ACs | 3 |
| T27 DOM events, 1 s tick, progress, SPA sessions, `pagehide`, `crypto.randomUUID` | 9 |
| T28 Dexie schema §3.4 | 4 |
| T28 record on segment message; immediate sync at threshold | 4 |
| T28 batches ≤ 50, §2.8 response handling, backoff (1, 2, 4 … 30 min), 401 → disconnected | 6 |
| T28 offline-then-online AC and retried-batch AC | 6 |
| T28 daily `/v1/config` refresh; sync alarm every `sync_interval_minutes` | 6, 10 |
| §3.4 prune synced segments > 30 days | 6, 10 |
| T29 PKCE (64-char verifier), `launchWebAuthFlow`, code/error parsing, token exchange, `/v1/me` | 5, 7, 10 |
| T29 AC: cancel or `access_denied` stores nothing | 7 |
| T30 popup states, today's minutes, current article, last 5, library link (React) | 11 |
| T31 enabled domains (permission + `registerContentScripts`), denied domains, setup picker, disconnect, delete (React) | 7, 10, 12 |
| §2.9 articles status used by the popup | 5, 11 |
| C19 (only the `rt_` token), C23 (no 0-second segment, `pagehide`), C24 (incognito, enabled-only generic), C25 (≤ 50/push) | 1, 3, 4, 6, 7, 9 |
| §7 thresholds, hosts and `API_BASE` never inlined | 1, 2, 7, 13 |
| Review Focus 1–5 | 9, 9, 4, 6, 4 |
