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
