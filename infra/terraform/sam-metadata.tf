# Metadata for `sam build/local invoke --hook-name terraform` (T7, D8). SAM CLI reads these
# null_resource triggers; Terraform itself does nothing with them.
resource "null_resource" "sam_metadata_aws_lambda_function_api" {
  triggers = {
    resource_name        = "aws_lambda_function.api"
    resource_type        = "ZIP_LAMBDA_FUNCTION"
    original_source_code = "${var.backend_dir}/cmd/api"
    built_output_path    = data.archive_file.api.output_path
  }
}

resource "null_resource" "sam_metadata_aws_lambda_function_worker" {
  triggers = {
    resource_name        = "aws_lambda_function.worker"
    resource_type        = "ZIP_LAMBDA_FUNCTION"
    original_source_code = "${var.backend_dir}/cmd/worker"
    built_output_path    = data.archive_file.worker.output_path
  }
}
