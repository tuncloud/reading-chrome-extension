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
