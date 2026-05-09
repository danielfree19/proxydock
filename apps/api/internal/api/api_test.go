package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/danielfree19/proxydock/apps/api/internal/auth"
	"github.com/danielfree19/proxydock/apps/api/internal/compiler"
	"github.com/danielfree19/proxydock/apps/api/internal/model"
	"github.com/danielfree19/proxydock/apps/api/internal/store/memory"
)

const (
	demoFleetID = "homelab"
	demoSecret1 = "00000000000000000000000000000001"
	demoPrefix1 = "a1a1a1a1"
	demoSecret2 = "00000000000000000000000000000002"
	demoPrefix2 = "a2a2a2a2"
)

// newTestServer wires a Server backed by the in-memory store, seeded
// with two agents and one published whoami revision (mirroring the demo).
func newTestServer(t *testing.T) (*Server, string, string) {
	t.Helper()
	st := memory.New()

	ctx := context.Background()
	if _, err := st.CreateFleet(ctx, model.Fleet{ID: demoFleetID, Name: "Homelab"}); err != nil {
		t.Fatal(err)
	}
	for _, a := range []model.Agent{
		{ID: "traefik-1", FleetID: demoFleetID, Name: "n1"},
		{ID: "traefik-2", FleetID: demoFleetID, Name: "n2"},
	} {
		if _, err := st.CreateAgent(ctx, a); err != nil {
			t.Fatal(err)
		}
	}
	tok1, h1, err := auth.FixedToken(demoPrefix1, demoSecret1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.MintToken(ctx, "traefik-1", "test", demoPrefix1, h1); err != nil {
		t.Fatal(err)
	}
	tok2, h2, err := auth.FixedToken(demoPrefix2, demoSecret2)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.MintToken(ctx, "traefik-2", "test", demoPrefix2, h2); err != nil {
		t.Fatal(err)
	}

	if _, err := st.CreateProxyHost(ctx, model.ProxyHost{
		FleetID: demoFleetID, Name: "whoami",
		Domain: "whoami.localhost", UpstreamURL: "http://whoami:80",
		EntryPoints: []string{"web"}, Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}
	hosts, err := st.ListProxyHosts(ctx, demoFleetID)
	if err != nil {
		t.Fatal(err)
	}
	res, err := compiler.Compile(hosts, nil)
	if err != nil {
		t.Fatal(err)
	}
	num, _ := st.NextRevisionNumber(ctx, demoFleetID)
	source, _ := json.Marshal(hosts)
	if _, err := st.CreateRevision(ctx, model.Revision{
		FleetID: demoFleetID, Number: num,
		CompiledConfig: res.Config, SourceProxyHosts: source, ETag: res.ETag,
	}, true); err != nil {
		t.Fatal(err)
	}

	return &Server{
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Store:  st,
	}, tok1, tok2
}

func do(t *testing.T, srv *Server, req *http.Request) *httptest.ResponseRecorder {
	t.Helper()
	rr := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rr, req)
	return rr
}

func TestHealthz(t *testing.T) {
	srv, _, _ := newTestServer(t)
	rr := do(t, srv, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
	var body map[string]string
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["status"] != "ok" {
		t.Fatalf("status field = %q", body["status"])
	}
}

func TestGetConfig_OK(t *testing.T) {
	srv, tok1, _ := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/agents/traefik-1/config", nil)
	req.Header.Set("Authorization", "Bearer "+tok1)
	rr := do(t, srv, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rr.Code, rr.Body.String())
	}
	if rr.Header().Get("ETag") == "" {
		t.Fatalf("ETag header missing")
	}
	var resp configResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.AgentID != "traefik-1" || resp.Revision != 1 {
		t.Fatalf("resp = %+v", resp)
	}
}

func TestGetConfig_MissingAuth(t *testing.T) {
	srv, _, _ := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/agents/traefik-1/config", nil)
	rr := do(t, srv, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d", rr.Code)
	}
}

func TestGetConfig_BadToken(t *testing.T) {
	srv, _, _ := newTestServer(t)
	cases := []string{
		"not-a-token",
		"tfm_aaaaaaaa_zzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzz", // bad hex
		"tfm_a1a1a1a1_99999999999999999999999999999999", // wrong secret for known prefix
	}
	for _, tok := range cases {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/agents/traefik-1/config", nil)
		req.Header.Set("Authorization", "Bearer "+tok)
		rr := do(t, srv, req)
		if rr.Code != http.StatusUnauthorized {
			t.Fatalf("token %q: status = %d", tok, rr.Code)
		}
	}
}

func TestGetConfig_TokenMismatch(t *testing.T) {
	srv, tok1, _ := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/agents/traefik-2/config", nil)
	req.Header.Set("Authorization", "Bearer "+tok1)
	rr := do(t, srv, req)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("status = %d", rr.Code)
	}
}

func TestGetConfig_IfNoneMatch(t *testing.T) {
	srv, tok1, _ := newTestServer(t)
	// First fetch to discover the ETag.
	req := httptest.NewRequest(http.MethodGet, "/api/v1/agents/traefik-1/config", nil)
	req.Header.Set("Authorization", "Bearer "+tok1)
	rr := do(t, srv, req)
	etag := rr.Header().Get("ETag")
	if etag == "" {
		t.Fatal("first request had no ETag")
	}
	// Second fetch with If-None-Match should be 304.
	req2 := httptest.NewRequest(http.MethodGet, "/api/v1/agents/traefik-1/config", nil)
	req2.Header.Set("Authorization", "Bearer "+tok1)
	req2.Header.Set("If-None-Match", etag)
	rr2 := do(t, srv, req2)
	if rr2.Code != http.StatusNotModified {
		t.Fatalf("status = %d body=%s", rr2.Code, rr2.Body.String())
	}
}

func TestHeartbeat_OK(t *testing.T) {
	srv, tok1, _ := newTestServer(t)
	body, _ := json.Marshal(map[string]any{
		"agent_id":         "traefik-1",
		"current_revision": 1,
		"provider_version": "0.1.0",
		"traefik_version":  "v3.1.0",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/agents/traefik-1/heartbeat", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+tok1)
	req.Header.Set("Content-Type", "application/json")
	rr := do(t, srv, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rr.Code, rr.Body.String())
	}
	a, err := srv.Store.GetAgent(context.Background(), "traefik-1")
	if err != nil {
		t.Fatal(err)
	}
	if a.LastRevisionSeen == nil || *a.LastRevisionSeen != 1 {
		t.Fatalf("LastRevisionSeen = %v", a.LastRevisionSeen)
	}
	if a.LastHeartbeatAt == nil {
		t.Fatalf("LastHeartbeatAt not set")
	}
}

func TestHeartbeat_BodyMismatch(t *testing.T) {
	srv, tok1, _ := newTestServer(t)
	body, _ := json.Marshal(map[string]any{
		"agent_id":         "traefik-2", // mismatch
		"current_revision": 1,
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/agents/traefik-1/heartbeat", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+tok1)
	rr := do(t, srv, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d", rr.Code)
	}
}

func TestRevokedTokenRejected(t *testing.T) {
	srv, tok1, _ := newTestServer(t)
	if err := srv.Store.RevokeToken(context.Background(), "traefik-1", demoPrefix1); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/agents/traefik-1/config", nil)
	req.Header.Set("Authorization", "Bearer "+tok1)
	rr := do(t, srv, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d body=%s", rr.Code, rr.Body.String())
	}
}
