// Package metrics owns the Prometheus registry and the small set of
// gauges/counters/histograms the manager exposes at /metrics.
//
// We use a private (non-default) registry so the manager's metrics are
// the only thing under our prefix; the standard Go process / runtime
// collectors are added explicitly so they get the same prefix-free
// treatment Prometheus tooling expects.
package metrics

import (
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

const namespace = "tfm"

// Registry holds every metric the manager publishes. One instance is
// created at startup; everything else takes a *Registry pointer.
type Registry struct {
	reg *prometheus.Registry

	HTTPRequests       *prometheus.CounterVec
	HTTPDurationSecs   *prometheus.HistogramVec
	ACMEJobsInProgress prometheus.Gauge
	ACMEJobOutcomes    *prometheus.CounterVec
	ACMEJobDurationSec *prometheus.HistogramVec
	HeartbeatsReceived *prometheus.CounterVec
	CertNotAfterUnix   *prometheus.GaugeVec
	BuildInfo          *prometheus.GaugeVec
}

// New constructs a Registry, registers every metric, and returns it.
//
// version is the manager-api version string, surfaced as a label on
// the build_info gauge so dashboards can group/filter by deploy.
func New(version string) *Registry {
	reg := prometheus.NewRegistry()

	r := &Registry{
		reg: reg,

		// Path is intentionally omitted from labels: we'd need the
		// matched mux pattern (not the raw URL) to keep cardinality
		// bounded, which would entangle the middleware with the mux.
		// method + status bucket is enough for operational dashboards;
		// per-route detail belongs in logs / traces.
		HTTPRequests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace, Subsystem: "http",
			Name: "requests_total",
			Help: "HTTP requests handled by the manager API.",
		}, []string{"method", "status"}),

		HTTPDurationSecs: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: namespace, Subsystem: "http",
			Name:    "request_duration_seconds",
			Help:    "HTTP handler duration.",
			Buckets: prometheus.DefBuckets,
		}, []string{"method", "status"}),

		ACMEJobsInProgress: prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace: namespace, Subsystem: "acme",
			Name: "jobs_in_progress",
			Help: "Number of ACME issuance jobs currently in pending or running state.",
		}),

		ACMEJobOutcomes: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace, Subsystem: "acme",
			Name: "jobs_total",
			Help: "ACME issuance jobs by outcome.",
		}, []string{"outcome"}), // succeeded, failed

		ACMEJobDurationSec: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: namespace, Subsystem: "acme",
			Name:    "job_duration_seconds",
			Help:    "End-to-end ACME issuance duration.",
			Buckets: []float64{1, 2, 5, 10, 20, 30, 60, 120, 240},
		}, []string{"outcome"}),

		HeartbeatsReceived: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace, Subsystem: "agent",
			Name: "heartbeats_total",
			Help: "Heartbeats received per agent.",
		}, []string{"fleet", "agent"}),

		CertNotAfterUnix: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: namespace, Subsystem: "cert",
			Name: "not_after_unix_seconds",
			Help: "Unix timestamp of the cert's expiry. (now() - this) gives time until expiry.",
		}, []string{"fleet", "name", "source"}),

		BuildInfo: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: namespace,
			Name:      "build_info",
			Help:      "Always 1; labels carry build metadata.",
		}, []string{"version"}),
	}

	reg.MustRegister(
		r.HTTPRequests, r.HTTPDurationSecs,
		r.ACMEJobsInProgress, r.ACMEJobOutcomes, r.ACMEJobDurationSec,
		r.HeartbeatsReceived, r.CertNotAfterUnix,
		r.BuildInfo,
		// Standard Go runtime + process collectors so the same scrape
		// covers GC pauses, FD count, RSS, etc.
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
	)
	r.BuildInfo.WithLabelValues(version).Set(1)
	return r
}

// Handler returns an http.Handler suitable for /metrics. When token
// is non-empty the handler requires `Authorization: Bearer <token>`,
// matching the pattern used by the rest of the admin API. An empty
// token leaves the endpoint open (default Prometheus behaviour).
func (r *Registry) Handler(token string) http.Handler {
	scrape := promhttp.HandlerFor(r.reg, promhttp.HandlerOpts{})
	if token == "" {
		return scrape
	}
	want := "Bearer " + token
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Header.Get("Authorization") != want {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		scrape.ServeHTTP(w, req)
	})
}

// ObserveHTTP is a small helper so handler middleware can record a
// single observation without juggling the underlying metric APIs.
func (r *Registry) ObserveHTTP(method string, status int, dur time.Duration) {
	statusStr := codeBucket(status)
	r.HTTPRequests.WithLabelValues(method, statusStr).Inc()
	r.HTTPDurationSecs.WithLabelValues(method, statusStr).Observe(dur.Seconds())
}

// codeBucket collapses status codes into 2xx/3xx/4xx/5xx so label
// cardinality stays bounded even if a future bug returns unusual codes.
func codeBucket(code int) string {
	switch {
	case code < 200:
		return "1xx"
	case code < 300:
		return "2xx"
	case code < 400:
		return "3xx"
	case code < 500:
		return "4xx"
	default:
		return "5xx"
	}
}
