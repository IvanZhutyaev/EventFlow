# EventFlow — developer tasks.
.PHONY: build test test-integration test-e2e lint fmt vet run-api run-worker migrate up down clean

build:
	go build ./...

fmt:
	gofmt -w .

vet:
	go vet ./...

lint: fmt vet
	command -v golangci-lint >/dev/null 2>&1 && golangci-lint run ./... || echo "golangci-lint not installed; skipped"

test:
	go test -race -count=1 ./internal/...

# Integration tests run inside the compose network via the test-runner service,
# reaching postgres/nats by service name.
test-integration:
	docker compose run --rm test-runner

# E2E requires the full stack: docker compose up -d
test-e2e:
	E2E=1 go test -race -count=1 -tags=e2e ./tests/e2e/...

run-api:
	go run ./cmd/api

run-worker:
	go run ./cmd/worker

migrate:
	go run ./cmd/migrate

up:
	docker compose up -d --build

down:
	docker compose down

clean:
	docker compose down -v