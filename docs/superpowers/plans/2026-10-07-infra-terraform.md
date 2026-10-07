# Infrastructure (Terraform) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Describe every AWS resource the Reading Tracker needs in Terraform (spec T2–T7): SSM-sourced secrets, the DynamoDB table, the SQS FIFO queue and DLQ, the two Go Lambdas with a public Function URL and an SQS trigger, least-privilege IAM, 7-day logs, a $1 budget and SAM CLI metadata. Each piece is verified offline by `terraform test`, and a runbook covers the one-time apply and the T32 cloud smoke test.

**Architecture:** One stack in `infra/terraform`, using Terraform's default **local state** (`terraform.tfstate`).
- This replaces spec T1's S3 backend, at the user's request.
- The repository is pushed to GitHub, and the state contains the SSM secret values, so the state file is gitignored and must never be committed.
- The Lambda zips are built by the backend's `make build`. The infra Makefile's `plan` target runs it first, and `archive_file` zips the bootstraps.
- Secrets are never in `.tf` or `.tfvars`. They are read from SSM parameters that are created by hand.
- Every behavioural requirement is pinned by a `terraform test` run against mocked providers, so CI and reviews need no AWS account.

**Tech Stack:** Terraform ≥ 1.10 (validated with 1.16.5); providers `hashicorp/aws` ~> 6.0 (6.67), `hashicorp/archive` ~> 2.8 and `hashicorp/null` ~> 3.2; GNU make; Go (backend build).

**Spec:** `docs/reading-tracker-agent-spec.md` §1 decisions D3–D8, §3.1, §3.5, §4.2 T1–T7 and T32, constraints C5, C10, C11, C14, C16, C17, C21, C26 and C27, and §7.

**Pre-validated:** every file in this plan was written and run in a scratch copy with Terraform 1.16.5 and the real provider schemas before the plan was written.
- `fmt -check` and `validate` are clean, and `terraform test` passes 16 runs (8 behaviour, 8 validation).
- Breaking the CORS methods and the memory size made 2 runs fail, which shows the assertions bite.

## Global Constraints

- **Tools.** Terraform ≥ 1.10 on PATH; on macOS, `brew install hashicorp/tap/terraform`. Every Makefile target also accepts `TF=/path/to/terraform`.
  - Use pnpm, never npm/npx/yarn (extension), and podman, never docker (DynamoDB Local).
- **Offline is the default.** `make check` and `make test` need no AWS credentials. Only `make plan/apply` and the runbook touch AWS.
- **Secrets.** No secret literal appears in any `.tf`, `.tfvars` or committed file (T2, C17). The state file holds the secret values, so it is never committed: `git check-ignore` must match it.
- **Naming.**
  - Table: `reading-<env>`.
  - Queue: `notion-sync-<env>.fifo`, DLQ `notion-sync-dlq-<env>.fifo`.
  - Functions: `reading-api-<env>` and `reading-worker-<env>`.
  - SSM: `/reading/<env>/<NAME>`.
- **DynamoDB.** PROVISIONED 25 RCU / 25 WCU, `pk`/`sk` strings, TTL on `ttl`, no GSIs, PITR off, autoscaling off (T3, C11).
- **SQS** (T4, C5, C21):
  - FIFO, with `content_based_deduplication = false`;
  - `delay_seconds = D` and `visibility_timeout_seconds = 360`;
  - FIFO DLQ kept 14 days, `maxReceiveCount = 10`;
  - D ≥ B, B ≤ 300, D ≤ 900, enforced by variable validation so `plan` fails.
- **Lambdas** (T5, C16, C27):
  - Both run `provided.al2023`, `arm64`, handler `bootstrap`, 128 MB.
  - Timeouts: api 10 s, worker 60 s.
  - Worker event source: batch 10, `ReportBatchItemFailures`, `scaling_config.maximum_concurrency = 2`.
  - No reserved concurrency (C27).
- **Function URL** (T5, C26):
  - `authorization_type = "NONE"`.
  - CORS allows only `chrome-extension://<id>` for each allowed id, methods GET/POST/DELETE, headers `authorization` and `content-type`.
  - A resource policy allows unauthenticated callers through both `lambda:InvokeFunctionUrl` and `lambda:InvokeFunction` with `invoked_via_function_url`.
- **Environment variables** (spec §3.5 and the backend's `internal/platform/config`):
  - Both functions: `APP_ENV`, `TABLE_NAME`, `QUEUE_URL`, `NOTION_CLIENT_ID`, `NOTION_CLIENT_SECRET`, `NOTION_VERSION=2025-09-03`, `TOKEN_ENC_KEYS`, and `TRACKING_CONFIG_JSON` only when set.
  - api only: `DEVICE_TOKEN_PEPPER`, `ALLOWED_EXTENSION_IDS` (comma-joined), `PUBLIC_BASE_URL`, `DEBOUNCE_BUCKET_SECONDS`.
  - `PUBLIC_BASE_URL` is a variable and never references this stack's Function URL (C14).
- **IAM** (T5): least privilege, each role scoped to this table, this queue and its own log group only.
  - api: DynamoDB `GetItem PutItem UpdateItem DeleteItem Query BatchGetItem TransactWriteItems`, plus `sqs:SendMessage`.
  - worker: the same DynamoDB actions plus `BatchWriteItem`, and SQS `ReceiveMessage DeleteMessage ChangeMessageVisibility GetQueueAttributes SendMessage`. `SendMessage` also authorizes the backend's `SendMessageBatch`.
- **Logs and budget.** Log groups are created explicitly with 7-day retention (T5). The budget is $1 monthly COST, with an alert at 80% ACTUAL to `budget_email` (T6).
- **State** (replaces T1, user decision).
  - Local state, with no `backend` block. `terraform.tfstate`, `terraform.tfstate.backup`, `*.tfplan` and `*.tfvars` (except `example.tfvars`) are gitignored, because state and saved plans contain the secret values.
  - `.terraform.lock.hcl` **is** committed, to pin provider versions.
  - The runbook tells the operator to keep a private backup of the state, because losing it orphans the resources.

## Review Focus

1. **State committed to GitHub.** Local `terraform.tfstate` holds `NOTION_CLIENT_SECRET`, `TOKEN_ENC_KEYS` and `DEVICE_TOKEN_PEPPER` in plain text. It must be ignored by git (Task 1 and Task 3 verification: `git check-ignore infra/terraform/terraform.tfstate infra/terraform/terraform.tfstate.backup infra/terraform/dev.tfplan infra/terraform/dev.tfvars` prints all four paths).
2. **D < B in someone's tfvars.** The last updates in a bucket would silently never reach Notion, and the backend's deleteMe ordering relies on D exceeding the handler runtime. `plan` must fail with a clear message (Task 1 test `delay_below_bucket_is_rejected`).
3. **CORS without DELETE or Authorization.** Account deletion (`DELETE /v1/me`) and every authenticated call would fail from the extension's pages (Task 2 test `function_url_is_public_with_extension_cors`).
4. **A secret leaking into tfvars or an api-only secret reaching the worker.** The worker must not get `DEVICE_TOKEN_PEPPER` or `ALLOWED_EXTENSION_IDS`, and secrets must come from SSM data (Task 2 test `lambdas_are_arm64_al2023_with_the_right_env`).
5. **A wildcard sneaking into IAM** (Task 2 test `iam_is_least_privilege`, which also asserts that no `"*"` appears in either policy).

## File Structure

```
infra/
├── README.md                    # runbook: secrets, apply, local state safety, extension wiring, T32 smoke
└── terraform/                   # the stack (local state, gitignored)
    ├── Makefile  .gitignore  example.tfvars  .terraform.lock.hcl (committed)
    ├── versions.tf  providers.tf  variables.tf  locals.tf
    ├── ssm.tf  dynamodb.tf  sqs.tf            # Task 1
    ├── logs.tf  iam.tf  lambda.tf             # Task 2
    ├── budget.tf  sam-metadata.tf  outputs.tf # Task 3
    └── tests/stack.tftest.hcl  tests/validation.tftest.hcl
```

---

### Task 1: Application stack foundation: providers, variables, SSM, DynamoDB, SQS

**Files:**
- Create: `infra/terraform/{versions,providers,variables,locals,ssm,dynamodb,sqs}.tf`, `.gitignore`, `.terraform.lock.hcl` (generated by `terraform init`, committed)
- Create: `infra/terraform/tests/stack.tftest.hcl` (mocks + DynamoDB/SQS runs), `infra/terraform/tests/validation.tftest.hcl`

**Interfaces:**
- **Produces** these resources:
  - `aws_dynamodb_table.main` (`arn`, `name`);
  - `aws_sqs_queue.sync` and `aws_sqs_queue.dlq` (`arn`, `url`);
  - the four `data.aws_ssm_parameter.*` data sources.
- **Produces** these locals: `local.name`, `local.notion_version`, `local.ssm_prefix`, `local.api_name`, `local.worker_name`.
- **Produces** these variables: `region`, `env`, `allowed_extension_ids`, `public_base_url`, `debounce_bucket_seconds`, `debounce_delay_seconds`, `tracking_config_json`, `budget_email`, `backend_dir`.

- [ ] **Step 1: Write the failing tests**

`infra/terraform/tests/stack.tftest.hcl` starts with the mocks and the shared variables, followed by the first two runs. Tasks 2 and 3 append further runs to this file:

```hcl
# Offline tests: every provider is mocked, so `terraform test` needs no AWS account.

mock_provider "aws" {
  mock_data "aws_ssm_parameter" {
    defaults = {
      value = "mock-secret"
    }
  }
  mock_resource "aws_dynamodb_table" {
    defaults = {
      arn = "arn:aws:dynamodb:ap-southeast-1:123456789012:table/reading-test"
    }
  }
  mock_resource "aws_sqs_queue" {
    defaults = {
      arn = "arn:aws:sqs:ap-southeast-1:123456789012:notion-sync-test.fifo"
      url = "https://sqs.ap-southeast-1.amazonaws.com/123456789012/notion-sync-test.fifo"
    }
  }
  mock_resource "aws_cloudwatch_log_group" {
    defaults = {
      arn = "arn:aws:logs:ap-southeast-1:123456789012:log-group:/aws/lambda/mock"
    }
  }
  mock_resource "aws_iam_role" {
    defaults = {
      arn = "arn:aws:iam::123456789012:role/mock"
    }
  }
  mock_resource "aws_lambda_function" {
    defaults = {
      arn = "arn:aws:lambda:ap-southeast-1:123456789012:function:mock"
    }
  }
}

mock_provider "archive" {
  mock_data "archive_file" {
    defaults = {
      output_base64sha256 = "bW9jaw=="
    }
  }
}

mock_provider "null" {}

variables {
  env                   = "test"
  allowed_extension_ids = ["abcdefghijklmnopabcdefghijklmnop"]
  budget_email          = "owner@example.com"
}

run "dynamodb_is_always_free_shaped" {
  command = apply

  assert {
    condition     = aws_dynamodb_table.main.billing_mode == "PROVISIONED" && aws_dynamodb_table.main.read_capacity == 25 && aws_dynamodb_table.main.write_capacity == 25
    error_message = "T3/C11: PROVISIONED 25/25"
  }
  assert {
    condition     = length(aws_dynamodb_table.main.global_secondary_index) == 0
    error_message = "C11: no GSIs"
  }
  assert {
    condition     = aws_dynamodb_table.main.ttl[0].enabled && aws_dynamodb_table.main.ttl[0].attribute_name == "ttl"
    error_message = "T3: TTL on ttl"
  }
  assert {
    condition     = aws_dynamodb_table.main.point_in_time_recovery[0].enabled == false
    error_message = "C11: PITR off"
  }
  assert {
    condition     = aws_dynamodb_table.main.name == "reading-test" && aws_dynamodb_table.main.hash_key == "pk" && aws_dynamodb_table.main.range_key == "sk"
    error_message = "§3.1: table reading-<env> with pk/sk"
  }
}

run "queue_is_fifo_debounced_with_dlq" {
  command = apply

  assert {
    condition     = aws_sqs_queue.sync.fifo_queue && !aws_sqs_queue.sync.content_based_deduplication && aws_sqs_queue.sync.name == "notion-sync-test.fifo"
    error_message = "T4: FIFO without content-based dedup"
  }
  assert {
    condition     = aws_sqs_queue.sync.delay_seconds == 120 && aws_sqs_queue.sync.visibility_timeout_seconds == 360
    error_message = "T4: delay D and visibility 360"
  }
  assert {
    condition     = jsondecode(aws_sqs_queue.sync.redrive_policy).maxReceiveCount == 10 && jsondecode(aws_sqs_queue.sync.redrive_policy).deadLetterTargetArn == aws_sqs_queue.dlq.arn
    error_message = "T4: redrive to the DLQ after 10 receives"
  }
  assert {
    condition     = aws_sqs_queue.dlq.fifo_queue && aws_sqs_queue.dlq.message_retention_seconds == 1209600
    error_message = "T4: FIFO DLQ kept 14 days"
  }
}
```

`infra/terraform/tests/validation.tftest.hcl`:

```hcl
# Input validation: bad values must be rejected at plan time.

mock_provider "aws" {}
mock_provider "archive" {}
mock_provider "null" {}

variables {
  env                   = "test"
  allowed_extension_ids = ["abcdefghijklmnopabcdefghijklmnop"]
  budget_email          = "owner@example.com"
}

run "delay_below_bucket_is_rejected" {
  command = plan
  variables {
    debounce_bucket_seconds = 120
    debounce_delay_seconds  = 60
  }
  expect_failures = [var.debounce_delay_seconds]
}

run "bucket_above_300_is_rejected" {
  command = plan
  variables {
    debounce_bucket_seconds = 301
    debounce_delay_seconds  = 400
  }
  expect_failures = [var.debounce_bucket_seconds]
}

run "delay_above_sqs_max_is_rejected" {
  command = plan
  variables {
    debounce_delay_seconds = 901
  }
  expect_failures = [var.debounce_delay_seconds]
}

run "extension_ids_must_be_chrome_ids" {
  command = plan
  variables {
    allowed_extension_ids = ["not-an-id"]
  }
  expect_failures = [var.allowed_extension_ids]
}

run "at_least_one_extension_id" {
  command = plan
  variables {
    allowed_extension_ids = []
  }
  expect_failures = [var.allowed_extension_ids]
}

run "budget_email_must_be_an_email" {
  command = plan
  variables {
    budget_email = "nobody"
  }
  expect_failures = [var.budget_email]
}

run "env_name_is_restricted" {
  command = plan
  variables {
    env = "Prod_1"
  }
  expect_failures = [var.env]
}

run "equal_delay_and_bucket_is_accepted" {
  command = plan
  variables {
    debounce_bucket_seconds = 300
    debounce_delay_seconds  = 300
  }
}
```

- [ ] **Step 2: Run them to see them fail**

Run: `cd infra/terraform && terraform init -input=false && terraform test`
Expected: FAIL. Terraform reports undeclared resources and variables.

- [ ] **Step 3: Write the foundation**

`infra/terraform/versions.tf`:

```hcl
terraform {
  required_version = ">= 1.10"
  required_providers {
    aws     = { source = "hashicorp/aws", version = "~> 6.0" }
    archive = { source = "hashicorp/archive", version = "~> 2.8" }
    null    = { source = "hashicorp/null", version = "~> 3.2" }
  }
}
```

`infra/terraform/providers.tf`:

```hcl
provider "aws" {
  region = var.region
  default_tags {
    tags = { app = "reading-tracker", env = var.env }
  }
}
```

`infra/terraform/variables.tf`:

```hcl
variable "region" {
  description = "AWS region (spec §7: placeholder ap-southeast-1)."
  type        = string
  default     = "ap-southeast-1"
}

variable "env" {
  description = "Environment name used in resource names, e.g. dev or prod."
  type        = string
  validation {
    condition     = can(regex("^[a-z0-9-]{1,16}$", var.env))
    error_message = "env must be 1-16 chars of a-z, 0-9 or -."
  }
}

variable "allowed_extension_ids" {
  description = "Chrome extension IDs allowed to connect (ALLOWED_EXTENSION_IDS and Function URL CORS)."
  type        = list(string)
  validation {
    condition     = length(var.allowed_extension_ids) > 0 && alltrue([for id in var.allowed_extension_ids : can(regex("^[a-p]{32}$", id))])
    error_message = "allowed_extension_ids needs at least one 32-char Chrome extension ID (letters a-p)."
  }
}

variable "public_base_url" {
  description = "PUBLIC_BASE_URL for the OAuth redirect; empty derives it from the request Host (C14). Never reference this stack's Function URL here."
  type        = string
  default     = ""
}

variable "debounce_bucket_seconds" {
  description = "B: debounce bucket for queue dedup IDs (C5: 1..300)."
  type        = number
  default     = 120
  validation {
    condition     = var.debounce_bucket_seconds >= 1 && var.debounce_bucket_seconds <= 300
    error_message = "debounce_bucket_seconds must be 1..300 (C5)."
  }
}

variable "debounce_delay_seconds" {
  description = "D: queue delivery delay (C5: D >= B; SQS max 900)."
  type        = number
  default     = 120
  validation {
    condition     = var.debounce_delay_seconds >= var.debounce_bucket_seconds && var.debounce_delay_seconds <= 900
    error_message = "debounce_delay_seconds must be >= debounce_bucket_seconds (C5: D >= B) and <= 900."
  }
}

variable "tracking_config_json" {
  description = "Optional TRACKING_CONFIG_JSON override for /v1/config; empty uses the backend defaults."
  type        = string
  default     = ""
}

variable "budget_email" {
  description = "Email that receives the $1 monthly budget alert (T6)."
  type        = string
  validation {
    condition     = can(regex("^[^@\\s]+@[^@\\s]+\\.[^@\\s]+$", var.budget_email))
    error_message = "budget_email must be an email address."
  }
}

variable "backend_dir" {
  description = "Path to the Go backend module that builds the Lambda bootstraps."
  type        = string
  default     = "../../backend"
}
```

`locals.tf` is the single source for every name, including the two functions whose resources arrive in Task 2:

`infra/terraform/locals.tf`:

```hcl
locals {
  name           = "reading-${var.env}"
  notion_version = "2025-09-03" # C10: pinned; the backend refuses any other value
  ssm_prefix     = "/reading/${var.env}"
  api_name       = "reading-api-${var.env}"
  worker_name    = "reading-worker-${var.env}"
}
```

`infra/terraform/ssm.tf`:

```hcl
# Secrets are created by hand (README) as Standard-tier SSM parameters and only read here (T2, C17).
data "aws_ssm_parameter" "notion_client_id" {
  name = "${local.ssm_prefix}/NOTION_CLIENT_ID"
}

data "aws_ssm_parameter" "notion_client_secret" {
  name            = "${local.ssm_prefix}/NOTION_CLIENT_SECRET"
  with_decryption = true
}

data "aws_ssm_parameter" "token_enc_keys" {
  name            = "${local.ssm_prefix}/TOKEN_ENC_KEYS"
  with_decryption = true
}

data "aws_ssm_parameter" "device_token_pepper" {
  name            = "${local.ssm_prefix}/DEVICE_TOKEN_PEPPER"
  with_decryption = true
}
```

`infra/terraform/dynamodb.tf`:

```hcl
# Single table, PROVISIONED 25/25, no GSIs, no autoscaling, PITR off (spec §3.1, T3, C11).
resource "aws_dynamodb_table" "main" {
  name           = local.name
  billing_mode   = "PROVISIONED"
  read_capacity  = 25
  write_capacity = 25
  hash_key       = "pk"
  range_key      = "sk"

  attribute {
    name = "pk"
    type = "S"
  }

  attribute {
    name = "sk"
    type = "S"
  }

  ttl {
    attribute_name = "ttl"
    enabled        = true
  }

  point_in_time_recovery {
    enabled = false
  }

  deletion_protection_enabled = true
}
```

`infra/terraform/sqs.tf`:

```hcl
# notion-sync FIFO queue + DLQ (spec T4, D6, C4, C5, C21).
resource "aws_sqs_queue" "dlq" {
  name                        = "notion-sync-dlq-${var.env}.fifo"
  fifo_queue                  = true
  content_based_deduplication = false
  message_retention_seconds   = 1209600 # 14 days
}

resource "aws_sqs_queue" "sync" {
  name                        = "notion-sync-${var.env}.fifo"
  fifo_queue                  = true
  content_based_deduplication = false
  delay_seconds               = var.debounce_delay_seconds
  visibility_timeout_seconds  = 360 # 6x the worker timeout

  redrive_policy = jsonencode({
    deadLetterTargetArn = aws_sqs_queue.dlq.arn
    maxReceiveCount     = 10
  })
}
```

`infra/terraform/.gitignore`:

```text
.terraform/
.build/
*.tfplan
*.tfstate
*.tfstate.*
*.tfvars
!example.tfvars
```

- [ ] **Step 4: Run the checks**

Run:

```bash
cd /Users/tuandoquoc/personal/reading-chrome-extension/infra/terraform
terraform fmt -check -recursive && terraform validate && terraform test
touch terraform.tfstate terraform.tfstate.backup dev.tfplan dev.tfvars
git check-ignore terraform.tfstate terraform.tfstate.backup dev.tfplan dev.tfvars; git check-ignore -q example.tfvars || echo "example.tfvars is tracked"
rm terraform.tfstate terraform.tfstate.backup dev.tfplan dev.tfvars
```

Expected:
- `Success! 10 passed, 0 failed.` That is the 2 stack runs plus the 8 validation runs.
- `git check-ignore` prints all four paths.
- The last check prints `example.tfvars is tracked`.

- [ ] **Step 5: Commit**

```bash
cd /Users/tuandoquoc/personal/reading-chrome-extension
git add infra/terraform
git diff --cached --name-only | grep -E 'tfstate|tfplan|\.tfvars$' && { echo 'STOP: state, a plan or tfvars is staged'; exit 1; }
git commit -m "feat(infra): providers, local gitignored state, variables with C5 validation, SSM secrets, DynamoDB and the SQS FIFO queue"
```

---

### Task 2: Lambdas, Function URL, SQS trigger, IAM and logs

**Files:**
- Create: `infra/terraform/{logs,iam,lambda}.tf`, `infra/terraform/Makefile`, `infra/terraform/example.tfvars`
- Modify: `infra/terraform/tests/stack.tftest.hcl` (append the five runs below)

**Interfaces:**
- **Consumes** from Task 1: the table, the queues, the SSM data sources, the locals and the variables.
- **Produces:**
  - `aws_lambda_function.api` and `aws_lambda_function.worker`;
  - `aws_lambda_function_url.api` (`function_url`);
  - `aws_lambda_event_source_mapping.worker`;
  - `aws_iam_role(_policy).api/worker`;
  - `aws_cloudwatch_log_group.api/worker`;
  - `data.archive_file.api/worker` (`output_path`, used by Task 3's SAM metadata).
- **Produces** the Makefile targets `build init check test plan apply`.

- [ ] **Step 1: Append the failing tests**

Append to `infra/terraform/tests/stack.tftest.hcl`:

```hcl
run "lambdas_are_arm64_al2023_with_the_right_env" {
  command = apply

  assert {
    condition     = alltrue([for f in [aws_lambda_function.api, aws_lambda_function.worker] : f.runtime == "provided.al2023" && f.handler == "bootstrap" && f.architectures == tolist(["arm64"]) && f.memory_size == 128])
    error_message = "T5/C16: provided.al2023, bootstrap, arm64, 128 MB"
  }
  assert {
    condition     = aws_lambda_function.api.timeout == 10 && aws_lambda_function.worker.timeout == 60
    error_message = "T5: api 10 s, worker 60 s"
  }
  assert {
    condition     = aws_lambda_function.api.environment[0].variables["NOTION_VERSION"] == "2025-09-03" && aws_lambda_function.worker.environment[0].variables["NOTION_VERSION"] == "2025-09-03"
    error_message = "C10: Notion version pinned"
  }
  assert {
    condition = alltrue([for k in ["APP_ENV", "TABLE_NAME", "QUEUE_URL", "NOTION_CLIENT_ID", "NOTION_CLIENT_SECRET", "NOTION_VERSION", "TOKEN_ENC_KEYS",
    "DEVICE_TOKEN_PEPPER", "ALLOWED_EXTENSION_IDS", "PUBLIC_BASE_URL", "DEBOUNCE_BUCKET_SECONDS"] : contains(keys(aws_lambda_function.api.environment[0].variables), k)])
    error_message = "§3.5: api gets every variable it requires"
  }
  assert {
    condition     = !contains(keys(aws_lambda_function.worker.environment[0].variables), "DEVICE_TOKEN_PEPPER") && !contains(keys(aws_lambda_function.worker.environment[0].variables), "ALLOWED_EXTENSION_IDS")
    error_message = "worker does not receive api-only secrets"
  }
  assert {
    condition     = aws_lambda_function.api.environment[0].variables["TOKEN_ENC_KEYS"] == "mock-secret" && aws_lambda_function.api.environment[0].variables["DEBOUNCE_BUCKET_SECONDS"] == "120"
    error_message = "T2: secrets come from SSM data sources"
  }
  assert {
    condition     = !contains(keys(aws_lambda_function.api.environment[0].variables), "TRACKING_CONFIG_JSON")
    error_message = "TRACKING_CONFIG_JSON is omitted when empty"
  }
}

run "function_url_is_public_with_extension_cors" {
  command = apply

  assert {
    condition     = aws_lambda_function_url.api.authorization_type == "NONE"
    error_message = "C26: NONE auth; the device token is checked in the app"
  }
  assert {
    condition     = aws_lambda_function_url.api.cors[0].allow_origins == toset(["chrome-extension://abcdefghijklmnopabcdefghijklmnop"])
    error_message = "T5: CORS allows only the extension origins"
  }
  assert {
    condition     = contains(aws_lambda_function_url.api.cors[0].allow_methods, "DELETE") && contains(aws_lambda_function_url.api.cors[0].allow_headers, "authorization")
    error_message = "DELETE /v1/me and the Authorization header must pass CORS"
  }
  assert {
    condition     = aws_lambda_permission.url_public.action == "lambda:InvokeFunctionUrl" && aws_lambda_permission.url_public.function_url_auth_type == "NONE" && aws_lambda_permission.url_invoke.invoked_via_function_url
    error_message = "C26: resource policy allows unauthenticated Function URL calls"
  }
}

run "worker_consumes_the_queue_in_order" {
  command = apply

  assert {
    condition     = aws_lambda_event_source_mapping.worker.batch_size == 10 && aws_lambda_event_source_mapping.worker.function_response_types == toset(["ReportBatchItemFailures"])
    error_message = "T5/C21: batch 10 with partial batch responses"
  }
  assert {
    condition     = aws_lambda_event_source_mapping.worker.scaling_config[0].maximum_concurrency == 2
    error_message = "C27: SQS maximum_concurrency 2"
  }
  assert {
    condition     = aws_lambda_event_source_mapping.worker.event_source_arn == aws_sqs_queue.sync.arn
    error_message = "worker reads the sync queue"
  }
}

run "iam_is_least_privilege" {
  command = apply

  assert {
    condition = toset(jsondecode(aws_iam_role_policy.api.policy).Statement[0].Action) == toset([
      "dynamodb:GetItem", "dynamodb:PutItem", "dynamodb:UpdateItem", "dynamodb:DeleteItem",
    "dynamodb:Query", "dynamodb:BatchGetItem", "dynamodb:TransactWriteItems"])
    error_message = "T5: api DynamoDB actions"
  }
  assert {
    condition     = jsondecode(aws_iam_role_policy.api.policy).Statement[0].Resource == aws_dynamodb_table.main.arn && jsondecode(aws_iam_role_policy.api.policy).Statement[1].Resource == aws_sqs_queue.sync.arn
    error_message = "T5: api policy references only this table and queue"
  }
  assert {
    condition     = contains(jsondecode(aws_iam_role_policy.worker.policy).Statement[0].Action, "dynamodb:BatchWriteItem") && !contains(jsondecode(aws_iam_role_policy.api.policy).Statement[0].Action, "dynamodb:BatchWriteItem")
    error_message = "only the worker may BatchWrite (purge)"
  }
  assert {
    condition = toset(jsondecode(aws_iam_role_policy.worker.policy).Statement[1].Action) == toset([
    "sqs:ReceiveMessage", "sqs:DeleteMessage", "sqs:ChangeMessageVisibility", "sqs:GetQueueAttributes", "sqs:SendMessage"])
    error_message = "T5: worker SQS actions"
  }
  assert {
    condition     = !strcontains(aws_iam_role_policy.api.policy, "\"*\"") && !strcontains(aws_iam_role_policy.worker.policy, "\"*\"")
    error_message = "no wildcard resources or actions"
  }
}

run "tracking_config_override_reaches_both_lambdas" {
  command = apply

  variables {
    tracking_config_json = "{\"version\":2}"
  }

  assert {
    condition     = aws_lambda_function.api.environment[0].variables["TRACKING_CONFIG_JSON"] == "{\"version\":2}" && aws_lambda_function.worker.environment[0].variables["TRACKING_CONFIG_JSON"] == "{\"version\":2}"
    error_message = "TRACKING_CONFIG_JSON is passed through when set"
  }
}
```

- [ ] **Step 2: Run them to see them fail**

Run: `cd infra/terraform && terraform test`
Expected: FAIL, with references to undeclared resources (`aws_lambda_function.api`, …).

- [ ] **Step 3: Write logs, IAM and Lambdas**

`infra/terraform/logs.tf`:

```hcl
# Explicit log groups so retention is 7 days instead of never-expire (T5).
resource "aws_cloudwatch_log_group" "api" {
  name              = "/aws/lambda/${local.api_name}"
  retention_in_days = 7
}

resource "aws_cloudwatch_log_group" "worker" {
  name              = "/aws/lambda/${local.worker_name}"
  retention_in_days = 7
}
```

`infra/terraform/iam.tf`:

```hcl
# Least-privilege roles: only this table, this queue, and each function's own log group (T5).
locals {
  assume_lambda = jsonencode({
    Version   = "2012-10-17"
    Statement = [{ Effect = "Allow", Principal = { Service = "lambda.amazonaws.com" }, Action = "sts:AssumeRole" }]
  })

  api_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Effect = "Allow"
        Action = [
          "dynamodb:GetItem", "dynamodb:PutItem", "dynamodb:UpdateItem", "dynamodb:DeleteItem",
          "dynamodb:Query", "dynamodb:BatchGetItem", "dynamodb:TransactWriteItems",
        ]
        Resource = aws_dynamodb_table.main.arn
      },
      { Effect = "Allow", Action = ["sqs:SendMessage"], Resource = aws_sqs_queue.sync.arn },
      { Effect = "Allow", Action = ["logs:CreateLogStream", "logs:PutLogEvents"], Resource = "${aws_cloudwatch_log_group.api.arn}:*" },
    ]
  })

  worker_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Effect = "Allow"
        Action = [
          "dynamodb:GetItem", "dynamodb:PutItem", "dynamodb:UpdateItem", "dynamodb:DeleteItem",
          "dynamodb:Query", "dynamodb:BatchGetItem", "dynamodb:BatchWriteItem", "dynamodb:TransactWriteItems",
        ]
        Resource = aws_dynamodb_table.main.arn
      },
      {
        Effect = "Allow"
        Action = [
          "sqs:ReceiveMessage", "sqs:DeleteMessage", "sqs:ChangeMessageVisibility",
          "sqs:GetQueueAttributes", "sqs:SendMessage",
        ]
        Resource = aws_sqs_queue.sync.arn
      },
      { Effect = "Allow", Action = ["logs:CreateLogStream", "logs:PutLogEvents"], Resource = "${aws_cloudwatch_log_group.worker.arn}:*" },
    ]
  })
}

resource "aws_iam_role" "api" {
  name               = local.api_name
  assume_role_policy = local.assume_lambda
}

resource "aws_iam_role_policy" "api" {
  name   = "${local.api_name}-access"
  role   = aws_iam_role.api.id
  policy = local.api_policy
}

resource "aws_iam_role" "worker" {
  name               = local.worker_name
  assume_role_policy = local.assume_lambda
}

resource "aws_iam_role_policy" "worker" {
  name   = "${local.worker_name}-access"
  role   = aws_iam_role.worker.id
  policy = local.worker_policy
}
```

`infra/terraform/lambda.tf`:

```hcl
# Two Go Lambdas on provided.al2023/arm64 (spec T5, C16). The bootstraps are built by
# `make build` in infra/terraform (which runs the backend's make build) before plan/apply.

locals {
  shared_env = merge(
    {
      APP_ENV              = var.env
      TABLE_NAME           = aws_dynamodb_table.main.name
      QUEUE_URL            = aws_sqs_queue.sync.url
      NOTION_CLIENT_ID     = data.aws_ssm_parameter.notion_client_id.value
      NOTION_CLIENT_SECRET = data.aws_ssm_parameter.notion_client_secret.value
      NOTION_VERSION       = local.notion_version
      TOKEN_ENC_KEYS       = data.aws_ssm_parameter.token_enc_keys.value
    },
    var.tracking_config_json == "" ? {} : { TRACKING_CONFIG_JSON = var.tracking_config_json },
  )

  api_env = merge(local.shared_env, {
    DEVICE_TOKEN_PEPPER     = data.aws_ssm_parameter.device_token_pepper.value
    ALLOWED_EXTENSION_IDS   = join(",", var.allowed_extension_ids)
    PUBLIC_BASE_URL         = var.public_base_url
    DEBOUNCE_BUCKET_SECONDS = tostring(var.debounce_bucket_seconds)
  })
}

data "archive_file" "api" {
  type        = "zip"
  source_file = "${var.backend_dir}/build/api/bootstrap"
  output_path = "${path.module}/.build/api.zip"
}

data "archive_file" "worker" {
  type        = "zip"
  source_file = "${var.backend_dir}/build/worker/bootstrap"
  output_path = "${path.module}/.build/worker.zip"
}

resource "aws_lambda_function" "api" {
  function_name    = local.api_name
  role             = aws_iam_role.api.arn
  runtime          = "provided.al2023"
  handler          = "bootstrap"
  architectures    = ["arm64"]
  memory_size      = 128
  timeout          = 10
  filename         = data.archive_file.api.output_path
  source_code_hash = data.archive_file.api.output_base64sha256

  environment {
    variables = local.api_env
  }

  depends_on = [aws_cloudwatch_log_group.api, aws_iam_role_policy.api]
}

resource "aws_lambda_function" "worker" {
  function_name    = local.worker_name
  role             = aws_iam_role.worker.arn
  runtime          = "provided.al2023"
  handler          = "bootstrap"
  architectures    = ["arm64"]
  memory_size      = 128
  timeout          = 60
  filename         = data.archive_file.worker.output_path
  source_code_hash = data.archive_file.worker.output_base64sha256

  environment {
    variables = local.shared_env
  }

  depends_on = [aws_cloudwatch_log_group.worker, aws_iam_role_policy.worker]
}

# Public Function URL; auth is the device token checked in Echo middleware (C26).
resource "aws_lambda_function_url" "api" {
  function_name      = aws_lambda_function.api.function_name
  authorization_type = "NONE"

  cors {
    allow_origins = [for id in var.allowed_extension_ids : "chrome-extension://${id}"]
    allow_methods = ["GET", "POST", "DELETE"]
    allow_headers = ["authorization", "content-type"]
    max_age       = 3600
  }
}

# A NONE-auth Function URL needs both statements for unauthenticated callers.
resource "aws_lambda_permission" "url_public" {
  statement_id           = "FunctionURLAllowPublicAccess"
  action                 = "lambda:InvokeFunctionUrl"
  function_name          = aws_lambda_function.api.function_name
  principal              = "*"
  function_url_auth_type = "NONE"
}

resource "aws_lambda_permission" "url_invoke" {
  statement_id             = "FunctionURLAllowInvokeAction"
  action                   = "lambda:InvokeFunction"
  function_name            = aws_lambda_function.api.function_name
  principal                = "*"
  invoked_via_function_url = true
}

resource "aws_lambda_event_source_mapping" "worker" {
  event_source_arn        = aws_sqs_queue.sync.arn
  function_name           = aws_lambda_function.worker.arn
  batch_size              = 10
  function_response_types = ["ReportBatchItemFailures"]

  scaling_config {
    maximum_concurrency = 2 # C27: instead of reserved concurrency
  }
}
```

`infra/terraform/Makefile`:

```make
# Reading Tracker infrastructure. Needs terraform >= 1.10 on PATH (override with TF=...),
# Go for the Lambda build, and AWS credentials for plan/apply. State is local
# (terraform.tfstate, gitignored): it contains secrets, never commit it.
TF          ?= terraform
BACKEND_DIR ?= ../../backend
ENV         ?= dev

.PHONY: build init check test plan apply

build:
	$(MAKE) -C $(BACKEND_DIR) build

init:
	$(TF) init

# check and test run offline: no AWS account needed.
check:
	$(TF) fmt -check -recursive
	$(TF) init -input=false > /dev/null
	$(TF) validate

test:
	$(TF) init -input=false > /dev/null
	$(TF) test

plan: build
	$(TF) plan -var-file=$(ENV).tfvars -out=$(ENV).tfplan

apply:
	$(TF) apply $(ENV).tfplan
```

`infra/terraform/example.tfvars`:

```hcl
# Copy to <env>.tfvars (e.g. dev.tfvars) and fill in. No secrets here: they live in SSM.
env                     = "dev"
region                  = "ap-southeast-1"
allowed_extension_ids   = ["abcdefghijklmnopabcdefghijklmnop"] # your pinned extension ID (extension/README.md)
budget_email            = "you@example.com"
public_base_url         = "" # set after the first apply if Notion needs a fixed redirect URI (C14)
debounce_bucket_seconds = 120
debounce_delay_seconds  = 120
```

- [ ] **Step 4: Run the checks, including a real backend build**

```bash
cd /Users/tuandoquoc/personal/reading-chrome-extension/infra/terraform
terraform fmt -check -recursive && terraform validate && terraform test
make build && file ../../backend/build/api/bootstrap ../../backend/build/worker/bootstrap
make -n plan ENV=dev | head -2
```

Expected:
- `Success! 15 passed, 0 failed.`
- Both bootstraps are `ELF 64-bit LSB executable, ARM aarch64 … statically linked`.
- The dry-run shows `make -C ../../backend build` before `terraform plan -var-file=dev.tfvars -out=dev.tfplan`.

- [ ] **Step 5: Commit**

```bash
cd /Users/tuandoquoc/personal/reading-chrome-extension
git add infra/terraform
git commit -m "feat(infra): api and worker Lambdas with Function URL, SQS trigger, least-privilege IAM and 7-day logs"
```

---

### Task 3: Budget, SAM metadata, outputs and the runbook

**Files:**
- Create: `infra/terraform/{budget,sam-metadata,outputs}.tf`, `infra/README.md`
- Modify: `infra/terraform/tests/stack.tftest.hcl` (append the final run)

**Interfaces:**
- **Consumes** from Task 2: `data.archive_file.api/worker`, `aws_lambda_function_url.api`, and the log groups and functions.
- **Produces** the outputs `function_url`, `table_name`, `queue_url` and `dlq_url`. `function_url` is the extension's `VITE_API_BASE`, without the trailing slash.

- [ ] **Step 1: Append the failing test**

Append to `infra/terraform/tests/stack.tftest.hcl`:

```hcl
run "logs_and_budget" {
  command = apply

  assert {
    condition     = aws_cloudwatch_log_group.api.retention_in_days == 7 && aws_cloudwatch_log_group.worker.retention_in_days == 7
    error_message = "T5: 7-day log retention"
  }
  assert {
    condition     = aws_cloudwatch_log_group.api.name == "/aws/lambda/${aws_lambda_function.api.function_name}"
    error_message = "log group matches the function name"
  }
  assert {
    condition     = aws_budgets_budget.monthly.limit_amount == "1" && aws_budgets_budget.monthly.limit_unit == "USD" && aws_budgets_budget.monthly.time_unit == "MONTHLY"
    error_message = "T6: $1 monthly budget"
  }
  assert {
    condition     = one(aws_budgets_budget.monthly.notification).threshold == 80 && one(aws_budgets_budget.monthly.notification).notification_type == "ACTUAL" && one(aws_budgets_budget.monthly.notification).subscriber_email_addresses == toset(["owner@example.com"])
    error_message = "T6: alert at 80% actual to the budget email"
  }
}
```

Run: `cd infra/terraform && terraform test`
Expected: FAIL, because `aws_budgets_budget.monthly` is undeclared.

- [ ] **Step 2: Write budget, SAM metadata and outputs**

`infra/terraform/budget.tf`:

```hcl
# $1/month cost budget with an 80% actual-spend alert (T6).
resource "aws_budgets_budget" "monthly" {
  name         = "${local.name}-monthly"
  budget_type  = "COST"
  limit_amount = "1"
  limit_unit   = "USD"
  time_unit    = "MONTHLY"

  notification {
    comparison_operator        = "GREATER_THAN"
    threshold                  = 80
    threshold_type             = "PERCENTAGE"
    notification_type          = "ACTUAL"
    subscriber_email_addresses = [var.budget_email]
  }
}
```

`infra/terraform/sam-metadata.tf`:

```hcl
# Metadata for `sam build/local invoke --hook-name terraform` (T7, D8). SAM CLI reads these
# null_resource triggers; Terraform itself does nothing with them.
resource "null_resource" "sam_metadata_aws_lambda_function_api" {
  triggers = {
    resource_name        = "aws_lambda_function.api"
    resource_type        = "ZIP_LAMBDA_FUNCTION"
    original_source_code = "${var.backend_dir}/cmd/api"
    built_output_path    = data.archive_file.api.output_path
  }
}

resource "null_resource" "sam_metadata_aws_lambda_function_worker" {
  triggers = {
    resource_name        = "aws_lambda_function.worker"
    resource_type        = "ZIP_LAMBDA_FUNCTION"
    original_source_code = "${var.backend_dir}/cmd/worker"
    built_output_path    = data.archive_file.worker.output_path
  }
}
```

`infra/terraform/outputs.tf`:

```hcl
output "function_url" {
  description = "API base URL: set the extension's VITE_API_BASE to this (without the trailing slash)."
  value       = aws_lambda_function_url.api.function_url
}

output "table_name" {
  value = aws_dynamodb_table.main.name
}

output "queue_url" {
  value = aws_sqs_queue.sync.url
}

output "dlq_url" {
  value = aws_sqs_queue.dlq.url
}
```

- [ ] **Step 3: Write the runbook**

`infra/README.md`:

````markdown
# Reading Tracker — infrastructure

Terraform for the AWS side of the system (spec T2–T7). State is local and gitignored: see section 2. `make check` and `make test` run offline. Everything else needs AWS credentials for the target account (`aws configure` or `AWS_PROFILE`).

## Prerequisites

- Terraform ≥ 1.10: `brew install hashicorp/tap/terraform`.
- Go, for the Lambda build. `make plan` runs the backend's `make build` first.
- AWS CLI v2, for the one-time secret setup below.
- Optional, for T7: AWS SAM CLI. `sam local invoke` needs a container runtime. With Podman, start the machine and export `DOCKER_HOST=unix://$(podman machine inspect --format '{{.ConnectionInfo.PodmanSocket.Path}}')`.

## 1. Create the secrets (once per environment)

Standard-tier SSM parameters (T2). Never put these values in `.tf` or `.tfvars`.

```bash
ENV=dev
aws ssm put-parameter --tier Standard --type String       --name /reading/$ENV/NOTION_CLIENT_ID     --value '<notion client id>'
aws ssm put-parameter --tier Standard --type SecureString --name /reading/$ENV/NOTION_CLIENT_SECRET --value '<notion client secret>'
aws ssm put-parameter --tier Standard --type SecureString --name /reading/$ENV/TOKEN_ENC_KEYS       --value "v1:$(openssl rand -base64 32)"
aws ssm put-parameter --tier Standard --type SecureString --name /reading/$ENV/DEVICE_TOKEN_PEPPER  --value "$(openssl rand -base64 32)"
```

**Key rotation** (scenario 19): prepend a new key, `v2:<b64>,v1:<b64>`, with `--overwrite`, then `make plan apply`.

**C17:** the values end up in Lambda environment variables and in the local Terraform state. Restrict `lambda:GetFunctionConfiguration` to administrators in your account's IAM.

## 2. Local state: keep it out of git, keep a backup

State lives in `infra/terraform/terraform.tfstate`, on the machine that runs `apply`.
- **Never commit it.** It holds the secret values in plain text. It is gitignored together with `terraform.tfstate.backup`, `*.tfplan` and `*.tfvars`. `git check-ignore infra/terraform/terraform.tfstate` must print the path.
- **Back it up privately** after each apply, for example in an encrypted password-manager attachment or an encrypted disk. Losing it means Terraform no longer knows about the resources, and you have to `terraform import` them or delete them by hand.
- **Run apply from one machine at a time.** Local state has no locking.

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

## 4. Point the extension at it

```bash
cd extension
echo "VITE_API_BASE=$(cd ../infra/terraform && terraform output -raw function_url | sed 's:/$::')" > .env.production.local
pnpm build
```

The extension ID in `allowed_extension_ids` must be the ID pinned with the manifest `key` (see `extension/README.md`).

## 5. SAM CLI (optional, T7)

```bash
cd infra/terraform
sam build --hook-name terraform
sam local invoke --hook-name terraform 'aws_lambda_function.worker' -e ../../backend/events/sqs-article.json
```

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
| `make check` | `fmt -check`, then `init -backend=false` and `validate` (offline) |
| `make test` | `terraform test` against mocked providers (offline): table shape, queue, Lambdas, CORS, IAM, logs, budget, input validation |
````

- [ ] **Step 4: Full verification**

```bash
cd /Users/tuandoquoc/personal/reading-chrome-extension/infra/terraform
make check && make test
git check-ignore terraform.tfstate terraform.tfstate.backup dev.tfplan dev.tfvars .build/api.zip
cd /Users/tuandoquoc/personal/reading-chrome-extension && git status --short --untracked-files=all infra | grep -E 'tfstate|tfplan|\.tfvars$|\.build/' || echo "no state, plans, tfvars or build output staged"
```

Expected:
- `make check` reports the configuration as valid.
- `make test` prints `Success! 16 passed, 0 failed.`
- `git check-ignore` prints all five paths.
- The last command prints `no state, plans, tfvars or build output staged`.
- `infra/terraform/.terraform.lock.hcl` is tracked (`git ls-files infra/terraform/.terraform.lock.hcl` prints it).

- [ ] **Step 5: Commit**

```bash
cd /Users/tuandoquoc/personal/reading-chrome-extension
git add infra
git commit -m "feat(infra): budget, SAM metadata, outputs and the deploy runbook"
```

---

## Spec coverage

| Spec item | Task |
|---|---|
| T1: replaced by local, gitignored state (user decision); backup and single-runner guidance | 1, 3 (README) |
| T2: SSM Standard parameters read by data sources, no secret literals | 1, 3 (README) |
| T3: DynamoDB PROVISIONED 25/25, TTL, no GSIs, PITR off | 1 |
| T4: FIFO queue and DLQ, redrive 10, visibility 360, D ≥ B and B ≤ 300 validation | 1 |
| T5: arm64 bootstrap Lambdas, Function URL with CORS, event source mapping, log retention 7, least-privilege IAM | 2 |
| T6: $1 budget with an 80% alert | 3 |
| T7: SAM metadata (`null_resource sam_metadata_*`) and runbook commands | 3 |
| T32: cloud smoke checklist | 3 (README) |
| C14: `PUBLIC_BASE_URL` as a variable, no self-reference | 1, 2 |
| C26: public Function URL resource policy | 2 |
| C27: `maximum_concurrency` instead of reserved concurrency | 2 |
