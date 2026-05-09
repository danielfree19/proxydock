# Roadmap

## Phase 0 — provider spike (this milestone)

- [x] Manager API (`/healthz`, `GET /config`, `POST /heartbeat`).
- [x] Traefik provider plugin (Yaegi-loaded), polling, ETag, heartbeat,
      keep-last-good cache.
- [x] Docker Compose demo with two Traefik instances + whoami.
- [x] Unit tests for token auth, ETag, response validation, cache.

## Phase 1 — persistence and revisions ✅

- [x] PostgreSQL backing store via pgx.
- [x] `fleets`, `agents`, `agent_tokens`, `proxy_hosts`,
      `config_revisions` tables, embedded migrations.
- [x] Publish + rollback. Rollback creates a new revision number that
      copies an older revision's compiled config.
- [x] CRUD endpoints under `/api/v1/fleets/*` and
      `/api/v1/agents/*/tokens*`.
- [x] Bearer token rotation: `tfm_<prefix>_<secret>` format, SHA-256
      hash stored, prefix indexed; mint / list / revoke endpoints.

## Phase 2 — config compiler ✅

- [x] Desired-state model: `ProxyHost` carries domain, upstream URL,
      entry points, and an inline middleware chain.
- [x] Deterministic compilation: same input → byte-identical output.
- [x] Validation: domain, upstream URL scheme, duplicate name/domain,
      supported middleware types.
- [x] Golden-file tests covering simple, multi-host, and middleware
      cases.
- [x] Supported middleware types: `headers`, `redirectScheme`,
      `stripPrefix`, `basicAuth`, `forwardAuth`.

## Phase 3 — web UI ✅

- [x] Vite + React + TypeScript front-end under `apps/web/`.
- [x] Pages: fleets, fleet detail (proxy hosts / agents / revisions
      tabs), proxy host edit form (with middleware editor), revision
      detail, agent detail (heartbeat + token mint/revoke).
- [x] Bundled into the manager-api binary via `embed.FS` (no separate
      UI container, no CORS).
- [ ] Audit log page — deferred until there's an audit log to show
      (Phase 5 hardening introduces one).
- [ ] Login — deferred to Phase 5 admin auth.

## Phase 4 — certificates ✅ (uploaded certs); 4b deferred (ACME)

- [x] Upload PEM cert + key per fleet, parse + validate matching pair,
      surface subject/issuer/SANs/expiry/fingerprint.
- [x] Compiler emits `tls.certificates` pool; per-proxy-host `tls`
      toggle adds `"tls": {}` to the router.
- [x] Web UI: Certificates tab (upload form, expiry tags, rotate via
      upload + delete), TLS toggle in the proxy-host form.
- [x] Demo seed: 1-year self-signed ECDSA cert for `*.localhost`,
      `secure-whoami` proxy host, websecure entry point + 8443/8444
      port mappings.
## Phase 4b — DNS-01 ACME ✅

- [x] `acme_accounts` (per fleet) + `dns_providers` (per fleet, by
      name); `certificates.source` distinguishes uploaded vs ACME.
- [x] `internal/acme` drives `golang.org/x/crypto/acme` for register +
      DNS-01 + issue. Pluggable `internal/acme/dns.Provider` with a
      pebble-challtestsrv implementation.
- [x] Pebble compatibility workaround: re-fetch order to get the
      canonical FinalizeURL, preserve URI across refreshes, fall back
      to manual `WaitOrder` + `FetchCert` when the empty-Location
      header bug fires.
- [x] Renewal goroutine re-issues certs past 2/3 of their lifetime
      and publishes a fresh revision so agents see the new key.
- [x] Web UI ACME panel: register account, manage DNS providers,
      request certs.
- [x] Compose demo: pebble + pebble-challtestsrv containers; seed
      registers, configures, issues, and publishes a real ACME cert
      for `acme.localhost`.

## Phase 5 — hardening (admin auth + encryption + signing) ✅

- [x] Admin authentication: bearer-token middleware on every non-agent
      endpoint, env-bootstrap token + `admin_tokens` table (sha256
      hashed, prefix-indexed), mint / list / revoke endpoints, web
      UI login + token storage.
- [x] Encryption-at-rest via `internal/cryptokit` (AES-256-GCM, prefix-
      tagged round-trip with plaintext passthrough for transparent
      migration). Wired through the Postgres store for
      `certificates.key_pem`, `acme_accounts.account_key_pem`,
      `dns_providers.config`.
- [x] Ed25519-signed revisions: manager signs `compiled_config` at
      publish + rollback + ACME-renewal time; agent endpoint surfaces
      `signature` + `signature_alg`; provider plugin's
      `signingPublicKey` opts into verification (rejects mismatch via
      keep-last-good).
- [x] `compiled_config` migrated JSONB → TEXT so the bytes the manager
      signs are byte-identical to the bytes agents receive.

## Phase 5b — operational features

- [x] Cloudflare DNS provider (`internal/acme/dns/cloudflare.go`,
      registered in `dns.Build`, UI dropdown adds `cloudflare`).
- [x] Per-agent labels (`agents.labels TEXT[]`,
      `proxy_hosts.label_selector TEXT`). Per-fetch agent-side
      recompile + re-sign against snapshotted source state. See
      `docs/labels.md`.
- [x] Async ACME issuance with job tracking. `acme_jobs` table +
      `FOR UPDATE SKIP LOCKED` worker; `POST /certificates/acme`
      returns 202; jobs surface via `GET /api/v1/jobs/{id}`. UI polls
      and shows a Jobs tab.
- [x] Route53 DNS provider via the AWS SDK Go v2; defaults to the
      standard credential chain (env / shared file / IRSA / instance
      profile), supports explicit `access_key`/`secret_key` for the
      cases where none of those work. UI dropdown gains a `route53`
      option.
- [ ] DigitalOcean DNS provider (~100 LoC behind the existing
      `dns.Provider` interface).
- [x] Prometheus metrics at `/metrics` (open by default, optional
      `MANAGER_API_METRICS_TOKEN` gate). HTTP requests + duration,
      ACME outcomes + duration + queue depth, heartbeats, cert
      expiry timestamps, build_info; standard Go runtime collectors
      included. See `docs/metrics.md`.
- [x] OpenTelemetry traces. `internal/tracing` wires the OTLP/HTTP
      exporter when `OTEL_EXPORTER_OTLP_ENDPOINT` is set; otelhttp
      around the root handler emits per-request server spans, and the
      ACME issuer + worker emit a 7-span tree per job. Jaeger
      all-in-one ships in the Compose demo at
      <http://localhost:16686>. See `docs/tracing.md`.
- [ ] Backup / restore of the manager's database.

## Phase 6 — advanced

- [x] TCP / UDP routing in the desired-state model. `proxy_hosts.protocol`
      branches the compiler into Traefik's `tcp` / `udp` sections;
      HostSNI rule (incl. `*` catch-all) for TCP, entry-point-matched
      router for UDP. Demo seeds a TCP route on port 9000 of each
      Traefik. See `docs/compiler.md`.
- [x] ForwardAuth / OIDC middleware templates. `forwardAuth` is now
      a supported middleware type; the proxy-host form ships a
      sensible oauth2-proxy skeleton (URL + standard
      `X-Auth-Request-*` response headers). See `docs/forwardauth.md`.
- [ ] Import an existing Traefik file/Docker provider config into
      the desired state.
- [ ] Canary rollouts: publish to label-matched agents first.
- [ ] Helm chart + Kubernetes operator.

## Phase 7 — middleware library + ops integrations ✅

- [x] Expanded compiler middleware catalogue: `rateLimit`,
      `ipAllowList`, `retry`, `compress`, `circuitBreaker`, `chain`
      (with name resolution to per-host mangled names). See
      `docs/compiler.md`.
- [x] Fleet-scoped middleware template library with apply-by-copy
      semantics. `middleware_templates` table + Library tab in the
      web UI + Apply Template dropdown on the proxy-host form. See
      `docs/middleware-library.md`.
- [x] Service auto-detect via the local Docker socket (opt-in with
      `MANAGER_API_DISCOVERY=docker`). Discover button next to the
      upstream URL field. See `docs/discovery.md`.
- [x] Multiple upstreams per proxy host with optional cookie-based
      sticky sessions and a per-host Traefik `loadBalancer.healthCheck`
      block. Compiler emits N `servers` entries; mixing http/https
      schemes is rejected. See `docs/upstreams.md`.
- [x] Webhooks on revision publish / rollback / ACME-issued events.
      HMAC-signed POSTs (`X-Webhook-Signature: sha256=…`) backed by
      a `webhook_jobs` queue with exponential-backoff retries. See
      `docs/webhooks.md`.
- [x] Per-agent `Config served` panel in the web UI showing the
      exact routers / services / middlewares each agent receives.
- [x] Sync banner on the fleet page surfacing unpublished proxy-host
      changes with a one-click Publish button.

## Future

- DigitalOcean DNS provider (~100 LoC behind `dns.Provider`).
- Database backup / restore tooling.
- Surfacing health-check state from each agent's Traefik instance
  (currently config-only).
- Canary rollouts: publish to a label-matched subset of agents
  first.
- Helm chart + Kubernetes operator.
- Traefik file/Docker provider config import.
