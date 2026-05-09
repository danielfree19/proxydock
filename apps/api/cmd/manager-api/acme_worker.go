package main

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	acmepkg "github.com/danielfree19/proxydock/apps/api/internal/acme"
	"github.com/danielfree19/proxydock/apps/api/internal/acme/dns"
	certpkg "github.com/danielfree19/proxydock/apps/api/internal/cert"
	"github.com/danielfree19/proxydock/apps/api/internal/cryptokit"
	"github.com/danielfree19/proxydock/apps/api/internal/metrics"
	"github.com/danielfree19/proxydock/apps/api/internal/model"
	"github.com/danielfree19/proxydock/apps/api/internal/store"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
)

// jobPollInterval is how often the worker scans for pending jobs when
// the queue is empty. On a hit it claims another row immediately, so a
// burst of issuance requests doesn't wait the interval between each one.
const jobPollInterval = 2 * time.Second

// runACMEJobWorker drains acme_jobs rows in pending status. Single
// goroutine; multiple replicas can run side-by-side because
// ClaimNextACMEJob uses FOR UPDATE SKIP LOCKED.
func runACMEJobWorker(ctx context.Context, st store.Store, logger *slog.Logger, httpClient *http.Client, signer *cryptokit.Signer, m *metrics.Registry) {
	tick := time.NewTicker(jobPollInterval)
	defer tick.Stop()
	for {
		// Drain as many rows as we can in one pass before sleeping.
		for {
			job, err := st.ClaimNextACMEJob(ctx)
			if err != nil {
				if !errors.Is(err, store.ErrNotFound) {
					logger.Warn("acme worker: claim failed", "err", err)
				}
				break
			}
			runACMEJob(ctx, st, logger, httpClient, signer, m, job)
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

// runACMEJob runs the ACME flow for one claimed job and persists the
// outcome. Errors during execution mark the row failed but never crash
// the worker.
func runACMEJob(ctx context.Context, st store.Store, logger *slog.Logger, httpClient *http.Client, signer *cryptokit.Signer, m *metrics.Registry, job model.ACMEJob) {
	logger.Info("acme job: running",
		"job_id", job.ID, "fleet", job.FleetID, "name", job.Name,
		"dns_names", job.DNSNames, "dns_provider", job.DNSProvider)

	// Each job gets its own root span. Issuance is async — there's no
	// inbound HTTP request to inherit a trace from — so the span is the
	// natural top of the trace tree the operator sees in Jaeger.
	ctx, span := otel.Tracer("cmd/manager-api").Start(ctx, "acme.job") // span attributes captured up front so even a panic mid-job
	// leaves the trace meaningfully labelled.

	span.SetAttributes(
		attribute.Int64("acme.job_id", job.ID),
		attribute.String("acme.fleet", job.FleetID),
		attribute.String("acme.name", job.Name),
		attribute.StringSlice("acme.dns_names", job.DNSNames),
		attribute.String("acme.dns_provider", job.DNSProvider),
	)

	start := time.Now()
	err := executeACMEJob(ctx, st, httpClient, signer, job)
	dur := time.Since(start).Seconds()
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
	}
	span.End()

	outcome := "succeeded"
	if err != nil {
		outcome = "failed"
		logger.Warn("acme job: failed",
			"job_id", job.ID, "fleet", job.FleetID, "err", err)
		if mErr := st.MarkACMEJobFailed(ctx, job.ID, err.Error()); mErr != nil {
			logger.Warn("acme job: mark failed bookkeeping failed",
				"job_id", job.ID, "err", mErr)
		}
	}
	if m != nil {
		m.ACMEJobOutcomes.WithLabelValues(outcome).Inc()
		m.ACMEJobDurationSec.WithLabelValues(outcome).Observe(dur)
	}
}

func executeACMEJob(ctx context.Context, st store.Store, httpClient *http.Client, signer *cryptokit.Signer, job model.ACMEJob) error {
	account, err := st.GetACMEAccount(ctx, job.FleetID)
	if err != nil {
		return err
	}
	acctSigner, err := acmepkg.ParseAccountKey(account.AccountKeyPEM)
	if err != nil {
		return err
	}
	provider, err := st.GetDNSProviderByName(ctx, job.FleetID, job.DNSProvider)
	if err != nil {
		return err
	}
	dnsImpl, err := dns.Build(provider.Type, json.RawMessage(provider.Config))
	if err != nil {
		return err
	}
	issuer := &acmepkg.Issuer{
		DirectoryURL: account.DirectoryURL,
		ContactEmail: account.ContactEmail,
		AccountKey:   acctSigner,
		AccountURL:   account.AccountURL,
		HTTPClient:   httpClient,
	}
	issueCtx, cancel := context.WithTimeout(ctx, 180*time.Second)
	defer cancel()
	certPEM, keyPEM, err := issuer.Issue(issueCtx, job.DNSNames, dnsImpl)
	if err != nil {
		return err
	}
	parsed, err := certpkg.Parse(certPEM, keyPEM)
	if err != nil {
		return err
	}
	saved, err := st.CreateCertificate(ctx, model.Certificate{
		FleetID:     job.FleetID,
		Name:        job.Name,
		CertPEM:     parsed.CertPEM,
		KeyPEM:      parsed.KeyPEM,
		Fingerprint: parsed.Fingerprint,
		Subject:     parsed.Subject,
		Issuer:      parsed.Issuer,
		DNSNames:    parsed.DNSNames,
		NotBefore:   parsed.NotBefore,
		NotAfter:    parsed.NotAfter,
		Source:      "acme",
	})
	if err != nil {
		return err
	}
	if err := st.MarkACMEJobSucceeded(ctx, job.ID, saved.ID); err != nil {
		return err
	}
	// Publish a fresh revision so the new cert reaches agents.
	if err := publishRevisionForFleet(ctx, st, job.FleetID, "ACME issuance: "+job.Name, signer); err != nil {
		return err
	}
	return nil
}
