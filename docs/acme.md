# ACME (Phase 4b)

The manager can request certificates from any RFC 8555 ACME CA using
DNS-01. Agents do not run ACME; only the issued cert lands in the
revision payload they fetch.

## Concepts

- **ACME account** (per fleet): account key + directory URL +
  contact email. Stored in `acme_accounts`. Created via
  `POST /api/v1/fleets/{id}/acme/account` or the **ACME** panel in
  the web UI.
- **DNS provider** (per fleet, by name): one or more rows in
  `dns_providers` carrying `{type, config}`. The challenge driver
  builds a `dns.Provider` from these fields. Phase 4b ships the
  `pebble` type for testing; production providers slot in by
  implementing the `internal/acme/dns.Provider` interface.
- **Certificate source**: `certificates.source` is `"upload"` or
  `"acme"`. Only `"acme"` rows are touched by the renewal goroutine.

## Issue flow (Phase 5b: async)

Issuance is queued — the request returns immediately with a job row,
and a background worker drives the ACME flow.

```
POST /api/v1/fleets/{id}/certificates/acme       (admin auth)
  body: { name, dns_names: [...], dns_provider: "<name>" }
  → 202 Accepted with the job row in pending status

GET  /api/v1/jobs/{id}                            (admin auth)
  → poll until status is "succeeded" or "failed"

GET  /api/v1/fleets/{id}/jobs                     (admin auth)
  → 50 most recent jobs for the fleet
```

Status transitions: `pending → running → (succeeded | failed)`.

The worker:

1. Claims one row via
   `UPDATE … status='running' WHERE id = (SELECT id … FOR UPDATE SKIP LOCKED)`.
2. Loads the fleet's ACME account + named DNS provider.
3. Runs the same `internal/acme.Issuer` Phase 4b shipped (incl. the
   Pebble empty-Location fallback in `pollAndFetch`).
4. On success: writes the cert row, `MarkACMEJobSucceeded`, and
   publishes a fresh revision so agents pick up the new key.
5. On failure: writes the error message via `MarkACMEJobFailed`. The
   `acme_jobs.error` column surfaces in the Jobs tab.

`SKIP LOCKED` lets multiple manager replicas share one queue. We ship
one worker per process; that's enough for the workloads in scope.

The agent endpoint isn't involved — the worker's
`publishRevisionForFleet` call lands the new cert in a new revision,
which agents fetch on their next poll.

## Renewal

A goroutine in `cmd/manager-api/acme_renew.go` runs once at startup
and then every hour:

1. List every certificate where `source='acme'`.
2. For each one past 2/3 of its lifetime
   (`NotBefore + 2/3*(NotAfter-NotBefore)` — same heuristic Let's
   Encrypt and Caddy use), re-issue using the same fleet account +
   first DNS provider.
3. Update the row in place via `UpdateCertificateMaterial`.
4. After all renewals in a fleet, **publish a new revision** so the
   refreshed PEM actually reaches agents.

Renewal failures are logged and retried on the next tick — Phase 4b
has no per-cert backoff.

## Pebble interop

`golang.org/x/crypto/acme` reads `Order.URI` from the response's
`Location` header. Pebble (and possibly other CAs) do not set
`Location` on subsequent GET / finalize responses, leaving the parsed
order's URI empty inside the library. This breaks
`CreateOrderCert`'s internal `WaitOrder` call.

We work around it in `acme.Issuer`:

- After `AuthorizeOrder`, we capture the original `URI` and reapply it
  if a refresh comes back blank.
- If `CreateOrderCert` fails with the empty-URL signature
  (`Post "": unsupported protocol scheme ""`), we fall back to
  manually polling `Client.WaitOrder(originalURI)` and
  `Client.FetchCert(o.CertURL, true)`. The CSR was already accepted by
  the CA — we just couldn't poll for completion via the library's
  default path.

The fallback works for any CA that omits the Location header. If a CA
sets it correctly, `CreateOrderCert` succeeds and the fallback never
runs.

## Demo (Pebble + pebble-challtestsrv)

`MANAGER_API_DEMO_ACME_DIR=https://pebble:14000/dir` and
`MANAGER_API_DEMO_DNS_BASE_URL=http://challtestsrv:8055` (set in the
Compose demo) make the seed:

1. Register an ACME account against Pebble.
2. Configure a `pebble`-typed DNS provider pointing at challtestsrv.
3. Issue a cert for `acme.localhost`.
4. Create an `acme-whoami` proxy host with `tls: true`.
5. Publish a fresh revision so agents pick up both the cert and the
   new host.

Then:

```sh
curl -k -H "Host: acme.localhost" https://localhost:8443
```

The cert is signed by Pebble's self-issued intermediate, so `-k` is
required. In a real deployment you'd point the manager at
`https://acme-v02.api.letsencrypt.org/directory` and a real DNS
provider — the wire shape and renewal logic are identical.

## Production checklist

- [x] **Cloudflare DNS provider**. Config:
      ```json
      {"api_token":"<scoped Zone.DNS:Edit token>","zone_name":"example.com"}
      ```
      `zone_id` may be supplied directly in lieu of `zone_name`. Add
      via the **DNS providers** form on the Certificates tab and pick
      type `cloudflare`. The token only needs `Zone.DNS:Edit` on the
      target zone.
- [x] **Route53 DNS provider**. Config:
      ```json
      {"zone_name":"example.com","region":"us-east-1"}
      ```
      `zone_id` may replace `zone_name` to skip a lookup. Auth
      defaults to the AWS SDK credential chain (env vars, shared
      file, IRSA, EC2 instance profile); supply `access_key` /
      `secret_key` only when none of those are workable. Required
      IAM: `route53:ChangeResourceRecordSets` and
      `route53:ListResourceRecordSets` on the hosted zone, plus
      `route53:ListHostedZonesByName` if you use `zone_name`.
- [ ] **DigitalOcean DNS provider**. ~100 LoC behind the existing
      `internal/acme/dns.Provider` interface.
- [ ] **Encrypted-at-rest secrets**. Account keys and DNS provider
      credentials sit in plaintext columns. Phase 5 hardening adds
      column-level encryption.
- [x] **Async issuance**. `POST /certificates/acme` enqueues an
      `acme_jobs` row and returns 202; a worker goroutine drains the
      queue with `FOR UPDATE SKIP LOCKED`. Status surfaces via
      `GET /api/v1/jobs/{id}` and `GET /api/v1/fleets/{id}/jobs`. The
      web UI polls the request's job until terminal and shows a Jobs
      tab listing recent runs.
- [ ] **Per-cert DNS provider association**. Phase 4b's renewal picks
      the first DNS provider for the fleet; multi-provider setups
      (e.g. apex on Cloudflare, subdomain on Route53) need per-cert
      provider tracking.
- [ ] **Rate limit / backoff handling**. Real CAs throttle; we should
      respect `Retry-After` headers and avoid hammering on validation
      failures.
