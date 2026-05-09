# Certificates (Phase 4)

The manager owns a per-fleet pool of TLS certificates. When a revision
is published, every certificate in the fleet is included in the
revision's compiled config under `tls.certificates`. Traefik selects
the right one for each TLS-enabled router via SNI.

## Lifecycle

1. Operator uploads a PEM certificate + private key via
   `POST /api/v1/fleets/{id}/certificates` or the **Certificates** tab
   in the web UI.
2. The manager parses the cert, validates that the cert and key match
   (`tls.X509KeyPair`), extracts metadata (subject, issuer, DNS SANs,
   validity window, sha256 fingerprint), and stores everything.
3. On the next publish, the cert lands in `tls.certificates` of the new
   revision; agents apply it on their next poll.
4. To rotate: upload the replacement under a new name, publish, then
   delete the old one and publish again. (Or just delete and re-upload
   under the same name if you don't care about overlap.)

## Per-host TLS toggle

A proxy host has a boolean `tls` field. When true:

- The router gets `"tls": {}` in the compiled config.
- The operator should add `"websecure"` to the host's entry points.

The compiler does **not** force `websecure` automatically — you may
want a router that only listens on `websecure`, or one that listens on
both `web` and `websecure` together with a `redirectScheme` middleware.

## Wire shape

The compiler emits PEM bytes inline (Traefik's `FileOrContent`
accepts either a path or the actual content). Example excerpt:

```json
{
  "tls": {
    "certificates": [
      {
        "certFile": "-----BEGIN CERTIFICATE-----\n...",
        "keyFile":  "-----BEGIN EC PRIVATE KEY-----\n..."
      }
    ]
  },
  "http": {
    "routers": {
      "secure-whoami": {
        "rule": "Host(`secure.localhost`)",
        "entryPoints": ["websecure"],
        "service": "secure-whoami",
        "tls": {}
      }
    }
  }
}
```

## Storage

Phase 4 stores both `cert_pem` and `key_pem` in plaintext columns. This
is the same security level as the demo's bearer tokens: fine for a
homelab, **not** appropriate for production with sensitive keys. Phase 5
hardening adds column-level encryption with a manager-side master key.

The agent endpoint always strips `key_pem` from API responses; the
private key only ever leaves the manager process via the agent-facing
revision payload (which is already authenticated with a per-agent
bearer token). Admin list/get endpoints return `key_pem: ""` even with
no auth, since admin auth is itself deferred.

## Demo

`MANAGER_API_DEMO_SEED=true` generates a 1-year self-signed ECDSA cert
for `whoami.localhost`, `secure.localhost`, and `*.localhost` on first
boot, uploads it, and creates a `secure-whoami` proxy host that
listens on `websecure` with `tls: true`.

```sh
curl -k -H "Host: secure.localhost" https://localhost:8443
curl -k -H "Host: secure.localhost" https://localhost:8444
```

The `-k` flag is required because the seeded cert is self-signed; in
real deployments you'd upload a cert signed by a CA your clients
trust, or use the (forthcoming) ACME flow.

## Not yet built

- ACME (Let's Encrypt / DNS-01) from the control plane.
- Column-level encryption of stored private keys.
- Per-host explicit cert pinning (assign a specific cert to a router
  rather than relying on SNI match).
- Cert chain validation against a trust store (we accept any
  syntactically valid chain, including self-signed).
