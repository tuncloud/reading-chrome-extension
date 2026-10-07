# Reading Tracker — Chrome extension

Measures active reading time per article. The data is stored in IndexedDB and pushed to the backend, which writes it to the user's Notion. See `docs/reading-tracker-agent-spec.md` (T25–T31) and `docs/reading-tracker-onboarding.md` §5.

## Architecture

Code is organized into five feature folders under `src/`:

| Folder | Runs in | Files |
|---|---|---|
| `shared` | everywhere | `types.ts`, `messages.ts` and `messaging.ts` (message protocol and helpers), `url.ts`, `medium.ts`, `domains.ts`, `reading.ts`, `config.ts`, `errors.ts`, `log.ts` |
| `content` | the page | `index.ts` (entry), `driver.ts` (DOM events to tracker), `tracker.ts` (state machine), `progress.ts`, `sites/{metadata,freedium-medium,generic,registry,types}.ts` |
| `background` | service worker (popup and options also import it) | `index.ts` (listeners), `router.ts`, `services.ts`, `db.ts` (Dexie), `api.ts`, `api-wire.ts`, `sync.ts`, `record.ts`, `config.ts`, `session.ts`, `setup.ts`, `domains.ts`, `pkce.ts`, `device.ts` |
| `popup` | popup page | `main.tsx`, `PopupApp.tsx`, `Popup.tsx`, `model.ts`, `tabs.ts`, `https-url.ts`, `messages.ts` |
| `options` | options page | `main.tsx`, `OptionsApp.tsx`, `OptionsView.tsx`, `controller.ts` |

`test/arch.test.ts` enforces these rules:
- `content/` imports only `content/` and `shared/`, so pages never bundle Dexie or the API client.
- `shared/` imports only `shared/`.
- `background/` imports neither `popup/`, `options/` nor `content/`.
- `popup/` and `options/` may not import `content/`; `popup/` may not import `options/`; `options/` may import only `popup/messages` from `popup/`.
- The `dexie` package is imported only from `background/`.
- React is imported only from `popup/` and `options/`. The import closures of `content/index.ts` and `background/index.ts` are React-free.
- Only the five folders exist under `src/`.

How a read becomes a Notion page:
1. `content/index.ts` asks the background for a tracking plan.
2. `content/driver.ts` feeds DOM events to `content/tracker.ts`.
3. Flushed segments go to the background, where `background/record.ts` stores them.
4. The `sync` alarm runs `background/sync.ts`, which sends them to `POST /v1/sync/push` through `background/api.ts`.
5. The backend queues the Notion write.

Modules call Dexie, `fetch` and `chrome.*` directly. Tests use fake-indexeddb, `test/helpers.ts` (fresh DB, clock, API stub) and `test/chrome.ts` (a `chrome` mock).

For a single end-to-end checklist (local development and first deploy), see [`docs/runbook.md`](../docs/runbook.md).

## Prerequisites

- Node ≥ 22 and pnpm.
- The backend running locally, using Podman for DynamoDB Local:

  ```bash
  cd ../backend && make ddb-up && make run-local
  ```

- For connecting Notion: a Notion public integration whose redirect URI is `http://localhost:8080/v1/oauth/notion/callback`. The backend's `.env.local` must hold its client id and secret, and `ALLOWED_EXTENSION_IDS` must include this extension's ID (next section).
- For `pnpm smoke`: Chrome for Testing (branded Chrome ignores `--load-extension` since version 137):

  ```bash
  pnpm dlx @puppeteer/browsers install chrome@stable --path "$HOME/.cache/chrome-for-testing"
  export CHROME_BIN="$(ls -d "$HOME"/.cache/chrome-for-testing/chrome/*/chrome-mac*/Google\ Chrome\ for\ Testing.app/Contents/MacOS/Google\ Chrome\ for\ Testing | tail -1)"
  ```

  On Linux, `CHROME_BIN` is `…/chrome-linux64/chrome`.

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
| `pnpm test` | unit tests (Vitest, jsdom, fake-indexeddb, React Testing Library) including the folder dependency rules |
| `pnpm typecheck` | `tsc --noEmit` |
| `pnpm dev` | rebuild `dist/` on every save (then reload the extension in `chrome://extensions`) |
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
