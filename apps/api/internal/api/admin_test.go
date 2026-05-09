package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/danielfree19/proxydock/apps/api/internal/store/memory"
)

// adminToken is the bootstrap token tests use to authorize admin calls.
// Real deployments rotate this away after creating persisted tokens.
const adminToken = "test-admin-bootstrap"

func newAdminServer(t *testing.T) *Server {
	t.Helper()
	st := memory.New()
	return &Server{
		Logger:              slog.New(slog.NewTextHandler(io.Discard, nil)),
		Store:               st,
		BootstrapAdminToken: adminToken,
	}
}

// adminDo is do() with the bootstrap admin Authorization header injected
// when the caller didn't already set one. Admin endpoints would otherwise
// 401 because of the auth middleware.
func adminDo(t *testing.T, srv *Server, req *http.Request) *httptest.ResponseRecorder {
	t.Helper()
	if req.Header.Get("Authorization") == "" {
		req.Header.Set("Authorization", "Bearer "+adminToken)
	}
	return do(t, srv, req)
}

func bodyJSON(t *testing.T, v any) *bytes.Reader {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return bytes.NewReader(b)
}

func TestAdminAuth_BlocksUnauthenticated(t *testing.T) {
	srv := newAdminServer(t)
	// No Authorization header.
	rr := do(t, srv, httptest.NewRequest(http.MethodGet, "/api/v1/fleets", nil))
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rr.Code)
	}
	// Wrong bootstrap token.
	req := httptest.NewRequest(http.MethodGet, "/api/v1/fleets", nil)
	req.Header.Set("Authorization", "Bearer not-the-real-token")
	rr = do(t, srv, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rr.Code)
	}
}

func TestAdminAuth_PublicPathsStillOpen(t *testing.T) {
	srv := newAdminServer(t)
	// /healthz must work without admin auth.
	rr := do(t, srv, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("/healthz blocked: %d", rr.Code)
	}
	// /api/v1/signing/pubkey too.
	rr = do(t, srv, httptest.NewRequest(http.MethodGet, "/api/v1/signing/pubkey", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("/signing/pubkey blocked: %d", rr.Code)
	}
}

func TestAdminTokenLifecycle(t *testing.T) {
	srv := newAdminServer(t)
	// Mint a real admin token via the bootstrap path.
	rr := adminDo(t, srv, httptest.NewRequest(http.MethodPost, "/api/v1/admin/tokens",
		bodyJSON(t, map[string]string{"name": "alice"})))
	if rr.Code != http.StatusCreated {
		t.Fatalf("mint admin token: %d %s", rr.Code, rr.Body.String())
	}
	var mint struct {
		Token    string `json:"token"`
		Metadata struct {
			Prefix string `json:"prefix"`
		} `json:"metadata"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &mint); err != nil {
		t.Fatal(err)
	}
	// New token authorizes admin requests.
	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/whoami", nil)
	req.Header.Set("Authorization", "Bearer "+mint.Token)
	rr = do(t, srv, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("whoami with new token: %d %s", rr.Code, rr.Body.String())
	}
	// Revoke and re-check.
	rr = adminDo(t, srv, httptest.NewRequest(http.MethodPost,
		"/api/v1/admin/tokens/"+mint.Metadata.Prefix+"/revoke", nil))
	if rr.Code != http.StatusNoContent {
		t.Fatalf("revoke: %d", rr.Code)
	}
	req2 := httptest.NewRequest(http.MethodGet, "/api/v1/admin/whoami", nil)
	req2.Header.Set("Authorization", "Bearer "+mint.Token)
	rr = do(t, srv, req2)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("revoked token still authorized: %d", rr.Code)
	}
}

func TestAuditLog_RecordsMutationsOnly(t *testing.T) {
	srv := newAdminServer(t)
	mustCreateFleet(t, srv, "homelab")
	// A few mutations.
	mustCreateProxyHost(t, srv, "homelab", map[string]any{
		"name": "whoami", "domain": "whoami.localhost", "upstream_url": "http://whoami:80",
	})
	mustPublish(t, srv, "homelab")
	// And a read that should NOT be audited.
	rr := adminDo(t, srv, httptest.NewRequest(http.MethodGet,
		"/api/v1/fleets/homelab/proxy_hosts", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("read failed: %d", rr.Code)
	}

	rr = adminDo(t, srv, httptest.NewRequest(http.MethodGet,
		"/api/v1/admin/audit", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("audit list: %d %s", rr.Code, rr.Body.String())
	}
	var resp struct {
		Entries []struct {
			Actor   string  `json:"actor"`
			Method  string  `json:"method"`
			Path    string  `json:"path"`
			Status  int     `json:"status"`
			FleetID *string `json:"fleet_id"`
		} `json:"entries"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	// Expect exactly the three mutating calls (POST fleet, POST proxy host,
	// POST revisions) — the GET read above must not appear.
	if len(resp.Entries) != 3 {
		t.Fatalf("got %d entries, want 3:\n%+v", len(resp.Entries), resp.Entries)
	}
	for _, e := range resp.Entries {
		if e.Method == http.MethodGet {
			t.Fatalf("read leaked into audit log: %+v", e)
		}
		if e.Actor != "bootstrap" {
			t.Fatalf("unexpected actor %q", e.Actor)
		}
	}
	// The proxy_host + publish entries should carry fleet_id; the
	// /api/v1/fleets entry should not (it's the create-fleet call).
	found := map[string]int{}
	for _, e := range resp.Entries {
		key := e.Path
		if e.FleetID != nil {
			key = "fleet:" + key
		}
		found[key]++
	}
	if found["/api/v1/fleets"] != 1 {
		t.Fatalf("missing /api/v1/fleets entry without fleet_id: %v", found)
	}
	if found["fleet:/api/v1/fleets/homelab/proxy_hosts"] != 1 {
		t.Fatalf("missing fleet-tagged proxy_hosts entry: %v", found)
	}
	if found["fleet:/api/v1/fleets/homelab/revisions"] != 1 {
		t.Fatalf("missing fleet-tagged revisions entry: %v", found)
	}
}

func TestAuditLog_NoActorOnUnauthorized(t *testing.T) {
	srv := newAdminServer(t)
	// Unauthenticated mutation: must be rejected AND not appear in the log.
	req := httptest.NewRequest(http.MethodPost, "/api/v1/fleets",
		bodyJSON(t, map[string]string{"id": "x", "name": "X"}))
	rr := do(t, srv, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rr.Code)
	}
	rr = adminDo(t, srv, httptest.NewRequest(http.MethodGet, "/api/v1/admin/audit", nil))
	var resp struct {
		Entries []any `json:"entries"`
	}
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	if len(resp.Entries) != 0 {
		t.Fatalf("unauthorized request appeared in audit log: %+v", resp.Entries)
	}
}

func TestPerAgentLabelTargeting(t *testing.T) {
	srv := newAdminServer(t)
	mustCreateFleet(t, srv, "homelab")
	mustCreateAgent(t, srv, "homelab", "us-agent")
	mustCreateAgent(t, srv, "homelab", "eu-agent")

	// Tag the two agents differently.
	mustUpdateLabels(t, srv, "us-agent", []string{"region=us"})
	mustUpdateLabels(t, srv, "eu-agent", []string{"region=eu"})

	// One US-only host, one EU-only host, one global host.
	mustCreateProxyHost(t, srv, "homelab", map[string]any{
		"name": "us-only", "domain": "us.localhost", "upstream_url": "http://x:80",
		"label_selector": "region=us",
	})
	mustCreateProxyHost(t, srv, "homelab", map[string]any{
		"name": "eu-only", "domain": "eu.localhost", "upstream_url": "http://x:80",
		"label_selector": "region=eu",
	})
	mustCreateProxyHost(t, srv, "homelab", map[string]any{
		"name": "global", "domain": "global.localhost", "upstream_url": "http://x:80",
	})

	mustPublish(t, srv, "homelab")

	// Mint agent tokens so we can hit /config as each one.
	usTok := mustMintAgentToken(t, srv, "us-agent")
	euTok := mustMintAgentToken(t, srv, "eu-agent")

	usCfg := getAgentConfig(t, srv, "us-agent", usTok)
	euCfg := getAgentConfig(t, srv, "eu-agent", euTok)

	if !cfgHasRouter(t, usCfg, "us-only") || cfgHasRouter(t, usCfg, "eu-only") {
		t.Fatalf("us-agent saw wrong routers: %s", usCfg)
	}
	if !cfgHasRouter(t, usCfg, "global") {
		t.Fatalf("us-agent missing global router: %s", usCfg)
	}
	if cfgHasRouter(t, euCfg, "us-only") || !cfgHasRouter(t, euCfg, "eu-only") {
		t.Fatalf("eu-agent saw wrong routers: %s", euCfg)
	}
}

func mustUpdateLabels(t *testing.T, srv *Server, agentID string, labels []string) {
	t.Helper()
	rr := adminDo(t, srv, httptest.NewRequest(http.MethodPut,
		"/api/v1/agents/"+agentID+"/labels",
		bodyJSON(t, map[string]any{"labels": labels})))
	if rr.Code != http.StatusOK {
		t.Fatalf("update labels (%s): %d %s", agentID, rr.Code, rr.Body.String())
	}
}

func mustMintAgentToken(t *testing.T, srv *Server, agentID string) string {
	t.Helper()
	rr := adminDo(t, srv, httptest.NewRequest(http.MethodPost,
		"/api/v1/agents/"+agentID+"/tokens", bodyJSON(t, map[string]string{"name": "t"})))
	if rr.Code != http.StatusCreated {
		t.Fatalf("mint agent token: %d %s", rr.Code, rr.Body.String())
	}
	var resp struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	return resp.Token
}

func getAgentConfig(t *testing.T, srv *Server, agentID, token string) []byte {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/agents/"+agentID+"/config", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rr := do(t, srv, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("get config (%s): %d %s", agentID, rr.Code, rr.Body.String())
	}
	return rr.Body.Bytes()
}

func cfgHasRouter(t *testing.T, body []byte, routerName string) bool {
	t.Helper()
	var resp struct {
		Config struct {
			HTTP struct {
				Routers map[string]any `json:"routers"`
			} `json:"http"`
		} `json:"config"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatal(err)
	}
	_, ok := resp.Config.HTTP.Routers[routerName]
	return ok
}

func TestFleetsCRUD(t *testing.T) {
	srv := newAdminServer(t)

	// Create
	rr := adminDo(t, srv, httptest.NewRequest(http.MethodPost, "/api/v1/fleets",
		bodyJSON(t, map[string]string{"id": "homelab", "name": "Homelab"})))
	if rr.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rr.Code, rr.Body.String())
	}

	// Conflict
	rr = adminDo(t, srv, httptest.NewRequest(http.MethodPost, "/api/v1/fleets",
		bodyJSON(t, map[string]string{"id": "homelab", "name": "Other"})))
	if rr.Code != http.StatusConflict {
		t.Fatalf("conflict: %d", rr.Code)
	}

	// Get
	rr = adminDo(t, srv, httptest.NewRequest(http.MethodGet, "/api/v1/fleets/homelab", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("get: %d", rr.Code)
	}

	// List
	rr = adminDo(t, srv, httptest.NewRequest(http.MethodGet, "/api/v1/fleets", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("list: %d", rr.Code)
	}

	// Delete
	rr = adminDo(t, srv, httptest.NewRequest(http.MethodDelete, "/api/v1/fleets/homelab", nil))
	if rr.Code != http.StatusNoContent {
		t.Fatalf("delete: %d", rr.Code)
	}

	rr = adminDo(t, srv, httptest.NewRequest(http.MethodGet, "/api/v1/fleets/homelab", nil))
	if rr.Code != http.StatusNotFound {
		t.Fatalf("get after delete: %d", rr.Code)
	}
}

func TestProxyHostCRUDAndPublish(t *testing.T) {
	srv := newAdminServer(t)
	mustCreateFleet(t, srv, "homelab")

	// Create a proxy host
	rr := adminDo(t, srv, httptest.NewRequest(http.MethodPost, "/api/v1/fleets/homelab/proxy_hosts",
		bodyJSON(t, map[string]any{
			"name": "whoami", "domain": "whoami.localhost", "upstream_url": "http://whoami:80",
		})))
	if rr.Code != http.StatusCreated {
		t.Fatalf("create proxy host: %d %s", rr.Code, rr.Body.String())
	}

	// Publish
	rr = adminDo(t, srv, httptest.NewRequest(http.MethodPost, "/api/v1/fleets/homelab/revisions",
		bodyJSON(t, map[string]string{"notes": "first"})))
	if rr.Code != http.StatusCreated {
		t.Fatalf("publish: %d %s", rr.Code, rr.Body.String())
	}

	// Confirm a revision exists
	rr = adminDo(t, srv, httptest.NewRequest(http.MethodGet, "/api/v1/fleets/homelab/revisions", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("list revisions: %d", rr.Code)
	}
	var listResp struct {
		Revisions []struct {
			Number int `json:"number"`
		} `json:"revisions"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &listResp); err != nil {
		t.Fatal(err)
	}
	if len(listResp.Revisions) != 1 || listResp.Revisions[0].Number != 1 {
		t.Fatalf("revisions = %+v", listResp)
	}
}

func TestPublish_RejectsInvalidProxyHost(t *testing.T) {
	srv := newAdminServer(t)
	mustCreateFleet(t, srv, "homelab")

	// Create a proxy host with a bogus upstream scheme.
	rr := adminDo(t, srv, httptest.NewRequest(http.MethodPost, "/api/v1/fleets/homelab/proxy_hosts",
		bodyJSON(t, map[string]any{
			"name": "bad", "domain": "bad.localhost", "upstream_url": "ftp://x",
		})))
	if rr.Code != http.StatusCreated {
		t.Fatalf("create: %d", rr.Code)
	}

	rr = adminDo(t, srv, httptest.NewRequest(http.MethodPost, "/api/v1/fleets/homelab/revisions", nil))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("publish should be 400: %d %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "scheme") {
		t.Fatalf("expected scheme error, got: %s", rr.Body.String())
	}
}

func TestRollback_CreatesNewRevisionWithSameConfig(t *testing.T) {
	srv := newAdminServer(t)
	mustCreateFleet(t, srv, "homelab")

	// Create proxy host #1, publish (rev 1)
	mustCreateProxyHost(t, srv, "homelab", map[string]any{
		"name": "whoami", "domain": "whoami.localhost", "upstream_url": "http://whoami:80",
	})
	mustPublish(t, srv, "homelab")

	// Add proxy host #2, publish (rev 2)
	mustCreateProxyHost(t, srv, "homelab", map[string]any{
		"name": "api", "domain": "api.localhost", "upstream_url": "http://api:80",
	})
	mustPublish(t, srv, "homelab")

	// Rollback to rev 1
	rr := adminDo(t, srv, httptest.NewRequest(http.MethodPost,
		"/api/v1/fleets/homelab/revisions/1/rollback", nil))
	if rr.Code != http.StatusCreated {
		t.Fatalf("rollback: %d %s", rr.Code, rr.Body.String())
	}
	var rollback struct {
		Number         int             `json:"number"`
		CompiledConfig json.RawMessage `json:"compiled_config"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &rollback); err != nil {
		t.Fatal(err)
	}
	if rollback.Number != 3 {
		t.Fatalf("rollback should produce a new (third) revision number, got %d", rollback.Number)
	}

	// The published revision should now equal what rev 1 had.
	rev1, err := srv.Store.GetRevision(context.Background(), "homelab", 1)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(rev1.CompiledConfig, rollback.CompiledConfig) {
		t.Fatalf("rollback config differs from rev1")
	}
}

func TestTokenLifecycle(t *testing.T) {
	srv := newAdminServer(t)
	mustCreateFleet(t, srv, "homelab")
	mustCreateAgent(t, srv, "homelab", "traefik-1")

	// Mint
	rr := adminDo(t, srv, httptest.NewRequest(http.MethodPost, "/api/v1/agents/traefik-1/tokens",
		bodyJSON(t, map[string]string{"name": "demo"})))
	if rr.Code != http.StatusCreated {
		t.Fatalf("mint: %d %s", rr.Code, rr.Body.String())
	}
	var mint struct {
		Token    string `json:"token"`
		Metadata struct {
			Prefix string `json:"prefix"`
		} `json:"metadata"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &mint); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(mint.Token, "tfm_") || mint.Metadata.Prefix == "" {
		t.Fatalf("bad mint response: %+v", mint)
	}

	// List
	rr = adminDo(t, srv, httptest.NewRequest(http.MethodGet, "/api/v1/agents/traefik-1/tokens", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("list tokens: %d", rr.Code)
	}

	// Revoke
	rr = adminDo(t, srv, httptest.NewRequest(http.MethodPost,
		"/api/v1/agents/traefik-1/tokens/"+mint.Metadata.Prefix+"/revoke", nil))
	if rr.Code != http.StatusNoContent {
		t.Fatalf("revoke: %d", rr.Code)
	}

	// Token should now fail config fetch.
	cfgReq := httptest.NewRequest(http.MethodGet, "/api/v1/agents/traefik-1/config", nil)
	cfgReq.Header.Set("Authorization", "Bearer "+mint.Token)
	rr = adminDo(t, srv, cfgReq)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("revoked token still works: %d", rr.Code)
	}
}

// --- helpers ---

func mustCreateFleet(t *testing.T, srv *Server, id string) {
	t.Helper()
	rr := adminDo(t, srv, httptest.NewRequest(http.MethodPost, "/api/v1/fleets",
		bodyJSON(t, map[string]string{"id": id, "name": id})))
	if rr.Code != http.StatusCreated {
		t.Fatalf("create fleet %s: %d %s", id, rr.Code, rr.Body.String())
	}
}

func mustCreateAgent(t *testing.T, srv *Server, fleetID, agentID string) {
	t.Helper()
	rr := adminDo(t, srv, httptest.NewRequest(http.MethodPost,
		"/api/v1/fleets/"+fleetID+"/agents",
		bodyJSON(t, map[string]string{"id": agentID, "name": agentID})))
	if rr.Code != http.StatusCreated {
		t.Fatalf("create agent %s: %d %s", agentID, rr.Code, rr.Body.String())
	}
}

func mustCreateProxyHost(t *testing.T, srv *Server, fleetID string, body map[string]any) {
	t.Helper()
	rr := adminDo(t, srv, httptest.NewRequest(http.MethodPost,
		"/api/v1/fleets/"+fleetID+"/proxy_hosts", bodyJSON(t, body)))
	if rr.Code != http.StatusCreated {
		t.Fatalf("create proxy host: %d %s", rr.Code, rr.Body.String())
	}
}

func mustPublish(t *testing.T, srv *Server, fleetID string) {
	t.Helper()
	rr := adminDo(t, srv, httptest.NewRequest(http.MethodPost,
		"/api/v1/fleets/"+fleetID+"/revisions", nil))
	if rr.Code != http.StatusCreated {
		t.Fatalf("publish: %d %s", rr.Code, rr.Body.String())
	}
}
