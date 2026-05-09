# Upstreams: load balancing, sticky sessions, health checks

Phase 7 turned a proxy host's single upstream into a list, added
optional cookie-based stickiness, and a Traefik health-check block
per HTTP host.

## Multiple upstreams

`ProxyHost.upstream_urls` (`TEXT[]`) is the authoritative list. The
old `upstream_url` (`TEXT`) field stays as a denormalized copy of
`upstream_urls[0]` so older API clients don't break.

The compiler emits one `loadBalancer.servers` entry per URL:

```json
"services": {
  "api": {
    "loadBalancer": {
      "servers": [
        {"url": "http://api-a:8080"},
        {"url": "http://api-b:8080"}
      ]
    }
  }
}
```

Traefik distributes requests round-robin by default. If you want a
different policy, today's options are:

- **Sticky sessions** — see below.
- Run more than one entry on the same upstream (Traefik weights by
  duplication; emit the same URL N times to weight it N×).

### Schema validation

- HTTP hosts must have at least one upstream URL. Empty array → 400.
- All upstreams on an HTTP host must share the same scheme. Mixing
  `http://` and `https://` is rejected because Traefik's load
  balancer can't switch schemes per server — the resulting behaviour
  is non-deterministic for the user.
- TCP/UDP upstreams use bare `host:port` with optional `tcp://` /
  `udp://` prefix. Validation is the same.

## Sticky sessions

`proxy_hosts.sticky_session` (`BOOLEAN`) opts the host into Traefik's
cookie-based stickiness. The compiler emits an empty cookie object,
which means Traefik picks sensible defaults (cookie name, secure
flag, HTTP-only):

```json
"loadBalancer": {
  "servers": [...],
  "sticky": {"cookie": {}}
}
```

Operators who want a custom cookie name or domain should set it via
Traefik's static config — we don't expose it on the proxy host yet
because most users want defaults.

Stickiness is HTTP-only. The field is silently ignored for TCP/UDP.

## Health checks

`proxy_hosts.health_check` (`JSONB`) is an optional Traefik
`loadBalancer.healthCheck` block per HTTP host. The accepted keys
are:

| Key               | Type   | Notes                                                                         |
| ----------------- | ------ | ----------------------------------------------------------------------------- |
| `path`            | string | **Required to enable the health check.** Empty → no health check is emitted. |
| `interval`        | string | Traefik duration, e.g. `"10s"`.                                              |
| `timeout`         | string | Traefik duration.                                                             |
| `scheme`          | string | `"http"` / `"https"` — overrides the upstream's scheme for the probe.         |
| `hostname`        | string | `Host:` header sent to the upstream.                                          |
| `port`            | int    | Probe port (defaults to the upstream's port).                                 |
| `followRedirects` | bool   | Follow 3xx during the probe.                                                  |

Compiled output:

```json
"loadBalancer": {
  "servers": [...],
  "healthCheck": {
    "path": "/healthz",
    "interval": "10s",
    "timeout": "3s"
  }
}
```

When the probe fails repeatedly, Traefik removes the failing server
from the rotation until it recovers. Surfacing the up/down state in
the UI is deferred — the demo doesn't expose Traefik's API
(`tcpentry` already claims port 9000), and a manager-side prober
would significantly grow the worker surface for a feature most homelab
users don't need surfaced live.

## Web UI

- The proxy host form replaces the single Upstream URL input with a
  repeating list of rows; **+ Add upstream** appends a row, **Remove**
  drops one.
- A **Sticky sessions** checkbox appears below the upstream list when
  there's more than one populated row (single-upstream sticky is a
  no-op — useful only when you have ≥ 2 backends).
- A collapsible **Health check** section sits between the upstream
  list and the middlewares. Default-collapsed when the host has no
  health check; pre-expanded when editing a host that does.

## API examples

```sh
# create a host with two upstreams + sticky + health check
curl -X POST -H "Authorization: Bearer $ADMIN" -H "Content-Type: application/json" \
  http://localhost:8090/api/v1/fleets/homelab/proxy_hosts \
  -d '{
    "name": "api",
    "domain": "api.example.com",
    "protocol": "http",
    "entry_points": ["web"],
    "upstream_urls": ["http://api-a:8080", "http://api-b:8080"],
    "sticky_session": true,
    "health_check": {"path": "/healthz", "interval": "10s", "timeout": "3s"}
  }'

# publish so agents pick it up
curl -X POST -H "Authorization: Bearer $ADMIN" \
  http://localhost:8090/api/v1/fleets/homelab/revisions
```
