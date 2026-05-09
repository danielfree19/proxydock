// Package api wires HTTP handlers for the manager.
//
// Routes are organized into two groups:
//
//   - Agent-facing (`/healthz`, `/api/v1/agents/{id}/config`,
//     `/api/v1/agents/{id}/heartbeat`): authenticated with a bearer
//     token in the `tfm_<prefix>_<secret>` format. These are what the
//     Traefik provider plugin calls.
//
//   - Admin (`/api/v1/fleets/*`, `/api/v1/agents/{id}/tokens*`,
//     etc.): currently unauthenticated. Phase 5 will introduce admin auth.
package api

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/danielfree19/proxydock/apps/api/internal/auth"
	"github.com/danielfree19/proxydock/apps/api/internal/cryptokit"
	"github.com/danielfree19/proxydock/apps/api/internal/discovery"
	"github.com/danielfree19/proxydock/apps/api/internal/metrics"
	"github.com/danielfree19/proxydock/apps/api/internal/store"
)

// Server holds dependencies shared by every handler.
type Server struct {
	Logger *slog.Logger
	Store  store.Store
	// Signer signs newly-published revisions when set. Phase 5+. The
	// agent endpoint surfaces the signature so the provider plugin can
	// verify it before applying. Nil means "no signing".
	Signer *cryptokit.Signer
	// BootstrapAdminToken, if set, authorizes the admin API as a fallback
	// when no admin_tokens row matches. Use it once to mint a real
	// admin token, then unset.
	BootstrapAdminToken string
	// InsecureACME, when true, makes the manager skip TLS verification
	// when talking to ACME directories. Required for the Pebble demo;
	// must be off in production.
	InsecureACME bool
	// Metrics, if set, records HTTP request counts/durations and is
	// exposed under /metrics. Nil means metrics are disabled.
	Metrics *metrics.Registry
	// MetricsToken, if set, requires `Authorization: Bearer <token>`
	// on /metrics. Empty leaves it open (default Prometheus pattern).
	MetricsToken string
	// Discovery, if set, enumerates upstream candidates for the New
	// Proxy Host form. Phase 7. Nil disables the /discover endpoint
	// (returns 503).
	Discovery discovery.Provider
}

// Routes returns the registered http.Handler. The handler chains a small
// access-log middleware around the mux.
func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()

	// Agent-facing
	mux.HandleFunc("GET /healthz", s.handleHealth)
	mux.HandleFunc("GET /api/v1/agents/{agent_id}/config", s.handleGetConfig)
	mux.HandleFunc("POST /api/v1/agents/{agent_id}/heartbeat", s.handleHeartbeat)

	// Public: signing pubkey (operators copy this into provider configs)
	mux.HandleFunc("GET /api/v1/signing/pubkey", s.handleSigningPubkey)

	// Public-or-token-gated: Prometheus scrape endpoint
	if s.Metrics != nil {
		mux.Handle("GET /metrics", s.Metrics.Handler(s.MetricsToken))
	}

	// Admin: admin tokens (admin-protected by the same middleware below)
	mux.HandleFunc("GET /api/v1/admin/tokens", s.handleListAdminTokens)
	mux.HandleFunc("POST /api/v1/admin/tokens", s.handleMintAdminToken)
	mux.HandleFunc("POST /api/v1/admin/tokens/{prefix}/revoke", s.handleRevokeAdminToken)
	mux.HandleFunc("GET /api/v1/admin/whoami", s.handleAdminWhoami)
	mux.HandleFunc("GET /api/v1/admin/audit", s.handleListAuditEntries)

	// Admin: fleets
	mux.HandleFunc("GET /api/v1/fleets", s.handleListFleets)
	mux.HandleFunc("POST /api/v1/fleets", s.handleCreateFleet)
	mux.HandleFunc("GET /api/v1/fleets/{fleet_id}", s.handleGetFleet)
	mux.HandleFunc("DELETE /api/v1/fleets/{fleet_id}", s.handleDeleteFleet)

	// Admin: agents (scoped to fleet for create/list)
	mux.HandleFunc("GET /api/v1/fleets/{fleet_id}/agents", s.handleListAgents)
	mux.HandleFunc("POST /api/v1/fleets/{fleet_id}/agents", s.handleCreateAgent)
	mux.HandleFunc("GET /api/v1/agents/{agent_id}", s.handleGetAgent)
	mux.HandleFunc("DELETE /api/v1/agents/{agent_id}", s.handleDeleteAgent)
	mux.HandleFunc("PUT /api/v1/agents/{agent_id}/labels", s.handleUpdateAgentLabels)
	mux.HandleFunc("GET /api/v1/agents/{agent_id}/config-preview", s.handleAgentConfigPreview)

	// Admin: tokens
	mux.HandleFunc("GET /api/v1/agents/{agent_id}/tokens", s.handleListTokens)
	mux.HandleFunc("POST /api/v1/agents/{agent_id}/tokens", s.handleMintToken)
	mux.HandleFunc("POST /api/v1/agents/{agent_id}/tokens/{prefix}/revoke", s.handleRevokeToken)

	// Admin: proxy hosts
	mux.HandleFunc("GET /api/v1/fleets/{fleet_id}/proxy_hosts", s.handleListProxyHosts)
	mux.HandleFunc("POST /api/v1/fleets/{fleet_id}/proxy_hosts", s.handleCreateProxyHost)
	mux.HandleFunc("GET /api/v1/fleets/{fleet_id}/proxy_hosts/{ph_id}", s.handleGetProxyHost)
	mux.HandleFunc("PUT /api/v1/fleets/{fleet_id}/proxy_hosts/{ph_id}", s.handleUpdateProxyHost)
	mux.HandleFunc("DELETE /api/v1/fleets/{fleet_id}/proxy_hosts/{ph_id}", s.handleDeleteProxyHost)

	// Service discovery (Phase 7 — opt-in via MANAGER_API_DISCOVERY)
	mux.HandleFunc("GET /api/v1/discover/services", s.handleDiscoverServices)

	// Webhooks (Phase 7 — outgoing notifications on revision events)
	mux.HandleFunc("GET /api/v1/fleets/{fleet_id}/webhooks", s.handleListWebhooks)
	mux.HandleFunc("POST /api/v1/fleets/{fleet_id}/webhooks", s.handleCreateWebhook)
	mux.HandleFunc("PUT /api/v1/fleets/{fleet_id}/webhooks/{webhook_id}", s.handleUpdateWebhook)
	mux.HandleFunc("DELETE /api/v1/fleets/{fleet_id}/webhooks/{webhook_id}", s.handleDeleteWebhook)

	// Middleware templates (Phase 7 — fleet-scoped reusable chains)
	mux.HandleFunc("GET /api/v1/fleets/{fleet_id}/middleware_templates", s.handleListMiddlewareTemplates)
	mux.HandleFunc("POST /api/v1/fleets/{fleet_id}/middleware_templates", s.handleCreateMiddlewareTemplate)
	mux.HandleFunc("GET /api/v1/fleets/{fleet_id}/middleware_templates/{tpl_id}", s.handleGetMiddlewareTemplate)
	mux.HandleFunc("PUT /api/v1/fleets/{fleet_id}/middleware_templates/{tpl_id}", s.handleUpdateMiddlewareTemplate)
	mux.HandleFunc("DELETE /api/v1/fleets/{fleet_id}/middleware_templates/{tpl_id}", s.handleDeleteMiddlewareTemplate)

	// Admin: revisions
	mux.HandleFunc("GET /api/v1/fleets/{fleet_id}/revisions", s.handleListRevisions)
	mux.HandleFunc("GET /api/v1/fleets/{fleet_id}/revisions/{number}", s.handleGetRevision)
	mux.HandleFunc("POST /api/v1/fleets/{fleet_id}/revisions", s.handlePublishRevision)
	mux.HandleFunc("POST /api/v1/fleets/{fleet_id}/revisions/{number}/rollback", s.handleRollback)

	// Admin: certificates
	mux.HandleFunc("GET /api/v1/fleets/{fleet_id}/certificates", s.handleListCertificates)
	mux.HandleFunc("POST /api/v1/fleets/{fleet_id}/certificates", s.handleCreateCertificate)
	mux.HandleFunc("GET /api/v1/fleets/{fleet_id}/certificates/{cert_id}", s.handleGetCertificate)
	mux.HandleFunc("DELETE /api/v1/fleets/{fleet_id}/certificates/{cert_id}", s.handleDeleteCertificate)
	mux.HandleFunc("POST /api/v1/fleets/{fleet_id}/certificates/acme", s.handleRequestACMECertificate)
	mux.HandleFunc("GET /api/v1/fleets/{fleet_id}/jobs", s.handleListACMEJobs)
	mux.HandleFunc("GET /api/v1/jobs/{job_id}", s.handleGetACMEJob)

	// Admin: ACME accounts
	mux.HandleFunc("GET /api/v1/fleets/{fleet_id}/acme/account", s.handleGetACMEAccount)
	mux.HandleFunc("POST /api/v1/fleets/{fleet_id}/acme/account", s.handleRegisterACMEAccount)
	mux.HandleFunc("DELETE /api/v1/fleets/{fleet_id}/acme/account", s.handleDeleteACMEAccount)

	// Admin: DNS providers
	mux.HandleFunc("GET /api/v1/fleets/{fleet_id}/dns_providers", s.handleListDNSProviders)
	mux.HandleFunc("POST /api/v1/fleets/{fleet_id}/dns_providers", s.handleCreateDNSProvider)
	mux.HandleFunc("DELETE /api/v1/fleets/{fleet_id}/dns_providers/{dns_id}", s.handleDeleteDNSProvider)

	// Order matters: observe → logging → requireAdmin (sets actor in
	// context) → audit (reads actor on the way out) → mux.
	return s.observe(logging(s.Logger, s.requireAdmin(s.audit(mux))))
}

// observe wraps the handler with a Prometheus-recording middleware
// when Metrics is configured. /metrics requests are skipped to avoid
// polluting the metric with self-scrapes.
func (s *Server) observe(next http.Handler) http.Handler {
	if s.Metrics == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/metrics" {
			next.ServeHTTP(w, r)
			return
		}
		sr := &statusRecorder{ResponseWriter: w, status: 200}
		start := time.Now()
		next.ServeHTTP(sr, r)
		s.Metrics.ObserveHTTP(r.Method, sr.status, time.Since(start))
	})
}

// requireAdmin enforces admin authentication on every path that isn't
// public-by-design. The agent-facing endpoints (/api/v1/agents/{id}/config,
// /api/v1/agents/{id}/heartbeat) carry their own per-agent bearer
// auth and are excluded here.
//
// On success the verified actor identity ("bootstrap" or
// "admin:<prefix>") is stashed in the request context so the audit
// middleware downstream can record who made each mutation.
func (s *Server) requireAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isPublicPath(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		actor, err := s.checkAdminAuth(r)
		if err != nil {
			s.Logger.Warn("admin auth check failed", "err", err, "path", r.URL.Path)
		}
		if actor == "" {
			writeError(w, http.StatusUnauthorized, "admin authentication required")
			return
		}
		next.ServeHTTP(w, r.WithContext(withActor(r.Context(), actor)))
	})
}

// isPublicPath returns true for the agent-facing endpoints, /healthz,
// the signing pubkey, and the SPA bundle (which doesn't go through
// this mux at all but is listed here for clarity).
func isPublicPath(p string) bool {
	switch {
	case p == "/healthz":
		return true
	case p == "/api/v1/signing/pubkey":
		return true
	case p == "/metrics":
		// /metrics has its own optional bearer-token gate inside the
		// metrics handler; the admin auth middleware skips it.
		return true
	case strings.HasPrefix(p, "/api/v1/agents/"):
		// Only "config" / "heartbeat" suffixes are public; anything else
		// (admin agent CRUD or token endpoints) requires admin auth.
		rest := strings.TrimPrefix(p, "/api/v1/agents/")
		return strings.HasSuffix(rest, "/config") || strings.HasSuffix(rest, "/heartbeat")
	default:
		return false
	}
}

// checkAdminAuth validates an Authorization: Bearer header against
// either the env-configured bootstrap token or a row in admin_tokens.
//
// Returns the actor identity ("bootstrap" or "admin:<prefix>") on
// success. Empty actor means "not authorized"; the err is set only
// for non-fatal store errors that the caller should log.
func (s *Server) checkAdminAuth(r *http.Request) (string, error) {
	tok, err := bearerHeader(r)
	if err != nil {
		return "", nil
	}
	if s.BootstrapAdminToken != "" && tok == s.BootstrapAdminToken {
		return actorBootstrap, nil
	}
	prefix, secret, err := auth.ParseBearer(tok)
	if err != nil {
		return "", nil
	}
	rec, hash, err := s.Store.LookupAdminToken(r.Context(), prefix)
	if err != nil {
		return "", err
	}
	if !auth.VerifySecret(secret, hash) {
		return "", nil
	}
	if err := s.Store.TouchAdminToken(r.Context(), prefix); err != nil {
		s.Logger.Warn("admin touch", "prefix", rec.Prefix, "err", err)
	}
	return "admin:" + rec.Prefix, nil
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// handleSigningPubkey returns the manager's ed25519 public key so
// operators can configure provider plugins to verify signatures. The
// endpoint is intentionally unauthenticated — the public key is, by
// design, public.
func (s *Server) handleSigningPubkey(w http.ResponseWriter, _ *http.Request) {
	if s.Signer == nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"enabled": false,
			"alg":     "",
			"public_key": "",
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"enabled":    true,
		"alg":        cryptokit.SignatureAlg,
		"public_key": s.Signer.PublicKey(),
	})
}

// --- response helpers ---

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// errorStatus maps store sentinel errors to a sensible HTTP status.
func errorStatus(err error) int {
	switch {
	case errors.Is(err, store.ErrNotFound):
		return http.StatusNotFound
	case errors.Is(err, store.ErrConflict):
		return http.StatusConflict
	case errors.Is(err, store.ErrInvalidInput):
		return http.StatusBadRequest
	case errors.Is(err, store.ErrTokenRevoked):
		return http.StatusUnauthorized
	default:
		return http.StatusInternalServerError
	}
}

// writeStoreError chooses the status code based on the store error and
// avoids leaking internal error messages to clients (the message is
// captured in the access-log line by the logging middleware).
func writeStoreError(w http.ResponseWriter, err error) {
	switch errorStatus(err) {
	case http.StatusInternalServerError:
		writeError(w, http.StatusInternalServerError, "internal error")
	case http.StatusNotFound:
		writeError(w, http.StatusNotFound, "not found")
	case http.StatusConflict:
		writeError(w, http.StatusConflict, "already exists")
	case http.StatusBadRequest:
		writeError(w, http.StatusBadRequest, err.Error())
	case http.StatusUnauthorized:
		writeError(w, http.StatusUnauthorized, "token revoked")
	}
}

func bearerHeader(r *http.Request) (string, error) {
	h := r.Header.Get("Authorization")
	if h == "" {
		return "", errors.New("missing Authorization header")
	}
	const prefix = "Bearer "
	if !strings.HasPrefix(h, prefix) {
		return "", errors.New("malformed Authorization header")
	}
	tok := strings.TrimSpace(h[len(prefix):])
	if tok == "" {
		return "", errors.New("empty bearer token")
	}
	return tok, nil
}

// etagMatches reports whether If-None-Match matches our ETag.
func etagMatches(header, etag string) bool {
	for _, v := range strings.Split(header, ",") {
		v = strings.TrimSpace(v)
		v = strings.TrimPrefix(v, "W/")
		if v == etag {
			return true
		}
	}
	return false
}

// logging is a small access-log middleware that records method, path,
// status code, and duration.
func logging(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sr := &statusRecorder{ResponseWriter: w, status: 200}
		start := time.Now()
		next.ServeHTTP(sr, r)
		logger.Info("http",
			"method", r.Method,
			"path", r.URL.Path,
			"status", sr.status,
			"dur_ms", time.Since(start).Milliseconds())
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}
