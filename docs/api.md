# API Reference

All endpoints are versioned under `/api/v1`.

## Authentication

Authenticated endpoints require an API key issued per project:

```
Authorization: Bearer ef_live_<hex>
```

Keys are stored only as SHA-256 hashes and are shown once at creation. All
event, subscription and delivery data is scoped to the key's project.

## Conventions

- **Request id** — every response carries `X-Request-ID`; pass your own to
  correlate logs.
- **Errors** — a single envelope for every failure:

  ```json
  { "error": { "code": "INVALID_REQUEST", "message": "event_type is required", "request_id": "req_123" } }
  ```

  Codes: `INVALID_REQUEST`, `UNAUTHORIZED`, `FORBIDDEN`, `NOT_FOUND`,
  `RATE_LIMITED`, `INTERNAL_ERROR`.
- **Pagination** — list endpoints accept `page` (default 1) and `limit`
  (default 20, max 100) and return:

  ```json
  { "data": [ ... ], "page": 1, "limit": 20, "total": 42 }
  ```

---

## Health

### `GET /health/live`

Liveness probe. Returns `{"status":"ok"}` while the process is up.

### `GET /health/ready`

Readiness probe. Pings PostgreSQL and NATS; returns `503` when any dependency
is unavailable:

```json
{ "status": "OK", "dependencies": { "postgres": "ok", "nats": "ok" } }
```

---

## Projects (admin bootstrap)

### `POST /api/v1/projects`

Creates a user (by email, upserted), a project and its first API key. Guarded
by `ADMIN_TOKEN` via the `X-Admin-Token` header.

Request:

```json
{ "email": "me@example.com", "name": "my-project", "description": "optional" }
```

Response `201`:

```json
{
  "project": { "id": "…", "user_id": "…", "name": "my-project", "…": "…" },
  "api_key": "ef_live_…"          // shown once; only the hash is stored
}
```

---

## Events

### `POST /api/v1/events`

Ingests an event. Requires the `Idempotency-Key` header. Body limits (spec §26):
`type`/`source` required and ≤128 chars, `subject` ≤256 chars, `data` valid JSON
≤1 MB.

Request:

```
POST /api/v1/events
Authorization: Bearer ef_live_…
Idempotency-Key: 4f9e…

{ "type": "order.created", "source": "shop", "subject": "order:9281",
  "data": { "order_id": 9281, "user_id": 42 } }
```

Response `202 Accepted`:

```json
{ "id": "…", "status": "accepted", "created": true }
```

Replaying the same `Idempotency-Key` returns the **same** event id with
`created: false` (spec §11).

### `GET /api/v1/events/{eventID}`

Returns a single event owned by the authenticated project.

### `GET /api/v1/events`

Lists events, newest first, with `page`/`limit` pagination.

---

## Subscriptions

A subscription binds an `event_pattern` to a `channel` + `destination`.

`event_pattern` supports `*` (match all), a single-segment wildcard (`order.*`)
and exact matches. `channel` is one of `webhook`, `email`, `telegram`; for
`webhook`, `destination` must be a valid `http(s)` URL. At most
`MAX_SUBSCRIPTIONS_PER_PROJECT` (default 100) subscriptions per project.

### `POST /api/v1/subscriptions`

```json
{ "name": "Order notifications", "event_pattern": "order.created",
  "channel": "webhook", "destination": "https://example.com/events",
  "enabled": true }
```

Response `201` with the created subscription (including `id`, timestamps).

### `GET /api/v1/subscriptions`

Lists subscriptions with `page`/`limit`.

### `GET /api/v1/subscriptions/{id}`

Returns one subscription. `404` if it belongs to another project.

### `PATCH /api/v1/subscriptions/{id}`

Partial update. Send only the fields to change (`name`, `event_pattern`,
`channel`, `destination`, `enabled`). Re-validates the merged result.

### `DELETE /api/v1/subscriptions/{id}`

Deletes the subscription. Returns `204 No Content`.

---

## Deliveries

### `GET /api/v1/deliveries`

Lists deliveries scoped to the authenticated project. Filters:

| Query | Meaning |
|-------|---------|
| `status` | `pending`, `retrying`, `delivered`, `dead_letter` |
| `event_id` | deliveries for a specific event |
| `subscription_id` | deliveries for a specific subscription |
| `page`, `limit` | pagination |

### `GET /api/v1/deliveries/{id}`

Returns a delivery with its full attempt history:

```json
{
  "id": "…", "event_id": "…", "subscription_id": "…",
  "status": "delivered", "attempts": 2,
  "created_at": "…", "completed_at": "…",
  "attempts": [
    { "attempt_number": 1, "status": "failed", "http_status": 500,
      "error_code": "HTTP_500", "started_at": "…", "finished_at": "…" },
    { "attempt_number": 2, "status": "success", "http_status": 200,
      "started_at": "…", "finished_at": "…" }
  ]
}
```

---

## Webhook delivery format

EventFlow POSTs to the subscription `destination`:

```
POST https://client.example.com/webhook
Content-Type: application/json
User-Agent: EventFlow/1.0
X-EventFlow-Event-ID: evt_…
X-EventFlow-Delivery-ID: del_…
X-EventFlow-Signature: sha256=<hex>

{
  "event": { "id": "evt_…", "type": "order.created", "source": "shop" },
  "data":  { "order_id": 9281 }
}
```

Verify the signature with `HMAC-SHA256(WEBHOOK_SECRET, request_body)` and
compare to the hex value after `sha256=` (constant-time comparison advised).