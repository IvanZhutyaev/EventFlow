# EventFlow

Event-driven notification platform for reliable asynchronous message delivery.

EventFlow is a backend platform built in Go that accepts events from external
applications, matches them against subscriptions, creates delivery jobs and
reliably delivers notifications over webhooks (email and Telegram are planned
for v2). It demonstrates production-grade backend engineering: durable
messaging, at-least-once delivery, idempotent processing, exponential retry,
HMAC webhook signing, rate limiting and multi-tenant isolation.

> Built to a written technical specification; see [`docs/`](docs/) for the
> architecture, API and delivery semantics.

---

## How it works

```
External Client
      │ HTTP  (Authorization: Bearer ef_live_..., Idempotency-Key)
      ▼
 ┌──────────┐  validate · idempotent store · match subscriptions
 │ Event API│──────────────┐
 └────┬─────┘               │ PostgreSQL (metadata)
      │ publish job        │
      ▼                    │
  NATS JetStream ──────────┤
      │ durable consumer   │
      ▼                    ▼
 Webhook Worker ──► client webhook   Delivery API (history)
 (retry · HMAC · attempts)
```

1. A client `POST`s an event with an `Idempotency-Key`.
2. The API validates it, stores it and matches it against the project's
   subscriptions (`order.*`, `*`, …).
3. One **delivery** row is created per matching subscription and a delivery job
   is published to **NATS JetStream**.
4. A **webhook worker** consumes the job, signs the payload with HMAC-SHA256 and
   POSTs it to the subscription's destination.
5. Failures are retried with exponential backoff; exhausted or non-retryable
   failures move to **dead letter**. Every attempt is recorded.

## Tech stack

| Area | Choice |
|------|--------|
| Language | Go 1.27 (`net/http`, `database/sql`, `context`) |
| Database | PostgreSQL |
| Broker | NATS JetStream (durable consumers, explicit ack) |
| Rate limiting | in-memory token bucket (Redis planned for v2) |
| Infra | Docker, Docker Compose |
| Observability | structured JSON logs (Prometheus/Grafana/OTel planned) |

## Quickstart

Requirements: Docker with Compose.

```bash
docker compose up -d --build
curl http://localhost:8080/health/ready
```

This starts PostgreSQL, NATS JetStream, runs migrations, then the API (`:8080`),
the worker and a webhook `testsink` (`:9091`) used by the E2E tests.

Create a project and its first API key (bootstrap endpoint, guarded by
`ADMIN_TOKEN`):

```bash
curl -s -X POST http://localhost:8080/api/v1/projects \
  -H 'Content-Type: application/json' \
  -H 'X-Admin-Token: dev-admin-token' \
  -d '{"email":"me@example.com","name":"my-project"}'
# → {"project":{...},"api_key":"ef_live_..."}   ← api_key shown once
```

Create a subscription:

```bash
curl -s -X POST http://localhost:8080/api/v1/subscriptions \
  -H "Authorization: Bearer $API_KEY" \
  -H 'Content-Type: application/json' \
  -d '{"name":"orders","event_pattern":"order.*","channel":"webhook",
       "destination":"http://testsink:8081/hook","enabled":true}'
```

> The destination is resolved **from inside the worker container**, so when the
> stack runs in Compose use the service name (`http://testsink:8081/hook`).

Send an event:

```bash
curl -s -X POST http://localhost:8080/api/v1/events \
  -H "Authorization: Bearer $API_KEY" \
  -H 'Content-Type: application/json' \
  -H "Idempotency-Key: $(uuidgen)" \
  -d '{"type":"order.created","source":"shop","subject":"order:9281",
       "data":{"order_id":9281,"user_id":42}}'
# → 202 {"id":"...","status":"accepted"}
```

Watch it being delivered:

```bash
curl -s "http://localhost:8080/api/v1/deliveries?status=delivered" \
  -H "Authorization: Bearer $API_KEY"

## Configuration

All configuration is provided via environment variables (`.env.example` lists
every option). Only `.env.example` is committed — real secrets never are.

| Variable | Default | Purpose |
|----------|---------|---------|
| `APP_ENV` | `development` | `development` logs text, otherwise JSON |
| `HTTP_PORT` | `8080` | API listen port |
| `DATABASE_URL` | — (required) | PostgreSQL DSN |
| `NATS_URL` | `nats://localhost:4222` | NATS server URL |
| `WORKER_CONCURRENCY` | `20` | parallel delivery jobs |
| `MAX_DELIVERY_ATTEMPTS` | `5` | attempts before dead letter |
| `REQUEST_TIMEOUT` | `10s` | outgoing webhook timeout |
| `SHUTDOWN_TIMEOUT` | `15s` | graceful shutdown grace period |
| `RETRY_INITIAL_DELAY` | `5s` | backoff base |
| `RETRY_MAX_DELAY` | `30m` | backoff cap |
| `RETRY_MULTIPLIER` | `2` | backoff growth factor |
| `RATE_LIMIT_RPS` | `100` | requests/second per project |
| `WEBHOOK_SECRET` | `change-me` | HMAC-SHA256 signing secret |
| `ADMIN_TOKEN` | `change-me` | token for the bootstrap admin API |
| `MAX_SUBSCRIPTIONS_PER_PROJECT` | `100` | subscription cap |
| `MAX_EVENT_BODY_BYTES` | `1048576` | max event body (1 MB) |

## API overview

All endpoints are versioned under `/api/v1` and use a single error envelope
`{"error":{"code","message","request_id"}}`.

| Method | Path | Auth | Description |
|--------|------|------|-------------|
| `GET` | `/health/live` | — | liveness |
| `GET` | `/health/ready` | — | readiness (PostgreSQL + NATS) |
| `POST` | `/api/v1/projects` | admin token | create project + API key |
| `POST` | `/api/v1/events` | API key | ingest event (needs `Idempotency-Key`) |
| `GET` | `/api/v1/events` | API key | list events (page/limit) |
| `GET` | `/api/v1/events/{id}` | API key | get event |
| `POST` | `/api/v1/subscriptions` | API key | create subscription |
| `GET` | `/api/v1/subscriptions` | API key | list subscriptions |
| `GET` | `/api/v1/subscriptions/{id}` | API key | get subscription |
| `PATCH` | `/api/v1/subscriptions/{id}` | API key | update subscription |
| `DELETE` | `/api/v1/subscriptions/{id}` | API key | delete subscription |
| `GET` | `/api/v1/deliveries` | API key | list deliveries (filters + pagination) |
| `GET` | `/api/v1/deliveries/{id}` | API key | get delivery with attempts |

Full details in [`docs/api.md`](docs/api.md).

## Delivery semantics

- **At-least-once.** Jobs are published to JetStream and consumed with explicit
  acknowledgement; a worker only ACKs after persisting the outcome. A crash
  before ACK causes redelivery, which is safe because processing is idempotent.
- **Idempotent ingestion.** One `Idempotency-Key` yields exactly one event per
  project; replays return the original event id.
- **Idempotent delivery.** A delivery already marked `delivered`/`dead_letter` is
  never re-sent, so redelivered messages are harmless.
- **Retry.** Transient failures (HTTP 408/429/5xx, timeouts, refused
  connections) retry with exponential backoff up to `MAX_DELIVERY_ATTEMPTS`.
  Permanent failures (HTTP 400/401/403/404) skip retries and go straight to
  dead letter.
- **Signing.** Webhooks carry `X-EventFlow-Signature: sha256=<hex>` — an
  HMAC-SHA256 of the request body — plus `X-EventFlow-Event-ID` and
  `X-EventFlow-Delivery-ID`.

See [`docs/delivery.md`](docs/delivery.md) for the full state machine.

## Testing

```bash
make test              # unit tests with the race detector
docker compose run --rm test-runner  # integration tests (Go inside the compose network, see below)
make test-e2e          # needs the full stack: `docker compose up -d`
```

- **Unit** — matcher, retry calculator, HMAC signature, rate limiter, API-key
  hashing, event validation.
- **Integration** — event idempotency, subscription matching, tenancy isolation,
  API-key lookup and the delivery lifecycle against a real PostgreSQL.
  The `test-runner` service runs Go **inside** the compose network, so tests
  reach dependencies via service names (`postgres:5432`, `nats:4222`).
- **E2E** — the complete pipeline over HTTP: project → key → subscription →
  event → delivery → webhook → delivered, plus the retry and dead-letter flows.

## Load testing

A k6 scenario lives in [`loadtest/ingest.js`](loadtest/ingest.js) and drives the
ingestion endpoint (~1000 events/sec). Run it against a running stack:

```bash
k6 run --vus=50 --duration=30s loadtest/ingest.js
```

Record requests/sec, p50/p95/p99 latency and error rate. Results from the
reference environment are summarized in [`docs/delivery.md`](docs/delivery.md).

## Project structure

```
cmd/
  api/        HTTP API server
  worker/     delivery worker (NATS JetStream consumer)
  migrate/    SQL migration runner
  testsink/   deterministic webhook sink used by e2e tests
internal/
  api/          handlers, middleware, router
  auth/         API key generation + hashing
  broker/       NATS JetStream wrapper
  config/       env-based configuration
  dbutil/       shared PostgreSQL helpers
  delivery/     delivery model, store, retry policy, HMAC signing
  event/        event model, validation, ingestion service
  observability/ structured logging
  project/      users, projects, API keys
  ratelimit/    per-project token bucket
  subscription/ subscriptions + pattern matching
  worker/       webhook delivery worker
migrations/     embedded SQL migrations
tests/          integration/ and e2e/
docs/           architecture, API and delivery docs
examples/       runnable example client
loadtest/       k6 load scenario
deploy/docker   Dockerfile
```

## Roadmap

Built iteratively; the current MVP proves the core pipeline
(API → PostgreSQL → JetStream → webhook worker → client).

- **v2** — Redis + shared rate limiting, email & Telegram senders,
  Prometheus/Grafana metrics.
- **v3** — OpenTelemetry distributed tracing, advanced retry policies,
  dead-letter handling, scheduled/delayed delivery.
- **v4** — horizontal scaling, NATS cluster, PostgreSQL replicas, partitioning,
  circuit breaking.

Extensions are added only when there is a real engineering reason for them.

## License

MIT — see [LICENSE](LICENSE).
```

See [`docs/api.md`](docs/api.md) for the full reference and
[`examples/client`](examples/client) for a runnable Go client.