# Contributing

Thanks for taking the time! Bug reports, feature requests, and PRs are
all welcome. The project is small and intentionally tries to stay that
way — when in doubt, lean toward the simpler change.

## Code of Conduct

Be kind. We follow the [Contributor Covenant](https://www.contributor-covenant.org/).

## Project setup

You don't need Go or Node on your host to develop — the demo's Docker
Compose stack builds and runs everything in containers. But if you
want fast iteration, install:

- Go 1.22+
- Node 20+ (Vite needs ≥18; we test on 20)
- Docker + Docker Compose (for the demo and integration tests)

Quick loop:

```sh
make demo-up                 # bring up the stack
make web-dev                 # run the SPA on :5173, proxying to manager on :8090
# edit Go files, then:
make demo-rebuild            # rebuilds + recreates only the manager-api container
```

## Branching & PRs

- Fork the repo, branch from `main`, open a PR back to `main`.
- Keep changes focused. One logical change per PR.
- Write a clear PR description: what changed, why, how you tested.
- CI must be green: `go test`, `npm run build`, and the linter pass.
- Squash-merge is the default; commit history is rewritten on merge,
  so don't worry about polishing every commit message.

## House style

### Go

- `gofmt` (`make fmt`) must pass.
- `go vet` (`make vet`) must pass.
- Errors get wrapped with `%w` when context matters; otherwise plain
  `fmt.Errorf` is fine.
- Comments explain **why**, not **what**. Hidden constraints, subtle
  invariants, workaround references — yes. Restating the obvious — no.
- Stdlib first. New direct dependencies need a justification in the
  PR description (the provider plugin specifically must stay
  stdlib-only because Yaegi loads it).
- Tests go next to the code (`*_test.go`); golden files in `testdata/`.

### TypeScript / React

- Page-local `useState` is fine; we don't use Redux/Zustand/useReducer.
- No `dangerouslySetInnerHTML`, no `eval`. The SPA renders only data
  it fetches from the manager API, which is the trust boundary.
- Mirror Go-side types in `apps/web/src/types.ts` — only the fields
  the UI consumes, not the whole struct.

### SQL / migrations

- Filenames follow `NNN_<slug>.sql`. `NNN` is monotonic; pick the
  next free number.
- Migrations are forward-only and run in lexical order.
- Don't put signed payloads into JSONB (Postgres normalizes whitespace
  → signature mismatch). TEXT is the right type for any
  byte-preserved blob.

## Adding a new middleware type

The compiler is the source of truth. To add a type:

1. Add it to `supportedMiddlewareTypes` in
   `apps/api/internal/compiler/compiler.go`.
2. Add a `case` to `renderMiddleware` that validates required fields
   and returns `{kind: config}`.
3. Drop a fixture pair in `apps/api/internal/compiler/testdata/`
   (`<type>_input.json` + `<type>_output.json`); add the case name to
   the `TestCompile_Golden` table in `compiler_test.go`.
4. Add the type to `MIDDLEWARE_TYPES` and a starter object to
   `middlewareSkeleton` in
   `apps/web/src/components/MiddlewareEditor.tsx`.
5. Document it in `docs/compiler.md`.

## Adding a new DNS provider

1. Implement `dns.Provider` in `apps/api/internal/acme/dns/<name>.go`.
2. Register it in `dns.Build` (`registry.go`).
3. Tests should exercise the `Provider` interface against a fake
   client (no live API calls in CI). See `dns/cloudflare_test.go`
   and `dns/route53_test.go` for the pattern.
4. Document the config keys in `docs/acme.md`.
5. Add the provider name to the dropdown in
   `apps/web/src/pages/FleetDetail.tsx`.

## Running the test suite

```sh
make test                # Go unit tests (~3s)
make test-integration    # Postgres integration tests (~60s, needs Docker)
cd apps/web && npm run build   # type-check + bundle (~1.5s)
```

Postgres integration tests are gated behind `-tags integration`
because they spin up a real `postgres:16-alpine` per test via
[testcontainers-go](https://golang.testcontainers.org/). They cover
the parts where Postgres semantics matter — encrypted-on-disk
columns, byte-preserved compiled_config, FOR UPDATE SKIP LOCKED job
claiming, FK cascade, etc.

## Reporting security issues

Please **don't** open a public issue for security problems. See
[SECURITY.md](SECURITY.md) for the disclosure process.

## Releasing

See [docs/releasing.md](docs/releasing.md). Tagged releases follow
SemVer; minor bumps for new features, patch bumps for fixes.
