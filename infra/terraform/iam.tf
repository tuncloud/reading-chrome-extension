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
