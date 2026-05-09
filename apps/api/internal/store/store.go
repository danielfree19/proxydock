// Package store is the persistence interface used by the API handlers.
//
// Two implementations are provided: an in-memory implementation under
// memory/ for unit tests, and a Postgres implementation under postgres/
// used in production and the Compose demo.
package store

import (
	"context"
	"errors"
	"time"

	"github.com/danielfree19/proxydock/apps/api/internal/model"
)

// Sentinel errors returned across both implementations so handlers can
// map them to HTTP statuses without caring which backend ran.
var (
	ErrNotFound      = errors.New("not found")
	ErrConflict      = errors.New("conflict")
	ErrInvalidInput  = errors.New("invalid input")
	ErrTokenRevoked  = errors.New("token revoked")
	ErrTokenMismatch = errors.New("token does not match")
)

// TokenRecord is the lookup result for verifying a presented bearer token.
type TokenRecord struct {
	Token   model.AgentToken
	Agent   model.Agent
	FleetID string
}

// Store is the union of all operations the API needs.
//
// The interface is intentionally narrow per concept; if a method takes
// raw bytes it accepts what came over the wire (e.g. compiled config),
// not a higher-level type, so the store does not encode validation
// rules — that lives in the API/compiler layers.
type Store interface {
	// Fleets
	CreateFleet(ctx context.Context, f model.Fleet) (model.Fleet, error)
	GetFleet(ctx context.Context, id string) (model.Fleet, error)
	ListFleets(ctx context.Context) ([]model.Fleet, error)
	DeleteFleet(ctx context.Context, id string) error

	// Agents
	CreateAgent(ctx context.Context, a model.Agent) (model.Agent, error)
	GetAgent(ctx context.Context, id string) (model.Agent, error)
	ListAgents(ctx context.Context, fleetID string) ([]model.Agent, error)
	DeleteAgent(ctx context.Context, id string) error
	UpdateAgentHeartbeat(ctx context.Context, agentID string, hb HeartbeatUpdate) error
	UpdateAgentLabels(ctx context.Context, agentID string, labels []string) error

	// Tokens
	MintToken(ctx context.Context, agentID, name, prefix string, secretHash []byte) (model.AgentToken, error)
	ListTokens(ctx context.Context, agentID string) ([]model.AgentToken, error)
	RevokeToken(ctx context.Context, agentID, prefix string) error
	LookupToken(ctx context.Context, prefix string) (TokenRecord, []byte, error)
	TouchToken(ctx context.Context, prefix string) error

	// Proxy hosts
	CreateProxyHost(ctx context.Context, p model.ProxyHost) (model.ProxyHost, error)
	GetProxyHost(ctx context.Context, fleetID string, id int64) (model.ProxyHost, error)
	ListProxyHosts(ctx context.Context, fleetID string) ([]model.ProxyHost, error)
	UpdateProxyHost(ctx context.Context, p model.ProxyHost) (model.ProxyHost, error)
	DeleteProxyHost(ctx context.Context, fleetID string, id int64) error

	// Middleware templates (Phase 7 — fleet-scoped reusable chains)
	CreateMiddlewareTemplate(ctx context.Context, t model.MiddlewareTemplate) (model.MiddlewareTemplate, error)
	GetMiddlewareTemplate(ctx context.Context, fleetID string, id int64) (model.MiddlewareTemplate, error)
	ListMiddlewareTemplates(ctx context.Context, fleetID string) ([]model.MiddlewareTemplate, error)
	UpdateMiddlewareTemplate(ctx context.Context, t model.MiddlewareTemplate) (model.MiddlewareTemplate, error)
	DeleteMiddlewareTemplate(ctx context.Context, fleetID string, id int64) error

	// Webhooks (Phase 7 — outgoing notifications)
	CreateWebhook(ctx context.Context, w model.Webhook) (model.Webhook, error)
	GetWebhook(ctx context.Context, fleetID string, id int64) (model.Webhook, error)
	ListWebhooks(ctx context.Context, fleetID string) ([]model.Webhook, error)
	ListEnabledWebhooks(ctx context.Context, fleetID string, event string) ([]model.Webhook, error)
	UpdateWebhook(ctx context.Context, w model.Webhook) (model.Webhook, error)
	DeleteWebhook(ctx context.Context, fleetID string, id int64) error
	// Webhook jobs (FOR UPDATE SKIP LOCKED queue, mirrors acme_jobs)
	EnqueueWebhookJob(ctx context.Context, j model.WebhookJob) (model.WebhookJob, error)
	ClaimNextWebhookJob(ctx context.Context) (model.WebhookJob, model.Webhook, error)
	FinishWebhookJob(ctx context.Context, id int64, status, lastErr string, nextRunAt time.Time, attempts int) error

	// Revisions
	CreateRevision(ctx context.Context, r model.Revision, makePublished bool) (model.Revision, error)
	GetRevision(ctx context.Context, fleetID string, number int) (model.Revision, error)
	ListRevisions(ctx context.Context, fleetID string) ([]model.Revision, error)
	GetPublishedRevision(ctx context.Context, fleetID string) (model.Revision, error)
	SetPublishedRevision(ctx context.Context, fleetID string, revisionID int64) error
	NextRevisionNumber(ctx context.Context, fleetID string) (int, error)

	// Certificates
	CreateCertificate(ctx context.Context, c model.Certificate) (model.Certificate, error)
	GetCertificate(ctx context.Context, fleetID string, id int64) (model.Certificate, error)
	ListCertificates(ctx context.Context, fleetID string) ([]model.Certificate, error)
	ListAllACMECertificates(ctx context.Context) ([]model.Certificate, error)
	UpdateCertificateMaterial(ctx context.Context, c model.Certificate) error
	DeleteCertificate(ctx context.Context, fleetID string, id int64) error

	// ACME accounts (one per fleet)
	UpsertACMEAccount(ctx context.Context, a model.ACMEAccount) error
	GetACMEAccount(ctx context.Context, fleetID string) (model.ACMEAccount, error)
	DeleteACMEAccount(ctx context.Context, fleetID string) error

	// DNS providers
	CreateDNSProvider(ctx context.Context, d model.DNSProvider) (model.DNSProvider, error)
	GetDNSProvider(ctx context.Context, fleetID string, id int64) (model.DNSProvider, error)
	GetDNSProviderByName(ctx context.Context, fleetID, name string) (model.DNSProvider, error)
	ListDNSProviders(ctx context.Context, fleetID string) ([]model.DNSProvider, error)
	DeleteDNSProvider(ctx context.Context, fleetID string, id int64) error

	// Admin tokens
	MintAdminToken(ctx context.Context, name, prefix string, secretHash []byte) (model.AdminToken, error)
	ListAdminTokens(ctx context.Context) ([]model.AdminToken, error)
	LookupAdminToken(ctx context.Context, prefix string) (model.AdminToken, []byte, error)
	TouchAdminToken(ctx context.Context, prefix string) error
	RevokeAdminToken(ctx context.Context, prefix string) error

	// ACME jobs
	CreateACMEJob(ctx context.Context, j model.ACMEJob) (model.ACMEJob, error)
	GetACMEJob(ctx context.Context, id int64) (model.ACMEJob, error)
	ListACMEJobs(ctx context.Context, fleetID string, limit int) ([]model.ACMEJob, error)
	// ClaimNextACMEJob atomically transitions one pending job to running
	// and returns it. Returns ErrNotFound when the queue is empty.
	ClaimNextACMEJob(ctx context.Context) (model.ACMEJob, error)
	MarkACMEJobSucceeded(ctx context.Context, id, certID int64) error
	MarkACMEJobFailed(ctx context.Context, id int64, errMsg string) error

	// Audit log
	AppendAuditEntry(ctx context.Context, e model.AuditEntry) error
	ListAuditEntries(ctx context.Context, q AuditQuery) ([]model.AuditEntry, error)
}

// AuditQuery describes a paginated audit-log read.
//
//   - FleetID == nil: every fleet (and global entries with fleet_id IS NULL).
//   - FleetID == &"": only global entries (fleet_id IS NULL).
//   - FleetID == &"foo": only entries for fleet "foo".
//
// BeforeID > 0 returns entries strictly older (smaller id) than that
// value, supporting "load more" pagination.
type AuditQuery struct {
	FleetID  *string
	BeforeID int64
	Limit    int
}

// HeartbeatUpdate is the subset of fields a heartbeat updates.
type HeartbeatUpdate struct {
	CurrentRevision int
	ProviderVersion string
	TraefikVersion  string
	LastError       string
}
