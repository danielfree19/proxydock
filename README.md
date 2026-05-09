# ProxyDock — Traefik Fleet Manager

[![CI](https://github.com/danielfree19/proxydock/actions/workflows/ci.yml/badge.svg)](https://github.com/danielfree19/proxydock/actions/workflows/ci.yml)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

A self-hosted control plane for fleets of [Traefik](https://traefik.io)
proxies — like Nginx Proxy Manager, but for Traefik, multi-instance,
and pull-based.

Each Traefik node runs a small Go provider plugin that **pulls** its
dynamic configuration from a central manager. Agents keep serving the
last known-good config if the manager goes down. The manager owns the
desired state (proxy hosts, certificates, labels) and a deterministic
compiler that turns it into Traefik dynamic JSON. Revisions are
Ed25519-signed; agents verify before applying.

```
┌──────────────┐         ┌──────────────────────┐
│   Web UI     │  HTTP   │     Manager API      │
│  (embedded)  │◄───────►│  Postgres + signer   │
└──────────────┘         └──────────┬───────────┘
                                    │   pull (bearer + ETag)
                                    ▼
                        ┌──────────────────────┐
                        │  Provider plugin     │
                        │  (keep-last-good)    │
                        ├──────────────────────┤
                        │  Traefik runtime     │
                        └──────────────────────┘
```

## Features

| Area                 | What you get                                                                                                              |
| -------------------- | ------------------------------------------------------------------------------------------------------------------------- |
| Routing              | HTTP / TCP / UDP routers; Host / HostSNI rules; per-host TLS toggle (terminate or passthrough). [docs/compiler.md]        |
| Middlewares          | 11 built-in types incl. forwardAuth, rateLimit, ipAllowList, retry, compress, circuitBreaker, chain. [docs/compiler.md]   |
| Middleware library   | Reusable named middleware chains per fleet, applied by copy. [docs/middleware-library.md]                                 |
| Upstreams            | Multiple URLs per host, sticky sessions, optional Traefik health checks. [docs/upstreams.md]                              |
| Service discovery    | Suggest upstreams from a Docker socket — no more guessing IP+port. [docs/discovery.md]                                    |
| Certificates         | PEM upload + ACME DNS-01 (Pebble / Cloudflare / Route53), auto-renewal, encrypted at rest. [docs/certificates.md]         |
| Per-agent labels     | Selector-based revision targeting; the same revision can serve different routes per agent. [docs/labels.md]               |
| Auth & signing       | Admin bearer tokens (sha256-hashed), Ed25519-signed revisions, AES-256-GCM column encryption, audit log. [docs/security.md] |
| Webhooks             | HMAC-signed POST notifications on revision publish / rollback / ACME issue. [docs/webhooks.md]                            |
| Observability        | Prometheus `/metrics` + OpenTelemetry traces over OTLP/HTTP. [docs/metrics.md] [docs/tracing.md]                          |
| Provider plugin      | Stdlib-only, Yaegi-loaded; ETag short-circuit, signature verify, last-known-good cache. [docs/provider.md]                |
| Web UI               | Vite + React + TS, embedded into the manager-api binary via `embed.FS` — no separate UI container, no CORS. [docs/web.md] |

## Quick start

Requirements: Docker + Docker Compose. The Go and Node toolchains are
not needed on the host — the demo image builds both stages internally.

```sh
make demo-up
make hosts-sync     # adds *.localhost domains to /etc/hosts (sudo)
make smoke
```

Then:

- **Web UI**: <http://localhost:8090/> — admin bootstrap token `demo-admin`.
- **Routes**: `http://whoami.localhost:8081`, `https://acme.localhost:8443`.
- **Tracing**: <http://localhost:16686> (Jaeger all-in-one).
- **Metrics**: <http://localhost:8090/metrics>.

Tear down with `make demo-down` (preserves data) or `make demo-clean`
(wipes Postgres + agent caches too).

See [docs/getting-started.md](docs/getting-started.md) for a complete
walkthrough including agent registration, token rotation, and
keep-last-good failover.

## Make targets

```text
make help              # full target list
make build             # compile manager-api with the SPA embedded
make test              # Go unit tests (fast)
make test-integration  # Postgres integration tests via testcontainers
make web               # build the Vite SPA
make web-dev           # Vite dev server with hot-reload
make demo-up/-down     # docker compose lifecycle
make agent-config AGENT=traefik-1   # show what an agent receives
make add-host NAME=… DOMAIN=… UPSTREAM=…   # quick one-shot host create + publish
make hosts-sync        # write live proxy-host domains into /etc/hosts (sudo)
```

## Repository layout

```
apps/
  api/                                 manager-api (Go, embeds the SPA)
    cmd/manager-api/                   main + ACME renewer + workers + metrics refresh
    internal/
      api/                             HTTP handlers (admin + agent)
      auth/                            tfm_<prefix>_<secret> tokens
      compiler/                        desired state → Traefik dynamic JSON
      cryptokit/                       AES-256-GCM cipher + Ed25519 signer
      cert/                            PEM parsing + validation
      acme/                            DNS-01 ACME client + DNS providers
      discovery/                       Docker-socket service discovery
      labels/                          parser for key=value selectors
      metrics/                         Prometheus registry
      tracing/                         OpenTelemetry OTLP/HTTP exporter
      db/migrations/                   embedded SQL migrations
      model/                           shared domain types
      store/                           Store interface + postgres + memory impls
      webui/                           go:embed + SPA fallback handler
  web/                                 Vite + React + TypeScript SPA

providers/
  traefik-fleet/                       Yaegi-loaded Traefik plugin

deploy/
  docker-compose/                      full demo (Postgres, Traefiks, Pebble, Jaeger, httpbin)

scripts/                               host.py helper for the Makefile
docs/                                  user + design docs (one .md per feature)
.github/workflows/                     CI
```

## Documentation

Start at the [docs index](docs/README.md), or jump straight to:

- [Architecture](docs/architecture.md)
- [Getting started](docs/getting-started.md)
- [HTTP API reference](docs/api.md)
- [Compiler](docs/compiler.md) — desired state → Traefik dynamic config
- [Provider plugin](docs/provider.md)
- [Web UI](docs/web.md)
- [Middleware library](docs/middleware-library.md)
- [Upstreams, sticky sessions, health checks](docs/upstreams.md)
- [ForwardAuth / OIDC](docs/forwardauth.md)
- [Service discovery](docs/discovery.md)
- [Certificates (uploaded + ACME)](docs/certificates.md)
- [ACME](docs/acme.md)
- [Per-agent labels](docs/labels.md)
- [Webhooks](docs/webhooks.md)
- [Metrics](docs/metrics.md)
- [Tracing](docs/tracing.md)
- [Security](docs/security.md)
- [Roadmap](docs/roadmap.md)

## Project status

Through Phase 7 (this release):

- Phases 0–5b are stable: persistence, deterministic compiler, web UI,
  uploaded TLS certs, ACME DNS-01 (Pebble/Cloudflare/Route53),
  encryption-at-rest, signed revisions, async ACME jobs, per-agent
  labels, metrics, OpenTelemetry tracing, audit log.
- Phase 6: TCP/UDP routing, ForwardAuth/OIDC middleware templates.
- Phase 7: expanded middleware catalogue (rateLimit, ipAllowList,
  retry, compress, circuitBreaker, chain), middleware template
  library, Docker-socket service discovery, multi-upstream load
  balancing with sticky sessions, optional per-upstream health checks,
  webhooks on revision lifecycle events.

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md). Bug reports, feature requests,
and PRs are welcome. The codebase deliberately keeps dependencies thin
and patterns consistent — please match the existing house style and
add tests where the change is non-obvious.

## License

[MIT](LICENSE).
