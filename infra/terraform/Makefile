# Reading Tracker infrastructure. Needs terraform >= 1.10 on PATH (override with TF=...),
# Go for the Lambda build, and AWS credentials for plan/apply. State is local
# (terraform.tfstate, gitignored): it contains secrets, never commit it.
TF          ?= terraform
BACKEND_DIR ?= ../../backend
ENV         ?= dev

.PHONY: build init check test plan apply

build:
	$(MAKE) -C $(BACKEND_DIR) build

init:
	$(TF) init

# check and test run offline: no AWS account needed.
check:
	$(TF) fmt -check -recursive
	$(TF) init -input=false > /dev/null
	$(TF) validate

test:
	$(TF) init -input=false > /dev/null
	$(TF) test

plan: build
	$(TF) plan -var-file=$(ENV).tfvars -out=$(ENV).tfplan

apply:
	$(TF) apply $(ENV).tfplan
