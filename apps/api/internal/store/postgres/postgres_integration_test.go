//go:build integration

// Package postgres integration tests run a real Postgres in a
// testcontainers-managed container, apply every migration, and exercise
// the Store implementation against the parts where Postgres semantics
// actually matter (encryption round-trip, TEXT[] labels, JSONB→TEXT
// migration, FOR UPDATE SKIP LOCKED, FK cascades, audit queries).
//
// Default `go test ./...` skips this file via the build tag. Run
// explicitly with `go test -tags integration ./internal/store/postgres/...`.
package postgres

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	tc "github.com/testcontainers/testcontainers-go"
	tcpg "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/danielfree19/proxydock/apps/api/internal/cryptokit"
	"github.com/danielfree19/proxydock/apps/api/internal/db"
	"github.com/danielfree19/proxydock/apps/api/internal/model"
	"github.com/danielfree19/proxydock/apps/api/internal/store"
)

// setupPG starts a fresh Postgres container, applies every migration,
// and returns a Store backed by it. The cipher is set so encryption
// paths exercise their non-nil branch; tests that want plaintext
// behaviour can replace s.cipher in-place.
func setupPG(t *testing.T) (*Store, *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	container, err := tcpg.RunContainer(ctx,
		tc.WithImage("postgres:16-alpine"),
		tcpg.WithDatabase("tfm"),
		tcpg.WithUsername("tfm"),
		tcpg.WithPassword("tfm"),
		tc.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(60*time.Second)),
	)
	if err != nil {
		t.Fatalf("start postgres container: %v", err)
	}
	t.Cleanup(func() {
		_ = container.Terminate(ctx)
	})
	dsn, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)

	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	cipher, err := cryptokit.NewCipherHex(
		"0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")
	if err != nil {
		t.Fatal(err)
	}
	return New(pool, cipher), pool
}

// mustFleet creates a fleet with the given id and returns it. Used as
// a precondition in most tests below.
func mustFleet(t *testing.T, s *Store, id string) {
	t.Helper()
	if _, err := s.CreateFleet(context.Background(), model.Fleet{ID: id, Name: id}); err != nil {
		t.Fatalf("CreateFleet(%s): %v", id, err)
	}
}

func TestIntegration_MigrationsApply(t *testing.T) {
	// setupPG itself applies every migration; reaching this line means
	// the SQL parsed and ran without error against a real Postgres.
	_, _ = setupPG(t)
}

func TestIntegration_FleetAgentTokenRoundtrip(t *testing.T) {
	s, _ := setupPG(t)
	ctx := context.Background()
	mustFleet(t, s, "homelab")

	if _, err := s.CreateAgent(ctx, model.Agent{
		ID: "traefik-1", FleetID: "homelab", Name: "node 1",
		Labels: []string{"region=us", "tier=prod"},
	}); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}
	got, err := s.GetAgent(ctx, "traefik-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Labels) != 2 || got.Labels[0] != "region=us" || got.Labels[1] != "tier=prod" {
		t.Fatalf("Labels TEXT[] round-trip lost data: %v", got.Labels)
	}

	// Mint a token, confirm SHA-256 hash bytes survive BYTEA round-trip.
	hash := []byte{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08,
		0x09, 0x0a, 0x0b, 0x0c, 0x0d, 0x0e, 0x0f, 0x10,
		0x11, 0x12, 0x13, 0x14, 0x15, 0x16, 0x17, 0x18,
		0x19, 0x1a, 0x1b, 0x1c, 0x1d, 0x1e, 0x1f, 0x20}
	if _, err := s.MintToken(ctx, "traefik-1", "demo", "abcd1234", hash); err != nil {
		t.Fatal(err)
	}
	rec, gotHash, err := s.LookupToken(ctx, "abcd1234")
	if err != nil {
		t.Fatal(err)
	}
	if rec.Agent.ID != "traefik-1" {
		t.Fatalf("Agent.ID = %q", rec.Agent.ID)
	}
	if string(gotHash) != string(hash) {
		t.Fatalf("BYTEA round-trip mangled hash: %x", gotHash)
	}
}

func TestIntegration_FKCascadeOnFleetDelete(t *testing.T) {
	s, pool := setupPG(t)
	ctx := context.Background()
	mustFleet(t, s, "homelab")
	if _, err := s.CreateAgent(ctx, model.Agent{
		ID: "traefik-1", FleetID: "homelab", Name: "n",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateProxyHost(ctx, model.ProxyHost{
		FleetID: "homelab", Name: "h", Domain: "x.y",
		UpstreamURL: "http://x", EntryPoints: []string{"web"}, Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteFleet(ctx, "homelab"); err != nil {
		t.Fatal(err)
	}
	// Verify the FK cascade actually fired by counting rows directly.
	var n int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM agents`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("agents not cascade-deleted: %d remain", n)
	}
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM proxy_hosts`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("proxy_hosts not cascade-deleted: %d remain", n)
	}
}

func TestIntegration_EncryptedColumns_RoundTrip(t *testing.T) {
	s, pool := setupPG(t)
	ctx := context.Background()
	mustFleet(t, s, "homelab")

	plaintextKey := "-----BEGIN EC PRIVATE KEY-----\nMHc...\n-----END EC PRIVATE KEY-----\n"
	saved, err := s.CreateCertificate(ctx, model.Certificate{
		FleetID: "homelab", Name: "c1",
		CertPEM: "-----BEGIN CERTIFICATE-----\n...\n-----END CERTIFICATE-----\n",
		KeyPEM:  plaintextKey,
		Fingerprint: "sha256:test", Subject: "CN=test", Issuer: "CN=ca",
		DNSNames: []string{"example.com"},
		NotBefore: time.Now(), NotAfter: time.Now().Add(24 * time.Hour),
		Source: "upload",
	})
	if err != nil {
		t.Fatal(err)
	}

	// Read raw column to confirm it actually got encrypted on disk.
	var stored string
	if err := pool.QueryRow(ctx,
		`SELECT key_pem FROM certificates WHERE id = $1`, saved.ID).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(stored, cryptokit.EncryptedPrefix) {
		t.Fatalf("key_pem stored without encryption prefix: %q", stored[:32])
	}
	if strings.Contains(stored, "BEGIN EC PRIVATE KEY") {
		t.Fatalf("plaintext leaked into stored key_pem")
	}

	// Read through the store API and confirm decryption returns the original.
	got, err := s.GetCertificate(ctx, "homelab", saved.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.KeyPEM != plaintextKey {
		t.Fatalf("KeyPEM round-trip differs:\nwant %q\ngot  %q", plaintextKey, got.KeyPEM)
	}
}

func TestIntegration_EncryptedColumns_PlaintextPassthrough(t *testing.T) {
	s, pool := setupPG(t)
	ctx := context.Background()
	mustFleet(t, s, "homelab")

	// Inject a row written without encryption (simulating data from
	// before the cipher was wired up). Ensure scanCertificate decrypts
	// it transparently.
	_, err := pool.Exec(ctx, `
		INSERT INTO certificates (fleet_id, name, cert_pem, key_pem,
		    fingerprint, subject, issuer, dns_names,
		    not_before, not_after, source)
		VALUES ('homelab', 'legacy', 'CERT', 'PLAIN_KEY',
		    'fp', 'CN=x', 'CN=y', ARRAY['x.y'],
		    now(), now() + interval '1 day', 'upload')`)
	if err != nil {
		t.Fatal(err)
	}
	certs, err := s.ListCertificates(ctx, "homelab")
	if err != nil {
		t.Fatal(err)
	}
	if len(certs) != 1 || certs[0].KeyPEM != "PLAIN_KEY" {
		t.Fatalf("plaintext passthrough broke: %+v", certs)
	}
}

func TestIntegration_RevisionRoundtrip_ByteIdentical(t *testing.T) {
	// compiled_config was migrated JSONB → TEXT in 005 specifically so
	// the bytes round-trip unchanged. Verify it does — losing a single
	// byte breaks ed25519 signature verification on the agents.
	s, _ := setupPG(t)
	ctx := context.Background()
	mustFleet(t, s, "homelab")

	original := []byte(`{ "http" :  {"routers":  {  "x":  {"rule":"Host(\"a.b\")"}  }  }  }`)
	saved, err := s.CreateRevision(ctx, model.Revision{
		FleetID: "homelab", Number: 1,
		CompiledConfig:   original,
		SourceProxyHosts: []byte(`[]`),
		ETag:             `"etag"`,
		Signature:        "sig", SignatureAlg: "ed25519",
	}, true)
	if err != nil {
		t.Fatal(err)
	}
	if string(saved.CompiledConfig) != string(original) {
		t.Fatalf("compiled_config bytes changed on round-trip:\nwant %q\ngot  %q",
			original, saved.CompiledConfig)
	}
	got, err := s.GetRevision(ctx, "homelab", 1)
	if err != nil {
		t.Fatal(err)
	}
	if string(got.CompiledConfig) != string(original) {
		t.Fatalf("compiled_config differs after refetch:\nwant %q\ngot  %q",
			original, got.CompiledConfig)
	}
	if got.Signature != "sig" || got.SignatureAlg != "ed25519" {
		t.Fatalf("signature columns dropped: %q / %q", got.Signature, got.SignatureAlg)
	}
}

func TestIntegration_ACMEJobs_SkipLocked(t *testing.T) {
	// The Postgres-backed worker uses FOR UPDATE SKIP LOCKED so two
	// claim calls that race never see the same row. Race them.
	s, _ := setupPG(t)
	ctx := context.Background()
	mustFleet(t, s, "homelab")

	const N = 5
	for i := 0; i < N; i++ {
		if _, err := s.CreateACMEJob(ctx, model.ACMEJob{
			FleetID: "homelab", Name: "c" + string(rune('a'+i)),
			DNSNames: []string{"x.y"}, DNSProvider: "p",
			Status: model.ACMEJobPending,
		}); err != nil {
			t.Fatal(err)
		}
	}

	const workers = 4
	got := make(chan int64, N+workers)
	var wg sync.WaitGroup
	wg.Add(workers)
	for w := 0; w < workers; w++ {
		go func() {
			defer wg.Done()
			for {
				j, err := s.ClaimNextACMEJob(ctx)
				if err != nil {
					return // queue empty
				}
				got <- j.ID
			}
		}()
	}
	wg.Wait()
	close(got)

	seen := map[int64]int{}
	for id := range got {
		seen[id]++
	}
	if len(seen) != N {
		t.Fatalf("claimed %d distinct jobs; want %d (seen=%v)", len(seen), N, seen)
	}
	for id, count := range seen {
		if count != 1 {
			t.Fatalf("job %d claimed %d times — SKIP LOCKED isn't locking", id, count)
		}
	}
}

func TestIntegration_ACMEJob_TerminalTransitions(t *testing.T) {
	s, _ := setupPG(t)
	ctx := context.Background()
	mustFleet(t, s, "homelab")
	j, err := s.CreateACMEJob(ctx, model.ACMEJob{
		FleetID: "homelab", Name: "c1",
		DNSNames: []string{"x.y"}, DNSProvider: "p",
		Status: model.ACMEJobPending,
	})
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := s.ClaimNextACMEJob(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if claimed.Status != model.ACMEJobRunning {
		t.Fatalf("claim didn't flip status: %q", claimed.Status)
	}
	if claimed.StartedAt == nil {
		t.Fatal("claim didn't stamp started_at")
	}
	// Need a cert id for MarkSucceeded — create a minimal cert.
	cert, err := s.CreateCertificate(ctx, model.Certificate{
		FleetID: "homelab", Name: "c1",
		CertPEM: "C", KeyPEM: "K", Fingerprint: "fp",
		Subject: "s", Issuer: "i", DNSNames: []string{"x.y"},
		NotBefore: time.Now(), NotAfter: time.Now().Add(time.Hour),
		Source: "acme",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.MarkACMEJobSucceeded(ctx, j.ID, cert.ID); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetACMEJob(ctx, j.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != model.ACMEJobSucceeded {
		t.Fatalf("status %q want succeeded", got.Status)
	}
	if got.CertID == nil || *got.CertID != cert.ID {
		t.Fatalf("cert_id = %v", got.CertID)
	}
	if got.FinishedAt == nil {
		t.Fatal("finished_at not set")
	}

	// Failure path on a fresh job.
	j2, _ := s.CreateACMEJob(ctx, model.ACMEJob{
		FleetID: "homelab", Name: "c2",
		DNSNames: []string{"x.y"}, DNSProvider: "p",
		Status: model.ACMEJobPending,
	})
	_, _ = s.ClaimNextACMEJob(ctx)
	if err := s.MarkACMEJobFailed(ctx, j2.ID, "boom"); err != nil {
		t.Fatal(err)
	}
	got2, _ := s.GetACMEJob(ctx, j2.ID)
	if got2.Status != model.ACMEJobFailed || got2.Error != "boom" {
		t.Fatalf("failed transition didn't take: status=%q err=%q", got2.Status, got2.Error)
	}
}

func TestIntegration_ACMEAccount_Upsert(t *testing.T) {
	s, _ := setupPG(t)
	ctx := context.Background()
	mustFleet(t, s, "homelab")

	if err := s.UpsertACMEAccount(ctx, model.ACMEAccount{
		FleetID: "homelab", DirectoryURL: "https://acme.example/dir",
		ContactEmail: "ops@example.com", AccountKeyPEM: "KEY1", AccountURL: "u1",
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertACMEAccount(ctx, model.ACMEAccount{
		FleetID: "homelab", DirectoryURL: "https://acme.example/dir",
		ContactEmail: "ops@example.com", AccountKeyPEM: "KEY2", AccountURL: "u2",
	}); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetACMEAccount(ctx, "homelab")
	if err != nil {
		t.Fatal(err)
	}
	if got.AccountURL != "u2" || got.AccountKeyPEM != "KEY2" {
		t.Fatalf("upsert didn't replace row: %+v", got)
	}
}

func TestIntegration_AuditQuery_Filters(t *testing.T) {
	s, _ := setupPG(t)
	ctx := context.Background()
	mustFleet(t, s, "homelab")
	mustFleet(t, s, "other")

	homelab := "homelab"
	other := "other"
	mk := func(actor, path string, fleet *string) {
		t.Helper()
		if err := s.AppendAuditEntry(ctx, model.AuditEntry{
			Actor: actor, Method: "POST", Path: path, Status: 201,
			FleetID: fleet,
		}); err != nil {
			t.Fatal(err)
		}
	}
	mk("bootstrap", "/api/v1/fleets", nil)            // global
	mk("admin:abcd1234", "/api/v1/fleets/homelab/x", &homelab)
	mk("admin:abcd1234", "/api/v1/fleets/other/x", &other)
	mk("bootstrap", "/api/v1/admin/tokens", nil)      // global

	// Filter == nil → all four.
	all, err := s.ListAuditEntries(ctx, store.AuditQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 4 {
		t.Fatalf("nil filter: got %d, want 4", len(all))
	}

	// Filter == &"" → only global (NULL fleet_id).
	empty := ""
	globalOnly, err := s.ListAuditEntries(ctx, store.AuditQuery{FleetID: &empty})
	if err != nil {
		t.Fatal(err)
	}
	if len(globalOnly) != 2 {
		t.Fatalf("global filter: got %d, want 2", len(globalOnly))
	}
	for _, e := range globalOnly {
		if e.FleetID != nil {
			t.Fatalf("global filter returned fleet-tagged row: %+v", e)
		}
	}

	// Filter == &"homelab" → only that fleet.
	specific, err := s.ListAuditEntries(ctx, store.AuditQuery{FleetID: &homelab})
	if err != nil {
		t.Fatal(err)
	}
	if len(specific) != 1 || specific[0].Path != "/api/v1/fleets/homelab/x" {
		t.Fatalf("specific filter: %+v", specific)
	}

	// BeforeID pagination: walk back through all 4 in pairs of 2.
	page1, _ := s.ListAuditEntries(ctx, store.AuditQuery{Limit: 2})
	if len(page1) != 2 {
		t.Fatalf("page1: got %d", len(page1))
	}
	page2, _ := s.ListAuditEntries(ctx, store.AuditQuery{Limit: 2, BeforeID: page1[len(page1)-1].ID})
	if len(page2) != 2 {
		t.Fatalf("page2: got %d", len(page2))
	}
	for _, e := range page2 {
		for _, p := range page1 {
			if e.ID == p.ID {
				t.Fatalf("page2 leaked entry from page1 (id %d)", e.ID)
			}
		}
	}
}

func TestIntegration_DNSProviderConfig_Encrypted(t *testing.T) {
	// dns_providers.config holds API tokens. Confirm the column actually
	// stores ciphertext on disk.
	s, pool := setupPG(t)
	ctx := context.Background()
	mustFleet(t, s, "homelab")

	cfg := []byte(`{"api_token":"super-secret","zone_name":"example.com"}`)
	saved, err := s.CreateDNSProvider(ctx, model.DNSProvider{
		FleetID: "homelab", Name: "primary", Type: "cloudflare", Config: cfg,
	})
	if err != nil {
		t.Fatal(err)
	}

	var stored string
	if err := pool.QueryRow(ctx,
		`SELECT config FROM dns_providers WHERE id = $1`, saved.ID).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(stored, "super-secret") {
		t.Fatalf("plaintext API token leaked into stored dns_providers.config: %q", stored)
	}
	if !strings.HasPrefix(stored, cryptokit.EncryptedPrefix) {
		t.Fatalf("config stored without encryption prefix: %q", stored[:32])
	}

	// Read back through the store and confirm we get the original JSON.
	got, err := s.GetDNSProvider(ctx, "homelab", saved.ID)
	if err != nil {
		t.Fatal(err)
	}
	if string(got.Config) != string(cfg) {
		t.Fatalf("config round-trip differs:\nwant %s\ngot  %s", cfg, got.Config)
	}
}

func TestIntegration_AdminTokenRevokeRejected(t *testing.T) {
	s, _ := setupPG(t)
	ctx := context.Background()

	hash := []byte("01234567890123456789012345678901") // 32 bytes
	if _, err := s.MintAdminToken(ctx, "alice", "abcdef01", hash); err != nil {
		t.Fatal(err)
	}
	if err := s.RevokeAdminToken(ctx, "abcdef01"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.LookupAdminToken(ctx, "abcdef01"); err != store.ErrTokenRevoked {
		t.Fatalf("LookupAdminToken on revoked token: got %v, want ErrTokenRevoked", err)
	}
}
