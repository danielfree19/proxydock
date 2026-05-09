# Per-agent labels (Phase 5b)

The manager can route different proxy hosts to different agents within
the same fleet via simple key/value label selectors.

## Concepts

- **Agent labels**: each agent carries an array of `key=value` strings
  (`agents.labels TEXT[]`). Set via the **Labels** field on the agent
  detail page or `PUT /api/v1/agents/{id}/labels`.
- **Proxy host label selector**: each proxy host has a comma-separated
  selector string (`proxy_hosts.label_selector TEXT`). Empty selector
  (the default) matches every agent.
- **Match semantics**: each requirement in the selector must be
  present (and value-equal) in the agent's labels. Equality matching
  only — no `key in (a,b)` / `!=` operators yet.

```
selector "region=us,tier=prod"   matches   ["region=us","tier=prod","extra=x"]
selector "region=us,tier=prod"   skips     ["region=us"]
selector ""                      matches   anything
```

## How it flows through the stack

1. Operator publishes a revision; the manager snapshots the fleet's
   current proxy hosts AND certificates into `config_revisions`
   (`source_proxy_hosts`, `source_certs` columns).
2. Agent polls `/api/v1/agents/{id}/config` with its bearer token.
3. Manager loads the published revision plus the calling agent's row
   (with its current labels), filters `source_proxy_hosts` by the
   selector, recompiles against `source_certs`, and re-signs.
4. The agent's `If-None-Match` ETag short-circuit still works: the
   per-agent ETag is the sha256 of the per-agent compiled bytes.

Filtering happens **per fetch**, not per publish. That means editing
an agent's labels takes effect on its next poll without needing a
republish, and two agents with different labels in the same fleet see
different (correctly-signed) configs from the same revision.

## Why source_certs is snapshotted alongside source_proxy_hosts

Per-agent recompilation runs on whatever certs the snapshot recorded,
not whatever certs are in the fleet *now*. Without the snapshot, a
later cert rotation would silently change what an agent receives
mid-revision — the published revision and what agents apply would drift.

Phase 5b stores the cert snapshot inline as JSON in the revision row
(`source_certs TEXT`), the same way `source_proxy_hosts` is stored.
Cipher decryption of cert key material happens at scan time, so the
snapshot already has plaintext keys when the recompile runs.

## API

### Update labels

```
PUT /api/v1/agents/{agent_id}/labels
Authorization: Bearer <admin-token>

{ "labels": ["region=us-east", "tier=prod"] }
```

The handler rejects malformed entries (missing `=` or empty key)
rather than silently dropping them — silent drops would let agents
miss matches without operators noticing.

### Edit selector on a proxy host

The selector is part of the existing `POST/PUT /proxy_hosts`
payload:

```json
{
  "name": "us-only",
  "domain": "us.example.com",
  "upstream_url": "http://api:8080",
  "label_selector": "region=us"
}
```

Bad selectors (e.g. `keyOnly` without `=`) are rejected with `400`.

## Example flow

```sh
# Tag the agents
curl -X PUT -H 'Authorization: Bearer admin' \
  -d '{"labels":["region=us"]}' \
  http://manager/api/v1/agents/traefik-1/labels
curl -X PUT -H 'Authorization: Bearer admin' \
  -d '{"labels":["region=eu"]}' \
  http://manager/api/v1/agents/traefik-2/labels

# Make a host that only goes to US agents
curl -X POST -H 'Authorization: Bearer admin' -H 'Content-Type: application/json' \
  -d '{"name":"us-api","domain":"api-us.example.com","upstream_url":"http://api:8080","label_selector":"region=us"}' \
  http://manager/api/v1/fleets/homelab/proxy_hosts

curl -X POST -H 'Authorization: Bearer admin' \
  http://manager/api/v1/fleets/homelab/revisions

# After one poll cycle: api-us.example.com routes through traefik-1
# (region=us) but 404s through traefik-2 (region=eu).
```

## Limitations

- Equality matching only (no `in`, `!=`, or `!key` set-based ops).
- No "would target N agents" preview yet — the manager has the
  information; the UI doesn't surface it.
- Per-agent compilation runs on every fetch. With `If-None-Match` the
  cost is one `compiler.Compile` + signature regen (~hundreds of µs),
  fine up to thousands of agents per fleet. Phase 6 might cache by
  label-set hash if profiles ever say otherwise.
