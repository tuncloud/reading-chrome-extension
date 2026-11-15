SHELL := /bin/bash
PG_CONTAINER := reading-pg

.PHONY: test pg-up pg-down run run-pg pg-migrate image

test:
	go test ./...

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

PG_URL := postgres://reading:reading@localhost:5432/reading?sslmode=disable

run: .env.local
	set -a && source .env.local && set +a && export DATABASE_URL=$(PG_URL) && \
		(go run ./cmd/poller & trap 'kill $$!' EXIT; go run ./cmd/server)

# Alias kept for habits and docs that predate the rename.
run-pg: run

pg-migrate:
	DATABASE_URL=$(PG_URL) go run ./cmd/migrate

image:
	podman build --platform linux/arm64 -t ghcr.io/tuncloud/reading-tracker:dev -f Containerfile .
