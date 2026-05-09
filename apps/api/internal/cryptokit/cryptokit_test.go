package cryptokit

import (
	"bytes"
	"strings"
	"testing"
)

func newTestCipher(t *testing.T) *Cipher {
	t.Helper()
	c, err := NewCipherHex("0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestCipher_Roundtrip(t *testing.T) {
	c := newTestCipher(t)
	in := "private key bytes go here\nover multiple lines\n"
	enc, err := c.Encrypt(in)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(enc, EncryptedPrefix) {
		t.Fatalf("missing prefix: %q", enc)
	}
	out, err := c.Decrypt(enc)
	if err != nil {
		t.Fatal(err)
	}
	if out != in {
		t.Fatalf("got %q want %q", out, in)
	}
}

func TestCipher_Plaintext_Passthrough(t *testing.T) {
	c := newTestCipher(t)
	plain := "not yet encrypted"
	out, err := c.Decrypt(plain)
	if err != nil {
		t.Fatal(err)
	}
	if out != plain {
		t.Fatalf("plaintext mangled: %q", out)
	}
}

func TestCipher_NilDecrypt_RejectsCiphertext(t *testing.T) {
	c := newTestCipher(t)
	enc, err := c.Encrypt("secret")
	if err != nil {
		t.Fatal(err)
	}
	var nilCipher *Cipher
	if _, err := nilCipher.Decrypt(enc); err == nil {
		t.Fatal("nil cipher should refuse to decrypt ciphertext")
	}
}

func TestCipher_NilEncrypt_PassesThrough(t *testing.T) {
	var nilCipher *Cipher
	out, err := nilCipher.Encrypt("hello")
	if err != nil {
		t.Fatal(err)
	}
	if out != "hello" {
		t.Fatalf("got %q", out)
	}
}

func TestCipher_BadKeyLength(t *testing.T) {
	if _, err := NewCipher(make([]byte, 16)); err == nil {
		t.Fatal("expected error for 16-byte key")
	}
}

func TestCipher_TamperedCiphertextRejected(t *testing.T) {
	c := newTestCipher(t)
	enc, err := c.Encrypt("secret")
	if err != nil {
		t.Fatal(err)
	}
	// Flip a byte deep in the ciphertext payload.
	tampered := []byte(enc)
	tampered[len(tampered)-3] ^= 0x01
	if _, err := c.Decrypt(string(tampered)); err == nil {
		t.Fatal("expected GCM tag mismatch")
	}
}

func TestSigner_Roundtrip(t *testing.T) {
	s, err := GenerateSigner()
	if err != nil {
		t.Fatal(err)
	}
	payload := []byte(`{"http":{"routers":{}}}`)
	sig := s.Sign(payload)
	if sig == "" {
		t.Fatal("empty signature")
	}
	if err := Verify(s.PublicKey(), sig, payload); err != nil {
		t.Fatal(err)
	}
}

func TestSigner_Tampered(t *testing.T) {
	s, _ := GenerateSigner()
	payload := []byte("hello")
	sig := s.Sign(payload)
	if err := Verify(s.PublicKey(), sig, []byte("hello!")); err == nil {
		t.Fatal("expected signature mismatch on tampered payload")
	}
}

func TestSigner_DerivedFromSeed(t *testing.T) {
	seed := bytes.Repeat([]byte{0x42}, 32)
	a, err := NewSigner(seed)
	if err != nil {
		t.Fatal(err)
	}
	b, err := NewSigner(seed)
	if err != nil {
		t.Fatal(err)
	}
	// Same seed → same public key.
	if a.PublicKey() != b.PublicKey() {
		t.Fatal("seeds should be deterministic")
	}
}
