package main

import (
	"context"
	"log/slog"
	"time"

	"github.com/danielfree19/proxydock/apps/api/internal/metrics"
	"github.com/danielfree19/proxydock/apps/api/internal/store"
)

// metricsRefreshInterval is how often we re-scan the database to
// rebuild the cert-expiry and queue-depth gauges. These metrics aren't
// updated on every API call; a periodic snapshot is sufficient and
// keeps hot paths simple.
const metricsRefreshInterval = 30 * time.Second

// runMetricsRefresh keeps gauges that reflect database state in sync
// with what's actually stored — cert expiry timestamps, in-progress
// job count. Counters/histograms are updated in-line by the handlers
// and worker; this loop covers the things that aren't naturally
// triggered by a request.
func runMetricsRefresh(ctx context.Context, st store.Store, logger *slog.Logger, m *metrics.Registry) {
	if m == nil {
		return
	}
	tick := time.NewTicker(metricsRefreshInterval)
	defer tick.Stop()
	refreshMetrics(ctx, st, logger, m)
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			refreshMetrics(ctx, st, logger, m)
		}
	}
}

func refreshMetrics(ctx context.Context, st store.Store, logger *slog.Logger, m *metrics.Registry) {
	fleets, err := st.ListFleets(ctx)
	if err != nil {
		logger.Warn("metrics refresh: list fleets failed", "err", err)
		return
	}

	// Cert expiry: rebuild the whole gauge family every cycle so deleted
	// certs drop out cleanly. The gauge is bounded by total cert count
	// across the deployment, which stays small in practice.
	m.CertNotAfterUnix.Reset()
	jobsInProgress := 0
	for _, f := range fleets {
		certs, err := st.ListCertificates(ctx, f.ID)
		if err != nil {
			logger.Warn("metrics refresh: list certs failed", "fleet", f.ID, "err", err)
			continue
		}
		for _, c := range certs {
			m.CertNotAfterUnix.
				WithLabelValues(c.FleetID, c.Name, c.Source).
				Set(float64(c.NotAfter.Unix()))
		}
		jobs, err := st.ListACMEJobs(ctx, f.ID, 200)
		if err != nil {
			logger.Warn("metrics refresh: list jobs failed", "fleet", f.ID, "err", err)
			continue
		}
		for _, j := range jobs {
			if j.Status == "pending" || j.Status == "running" {
				jobsInProgress++
			}
		}
	}
	m.ACMEJobsInProgress.Set(float64(jobsInProgress))
}
