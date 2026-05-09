// Package auth handles agent bearer-token format, generation, and
// verification.
//
// Token wire format: "tfm_<prefix>_<secret>"
//   - prefix: 8 hex chars (4 random bytes), non-secret, indexable lookup key
//   - secret: 32 hex chars (16 random bytes), the entropy that authorizes
//
// We store SHA-256(secret) in the DB so a database leak does not give
// attackers usable bearer tokens. Verification uses subtle.ConstantTimeCompare.
package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

// TokenPrefixLen is the length, in hex chars, of the lookup prefix.
const TokenPrefixLen = 8

// TokenSecretLen is the length, in hex chars, of the secret half.
const TokenSecretLen = 32

const tokenScheme = "tfm_"

// MintToken creates a fresh bearer token. It returns the plaintext token
// (caller must show it once and never store it), the prefix to persist,
// and the SHA-256 hash of the secret.
func MintToken() (token, prefix string, secretHash []byte, err error) {
	prefixBytes := make([]byte, TokenPrefixLen/2)
	secretBytes := make([]byte, TokenSecretLen/2)
	if _, err := rand.Read(prefixBytes); err != nil {
		return "", "", nil, err
	}
	if _, err := rand.Read(secretBytes); err != nil {
		return "", "", nil, err
	}
	prefix = hex.EncodeToString(prefixBytes)
	secret := hex.EncodeToString(secretBytes)
	token = tokenScheme + prefix + "_" + secret
	sum := sha256.Sum256([]byte(secret))
	return token, prefix, sum[:], nil
}

// MintTokenWithSecret is for tests and seeding: it uses a caller-supplied
// secret instead of randomly generating one. The prefix is still random.
//
// secret must be exactly TokenSecretLen hex characters.
func MintTokenWithSecret(secret string) (token, prefix string, secretHash []byte, err error) {
	if len(secret) != TokenSecretLen {
		return "", "", nil, fmt.Errorf("auth: secret must be %d hex chars", TokenSecretLen)
	}
	if _, err := hex.DecodeString(secret); err != nil {
		return "", "", nil, fmt.Errorf("auth: secret must be hex: %w", err)
	}
	prefixBytes := make([]byte, TokenPrefixLen/2)
	if _, err := rand.Read(prefixBytes); err != nil {
		return "", "", nil, err
	}
	prefix = hex.EncodeToString(prefixBytes)
	token = tokenScheme + prefix + "_" + secret
	sum := sha256.Sum256([]byte(secret))
	return token, prefix, sum[:], nil
}

// FixedToken is for deterministic seeding (e.g. the demo). It composes a
// token from a caller-chosen prefix and secret. Both must be valid hex of
// the standard lengths.
func FixedToken(prefix, secret string) (token string, secretHash []byte, err error) {
	if len(prefix) != TokenPrefixLen {
		return "", nil, fmt.Errorf("auth: prefix must be %d hex chars", TokenPrefixLen)
	}
	if len(secret) != TokenSecretLen {
		return "", nil, fmt.Errorf("auth: secret must be %d hex chars", TokenSecretLen)
	}
	if _, err := hex.DecodeString(prefix); err != nil {
		return "", nil, fmt.Errorf("auth: prefix must be hex: %w", err)
	}
	if _, err := hex.DecodeString(secret); err != nil {
		return "", nil, fmt.Errorf("auth: secret must be hex: %w", err)
	}
	sum := sha256.Sum256([]byte(secret))
	return tokenScheme + prefix + "_" + secret, sum[:], nil
}

// ParseBearer extracts (prefix, secret) from a wire-format token.
//
// The function does not look anything up; it only checks the format.
func ParseBearer(token string) (prefix, secret string, err error) {
	if !strings.HasPrefix(token, tokenScheme) {
		return "", "", errors.New("auth: bad token scheme")
	}
	rest := token[len(tokenScheme):]
	parts := strings.SplitN(rest, "_", 2)
	if len(parts) != 2 {
		return "", "", errors.New("auth: malformed token")
	}
	prefix, secret = parts[0], parts[1]
	if len(prefix) != TokenPrefixLen || len(secret) != TokenSecretLen {
		return "", "", errors.New("auth: bad token length")
	}
	if _, err := hex.DecodeString(prefix); err != nil {
		return "", "", errors.New("auth: prefix not hex")
	}
	if _, err := hex.DecodeString(secret); err != nil {
		return "", "", errors.New("auth: secret not hex")
	}
	return prefix, secret, nil
}

// VerifySecret returns true if the presented secret hashes to the stored value.
func VerifySecret(secret string, expectedHash []byte) bool {
	sum := sha256.Sum256([]byte(secret))
	return subtle.ConstantTimeCompare(sum[:], expectedHash) == 1
}
