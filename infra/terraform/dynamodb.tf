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
