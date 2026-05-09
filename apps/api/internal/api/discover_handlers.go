package api

import (
	"net/http"
)

// handleDiscoverServices returns upstream candidates from the
// configured discovery provider. Returns 503 when discovery is
// disabled so the UI can degrade gracefully (the Discover button
// stays hidden).
func (s *Server) handleDiscoverServices(w http.ResponseWriter, r *http.Request) {
	if s.Discovery == nil {
		writeError(w, http.StatusServiceUnavailable, "discovery is not enabled (set MANAGER_API_DISCOVERY=docker)")
		return
	}
	services, err := s.Discovery.List(r.Context())
	if err != nil {
		s.Logger.Error("discovery list", "provider", s.Discovery.Name(), "err", err)
		writeError(w, http.StatusInternalServerError, "discovery failed: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"provider": s.Discovery.Name(),
		"services": services,
	})
}
