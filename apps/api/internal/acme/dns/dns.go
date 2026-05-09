// Package dns is the DNS-01 provider abstraction used by the ACME
// issuer.
//
// A provider knows how to insert and remove a TXT record at
// `_acme-challenge.<domain>.` with the value the ACME server expects.
// The implementation can be a real DNS API (Cloudflare, Route53) or a
// test harness like pebble-challtestsrv. Phase 4b ships only the
// pebble-challtestsrv implementation; production providers slot in by
// implementing this interface.
package dns

import "context"

// Provider is what the ACME issuer calls during a DNS-01 flow.
type Provider interface {
	// Present creates a TXT record at fqdn (e.g.
	// "_acme-challenge.example.com.") with the given value. fqdn is
	// always trailing-dotted by the issuer for unambiguity.
	Present(ctx context.Context, fqdn, value string) error

	// CleanUp removes the TXT record after the challenge resolves.
	// Cleanup failures are logged but never block issuance.
	CleanUp(ctx context.Context, fqdn, value string) error
}
