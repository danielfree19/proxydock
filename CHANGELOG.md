# Changelog

All notable changes are tracked here. Format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).
This project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [0.7.0] — 2026-05-09

Phase 7 — middleware library, service auto-detect, multi-upstream, health checks, webhooks.

### Added

- Six new compiler-supported middleware types: `rateLimit`, `ipAllowList`,
  `retry`, `compress`, `circuitBreaker`, `chain` (with name resolution
  against the host's other middlewares).
- `middleware_templates` table + CRUD + a Library tab in the web UI.
  Apply-by-copy semantics; templates are starting points, not live
  references. (See `docs/middleware-library.md`.)
- Service auto-detect via the local Docker socket (opt-in with
  `MANAGER_API_DISCOVERY=docker`). New `GET /api/v1/discover/services`
  endpoint and a Discover button in the proxy host form.
  (See `docs/discovery.md`.)
- Multiple upstream URLs per proxy host with optional cookie-based
  sticky sessions. (See `docs/upstreams.md`.)
- Optional Traefik `loadBalancer.healthCheck` block per HTTP host
  (path/interval/timeout/scheme/hostname/port/followRedirects).
- Webhooks: HMAC-signed POST notifications fired on
  `revision_published` / `revision_rolled_back` /
  `acme_certificate_issued`. Backed by a `webhook_jobs` queue with
  exponential-backoff retries (max 5 attempts).
  (See `docs/webhooks.md`.)
- `make` helpers: `add-host`, `rm-host`, `hosts`, `publish`,
  `hosts-sync`, `hosts-clear`, `agent-config`, `info`, `smoke`.
- Per-agent `Config served` panel on the Agent Detail page —
  admin-readable mirror of what each agent receives via `/config`.
- Sync banner on the fleet page surfacing unpublished proxy-host
  changes with a one-click Publish button.
- "Revision (current → expected)" column on the agents table; warn
  when an agent lags the published revision.

### Changed

- `MiddlewareEditor` extracted from `ProxyHostForm` into
  `apps/web/src/components/MiddlewareEditor.tsx` and reused by the
  middleware template form.
- `proxy_hosts.upstream_url` (TEXT) is now a denormalized copy of
  `upstream_urls[0]` for back-compat. Migration `011_multi_upstream.sql`
  backfills existing rows.
- The compiler now rejects HTTP hosts that mix `http://` and `https://`
  upstream schemes (Traefik's load balancer can't switch per server).

### Compose

- `manager-api` now mounts the host's docker socket read-only (when
  `MANAGER_API_DISCOVERY=docker`) and runs as `root` so it can read
  the socket. Production deployments that don't need discovery should
  unset the env var and revert the user override.
- Added an `httpbin` container as a stand-in auth gate for the
  forwardAuth and webhook demos.

## [0.6.0] — 2026-05-08

Phase 6 — TCP/UDP routing + ForwardAuth/OIDC.

### Added

- `proxy_hosts.protocol` field (`http` | `tcp` | `udp`) with per-protocol
  validation. TCP uses `HostSNI(\`...\`)` (incl. `*` catch-all); UDP
  routers are matched by entry point alone.
- `forwardAuth` middleware type with a sensible oauth2-proxy starter
  in the UI dropdown. (See `docs/forwardauth.md`.)
- `tcpentry` entry point in the demo Traefiks (host `:6900` / `:6901`).

## [0.5.1] — 2026-05-08

Phase 5b — operational features.

### Added

- Cloudflare and Route53 DNS providers (in addition to the existing
  pebble-challtestsrv test provider).
- Per-agent labels + selector-based revision targeting; per-agent
  recompile + re-sign on every fetch from the snapshotted source
  state. (See `docs/labels.md`.)
- Async ACME issuance via an `acme_jobs` queue + worker
  (`FOR UPDATE SKIP LOCKED`). HTTP handler returns 202; UI polls
  `GET /api/v1/jobs/{id}` until terminal.
- Prometheus metrics at `/metrics` with optional bearer-token gate
  via `MANAGER_API_METRICS_TOKEN`. HTTP request/duration counters,
  ACME outcome+duration, queue depth, heartbeats, cert expiry,
  build_info. (See `docs/metrics.md`.)
- OpenTelemetry traces over OTLP/HTTP. otelhttp wraps the root
  handler; ACME emits a 7-span tree per issuance. Jaeger all-in-one
  ships in the Compose demo at <http://localhost:16686>.
  (See `docs/tracing.md`.)
- Append-only audit log of every mutating admin request.

## [0.5.0] — 2026-05-08

Phase 5 — hardening.

### Added

- Admin authentication. `tfm_<prefix>_<secret>` bearer tokens
  (sha256-hashed, prefix-indexed) on every non-agent endpoint.
  Bootstrap token via `MANAGER_API_BOOTSTRAP_ADMIN_TOKEN`; UI login.
- `cryptokit` package: AES-256-GCM column encryption with prefix-tagged
  round-trip + plaintext passthrough for transparent migration.
  Wired through `certificates.key_pem`, `acme_accounts.account_key_pem`,
  `dns_providers.config`.
- Ed25519 revision signing. Manager signs `compiled_config` at publish
  / rollback / ACME-renewal time; the provider plugin's
  `signingPublicKey` opts into verification (mismatch → keep-last-good).
- `compiled_config` migrated JSONB → TEXT so the bytes the manager
  signs are byte-identical to the bytes agents receive.

## [0.4.0] — Phase 4 + 4b

- Uploaded TLS certs (PEM upload, expiry display, rotate) plus a
  per-fleet pool that emits into `tls.certificates` for SNI selection.
- ACME DNS-01 issuance driving `golang.org/x/crypto/acme`. Renewal
  goroutine re-issues past 2/3 of cert lifetime and republishes.

## [0.3.0] — Phase 3

- Vite + React + TypeScript SPA bundled into the manager-api binary
  via `embed.FS`. No separate UI container, no CORS dance.

## [0.2.0] — Phase 2

- Deterministic compiler (proxy hosts → Traefik dynamic JSON) with
  golden-file tests. SHA-256 ETag for `If-None-Match` short-circuit.

## [0.1.0] — Phase 1

- Postgres backing store via pgx. Embedded migrations.
- `fleets`, `agents`, `agent_tokens`, `proxy_hosts`,
  `config_revisions` tables. Publish + rollback semantics.
- Bearer token rotation: `tfm_<prefix>_<secret>` format.

## [0.0.1] — Phase 0

- Manager API spike (`/healthz`, `GET /config`, `POST /heartbeat`).
- Traefik provider plugin (Yaegi-loaded), polling, ETag, heartbeat,
  keep-last-good cache.
- Docker Compose demo with two Traefik instances + whoami.
