# OpenTelemetry tracing

The manager emits OpenTelemetry traces via OTLP/HTTP when the standard
env var is set:

```sh
OTEL_EXPORTER_OTLP_ENDPOINT=http://otel-collector.example:4318
OTEL_SERVICE_NAME=manager-api          # default if unset
OTEL_EXPORTER_OTLP_PROTOCOL=http/protobuf
```

When the endpoint is unset the SDK falls back to a no-op tracer, so
`Tracer.Start` calls in the codebase cost nothing per span. The boot
log makes the choice explicit:

```
{"level":"INFO","msg":"tracing enabled (OTLP/HTTP)"}
{"level":"INFO","msg":"tracing disabled (no OTEL_EXPORTER_OTLP_ENDPOINT)"}
```

## What's instrumented

- **Every HTTP request** via `otelhttp.NewHandler` wrapped around the
  root handler — server-kind span named `<METHOD> <path>`. Inbound
  `traceparent` headers are honoured (so an upstream proxy that runs
  its own tracing keeps the same trace id).
- **The async ACME issuance flow** in `internal/acme/acme.go`:
  ```
  acme.job
   └─ acme.issue                  attrs: dns_names, order_uri
      ├─ acme.authorize_order
      ├─ acme.authorization       (one per FQDN)
      │   ├─ acme.dns_present     attrs: fqdn
      │   └─ acme.wait_authorization
      └─ acme.finalize
  ```
  The `acme.job` span is the natural root for the worker; in-flight
  jobs are easy to find in Jaeger by filtering on the `acme.fleet` /
  `acme.name` attributes.

We deliberately **don't** instrument:

- Postgres queries — keeps the dep surface small. If query-level spans
  ever become useful, `otelpgx` slots in via `pgxpool.Config.Tracer`
  without changing call sites.
- The renewal goroutine and `metrics_refresh` — they're periodic, no
  request to correlate with, and any failure already shows up in
  metrics + logs.

## Demo

The Compose stack ships a Jaeger all-in-one container. Visit
<http://localhost:16686> after running anything against the manager
and pick `manager-api` from the **Service** dropdown.

```yaml
# deploy/docker-compose/docker-compose.yml (excerpt)
jaeger:
  image: jaegertracing/all-in-one:1.57
  environment:
    COLLECTOR_OTLP_ENABLED: "true"
  ports:
    - "16686:16686"
```

The manager-api container is configured to ship traces there:

```yaml
OTEL_SERVICE_NAME: "manager-api"
OTEL_EXPORTER_OTLP_ENDPOINT: "http://jaeger:4318"
OTEL_EXPORTER_OTLP_PROTOCOL: "http/protobuf"
```

Try this for a rich trace tree:

```sh
curl -X POST -H 'Authorization: Bearer demo-admin' -H 'Content-Type: application/json' \
  -d '{"name":"otel-test","dns_names":["otel.localhost"],"dns_provider":"pebble"}' \
  http://localhost:8090/api/v1/fleets/homelab/certificates/acme
```

Then in Jaeger pick **Service: manager-api**, **Operation: acme.job**,
and click the resulting trace. You'll see the full DNS-01 flow with
per-FQDN timing.

## Production checklist

- Use the OTel Collector instead of pointing at Jaeger directly — that
  way the manager doesn't need to know which backends you eventually
  ship to.
- Keep `OTEL_TRACES_SAMPLER` at the default (parent-based, sample
  everything) until you have enough volume to need head-sampling. The
  manager isn't a high-RPS service — every trace is useful.
- The agent endpoints (`/api/v1/agents/{id}/config`, `/heartbeat`) are
  the loudest spans by request count. If they dominate your trace
  budget, set `OTEL_TRACES_SAMPLER=parentbased_traceidratio` with
  `OTEL_TRACES_SAMPLER_ARG=0.05`.
