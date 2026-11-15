# Reading Tracker — Runbook

Two procedures:
- **Local development:** run the whole system on one machine, and run every test suite.
- **End-to-end first deploy:** deploy the backend to the Kubernetes cluster and get a working extension that writes to Notion.

Each component's README has more detail: [`backend/README.md`](../backend/README.md), [`extension/README.md`](../extension/README.md). The spec is [`reading-tracker-agent-spec.md`](reading-tracker-agent-spec.md).

**Every code block starts from the repository root.**

---

## Prerequisites

| Tool | Needed for | Install (macOS) |
|---|---|---|
| Go 1.26 | backend | `brew install go` |
| Node ≥ 22 and pnpm | extension | `brew install node pnpm` |
| Podman (machine running) | local Postgres | `brew install podman && podman machine init && podman machine start` |
| openssl | secrets, extension key | preinstalled |
| Chrome (or Chromium) | loading the extension | — |
| Chrome for Testing | `pnpm smoke` only | see [Run all tests](#7-run-all-tests) |

---

## Part 1 — Local development

### 1. Pin the extension ID (once)

The backend only accepts OAuth redirects for allow-listed extension IDs. An unpacked extension gets a new ID per folder unless the manifest has a `key`, so generate one key and reuse it everywhere.

```bash
cd extension
pnpm install
cp .env.example .env.local                       # VITE_API_BASE=http://localhost:8080
openssl genrsa 2048 | openssl pkcs8 -topk8 -nocrypt -out extension-key.pem
echo "EXTENSION_KEY=$(openssl rsa -in extension-key.pem -pubout -outform DER | openssl base64 -A)" >> .env.local
openssl rsa -in extension-key.pem -pubout -outform DER | shasum -a 256 | head -c 32 | tr 0-9a-f a-p; echo
```

The last command prints the **extension ID**, 32 letters a–p. Write it down.

`extension-key.pem` is gitignored (`*.pem`), so it never reaches GitHub. Keep a private backup: whoever holds it can publish an extension under your ID.

### 2. Create a Notion integration for development

In Notion's integration settings, create a **public** integration:
- Redirect URI: `http://localhost:8080/v1/oauth/notion/callback`.
- Note the **client ID** and **client secret**.
- Optional: set the template page (spec T24), so that connecting offers "duplicate template". Without it, the user finishes setup in the extension's Options page.

You can skip this step if you only want to work on tracking. Pages are still tracked and stored locally, but nothing syncs until you connect.

### 3. Start the backend

```bash
podman machine start              # if not already running
cd backend
make pg-up                        # Postgres on :5432 (container reading-pg, no volume)
make .env.local                   # generates TOKEN_ENC_KEYS and DEVICE_TOKEN_PEPPER
```

Edit `backend/.env.local`:

```
NOTION_CLIENT_ID=<client id from step 2>
NOTION_CLIENT_SECRET=<client secret from step 2>
ALLOWED_EXTENSION_IDS=<extension id from step 1>
```

```bash
cd backend
make run                          # API on :8080 and the poller; leave it running
```

Check it from another terminal: `curl -s localhost:8080/v1/config` returns JSON starting with `{"version":1,"tracking":`.

`backend/.env.local` is gitignored. Locally, the queue delay and debounce bucket are 5 s, so Notion updates arrive within seconds.

### 4. Build and load the extension

```bash
cd extension
pnpm dev                          # rebuilds dist/ on every save; leave it running
```

In Chrome, open `chrome://extensions`, turn on **Developer mode**, click **Load unpacked**, and pick `extension/dist`. The ID shown must match step 1.

After a code change, `pnpm dev` rebuilds `dist/`. Click the reload icon on the extension's card. Content-script changes also need the open tab reloaded.

### 5. Try it

1. Open the extension popup and click **Connect Notion**. Pick a workspace, then accept the template or pick a page in Options.
2. Read a Medium or Freedium article for a minute or two. Medium pages are tracked without any setup. For other sites, enable the domain in Options first.
3. The popup shows today's minutes. Within about a minute, a Library page appears in Notion. You need 30 seconds of reading before a page is created.

### 6. Stop everything

Stop `make run` (server and poller) and `pnpm dev` with Ctrl-C, then:

```bash
cd backend && make pg-down        # Postgres (no volume: data is gone)
```

### 7. Run all tests

| Suite | Command | Needs |
|---|---|---|
| Backend | `cd backend && gofmt -l . ; go vet ./... && go test -race ./...` | `make pg-up` |
| Extension | `cd extension && pnpm check` | — (typecheck, tests, both builds) |
| Extension in a real browser | `cd extension && pnpm smoke` | the backend running (step 3) and `CHROME_BIN` (below) |

Chrome for Testing, needed by `pnpm smoke` only, because branded Chrome ignores `--load-extension`:

```bash
cd extension
pnpm dlx @puppeteer/browsers install chrome@stable --path "$HOME/.cache/chrome-for-testing"
export CHROME_BIN="$(ls -d "$HOME"/.cache/chrome-for-testing/chrome/*/chrome-mac*/Google\ Chrome\ for\ Testing.app/Contents/MacOS/Google\ Chrome\ for\ Testing | tail -1)"
pnpm smoke                        # expect 8 PASS lines, then "smoke: all checks passed"
```

### Troubleshooting (local)

| Symptom | Cause and fix |
|---|---|
| `make pg-up` hangs or fails | The Podman machine is not running: `podman machine start`. |
| Backend exits with `config: missing env …` | `backend/.env.local` is missing a value. Run `make .env.local`, then fill in the Notion and extension values. |
| "Link expired" or a 400 when connecting | The extension ID is not in `ALLOWED_EXTENSION_IDS`, or the extension was loaded without the key. Repeat step 1, rebuild, then restart `make run`. |
| Nothing is tracked | Open the service-worker console from `chrome://extensions`. The extension needs `/v1/config` from the backend before it tracks, so check that the backend is reachable. |
| Notion never updates | Check the backend log for poller errors. The Notion integration must have access to the page (template or chosen parent). |

---

## Part 2 — End-to-end first deploy

This deploys the backend, `cmd/server` + `cmd/poller` on Postgres, on the tuncloud cluster, and builds the extension against it.

### 1. Have the extension ID

Use the same key and ID as Part 1, step 1. The manifest's `ALLOWED_EXTENSION_IDS` and the Ingress CORS annotation are built from it, so you need it before the first apply.

### 2. Create the Notion integration

Create a Notion **public** integration (you can reuse the development one):
- Note the client ID and client secret.
- Optional: set the template page (spec T24).
- The production redirect URI is added during the deploy (step 3).

### 3. Deploy the backend

The operator steps live in [`backend/README.md`](../backend/README.md) → "Deploy to tuncloud"; follow
them in order.

Once deployed, smoke-test it:

```bash
curl -s https://reading.tuando.app/healthz    # ok
```

### 4. Build the extension for the deployed API

```bash
cd extension
echo "VITE_API_BASE=https://reading.tuando.app" > .env.production.local
pnpm build
```

- `.env.production.local` overrides the localhost URL for `pnpm build`.
- `EXTENSION_KEY` still comes from `.env.local`, so the ID stays the same.
- Load `extension/dist` unpacked as in Part 1, step 4, removing the local build first.
- To distribute it as a zip: `cd extension/dist && zip -r ../reading-tracker.zip .`

`pnpm build` and `pnpm dev` both build in production mode, so while `.env.production.local` exists, both target the deployed API. To develop against the local backend again, delete `extension/.env.production.local`, then rebuild.

### 5. Smoke-test the deployment (spec T32)

1. In the popup, click **Connect Notion** and accept the template. The popup shows today's minutes.
2. Read a Freedium article for 3 minutes. Within about 5 minutes, the Library has a page with Reading time ≈ 3 and Progress > 0. That is D + B + 60 s with the manifest's 120 s values.
3. In Notion, set Tags, Status and Rating, then read again. Those values stay and the metrics update.
4. A Daily Log row for today exists.
5. Check that no sync job has died:

   ```bash
   ssh worker "sudo -u postgres psql reading_tracker -Atc \"select count(*) from sync_jobs where state='dead'\""   # 0
   ```

   A non-zero count means jobs that failed 10 times; inspect their `last_error` column for the cause.

6. Record the spec's open items in its §7:
   - Status property creation;
   - the archived-page error text;
   - whether relations carry `data_source_id`.

### 6. Ship later changes

| Change | Do |
|---|---|
| Backend code | Push to `main`. [`.github/workflows/backend-image.yml`](../.github/workflows/backend-image.yml) runs the tests, builds the image and rolls out the poller, then the server. |
| Extension code | `cd extension && pnpm build`, then reload the extension, or zip `dist/` again. |
| Kubernetes manifest | Edit `backend/deploy/manifest.yaml`, then validate and apply it as in `backend/README.md` → "Deploy to tuncloud", steps 5 and 6. |
