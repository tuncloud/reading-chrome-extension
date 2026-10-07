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
