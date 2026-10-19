# Architecture

EventFlow is an event-driven notification platform. Its defining concern is
**reliable asynchronous delivery**: an accepted event must eventually produce a
delivery attempt for every matching subscription, surviving process crashes and
transient failures without duplicating side effects.

## Components

| Component | Responsibility |
|-----------|----------------|
| **API** (`cmd/api`) | HTTP surface: authentication, validation, event ingestion, subscription CRUD, delivery history, health. |
| **Router** (`internal/api/router.go`) | Matches events to subscriptions by `event_pattern`. |
| **Dispatcher** (`internal/event/service.go`) | Creates delivery rows and publishes delivery jobs to JetStream. |
| **Worker** (`cmd/worker`, `internal/worker`) | Consumes delivery jobs and performs the outbound webhook calls. |
| **Delivery** (`internal/delivery`) | Attempt bookkeeping, retry policy, HMAC signing, dead-letter transitions. |

## Data flow

```
POST /api/v1/events
        │
        ▼
  ┌─────────────┐   validate (spec §26)
  │  Ingest     │──► Idempotency-Key lookup (spec §11)
  └──────┬──────┘
         │ BEGIN TX
         ├─ INSERT event
         ├─ SELECT enabled subscriptions → match pattern
         ├─ INSERT delivery (pending) per match
         │ COMMIT
         ▼
  publish job ──► NATS JetStream (eventflow.deliveries.webhook)
                        │ durable consumer, explicit ack
                        ▼
                 Webhook Worker ──► POST destination (HMAC signed)
                        │
          ┌─────────────┼─────────────┐
          ▼             ▼             ▼
      2xx → delivered  retryable   non-retryable /
                     → retrying     attempts spent
                                    → dead_letter
```

## Storage

The event, its deliveries and the matching subscriptions are committed in a
**single transaction**, so a stored event always has a consistent set of
deliveries. Jobs are published *after* the commit; if publishing fails the
deliveries remain `pending` and are visible through the Delivery API — a
deliberate trade-off that keeps the database the source of truth.

Tables follow the specification: `users`, `projects`, `api_keys`, `events`,
`subscriptions`, `deliveries`, `delivery_attempts`, `dead_letters`. The pair
`(project_id, idempotency_key)` is unique, which is what makes ingestion
idempotent.

## Messaging

- **Stream** `EVENTFLOW` covers `eventflow.>`, file storage, 24 h retention.
- **Consumer** `webhook-worker` is a durable consumer on
  `eventflow.deliveries.webhook` with **explicit acknowledgement**.
- Retries use `NakWithDelay`, so the backoff schedule lives in JetStream rather
  than a separate scheduler service.

Because delivery is at-least-once, the worker checks the delivery status before
sending; a message redelivered after success is ACKed without re-sending.

## Multi-tenancy

Every request is authenticated with `Authorization: Bearer <api-key>`. The key
is looked up by SHA-256 hash, and the resolved `project_id` scopes **every**
query. One project can never read another project's events, subscriptions or
deliveries.

## Concurrency

The worker runs `WORKER_CONCURRENCY` goroutines pulling from one durable
consumer. Each handles one job: load relations → send → persist attempt →
update delivery → ACK/NACK. All outbound calls honour `context` cancellation and
`REQUEST_TIMEOUT`.

## Failure handling

| Failure | Handling |
|---------|----------|
| Transient webhook error (408/429/5xx, timeout) | `retrying` + `NakWithDelay(backoff)` |
| Permanent webhook error (400/401/403/404) | immediate `dead_letter` |
| Attempts exhausted | `dead_letter` with reason |
| Crash before ACK | JetStream redelivers; processing is idempotent |
| NATS publish failure at ingest | delivery stays `pending`, logged |
| Process shutdown (SIGTERM) | graceful: finish in-flight jobs, close connections |

## Deliberate simplifications (MVP)

- **In-memory rate limiting** — a single API instance; Redis arrives in v2.
- **Retry via `NakWithDelay`** — no separate retry worker or scheduler service.
- **Admin bootstrap endpoint** — project/key creation is guarded by a static
  `ADMIN_TOKEN`; a full user-management API is out of MVP scope.

Each is an intentional MVP boundary with a clear v2/v3 path, not an oversight.