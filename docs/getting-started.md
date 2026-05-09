# Getting started

This walks through the demo end-to-end: bring it up, log in, register
an agent, publish a revision, exercise an OIDC route, and verify
keep-last-good failover. About 10 minutes.

## Requirements

- Docker + Docker Compose. The demo image builds Go and Node stages
  internally; you don't need either toolchain on the host.
- Linux or macOS (the `make hosts-sync` target is sudo-friendly; the
  rest is OS-agnostic).

## 1. Bring up the stack

```sh
make demo-up
```

This builds and starts:

| Service        | Purpose                                                    |
| -------------- | ---------------------------------------------------------- |
| `manager-api`  | The control plane + embedded SPA on host:8090.             |
| `traefik-1/2`  | Two Traefik agents (HTTP :8081 / :8082, TLS :8443 / :8444).|
| `postgres`     | Single source of truth for desired state.                  |
| `pebble`       | Local ACME test CA — issues real ACME certs in the demo.   |
| `challtestsrv` | DNS-01 mock (handles `_acme-challenge` records).           |
| `jaeger`       | Tracing UI on host:16686.                                  |
| `whoami`       | A simple HTTP backend.                                     |
| `httpbin`      | Stand-in auth gate / webhook sink.                         |

`make hosts-sync` writes the live proxy-host domains into
`/etc/hosts` (sudo prompt) so you can browse to
`http://whoami.localhost:8081/` without a `Host:` header.

## 2. Log in

Open <http://localhost:8090/>. Bootstrap admin token (env-injected
for the demo): **`demo-admin`**.

The first thing to do in a real deployment is mint a real admin token
under **Admin → Tokens**, copy it, then drop the bootstrap env var
from the manager-api container. The bootstrap path is intentionally a
short-lived ramp.

## 3. The seed dataset

The demo seed creates a `homelab` fleet with two agents (`traefik-1`,
`traefik-2`) and a handful of proxy hosts. Tour them in the UI:

- **Proxy hosts** — `whoami` (HTTP), `secure-whoami` (HTTPS with
  self-signed cert), `acme-whoami` (HTTPS with a real ACME cert from
  the bundled Pebble CA), `tcp-whoami` (TCP catch-all on `:9000`).
- **Agents** — heartbeats from the two Traefiks. Click an agent to
  see the **Config served** card: routers, services, and middlewares
  exactly as that agent receives them.
- **Revisions** — every publish/rollback is here, with diffs between
  any two revisions (`/diff` URL).
- **Library** — empty until you create a middleware template (Phase 7).
- **Certificates / Jobs / Webhooks** — empty until you populate them.

## 4. Add a host

Click **+ New proxy host**. The form supports:

- Multiple upstream URLs (Phase 7 — useful for round-robin LB).
- A **Discover…** button next to Upstream (Phase 7) that lists
  reachable Docker containers and fills the URL on click.
- An **Apply template…** button that pulls in a saved middleware
  chain.
- Per-protocol fields: HTTP → domain rule, TCP → HostSNI rule (or `*`
  catch-all), UDP → entry-point-only.
- A health-check section, sticky-session toggle, label selector, etc.

After saving, the **Sync banner** at the top of the fleet page shows
"X proxy hosts edited since revision #N was published". Click
**Publish revision**; agents pick it up within their poll interval
(~5 s in the demo).

## 5. Verify keep-last-good

Stop the manager — the agents should keep serving:

```sh
docker compose stop manager-api
curl -H "Host: whoami.localhost" http://localhost:8081/   # 200, whoami body
docker compose logs traefik-1 | grep -i "fetch error"     # "keeping last-good"
docker compose start manager-api
```

The last-known-good blob is on disk inside each Traefik container,
under `/var/lib/traefik-fleet/last-good.json`. The provider plugin
loads it on startup; even fresh-restarting the agent while the
manager is down still serves the previous config.

## 6. Try the helpers

The Makefile + `scripts/host.py` give you a quick CLI:

```sh
make info                              # what's running, with a live snapshot
make hosts                             # list proxy hosts in $FLEET
make add-host NAME=foo DOMAIN=foo.localhost UPSTREAM=http://whoami:80
make agent-config AGENT=traefik-1      # what each agent has
```

## 7. What to read next

- [HTTP API reference](api.md) — the full surface, copy-pastable.
- [Compiler](compiler.md) — how desired state turns into Traefik JSON.
- [Security](security.md) — admin auth, encryption, signing.
- [Roadmap](roadmap.md) — what's done, what's next.
