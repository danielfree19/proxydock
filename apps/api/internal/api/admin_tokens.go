package api

import (
	"net/http"

	"github.com/danielfree19/proxydock/apps/api/internal/auth"
)

// handleListAdminTokens returns metadata for every admin token,
// including revoked ones (so operators can audit rotation).
func (s *Server) handleListAdminTokens(w http.ResponseWriter, r *http.Request) {
	out, err := s.Store.ListAdminTokens(r.Context())
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"admin_tokens": out})
}

// handleMintAdminToken issues a new admin token; the plaintext is
// returned exactly once.
func (s *Server) handleMintAdminToken(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name string `json:"name"`
	}
	_ = decodeBody(w, r, &in)

	token, prefix, hash, err := auth.MintToken()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not mint token")
		return
	}
	rec, err := s.Store.MintAdminToken(r.Context(), in.Name, prefix, hash)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"token":    token,
		"metadata": rec,
	})
}

// handleRevokeAdminToken marks an admin token as revoked.
//
// Operators should always have at least one other working admin token
// before revoking the one they're holding — there's no recovery path
// from "all admin tokens revoked, bootstrap token removed".
func (s *Server) handleRevokeAdminToken(w http.ResponseWriter, r *http.Request) {
	prefix := r.PathValue("prefix")
	if err := s.Store.RevokeAdminToken(r.Context(), prefix); err != nil {
		writeStoreError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleAdminWhoami is used by the web UI's login page to verify a
// candidate token before persisting it. Returns 200 if the request
// passed the admin auth middleware (a no-op handler is enough).
func (s *Server) handleAdminWhoami(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
