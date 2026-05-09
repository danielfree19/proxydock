package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/danielfree19/proxydock/apps/api/internal/auth"
	"github.com/danielfree19/proxydock/apps/api/internal/cert"
	"github.com/danielfree19/proxydock/apps/api/internal/compiler"
	"github.com/danielfree19/proxydock/apps/api/internal/cryptokit"
	"github.com/danielfree19/proxydock/apps/api/internal/labels"
	"github.com/danielfree19/proxydock/apps/api/internal/model"
	"github.com/danielfree19/proxydock/apps/api/internal/store"
)

// signRevision attaches a signature + alg to the revision when the
// server has a signer configured. Idempotent on a Server with no
// Signer.
func (s *Server) signRevision(rev *model.Revision) {
	if s.Signer == nil {
		return
	}
	rev.Signature = s.Signer.Sign(rev.CompiledConfig)
	rev.SignatureAlg = cryptokit.SignatureAlg
}

// --- Fleets ---

func (s *Server) handleListFleets(w http.ResponseWriter, r *http.Request) {
	fl, err := s.Store.ListFleets(r.Context())
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"fleets": fl})
}

func (s *Server) handleCreateFleet(w http.ResponseWriter, r *http.Request) {
	var in struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	if !decodeBody(w, r, &in) {
		return
	}
	if in.ID == "" || in.Name == "" {
		writeError(w, http.StatusBadRequest, "id and name are required")
		return
	}
	f, err := s.Store.CreateFleet(r.Context(), model.Fleet{ID: in.ID, Name: in.Name})
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, f)
}

func (s *Server) handleGetFleet(w http.ResponseWriter, r *http.Request) {
	f, err := s.Store.GetFleet(r.Context(), r.PathValue("fleet_id"))
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, f)
}

func (s *Server) handleDeleteFleet(w http.ResponseWriter, r *http.Request) {
	if err := s.Store.DeleteFleet(r.Context(), r.PathValue("fleet_id")); err != nil {
		writeStoreError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- Agents ---

func (s *Server) handleListAgents(w http.ResponseWriter, r *http.Request) {
	fleetID := r.PathValue("fleet_id")
	// Confirm the fleet exists so callers see a 404, not an empty list.
	if _, err := s.Store.GetFleet(r.Context(), fleetID); err != nil {
		writeStoreError(w, err)
		return
	}
	ags, err := s.Store.ListAgents(r.Context(), fleetID)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"agents": ags})
}

func (s *Server) handleCreateAgent(w http.ResponseWriter, r *http.Request) {
	fleetID := r.PathValue("fleet_id")
	var in struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	if !decodeBody(w, r, &in) {
		return
	}
	if in.ID == "" || in.Name == "" {
		writeError(w, http.StatusBadRequest, "id and name are required")
		return
	}
	a, err := s.Store.CreateAgent(r.Context(), model.Agent{
		ID: in.ID, FleetID: fleetID, Name: in.Name,
	})
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, a)
}

func (s *Server) handleGetAgent(w http.ResponseWriter, r *http.Request) {
	a, err := s.Store.GetAgent(r.Context(), r.PathValue("agent_id"))
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, a)
}

func (s *Server) handleDeleteAgent(w http.ResponseWriter, r *http.Request) {
	if err := s.Store.DeleteAgent(r.Context(), r.PathValue("agent_id")); err != nil {
		writeStoreError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleUpdateAgentLabels(w http.ResponseWriter, r *http.Request) {
	agentID := r.PathValue("agent_id")
	var in struct {
		Labels []string `json:"labels"`
	}
	if !decodeBody(w, r, &in) {
		return
	}
	// Validate each label looks like "key=value" — silently dropping
	// malformed entries would let agents quietly miss matches.
	for _, l := range in.Labels {
		if l == "" {
			writeError(w, http.StatusBadRequest, "labels: empty entry not allowed")
			return
		}
		if k, _, ok := strings.Cut(l, "="); !ok || strings.TrimSpace(k) == "" {
			writeError(w, http.StatusBadRequest, "labels: each entry must be key=value")
			return
		}
	}
	if err := s.Store.UpdateAgentLabels(r.Context(), agentID, in.Labels); err != nil {
		writeStoreError(w, err)
		return
	}
	a, err := s.Store.GetAgent(r.Context(), agentID)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, a)
}

// --- Tokens ---

func (s *Server) handleListTokens(w http.ResponseWriter, r *http.Request) {
	agentID := r.PathValue("agent_id")
	if _, err := s.Store.GetAgent(r.Context(), agentID); err != nil {
		writeStoreError(w, err)
		return
	}
	ts, err := s.Store.ListTokens(r.Context(), agentID)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"tokens": ts})
}

func (s *Server) handleMintToken(w http.ResponseWriter, r *http.Request) {
	agentID := r.PathValue("agent_id")
	if _, err := s.Store.GetAgent(r.Context(), agentID); err != nil {
		writeStoreError(w, err)
		return
	}
	var in struct {
		Name string `json:"name"`
	}
	_ = decodeBody(w, r, &in) // body is optional; ignore decode error if empty

	token, prefix, hash, err := auth.MintToken()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not mint token")
		return
	}
	rec, err := s.Store.MintToken(r.Context(), agentID, in.Name, prefix, hash)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	// Plaintext token is returned ONCE here. Callers must capture it now.
	writeJSON(w, http.StatusCreated, map[string]any{
		"token":    token,
		"metadata": rec,
	})
}

func (s *Server) handleRevokeToken(w http.ResponseWriter, r *http.Request) {
	agentID := r.PathValue("agent_id")
	prefix := r.PathValue("prefix")
	if err := s.Store.RevokeToken(r.Context(), agentID, prefix); err != nil {
		writeStoreError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- Proxy hosts ---

func (s *Server) handleListProxyHosts(w http.ResponseWriter, r *http.Request) {
	fleetID := r.PathValue("fleet_id")
	if _, err := s.Store.GetFleet(r.Context(), fleetID); err != nil {
		writeStoreError(w, err)
		return
	}
	phs, err := s.Store.ListProxyHosts(r.Context(), fleetID)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"proxy_hosts": phs})
}

func (s *Server) handleCreateProxyHost(w http.ResponseWriter, r *http.Request) {
	fleetID := r.PathValue("fleet_id")
	var in proxyHostInput
	if !decodeBody(w, r, &in) {
		return
	}
	if err := in.validate(); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := labels.Validate(in.LabelSelector); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	p := model.ProxyHost{
		FleetID:       fleetID,
		Name:          in.Name,
		Protocol:      in.Protocol,
		Domain:        in.Domain,
		UpstreamURL:   in.UpstreamURL,
		UpstreamURLs:  in.UpstreamURLs,
		StickySession: in.StickySession,
		HealthCheck:   in.HealthCheck,
		EntryPoints:   in.EntryPoints,
		Middlewares:   in.Middlewares,
		TLS:           in.TLS,
		LabelSelector: in.LabelSelector,
		Enabled:       in.enabledOrDefault(),
	}
	out, err := s.Store.CreateProxyHost(r.Context(), p)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, out)
}

func (s *Server) handleGetProxyHost(w http.ResponseWriter, r *http.Request) {
	fleetID := r.PathValue("fleet_id")
	id, err := parsePathID(r.PathValue("ph_id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "ph_id must be an integer")
		return
	}
	p, err := s.Store.GetProxyHost(r.Context(), fleetID, id)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, p)
}

func (s *Server) handleUpdateProxyHost(w http.ResponseWriter, r *http.Request) {
	fleetID := r.PathValue("fleet_id")
	id, err := parsePathID(r.PathValue("ph_id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "ph_id must be an integer")
		return
	}
	var in proxyHostInput
	if !decodeBody(w, r, &in) {
		return
	}
	if err := in.validate(); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := labels.Validate(in.LabelSelector); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	out, err := s.Store.UpdateProxyHost(r.Context(), model.ProxyHost{
		ID:            id,
		FleetID:       fleetID,
		Name:          in.Name,
		Protocol:      in.Protocol,
		Domain:        in.Domain,
		UpstreamURL:   in.UpstreamURL,
		UpstreamURLs:  in.UpstreamURLs,
		StickySession: in.StickySession,
		HealthCheck:   in.HealthCheck,
		EntryPoints:   in.EntryPoints,
		Middlewares:   in.Middlewares,
		TLS:           in.TLS,
		LabelSelector: in.LabelSelector,
		Enabled:       in.enabledOrDefault(),
	})
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleDeleteProxyHost(w http.ResponseWriter, r *http.Request) {
	fleetID := r.PathValue("fleet_id")
	id, err := parsePathID(r.PathValue("ph_id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "ph_id must be an integer")
		return
	}
	if err := s.Store.DeleteProxyHost(r.Context(), fleetID, id); err != nil {
		writeStoreError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- Revisions ---

func (s *Server) handleListRevisions(w http.ResponseWriter, r *http.Request) {
	fleetID := r.PathValue("fleet_id")
	if _, err := s.Store.GetFleet(r.Context(), fleetID); err != nil {
		writeStoreError(w, err)
		return
	}
	revs, err := s.Store.ListRevisions(r.Context(), fleetID)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"revisions": revs})
}

func (s *Server) handleGetRevision(w http.ResponseWriter, r *http.Request) {
	fleetID := r.PathValue("fleet_id")
	num, err := strconv.Atoi(r.PathValue("number"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "number must be an integer")
		return
	}
	rev, err := s.Store.GetRevision(r.Context(), fleetID, num)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rev)
}

// handlePublishRevision compiles the current proxy_hosts of a fleet
// into a new revision and marks it as published.
func (s *Server) handlePublishRevision(w http.ResponseWriter, r *http.Request) {
	fleetID := r.PathValue("fleet_id")
	if _, err := s.Store.GetFleet(r.Context(), fleetID); err != nil {
		writeStoreError(w, err)
		return
	}

	var in struct {
		Notes string `json:"notes"`
	}
	_ = decodeBody(w, r, &in) // optional body

	hosts, err := s.Store.ListProxyHosts(r.Context(), fleetID)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	certs, err := s.Store.ListCertificates(r.Context(), fleetID)
	if err != nil {
		writeStoreError(w, err)
		return
	}

	res, err := compiler.Compile(hosts, certs)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	num, err := s.Store.NextRevisionNumber(r.Context(), fleetID)
	if err != nil {
		writeStoreError(w, err)
		return
	}

	source, err := json.Marshal(hosts)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not snapshot proxy hosts")
		return
	}
	sourceCerts, err := json.Marshal(certs)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not snapshot certificates")
		return
	}
	rev := model.Revision{
		FleetID:          fleetID,
		Number:           num,
		CompiledConfig:   res.Config,
		SourceProxyHosts: source,
		SourceCerts:      sourceCerts,
		ETag:             res.ETag,
		Notes:            strings.TrimSpace(in.Notes),
	}
	s.signRevision(&rev)
	saved, err := s.Store.CreateRevision(r.Context(), rev, true)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	s.fireWebhooks(r.Context(), fleetID, "revision_published", saved)
	writeJSON(w, http.StatusCreated, saved)
}

// fireWebhooks enqueues a delivery job for every enabled webhook in
// the fleet that subscribes to `event`. Failures are logged and
// swallowed — webhooks are operational signal, not on the request's
// critical path.
func (s *Server) fireWebhooks(ctx context.Context, fleetID, event string, rev model.Revision) {
	hooks, err := s.Store.ListEnabledWebhooks(ctx, fleetID, event)
	if err != nil {
		s.Logger.Warn("list webhooks failed", "err", err, "fleet_id", fleetID)
		return
	}
	if len(hooks) == 0 {
		return
	}
	payload, err := json.Marshal(map[string]any{
		"event":           event,
		"fleet_id":        fleetID,
		"revision_number": rev.Number,
		"etag":            rev.ETag,
		"generated_at":    rev.GeneratedAt,
	})
	if err != nil {
		s.Logger.Warn("marshal webhook payload", "err", err)
		return
	}
	for _, h := range hooks {
		if _, err := s.Store.EnqueueWebhookJob(ctx, model.WebhookJob{
			WebhookID: h.ID,
			Payload:   string(payload),
		}); err != nil {
			s.Logger.Warn("enqueue webhook", "webhook_id", h.ID, "err", err)
		}
	}
}

// handleRollback creates a new revision whose compiled config is a copy
// of an older revision, and publishes that. We do not reuse the old
// revision number — every published revision keeps a unique, monotonic
// number to make audit logs unambiguous.
func (s *Server) handleRollback(w http.ResponseWriter, r *http.Request) {
	fleetID := r.PathValue("fleet_id")
	num, err := strconv.Atoi(r.PathValue("number"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "number must be an integer")
		return
	}
	old, err := s.Store.GetRevision(r.Context(), fleetID, num)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	next, err := s.Store.NextRevisionNumber(r.Context(), fleetID)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	rev := model.Revision{
		FleetID:          fleetID,
		Number:           next,
		CompiledConfig:   old.CompiledConfig,
		SourceProxyHosts: old.SourceProxyHosts,
		SourceCerts:      old.SourceCerts,
		ETag:             old.ETag,
		Notes:            "rollback to revision " + strconv.Itoa(num),
	}
	s.signRevision(&rev)
	saved, err := s.Store.CreateRevision(r.Context(), rev, true)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	s.fireWebhooks(r.Context(), fleetID, "revision_rolled_back", saved)
	writeJSON(w, http.StatusCreated, saved)
}

// --- Certificates ---

func (s *Server) handleListCertificates(w http.ResponseWriter, r *http.Request) {
	fleetID := r.PathValue("fleet_id")
	if _, err := s.Store.GetFleet(r.Context(), fleetID); err != nil {
		writeStoreError(w, err)
		return
	}
	cs, err := s.Store.ListCertificates(r.Context(), fleetID)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	// Strip key bytes before they leave the process.
	for i := range cs {
		cs[i].KeyPEM = ""
	}
	writeJSON(w, http.StatusOK, map[string]any{"certificates": cs})
}

func (s *Server) handleGetCertificate(w http.ResponseWriter, r *http.Request) {
	fleetID := r.PathValue("fleet_id")
	id, err := parsePathID(r.PathValue("cert_id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "cert_id must be an integer")
		return
	}
	c, err := s.Store.GetCertificate(r.Context(), fleetID, id)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	c.KeyPEM = ""
	writeJSON(w, http.StatusOK, c)
}

func (s *Server) handleCreateCertificate(w http.ResponseWriter, r *http.Request) {
	fleetID := r.PathValue("fleet_id")
	if _, err := s.Store.GetFleet(r.Context(), fleetID); err != nil {
		writeStoreError(w, err)
		return
	}
	var in struct {
		Name    string `json:"name"`
		CertPEM string `json:"cert_pem"`
		KeyPEM  string `json:"key_pem"`
	}
	if !decodeBody(w, r, &in) {
		return
	}
	if strings.TrimSpace(in.Name) == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	parsed, err := cert.Parse(in.CertPEM, in.KeyPEM)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	saved, err := s.Store.CreateCertificate(r.Context(), model.Certificate{
		FleetID:     fleetID,
		Name:        in.Name,
		CertPEM:     parsed.CertPEM,
		KeyPEM:      parsed.KeyPEM,
		Fingerprint: parsed.Fingerprint,
		Subject:     parsed.Subject,
		Issuer:      parsed.Issuer,
		DNSNames:    parsed.DNSNames,
		NotBefore:   parsed.NotBefore,
		NotAfter:    parsed.NotAfter,
	})
	if err != nil {
		writeStoreError(w, err)
		return
	}
	saved.KeyPEM = ""
	writeJSON(w, http.StatusCreated, saved)
}

func (s *Server) handleDeleteCertificate(w http.ResponseWriter, r *http.Request) {
	fleetID := r.PathValue("fleet_id")
	id, err := parsePathID(r.PathValue("cert_id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "cert_id must be an integer")
		return
	}
	if err := s.Store.DeleteCertificate(r.Context(), fleetID, id); err != nil {
		writeStoreError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- helpers ---

type proxyHostInput struct {
	Name          string             `json:"name"`
	Protocol      string             `json:"protocol,omitempty"`
	Domain        string             `json:"domain"`
	UpstreamURL   string             `json:"upstream_url,omitempty"`
	UpstreamURLs  []string           `json:"upstream_urls,omitempty"`
	StickySession bool               `json:"sticky_session,omitempty"`
	HealthCheck   map[string]any     `json:"health_check,omitempty"`
	EntryPoints   []string           `json:"entry_points,omitempty"`
	Middlewares   []model.Middleware `json:"middlewares,omitempty"`
	TLS           bool               `json:"tls,omitempty"`
	LabelSelector string             `json:"label_selector,omitempty"`
	Enabled       *bool              `json:"enabled,omitempty"`
}

func (in proxyHostInput) validate() error {
	if strings.TrimSpace(in.Name) == "" {
		return errors.New("name is required")
	}
	// Phase 7: either the legacy single URL or the array form is fine
	// (compiler normalizes them); reject only when both are empty.
	hasLegacy := strings.TrimSpace(in.UpstreamURL) != ""
	hasArray := false
	for _, u := range in.UpstreamURLs {
		if strings.TrimSpace(u) != "" {
			hasArray = true
			break
		}
	}
	if !hasLegacy && !hasArray {
		return errors.New("upstream_url or upstream_urls is required")
	}
	// Domain is required for HTTP and TCP routers (it's the host /
	// SNI rule); UDP routers are matched by entry point alone, so
	// the field is ignored. The compiler does deeper protocol-aware
	// validation; this check stops obviously broken HTTP/TCP rows
	// at the API surface.
	proto := in.Protocol
	if proto == "" {
		proto = "http"
	}
	if proto != "udp" && strings.TrimSpace(in.Domain) == "" {
		return errors.New("domain is required for http and tcp protocols")
	}
	return nil
}

func (in proxyHostInput) enabledOrDefault() bool {
	if in.Enabled == nil {
		return true
	}
	return *in.Enabled
}

func decodeBody(w http.ResponseWriter, r *http.Request, dst any) bool {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		// An empty body is fine for endpoints with optional input.
		if errors.Is(err, errReqEmpty) {
			return true
		}
		// json.Decoder returns io.EOF on empty body
		if err.Error() == "EOF" {
			return true
		}
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return false
	}
	return true
}

var errReqEmpty = errors.New("empty body")

func parsePathID(s string) (int64, error) {
	return strconv.ParseInt(s, 10, 64)
}

// --- Audit ---

func (s *Server) handleListAuditEntries(w http.ResponseWriter, r *http.Request) {
	q := store.AuditQuery{Limit: 100}
	if v := r.URL.Query().Get("fleet_id"); v != "" {
		// "global" is a sentinel that means fleet_id IS NULL — handy
		// for filtering to non-fleet actions like admin token mgmt.
		fleet := v
		if v == "global" {
			fleet = ""
		}
		q.FleetID = &fleet
	}
	if v := r.URL.Query().Get("before"); v != "" {
		if id, err := strconv.ParseInt(v, 10, 64); err == nil {
			q.BeforeID = id
		}
	}
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			q.Limit = n
		}
	}
	out, err := s.Store.ListAuditEntries(r.Context(), q)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"entries": out})
}
