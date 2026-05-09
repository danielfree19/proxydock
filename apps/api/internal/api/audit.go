package api

import (
	"context"
	"net/http"
	"regexp"

	"github.com/danielfree19/proxydock/apps/api/internal/model"
)

// actorContextKey is the type used to thread the authorized actor's
// identity through the request context. It's a private type so other
// packages can't accidentally shadow it.
type actorContextKey struct{}

const (
	// actorBootstrap marks requests authorized via the env-bootstrap
	// token rather than a persisted admin_tokens row.
	actorBootstrap = "bootstrap"
)

// withActor returns a copy of ctx tagged with who is making the request.
func withActor(ctx context.Context, actor string) context.Context {
	if actor == "" {
		return ctx
	}
	return context.WithValue(ctx, actorContextKey{}, actor)
}

// actorFromCtx returns the actor recorded by withActor, or "" if none.
func actorFromCtx(ctx context.Context) string {
	v, _ := ctx.Value(actorContextKey{}).(string)
	return v
}

// fleetPathRE captures the fleet id from any path of the form
// /api/v1/fleets/{id}[/...]. It's deliberately a single concrete pattern
// rather than a generic resource matcher; we'd rather miss a fleet
// association on a hypothetical future endpoint than mis-attribute one.
var fleetPathRE = regexp.MustCompile(`^/api/v1/fleets/([^/]+)(?:/|$)`)

func fleetIDFromPath(p string) string {
	if m := fleetPathRE.FindStringSubmatch(p); len(m) == 2 {
		return m[1]
	}
	return ""
}

// shouldAudit returns true for the methods we want to record. Reads
// (GET, HEAD, OPTIONS) are excluded — they'd dominate the table.
func shouldAudit(method string) bool {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	default:
		return false
	}
}

// audit wraps `next` (the admin-protected handler) with a writer that,
// after the response has been sent, persists one audit_log row. The
// middleware never blocks the response: failures to persist are logged
// but don't surface to the caller.
func (s *Server) audit(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !shouldAudit(r.Method) {
			next.ServeHTTP(w, r)
			return
		}
		sr := &statusRecorder{ResponseWriter: w, status: 200}
		next.ServeHTTP(sr, r)

		actor := actorFromCtx(r.Context())
		if actor == "" {
			// Public-path mutations don't carry an actor. Skip — there
			// are no admin-mutating public paths today, but if one is
			// added later we don't want to log "" as an authorizer.
			return
		}

		entry := model.AuditEntry{
			Actor:  actor,
			Method: r.Method,
			Path:   r.URL.Path,
			Status: sr.status,
		}
		if fid := fleetIDFromPath(r.URL.Path); fid != "" {
			entry.FleetID = &fid
		}
		if err := s.Store.AppendAuditEntry(r.Context(), entry); err != nil {
			s.Logger.Warn("audit append failed",
				"err", err, "method", r.Method, "path", r.URL.Path)
		}
	})
}
