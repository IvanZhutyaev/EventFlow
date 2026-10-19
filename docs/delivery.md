# Delivery

A **delivery** is the unit of work: deliver one event to one subscription. This
document describes its lifecycle, retry semantics, idempotency and the records
EventFlow keeps.

## Lifecycle

```
                ┌──────────────────────────────┐
   (created)    │                              │
        ┌───────▼───────┐                      │
        │   pending     │  worker picks up job │
        └───────┬───────┘                      │
                │                              │
        ┌───────▼───────┐   2xx                │
        │   (sending)   ├──────────► delivered │  (terminal)
        └───────┬───────┘                      │
                │ transient error, attempts left
        ┌───────▼───────┐   next attempt due   │
        │   retrying    │── NakWithDelay ──────┘
        └───────┬───────┘
                │ permanent error OR attempts exhausted
        ┌───────▼──────┐
        │ dead_letter  │  (terminal)
        └──────────────┘
```

| Status | Meaning |
|--------|---------|
| `pending` | Created; a job is in JetStream awaiting the first attempt. |
| `retrying` | An attempt failed; `next_attempt_at` holds the scheduled retry time. |
| `delivered` | A webhook call returned 2xx. Terminal. |
| `dead_letter` | Permanent failure or attempts exhausted. Terminal. |

Terminal states are sticky: once `delivered` or `dead_letter`, a redelivered
message is ACKed without changing state, preserving idempotency.

## Retry policy

Backoff is exponential with a cap (spec §9):

| Attempt | Delay before next |
|---------|-------------------|
| 1 | `RETRY_INITIAL_DELAY` (5s) |
| 2 | 10s |
| 3 | 20s |
| 4 | 40s |
| … | ×`RETRY_MULTIPLIER`, capped at `RETRY_MAX_DELAY` (30m) |

`MAX_DELIVERY_ATTEMPTS` (default 5) bounds the total tries. The delay is handed
to JetStream via `NakWithDelay`, so the schedule survives worker restarts.

## Retryable vs permanent errors

| Outcome | Classification |
|---------|----------------|
| HTTP `408`, `429`, `500`, `502`, `503`, `504` | retryable |
| network timeout, connection refused, temporary DNS failure | retryable |
| HTTP `400`, `401`, `403`, `404` | permanent → immediate dead letter |

A retryable error with no attempts left is also moved to dead letter with reason
`max_attempts_exhausted`; a permanent one uses `non_retryable_error`.

## Records

- **`deliveries`** — current status, `attempts` count, `next_attempt_at`,
  `completed_at`.
- **`delivery_attempts`** — one row per try: `attempt_number`, `status`
  (`success`/`failed`), `http_status`, `error_code`, `error_message`,
  `started_at`, `finished_at`. Exposed via `GET /api/v1/deliveries/{id}`.
- **`dead_letters`** — one row per dead-lettered delivery: `reason`,
  `last_error`, `created_at`, `resolved_at` (nullable, for future replay UI).

## Idempotency

Two layers make at-least-once safe:

1. **Ingestion** — the unique `(project_id, idempotency_key)` pair means a
   retried `POST /events` never creates a second event.
2. **Delivery** — before sending, the worker loads the delivery row; if it is
   already `delivered`/`dead_letter` the message is ACKed and discarded. A
   worker crash before ACK causes redelivery, but the state check prevents a
   duplicate side effect in the common case (the success was never persisted).

## Webhook signing

Every webhook body is signed with HMAC-SHA256 using `WEBHOOK_SECRET`:

```
X-EventFlow-Signature: sha256=<hex(HMAC_SHA256(secret, body))>
```

Clients recompute the MAC over the raw request body and compare in constant
time. The secret is never logged and never stored in Git.

## Graceful shutdown

On `SIGTERM` the worker stops fetching new jobs, lets in-flight jobs finish,
persists their outcomes and closes connections (bounded by
`SHUTDOWN_TIMEOUT`). The API stops accepting connections and drains active
requests before exiting.

## Load test results

Run the k6 scenario (`loadtest/ingest.js`) against a warmed stack and record the
summary here:

```
k6 run --vus=50 --duration=30s loadtest/ingest.js
```

Capture requests/sec, p50/p95/p99, error rate, and container CPU/memory.
Fill in the measured numbers from your environment — no figures are hardcoded
here to avoid presenting unverified results.