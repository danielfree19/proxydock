// Package model defines the domain types used across the manager.
//
// These types are exposed at the API and persisted in the database; the
// JSON tags are part of the public contract.
package model

import (
	"encoding/json"
	"time"
)

// Fleet groups agents that share a published configuration.
type Fleet struct {
	ID                  string    `json:"id"`
	Name                string    `json:"name"`
	PublishedRevisionID *int64    `json:"published_revision_id,omitempty"`
	CreatedAt           time.Time `json:"created_at"`
}

// Agent is a single Traefik instance.
type Agent struct {
	ID                  string     `json:"id"`
	FleetID             string     `json:"fleet_id"`
	Name                string     `json:"name"`
	// Labels are "key=value" strings the operator attaches to the
	// agent for selector-based revision targeting (Phase 5b).
	Labels              []string   `json:"labels"`
	LastHeartbeatAt     *time.Time `json:"last_heartbeat_at,omitempty"`
	LastRevisionSeen    *int       `json:"last_revision_seen,omitempty"`
	LastProviderVersion *string    `json:"last_provider_version,omitempty"`
	LastTraefikVersion  *string    `json:"last_traefik_version,omitempty"`
	LastError           *string    `json:"last_error,omitempty"`
	CreatedAt           time.Time  `json:"created_at"`
}

// AgentToken is metadata about an issued bearer token.
//
// The secret half is never returned after issuance; only Mint() returns
// the plaintext token to the caller.
type AgentToken struct {
	Prefix     string     `json:"prefix"`
	AgentID    string     `json:"agent_id"`
	Name       string     `json:"name,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
	RevokedAt  *time.Time `json:"revoked_at,omitempty"`
}

// Middleware is one entry in a proxy host's middleware chain.
//
// Type names map to Traefik's built-in middleware types (e.g. "headers",
// "redirectScheme", "stripPrefix", "basicAuth").
type Middleware struct {
	Name   string         `json:"name"`
	Type   string         `json:"type"`
	Config map[string]any `json:"config,omitempty"`
}

// MiddlewareTemplate is a fleet-scoped, named, reusable chain of
// middlewares. Apply-by-copy: when a proxy host applies a template,
// the template's middlewares are deep-copied into the host. Editing
// the template later does NOT affect already-applied hosts — this
// keeps the mental model simple ("templates are starting points") and
// avoids "edit one row, surprise change to N hosts" panic.
type MiddlewareTemplate struct {
	ID          int64        `json:"id"`
	FleetID     string       `json:"fleet_id"`
	Name        string       `json:"name"`
	Description string       `json:"description"`
	Middlewares []Middleware `json:"middlewares"`
	CreatedAt   time.Time    `json:"created_at"`
	UpdatedAt   time.Time    `json:"updated_at"`
}

// ProxyHost is the desired state for one routed entry. Phase 6 added a
// Protocol field; "http" (the default for legacy rows) keeps the
// existing behaviour, "tcp" and "udp" switch the compiler into
// Traefik's L4 routing modes.
type ProxyHost struct {
	ID      int64  `json:"id"`
	FleetID string `json:"fleet_id"`
	Name    string `json:"name"`
	// Protocol is "http" | "tcp" | "udp". Empty is treated as "http".
	Protocol string `json:"protocol"`
	Domain   string `json:"domain"`
	// UpstreamURL is the legacy single-URL field. The authoritative
	// list is UpstreamURLs (Phase 7). When the array is non-empty
	// the compiler ignores this field; it stays populated as
	// UpstreamURLs[0] for older API clients that haven't rolled
	// forward yet.
	UpstreamURL string `json:"upstream_url"`
	// UpstreamURLs is the multi-server load-balanced upstream list.
	// Phase 7. Empty falls back to []{UpstreamURL}.
	UpstreamURLs []string `json:"upstream_urls"`
	// StickySession enables Traefik's cookie-based stickiness on the
	// HTTP load balancer. Ignored for TCP/UDP. Phase 7.
	StickySession bool `json:"sticky_session"`
	// HealthCheck holds the Traefik healthCheck config for HTTP
	// services. Empty map disables health checks. Phase 7.
	HealthCheck   map[string]any `json:"health_check,omitempty"`
	EntryPoints   []string       `json:"entry_points"`
	Middlewares   []Middleware `json:"middlewares"`
	TLS           bool         `json:"tls"`
	// LabelSelector is an empty-or-comma-separated list of
	// "key=value" requirements (Phase 5b). An empty selector matches
	// every agent in the fleet.
	LabelSelector string    `json:"label_selector"`
	Enabled       bool      `json:"enabled"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// Certificate is an uploaded PEM certificate + private key, owned by a
// fleet and pooled into every published revision's tls.certificates.
//
// CertPEM is returned in API responses; KeyPEM is never returned after
// upload — the only places the key lives are the database and the
// compiled revision payload sent to authenticated agents.
type Certificate struct {
	ID          int64     `json:"id"`
	FleetID     string    `json:"fleet_id"`
	Name        string    `json:"name"`
	CertPEM     string    `json:"cert_pem,omitempty"`
	KeyPEM      string    `json:"-"`
	Fingerprint string    `json:"fingerprint"`
	Subject     string    `json:"subject"`
	Issuer      string    `json:"issuer"`
	DNSNames    []string  `json:"dns_names"`
	NotBefore   time.Time `json:"not_before"`
	NotAfter    time.Time `json:"not_after"`
	// Source is "upload" or "acme". The renewal goroutine only touches
	// "acme" rows.
	Source    string    `json:"source"`
	CreatedAt time.Time `json:"created_at"`
}

// ACMEAccount is the per-fleet account key registered with an ACME CA.
//
// AccountKeyPEM is sensitive. The handler layer strips it before
// returning ACMEAccount in responses.
type ACMEAccount struct {
	FleetID       string    `json:"fleet_id"`
	DirectoryURL  string    `json:"directory_url"`
	ContactEmail  string    `json:"contact_email"`
	AccountKeyPEM string    `json:"-"`
	AccountURL    string    `json:"account_url"`
	CreatedAt     time.Time `json:"created_at"`
}

// Webhook is one outgoing destination per fleet. Phase 7. Fired on
// revision_published / revision_rolled_back / acme_certificate_issued
// events.
type Webhook struct {
	ID        int64     `json:"id"`
	FleetID   string    `json:"fleet_id"`
	Name      string    `json:"name"`
	URL       string    `json:"url"`
	Secret    string    `json:"-"` // HMAC key — never returned in API responses
	HasSecret bool      `json:"has_secret"`
	Events    []string  `json:"events"`
	Enabled   bool      `json:"enabled"`
	CreatedAt time.Time `json:"created_at"`
}

// WebhookJob is one queued delivery attempt. Mirrors acme_jobs.
type WebhookJob struct {
	ID         int64      `json:"id"`
	WebhookID  int64      `json:"webhook_id"`
	Payload    string     `json:"payload"`
	Status     string     `json:"status"` // pending | running | succeeded | failed
	Attempts   int        `json:"attempts"`
	NextRunAt  time.Time  `json:"next_run_at"`
	LastError  string     `json:"last_error,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
}

// AdminToken authorizes admin API requests. The prefix is non-secret
// and indexable; only sha256(secret) is stored. Mirrors AgentToken.
type AdminToken struct {
	Prefix     string     `json:"prefix"`
	Name       string     `json:"name,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
	RevokedAt  *time.Time `json:"revoked_at,omitempty"`
}

// AuditEntry is one row of the admin-action audit log.
type AuditEntry struct {
	ID        int64     `json:"id"`
	Actor     string    `json:"actor"`
	Method    string    `json:"method"`
	Path      string    `json:"path"`
	Status    int       `json:"status"`
	FleetID   *string   `json:"fleet_id,omitempty"`
	Summary   string    `json:"summary,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

// ACMEJobStatus is a small finite enum for the worker queue.
type ACMEJobStatus string

const (
	ACMEJobPending   ACMEJobStatus = "pending"
	ACMEJobRunning   ACMEJobStatus = "running"
	ACMEJobSucceeded ACMEJobStatus = "succeeded"
	ACMEJobFailed    ACMEJobStatus = "failed"
)

// ACMEJob is one queued certificate issuance. The worker scans
// status='pending' rows, claims one, runs the ACME flow, and writes
// the result back.
type ACMEJob struct {
	ID          int64         `json:"id"`
	FleetID     string        `json:"fleet_id"`
	Name        string        `json:"name"`
	DNSNames    []string      `json:"dns_names"`
	DNSProvider string        `json:"dns_provider"`
	Status      ACMEJobStatus `json:"status"`
	Error       string        `json:"error,omitempty"`
	// CertID is set after a successful run; the UI links to the cert.
	CertID     *int64     `json:"cert_id,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
	StartedAt  *time.Time `json:"started_at,omitempty"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
}

// DNSProvider is a configured DNS-01 provider (per fleet, by name).
//
// Type picks the implementation; Config carries the type-specific
// credentials/options as JSON. Phase 5 hardening will encrypt Config
// at rest. The handler layer never returns Config in list responses
// (only on the explicit GET endpoint, and only behind admin auth once
// admin auth lands).
type DNSProvider struct {
	ID        int64           `json:"id"`
	FleetID   string          `json:"fleet_id"`
	Name      string          `json:"name"`
	Type      string          `json:"type"`
	Config    json.RawMessage `json:"config,omitempty"`
	CreatedAt time.Time       `json:"created_at"`
}

// Revision is one published, compiled configuration for a fleet.
type Revision struct {
	ID               int64           `json:"id"`
	FleetID          string          `json:"fleet_id"`
	Number           int             `json:"number"`
	CompiledConfig   json.RawMessage `json:"compiled_config"`
	SourceProxyHosts json.RawMessage `json:"source_proxy_hosts"`
	// SourceCerts is the snapshot of fleet.certificates at publish time
	// (Phase 5b). The agent endpoint re-runs the compiler per-agent on
	// SourceProxyHosts + SourceCerts so a later cert rotation doesn't
	// silently change what an agent receives mid-revision.
	SourceCerts      json.RawMessage `json:"source_certs"`
	ETag             string          `json:"etag"`
	Notes            string          `json:"notes,omitempty"`
	// Signature is a base64-encoded signature over CompiledConfig
	// (Phase 5+). Empty for revisions published before signing was
	// enabled; the provider plugin treats absent signatures as
	// "no verification required".
	Signature    string    `json:"signature,omitempty"`
	SignatureAlg string    `json:"signature_alg,omitempty"`
	GeneratedAt  time.Time `json:"generated_at"`
}
