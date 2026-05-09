package acme

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
)

// GenerateAccountKey produces a fresh P-256 ECDSA key suitable for
// registering an ACME account. PEM-encoded so it can be persisted in a
// TEXT column.
func GenerateAccountKey() (keyPEM string, signer crypto.Signer, err error) {
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return "", nil, err
	}
	der, err := x509.MarshalECPrivateKey(priv)
	if err != nil {
		return "", nil, err
	}
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der})
	return string(pemBytes), priv, nil
}

// ParseAccountKey decodes a PEM private key (EC or PKCS#8). Returns a
// crypto.Signer ready to hand to xacme.Client.
func ParseAccountKey(keyPEM string) (crypto.Signer, error) {
	block, _ := pem.Decode([]byte(keyPEM))
	if block == nil {
		return nil, errors.New("acme: account key PEM is empty")
	}
	switch block.Type {
	case "EC PRIVATE KEY":
		return x509.ParseECPrivateKey(block.Bytes)
	case "PRIVATE KEY":
		k, err := x509.ParsePKCS8PrivateKey(block.Bytes)
		if err != nil {
			return nil, err
		}
		signer, ok := k.(crypto.Signer)
		if !ok {
			return nil, fmt.Errorf("acme: PKCS#8 key of type %T is not a Signer", k)
		}
		return signer, nil
	default:
		return nil, fmt.Errorf("acme: unsupported PEM block %q", block.Type)
	}
}
