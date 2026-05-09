# Architecture

## Components

- **Manager API** (`apps/api`)
  - Go HTTP server backed by Postgres (pgx).
  - Owns desired state: fleets, agents, agent tokens, proxy hosts,
    config revisions.
  - Hashes every minted token (SHA-256 of the random secret half;
    a non-secret 8-hex-char prefix is the lookup key).
  - Compiler turns proxy hosts into Traefik dynamic config; revisions
    capture the compiled output, an ETag, and the source-of-truth
    snapshot of the proxy hosts.

- **Provider plugin** (`providers/traefik-fleet`)
  - Loaded by Traefik via Yaegi as a local plugin
    (`experimental.localPlugins`). No host Go toolchain.
  - Polls the manager, validates responses, persists last-known-good to
    disk, and pushes configuration into Traefik's `cfgChan`.
  - Reports a heartbeat once per poll cycle (success or failure).

- **Postgres**
  - Authoritative store for everything except the agents' applied
    cache. Migrations live in `apps/api/internal/db/migrations/` and are
    applied automatically at manager-api startup.

## Pull-based by design

Agents are the only side that initiates connections:

- Agents need outbound HTTP to the manager.
- Agents do **not** expose any management port to the manager.
- The manager has no list of agents to "push" to; it serves whoever
  arrives with a valid bearer token, scoped to that token's agent.

## Data model

```
fleets ─┐
        ├── agents ── agent_tokens
        ├── proxy_hosts (the desired state)
        └── config_revisions (compiled snapshots)

fleets.published_revision_id ──> config_revisions.id (current published)
```

Each fleet has at most one published revision. Agents always fetch the
fleet's published revision; rolling back means publishing a copy of an
older revision under a fresh number.

## Revision diffs

Each revision row stores a JSON snapshot of the source state that
produced it (`source_proxy_hosts`, `source_certs`). The web UI's
**Diff** column on the revisions list links to a per-revision-pair diff
page (`/fleets/:id/revisions/:from/diff/:to`) that:

- Walks both snapshots, matched by `name`.
- Classifies each entry as `added` / `removed` / `modified` /
  `unchanged`.
- For `modified` rows, surfaces a per-field change list (domain,
  upstream URL, entry points, middlewares, TLS, label selector,
  enabled).
- Hides unchanged rows so operators can scan for the actual diff.

The diff is computed client-side in `apps/web/src/components/diff.ts`;
no new server endpoints. Snapshots are bounded (10s of hosts), so
sending two revision payloads is fine.

## Compile + publish flow

```
proxy_hosts (desired state)        ──┐
                                     ▼
            compiler.Compile() ─► Validation (domain / upstream / middleware shape)
                                     │
                                     ├─ deterministic JSON
                                     └─ sha256-prefixed ETag
                                     ▼
   INSERT INTO config_revisions (...) RETURNING id
                                     ▼
   UPDATE fleets SET published_revision_id = <new id>
                                     ▼
            agents pull → ETag-aware 200 / 304
```

The compiler is pure: same inputs → byte-identical outputs. Tests in
`internal/compiler/testdata/` exercise this with golden files.

## Static vs dynamic config

The manager only owns Traefik's **dynamic** configuration:

- Routers, services, middlewares, TLS options/certs (HTTP/TCP/UDP).

Anything in Traefik's **static** config — entry points, plugin
declarations, ACME resolver setup, the dashboard, metrics — is the
operator's responsibility on each agent and is baked into the per-agent
config file.

## Failure modes

The provider's `failMode` is `keep-last-good` (currently the only
supported mode):

- **Manager unreachable / 5xx**: log the error, keep the last applied
  config in memory, do not push to `cfgChan`. Heartbeats fail silently
  but resume on recovery.
- **Auth failure** (`401`/`403`): same as unreachable. This is exactly
  what happens after revoking the token in use; routing keeps working
  while the operator rotates secrets.
- **Invalid response** (decode error, agent/fleet ID mismatch, empty /
  null / `{}` payload): rejected; last-known-good kept.

On startup the provider applies the on-disk cache *before* the first
poll, so a Traefik restart while the manager is down still routes
traffic from the previous good revision.
