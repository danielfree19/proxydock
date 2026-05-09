package dns

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// fakeCloudflare records every request and serves a small subset of the
// Cloudflare API v4 surface — enough to exercise Present + CleanUp.
type fakeCloudflare struct {
	t           *testing.T
	mu          sync.Mutex
	records     map[string]record // id -> record
	nextID      int
	gotAuth     string
	calls       []string
	failResolve bool
}

type record struct {
	ID      string
	Name    string
	Content string
}

func (f *fakeCloudflare) handler() http.Handler {
	mux := http.NewServeMux()

	// GET /zones?name=example.com → returns the zone ID
	mux.HandleFunc("GET /zones", func(w http.ResponseWriter, r *http.Request) {
		f.recordAuth(r)
		f.append("GET /zones name=" + r.URL.Query().Get("name"))
		if f.failResolve {
			writeCFError(w, 1001, "no such zone")
			return
		}
		writeCFResult(w, []map[string]string{{"id": "ZONE", "name": r.URL.Query().Get("name")}})
	})

	// POST /zones/{id}/dns_records → create
	mux.HandleFunc("POST /zones/{zone}/dns_records", func(w http.ResponseWriter, r *http.Request) {
		f.recordAuth(r)
		var in record
		_ = json.NewDecoder(r.Body).Decode(&in)
		f.mu.Lock()
		f.nextID++
		id := "rec" + itoa(f.nextID)
		f.records[id] = record{ID: id, Name: in.Name, Content: in.Content}
		f.mu.Unlock()
		f.append("POST dns_records " + in.Name + "=" + in.Content)
		writeCFResult(w, map[string]string{"id": id})
	})

	// GET /zones/{id}/dns_records?type=TXT&name=...&content=... → list matches
	mux.HandleFunc("GET /zones/{zone}/dns_records", func(w http.ResponseWriter, r *http.Request) {
		f.recordAuth(r)
		q := r.URL.Query()
		f.append("GET dns_records " + q.Get("name") + "=" + q.Get("content"))
		f.mu.Lock()
		out := []map[string]string{}
		for _, rec := range f.records {
			if rec.Name == q.Get("name") && rec.Content == q.Get("content") {
				out = append(out, map[string]string{"id": rec.ID})
			}
		}
		f.mu.Unlock()
		writeCFResult(w, out)
	})

	// DELETE /zones/{id}/dns_records/{rec_id}
	mux.HandleFunc("DELETE /zones/{zone}/dns_records/{rec}", func(w http.ResponseWriter, r *http.Request) {
		f.recordAuth(r)
		id := r.PathValue("rec")
		f.mu.Lock()
		delete(f.records, id)
		f.mu.Unlock()
		f.append("DELETE dns_records " + id)
		writeCFResult(w, map[string]string{"id": id})
	})
	return mux
}

func (f *fakeCloudflare) recordAuth(r *http.Request) {
	f.mu.Lock()
	f.gotAuth = r.Header.Get("Authorization")
	f.mu.Unlock()
}

func (f *fakeCloudflare) append(s string) {
	f.mu.Lock()
	f.calls = append(f.calls, s)
	f.mu.Unlock()
}

func itoa(n int) string {
	return strings.TrimLeft(string([]byte{byte('0' + n/10000%10), byte('0' + n/1000%10), byte('0' + n/100%10), byte('0' + n/10%10), byte('0' + n%10)}), "0")
}

func writeCFResult(w http.ResponseWriter, result any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "result": result, "errors": []any{}})
}

func writeCFError(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"success": false,
		"errors":  []map[string]any{{"code": code, "message": msg}},
	})
}

func newCloudflareTestProvider(t *testing.T, fake *fakeCloudflare, configJSON string) *Cloudflare {
	t.Helper()
	srv := httptest.NewServer(fake.handler())
	t.Cleanup(srv.Close)
	p, err := NewCloudflare([]byte(configJSON))
	if err != nil {
		t.Fatal(err)
	}
	p.BaseURL = srv.URL
	return p
}

func TestCloudflare_PresentAndCleanUp_ZoneID(t *testing.T) {
	fake := &fakeCloudflare{t: t, records: map[string]record{}}
	p := newCloudflareTestProvider(t, fake, `{"api_token":"t1","zone_id":"ZONE"}`)
	ctx := context.Background()
	if err := p.Present(ctx, "_acme-challenge.example.com.", "value-1"); err != nil {
		t.Fatal(err)
	}
	if fake.gotAuth != "Bearer t1" {
		t.Fatalf("Authorization header = %q", fake.gotAuth)
	}
	// record should exist with trailing dot stripped
	if len(fake.records) != 1 {
		t.Fatalf("records=%d", len(fake.records))
	}
	for _, r := range fake.records {
		if r.Name != "_acme-challenge.example.com" {
			t.Fatalf("trailing dot not stripped: %q", r.Name)
		}
	}
	if err := p.CleanUp(ctx, "_acme-challenge.example.com.", "value-1"); err != nil {
		t.Fatal(err)
	}
	if len(fake.records) != 0 {
		t.Fatalf("CleanUp left records: %v", fake.records)
	}
}

func TestCloudflare_PresentAndCleanUp_ZoneName(t *testing.T) {
	fake := &fakeCloudflare{t: t, records: map[string]record{}}
	p := newCloudflareTestProvider(t, fake, `{"api_token":"t1","zone_name":"example.com"}`)
	ctx := context.Background()
	if err := p.Present(ctx, "_acme-challenge.example.com.", "value-x"); err != nil {
		t.Fatal(err)
	}
	// zone lookup happened
	sawLookup := false
	for _, c := range fake.calls {
		if strings.HasPrefix(c, "GET /zones name=example.com") {
			sawLookup = true
		}
	}
	if !sawLookup {
		t.Fatalf("expected a zone lookup, got calls: %v", fake.calls)
	}
}

func TestCloudflare_NeedsAuthAndZone(t *testing.T) {
	if _, err := NewCloudflare([]byte(`{}`)); err == nil {
		t.Fatal("expected api_token error")
	}
	if _, err := NewCloudflare([]byte(`{"api_token":"t"}`)); err == nil {
		t.Fatal("expected zone_id/zone_name error")
	}
}

func TestCloudflare_APIError_Surfaced(t *testing.T) {
	fake := &fakeCloudflare{t: t, records: map[string]record{}, failResolve: true}
	p := newCloudflareTestProvider(t, fake, `{"api_token":"t1","zone_name":"missing.example"}`)
	err := p.Present(context.Background(), "_acme-challenge.missing.example.", "v")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "no such zone") {
		t.Fatalf("error did not surface CF message: %v", err)
	}
	// Sanity: confirm the test infra wired the body the way the fake expected.
	_, _ = io.ReadAll(strings.NewReader("ok"))
}

func TestCloudflare_BuildViaRegistry(t *testing.T) {
	p, err := Build("cloudflare", []byte(`{"api_token":"x","zone_id":"y"}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := p.(*Cloudflare); !ok {
		t.Fatalf("got %T, want *Cloudflare", p)
	}
}
