// manager-api is the manager control-plane HTTP server.
//
// Phase 1+ wiring:
//   - Open a pgx connection pool from DATABASE_URL.
//   - Apply embedded SQL migrations.
//   - Optionally seed the demo dataset (homelab fleet + agents + tokens
//   - whoami proxy host) when MANAGER_API_DEMO_SEED=true.
//   - Serve agent-facing + admin HTTP API.
package main

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	cryptorand "crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	acmepkg "github.com/danielfree19/proxydock/apps/api/internal/acme"
	dnspkg "github.com/danielfree19/proxydock/apps/api/internal/acme/dns"
	"github.com/danielfree19/proxydock/apps/api/internal/api"
	"github.com/danielfree19/proxydock/apps/api/internal/auth"
	certpkg "github.com/danielfree19/proxydock/apps/api/internal/cert"
	"github.com/danielfree19/proxydock/apps/api/internal/compiler"
	"github.com/danielfree19/proxydock/apps/api/internal/cryptokit"
	"github.com/danielfree19/proxydock/apps/api/internal/db"
	"github.com/danielfree19/proxydock/apps/api/internal/discovery"
	"github.com/danielfree19/proxydock/apps/api/internal/metrics"
	"github.com/danielfree19/proxydock/apps/api/internal/model"
	"github.com/danielfree19/proxydock/apps/api/internal/store"
	pgstore "github.com/danielfree19/proxydock/apps/api/internal/store/postgres"
	"github.com/danielfree19/proxydock/apps/api/internal/tracing"
	"github.com/danielfree19/proxydock/apps/api/internal/webui"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

func main() {
	addr := envOr("MANAGER_API_ADDR", ":8080")
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		fmt.Fprintln(os.Stderr, "DATABASE_URL is required")
		os.Exit(2)
	}
	demoSeed := os.Getenv("MANAGER_API_DEMO_SEED") == "true"
	insecureACME := os.Getenv("MANAGER_API_INSECURE_ACME") == "true"
	demoACMEDir := os.Getenv("MANAGER_API_DEMO_ACME_DIR")
	demoChallSrv := os.Getenv("MANAGER_API_DEMO_DNS_BASE_URL")
	encryptionKeyHex := os.Getenv("MANAGER_API_ENCRYPTION_KEY")
	signingKeyHex := os.Getenv("MANAGER_API_SIGNING_KEY")
	bootstrapAdminToken := os.Getenv("MANAGER_API_BOOTSTRAP_ADMIN_TOKEN")
	metricsToken := os.Getenv("MANAGER_API_METRICS_TOKEN")
	discoveryKind := os.Getenv("MANAGER_API_DISCOVERY")

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
	slog.SetDefault(logger)

	bootCtx, bootCancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer bootCancel()

	traceShutdown, traceEnabled, err := tracing.Setup(bootCtx, "manager-api", "0.5.0")
	if err != nil {
		logger.Error("tracing setup", "err", err)
		os.Exit(1)
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := traceShutdown(shutdownCtx); err != nil {
			logger.Warn("tracing shutdown", "err", err)
		}
	}()
	if traceEnabled {
		logger.Info("tracing enabled (OTLP/HTTP)")
	} else {
		logger.Info("tracing disabled (no OTEL_EXPORTER_OTLP_ENDPOINT)")
	}

	pool, err := db.Open(bootCtx, dsn)
	if err != nil {
		logger.Error("open db", "err", err)
		os.Exit(1)
	}
	defer pool.Close()

	if err := waitForDB(bootCtx, logger, pool); err != nil {
		logger.Error("postgres did not become ready", "err", err)
		os.Exit(1)
	}
	if err := db.Migrate(bootCtx, pool); err != nil {
		logger.Error("migrate", "err", err)
		os.Exit(1)
	}

	var cipher *cryptokit.Cipher
	if encryptionKeyHex != "" {
		c, err := cryptokit.NewCipherHex(encryptionKeyHex)
		if err != nil {
			logger.Error("encryption key", "err", err)
			os.Exit(1)
		}
		cipher = c
		logger.Info("column-level encryption enabled")
	} else {
		logger.Warn("MANAGER_API_ENCRYPTION_KEY is unset; secrets will be stored as plaintext")
	}

	var signer *cryptokit.Signer
	if signingKeyHex != "" {
		seed, err := hex.DecodeString(signingKeyHex)
		if err != nil {
			logger.Error("signing key not hex", "err", err)
			os.Exit(1)
		}
		s, err := cryptokit.NewSigner(seed)
		if err != nil {
			logger.Error("signing key", "err", err)
			os.Exit(1)
		}
		signer = s
		logger.Info("revision signing enabled", "public_key", signer.PublicKey())
	} else {
		logger.Warn("MANAGER_API_SIGNING_KEY is unset; revisions will not be signed")
	}

	st := pgstore.New(pool, cipher)

	if demoSeed {
		if err := seedDemo(bootCtx, st, logger, demoACMEDir, demoChallSrv, insecureACME, signer); err != nil {
			logger.Error("seed demo", "err", err)
			os.Exit(1)
		}
	}

	mreg := metrics.New("0.5.0")
	discoveryProv, err := discovery.Build(discoveryKind)
	if err != nil {
		logger.Error("discovery init failed", "kind", discoveryKind, "err", err)
		os.Exit(1)
	}
	if discoveryProv != nil {
		logger.Info("service discovery enabled", "provider", discoveryProv.Name())
	}
	srv := &api.Server{
		Logger:              logger,
		Store:               st,
		Signer:              signer,
		BootstrapAdminToken: bootstrapAdminToken,
		InsecureACME:        insecureACME,
		Metrics:             mreg,
		MetricsToken:        metricsToken,
		Discovery:           discoveryProv,
	}
	if metricsToken == "" {
		logger.Info("metrics enabled at /metrics (open scrape)")
	} else {
		logger.Info("metrics enabled at /metrics (bearer-token gated)")
	}
	if bootstrapAdminToken != "" {
		logger.Warn("MANAGER_API_BOOTSTRAP_ADMIN_TOKEN is set; remove after creating a real admin token via the UI / API")
	} else {
		logger.Info("admin auth: no bootstrap token; provision admin tokens before exposing the manager publicly")
	}

	// Background goroutines: renewer + async-issuance worker + metrics refresher.
	bgCtx, cancelBg := context.WithCancel(context.Background())
	defer cancelBg()
	go runACMERenewal(bgCtx, st, logger, acmeHTTPClient(insecureACME), signer)
	go runACMEJobWorker(bgCtx, st, logger, acmeHTTPClient(insecureACME), signer, mreg)
	go runMetricsRefresh(bgCtx, st, logger, mreg)
	go runWebhookWorker(bgCtx, st, logger)

	// Wrap the API mux so the same port serves the embedded web UI at /
	// while /api/* and /healthz still hit the JSON handlers. otelhttp
	// adds an HTTP server span around every request; the SpanNameFormatter
	// drops the path (high-cardinality), keeping the span name to method
	// only — sub-spans inside handlers carry the operation-level detail.
	rootHandler := otelhttp.NewHandler(
		webui.Handler(srv.Routes()),
		"manager-api",
		otelhttp.WithSpanNameFormatter(func(_ string, r *http.Request) string {
			return r.Method + " " + r.URL.Path
		}),
	)
	httpSrv := &http.Server{
		Addr:              addr,
		Handler:           rootHandler,
		ReadHeaderTimeout: 5 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go func() {
		logger.Info("manager-api listening", "addr", addr, "demo_seed", demoSeed)
		if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("http server failed", "err", err)
			stop()
		}
	}()

	<-ctx.Done()
	logger.Info("shutting down")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := httpSrv.Shutdown(shutdownCtx); err != nil {
		logger.Error("graceful shutdown failed", "err", err)
		os.Exit(1)
	}
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// waitForDB pings the database with backoff until the parent context times out.
func waitForDB(ctx context.Context, logger *slog.Logger, pool interface{ Ping(context.Context) error }) error {
	delay := 250 * time.Millisecond
	for {
		if err := pool.Ping(ctx); err == nil {
			return nil
		} else {
			logger.Info("waiting for postgres", "err", err.Error())
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(delay):
		}
		if delay < 2*time.Second {
			delay *= 2
		}
	}
}

// seedDemo creates the homelab fleet, two agents with deterministic
// dev tokens, a whoami proxy host, the self-signed demo cert, and (when
// the relevant env vars are set) an ACME account against Pebble plus a
// pebble-challtestsrv DNS provider plus an ACME-issued cert.
//
// All operations are idempotent (conflicts are ignored) so the seed can
// safely run on every container start.
func seedDemo(ctx context.Context, st store.Store, logger *slog.Logger, acmeDirURL, challtestsrvURL string, insecureACME bool, signer *cryptokit.Signer) error {
	fleetID := "homelab"

	if _, err := st.CreateFleet(ctx, model.Fleet{ID: fleetID, Name: "Homelab"}); err != nil && !errors.Is(err, store.ErrConflict) {
		return fmt.Errorf("create fleet: %w", err)
	}

	type seedAgent struct {
		ID, Name    string
		TokenPrefix string
		TokenSecret string
	}
	// Tokens here are deterministic so the Compose demo can ship the
	// matching plaintext under deploy/docker-compose/secrets/.
	agentsToSeed := []seedAgent{
		{"traefik-1", "Traefik node 1", "a1a1a1a1", "00000000000000000000000000000001"},
		{"traefik-2", "Traefik node 2", "a2a2a2a2", "00000000000000000000000000000002"},
	}
	for _, a := range agentsToSeed {
		if _, err := st.CreateAgent(ctx, model.Agent{ID: a.ID, FleetID: fleetID, Name: a.Name}); err != nil && !errors.Is(err, store.ErrConflict) {
			return fmt.Errorf("create agent %s: %w", a.ID, err)
		}
		_, hash, err := auth.FixedToken(a.TokenPrefix, a.TokenSecret)
		if err != nil {
			return fmt.Errorf("compose token for %s: %w", a.ID, err)
		}
		if _, err := st.MintToken(ctx, a.ID, "demo", a.TokenPrefix, hash); err != nil && !errors.Is(err, store.ErrConflict) {
			return fmt.Errorf("mint token for %s: %w", a.ID, err)
		}
	}

	// Ensure a whoami proxy host exists.
	hosts, err := st.ListProxyHosts(ctx, fleetID)
	if err != nil {
		return fmt.Errorf("list proxy hosts: %w", err)
	}
	hasWhoami, hasSecure, hasTCP := false, false, false
	for _, h := range hosts {
		if h.Name == "whoami" {
			hasWhoami = true
		}
		if h.Name == "secure-whoami" {
			hasSecure = true
		}
		if h.Name == "tcp-whoami" {
			hasTCP = true
		}
	}
	if !hasWhoami {
		if _, err := st.CreateProxyHost(ctx, model.ProxyHost{
			FleetID: fleetID, Name: "whoami",
			Domain: "whoami.localhost", UpstreamURL: "http://whoami:80",
			EntryPoints: []string{"web"}, Enabled: true,
			Middlewares: []model.Middleware{},
		}); err != nil && !errors.Is(err, store.ErrConflict) {
			return fmt.Errorf("create whoami: %w", err)
		}
	}
	if !hasSecure {
		if _, err := st.CreateProxyHost(ctx, model.ProxyHost{
			FleetID: fleetID, Name: "secure-whoami",
			Domain: "secure.localhost", UpstreamURL: "http://whoami:80",
			EntryPoints: []string{"websecure"}, TLS: true, Enabled: true,
			Middlewares: []model.Middleware{},
		}); err != nil && !errors.Is(err, store.ErrConflict) {
			return fmt.Errorf("create secure-whoami: %w", err)
		}
	}
	if !hasTCP {
		// Phase 6 demo: a raw TCP route. whoami answers HTTP, but the
		// path is identical to a real TCP backend — operators see how
		// HostSNI(`*`) catch-all routing reaches the right service.
		if _, err := st.CreateProxyHost(ctx, model.ProxyHost{
			FleetID: fleetID, Name: "tcp-whoami",
			Protocol:    "tcp",
			Domain:      "*",
			UpstreamURL: "whoami:80",
			EntryPoints: []string{"tcpentry"},
			Enabled:     true,
			Middlewares: []model.Middleware{},
		}); err != nil && !errors.Is(err, store.ErrConflict) {
			return fmt.Errorf("create tcp-whoami: %w", err)
		}
	}

	// Ensure a self-signed demo cert exists. The cert is regenerated only
	// if it isn't already in the database — the demo wants reproducibility,
	// not key freshness.
	existingCerts, err := st.ListCertificates(ctx, fleetID)
	if err != nil {
		return fmt.Errorf("list certs: %w", err)
	}
	hasDemoCert := false
	for _, c := range existingCerts {
		if c.Name == "demo-localhost" {
			hasDemoCert = true
			break
		}
	}
	if !hasDemoCert {
		certPEM, keyPEM, err := generateDemoCert([]string{"whoami.localhost", "secure.localhost", "*.localhost"})
		if err != nil {
			return fmt.Errorf("generate demo cert: %w", err)
		}
		parsed, err := certpkg.Parse(certPEM, keyPEM)
		if err != nil {
			return fmt.Errorf("parse demo cert: %w", err)
		}
		if _, err := st.CreateCertificate(ctx, model.Certificate{
			FleetID: fleetID, Name: "demo-localhost",
			CertPEM:     parsed.CertPEM,
			KeyPEM:      parsed.KeyPEM,
			Fingerprint: parsed.Fingerprint,
			Subject:     parsed.Subject,
			Issuer:      parsed.Issuer,
			DNSNames:    parsed.DNSNames,
			NotBefore:   parsed.NotBefore,
			NotAfter:    parsed.NotAfter,
		}); err != nil && !errors.Is(err, store.ErrConflict) {
			return fmt.Errorf("create demo cert: %w", err)
		}
		logger.Info("seeded demo TLS cert", "fleet", fleetID, "dns_names", parsed.DNSNames)
	}

	// Publish a revision iff none exists yet.
	if _, err := st.GetPublishedRevision(ctx, fleetID); errors.Is(err, store.ErrNotFound) {
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
			return fmt.Errorf("compile seed: %w", err)
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
			FleetID:          fleetID,
			Number:           num,
			CompiledConfig:   res.Config,
			SourceProxyHosts: source,
			SourceCerts:      sourceCerts,
			ETag:             res.ETag,
			Notes:            "demo seed",
		}
		if signer != nil {
			rev.Signature = signer.Sign(rev.CompiledConfig)
			rev.SignatureAlg = cryptokit.SignatureAlg
		}
		if _, err := st.CreateRevision(ctx, rev, true); err != nil {
			return fmt.Errorf("create revision: %w", err)
		}
		logger.Info("seeded initial revision", "fleet", fleetID, "number", num)
	} else if err != nil {
		return err
	}

	// Optional ACME demo: register an account against Pebble + a
	// pebble-challtestsrv DNS provider + issue a cert for acme.localhost.
	if acmeDirURL != "" && challtestsrvURL != "" {
		if err := seedACMEDemo(ctx, st, logger, fleetID, acmeDirURL, challtestsrvURL, insecureACME, signer); err != nil {
			return fmt.Errorf("seed acme demo: %w", err)
		}
	}

	return nil
}

// seedACMEDemo is split out because it's quite a bit longer than the
// rest of seedDemo and only runs in the Pebble demo path.
func seedACMEDemo(ctx context.Context, st store.Store, logger *slog.Logger, fleetID, acmeDirURL, challtestsrvURL string, insecureACME bool, signer *cryptokit.Signer) error {
	httpClient := acmeHTTPClient(insecureACME)

	// 1. Register / load the ACME account.
	account, err := st.GetACMEAccount(ctx, fleetID)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return err
	}
	var acctSigner crypto.Signer
	if errors.Is(err, store.ErrNotFound) {
		keyPEM, sgnr, kerr := acmepkg.GenerateAccountKey()
		if kerr != nil {
			return fmt.Errorf("generate account key: %w", kerr)
		}
		issuer := &acmepkg.Issuer{
			DirectoryURL: acmeDirURL,
			ContactEmail: "demo@example.com",
			AccountKey:   sgnr,
			HTTPClient:   httpClient,
		}
		regCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		accountURL, rerr := issuer.EnsureRegistered(regCtx)
		if rerr != nil {
			return fmt.Errorf("register acme account: %w", rerr)
		}
		account = model.ACMEAccount{
			FleetID: fleetID, DirectoryURL: acmeDirURL,
			ContactEmail: "demo@example.com", AccountKeyPEM: keyPEM, AccountURL: accountURL,
		}
		if err := st.UpsertACMEAccount(ctx, account); err != nil {
			return err
		}
		acctSigner = sgnr
		logger.Info("seeded acme account", "fleet", fleetID, "directory", acmeDirURL)
	} else {
		acctSigner, err = acmepkg.ParseAccountKey(account.AccountKeyPEM)
		if err != nil {
			return fmt.Errorf("parse stored account key: %w", err)
		}
	}

	// 2. Ensure the pebble DNS provider is configured.
	if _, err := st.GetDNSProviderByName(ctx, fleetID, "pebble"); errors.Is(err, store.ErrNotFound) {
		cfg, _ := json.Marshal(map[string]string{"base_url": challtestsrvURL})
		if _, err := st.CreateDNSProvider(ctx, model.DNSProvider{
			FleetID: fleetID, Name: "pebble", Type: "pebble", Config: cfg,
		}); err != nil && !errors.Is(err, store.ErrConflict) {
			return fmt.Errorf("create dns provider: %w", err)
		}
		logger.Info("seeded dns provider", "fleet", fleetID, "name", "pebble", "url", challtestsrvURL)
	} else if err != nil {
		return err
	}

	// 3. Issue an ACME-backed cert (and a matching proxy host) iff one
	// doesn't already exist. Subsequent boots reuse what's there.
	existing, err := st.ListCertificates(ctx, fleetID)
	if err != nil {
		return err
	}
	hasACMECert := false
	for _, c := range existing {
		if c.Name == "acme-localhost" {
			hasACMECert = true
			break
		}
	}
	if !hasACMECert {
		provider, err := st.GetDNSProviderByName(ctx, fleetID, "pebble")
		if err != nil {
			return err
		}
		dnsImpl, err := dnspkg.Build(provider.Type, json.RawMessage(provider.Config))
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
		issueCtx, cancel := context.WithTimeout(ctx, 120*time.Second)
		defer cancel()
		certPEM, keyPEM, err := issuer.Issue(issueCtx, []string{"acme.localhost"}, dnsImpl)
		if err != nil {
			return fmt.Errorf("issue acme cert: %w", err)
		}
		parsed, err := certpkg.Parse(certPEM, keyPEM)
		if err != nil {
			return fmt.Errorf("parse issued cert: %w", err)
		}
		if _, err := st.CreateCertificate(ctx, model.Certificate{
			FleetID: fleetID, Name: "acme-localhost",
			CertPEM:     parsed.CertPEM,
			KeyPEM:      parsed.KeyPEM,
			Fingerprint: parsed.Fingerprint,
			Subject:     parsed.Subject,
			Issuer:      parsed.Issuer,
			DNSNames:    parsed.DNSNames,
			NotBefore:   parsed.NotBefore,
			NotAfter:    parsed.NotAfter,
			Source:      "acme",
		}); err != nil && !errors.Is(err, store.ErrConflict) {
			return fmt.Errorf("save acme cert: %w", err)
		}
		logger.Info("seeded acme cert", "fleet", fleetID, "fingerprint", parsed.Fingerprint, "not_after", parsed.NotAfter)

		// Also drop a proxy host that uses TLS so the demo curl works.
		hosts, err := st.ListProxyHosts(ctx, fleetID)
		if err != nil {
			return err
		}
		hasACMEHost := false
		for _, h := range hosts {
			if h.Name == "acme-whoami" {
				hasACMEHost = true
				break
			}
		}
		if !hasACMEHost {
			if _, err := st.CreateProxyHost(ctx, model.ProxyHost{
				FleetID: fleetID, Name: "acme-whoami",
				Domain: "acme.localhost", UpstreamURL: "http://whoami:80",
				EntryPoints: []string{"websecure"}, TLS: true, Enabled: true,
				Middlewares: []model.Middleware{},
			}); err != nil && !errors.Is(err, store.ErrConflict) {
				return fmt.Errorf("create acme proxy host: %w", err)
			}
		}
		// Re-publish so the new cert + host land in a revision.
		hosts, err = st.ListProxyHosts(ctx, fleetID)
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
			ETag: res.ETag, Notes: "acme demo",
		}
		if signer != nil {
			rev.Signature = signer.Sign(rev.CompiledConfig)
			rev.SignatureAlg = cryptokit.SignatureAlg
		}
		if _, err := st.CreateRevision(ctx, rev, true); err != nil {
			return err
		}
		logger.Info("published revision with acme cert", "fleet", fleetID, "number", num)
	}
	return nil
}

// acmeHTTPClient is the http.Client the manager uses for ACME calls.
//
// In the Pebble demo (insecureACME=true) we accept the self-signed CA
// pebble presents on its directory + ACME endpoints. Real deployments
// should leave this off so ACME provider TLS is verified normally.
func acmeHTTPClient(insecureACME bool) *http.Client {
	if !insecureACME {
		return nil
	}
	return &http.Client{
		Timeout: 60 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		},
	}
}

// generateDemoCert produces a 1-year self-signed ECDSA certificate for
// the given DNS names. Used only by the demo seed.
func generateDemoCert(dnsNames []string) (certPEM, keyPEM string, err error) {
	priv, err := ecdsa.GenerateKey(elliptic.P256(), cryptorand.Reader)
	if err != nil {
		return "", "", err
	}
	serial, err := cryptorand.Int(cryptorand.Reader, big.NewInt(1<<62))
	if err != nil {
		return "", "", err
	}
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: dnsNames[0], Organization: []string{"traefik-fleet-manager demo"}},
		NotBefore:    time.Now().Add(-time.Minute),
		NotAfter:     time.Now().Add(365 * 24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     dnsNames,
	}
	der, err := x509.CreateCertificate(cryptorand.Reader, tmpl, tmpl, &priv.PublicKey, priv)
	if err != nil {
		return "", "", err
	}
	keyDER, err := x509.MarshalECPrivateKey(priv)
	if err != nil {
		return "", "", err
	}
	certPEM = string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
	keyPEM = string(pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}))
	return certPEM, keyPEM, nil
}
