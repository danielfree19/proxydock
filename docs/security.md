# Security (Phase 5)

The Phase 5 hardening pass adds three pieces:

1. **Admin authentication** on every non-agent / non-public endpoint.
2. **Encryption-at-rest** for the most sensitive columns.
3. **Signed config revisions** so agents only apply manager-blessed
   compiled configs.

What's still **not** in scope (deferred to Phase 5b/6): per-agent
labels, metrics, async ACME jobs, additional DNS providers, signed
provider plugin code.

## Audit log

Every authenticated admin **mutation** (POST / PUT / PATCH / DELETE)
appends one row to `audit_log` with:

- `actor` — `bootstrap` for env-token-authorized calls,
  `admin:<prefix>` for `admin_tokens`-authorized calls.
- `method` + `path` + HTTP `status` (the response code is recorded
  even on 4xx/5xx, so failed attempts are forensically visible).
- `fleet_id` — best-effort extracted from paths matching
  `/api/v1/fleets/{id}[/...]`. `NULL` for global endpoints
  (`POST /api/v1/fleets`, admin token mgmt, etc.).
- `created_at`.

Reads (GET / HEAD / OPTIONS) are not recorded — they would dominate
the table without forensic value. Unauthenticated rejections leave
no row either: the audit middleware reads the actor from request
context, which `requireAdmin` only populates after a successful auth
check.

The endpoint is `GET /api/v1/admin/audit`, with optional
`?fleet_id=...` (use `global` to filter to entries with no fleet),
`?before=<id>` for pagination, and `?limit=<n>` (default 100, max
500). The web UI surfaces it as the **Audit log** sidebar item.

The middleware order matters and is wired in `Server.Routes`:

```
observe → logging → requireAdmin → audit → mux
```

`requireAdmin` rejects unauthenticated calls before the audit
middleware ever sees them, so the log only contains successful
auth + successful or failed handler runs.

## Threat model

The manager protects three classes of assets:

- **Agent connections**: pull-only, per-agent bearer tokens. Phase 1.
- **Operator connections**: admin API + web UI; Phase 5 introduces the
  bearer-token gate.
- **Persisted secrets**: TLS private keys, ACME account keys, DNS
  provider credentials. Phase 5 encrypts these at rest.

The threat model assumes the operator runs a single trusted manager
instance with a Postgres they control. We do **not** model multi-tenant
isolation, side-channel attacks, or compromised provider plugins.

## Admin authentication

All endpoints under `/api/v1/*` require an admin bearer token, with
two exceptions that match Phase 0–4 semantics:

- `/api/v1/agents/{id}/config` — agent token (the per-agent
  `tfm_<prefix>_<secret>` from Phase 1).
- `/api/v1/agents/{id}/heartbeat` — same.

Plus two intentionally public endpoints:

- `/healthz` — liveness, no auth.
- `/api/v1/signing/pubkey` — the manager's signing public key, by
  design observable.

### Token sources

Two ways to authorize the admin API:

1. **Bootstrap token** from `MANAGER_API_BOOTSTRAP_ADMIN_TOKEN`
   (single arbitrary string). Useful for first-run; remove the env
   var after minting a real token.
2. **Persisted admin tokens** in the `admin_tokens` table, in the same
   `tfm_<prefix>_<secret>` format as agent tokens. SHA-256 of the
   secret half is stored; the prefix is the indexed lookup key.

Both verify in the same middleware
(`api.Server.requireAdmin`/`checkAdminAuth`); both go through
`subtle.ConstantTimeCompare` for the secret check.

### Endpoints

| Method | Path                                              | Description                            |
| ------ | ------------------------------------------------- | -------------------------------------- |
| GET    | `/api/v1/admin/tokens`                            | list admin tokens (metadata only)      |
| POST   | `/api/v1/admin/tokens`                            | mint, returns plaintext **once**       |
| POST   | `/api/v1/admin/tokens/{prefix}/revoke`            | revoke                                 |
| GET    | `/api/v1/admin/whoami`                            | trivial check used by the UI's login   |

### Web UI

The UI's login page (`/login`) captures a token, calls
`/api/v1/admin/whoami`, and on success persists the token in
`sessionStorage` (intentionally tab-scoped). Every subsequent call
through `apps/web/src/api.ts` injects `Authorization: Bearer <token>`.
A "Sign out" button in the sidebar clears the token.

## Encryption at rest

`internal/cryptokit` provides AES-256-GCM with a versioned, prefix-tagged
on-disk format:

```
enc-aes256gcm-v1:<base64-nonce>:<base64-ciphertext+tag>
```

Tagged values round-trip through `Cipher.Decrypt`; untagged values
pass through unchanged so plaintext rows from before encryption was
wired up keep working. The Postgres store transparently encrypts on
write and decrypts on read for these columns:

- `certificates.key_pem`
- `acme_accounts.account_key_pem`
- `dns_providers.config`

The cipher is constructed from `MANAGER_API_ENCRYPTION_KEY`
(64-character hex, 32 bytes). When unset the manager logs a warning
and stores plaintext — a recoverable state for development that should
never happen in production.

A leaked database dump alone is no longer enough to recover usable TLS
private keys / ACME account keys / DNS provider credentials. An
attacker would also need the encryption key.

### Out of scope this phase

- Key rotation. Adding a v2 format requires generating new ciphertext
  for every existing row; we ship the prefix structure but not the
  rotation tooling.
- Per-tenant keys. Single global key for now.
- HSM / KMS integration. The encryption key is read from an env var.

## Signed revisions

When `MANAGER_API_SIGNING_KEY` is set (32-byte hex Ed25519 seed) the
manager signs every newly-published revision's `compiled_config`
bytes. The signature + `"ed25519"` algorithm tag travel back to agents
in the `GET /config` response:

```json
{
  "fleet_id": "...",
  "revision": 2,
  "etag": "\"sha256-..\"",
  "config": { ... },
  "signature": "AmWZrhaX9i6r...Dg==",
  "signature_alg": "ed25519"
}
```

The provider plugin's `signingPublicKey` (base64 ed25519) opts each
agent into verification. When set:

- Missing signature → reject (treated as fetch error, keep-last-good).
- Mismatch → reject, keep-last-good, and surface the error in the
  next heartbeat.

The plugin's verifier (`providers/traefik-fleet/verify.go`) reuses
only `crypto/ed25519` from the standard library so it stays
Yaegi-friendly.

### Byte preservation

`compiled_config` is a `TEXT` column (not `JSONB`) so the bytes the
manager signs are byte-identical to the bytes agents receive — Postgres
JSONB silently normalizes whitespace and key order, which would break
verification. Migration `005_compiled_text.sql` makes the switch.

### Operator workflow

The signing public key is published at `/api/v1/signing/pubkey`. To
turn on verification on existing agents:

```sh
curl http://manager.example.com/api/v1/signing/pubkey
# {"alg":"ed25519","enabled":true,"public_key":"<base64>"}
```

then add `signingPublicKey: "<base64>"` to the provider plugin block
in each agent's static `traefik.yml`. No restart is needed beyond what
Traefik usually requires for static-config changes.

## Demo configuration

`deploy/docker-compose/docker-compose.yml` ships deterministic keys so
the demo is reproducible:

| Env var                                  | Demo value                            |
| ---------------------------------------- | ------------------------------------- |
| `MANAGER_API_ENCRYPTION_KEY`             | 64 hex chars (`0123…cdef…`)           |
| `MANAGER_API_SIGNING_KEY`                | 32 bytes of `0x11`                    |
| `MANAGER_API_BOOTSTRAP_ADMIN_TOKEN`      | `demo-admin`                          |

The matching ed25519 public key
(`0EqyMnQrtKs6E2i9RhXk5tAiSrcaAWuvhSCjMsl3hzc=`) is hard-coded in
`deploy/docker-compose/traefik/traefik-{1,2}.yml` so verification is
on for both demo agents.

**Never** reuse these values outside the demo.

## Service discovery (Phase 7) — operator opt-in

`MANAGER_API_DISCOVERY=docker` makes the manager-api read the local
Docker daemon's `/containers/json` to suggest upstream candidates in
the New Proxy Host form (see [discovery.md](discovery.md)). This
requires mounting `/var/run/docker.sock` into the manager container,
which on most hosts is **root-equivalent**: the Docker daemon will
happily run any container the manager can request, including
privileged or host-networked ones. The `:ro` mount flag is mostly
cosmetic — the socket protocol exposes operations that would let an
attacker who compromises the manager-api escalate to root on the
host.

Trust posture:

- This feature is **off by default**. The env var must be set
  explicitly.
- The Compose demo turns it on because the demo is single-tenant by
  design.
- **Do not enable it on multi-tenant deployments** or anywhere the
  manager-api boundary is not also the host's root boundary.
- For shared deployments, leave discovery off. Operators paste IPs
  + ports manually like any other reverse-proxy admin panel.

## Webhook secrets

The webhook HMAC secrets (Phase 7) are stored encrypted via the same
`cryptokit` cipher used for cert keys / ACME accounts / DNS provider
configs, and are never echoed in API responses (`has_secret` boolean
exposes whether one is set). On `PUT /webhooks/{id}` an empty
`secret` field means *leave the existing one* and a non-empty value
rotates it — mirrors the `dns_providers` pattern.
