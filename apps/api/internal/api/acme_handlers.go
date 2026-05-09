package api

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/danielfree19/proxydock/apps/api/internal/acme"
	"github.com/danielfree19/proxydock/apps/api/internal/acme/dns"
	"github.com/danielfree19/proxydock/apps/api/internal/model"
)

// --- ACME account ---

func (s *Server) handleGetACMEAccount(w http.ResponseWriter, r *http.Request) {
	fleetID := r.PathValue("fleet_id")
	if _, err := s.Store.GetFleet(r.Context(), fleetID); err != nil {
		writeStoreError(w, err)
		return
	}
	a, err := s.Store.GetACMEAccount(r.Context(), fleetID)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	a.AccountKeyPEM = ""
	writeJSON(w, http.StatusOK, a)
}

func (s *Server) handleRegisterACMEAccount(w http.ResponseWriter, r *http.Request) {
	fleetID := r.PathValue("fleet_id")
	if _, err := s.Store.GetFleet(r.Context(), fleetID); err != nil {
		writeStoreError(w, err)
		return
	}
	var in struct {
		DirectoryURL string `json:"directory_url"`
		ContactEmail string `json:"contact_email"`
	}
	if !decodeBody(w, r, &in) {
		return
	}
	if strings.TrimSpace(in.DirectoryURL) == "" || strings.TrimSpace(in.ContactEmail) == "" {
		writeError(w, http.StatusBadRequest, "directory_url and contact_email are required")
		return
	}

	keyPEM, signer, err := acme.GenerateAccountKey()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not generate account key: "+err.Error())
		return
	}
	issuer := &acme.Issuer{
		DirectoryURL: in.DirectoryURL,
		ContactEmail: in.ContactEmail,
		AccountKey:   signer,
		HTTPClient:   s.acmeHTTPClient(),
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	accountURL, err := issuer.EnsureRegistered(ctx)
	if err != nil {
		writeError(w, http.StatusBadGateway, "register failed: "+err.Error())
		return
	}

	rec := model.ACMEAccount{
		FleetID:       fleetID,
		DirectoryURL:  in.DirectoryURL,
		ContactEmail:  in.ContactEmail,
		AccountKeyPEM: keyPEM,
		AccountURL:    accountURL,
	}
	if err := s.Store.UpsertACMEAccount(r.Context(), rec); err != nil {
		writeStoreError(w, err)
		return
	}
	rec.AccountKeyPEM = ""
	writeJSON(w, http.StatusCreated, rec)
}

func (s *Server) handleDeleteACMEAccount(w http.ResponseWriter, r *http.Request) {
	if err := s.Store.DeleteACMEAccount(r.Context(), r.PathValue("fleet_id")); err != nil {
		writeStoreError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- DNS providers ---

func (s *Server) handleListDNSProviders(w http.ResponseWriter, r *http.Request) {
	fleetID := r.PathValue("fleet_id")
	if _, err := s.Store.GetFleet(r.Context(), fleetID); err != nil {
		writeStoreError(w, err)
		return
	}
	out, err := s.Store.ListDNSProviders(r.Context(), fleetID)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	// List view omits credentials.
	for i := range out {
		out[i].Config = nil
	}
	writeJSON(w, http.StatusOK, map[string]any{"dns_providers": out})
}

func (s *Server) handleCreateDNSProvider(w http.ResponseWriter, r *http.Request) {
	fleetID := r.PathValue("fleet_id")
	if _, err := s.Store.GetFleet(r.Context(), fleetID); err != nil {
		writeStoreError(w, err)
		return
	}
	var in struct {
		Name   string          `json:"name"`
		Type   string          `json:"type"`
		Config json.RawMessage `json:"config"`
	}
	if !decodeBody(w, r, &in) {
		return
	}
	if strings.TrimSpace(in.Name) == "" || strings.TrimSpace(in.Type) == "" {
		writeError(w, http.StatusBadRequest, "name and type are required")
		return
	}
	// Validate by constructing the provider once; reject early if the
	// config is malformed.
	if _, err := dns.Build(in.Type, in.Config); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	saved, err := s.Store.CreateDNSProvider(r.Context(), model.DNSProvider{
		FleetID: fleetID, Name: in.Name, Type: in.Type, Config: in.Config,
	})
	if err != nil {
		writeStoreError(w, err)
		return
	}
	saved.Config = nil
	writeJSON(w, http.StatusCreated, saved)
}

func (s *Server) handleDeleteDNSProvider(w http.ResponseWriter, r *http.Request) {
	fleetID := r.PathValue("fleet_id")
	id, err := parsePathID(r.PathValue("dns_id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "dns_id must be an integer")
		return
	}
	if err := s.Store.DeleteDNSProvider(r.Context(), fleetID, id); err != nil {
		writeStoreError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- Request an ACME-issued certificate (async) ---

// handleRequestACMECertificate enqueues a job and returns 202 Accepted.
// The actual ACME flow runs in a worker (cmd/manager-api/acme_worker.go);
// callers poll GET /api/v1/jobs/{id} for status.
//
// Phase 4b ran issuance synchronously in this handler. The blocking
// version had two problems: 5–30 s requests through any reverse proxy
// in front of the manager risked timing out, and the UI couldn't show
// progress. Phase 5b moves it to a queue.
func (s *Server) handleRequestACMECertificate(w http.ResponseWriter, r *http.Request) {
	fleetID := r.PathValue("fleet_id")
	if _, err := s.Store.GetFleet(r.Context(), fleetID); err != nil {
		writeStoreError(w, err)
		return
	}
	var in struct {
		Name        string   `json:"name"`
		DNSNames    []string `json:"dns_names"`
		DNSProvider string   `json:"dns_provider"`
	}
	if !decodeBody(w, r, &in) {
		return
	}
	if strings.TrimSpace(in.Name) == "" || len(in.DNSNames) == 0 || strings.TrimSpace(in.DNSProvider) == "" {
		writeError(w, http.StatusBadRequest, "name, dns_names, and dns_provider are required")
		return
	}

	// Eager existence checks so the caller learns about misconfiguration
	// at submit time rather than from a failed-job row two seconds later.
	if _, err := s.Store.GetACMEAccount(r.Context(), fleetID); err != nil {
		writeStoreError(w, err)
		return
	}
	if _, err := s.Store.GetDNSProviderByName(r.Context(), fleetID, in.DNSProvider); err != nil {
		writeStoreError(w, err)
		return
	}

	job, err := s.Store.CreateACMEJob(r.Context(), model.ACMEJob{
		FleetID:     fleetID,
		Name:        in.Name,
		DNSNames:    in.DNSNames,
		DNSProvider: in.DNSProvider,
		Status:      model.ACMEJobPending,
	})
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, job)
}

// --- ACME job status ---

func (s *Server) handleGetACMEJob(w http.ResponseWriter, r *http.Request) {
	id, err := parsePathID(r.PathValue("job_id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "job_id must be an integer")
		return
	}
	j, err := s.Store.GetACMEJob(r.Context(), id)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, j)
}

func (s *Server) handleListACMEJobs(w http.ResponseWriter, r *http.Request) {
	fleetID := r.PathValue("fleet_id")
	if _, err := s.Store.GetFleet(r.Context(), fleetID); err != nil {
		writeStoreError(w, err)
		return
	}
	jobs, err := s.Store.ListACMEJobs(r.Context(), fleetID, 50)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"jobs": jobs})
}

// acmeHTTPClient returns an http.Client suitable for talking to ACME
// servers. When MANAGER_API_INSECURE_ACME=true (set in the demo so the
// manager can talk to Pebble's self-signed CA), TLS verification is
// disabled. Real deployments must leave this off.
func (s *Server) acmeHTTPClient() *http.Client {
	if s.InsecureACME {
		return &http.Client{
			Timeout: 60 * time.Second,
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
			},
		}
	}
	return nil
}
