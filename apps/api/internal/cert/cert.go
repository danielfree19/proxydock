// Package cert parses and validates uploaded PEM certificates and keys
// before the manager persists them.
//
// The package extracts the leaf certificate's metadata (subject, issuer,
// DNS SANs, validity window, fingerprint) so the UI can render expiry
// without re-parsing on every request, and refuses obviously bogus
// uploads early (mismatched cert/key, invalid PEM blocks, no certs in
// the chain).
package cert

import (
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Parsed is everything we extract from an uploaded cert + key.
type Parsed struct {
	CertPEM     string
	KeyPEM      string
	Fingerprint string
	Subject     string
	Issuer      string
	DNSNames    []string
	NotBefore   time.Time
	NotAfter    time.Time
}

// Parse validates a PEM certificate chain + key pair and returns the
// extracted leaf metadata.
//
// Validation rules:
//   - certPEM must contain at least one CERTIFICATE block.
//   - The first certificate is treated as the leaf.
//   - keyPEM must contain a single PRIVATE KEY (PKCS#1, PKCS#8, or EC).
//   - cert and key must form a valid TLS pair (tls.X509KeyPair).
func Parse(certPEM, keyPEM string) (*Parsed, error) {
	certPEM = strings.TrimSpace(certPEM)
	keyPEM = strings.TrimSpace(keyPEM)
	if certPEM == "" {
		return nil, errors.New("cert_pem is empty")
	}
	if keyPEM == "" {
		return nil, errors.New("key_pem is empty")
	}

	// Verify the cert/key match before doing any expensive parsing.
	if _, err := tls.X509KeyPair([]byte(certPEM), []byte(keyPEM)); err != nil {
		return nil, fmt.Errorf("cert and key do not match: %w", err)
	}

	leaf, err := firstCertificate(certPEM)
	if err != nil {
		return nil, err
	}

	sum := sha256.Sum256(leaf.Raw)
	return &Parsed{
		CertPEM:     certPEM + "\n",
		KeyPEM:      keyPEM + "\n",
		Fingerprint: "sha256:" + hex.EncodeToString(sum[:]),
		Subject:     leaf.Subject.String(),
		Issuer:      leaf.Issuer.String(),
		DNSNames:    append([]string(nil), leaf.DNSNames...),
		NotBefore:   leaf.NotBefore.UTC(),
		NotAfter:    leaf.NotAfter.UTC(),
	}, nil
}

// firstCertificate returns the first CERTIFICATE block in a chain PEM.
func firstCertificate(certPEM string) (*x509.Certificate, error) {
	rest := []byte(certPEM)
	for {
		block, r := pem.Decode(rest)
		if block == nil {
			return nil, errors.New("no CERTIFICATE block found")
		}
		if block.Type == "CERTIFICATE" {
			return x509.ParseCertificate(block.Bytes)
		}
		rest = r
	}
}
