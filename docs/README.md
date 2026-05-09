# Documentation

Start here.

## New to ProxyDock?

- [Getting started](getting-started.md) — a 10-minute demo walkthrough.
- [Architecture](architecture.md) — what owns what, why pull-based.
- [Roadmap](roadmap.md) — what's shipped, what's next.

## Operating

- [HTTP API reference](api.md) — every endpoint, copy-pastable.
- [Provider plugin](provider.md) — the Yaegi-loaded Traefik plugin.
- [Web UI](web.md) — login, fleets, hosts, revisions, the lot.
- [Security](security.md) — admin auth, encryption-at-rest,
  Ed25519 revision signing, threat model.

## Configuration features

- [Compiler](compiler.md) — desired state → Traefik dynamic JSON.
  Lists every supported middleware type.
- [Upstreams, sticky sessions, health checks](upstreams.md)
- [ForwardAuth / OIDC](forwardauth.md) — oauth2-proxy / vouch /
  traefik-forward-auth integration.
- [Middleware library](middleware-library.md) — fleet-scoped reusable
  chains.
- [Per-agent labels](labels.md) — selector-based revision targeting.

## Certificates

- [Certificates](certificates.md) — uploaded PEM + per-fleet pool.
- [ACME](acme.md) — DNS-01 issuance, renewal, async jobs.

## Discovery & integrations

- [Service discovery](discovery.md) — Docker-socket upstream
  suggestions in the New Proxy Host form.
- [Webhooks](webhooks.md) — HMAC-signed POSTs on revision lifecycle
  events.

## Observability

- [Metrics](metrics.md) — Prometheus `/metrics`.
- [Tracing](tracing.md) — OpenTelemetry over OTLP/HTTP.

## Project process

- [Releasing](releasing.md) — how a new tag goes out the door.
- [Contributing](../CONTRIBUTING.md) — house style, PR flow,
  adding new middleware types and DNS providers.
- [Security policy](../SECURITY.md) — how to disclose issues.
