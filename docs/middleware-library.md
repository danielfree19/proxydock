# Middleware library

Phase 7 added a per-fleet **middleware template** store so operators
can capture a reusable chain (e.g. an OIDC + security-headers combo)
once and apply it to many proxy hosts.

## Apply-by-copy semantics

When a proxy host applies a template, the template's `middlewares`
array is **deep-copied** into the host. After that, the host owns
those middleware rows. Editing the template later does **not** mutate
hosts that already applied it.

This keeps the mental model simple — templates are *starting points*,
not live links — and avoids the "edit one row, surprise change to N
hosts" failure mode.

If you want a different policy (live references), the right fix is
to keep using copies and write a small script that re-applies the
template across hosts. Templates are versionless on purpose.

## API

```text
GET    /api/v1/fleets/{fleet_id}/middleware_templates
POST   /api/v1/fleets/{fleet_id}/middleware_templates
GET    /api/v1/fleets/{fleet_id}/middleware_templates/{tpl_id}
PUT    /api/v1/fleets/{fleet_id}/middleware_templates/{tpl_id}
DELETE /api/v1/fleets/{fleet_id}/middleware_templates/{tpl_id}
```

Body shape mirrors the proxy host's `middlewares` array exactly:

```json
{
  "name": "standard-oidc",
  "description": "oauth2-proxy + standard X-Frame headers",
  "middlewares": [
    {
      "name": "oidc",
      "type": "forwardAuth",
      "config": {
        "address": "http://oauth2-proxy:4180/oauth2/auth",
        "trustForwardHeader": true,
        "authResponseHeaders": [
          "X-Auth-Request-User",
          "X-Auth-Request-Email"
        ]
      }
    },
    {
      "name": "security-headers",
      "type": "headers",
      "config": {
        "customResponseHeaders": {
          "X-Frame-Options": "DENY",
          "X-Content-Type-Options": "nosniff"
        }
      }
    }
  ]
}
```

The same `compiler.ValidateMiddlewares` check the proxy host create
runs is applied here, so a template never lands in a state that would
fail compilation later.

## Web UI

- **Library tab** on the fleet detail page lists templates and links
  to a create / edit form that reuses the same `MiddlewareEditor`
  component the proxy host form uses.
- On the proxy host form, an **Apply template…** button above the
  middleware list opens a picker; clicking **Apply** appends the
  template's middlewares (deep-copied) to the host's chain. Operators
  can then tweak the result per host.

## Expanded middleware catalogue

Phase 7 also grew the supported middleware types from 5 to 11. See
[compiler.md](compiler.md) for the full table; the new ones are:

- `rateLimit` — token-bucket; `average` and/or `burst` required.
- `ipAllowList` — `sourceRange` array of CIDRs.
- `retry` — `attempts` (positive int), optional `initialInterval`.
- `compress` — gzip / brotli; default config (`{}`) works.
- `circuitBreaker` — `expression` (Traefik's circuit-breaker DSL).
- `chain` — `middlewares` array of *raw* names of other middlewares
  on the same host. The compiler resolves them to the per-host
  mangled names (`<host>-<index>-<raw-name>`) at compile time, so
  operators reference what they typed.

## Examples

### A "lan-only" template

```json
{
  "name": "lan-only",
  "description": "Block non-LAN clients before anything else runs.",
  "middlewares": [
    {
      "name": "lan-allow",
      "type": "ipAllowList",
      "config": {
        "sourceRange": ["10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16"]
      }
    }
  ]
}
```

### A "rate-limited public API" template

```json
{
  "name": "public-api-rate-limit",
  "description": "100 req/s sustained, 200 burst, retry on transient",
  "middlewares": [
    {
      "name": "rl",
      "type": "rateLimit",
      "config": {"average": 100, "burst": 200}
    },
    {
      "name": "retry",
      "type": "retry",
      "config": {"attempts": 3, "initialInterval": "100ms"}
    }
  ]
}
```

### Chaining inside a single host

A `chain` middleware references other middlewares on the same host
by their raw names:

```json
"middlewares": [
  {"name": "rl",  "type": "rateLimit",   "config": {"average": 100}},
  {"name": "lan", "type": "ipAllowList", "config": {"sourceRange": ["10.0.0.0/8"]}},
  {"name": "all", "type": "chain",       "config": {"middlewares": ["rl", "lan"]}}
]
```

The compiler emits the chain's `middlewares` field as the **mangled**
names so Traefik can resolve them:

```json
"chain": {"middlewares": ["host-0-rl", "host-1-lan"]}
```
