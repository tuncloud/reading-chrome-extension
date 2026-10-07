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
