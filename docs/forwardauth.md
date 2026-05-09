# ForwardAuth & OIDC

ProxyDock supports Traefik's built-in `forwardAuth` middleware as a
desired-state middleware type. It is the conventional way to add
OIDC / SSO in front of a backend that doesn't speak it natively:
Traefik forwards each request to an authenticator (oauth2-proxy,
vouch, traefik-forward-auth, …); the authenticator either returns
2xx (request continues, with identity headers tacked on) or 3xx /
401 (login flow / reject).

## Why `forwardAuth` and not a first-class OIDC middleware

Traefik does not ship an OIDC middleware in the OSS edition — OIDC is
implemented by external authenticators, and Traefik integrates with
them through `forwardAuth`. Modeling a first-class OIDC type in the
manager would either reimplement OAuth2 in Go (large surface, low
upside) or just rename `forwardAuth` (zero benefit). Instead the UI
ships a sensible `forwardAuth` skeleton pre-filled for oauth2-proxy
and operators tweak as needed.

## Schema

```json
{
  "name": "oidc",
  "type": "forwardAuth",
  "config": {
    "address": "http://oauth2-proxy:4180/oauth2/auth",
    "trustForwardHeader": true,
    "authResponseHeaders": [
      "X-Auth-Request-User",
      "X-Auth-Request-Email",
      "X-Auth-Request-Groups"
    ]
  }
}
```

`address` is required (the compiler rejects the chain otherwise). The
remaining fields pass through verbatim to Traefik — anything Traefik's
[forwardAuth docs](https://doc.traefik.io/traefik/middlewares/http/forwardauth/)
accepts is fair game (`tls`, `addAuthCookiesToResponse`,
`authRequestHeaders`, …).

## UI skeleton

In the proxy-host form, picking `forwardAuth` from the middleware
type dropdown rewrites the JSON config to the oauth2-proxy default
above. Switching back to a different type rewrites it again — no need
to clear by hand. Use a different starter if you're running vouch or
traefik-forward-auth:

**vouch-proxy:**

```json
{
  "address": "http://vouch:9090/validate",
  "trustForwardHeader": true,
  "authResponseHeaders": ["X-Vouch-User"]
}
```

**traefik-forward-auth:**

```json
{
  "address": "http://traefik-forward-auth:4181",
  "authResponseHeaders": ["X-Forwarded-User"]
}
```

## End-to-end with oauth2-proxy

A complete deployment needs three moving parts that the manager does
**not** automate (these are operator concerns, not desired state):

1. **An OIDC provider** — Auth0, Authentik, Keycloak, Google Workspace,
   …. You give it a redirect URL pointing at oauth2-proxy
   (`https://<your-domain>/oauth2/callback`).
2. **oauth2-proxy** running somewhere reachable by Traefik (typically
   a sidecar on each Traefik host, or a clustered service). It needs
   `--cookie-domain`, `--whitelist-domain`, `--upstream=static://200`,
   and the OIDC issuer/client/secret.
3. **A Traefik route to oauth2-proxy itself** for the `/oauth2/*`
   callbacks. In ProxyDock terms this is a separate proxy host
   pointing the `oauth2.example.com` (or a subpath) at the
   oauth2-proxy upstream — no `forwardAuth` middleware on that one.

Then on every protected proxy host, attach the `forwardAuth`
middleware. The skeleton's `authResponseHeaders` list which headers
oauth2-proxy is allowed to set on the request as it forwards to your
backend — the backend reads `X-Auth-Request-Email` etc. for identity.

## Compiler emission

A host with a forwardAuth middleware produces:

```json
"http": {
  "middlewares": {
    "<host>-0-<mw-name>": {
      "forwardAuth": {
        "address": "http://oauth2-proxy:4180/oauth2/auth",
        "trustForwardHeader": true,
        "authResponseHeaders": ["X-Auth-Request-User", ...]
      }
    }
  },
  "routers": {
    "<host>": {
      "rule": "Host(`...`)",
      "service": "<host>",
      "entryPoints": [...],
      "middlewares": ["<host>-0-<mw-name>"]
    }
  }
}
```

See `internal/compiler/testdata/forwardauth_*.json` for the canonical
golden file pair.

## Validation

`compiler.Validate` rejects:

- A `forwardAuth` middleware with an empty / missing `address`.
- Any middleware on a TCP / UDP proxy host (Traefik's TCP/UDP routers
  don't support `forwardAuth` — it's HTTP-only by design).
