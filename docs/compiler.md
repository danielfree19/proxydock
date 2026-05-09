# Compiler

`internal/compiler` turns a fleet's proxy-host desired state into the
Traefik dynamic configuration JSON served to agents.

## Protocols

Phase 6 added a `protocol` field to `ProxyHost`. The compiler branches
on it; HTTP keeps its existing emission shape, TCP and UDP get their
own Traefik dynamic-config sections.

| `protocol` | Router section | Rule template            | Service field   | TLS | Middlewares |
| ---------- | -------------- | ------------------------ | --------------- | --- | ----------- |
| `http`     | `http`         | `` Host(`<domain>`) ``   | `url: http://…` | yes | yes         |
| `tcp`      | `tcp`          | `` HostSNI(`<domain>`) `` (or `` HostSNI(`*`) ``) | `address: host:port` | yes (terminate) or no (passthrough) | no |
| `udp`      | `udp`          | (none — entry-point match) | `address: host:port` | no | no          |

UDP routers don't have rules — Traefik matches them on the entry
point alone. The validator therefore enforces (a) at least one
`entry_points` value, and (b) only one UDP router per entry point in
a fleet.

TCP supports a `domain="*"` wildcard that becomes `HostSNI(`*`)` —
Traefik's catch-all for connections with no SNI / no TLS handshake.
Use it for raw TCP routes like Postgres, Redis, custom protocols.

## Inputs / outputs

**Input**: a `[]model.ProxyHost` (the rows for one fleet).

**Output**: a `compiler.Result` with:

- `Config json.RawMessage` — the Traefik dynamic config, ready to
  forward to agents byte-for-byte.
- `ETag string` — `"sha256-<8-hex>"`, derived from the SHA-256 of
  the compiled bytes.

## Per-host emit shape

For each enabled host:

```json
"http": {
  "routers": {
    "<name>": {
      "rule": "Host(`<domain>`)",
      "entryPoints": ["web", ...],
      "service": "<name>",
      "middlewares": ["<name>-0-mw1", "<name>-1-mw2"]
    }
  },
  "services": {
    "<name>": {
      "loadBalancer": {
        "servers": [{"url": "<upstream_url>"}]
      }
    }
  },
  "middlewares": {
    "<name>-0-mw1": { "<type>": { ... } },
    "<name>-1-mw2": { "<type>": { ... } }
  }
}
```

Disabled hosts are dropped silently. Unique middleware names are derived
from the host name + position + middleware name to avoid collisions
between hosts that reuse middleware names.

## Determinism

Same input → byte-identical output, regardless of input order. We sort
hosts by name before emitting and rely on `encoding/json`'s alphabetical
key ordering for `map[string]any`. The unit tests assert this both via
golden files and an explicit shuffle test.

This matters because:

1. The ETag is derived from the bytes; non-determinism would break the
   `If-None-Match` short-circuit on the agents.
2. Reviewing diffs between revisions is much easier when the only
   changes are the ones the operator actually intended.

## Validation

`compiler.Validate(hosts)` runs before `Compile` to surface a list of
errors at once instead of failing on the first bad row. Checks:

- Every proxy host has a non-empty `name`.
- Names are unique within the fleet.
- For **enabled** hosts:
  - `domain` is a plausible hostname (length, label charset).
  - No two enabled hosts share a domain.
  - `upstream_url` parses and uses `http`/`https`.
  - Every middleware has a supported `type`.

Disabled hosts skip the strict checks so users can save partial drafts.

## Supported middleware types

Phase 2 supports a small set that maps directly onto Traefik
built-ins:

| Type             | Required config             | Notes                                          |
| ---------------- | --------------------------- | ---------------------------------------------- |
| `headers`        | (passes through)            | Sent verbatim under `headers`                  |
| `redirectScheme` | `scheme` (default `https`)  | Optional `permanent`, `port`                   |
| `stripPrefix`    | `prefixes` (`[]string`)     | —                                              |
| `basicAuth`      | `users` (`[]string`)        | Each entry is `username:bcrypt-hash`           |
| `forwardAuth`    | `address` (URL)             | OIDC via oauth2-proxy / vouch / traefik-forward-auth — see `forwardauth.md` |
| `rateLimit`      | `average` or `burst`        | Token-bucket; pass through `period`, `sourceCriterion` |
| `ipAllowList`    | `sourceRange` (`[]CIDR`)    | Reject clients outside the listed CIDRs        |
| `retry`          | `attempts` (int > 0)        | Retry idempotent requests on upstream error    |
| `compress`       | (none — `{}` works)         | Gzip / Brotli when supported by the client     |
| `circuitBreaker` | `expression`                | Trip the breaker on `NetworkErrorRatio()` etc. |
| `chain`          | `middlewares` (`[]string`)  | References other middleware *raw* names on the same host; compiler resolves them |

Adding a new type means:

1. Add it to `supportedMiddlewareTypes` in `compiler.go`.
2. Add a `case` in `renderMiddleware` that returns the right shape.
3. Drop a fixture pair in `internal/compiler/testdata/`.
