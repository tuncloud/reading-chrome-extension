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
