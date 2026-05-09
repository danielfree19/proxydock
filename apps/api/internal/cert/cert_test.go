package cert

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"strings"
	"testing"
	"time"
)

// generateSelfSigned builds a cert/key pair for the given DNS names so
// the tests don't depend on any committed PEM fixtures.
func generateSelfSigned(t *testing.T, dnsNames ...string) (string, string) {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("gen key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: dnsNames[0], Organization: []string{"tfm-test"}},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     dnsNames,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &priv.PublicKey, priv)
	if err != nil {
		t.Fatalf("CreateCertificate: %v", err)
	}
	keyDER, err := x509.MarshalECPrivateKey(priv)
	if err != nil {
		t.Fatalf("MarshalECPrivateKey: %v", err)
	}
	certPEM := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
	keyPEM := string(pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}))
	return certPEM, keyPEM
}

func TestParse_OK(t *testing.T) {
	certPEM, keyPEM := generateSelfSigned(t, "whoami.localhost", "secure.localhost")
	p, err := Parse(certPEM, keyPEM)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if !strings.HasPrefix(p.Fingerprint, "sha256:") {
		t.Fatalf("Fingerprint = %q", p.Fingerprint)
	}
	if len(p.DNSNames) != 2 || p.DNSNames[0] != "whoami.localhost" {
		t.Fatalf("DNSNames = %v", p.DNSNames)
	}
	if !p.NotAfter.After(time.Now()) {
		t.Fatalf("NotAfter not in future: %v", p.NotAfter)
	}
}

func TestParse_Mismatch(t *testing.T) {
	certPEM, _ := generateSelfSigned(t, "a.localhost")
	_, otherKey := generateSelfSigned(t, "b.localhost")
	_, err := Parse(certPEM, otherKey)
	if err == nil || !strings.Contains(err.Error(), "do not match") {
		t.Fatalf("expected mismatch, got %v", err)
	}
}

func TestParse_EmptyKey(t *testing.T) {
	certPEM, _ := generateSelfSigned(t, "a.localhost")
	_, err := Parse(certPEM, "")
	if err == nil {
		t.Fatal("expected empty-key error")
	}
}

func TestParse_GarbageCert(t *testing.T) {
	_, keyPEM := generateSelfSigned(t, "a.localhost")
	_, err := Parse("not a pem", keyPEM)
	if err == nil {
		t.Fatal("expected garbage-cert error")
	}
}
