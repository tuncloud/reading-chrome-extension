SHELL := /bin/bash
DDB_CONTAINER := reading-ddb

.PHONY: test ddb-up ddb-down run-local

test:
	go test ./...

ddb-up:
	@podman container exists $(DDB_CONTAINER) || podman run -d --rm --name $(DDB_CONTAINER) -p 8000:8000 \
		docker.io/amazon/dynamodb-local:latest -jar DynamoDBLocal.jar -inMemory -sharedDb
	@for i in $$(seq 1 30); do curl -s -o /dev/null http://localhost:8000 && exit 0; sleep 1; done; \
		echo "DynamoDB Local did not start" >&2; exit 1

ddb-down:
	-podman stop $(DDB_CONTAINER)

.env.local:
	sed -e "s|^TOKEN_ENC_KEYS=.*|TOKEN_ENC_KEYS=v1:$$(openssl rand -base64 32)|" \
	    -e "s|^DEVICE_TOKEN_PEPPER=.*|DEVICE_TOKEN_PEPPER=$$(openssl rand -base64 32)|" .env.local.example > .env.local

run-local: .env.local
	set -a && source .env.local && set +a && go run ./cmd/local
