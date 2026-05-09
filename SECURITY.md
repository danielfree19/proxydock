# Security policy

## Supported versions

The latest tagged release on the `main` branch is supported. Older
releases are best-effort; please upgrade to receive fixes.

## Reporting a vulnerability

**Do not open public GitHub issues for security problems.**

Please email security findings to `security@proxydock.invalid`
(replace with the real address before publishing) with:

- A description of the vulnerability and its impact.
- Reproduction steps or a proof-of-concept.
- Whether you'd like credit in the release notes.

You should hear back within 5 business days. Coordinated disclosure:
we'll work with you on a fix and won't publish details until a patch
is available.

## Threat model summary

The detailed model lives in [docs/security.md](docs/security.md). The
short version:

- The manager-api is the trust root: admin tokens (sha256-hashed),
  AES-256-GCM column encryption for cert keys / ACME accounts / DNS
  credentials, Ed25519 revision signing.
- The provider plugin verifies signatures before applying any
  configuration. Rejected revisions trigger keep-last-good behaviour.
- Bearer tokens are constant-time compared. Path traversal and
  injection surfaces have been audited (see `docs/security.md`).
- The Docker socket discovery feature (`MANAGER_API_DISCOVERY=docker`)
  is opt-in and root-equivalent on the host; do not enable it on
  multi-tenant deployments.

## What is *not* in scope

- The Compose demo's deterministic keys and bootstrap admin token
  (`demo-admin`). These are documented as test-only credentials.
- Findings against unpatched / outdated dependencies (please open a
  PR or issue with the upgrade instead).
- Issues that require already having full admin access (e.g. an admin
  uploading a malicious cert PEM).
