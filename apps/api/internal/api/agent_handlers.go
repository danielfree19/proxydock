package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/danielfree19/proxydock/apps/api/internal/auth"
	"github.com/danielfree19/proxydock/apps/api/internal/compiler"
	"github.com/danielfree19/proxydock/apps/api/internal/cryptokit"
	"github.com/danielfree19/proxydock/apps/api/internal/labels"
	"github.com/danielfree19/proxydock/apps/api/internal/model"
	"github.com/danielfree19/proxydock/apps/api/internal/store"
)

// configResponse is the body returned for GET /config. The provider
// plugin decodes exactly this shape.
type configResponse struct {
	FleetID      string          `json:"fleet_id"`
	AgentID      string          `json:"agent_id"`
	Revision     int             `json:"revision"`
	ETag         string          `json:"etag"`
	GeneratedAt  time.Time       `json:"generated_at"`
	Config       json.RawMessage `json:"config"`
	Signature    string          `json:"signature,omitempty"`
	SignatureAlg string          `json:"signature_alg,omitempty"`
}

func (s *Server) handleGetConfig(w http.ResponseWriter, r *http.Request) {
	urlAgentID := r.PathValue("agent_id")
	rec, err := s.authenticateAgent(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, err.Error())
		return
	}
	if rec.Agent.ID != urlAgentID {
		s.Logger.Warn("agent_id mismatch",
			"url_agent_id", urlAgentID, "token_agent_id", rec.Agent.ID)
		writeError(w, http.StatusForbidden, "token does not match agent_id")
		return
	}

	rev, err := s.Store.GetPublishedRevision(r.Context(), rec.FleetID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "no published revision for fleet")
			return
		}
		s.Logger.Error("get published revision", "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	// Reload the agent's full state so we have its current label set.
	// The token-lookup version of rec.Agent may be stale on agents whose
	// labels were just edited; touch is best-effort but a fresh GetAgent
	// is cheap and lets the handler use exact bytes.
	agent, err := s.Store.GetAgent(r.Context(), rec.Agent.ID)
	if err != nil {
		s.Logger.Error("get agent", "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	resp, err := s.buildAgentConfigResponse(agent, rev)
	if err != nil {
		s.Logger.Error("compile per-agent config",
			"agent_id", agent.ID, "fleet_id", agent.FleetID, "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	w.Header().Set("ETag", resp.ETag)
	if match := r.Header.Get("If-None-Match"); match != "" && etagMatches(match, resp.ETag) {
		s.Logger.Info("config not modified",
			"agent_id", agent.ID, "fleet_id", agent.FleetID, "revision", rev.Number)
		w.WriteHeader(http.StatusNotModified)
		return
	}

	s.Logger.Info("config served",
		"agent_id", agent.ID, "fleet_id", agent.FleetID,
		"revision", rev.Number, "etag", resp.ETag)
	writeJSON(w, http.StatusOK, resp)
}

// buildAgentConfigResponse runs the per-agent compile path: filter the
// revision's snapshotted proxy hosts by the agent's label set, recompile
// against the snapshotted certs, and (re-)sign the result. Falls back to
// the revision's already-compiled bytes for legacy revisions that don't
// carry a SourceProxyHosts snapshot — keeps demo databases from before
// migration 006 working without a re-publish.
func (s *Server) buildAgentConfigResponse(agent model.Agent, rev model.Revision) (configResponse, error) {
	resp := configResponse{
		FleetID:      rev.FleetID,
		AgentID:      agent.ID,
		Revision:     rev.Number,
		GeneratedAt:  rev.GeneratedAt,
		Config:       rev.CompiledConfig,
		ETag:         rev.ETag,
		Signature:    rev.Signature,
		SignatureAlg: rev.SignatureAlg,
	}

	if len(rev.SourceProxyHosts) == 0 || string(rev.SourceProxyHosts) == "[]" {
		// No source snapshot to filter on; agents see the canonical
		// compiled bytes.
		return resp, nil
	}

	var hosts []model.ProxyHost
	if err := json.Unmarshal(rev.SourceProxyHosts, &hosts); err != nil {
		return configResponse{}, err
	}

	filtered := make([]model.ProxyHost, 0, len(hosts))
	for _, h := range hosts {
		sel, err := labels.Parse(h.LabelSelector)
		if err != nil {
			// Selector failed validation at publish time too; surface
			// the row in the canonical compile but skip it for this
			// agent so the bug doesn't propagate silently.
			continue
		}
		if !sel.Matches(agent.Labels) {
			continue
		}
		filtered = append(filtered, h)
	}

	var certs []model.Certificate
	if len(rev.SourceCerts) > 0 {
		if err := json.Unmarshal(rev.SourceCerts, &certs); err != nil {
			return configResponse{}, err
		}
	}

	res, err := compiler.Compile(filtered, certs)
	if err != nil {
		return configResponse{}, err
	}
	resp.Config = res.Config
	resp.ETag = res.ETag
	if s.Signer != nil {
		resp.Signature = s.Signer.Sign(res.Config)
		resp.SignatureAlg = cryptokit.SignatureAlg
	} else {
		// No signer means we shouldn't keep the published-revision
		// signature either — its bytes are different from these.
		resp.Signature = ""
		resp.SignatureAlg = ""
	}
	return resp, nil
}

// handleAgentConfigPreview is the admin-authenticated read of what a
// given agent receives from /config — same compile path, same bytes,
// but skips the agent-token check so operators can inspect routes,
// services, and middlewares per agent from the web UI without
// possessing the agent's own token.
//
// The route deliberately does not end in `/config` because that suffix
// is on isPublicPath's allow-list for the agent-token endpoint.
func (s *Server) handleAgentConfigPreview(w http.ResponseWriter, r *http.Request) {
	agentID := r.PathValue("agent_id")
	agent, err := s.Store.GetAgent(r.Context(), agentID)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	rev, err := s.Store.GetPublishedRevision(r.Context(), agent.FleetID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "no published revision for fleet")
			return
		}
		s.Logger.Error("get published revision", "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	resp, err := s.buildAgentConfigResponse(agent, rev)
	if err != nil {
		s.Logger.Error("compile per-agent config (preview)",
			"agent_id", agent.ID, "fleet_id", agent.FleetID, "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleHeartbeat(w http.ResponseWriter, r *http.Request) {
	urlAgentID := r.PathValue("agent_id")
	rec, err := s.authenticateAgent(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, err.Error())
		return
	}
	if rec.Agent.ID != urlAgentID {
		writeError(w, http.StatusForbidden, "token does not match agent_id")
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<16))
	if err != nil {
		writeError(w, http.StatusBadRequest, "could not read body")
		return
	}
	var payload struct {
		AgentID         string `json:"agent_id"`
		CurrentRevision int    `json:"current_revision"`
		ProviderVersion string `json:"provider_version"`
		TraefikVersion  string `json:"traefik_version"`
		LastError       string `json:"last_error"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if payload.AgentID != "" && payload.AgentID != rec.Agent.ID {
		writeError(w, http.StatusBadRequest, "heartbeat agent_id mismatch")
		return
	}
	hb := store.HeartbeatUpdate{
		CurrentRevision: payload.CurrentRevision,
		ProviderVersion: payload.ProviderVersion,
		TraefikVersion:  payload.TraefikVersion,
		LastError:       payload.LastError,
	}
	if err := s.Store.UpdateAgentHeartbeat(r.Context(), rec.Agent.ID, hb); err != nil {
		writeStoreError(w, err)
		return
	}
	if s.Metrics != nil {
		s.Metrics.HeartbeatsReceived.WithLabelValues(rec.FleetID, rec.Agent.ID).Inc()
	}
	s.Logger.Info("heartbeat received",
		"agent_id", rec.Agent.ID,
		"current_revision", hb.CurrentRevision,
		"provider_version", hb.ProviderVersion,
		"traefik_version", hb.TraefikVersion,
		"last_error", hb.LastError)
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// authenticateAgent extracts the bearer token, looks it up, verifies the
// secret in constant time, and (best-effort) updates last_used_at.
//
// Returned errors are safe to send to the client; we do not distinguish
// "no such prefix" from "wrong secret" in the public message so the
// endpoint cannot be used to enumerate valid prefixes.
func (s *Server) authenticateAgent(r *http.Request) (store.TokenRecord, error) {
	tok, err := bearerHeader(r)
	if err != nil {
		return store.TokenRecord{}, errors.New("invalid or missing bearer token")
	}
	prefix, secret, err := auth.ParseBearer(tok)
	if err != nil {
		return store.TokenRecord{}, errors.New("invalid or missing bearer token")
	}
	rec, hash, err := s.Store.LookupToken(r.Context(), prefix)
	if err != nil {
		return store.TokenRecord{}, errors.New("invalid or missing bearer token")
	}
	if !auth.VerifySecret(secret, hash) {
		return store.TokenRecord{}, errors.New("invalid or missing bearer token")
	}
	// Touch is best-effort; failure here doesn't block the request. Log
	// the error for operators but do not surface it.
	if err := s.Store.TouchToken(r.Context(), prefix); err != nil {
		s.Logger.Warn("touch token", "err", err, "prefix", prefix)
	}
	return rec, nil
}
