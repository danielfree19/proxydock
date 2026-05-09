package compiler

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/danielfree19/proxydock/apps/api/internal/model"
)

func TestCompile_Golden(t *testing.T) {
	cases := []struct {
		name string
	}{
		{"simple"},
		{"multi"},
		{"middleware"},
		{"tcp_udp"},
		{"forwardauth"},
		{"extras_middleware"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			inputBytes := readFile(t, filepath.Join("testdata", tc.name+"_input.json"))
			expectedBytes := readFile(t, filepath.Join("testdata", tc.name+"_output.json"))

			var hosts []model.ProxyHost
			if err := json.Unmarshal(inputBytes, &hosts); err != nil {
				t.Fatalf("decode input: %v", err)
			}

			res, err := Compile(hosts, nil)
			if err != nil {
				t.Fatalf("Compile: %v", err)
			}

			// Compare semantically (re-marshal expected through Go to normalize)
			// so we don't fail on whitespace/key-order differences in the
			// fixture file authored by hand.
			gotNorm := normalize(t, res.Config)
			wantNorm := normalize(t, expectedBytes)
			if !bytes.Equal(gotNorm, wantNorm) {
				t.Fatalf("compiled config differs from golden\n--- want ---\n%s\n--- got ---\n%s",
					wantNorm, gotNorm)
			}
		})
	}
}

func TestCompile_Deterministic(t *testing.T) {
	hosts := []model.ProxyHost{
		{Name: "z-host", Domain: "z.example.com", UpstreamURL: "http://z:80",
			EntryPoints: []string{"web"}, Enabled: true},
		{Name: "a-host", Domain: "a.example.com", UpstreamURL: "http://a:80",
			EntryPoints: []string{"web"}, Enabled: true},
	}
	r1, err := Compile(hosts, nil)
	if err != nil {
		t.Fatal(err)
	}
	// Reverse the input order; output must be byte-identical.
	rev := make([]model.ProxyHost, len(hosts))
	for i, h := range hosts {
		rev[len(hosts)-1-i] = h
	}
	r2, err := Compile(rev, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(r1.Config, r2.Config) {
		t.Fatalf("non-deterministic output\nfirst:  %s\nsecond: %s", r1.Config, r2.Config)
	}
	if r1.ETag != r2.ETag {
		t.Fatalf("ETag drifts: %s vs %s", r1.ETag, r2.ETag)
	}
}

func TestCompile_TLSRouterAndPool(t *testing.T) {
	hosts := []model.ProxyHost{
		{Name: "secure", Domain: "secure.localhost", UpstreamURL: "http://x:80",
			EntryPoints: []string{"websecure"}, TLS: true, Enabled: true},
	}
	certs := []model.Certificate{
		{Name: "wildcard", FleetID: "homelab",
			CertPEM: "PEM-CERT", KeyPEM: "PEM-KEY",
			DNSNames: []string{"*.localhost"}},
	}
	res, err := Compile(hosts, certs)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(res.Config, &got); err != nil {
		t.Fatal(err)
	}
	router := got["http"].(map[string]any)["routers"].(map[string]any)["secure"].(map[string]any)
	if _, ok := router["tls"]; !ok {
		t.Fatalf("router missing tls: %+v", router)
	}
	pool := got["tls"].(map[string]any)["certificates"].([]any)
	if len(pool) != 1 {
		t.Fatalf("expected 1 cert in pool, got %d", len(pool))
	}
	first := pool[0].(map[string]any)
	if first["certFile"] != "PEM-CERT" || first["keyFile"] != "PEM-KEY" {
		t.Fatalf("cert pool entry has wrong shape: %+v", first)
	}
}

func TestCompile_NoCertsNoTLSSection(t *testing.T) {
	hosts := []model.ProxyHost{
		{Name: "h", Domain: "x.localhost", UpstreamURL: "http://x:80",
			EntryPoints: []string{"web"}, Enabled: true},
	}
	res, err := Compile(hosts, nil)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(res.Config, &got); err != nil {
		t.Fatal(err)
	}
	if _, present := got["tls"]; present {
		t.Fatalf("tls section should be absent when no certs are present: %s", res.Config)
	}
}

func TestCompile_DroppedDisabled(t *testing.T) {
	hosts := []model.ProxyHost{
		{Name: "draft", Domain: "draft.localhost", UpstreamURL: "http://x:80",
			EntryPoints: []string{"web"}, Enabled: false},
	}
	res, err := Compile(hosts, nil)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(res.Config, &got); err != nil {
		t.Fatal(err)
	}
	http := got["http"].(map[string]any)
	if r := http["routers"].(map[string]any); len(r) != 0 {
		t.Fatalf("expected zero routers from a fleet with only disabled hosts, got %v", r)
	}
}

func TestValidate_DuplicateDomain(t *testing.T) {
	err := Validate([]model.ProxyHost{
		{Name: "a", Domain: "x.example.com", UpstreamURL: "http://x", Enabled: true},
		{Name: "b", Domain: "x.example.com", UpstreamURL: "http://y", Enabled: true},
	})
	if err == nil || !strings.Contains(err.Error(), "already used") {
		t.Fatalf("expected duplicate-domain error, got %v", err)
	}
}

func TestValidate_DuplicateName(t *testing.T) {
	err := Validate([]model.ProxyHost{
		{Name: "a", Domain: "x.example.com", UpstreamURL: "http://x", Enabled: true},
		{Name: "a", Domain: "y.example.com", UpstreamURL: "http://y", Enabled: true},
	})
	if err == nil || !strings.Contains(err.Error(), "duplicate proxy host name") {
		t.Fatalf("expected duplicate-name error, got %v", err)
	}
}

func TestValidate_BadUpstream(t *testing.T) {
	cases := []struct {
		name   string
		url    string
		expect string
	}{
		{"empty", "", "required"},
		{"bad scheme", "ftp://x", "scheme"},
		{"no host", "http://", "missing host"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := Validate([]model.ProxyHost{{
				Name: "h", Domain: "x.example.com", UpstreamURL: tc.url, Enabled: true,
			}})
			if err == nil || !strings.Contains(err.Error(), tc.expect) {
				t.Fatalf("err=%v want substring %q", err, tc.expect)
			}
		})
	}
}

func TestValidate_BadDomain(t *testing.T) {
	err := Validate([]model.ProxyHost{
		{Name: "a", Domain: "has spaces", UpstreamURL: "http://x", Enabled: true},
	})
	if err == nil || !strings.Contains(err.Error(), "invalid domain") {
		t.Fatalf("err=%v", err)
	}
}

func TestValidate_DisabledNotChecked(t *testing.T) {
	// Disabled hosts skip domain/upstream validation, so users can save
	// drafts before they're complete.
	err := Validate([]model.ProxyHost{
		{Name: "draft", Domain: "", UpstreamURL: "", Enabled: false},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidate_UnsupportedMiddleware(t *testing.T) {
	err := Validate([]model.ProxyHost{
		{Name: "a", Domain: "x.example.com", UpstreamURL: "http://x", Enabled: true,
			Middlewares: []model.Middleware{{Name: "x", Type: "buffering"}}},
	})
	if err == nil || !strings.Contains(err.Error(), "unsupported middleware type") {
		t.Fatalf("err=%v", err)
	}
}

func TestValidate_TCPUpstreamMustBeHostPort(t *testing.T) {
	err := Validate([]model.ProxyHost{{
		Name: "x", Protocol: "tcp", Domain: "x.example.com",
		UpstreamURL: "http://x:80", Enabled: true,
	}})
	if err == nil || !strings.Contains(err.Error(), "host:port") {
		t.Fatalf("err=%v", err)
	}
}

func TestValidate_UDPNeedsEntryPointAndNoTLS(t *testing.T) {
	err := Validate([]model.ProxyHost{{
		Name: "u", Protocol: "udp",
		UpstreamURL: "x:53", TLS: true, Enabled: true,
	}})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "UDP routers cannot use TLS") ||
		!strings.Contains(err.Error(), "require at least one entry_point") {
		t.Fatalf("err=%v", err)
	}
}

func TestValidate_UDPEntryPointConflict(t *testing.T) {
	err := Validate([]model.ProxyHost{
		{Name: "a", Protocol: "udp", UpstreamURL: "x:53", EntryPoints: []string{"dnsudp"}, Enabled: true},
		{Name: "b", Protocol: "udp", UpstreamURL: "y:53", EntryPoints: []string{"dnsudp"}, Enabled: true},
	})
	if err == nil || !strings.Contains(err.Error(), "already used") {
		t.Fatalf("err=%v", err)
	}
}

func TestValidate_TCPNoMiddlewares(t *testing.T) {
	err := Validate([]model.ProxyHost{{
		Name: "x", Protocol: "tcp", Domain: "x.example.com",
		UpstreamURL: "x:443", Enabled: true,
		Middlewares: []model.Middleware{{Name: "h", Type: "headers"}},
	}})
	if err == nil || !strings.Contains(err.Error(), "not supported on TCP") {
		t.Fatalf("err=%v", err)
	}
}

func TestValidate_UnsupportedProtocol(t *testing.T) {
	err := Validate([]model.ProxyHost{{
		Name: "x", Protocol: "smtp", UpstreamURL: "mail:25", Enabled: true,
	}})
	if err == nil || !strings.Contains(err.Error(), "unsupported protocol") {
		t.Fatalf("err=%v", err)
	}
}

func TestCompile_ForwardAuth_MissingAddress(t *testing.T) {
	_, err := Compile([]model.ProxyHost{{
		Name: "a", Domain: "x.example.com", UpstreamURL: "http://x", Enabled: true,
		Middlewares: []model.Middleware{{
			Name: "auth", Type: "forwardAuth",
			Config: map[string]any{"trustForwardHeader": true},
		}},
	}}, nil)
	if err == nil || !strings.Contains(err.Error(), "forwardAuth requires 'address'") {
		t.Fatalf("err=%v", err)
	}
}

func TestCompile_MissingMiddlewareConfig(t *testing.T) {
	_, err := Compile([]model.ProxyHost{{
		Name: "a", Domain: "x.example.com", UpstreamURL: "http://x", Enabled: true,
		Middlewares: []model.Middleware{{Name: "strip", Type: "stripPrefix"}},
	}}, nil)
	if err == nil || !strings.Contains(err.Error(), "stripPrefix requires") {
		t.Fatalf("err=%v", err)
	}
}

// readFile / normalize keep the goldens readable while still letting the
// tests check semantic equality.

func readFile(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return b
}

func normalize(t *testing.T, b []byte) []byte {
	t.Helper()
	var v any
	if err := json.Unmarshal(b, &v); err != nil {
		t.Fatalf("normalize: %v\n%s", err, b)
	}
	out, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("re-marshal: %v", err)
	}
	return out
}
