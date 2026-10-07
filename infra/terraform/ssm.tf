# Secrets are created by hand (README) as Standard-tier SSM parameters and only read here (T2, C17).
data "aws_ssm_parameter" "notion_client_id" {
  name = "${local.ssm_prefix}/NOTION_CLIENT_ID"
}

data "aws_ssm_parameter" "notion_client_secret" {
  name            = "${local.ssm_prefix}/NOTION_CLIENT_SECRET"
  with_decryption = true
}

data "aws_ssm_parameter" "token_enc_keys" {
  name            = "${local.ssm_prefix}/TOKEN_ENC_KEYS"
  with_decryption = true
}

data "aws_ssm_parameter" "device_token_pepper" {
  name            = "${local.ssm_prefix}/DEVICE_TOKEN_PEPPER"
  with_decryption = true
}
