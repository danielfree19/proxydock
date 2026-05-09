# Manager API

Base URL in the Compose demo: `http://localhost:8090` from the host;
`http://manager-api:8080` from inside the demo network.

## Authentication

| Endpoint group         | Auth                                          |
| ---------------------- | --------------------------------------------- |
| Agent-facing           | Bearer token (`tfm_<prefix>_<secret>` format) |
| Admin / CRUD           | None *(Phase 5 will add admin auth)*          |

Agent tokens are bound to a single `agent_id`; using a token against a
different agent's URL returns `403`. Revoked tokens return `401`.

## Agent-facing endpoints

### `GET /healthz`

```json
{"status":"ok"}
```

### `GET /api/v1/agents/{agent_id}/config`

Headers:

| Header           | Required | Notes                                   |
| ---------------- | -------- | --------------------------------------- |
| `Authorization`  | yes      | `Bearer tfm_<prefix>_<secret>`          |
| `If-None-Match`  | no       | Last seen ETag, e.g. `"sha256-abc..."`  |

Successful response (`200`):

```json
{
  "fleet_id": "homelab",
  "agent_id": "traefik-1",
  "revision": 1,
  "etag": "\"sha256-06e7492d32a58403\"",
  "generated_at": "...",
  "config": { "http": { "routers": {...}, "services": {...} } }
}
```

Returns `304 Not Modified` if `If-None-Match` matches.

### `POST /api/v1/agents/{agent_id}/heartbeat`

```json
{
  "agent_id": "traefik-1",
  "current_revision": 1,
  "provider_version": "0.1.0",
  "traefik_version": "v3.1.0",
  "last_error": null
}
```

## Admin: fleets

| Method | Path                          | Description           |
| ------ | ----------------------------- | --------------------- |
| GET    | `/api/v1/fleets`              | list fleets           |
| POST   | `/api/v1/fleets`              | create `{id, name}`   |
| GET    | `/api/v1/fleets/{fleet_id}`   | get single fleet      |
| DELETE | `/api/v1/fleets/{fleet_id}`   | delete (cascades)     |

## Admin: agents

| Method | Path                                          | Description                  |
| ------ | --------------------------------------------- | ---------------------------- |
| GET    | `/api/v1/fleets/{fleet_id}/agents`            | list agents in a fleet       |
| POST   | `/api/v1/fleets/{fleet_id}/agents`            | create `{id, name}`          |
| GET    | `/api/v1/agents/{agent_id}`                   | get single agent             |
| DELETE | `/api/v1/agents/{agent_id}`                   | delete (cascades tokens)     |

The agent record carries the most recent heartbeat fields:
`last_heartbeat_at`, `last_revision_seen`, `last_provider_version`,
`last_traefik_version`, `last_error`.

## Admin: tokens

| Method | Path                                                       | Description                                              |
| ------ | ---------------------------------------------------------- | -------------------------------------------------------- |
| GET    | `/api/v1/agents/{agent_id}/tokens`                         | list tokens (metadata only — no plaintext)               |
| POST   | `/api/v1/agents/{agent_id}/tokens`                         | mint a new token (returns plaintext **once**)            |
| POST   | `/api/v1/agents/{agent_id}/tokens/{prefix}/revoke`         | revoke                                                   |

`POST tokens` body (optional):

```json
{ "name": "rotated 2026-05-06" }
```

Mint response:

```json
{
  "token": "tfm_89f43415_3cf532a97654f5f0c9d9019de703d72f",
  "metadata": {
    "prefix": "89f43415",
    "agent_id": "traefik-1",
    "name": "rotated 2026-05-06",
    "created_at": "..."
  }
}
```

The plaintext `token` field is the only place the secret ever leaves
the server — store it once and use it. The DB only retains
`SHA-256(secret)`.

## Admin: proxy hosts

| Method | Path                                                  | Description       |
| ------ | ----------------------------------------------------- | ----------------- |
| GET    | `/api/v1/fleets/{fleet_id}/proxy_hosts`               | list              |
| POST   | `/api/v1/fleets/{fleet_id}/proxy_hosts`               | create            |
| GET    | `/api/v1/fleets/{fleet_id}/proxy_hosts/{ph_id}`       | get single host   |
| PUT    | `/api/v1/fleets/{fleet_id}/proxy_hosts/{ph_id}`       | replace           |
| DELETE | `/api/v1/fleets/{fleet_id}/proxy_hosts/{ph_id}`       | delete            |

Body (create / update):

```json
{
  "name": "whoami",
  "domain": "whoami.localhost",
  "upstream_url": "http://whoami:80",
  "entry_points": ["web"],
  "middlewares": [
    {"name": "force-https", "type": "redirectScheme",
     "config": {"scheme": "https", "permanent": true}}
  ],
  "enabled": true
}
```

Supported middleware `type`s in Phase 2: `headers`, `redirectScheme`,
`stripPrefix`, `basicAuth`. Anything else is rejected at publish time.

## Admin: revisions

| Method | Path                                                            | Description                                          |
| ------ | --------------------------------------------------------------- | ---------------------------------------------------- |
| GET    | `/api/v1/fleets/{fleet_id}/revisions`                           | list (number desc; bodies omitted)                   |
| GET    | `/api/v1/fleets/{fleet_id}/revisions/{number}`                  | get with `compiled_config` and `source_proxy_hosts`  |
| POST   | `/api/v1/fleets/{fleet_id}/revisions`                           | publish: compile current proxy hosts → new revision  |
| POST   | `/api/v1/fleets/{fleet_id}/revisions/{number}/rollback`         | publish a copy of revision N as a new revision       |

Publish body (optional):

```json
{ "notes": "add api host" }
```

A publish returns `400` if any enabled proxy host fails validation
(invalid domain, bad upstream URL, unsupported middleware, duplicate
name or domain). The error body includes a per-host list of issues.

A rollback always creates a fresh revision number; it never reuses an
old number, so the audit log stays unambiguous.

## Admin: certificates

| Method | Path                                                    | Description                                  |
| ------ | ------------------------------------------------------- | -------------------------------------------- |
| GET    | `/api/v1/fleets/{fleet_id}/certificates`                | list (no key bytes)                          |
| POST   | `/api/v1/fleets/{fleet_id}/certificates`                | upload `{name, cert_pem, key_pem}`           |
| GET    | `/api/v1/fleets/{fleet_id}/certificates/{cert_id}`      | get single cert (no key bytes)               |
| DELETE | `/api/v1/fleets/{fleet_id}/certificates/{cert_id}`      | delete                                       |

Upload body:

```json
{
  "name": "example.com-2026",
  "cert_pem": "-----BEGIN CERTIFICATE-----\nMII...",
  "key_pem":  "-----BEGIN PRIVATE KEY-----\nMII..."
}
```

Returned metadata:

```json
{
  "id": 1,
  "fleet_id": "homelab",
  "name": "example.com-2026",
  "cert_pem": "-----BEGIN CERTIFICATE-----\n...",
  "fingerprint": "sha256:3ca6...",
  "subject": "CN=example.com",
  "issuer":  "CN=example.com",
  "dns_names": ["example.com", "*.example.com"],
  "not_before": "...",
  "not_after":  "...",
  "created_at": "..."
}
```

The private key is never returned in API responses; it is only embedded
in the compiled revision payload that authenticated agents fetch.

The cert pool plus a per-proxy-host `tls` boolean are how routers opt
into TLS — see `docs/certificates.md` for the wire shape.
