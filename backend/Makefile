SHELL := /bin/bash
DDB_CONTAINER := reading-ddb
PG_CONTAINER := reading-pg

.PHONY: test ddb-up ddb-down pg-up pg-down run-local build build-api build-worker run-pg pg-migrate image

test:
	go test ./...

ddb-up:
	@podman container exists $(DDB_CONTAINER) || podman run -d --rm --name $(DDB_CONTAINER) -p 8000:8000 \
		docker.io/amazon/dynamodb-local:latest -jar DynamoDBLocal.jar -inMemory -sharedDb
	@for i in $$(seq 1 30); do curl -s -o /dev/null http://localhost:8000 && exit 0; sleep 1; done; \
		echo "DynamoDB Local did not start" >&2; exit 1

ddb-down:
	-podman stop $(DDB_CONTAINER)

pg-up:
	@podman container exists $(PG_CONTAINER) || podman run -d --rm --name $(PG_CONTAINER) -p 5432:5432 \
		-e POSTGRES_USER=reading -e POSTGRES_PASSWORD=reading -e POSTGRES_DB=reading docker.io/library/postgres:17
	@for i in $$(seq 1 30); do podman exec $(PG_CONTAINER) pg_isready -U reading -q && exit 0; sleep 1; done; \
		echo "Postgres did not start" >&2; exit 1

pg-down:
	-podman stop $(PG_CONTAINER)

.env.local:
	sed -e "s|^TOKEN_ENC_KEYS=.*|TOKEN_ENC_KEYS=v1:$$(openssl rand -base64 32)|" \
	    -e "s|^DEVICE_TOKEN_PEPPER=.*|DEVICE_TOKEN_PEPPER=$$(openssl rand -base64 32)|" .env.local.example > .env.local

run-local: .env.local
	set -a && source .env.local && set +a && go run ./cmd/local

LAMBDA_ENV   := GOOS=linux GOARCH=arm64 CGO_ENABLED=0
LAMBDA_FLAGS := -tags lambda.norpc -trimpath -ldflags="-s -w"

build: build-api build-worker

build-api:
	$(LAMBDA_ENV) go build $(LAMBDA_FLAGS) -o build/api/bootstrap ./cmd/api

build-worker:
	$(LAMBDA_ENV) go build $(LAMBDA_FLAGS) -o build/worker/bootstrap ./cmd/worker

PG_URL := postgres://reading:reading@localhost:5432/reading?sslmode=disable

run-pg: .env.local
	set -a && source .env.local && set +a && export STORE=postgres DATABASE_URL=$(PG_URL) && \
		(go run ./cmd/poller & trap 'kill $$!' EXIT; go run ./cmd/server)

pg-migrate:
	DATABASE_URL=$(PG_URL) go run ./cmd/migrate

image:
	podman build --platform linux/arm64 -t ghcr.io/tuncloud/reading-tracker:dev -f Containerfile .
