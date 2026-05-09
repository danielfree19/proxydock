package cryptokit

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
)

// SignatureAlg is the algorithm name embedded in the wire format.
const SignatureAlg = "ed25519"

// Signer holds an ed25519 keypair used to sign compiled config bytes.
//
// Public keys are exposed via API so operators can copy them into the
// provider plugin's static config; the plugin's verifier (the
// VerifyFunc returned by NewVerifier) is intentionally tiny so it can
// be lifted into the Yaegi-loaded plugin code without dragging in this
// package's other dependencies.
type Signer struct {
	priv ed25519.PrivateKey
}

// NewSigner returns a Signer derived from a 32-byte seed. Pass the seed
// either raw or hex-encoded; the env var the manager reads is hex.
func NewSigner(seed []byte) (*Signer, error) {
	if len(seed) != ed25519.SeedSize {
		return nil, fmt.Errorf("cryptokit: signing seed must be %d bytes, got %d",
			ed25519.SeedSize, len(seed))
	}
	priv := ed25519.NewKeyFromSeed(seed)
	return &Signer{priv: priv}, nil
}

// GenerateSigner produces a fresh keypair (used by the demo seed when
// no MANAGER_API_SIGNING_KEY is configured).
func GenerateSigner() (*Signer, error) {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	return &Signer{priv: priv}, nil
}

// Sign returns a base64-encoded signature for the given bytes.
func (s *Signer) Sign(payload []byte) string {
	if s == nil {
		return ""
	}
	sig := ed25519.Sign(s.priv, payload)
	return base64.StdEncoding.EncodeToString(sig)
}

// PublicKey returns the verifier-side public key as base64 (the format
// the provider plugin's static config expects).
func (s *Signer) PublicKey() string {
	pub := s.priv.Public().(ed25519.PublicKey)
	return base64.StdEncoding.EncodeToString(pub)
}

// Verify returns true if signatureBase64 is a valid ed25519 signature
// over payload using publicKeyBase64.
//
// This is the verifier the provider plugin uses; we keep it here as a
// reference implementation and re-implement the same logic inline in
// the plugin to avoid Yaegi having to load this whole package.
func Verify(publicKeyBase64, signatureBase64 string, payload []byte) error {
	if publicKeyBase64 == "" {
		return errors.New("cryptokit: empty public key")
	}
	pub, err := base64.StdEncoding.DecodeString(publicKeyBase64)
	if err != nil {
		return fmt.Errorf("cryptokit: bad public key: %w", err)
	}
	if len(pub) != ed25519.PublicKeySize {
		return fmt.Errorf("cryptokit: public key length %d != %d", len(pub), ed25519.PublicKeySize)
	}
	sig, err := base64.StdEncoding.DecodeString(signatureBase64)
	if err != nil {
		return fmt.Errorf("cryptokit: bad signature: %w", err)
	}
	if !ed25519.Verify(pub, payload, sig) {
		return errors.New("cryptokit: signature does not verify")
	}
	return nil
}
