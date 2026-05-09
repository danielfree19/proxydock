# Metrics (Phase 5b)

The manager exposes Prometheus metrics at `GET /metrics`. The endpoint
is served from the same port as the API and the web UI, so a single
scrape config covers everything:

```yaml
- job_name: traefik-fleet-manager
  static_configs:
    - targets: ['manager.example.com:8080']
  metrics_path: /metrics
  scheme: https
```

By default `/metrics` is open (the standard Prometheus pattern). To
require a bearer token, set `MANAGER_API_METRICS_TOKEN` in the manager
env and add the matching Authorization header to your scrape config:

```yaml
authorization:
  type: Bearer
  credentials: <MANAGER_API_METRICS_TOKEN value>
```

Metrics use the `tfm_` namespace.

## What's exposed

| Metric                                         | Type      | Labels                   | Notes                                                      |
| ---------------------------------------------- | --------- | ------------------------ | ---------------------------------------------------------- |
| `tfm_http_requests_total`                      | counter   | method, status           | Status is bucketed (2xx/3xx/4xx/5xx) for cardinality.      |
| `tfm_http_request_duration_seconds`            | histogram | method, status           | Default Prometheus buckets (.005s … 10s).                  |
| `tfm_agent_heartbeats_total`                   | counter   | fleet, agent             | One increment per `POST /heartbeat` that authenticates.    |
| `tfm_acme_jobs_in_progress`                    | gauge     | —                        | Pending+running rows. Refreshed every 30 s.                |
| `tfm_acme_jobs_total`                          | counter   | outcome                  | `outcome=succeeded\|failed`. Incremented when a job ends.  |
| `tfm_acme_job_duration_seconds`                | histogram | outcome                  | End-to-end ACME issuance time.                             |
| `tfm_cert_not_after_unix_seconds`              | gauge     | fleet, name, source      | Use `(now() - $value)` for time until expiry.              |
| `tfm_build_info{version="…"}`                  | gauge     | version                  | Always 1; surface deploy version.                          |

The standard Go runtime + process collectors are also registered, so
GC pauses, RSS, FD count, etc. are all in the same scrape.

## Why path isn't in the HTTP labels

We use `method` + bucketed `status` only. Adding the request path
would either explode cardinality (every distinct URL becomes a series)
or require lifting the matched route pattern out of `http.ServeMux`,
which entangles the middleware with the mux. Operational dashboards
care about overall rate / error rate / p99 by method; per-route detail
belongs in logs and traces.

## Why cert expiry is a unix timestamp gauge

Storing the expiry timestamp (rather than seconds remaining) keeps the
metric monotonic between refreshes — Prometheus alert rules use
`(time() - tfm_cert_not_after_unix_seconds < threshold)` to alert
before a cert lapses. The `source` label distinguishes uploaded certs
(static expiry) from ACME-issued ones (the renewal goroutine bumps
the timestamp every couple of months).

## Refresh cadence

- HTTP and heartbeat counters: updated in-line by handlers.
- ACME outcome counter / duration histogram: updated by the worker
  when a job ends.
- ACME jobs-in-progress + cert expiry gauges: refreshed every 30 s
  by `cmd/manager-api/metrics_refresh.go`. They reflect database
  state, so an in-line update on every change isn't needed.

## Suggested alerts

```
# Cert expires in less than 7 days
tfm_cert_not_after_unix_seconds - time() < 7 * 86400

# Agent missed heartbeats
rate(tfm_agent_heartbeats_total[5m]) == 0

# ACME issuance failure rate
rate(tfm_acme_jobs_total{outcome="failed"}[15m]) > 0

# 5xx spike
rate(tfm_http_requests_total{status="5xx"}[5m]) > 0
```
