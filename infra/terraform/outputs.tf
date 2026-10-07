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
