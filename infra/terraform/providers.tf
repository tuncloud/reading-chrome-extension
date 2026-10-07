provider "aws" {
  region = var.region
  default_tags {
    tags = { app = "reading-tracker", env = var.env }
  }
}
