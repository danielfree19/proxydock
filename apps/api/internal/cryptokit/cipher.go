// Package cryptokit holds the small set of cryptographic primitives the
// manager uses outside of the ACME / TLS code paths:
//
//   - Cipher: AES-256-GCM encryption of column values, with a versioned,
//     prefix-tagged on-disk format so plaintext rows from before
//     encryption was wired up still decode cleanly.
//   - Signer: Ed25519 signing of compiled-config bytes plus a verifier
//     used by the provider plugin (kept self-contained so the plugin
//     can lift the verifier without depending on this package).
package cryptokit

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

// EncryptedPrefix marks a value as ciphertext so plaintext values from
// before encryption was enabled keep working (they pass through Decrypt
// unchanged). The version (v1) is split from the algorithm name so we
// can ship a v2 format later without breaking existing rows.
const EncryptedPrefix = "enc-aes256gcm-v1:"

// Cipher encrypts and decrypts string values with AES-256-GCM.
//
// The on-disk format is:
//
//	enc-aes256gcm-v1:<base64-nonce>:<base64-ciphertext+tag>
//
// `Decrypt` returns plaintext input unchanged if it does not begin with
// the prefix, so a column can transparently hold a mix of plaintext and
// ciphertext during a migration.
type Cipher struct {
	aead cipher.AEAD
}

// NewCipher constructs a Cipher from a 32-byte key. The key may be
// passed either as raw bytes (len == 32) or as a 64-character hex
// string — the latter makes it easy to keep the key in an env var.
func NewCipher(key []byte) (*Cipher, error) {
	if len(key) != 32 {
		return nil, fmt.Errorf("cryptokit: encryption key must be 32 bytes, got %d", len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Cipher{aead: gcm}, nil
}

// NewCipherHex parses a 64-character hex key and returns a Cipher.
func NewCipherHex(hexKey string) (*Cipher, error) {
	hexKey = strings.TrimSpace(hexKey)
	key, err := hex.DecodeString(hexKey)
	if err != nil {
		return nil, fmt.Errorf("cryptokit: encryption key must be hex: %w", err)
	}
	return NewCipher(key)
}

// Encrypt produces an EncryptedPrefix-tagged string that round-trips
// through Decrypt. A nil Cipher returns plaintext unchanged so the
// memory store and tests can run without a configured key.
func (c *Cipher) Encrypt(plaintext string) (string, error) {
	if c == nil {
		return plaintext, nil
	}
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	ct := c.aead.Seal(nil, nonce, []byte(plaintext), nil)
	return EncryptedPrefix +
		base64.RawStdEncoding.EncodeToString(nonce) + ":" +
		base64.RawStdEncoding.EncodeToString(ct), nil
}

// Decrypt returns the plaintext for an encrypted value, or the value
// itself unchanged if it isn't tagged. A nil Cipher is OK iff the input
// is plaintext; it errors otherwise so we don't silently lose data.
func (c *Cipher) Decrypt(stored string) (string, error) {
	if !strings.HasPrefix(stored, EncryptedPrefix) {
		return stored, nil
	}
	if c == nil {
		return "", errors.New("cryptokit: ciphertext encountered without a configured cipher")
	}
	rest := stored[len(EncryptedPrefix):]
	parts := strings.SplitN(rest, ":", 2)
	if len(parts) != 2 {
		return "", errors.New("cryptokit: malformed ciphertext (missing nonce/data split)")
	}
	nonce, err := base64.RawStdEncoding.DecodeString(parts[0])
	if err != nil {
		return "", fmt.Errorf("cryptokit: bad nonce: %w", err)
	}
	if len(nonce) != c.aead.NonceSize() {
		return "", fmt.Errorf("cryptokit: nonce length %d != %d", len(nonce), c.aead.NonceSize())
	}
	ct, err := base64.RawStdEncoding.DecodeString(parts[1])
	if err != nil {
		return "", fmt.Errorf("cryptokit: bad ciphertext: %w", err)
	}
	pt, err := c.aead.Open(nil, nonce, ct, nil)
	if err != nil {
		return "", fmt.Errorf("cryptokit: open: %w", err)
	}
	return string(pt), nil
}

// EncryptBytes / DecryptBytes are byte-string variants used for jsonb
// columns whose underlying representation is bytes, not text.
func (c *Cipher) EncryptBytes(plaintext []byte) ([]byte, error) {
	out, err := c.Encrypt(string(plaintext))
	return []byte(out), err
}

func (c *Cipher) DecryptBytes(stored []byte) ([]byte, error) {
	out, err := c.Decrypt(string(stored))
	return []byte(out), err
}
