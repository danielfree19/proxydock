package api

import (
	"errors"
	"net/http"
	"strings"

	"github.com/danielfree19/proxydock/apps/api/internal/compiler"
	"github.com/danielfree19/proxydock/apps/api/internal/model"
)

// Phase 7 — middleware library.
//
// Templates are fleet-scoped and reusable across proxy hosts. Apply-by-
// copy semantics: a host's middlewares array is a deep copy of the
// template's at apply time. Editing the template later does not mutate
// hosts that already applied it. The proxy host UI and API always own
// the host's authoritative middlewares list.

type middlewareTemplateInput struct {
	Name        string             `json:"name"`
	Description string             `json:"description,omitempty"`
	Middlewares []model.Middleware `json:"middlewares"`
}

func (in middlewareTemplateInput) validate() error {
	if strings.TrimSpace(in.Name) == "" {
		return errors.New("name is required")
	}
	// Middlewares must pass the same shape checks the proxy-host
	// compiler runs; otherwise applying the template later would land
	// the host in a non-compilable state.
	if err := compiler.ValidateMiddlewares(in.Middlewares); err != nil {
		return err
	}
	return nil
}

func (s *Server) handleListMiddlewareTemplates(w http.ResponseWriter, r *http.Request) {
	fleetID := r.PathValue("fleet_id")
	if _, err := s.Store.GetFleet(r.Context(), fleetID); err != nil {
		writeStoreError(w, err)
		return
	}
	ts, err := s.Store.ListMiddlewareTemplates(r.Context(), fleetID)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"middleware_templates": ts})
}

func (s *Server) handleCreateMiddlewareTemplate(w http.ResponseWriter, r *http.Request) {
	fleetID := r.PathValue("fleet_id")
	var in middlewareTemplateInput
	if !decodeBody(w, r, &in) {
		return
	}
	if err := in.validate(); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	out, err := s.Store.CreateMiddlewareTemplate(r.Context(), model.MiddlewareTemplate{
		FleetID:     fleetID,
		Name:        in.Name,
		Description: in.Description,
		Middlewares: in.Middlewares,
	})
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, out)
}

func (s *Server) handleGetMiddlewareTemplate(w http.ResponseWriter, r *http.Request) {
	fleetID := r.PathValue("fleet_id")
	id, err := parsePathID(r.PathValue("tpl_id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "tpl_id must be an integer")
		return
	}
	t, err := s.Store.GetMiddlewareTemplate(r.Context(), fleetID, id)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, t)
}

func (s *Server) handleUpdateMiddlewareTemplate(w http.ResponseWriter, r *http.Request) {
	fleetID := r.PathValue("fleet_id")
	id, err := parsePathID(r.PathValue("tpl_id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "tpl_id must be an integer")
		return
	}
	var in middlewareTemplateInput
	if !decodeBody(w, r, &in) {
		return
	}
	if err := in.validate(); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	out, err := s.Store.UpdateMiddlewareTemplate(r.Context(), model.MiddlewareTemplate{
		ID:          id,
		FleetID:     fleetID,
		Name:        in.Name,
		Description: in.Description,
		Middlewares: in.Middlewares,
	})
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleDeleteMiddlewareTemplate(w http.ResponseWriter, r *http.Request) {
	fleetID := r.PathValue("fleet_id")
	id, err := parsePathID(r.PathValue("tpl_id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "tpl_id must be an integer")
		return
	}
	if err := s.Store.DeleteMiddlewareTemplate(r.Context(), fleetID, id); err != nil {
		writeStoreError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
