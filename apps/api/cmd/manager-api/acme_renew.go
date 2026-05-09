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
	"github.com/danielfree19/proxydock/apps/api/internal/compiler"
	"github.com/danielfree19/proxydock/apps/api/internal/cryptokit"
	"github.com/danielfree19/proxydock/apps/api/internal/model"
	"github.com/danielfree19/proxydock/apps/api/internal/store"
)

// renewalInterval is how often the renewal loop scans the cert table.
const renewalInterval = 1 * time.Hour

// shouldRenew returns true if a cert is past 2/3 of its lifetime, the
// same heuristic Let's Encrypt and Caddy use. For a 90-day cert that's
// ~30 days before expiry; for Pebble's 7-day demo certs it's ~5 days.
func shouldRenew(c model.Certificate, now time.Time) bool {
	lifetime := c.NotAfter.Sub(c.NotBefore)
	if lifetime <= 0 {
		return true
	}
	threshold := c.NotBefore.Add(lifetime * 2 / 3)
	return now.After(threshold)
}

// runACMERenewal scans every ACME-sourced cert on a fixed cadence and
// re-issues those approaching expiry. Failures are logged and retried
// on the next tick. The loop exits when ctx is canceled.
//
// This is intentionally simple — single goroutine, no per-cert
// scheduling, no exponential backoff.
func runACMERenewal(ctx context.Context, st store.Store, logger *slog.Logger, insecureACMEHTTP *http.Client, signer *cryptokit.Signer) {
	tick := time.NewTicker(renewalInterval)
	defer tick.Stop()

	// Run once on startup so a stack that booted with stale certs catches
	// up immediately, instead of waiting an hour.
	renewOnce(ctx, st, logger, insecureACMEHTTP, signer)

	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			renewOnce(ctx, st, logger, insecureACMEHTTP, signer)
		}
	}
}

func renewOnce(ctx context.Context, st store.Store, logger *slog.Logger, httpClient *http.Client, signer *cryptokit.Signer) {
	certs, err := st.ListAllACMECertificates(ctx)
	if err != nil {
		logger.Warn("acme renewal: list failed", "err", err)
		return
	}
	// Track which fleets actually had something renewed so we publish at
	// most once per fleet per pass.
	renewedFleets := map[string]bool{}
	now := time.Now().UTC()
	for _, c := range certs {
		if !shouldRenew(c, now) {
			continue
		}
		if err := renewCert(ctx, st, logger, httpClient, c); err != nil {
			logger.Warn("acme renewal failed",
				"fleet", c.FleetID, "cert", c.Name, "expires", c.NotAfter,
				"err", err)
			continue
		}
		renewedFleets[c.FleetID] = true
	}
	for fleetID := range renewedFleets {
		if err := publishRevisionForFleet(ctx, st, fleetID, "automatic ACME renewal", signer); err != nil {
			logger.Warn("acme renewal: publish failed", "fleet", fleetID, "err", err)
			continue
		}
		logger.Info("acme renewal: published new revision", "fleet", fleetID)
	}
}

// publishRevisionForFleet compiles the fleet's current proxy_hosts +
// certificates and publishes a new revision. Used after the renewal
// goroutine refreshes ACME-sourced certs so the new key material
// actually reaches agents.
func publishRevisionForFleet(ctx context.Context, st store.Store, fleetID, notes string, signer *cryptokit.Signer) error {
	hosts, err := st.ListProxyHosts(ctx, fleetID)
	if err != nil {
		return err
	}
	certs, err := st.ListCertificates(ctx, fleetID)
	if err != nil {
		return err
	}
	res, err := compiler.Compile(hosts, certs)
	if err != nil {
		return err
	}
	num, err := st.NextRevisionNumber(ctx, fleetID)
	if err != nil {
		return err
	}
	source, err := json.Marshal(hosts)
	if err != nil {
		return err
	}
	sourceCerts, err := json.Marshal(certs)
	if err != nil {
		return err
	}
	rev := model.Revision{
		FleetID: fleetID, Number: num,
		CompiledConfig: res.Config, SourceProxyHosts: source, SourceCerts: sourceCerts,
		ETag: res.ETag, Notes: notes,
	}
	if signer != nil {
		rev.Signature = signer.Sign(rev.CompiledConfig)
		rev.SignatureAlg = cryptokit.SignatureAlg
	}
	_, err = st.CreateRevision(ctx, rev, true)
	return err
}

// renewCert is broken out so the seed code path can call it directly
// when bootstrapping a brand-new ACME cert.
func renewCert(ctx context.Context, st store.Store, logger *slog.Logger, httpClient *http.Client, c model.Certificate) error {
	logger.Info("renewing acme cert", "fleet", c.FleetID, "cert", c.Name)

	account, err := st.GetACMEAccount(ctx, c.FleetID)
	if err != nil {
		return err
	}
	signer, err := acmepkg.ParseAccountKey(account.AccountKeyPEM)
	if err != nil {
		return err
	}

	// Pick the first DNS provider configured for the fleet. Phase 4b
	// doesn't track per-cert provider associations; the assumption is
	// one provider per fleet, which matches the demo.
	providers, err := st.ListDNSProviders(ctx, c.FleetID)
	if err != nil {
		return err
	}
	if len(providers) == 0 {
		return errNoDNSProvider
	}
	pCfg := providers[0]
	// ListDNSProviders strips Config; reload through Get for the bytes.
	full, err := st.GetDNSProvider(ctx, pCfg.FleetID, pCfg.ID)
	if err != nil {
		return err
	}
	dnsImpl, err := dns.Build(full.Type, json.RawMessage(full.Config))
	if err != nil {
		return err
	}

	issuer := &acmepkg.Issuer{
		DirectoryURL: account.DirectoryURL,
		ContactEmail: account.ContactEmail,
		AccountKey:   signer,
		AccountURL:   account.AccountURL,
		HTTPClient:   httpClient,
	}

	issueCtx, cancel := context.WithTimeout(ctx, 120*time.Second)
	defer cancel()
	certPEM, keyPEM, err := issuer.Issue(issueCtx, c.DNSNames, dnsImpl)
	if err != nil {
		return err
	}
	parsed, err := certpkg.Parse(certPEM, keyPEM)
	if err != nil {
		return err
	}
	updated := c
	updated.CertPEM = parsed.CertPEM
	updated.KeyPEM = parsed.KeyPEM
	updated.Fingerprint = parsed.Fingerprint
	updated.Subject = parsed.Subject
	updated.Issuer = parsed.Issuer
	updated.DNSNames = parsed.DNSNames
	updated.NotBefore = parsed.NotBefore
	updated.NotAfter = parsed.NotAfter
	if err := st.UpdateCertificateMaterial(ctx, updated); err != nil {
		return err
	}
	logger.Info("renewed acme cert",
		"fleet", c.FleetID, "cert", c.Name, "new_not_after", updated.NotAfter)
	return nil
}

var errNoDNSProvider = errors.New("acme renewal: no DNS provider configured for fleet")
