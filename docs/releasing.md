# Releasing

ProxyDock follows [Semantic Versioning](https://semver.org/). The
manager-api binary, the provider plugin, and the SPA are versioned
together — they're released as one product because the wire format
between them changes in lockstep.

## Cutting a release

1. **Confirm CI is green** on `main` for the commit you want to tag.
2. **Update the changelog**. Move items from `## [Unreleased]` into a
   new `## [X.Y.Z] — YYYY-MM-DD` section. Keep the Keep-a-Changelog
   sub-headings (Added / Changed / Deprecated / Removed / Fixed /
   Security).
3. **Bump version strings** if any are pinned (the SPA's
   `apps/web/package.json` is the primary one; the Go binary's
   `metrics.New("0.5.0")` call carries a build label that should
   match).
4. **Tag**:

   ```sh
   git tag -s -a v0.7.0 -m "v0.7.0 — Phase 7 (middleware library, discovery, multi-upstream, webhooks)"
   git push origin v0.7.0
   ```

   The `Docker image` workflow (`.github/workflows/docker.yml`)
   builds + pushes a multi-arch (amd64 / arm64) image to
   `ghcr.io/<org>/<repo>/manager-api:vX.Y.Z` and `:X.Y` aliases.
5. **Draft GitHub Release** referencing the changelog section. The
   release notes can be a copy of that section verbatim.
6. **Announce** if appropriate (homelab subreddit, Bluesky, the
   Traefik community channel, etc.).

## Versioning rules

- **Patch** (`0.7.0` → `0.7.1`): bug fixes, doc updates, dependency
  bumps. Backwards-compatible.
- **Minor** (`0.7.x` → `0.8.0`): new features, additive API changes,
  new middleware types, new endpoints, new config keys. The wire
  format between manager and provider plugin remains compatible
  (older plugins keep working).
- **Major** (`0.x` → `1.0`): once the API + plugin contract is
  considered stable. Will require a deprecation period for breaking
  changes.

## Provider-plugin compatibility

The Traefik provider plugin (`providers/traefik-fleet`) is loaded by
Yaegi on each agent. It must:

- Stay stdlib-only (no module deps the user has to install).
- Speak the same `/api/v1/agents/{id}/config` shape as the manager.
- Verify the Ed25519 signature when `signingPublicKey` is configured.

When the plugin is changed, mirror the change in the manager and
update `docs/provider.md`. The plugin's effective version is the
commit hash; users pin it in their Traefik static config.

## Deprecation policy

When a config key or endpoint is going to be removed, follow the
two-release pattern:

1. Release N: introduce the new field. Mark the old one deprecated in
   the docs and changelog. Keep both working.
2. Release N+1 (or later): drop the old field. Note it under
   `### Removed` with the migration recipe.

A current example is `proxy_hosts.upstream_url`: superseded by
`upstream_urls` in 0.7.0, kept as a denormalized copy of
`upstream_urls[0]`. A future minor release will drop it after a
deprecation note in the changelog.
