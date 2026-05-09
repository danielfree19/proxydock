# Service discovery

Phase 7 added an opt-in **service auto-detect** feature so operators
adding a proxy host don't have to remember the IP and port of every
container. The manager-api can talk to the local Docker daemon and
suggest running containers as upstream candidates.

> **Trust model.** The Docker socket is root-equivalent on the host.
> This feature is intended for single-tenant, operator-managed
> deployments. **Do not enable it on multi-tenant systems.** See
> [security.md](security.md).

## Enabling

Set `MANAGER_API_DISCOVERY=docker` and mount the Docker socket
read-only. The Compose demo enables both by default; the relevant
fragment:

```yaml
services:
  manager-api:
    environment:
      MANAGER_API_DISCOVERY: "docker"
    user: root          # nonroot can't read /var/run/docker.sock
    volumes:
      - /var/run/docker.sock:/var/run/docker.sock:ro
```

When `MANAGER_API_DISCOVERY` is unset, the `/api/v1/discover/services`
endpoint returns 503 and the Discover button stays hidden in the UI.

## How it works

1. `internal/discovery` defines a `Provider` interface
   (`List(ctx) ([]Service, error)`); only the Docker provider ships
   in v1.
2. The Docker provider opens an `http.Client` against the unix
   socket — stdlib only, no SDK dependency.
3. It calls `GET /containers/{HOSTNAME}/json` to learn which Docker
   networks the manager-api itself is on, then `GET /containers/json`
   for the full list.
4. For each container it returns one `Service` per
   *shared-network × IP* combination, with the container's TCP ports.
5. The manager filters its own container out (no point routing back
   to ourselves) and skips containers with no IP on any shared
   network.

The endpoint:

```text
GET /api/v1/discover/services

200 OK
{
  "provider": "docker",
  "services": [
    {
      "id": "abc123def456",
      "name": "tfm-whoami",
      "image": "traefik/whoami:v1.10",
      "ip": "172.23.0.6",
      "network": "traefik-fleet-demo_tfm",
      "ports": [80]
    },
    ...
  ]
}
```

## Web UI

The proxy host form has a **Discover…** button next to the upstream
URL field. Clicking it loads the candidate list; clicking a port
fills the upstream URL with `http://<ip>:<port>` (or bare `host:port`
for TCP/UDP protocols).

If you click Discover when more than one candidate row is selected
(or when the upstream list already has at least one entry), additional
picks **append** rather than replace — useful for setting up
multi-upstream load balancing across containers.

## Limits

- TCP ports only. UDP discovery would land in a different upstream
  shape (`proxy_hosts.protocol = udp`); a follow-up phase can extend
  this.
- The manager filters to containers on networks the manager itself
  is also on, so suggested IPs are reachable from Traefik agents on
  the same network. Containers on isolated networks won't appear.
- No mDNS/zeroconf, no Kubernetes service discovery, no consul. The
  `Provider` interface is small (just `List`) so adding more is a
  contained change — see CONTRIBUTING.md.

## Adding a new provider

1. Implement `discovery.Provider` (`Name() string`,
   `List(ctx) ([]Service, error)`).
2. Add a case to `discovery.Build` mapping the
   `MANAGER_API_DISCOVERY=<kind>` value to the constructor.
3. Document the env vars / config keys in this file.
4. Don't pull SDK types into store code — keep the provider package
   self-contained, the same way Cloudflare / Route53 keep their
   AWS / Cloudflare imports out of the rest of the codebase.
