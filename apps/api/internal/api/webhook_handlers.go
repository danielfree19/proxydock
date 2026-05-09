package api

import (
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/danielfree19/proxydock/apps/api/internal/model"
)

// Phase 7 — outgoing webhooks fired on revision lifecycle events.

type webhookInput struct {
	Name    string   `json:"name"`
	URL     string   `json:"url"`
	Secret  string   `json:"secret,omitempty"`
	Events  []string `json:"events,omitempty"`
	Enabled *bool    `json:"enabled,omitempty"`
}

func (in webhookInput) validate() error {
	if strings.TrimSpace(in.Name) == "" {
		return errors.New("name is required")
	}
	u, err := url.Parse(strings.TrimSpace(in.URL))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return errors.New("url must be an absolute http(s) URL")
	}
	if len(in.Events) == 0 {
		return errors.New("events must include at least one event")
	}
	allowed := map[string]bool{
		"revision_published":   true,
		"revision_rolled_back": true,
		"acme_certificate_issued": true,
	}
	for _, ev := range in.Events {
		if !allowed[ev] {
			return errors.New("unknown event: " + ev)
		}
	}
	return nil
}

func (in webhookInput) enabledOrDefault() bool {
	if in.Enabled == nil {
		return true
	}
	return *in.Enabled
}

func (s *Server) handleListWebhooks(w http.ResponseWriter, r *http.Request) {
	fleetID := r.PathValue("fleet_id")
	if _, err := s.Store.GetFleet(r.Context(), fleetID); err != nil {
		writeStoreError(w, err)
		return
	}
	hooks, err := s.Store.ListWebhooks(r.Context(), fleetID)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	// Strip secrets — only the HasSecret bool is exposed.
	for i := range hooks {
		hooks[i].Secret = ""
	}
	writeJSON(w, http.StatusOK, map[string]any{"webhooks": hooks})
}

func (s *Server) handleCreateWebhook(w http.ResponseWriter, r *http.Request) {
	fleetID := r.PathValue("fleet_id")
	var in webhookInput
	if !decodeBody(w, r, &in) {
		return
	}
	if err := in.validate(); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	out, err := s.Store.CreateWebhook(r.Context(), model.Webhook{
		FleetID: fleetID,
		Name:    in.Name,
		URL:     in.URL,
		Secret:  in.Secret,
		Events:  in.Events,
		Enabled: in.enabledOrDefault(),
	})
	if err != nil {
		writeStoreError(w, err)
		return
	}
	out.Secret = ""
	writeJSON(w, http.StatusCreated, out)
}

func (s *Server) handleUpdateWebhook(w http.ResponseWriter, r *http.Request) {
	fleetID := r.PathValue("fleet_id")
	id, err := parsePathID(r.PathValue("webhook_id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "webhook_id must be an integer")
		return
	}
	var in webhookInput
	if !decodeBody(w, r, &in) {
		return
	}
	if err := in.validate(); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	out, err := s.Store.UpdateWebhook(r.Context(), model.Webhook{
		ID:      id,
		FleetID: fleetID,
		Name:    in.Name,
		URL:     in.URL,
		Secret:  in.Secret,
		Events:  in.Events,
		Enabled: in.enabledOrDefault(),
	})
	if err != nil {
		writeStoreError(w, err)
		return
	}
	out.Secret = ""
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleDeleteWebhook(w http.ResponseWriter, r *http.Request) {
	fleetID := r.PathValue("fleet_id")
	id, err := parsePathID(r.PathValue("webhook_id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "webhook_id must be an integer")
		return
	}
	if err := s.Store.DeleteWebhook(r.Context(), fleetID, id); err != nil {
		writeStoreError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
