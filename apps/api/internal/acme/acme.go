// Package acme drives the DNS-01 ACME flow on behalf of the manager.
//
// The package is intentionally small: account key management,
// registration, and order issuance/renewal. The DNS provider that
// publishes _acme-challenge TXT records is plugged in via the
// internal/acme/dns package.
//
// The ACME library used is golang.org/x/crypto/acme, which is the
// stdlib-adjacent maintained client. We do *not* use autocert; this is
// not a TLS server, the issued cert lives in the manager's database
// and is shipped to agents via the existing revision payload.
package acme

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	xacme "golang.org/x/crypto/acme"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

// Issuer holds an ACME account + DNS provider and can issue or renew
// certs. It is safe to reuse across orders.
type Issuer struct {
	DirectoryURL string
	ContactEmail string
	AccountKey   crypto.Signer
	AccountURL   string

	HTTPClient *http.Client // optional, for talking to a self-signed CA (Pebble)
}

// EnsureRegistered registers the account with the ACME server if no
// AccountURL is recorded yet. Returns the (possibly newly assigned)
// account URL so callers can persist it.
func (i *Issuer) EnsureRegistered(ctx context.Context) (string, error) {
	c := i.client()
	if i.AccountURL != "" {
		return i.AccountURL, nil
	}
	acc := &xacme.Account{Contact: []string{"mailto:" + i.ContactEmail}}
	got, err := c.Register(ctx, acc, xacme.AcceptTOS)
	if err != nil {
		// If the key is already registered with the CA, GetReg returns
		// the existing account.
		if existing, gerr := c.GetReg(ctx, ""); gerr == nil {
			return existing.URI, nil
		}
		return "", fmt.Errorf("acme register: %w", err)
	}
	return got.URI, nil
}

// Issue runs a DNS-01 order for the supplied domains and returns the
// PEM-encoded certificate chain plus a freshly generated private key.
//
// The issuer cleans up its TXT records before returning, even on error.
func (i *Issuer) Issue(ctx context.Context, domains []string, dnsProvider DNSPresenter) (certPEM, keyPEM string, err error) {
	if len(domains) == 0 {
		return "", "", errors.New("acme: at least one domain is required")
	}

	tr := otel.Tracer("internal/acme")
	ctx, rootSpan := tr.Start(ctx, "acme.issue",
		trace.WithAttributes(attribute.StringSlice("acme.dns_names", domains)))
	defer func() {
		if err != nil {
			rootSpan.RecordError(err)
			rootSpan.SetStatus(codes.Error, err.Error())
		}
		rootSpan.End()
	}()

	c := i.client()

	authzCtx, authzSpan := tr.Start(ctx, "acme.authorize_order")
	order, err := c.AuthorizeOrder(authzCtx, xacme.DomainIDs(domains...))
	authzSpan.End()
	if err != nil {
		return "", "", fmt.Errorf("authorize order: %w", err)
	}
	rootSpan.SetAttributes(attribute.String("acme.order_uri", order.URI))

	// Track the records we've created so we can clean up on the way out.
	var planted []plantedRecord
	defer func() {
		// Cleanup uses a fresh, short context so we still tidy up on
		// timeout/cancellation paths.
		cleanCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		for _, p := range planted {
			if cerr := dnsProvider.CleanUp(cleanCtx, p.fqdn, p.value); cerr != nil {
				// Surface only as wrapped error if Issue itself succeeded;
				// otherwise the original err takes precedence.
				if err == nil {
					err = fmt.Errorf("dns cleanup: %w", cerr)
				}
			}
		}
	}()

	for _, authzURL := range order.AuthzURLs {
		// One sub-span per authz so the trace tree shows per-domain
		// timing — useful when a single host stalls validation.
		authzCtx, authzSpan := tr.Start(ctx, "acme.authorization")
		err = i.runAuthorization(authzCtx, c, authzURL, dnsProvider, &planted)
		if err != nil {
			authzSpan.RecordError(err)
			authzSpan.SetStatus(codes.Error, err.Error())
			authzSpan.End()
			return "", "", err
		}
		authzSpan.End()
	}

	// Re-fetch the order so we read the canonical FinalizeURL.
	//
	// We deliberately keep the URI we got from AuthorizeOrder — RFC 8555
	// only mandates a Location header on the order-creation response,
	// not on subsequent GETs. x/crypto/acme.responseOrder unconditionally
	// reads URI from the Location header, so a refreshed order from
	// CAs like Pebble has an empty URI. Preserving the original URI
	// keeps WaitOrder/FetchCert callable below.
	originalURI := order.URI
	order, err = c.GetOrder(ctx, order.URI)
	if err != nil {
		return "", "", fmt.Errorf("get order: %w", err)
	}
	if order.URI == "" {
		order.URI = originalURI
	}
	if order.FinalizeURL == "" {
		return "", "", fmt.Errorf("order has empty FinalizeURL (status=%s)", order.Status)
	}

	certKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return "", "", fmt.Errorf("generate cert key: %w", err)
	}
	csr, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{
		Subject:  pkix.Name{CommonName: domains[0]},
		DNSNames: domains,
	}, certKey)
	if err != nil {
		return "", "", fmt.Errorf("build csr: %w", err)
	}

	finalizeCtx, finalizeSpan := tr.Start(ctx, "acme.finalize")
	der, _, err := c.CreateOrderCert(finalizeCtx, order.FinalizeURL, csr, true)
	finalizeSpan.End()
	if err != nil {
		// Some CAs (notably Pebble) return a finalize response without
		// a Location header, leaving x/crypto/acme's internal Order URI
		// empty and causing WaitOrder to fail with a Post "" error.
		// Fall back to polling the order ourselves and fetching the cert
		// when it's available.
		if strings.Contains(err.Error(), `Post ""`) || strings.Contains(err.Error(), `unsupported protocol scheme ""`) {
			slog.Warn("acme: CreateOrderCert hit empty-URL bug; falling back to manual poll", "err", err)
			derFallback, errFB := pollAndFetch(ctx, c, order.URI)
			if errFB != nil {
				return "", "", fmt.Errorf("create order cert (fallback poll): %w", errFB)
			}
			der = derFallback
		} else {
			return "", "", fmt.Errorf("create order cert: %w", err)
		}
	}

	var chain strings.Builder
	for _, b := range der {
		_ = pem.Encode(&chain, &pem.Block{Type: "CERTIFICATE", Bytes: b})
	}
	keyDER, err := x509.MarshalECPrivateKey(certKey)
	if err != nil {
		return "", "", fmt.Errorf("marshal cert key: %w", err)
	}
	keyPEMBytes := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})

	return chain.String(), string(keyPEMBytes), nil
}

// plantedRecord is one TXT record we've installed and need to clean up.
type plantedRecord struct{ fqdn, value string }

// runAuthorization handles one authz URL: fetch, find DNS-01 challenge,
// publish the TXT record, accept, and wait for validation. The TXT
// record is registered with the planted slice so the parent's deferred
// cleanup removes it on the way out, success or failure.
func (i *Issuer) runAuthorization(
	ctx context.Context,
	c *xacme.Client,
	authzURL string,
	dnsProvider DNSPresenter,
	planted *[]plantedRecord,
) error {
	tr := otel.Tracer("internal/acme")

	authz, err := c.GetAuthorization(ctx, authzURL)
	if err != nil {
		return fmt.Errorf("get auth: %w", err)
	}
	if authz.Status == xacme.StatusValid {
		return nil
	}

	var chal *xacme.Challenge
	for _, ch := range authz.Challenges {
		if ch.Type == "dns-01" {
			chal = ch
			break
		}
	}
	if chal == nil {
		return fmt.Errorf("no dns-01 challenge for %q", authz.Identifier.Value)
	}

	val, err := c.DNS01ChallengeRecord(chal.Token)
	if err != nil {
		return fmt.Errorf("dns01 record: %w", err)
	}
	fqdn := "_acme-challenge." + authz.Identifier.Value + "."

	presentCtx, presentSpan := tr.Start(ctx, "acme.dns_present",
		trace.WithAttributes(attribute.String("acme.fqdn", fqdn)))
	if err := dnsProvider.Present(presentCtx, fqdn, val); err != nil {
		presentSpan.RecordError(err)
		presentSpan.SetStatus(codes.Error, err.Error())
		presentSpan.End()
		return fmt.Errorf("dns present: %w", err)
	}
	presentSpan.End()
	*planted = append(*planted, plantedRecord{fqdn: fqdn, value: val})

	if _, err := c.Accept(ctx, chal); err != nil {
		return fmt.Errorf("accept challenge: %w", err)
	}
	waitCtx, waitSpan := tr.Start(ctx, "acme.wait_authorization")
	_, err = c.WaitAuthorization(waitCtx, authzURL)
	waitSpan.End()
	if err != nil {
		return fmt.Errorf("wait authorization: %w", err)
	}
	return nil
}

// pollAndFetch is a fallback used when x/crypto/acme.CreateOrderCert
// errors after successfully POSTing the CSR but before fetching the
// issued cert. The CA has already begun processing the order; we just
// need to wait for it to finish and download the bytes.
//
// This is required because some ACME CAs (Pebble in particular) do not
// set a Location header on the finalize response, leaving the parsed
// order's URI empty inside x/crypto/acme — the library's WaitOrder
// then runs a POST against an empty URL.
func pollAndFetch(ctx context.Context, c *xacme.Client, orderURI string) ([][]byte, error) {
	if orderURI == "" {
		return nil, errors.New("pollAndFetch: empty orderURI")
	}
	o, err := c.WaitOrder(ctx, orderURI)
	if err != nil {
		return nil, fmt.Errorf("WaitOrder(%s): %w", orderURI, err)
	}
	if o.CertURL == "" {
		// WaitOrder accepts both StatusReady and StatusValid; if we got
		// Ready the cert isn't ready yet, poll a bit more via GetOrder.
		deadline := time.Now().Add(60 * time.Second)
		for {
			if time.Now().After(deadline) {
				return nil, fmt.Errorf("order %s: cert URL still empty after wait (status=%s)", orderURI, o.Status)
			}
			time.Sleep(1 * time.Second)
			o, err = c.GetOrder(ctx, orderURI)
			if err != nil {
				return nil, fmt.Errorf("GetOrder(%s): %w", orderURI, err)
			}
			if o.CertURL != "" {
				break
			}
		}
	}
	return c.FetchCert(ctx, o.CertURL, true)
}

func (i *Issuer) client() *xacme.Client {
	c := &xacme.Client{
		Key:          i.AccountKey,
		DirectoryURL: i.DirectoryURL,
	}
	if i.HTTPClient != nil {
		c.HTTPClient = i.HTTPClient
	}
	return c
}

// DNSPresenter is the subset of dns.Provider that the issuer needs.
// It's redeclared here to avoid an import cycle (the dns package is in
// a subdirectory, but defining the interface twice keeps the issuer
// itself dependency-free).
type DNSPresenter interface {
	Present(ctx context.Context, fqdn, value string) error
	CleanUp(ctx context.Context, fqdn, value string) error
}
