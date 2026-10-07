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

run "tracking_config_json_size_is_capped" {
  command = plan
  variables {
    tracking_config_json = join("", [for i in range(700) : "xxx"])
  }
  expect_failures = [var.tracking_config_json]
}
