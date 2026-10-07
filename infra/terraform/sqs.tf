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
