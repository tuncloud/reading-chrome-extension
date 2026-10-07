locals {
  name           = "reading-${var.env}"
  notion_version = "2025-09-03" # C10: pinned; the backend refuses any other value
  ssm_prefix     = "/reading/${var.env}"
  api_name       = "reading-api-${var.env}"
  worker_name    = "reading-worker-${var.env}"
}
