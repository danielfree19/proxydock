# Web UI (Phase 3)

A Vite + React + TypeScript SPA lives at `apps/web/`. The Compose demo
serves it from the manager-api itself: same origin, no CORS, no second
container.

## What it covers

- **Fleets list** (`/`) — list, create, delete fleets.
- **Fleet detail** (`/fleets/:id`) — three tabs:
  - Proxy hosts: list, link to edit form, delete.
  - Agents: list with heartbeat status (last seen, revision, provider /
    Traefik version, last error). Register / delete agents.
  - Revisions: publish a new revision (with optional notes), browse
    history, roll back to any older revision. Surfaces compiler
    validation errors directly in the form.
- **Proxy host form** (`/fleets/:id/proxy-hosts/{new|:phId}`) — name,
  domain, upstream URL, entry points, enabled flag, plus a JSON-config
  editor for the four supported middleware types
  (`headers`, `redirectScheme`, `stripPrefix`, `basicAuth`).
- **Revision detail** (`/fleets/:id/revisions/:n`) — pretty-prints the
  compiled Traefik dynamic config and the source proxy-host snapshot
  that produced it.
- **Agent detail** (`/agents/:id`) — heartbeat panel; mint, list, revoke
  bearer tokens. Newly minted plaintext is shown once in a banner.

There is intentionally no login. Admin auth lands in Phase 5.

## How it ships

```
apps/web/                  # Vite source
  src/api.ts               # thin fetch wrapper around /api/v1
  src/types.ts             # mirrors apps/api/internal/model
  src/pages/*.tsx          # one file per route
  src/components/*         # shared bits + tiny useFetch hook
  src/styles.css           # all styles, ~250 lines

apps/api/internal/webui/   # Go package that serves the bundle
  webui.go                 # //go:embed all:dist + SPA fallback handler
  dist/                    # placeholder index.html (committed)
                           # replaced with the real Vite output at
                           # docker build time
```

### Dockerfile

`apps/api/Dockerfile` is a three-stage build:

1. `web-build` (node:20-alpine): runs `npm install && npm run build` in
   `apps/web/`.
2. `api-build` (golang:1.22-alpine): copies `web-build`'s `/web/dist`
   into `internal/webui/dist`, then `go build`. The Go `embed` directive
   picks up the freshly-built assets.
3. distroless final image with just the `manager-api` binary.

The Compose build context is the repo root so both `apps/api/` and
`apps/web/` are reachable.

### Caching

- `/assets/*` (Vite hashes the filenames): `Cache-Control: public,
  max-age=31536000, immutable`.
- Everything else (mainly `index.html`): `Cache-Control: no-cache` so a
  fresh deploy rolls out instantly.

### SPA fallback

Any path that isn't `/api/*`, `/healthz`, or a real file under `dist/`
returns `index.html` with a `200`. React Router takes over from there,
so deep links like `/fleets/homelab/proxy-hosts/3` survive a hard
refresh.

## Local dev (no Docker)

If you have Node and a running Compose demo:

```sh
cd apps/web
npm install
npm run dev   # http://localhost:5173 with /api proxied to :8090
```

`vite.config.ts` proxies `/api` and `/healthz` to `http://localhost:8090`,
so the Vite dev server talks to whatever manager-api you have running.
