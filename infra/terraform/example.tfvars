# Copy to <env>.tfvars (e.g. dev.tfvars) and fill in. No secrets here: they live in SSM.
env                     = "dev"
region                  = "ap-southeast-1"
allowed_extension_ids   = ["abcdefghijklmnopabcdefghijklmnop"] # your pinned extension ID (extension/README.md)
budget_email            = "you@example.com"
public_base_url         = "" # set after the first apply if Notion needs a fixed redirect URI (C14)
debounce_bucket_seconds = 120
debounce_delay_seconds  = 120
