# Reading Tracker — Runbook

Two procedures:
- **Local development:** run the whole system on one machine, and run every test suite.
- **End-to-end first deploy:** go from an empty AWS account to a working extension that writes to Notion.

Each component's README has more detail: [`backend/README.md`](../backend/README.md), [`extension/README.md`](../extension/README.md), [`infra/README.md`](../infra/README.md). The spec is [`reading-tracker-agent-spec.md`](reading-tracker-agent-spec.md).

**Every code block starts from the repository root.**

---

## Prerequisites

| Tool | Needed for | Install (macOS) |
|---|---|---|
| Go 1.26 | backend | `brew install go` |
| Node ≥ 22 and pnpm | extension | `brew install node pnpm` |
| Podman (machine running) | DynamoDB Local | `brew install podman && podman machine init && podman machine start` |
| openssl | secrets, extension key | preinstalled |
| Chrome (or Chromium) | loading the extension | — |
| Chrome for Testing | `pnpm smoke` only | see [Run all tests](#7-run-all-tests) |
| Terraform ≥ 1.10 | deploy | `brew install hashicorp/tap/terraform` |
| AWS CLI v2 + credentials | deploy | `brew install awscli`, then `aws configure` |

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
make ddb-up                       # DynamoDB Local on :8000 (container reading-ddb, in-memory)
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
make run-local                    # API on :8080, worker in-process; leave it running
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

```bash
lsof -ti tcp:8080 | xargs kill    # backend
cd backend && make ddb-down       # DynamoDB Local (in-memory: data is gone)
```

Stop `pnpm dev` with Ctrl-C.

### 7. Run all tests

| Suite | Command | Needs |
|---|---|---|
| Backend | `cd backend && gofmt -l . ; go vet ./... && go test -race ./...` | `make ddb-up` and `make pg-up` |
| Extension | `cd extension && pnpm check` | — (typecheck, tests, both builds) |
| Extension in a real browser | `cd extension && pnpm smoke` | the backend running (step 3) and `CHROME_BIN` (below) |
| Infrastructure | `cd infra/terraform && make check && make test` | Terraform only, no AWS (offline, mocked providers) |

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
| `make ddb-up` hangs or fails | The Podman machine is not running: `podman machine start`. |
| Backend exits with `config: missing env …` | `backend/.env.local` is missing a value. Run `make .env.local`, then fill in the Notion and extension values. |
| "Link expired" or a 400 when connecting | The extension ID is not in `ALLOWED_EXTENSION_IDS`, or the extension was loaded without the key. Repeat step 1, rebuild, then restart `make run-local`. |
| Nothing is tracked | Open the service-worker console from `chrome://extensions`. The extension needs `/v1/config` from the backend before it tracks, so check that the backend is reachable. |
| Notion never updates | Check the backend log for worker errors. The Notion integration must have access to the page (template or chosen parent). |

---

## Part 2 — End-to-end first deploy

This takes you from an empty AWS account to a working extension. It deploys one environment, `dev`. For another environment, repeat from step 3 with a different `ENV`. Use a separate AWS account per environment to stay in the free tier.

### 1. Have the extension ID

Use the same key and ID as Part 1, step 1. Terraform's `allowed_extension_ids` and the Function URL's CORS are built from it, so you need it before the first apply.

### 2. Create the Notion integration

Create a Notion **public** integration (you can reuse the development one):
- Note the client ID and client secret.
- Optional: set the template page (spec T24).
- The production redirect URI is added in step 6, once the API URL exists.

### 3. Store the secrets in SSM

```bash
ENV=dev
read -r NOTION_CLIENT_ID
read -rs NOTION_CLIENT_SECRET     # not echoed, not in shell history
aws ssm put-parameter --tier Standard --type String       --name /reading/$ENV/NOTION_CLIENT_ID     --value "$NOTION_CLIENT_ID"
aws ssm put-parameter --tier Standard --type SecureString --name /reading/$ENV/NOTION_CLIENT_SECRET --value "$NOTION_CLIENT_SECRET"
aws ssm put-parameter --tier Standard --type SecureString --name /reading/$ENV/TOKEN_ENC_KEYS       --value "v1:$(openssl rand -base64 32)"
aws ssm put-parameter --tier Standard --type SecureString --name /reading/$ENV/DEVICE_TOKEN_PEPPER  --value "$(openssl rand -base64 32)"
```

### 4. Configure Terraform

```bash
cd infra/terraform
cp example.tfvars dev.tfvars
```

Edit `infra/terraform/dev.tfvars`:
- `allowed_extension_ids = ["<extension id>"]`
- `budget_email = "<you@…>"`
- Keep `debounce_bucket_seconds = 120` and `debounce_delay_seconds = 120`. The delay must be ≥ the bucket, or `plan` fails.

`dev.tfvars` is gitignored.

### 5. Deploy

```bash
cd infra/terraform
make init
make plan ENV=dev                 # builds both Lambda binaries, then plans; review the plan
make apply ENV=dev                # applies, then deletes the saved plan (it holds secrets)
terraform output -raw function_url
```

Verify:

```bash
cd infra/terraform
curl -s "$(terraform output -raw function_url)v1/config" | head -c 60; echo    # {"version":1,"tracking":...
```

Then confirm the budget-alert email: AWS Budgets sends a subscription confirmation to `budget_email`.

### 6. Register the production redirect URI

In the Notion integration, add `<function_url>v1/oauth/notion/callback` as a redirect URI. `function_url` already ends with `/`, so do not add another. Keep the localhost URI too if you still develop locally.

### 7. Back up the Terraform state

`infra/terraform/terraform.tfstate` now exists. It holds the secrets in plain text and is gitignored.
- Copy it to a private, encrypted place, such as a password-manager attachment.
- Back it up again after every apply.
- Run `apply` from one machine only, because local state has no locking.

`infra/README.md` section 2 covers what to do if the state is lost.

### 8. Build the extension for the deployed API

```bash
cd extension
echo "VITE_API_BASE=$(cd ../infra/terraform && terraform output -raw function_url | sed 's:/$::')" > .env.production.local
pnpm build
```

- `.env.production.local` overrides the localhost URL for `pnpm build`.
- `EXTENSION_KEY` still comes from `.env.local`, so the ID stays the same.
- Load `extension/dist` unpacked as in Part 1, step 4, removing the local build first.
- To distribute it as a zip: `cd extension/dist && zip -r ../reading-tracker.zip .`

`pnpm build` and `pnpm dev` both build in production mode, so while `.env.production.local` exists, both target the deployed API. To develop against the local backend again, delete `extension/.env.production.local`, then rebuild.

### 9. Smoke-test the deployment (spec T32)

1. In the popup, click **Connect Notion** and accept the template. The popup shows today's minutes.
2. Read a Freedium article for 3 minutes. Within about 5 minutes, the Library has a page with Reading time ≈ 3 and Progress > 0. That is D + B + 60 s with the 120 s defaults.
3. In Notion, set Tags, Status and Rating, then read again. Those values stay and the metrics update.
4. A Daily Log row for today exists.
5. Check the logs (`/aws/lambda/reading-api-dev`, `/aws/lambda/reading-worker-dev`) for errors, and check that the dead-letter queue is empty:

   ```bash
   cd infra/terraform
   aws sqs get-queue-attributes --queue-url "$(terraform output -raw dlq_url)" --attribute-names ApproximateNumberOfMessages
   ```

6. Record the spec's open items in its §7:
   - Status property creation;
   - the archived-page error text;
   - whether relations carry `data_source_id`.

### 10. Ship later changes

| Change | Do |
|---|---|
| Backend code | `cd infra/terraform && make plan ENV=dev && make apply ENV=dev`. `plan` rebuilds the binaries. Back up the state afterwards. |
| Extension code | `cd extension && pnpm build`, then reload the extension, or zip `dist/` again. |
| Infrastructure | Edit `infra/terraform`, run `make check && make test`, then plan and apply. Back up the state afterwards. |
| Rotate `TOKEN_ENC_KEYS` | Prepend the new key (`v2:<b64>,v1:<b64>`) with `aws ssm put-parameter --overwrite`, then plan and apply (spec scenario 19). |
| Tear down | Set `deletion_protection_enabled = false` in `dynamodb.tf`, apply, then `terraform destroy -var-file=dev.tfvars`. Notion pages are not touched. |

---

## Part 3 — Kubernetes (Postgres)

This deploys the second mode: `cmd/server` + `cmd/poller` on Postgres, on the tuncloud cluster. The
operator steps live in [`backend/README.md`](../backend/README.md) → "Deploy to tuncloud"; follow
them in order.

Once deployed, smoke-test it:

```bash
curl -s https://reading.tuando.app/healthz    # ok
```

Then build the extension against it:

```bash
cd extension
echo "VITE_API_BASE=https://reading.tuando.app" > .env.production.local
pnpm build
```

Load it and repeat [Part 2, step 9](#9-smoke-test-the-deployment-spec-t32) against this build,
exactly as for the Lambda deployment.

In place of the dead-letter queue check, count dead jobs:

```bash
ssh worker "sudo -u postgres psql reading_tracker -Atc \"select count(*) from sync_jobs where state='dead'\""   # 0
```

A non-zero count means jobs that failed 10 times; inspect their `last_error` column for the cause.
