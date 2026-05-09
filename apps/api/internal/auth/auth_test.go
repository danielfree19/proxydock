package auth

import (
	"strings"
	"testing"
)

func TestMintAndVerify(t *testing.T) {
	tok, prefix, hash, err := MintToken()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(tok, "tfm_") {
		t.Fatalf("bad scheme: %q", tok)
	}
	gotPrefix, gotSecret, err := ParseBearer(tok)
	if err != nil {
		t.Fatal(err)
	}
	if gotPrefix != prefix {
		t.Fatalf("prefix = %q want %q", gotPrefix, prefix)
	}
	if !VerifySecret(gotSecret, hash) {
		t.Fatal("VerifySecret = false for the secret we just minted")
	}
	if VerifySecret("00000000000000000000000000000000", hash) {
		t.Fatal("VerifySecret accepted a wrong secret")
	}
}

func TestParseBearer_Errors(t *testing.T) {
	cases := []string{
		"",
		"token-without-scheme",
		"tfm_abcabcab", // missing _secret
		"tfm_abc_def",  // wrong lengths
		"tfm_zzzzzzzz_00000000000000000000000000000000", // prefix not hex
	}
	for _, in := range cases {
		if _, _, err := ParseBearer(in); err == nil {
			t.Fatalf("ParseBearer(%q) returned nil error", in)
		}
	}
}

func TestFixedToken_Roundtrip(t *testing.T) {
	tok, hash, err := FixedToken("a1a1a1a1", "00000000000000000000000000000001")
	if err != nil {
		t.Fatal(err)
	}
	prefix, secret, err := ParseBearer(tok)
	if err != nil {
		t.Fatal(err)
	}
	if prefix != "a1a1a1a1" {
		t.Fatalf("prefix = %q", prefix)
	}
	if !VerifySecret(secret, hash) {
		t.Fatal("VerifySecret failed for FixedToken roundtrip")
	}
}
