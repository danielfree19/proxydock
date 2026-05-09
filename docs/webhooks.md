# Webhooks

Phase 7 added outgoing HTTP webhooks for revision lifecycle events.
Use them to wire the manager into Slack / Discord / your own
deployment system, or to trigger downstream consumers when a fleet's
config changes.

## Events

| Event                     | When                                                   |
| ------------------------- | ------------------------------------------------------ |
| `revision_published`      | A new revision is published (manual or via the API).   |
| `revision_rolled_back`    | A rollback creates a new revision from an older one.   |
| `acme_certificate_issued` | An ACME job lands a new cert and republishes the fleet.|

A webhook subscribes to one or more events; the worker fires a
delivery for every webhook whose `events` list contains the
triggering event.

## Delivery shape

```http
POST /your/endpoint HTTP/1.1
Content-Type: application/json
User-Agent: proxydock-webhook/1.0
X-Webhook-Signature: sha256=<hex hmac>

{
  "event": "revision_published",
  "fleet_id": "homelab",
  "revision_number": 12,
  "etag": "\"sha256-3a8fccb0a2c99d3d\"",
  "generated_at": "2026-05-09T19:38:24.123Z"
}
```

The `X-Webhook-Signature` header is omitted when no HMAC secret is
configured.

## HMAC verification

Receivers verify the signature against the shared secret:

```python
import hmac, hashlib
expected = "sha256=" + hmac.new(SECRET.encode(), body, hashlib.sha256).hexdigest()
if not hmac.compare_digest(expected, request.headers["X-Webhook-Signature"]):
    abort(401)
```

```js
import { createHmac, timingSafeEqual } from "node:crypto";
const expected = "sha256=" + createHmac("sha256", SECRET).update(body).digest("hex");
const got = req.headers["x-webhook-signature"] ?? "";
if (!timingSafeEqual(Buffer.from(expected), Buffer.from(got))) reject();
```

The secret is stored encrypted at rest via `cryptokit` (AES-256-GCM)
when `MANAGER_API_ENCRYPTION_KEY` is set, and is never returned in
API responses — the webhook record exposes a `has_secret` boolean
instead.

## Retries

Failed deliveries (transport error or non-2xx response) retry with
exponential backoff: ~5 s, 25 s, 2 m 5 s, 10 m 25 s. After 5 attempts
the job is marked `failed` and stays in the table for inspection.

The queue is backed by `webhook_jobs` with `FOR UPDATE SKIP LOCKED`
claim semantics — multiple manager replicas can share the queue
without external coordination.

## API

```text
GET    /api/v1/fleets/{fleet_id}/webhooks
POST   /api/v1/fleets/{fleet_id}/webhooks
PUT    /api/v1/fleets/{fleet_id}/webhooks/{webhook_id}
DELETE /api/v1/fleets/{fleet_id}/webhooks/{webhook_id}
```

```json
{
  "name": "slack-ops",
  "url": "https://hooks.slack.com/services/T00000000/B00000000/XXXXXXXXXXXXXXXXXXXXXXXX",
  "secret": "shared-with-receiver",
  "events": ["revision_published", "revision_rolled_back"],
  "enabled": true
}
```

On update, an empty `secret` field means *leave the existing one*; a
non-empty value rotates it. Mirrors the `dns_providers` pattern.

## Web UI

The fleet detail page has a **Webhooks** tab listing existing
webhooks with their event subscriptions and a `signed`/`unsigned`
badge based on whether an HMAC secret is configured. New webhooks are
created inline from the same tab.

## Slack-style endpoints

Slack and Discord webhooks expect a particular JSON body shape (e.g.
`{"text": "..."}`). ProxyDock posts the raw event JSON, so for those
services you'll need an intermediate translator. Two common patterns:

1. **An nginx + Lua / Caddy snippet** that rewrites the body.
2. **A tiny serverless function** (Cloudflare Worker, Lambda) that
   formats the message — get the bonus of being able to dedupe or
   throttle alerts.

Direct integration with chat services in the manager itself is
deliberately out of scope; the chat-shape churn rate is much higher
than the manager's API surface, and "raw POST + transformer" is the
universal escape hatch.

## Local testing

The Compose demo includes an `httpbin` container; `http://httpbin/anything`
echoes whatever it receives, which makes it a great debug sink:

```sh
curl -X POST -H "Authorization: Bearer demo-admin" -H "Content-Type: application/json" \
  http://localhost:8090/api/v1/fleets/homelab/webhooks \
  -d '{"name":"debug","url":"http://httpbin:80/anything","secret":"demo","events":["revision_published"]}'

make publish

# Inspect the delivery payload:
docker exec tfm-postgres psql -U tfm -d tfm \
  -c "SELECT status, attempts, payload FROM webhook_jobs ORDER BY id DESC LIMIT 1;"
```
