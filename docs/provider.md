# Provider plugin (Phase 0)

The plugin lives at `providers/traefik-fleet/` and is loaded by Traefik
as a [Yaegi-based local plugin](https://plugins.traefik.io/install). No
host Go toolchain is needed; Traefik interprets the source directly.

## Module path

```
github.com/danielfree19/proxydock-traefik-fleet
```

The directory containing the source must live under
`/plugins-local/src/<moduleName>/` inside the Traefik container — the
Compose demo bind-mounts the provider source there.

## Static config

```yaml
experimental:
  localPlugins:
    fleet:
      moduleName: github.com/danielfree19/proxydock-traefik-fleet

providers:
  plugin:
    fleet:
      endpoint: "http://manager-api:8080"   # required
      fleetID: "homelab"                    # required
      agentID: "traefik-1"                  # required
      tokenFile: "/run/secrets/traefik-fleet-token"  # or `token: "..."`
      pollInterval: "5s"                    # default 10s, min 1s
      pollTimeout: "5s"                     # default 5s
      cacheFile: "/var/lib/traefik-fleet/last-good.json"  # optional
      failMode: "keep-last-good"            # only mode supported
```

Only one of `token` / `tokenFile` is required. `tokenFile` is preferred
in production because it is re-read on every poll, so rotated secrets
are picked up without restarting Traefik.

## Lifecycle

| Hook        | Behavior                                                           |
| ----------- | ------------------------------------------------------------------ |
| `CreateConfig` | Returns a `Config` with sensible defaults.                      |
| `New`       | Validates and normalizes the config; **no network calls**.         |
| `Init`      | Confirms the token is readable and logs the effective settings.    |
| `Provide`   | Spawns the polling goroutine and returns immediately.              |
| `Stop`      | Cancels the goroutine and waits up to 2s for it to exit.           |

## Polling cycle

1. Compute `If-None-Match` from the last successful ETag.
2. `GET /api/v1/agents/{agentID}/config` with bearer + ETag.
3. Branch on response:
   - `304 Not Modified` → log "config unchanged"; send heartbeat.
   - `200 OK` → validate (agent / fleet / non-empty body), push payload
     onto `cfgChan` as a `json.RawMessage`, persist cache atomically,
     update last applied revision/ETag, send heartbeat.
   - Any other status / network error → log `fetch error` with
     `failMode=keep-last-good`, leave applied config unchanged, send
     heartbeat with `last_error` populated.

## On-disk cache

When `cacheFile` is set, every successful 200 is persisted via
`os.CreateTemp` + `os.Rename`. On startup the plugin attempts to load
this cache and apply it *before* the first poll — so a Traefik restart
during a manager outage still routes traffic.

## Logging

All plugin logs are written to stdout, prefixed with
`[traefik-fleet plugin-fleet agent=<agentID>]`, e.g.

```
[traefik-fleet plugin-fleet agent=traefik-1] applied revision=1 etag="revision-1"
[traefik-fleet plugin-fleet agent=traefik-1] config unchanged (revision=1 etag="revision-1")
[traefik-fleet plugin-fleet agent=traefik-1] fetch error (failMode=keep-last-good, keeping last-good): ...
```
