# Reading Tracker — infrastructure

Terraform for the AWS side of the system (spec T2–T7). State is local and gitignored: see section 2. `make check` and `make test` run offline. Everything else needs AWS credentials for the target account (`aws configure` or `AWS_PROFILE`).

For the project-wide C4 architecture diagrams, see [`../docs/c4-architecture.md`](../docs/c4-architecture.md).

**Every code block below starts from the repository root.** For a single end-to-end checklist (local development and first deploy), see [`docs/runbook.md`](../docs/runbook.md).

**Two environments in one AWS account:** dev and prod together would double DynamoDB provisioned capacity to 50 RCU/WCU and leave the Always Free tier (C11). Use separate accounts, or accept the cost.

Running on Kubernetes + Postgres instead? See `backend/README.md` → Deploy to tuncloud.

## Prerequisites

- Terraform ≥ 1.10: `brew install hashicorp/tap/terraform`.
- Go, for the Lambda build. `make plan` runs the backend's `make build` first.
- AWS CLI v2, for the one-time secret setup below.
- Optional, for T7: AWS SAM CLI. `sam local invoke` needs a container runtime. With Podman, start the machine and export `DOCKER_HOST=unix://$(podman machine inspect --format '{{.ConnectionInfo.PodmanSocket.Path}}')`.

## 0. Create the Notion integration

Create a Notion public integration and note its client ID and client secret. Its redirect URI is added after the first apply (see the OAuth redirect note in section 3).

## 1. Create the secrets (once per environment)

Standard-tier SSM parameters (T2). Never put these values in `.tf` or `.tfvars`.

```bash
ENV=dev
read -r NOTION_CLIENT_ID
read -rs NOTION_CLIENT_SECRET   # silent: not echoed, not in shell history
aws ssm put-parameter --tier Standard --type String       --name /reading/$ENV/NOTION_CLIENT_ID     --value "$NOTION_CLIENT_ID"
aws ssm put-parameter --tier Standard --type SecureString --name /reading/$ENV/NOTION_CLIENT_SECRET --value "$NOTION_CLIENT_SECRET"
aws ssm put-parameter --tier Standard --type SecureString --name /reading/$ENV/TOKEN_ENC_KEYS       --value "v1:$(openssl rand -base64 32)"
aws ssm put-parameter --tier Standard --type SecureString --name /reading/$ENV/DEVICE_TOKEN_PEPPER  --value "$(openssl rand -base64 32)"
```

**Key rotation** (scenario 19): prepend a new key, `v2:<b64>,v1:<b64>`, with `--overwrite`, then:

```bash
cd infra/terraform
make plan ENV=dev && make apply ENV=dev
```

**C17:** the values end up in Lambda environment variables and in the local Terraform state. Restrict `lambda:GetFunctionConfiguration` to administrators in your account's IAM.

## 2. Local state: keep it out of git, keep a backup

State lives in `infra/terraform/terraform.tfstate`, on the machine that runs `apply`.
- **Never commit it.** It holds the secret values in plain text. It is gitignored together with `terraform.tfstate.backup`, `*.tfplan` and `*.tfvars`. `git check-ignore infra/terraform/terraform.tfstate` must print the path.
- **Back it up privately** after each apply, for example in an encrypted password-manager attachment or an encrypted disk. Losing it means Terraform no longer knows about the resources, and you have to `terraform import` them or delete them by hand.
- **Run apply from one machine at a time.** Local state has no locking.
- **Orphaned resources** can be found by tag: `aws resourcegroupstaggingapi get-resources --tag-filters Key=app,Values=reading-tracker`.
- **Re-applying after losing the state** fails with "already exists", because the names are fixed. Import the resources or delete them first.
- **Don't keep the repo in a cloud-synced folder**, because the state file holds secrets.

## 3. Plan and apply

```bash
cd infra/terraform
cp example.tfvars dev.tfvars     # fill in: allowed_extension_ids, budget_email
make init
make plan ENV=dev                # builds the bootstraps, then plans
make apply ENV=dev
terraform output function_url
```

`curl $(terraform output -raw function_url)v1/config` returns the config JSON (T5 acceptance criterion).

**OAuth redirect (C14).** Register `<function_url>v1/oauth/notion/callback` as a redirect URI in the Notion integration. The backend derives it from the request host when `public_base_url` is empty. To pin it instead, set `public_base_url` to the Function URL without the trailing slash and apply again. Never wire it to the Function URL resource itself, because that creates a dependency cycle.

**Tearing down:** the DynamoDB table has deletion protection on, so `terraform destroy` fails until you set `deletion_protection_enabled = false` in `dynamodb.tf` and apply that first. This is deliberate: the table holds every user's reading history.

## 4. Point the extension at it

```bash
cd extension
echo "VITE_API_BASE=$(cd ../infra/terraform && terraform output -raw function_url | sed 's:/$::')" > .env.production.local
pnpm build
```

The extension ID in `allowed_extension_ids` must be the ID pinned with the manifest `key` (see `extension/README.md`).

## 5. SAM CLI (optional, T7)

`sam build --hook-name terraform` runs `terraform plan` and apply non-interactively, so it needs the required variables, AWS credentials (SSM is read) and the Lambda bootstraps already built.

```bash
cd infra/terraform
make build
TF_CLI_ARGS_plan=-var-file=dev.tfvars TF_CLI_ARGS_apply=-var-file=dev.tfvars sam build --hook-name terraform
sam local invoke --hook-name terraform 'aws_lambda_function.worker' -e ../../backend/events/sqs-article.json
```

- Pass the var file on that one command only. Do not `export` these variables: `TF_CLI_ARGS_apply` would break `make apply` on a saved plan.
- It needs AWS credentials.
- `sam local invoke` on the worker uses the real dev table, queue and Notion secrets.
- Verify once with your SAM CLI version, as the hook's behavior varies between releases.

## 6. Cloud smoke test (T32)

Use a dev account and a test Notion workspace.

1. Connect Notion from the extension popup and accept the template. The popup shows today's minutes.
2. Read a Freedium article for 3 minutes. Within `D + B + 60 s`, the Library has a page with Reading time ≈ 3 and Progress > 0.
3. Set Tags, Status and Rating in Notion, then read again. Those values stay; the metrics update.
4. A Daily Log row for today exists.
5. Also check the spec's open items:
   - a new data source gets its Status property;
   - an archived page's error text;
   - whether relation properties include `data_source_id`.

   Record the results in the spec's §7.
6. Check the logs (`/aws/lambda/reading-*-dev`, 7-day retention) and that the DLQ is empty.

## Tests

| Command | Does |
|---|---|
| `make check` | `fmt -check`, `init -input=false` and `validate` (offline) |
| `make test` | `terraform test` against mocked providers (offline): table shape, queue, Lambdas, CORS, IAM, logs, budget, input validation |
