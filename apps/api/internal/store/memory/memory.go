// Package memory is an in-memory implementation of store.Store used for
// unit tests. It is intentionally simple, single-process, and not safe
// for parallel writes from multiple goroutines (a single mutex covers
// every field).
package memory

import (
	"bytes"
	"context"
	"sort"
	"sync"
	"time"

	"github.com/danielfree19/proxydock/apps/api/internal/model"
	"github.com/danielfree19/proxydock/apps/api/internal/store"
)

type Store struct {
	mu sync.Mutex

	fleets map[string]model.Fleet
	agents map[string]model.Agent
	tokens map[string]tokenEntry // prefix -> entry

	proxyHostSeq int64
	proxyHosts   map[int64]model.ProxyHost

	mwTemplateSeq int64
	mwTemplates   map[int64]model.MiddlewareTemplate

	webhookSeq    int64
	webhooks      map[int64]model.Webhook
	webhookJobSeq int64
	webhookJobs   map[int64]model.WebhookJob

	revisionSeq int64
	revisions   map[int64]model.Revision

	certificateSeq int64
	certificates   map[int64]model.Certificate

	acmeAccounts map[string]model.ACMEAccount

	dnsProviderSeq int64
	dnsProviders   map[int64]model.DNSProvider

	adminTokens map[string]adminTokenEntry

	acmeJobSeq int64
	acmeJobs   map[int64]model.ACMEJob

	auditSeq int64
	audit    []model.AuditEntry
}

type adminTokenEntry struct {
	token model.AdminToken
	hash  []byte
}

type tokenEntry struct {
	token  model.AgentToken
	hash   []byte
	closed bool
}

// New constructs an empty in-memory Store.
func New() *Store {
	return &Store{
		fleets:       map[string]model.Fleet{},
		agents:       map[string]model.Agent{},
		tokens:       map[string]tokenEntry{},
		proxyHosts:   map[int64]model.ProxyHost{},
		mwTemplates:  map[int64]model.MiddlewareTemplate{},
		webhooks:     map[int64]model.Webhook{},
		webhookJobs:  map[int64]model.WebhookJob{},
		revisions:    map[int64]model.Revision{},
		certificates: map[int64]model.Certificate{},
		acmeAccounts: map[string]model.ACMEAccount{},
		dnsProviders: map[int64]model.DNSProvider{},
		adminTokens:  map[string]adminTokenEntry{},
		acmeJobs:     map[int64]model.ACMEJob{},
		audit:        []model.AuditEntry{},
	}
}

// --- Audit log ---

func (s *Store) AppendAuditEntry(_ context.Context, e model.AuditEntry) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.auditSeq++
	e.ID = s.auditSeq
	if e.CreatedAt.IsZero() {
		e.CreatedAt = time.Now().UTC()
	}
	s.audit = append(s.audit, e)
	return nil
}

func (s *Store) ListAuditEntries(_ context.Context, q store.AuditQuery) ([]model.AuditEntry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	limit := q.Limit
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	out := []model.AuditEntry{}
	// Walk backwards (newest first).
	for i := len(s.audit) - 1; i >= 0; i-- {
		e := s.audit[i]
		if q.BeforeID > 0 && e.ID >= q.BeforeID {
			continue
		}
		if q.FleetID != nil {
			want := *q.FleetID
			switch {
			case want == "" && e.FleetID != nil:
				continue
			case want != "" && (e.FleetID == nil || *e.FleetID != want):
				continue
			}
		}
		out = append(out, e)
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}

// --- ACME jobs ---

func (s *Store) CreateACMEJob(_ context.Context, j model.ACMEJob) (model.ACMEJob, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.fleets[j.FleetID]; !ok {
		return model.ACMEJob{}, store.ErrNotFound
	}
	s.acmeJobSeq++
	j.ID = s.acmeJobSeq
	if j.CreatedAt.IsZero() {
		j.CreatedAt = time.Now().UTC()
	}
	if j.Status == "" {
		j.Status = model.ACMEJobPending
	}
	s.acmeJobs[j.ID] = j
	return j, nil
}

func (s *Store) GetACMEJob(_ context.Context, id int64) (model.ACMEJob, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	j, ok := s.acmeJobs[id]
	if !ok {
		return model.ACMEJob{}, store.ErrNotFound
	}
	return j, nil
}

func (s *Store) ListACMEJobs(_ context.Context, fleetID string, limit int) ([]model.ACMEJob, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []model.ACMEJob{}
	for _, j := range s.acmeJobs {
		if j.FleetID == fleetID {
			out = append(out, j)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID > out[j].ID })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (s *Store) ClaimNextACMEJob(_ context.Context) (model.ACMEJob, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	// FIFO scan; the in-memory store doesn't have to model SKIP LOCKED.
	keys := make([]int64, 0, len(s.acmeJobs))
	for k := range s.acmeJobs {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	for _, k := range keys {
		j := s.acmeJobs[k]
		if j.Status == model.ACMEJobPending {
			j.Status = model.ACMEJobRunning
			now := time.Now().UTC()
			j.StartedAt = &now
			s.acmeJobs[k] = j
			return j, nil
		}
	}
	return model.ACMEJob{}, store.ErrNotFound
}

func (s *Store) MarkACMEJobSucceeded(_ context.Context, id, certID int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	j, ok := s.acmeJobs[id]
	if !ok {
		return store.ErrNotFound
	}
	now := time.Now().UTC()
	j.Status = model.ACMEJobSucceeded
	j.CertID = &certID
	j.FinishedAt = &now
	j.Error = ""
	s.acmeJobs[id] = j
	return nil
}

func (s *Store) MarkACMEJobFailed(_ context.Context, id int64, errMsg string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	j, ok := s.acmeJobs[id]
	if !ok {
		return store.ErrNotFound
	}
	now := time.Now().UTC()
	j.Status = model.ACMEJobFailed
	j.Error = errMsg
	j.FinishedAt = &now
	s.acmeJobs[id] = j
	return nil
}

// --- Admin tokens ---

func (s *Store) MintAdminToken(_ context.Context, name, prefix string, secretHash []byte) (model.AdminToken, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.adminTokens[prefix]; ok {
		return model.AdminToken{}, store.ErrConflict
	}
	t := model.AdminToken{Prefix: prefix, Name: name, CreatedAt: time.Now().UTC()}
	s.adminTokens[prefix] = adminTokenEntry{token: t, hash: append([]byte(nil), secretHash...)}
	return t, nil
}

func (s *Store) ListAdminTokens(_ context.Context) ([]model.AdminToken, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []model.AdminToken{}
	for _, e := range s.adminTokens {
		out = append(out, e.token)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out, nil
}

func (s *Store) LookupAdminToken(_ context.Context, prefix string) (model.AdminToken, []byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.adminTokens[prefix]
	if !ok {
		return model.AdminToken{}, nil, store.ErrNotFound
	}
	if e.token.RevokedAt != nil {
		return model.AdminToken{}, nil, store.ErrTokenRevoked
	}
	return e.token, append([]byte(nil), e.hash...), nil
}

func (s *Store) TouchAdminToken(_ context.Context, prefix string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.adminTokens[prefix]
	if !ok {
		return store.ErrNotFound
	}
	now := time.Now().UTC()
	e.token.LastUsedAt = &now
	s.adminTokens[prefix] = e
	return nil
}

func (s *Store) RevokeAdminToken(_ context.Context, prefix string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.adminTokens[prefix]
	if !ok {
		return store.ErrNotFound
	}
	now := time.Now().UTC()
	e.token.RevokedAt = &now
	s.adminTokens[prefix] = e
	return nil
}

// --- Fleets ---

func (s *Store) CreateFleet(_ context.Context, f model.Fleet) (model.Fleet, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.fleets[f.ID]; ok {
		return model.Fleet{}, store.ErrConflict
	}
	if f.CreatedAt.IsZero() {
		f.CreatedAt = time.Now().UTC()
	}
	s.fleets[f.ID] = f
	return f, nil
}

func (s *Store) GetFleet(_ context.Context, id string) (model.Fleet, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, ok := s.fleets[id]
	if !ok {
		return model.Fleet{}, store.ErrNotFound
	}
	return f, nil
}

func (s *Store) ListFleets(_ context.Context) ([]model.Fleet, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]model.Fleet, 0, len(s.fleets))
	for _, f := range s.fleets {
		out = append(out, f)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func (s *Store) DeleteFleet(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.fleets[id]; !ok {
		return store.ErrNotFound
	}
	delete(s.fleets, id)
	for aid, a := range s.agents {
		if a.FleetID == id {
			delete(s.agents, aid)
		}
	}
	for pid, p := range s.proxyHosts {
		if p.FleetID == id {
			delete(s.proxyHosts, pid)
		}
	}
	for rid, r := range s.revisions {
		if r.FleetID == id {
			delete(s.revisions, rid)
		}
	}
	return nil
}

// --- Agents ---

func (s *Store) CreateAgent(_ context.Context, a model.Agent) (model.Agent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.fleets[a.FleetID]; !ok {
		return model.Agent{}, store.ErrNotFound
	}
	if _, ok := s.agents[a.ID]; ok {
		return model.Agent{}, store.ErrConflict
	}
	if a.CreatedAt.IsZero() {
		a.CreatedAt = time.Now().UTC()
	}
	s.agents[a.ID] = a
	return a, nil
}

func (s *Store) GetAgent(_ context.Context, id string) (model.Agent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, ok := s.agents[id]
	if !ok {
		return model.Agent{}, store.ErrNotFound
	}
	return a, nil
}

func (s *Store) ListAgents(_ context.Context, fleetID string) ([]model.Agent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []model.Agent{}
	for _, a := range s.agents {
		if a.FleetID == fleetID {
			out = append(out, a)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func (s *Store) DeleteAgent(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.agents[id]; !ok {
		return store.ErrNotFound
	}
	delete(s.agents, id)
	for prefix, t := range s.tokens {
		if t.token.AgentID == id {
			delete(s.tokens, prefix)
		}
	}
	return nil
}

func (s *Store) UpdateAgentLabels(_ context.Context, agentID string, labels []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, ok := s.agents[agentID]
	if !ok {
		return store.ErrNotFound
	}
	if labels == nil {
		labels = []string{}
	}
	a.Labels = append([]string(nil), labels...)
	s.agents[agentID] = a
	return nil
}

func (s *Store) UpdateAgentHeartbeat(_ context.Context, agentID string, hb store.HeartbeatUpdate) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, ok := s.agents[agentID]
	if !ok {
		return store.ErrNotFound
	}
	now := time.Now().UTC()
	a.LastHeartbeatAt = &now
	rev := hb.CurrentRevision
	a.LastRevisionSeen = &rev
	pv := hb.ProviderVersion
	a.LastProviderVersion = &pv
	tv := hb.TraefikVersion
	a.LastTraefikVersion = &tv
	if hb.LastError != "" {
		le := hb.LastError
		a.LastError = &le
	} else {
		a.LastError = nil
	}
	s.agents[agentID] = a
	return nil
}

// --- Tokens ---

func (s *Store) MintToken(_ context.Context, agentID, name, prefix string, secretHash []byte) (model.AgentToken, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.agents[agentID]; !ok {
		return model.AgentToken{}, store.ErrNotFound
	}
	if _, ok := s.tokens[prefix]; ok {
		return model.AgentToken{}, store.ErrConflict
	}
	t := model.AgentToken{
		Prefix:    prefix,
		AgentID:   agentID,
		Name:      name,
		CreatedAt: time.Now().UTC(),
	}
	s.tokens[prefix] = tokenEntry{token: t, hash: append([]byte(nil), secretHash...)}
	return t, nil
}

func (s *Store) ListTokens(_ context.Context, agentID string) ([]model.AgentToken, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []model.AgentToken{}
	for _, t := range s.tokens {
		if t.token.AgentID == agentID {
			out = append(out, t.token)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out, nil
}

func (s *Store) RevokeToken(_ context.Context, agentID, prefix string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.tokens[prefix]
	if !ok || t.token.AgentID != agentID {
		return store.ErrNotFound
	}
	now := time.Now().UTC()
	t.token.RevokedAt = &now
	s.tokens[prefix] = t
	return nil
}

func (s *Store) LookupToken(_ context.Context, prefix string) (store.TokenRecord, []byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.tokens[prefix]
	if !ok {
		return store.TokenRecord{}, nil, store.ErrNotFound
	}
	if t.token.RevokedAt != nil {
		return store.TokenRecord{}, nil, store.ErrTokenRevoked
	}
	a, ok := s.agents[t.token.AgentID]
	if !ok {
		return store.TokenRecord{}, nil, store.ErrNotFound
	}
	return store.TokenRecord{
		Token:   t.token,
		Agent:   a,
		FleetID: a.FleetID,
	}, append([]byte(nil), t.hash...), nil
}

func (s *Store) TouchToken(_ context.Context, prefix string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.tokens[prefix]
	if !ok {
		return store.ErrNotFound
	}
	now := time.Now().UTC()
	t.token.LastUsedAt = &now
	s.tokens[prefix] = t
	return nil
}

// --- Proxy hosts ---

func (s *Store) CreateProxyHost(_ context.Context, p model.ProxyHost) (model.ProxyHost, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.fleets[p.FleetID]; !ok {
		return model.ProxyHost{}, store.ErrNotFound
	}
	for _, existing := range s.proxyHosts {
		if existing.FleetID == p.FleetID && existing.Name == p.Name {
			return model.ProxyHost{}, store.ErrConflict
		}
	}
	s.proxyHostSeq++
	p.ID = s.proxyHostSeq
	now := time.Now().UTC()
	p.CreatedAt = now
	p.UpdatedAt = now
	if p.Middlewares == nil {
		p.Middlewares = []model.Middleware{}
	}
	if p.EntryPoints == nil {
		p.EntryPoints = []string{"web"}
	}
	if p.Protocol == "" {
		p.Protocol = "http"
	}
	// Phase 7: legacy `upstream_url` ↔ new `upstream_urls`. Keep them
	// in sync so callers that still use the singular form work and the
	// compiler always sees the array form populated.
	if len(p.UpstreamURLs) == 0 && p.UpstreamURL != "" {
		p.UpstreamURLs = []string{p.UpstreamURL}
	}
	if p.UpstreamURL == "" && len(p.UpstreamURLs) > 0 {
		p.UpstreamURL = p.UpstreamURLs[0]
	}
	if p.UpstreamURLs == nil {
		p.UpstreamURLs = []string{}
	}
	s.proxyHosts[p.ID] = p
	return p, nil
}

func (s *Store) GetProxyHost(_ context.Context, fleetID string, id int64) (model.ProxyHost, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.proxyHosts[id]
	if !ok || p.FleetID != fleetID {
		return model.ProxyHost{}, store.ErrNotFound
	}
	return p, nil
}

func (s *Store) ListProxyHosts(_ context.Context, fleetID string) ([]model.ProxyHost, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []model.ProxyHost{}
	for _, p := range s.proxyHosts {
		if p.FleetID == fleetID {
			out = append(out, p)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func (s *Store) UpdateProxyHost(_ context.Context, p model.ProxyHost) (model.ProxyHost, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cur, ok := s.proxyHosts[p.ID]
	if !ok || cur.FleetID != p.FleetID {
		return model.ProxyHost{}, store.ErrNotFound
	}
	for _, existing := range s.proxyHosts {
		if existing.ID != p.ID && existing.FleetID == p.FleetID && existing.Name == p.Name {
			return model.ProxyHost{}, store.ErrConflict
		}
	}
	cur.Name = p.Name
	cur.Protocol = p.Protocol
	if cur.Protocol == "" {
		cur.Protocol = "http"
	}
	cur.Domain = p.Domain
	cur.UpstreamURL = p.UpstreamURL
	cur.UpstreamURLs = append([]string(nil), p.UpstreamURLs...)
	if len(cur.UpstreamURLs) == 0 && cur.UpstreamURL != "" {
		cur.UpstreamURLs = []string{cur.UpstreamURL}
	}
	if cur.UpstreamURL == "" && len(cur.UpstreamURLs) > 0 {
		cur.UpstreamURL = cur.UpstreamURLs[0]
	}
	cur.StickySession = p.StickySession
	cur.HealthCheck = p.HealthCheck
	cur.EntryPoints = append([]string(nil), p.EntryPoints...)
	cur.Middlewares = append([]model.Middleware(nil), p.Middlewares...)
	cur.TLS = p.TLS
	cur.LabelSelector = p.LabelSelector
	cur.Enabled = p.Enabled
	cur.UpdatedAt = time.Now().UTC()
	s.proxyHosts[p.ID] = cur
	return cur, nil
}

func (s *Store) DeleteProxyHost(_ context.Context, fleetID string, id int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.proxyHosts[id]
	if !ok || p.FleetID != fleetID {
		return store.ErrNotFound
	}
	delete(s.proxyHosts, id)
	return nil
}

// --- Middleware templates ---

func (s *Store) CreateMiddlewareTemplate(_ context.Context, t model.MiddlewareTemplate) (model.MiddlewareTemplate, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.fleets[t.FleetID]; !ok {
		return model.MiddlewareTemplate{}, store.ErrNotFound
	}
	for _, existing := range s.mwTemplates {
		if existing.FleetID == t.FleetID && existing.Name == t.Name {
			return model.MiddlewareTemplate{}, store.ErrConflict
		}
	}
	s.mwTemplateSeq++
	t.ID = s.mwTemplateSeq
	now := time.Now().UTC()
	t.CreatedAt = now
	t.UpdatedAt = now
	if t.Middlewares == nil {
		t.Middlewares = []model.Middleware{}
	}
	s.mwTemplates[t.ID] = t
	return t, nil
}

func (s *Store) GetMiddlewareTemplate(_ context.Context, fleetID string, id int64) (model.MiddlewareTemplate, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.mwTemplates[id]
	if !ok || t.FleetID != fleetID {
		return model.MiddlewareTemplate{}, store.ErrNotFound
	}
	return t, nil
}

func (s *Store) ListMiddlewareTemplates(_ context.Context, fleetID string) ([]model.MiddlewareTemplate, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []model.MiddlewareTemplate{}
	for _, t := range s.mwTemplates {
		if t.FleetID == fleetID {
			out = append(out, t)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func (s *Store) UpdateMiddlewareTemplate(_ context.Context, t model.MiddlewareTemplate) (model.MiddlewareTemplate, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cur, ok := s.mwTemplates[t.ID]
	if !ok || cur.FleetID != t.FleetID {
		return model.MiddlewareTemplate{}, store.ErrNotFound
	}
	for _, existing := range s.mwTemplates {
		if existing.ID != t.ID && existing.FleetID == t.FleetID && existing.Name == t.Name {
			return model.MiddlewareTemplate{}, store.ErrConflict
		}
	}
	cur.Name = t.Name
	cur.Description = t.Description
	cur.Middlewares = append([]model.Middleware(nil), t.Middlewares...)
	cur.UpdatedAt = time.Now().UTC()
	s.mwTemplates[t.ID] = cur
	return cur, nil
}

func (s *Store) DeleteMiddlewareTemplate(_ context.Context, fleetID string, id int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.mwTemplates[id]
	if !ok || t.FleetID != fleetID {
		return store.ErrNotFound
	}
	delete(s.mwTemplates, id)
	return nil
}

// --- Webhooks ---

func (s *Store) CreateWebhook(_ context.Context, w model.Webhook) (model.Webhook, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.fleets[w.FleetID]; !ok {
		return model.Webhook{}, store.ErrNotFound
	}
	for _, existing := range s.webhooks {
		if existing.FleetID == w.FleetID && existing.Name == w.Name {
			return model.Webhook{}, store.ErrConflict
		}
	}
	s.webhookSeq++
	w.ID = s.webhookSeq
	w.CreatedAt = time.Now().UTC()
	if w.Events == nil {
		w.Events = []string{"revision_published"}
	}
	w.HasSecret = w.Secret != ""
	s.webhooks[w.ID] = w
	return w, nil
}

func (s *Store) GetWebhook(_ context.Context, fleetID string, id int64) (model.Webhook, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	w, ok := s.webhooks[id]
	if !ok || w.FleetID != fleetID {
		return model.Webhook{}, store.ErrNotFound
	}
	return w, nil
}

func (s *Store) ListWebhooks(_ context.Context, fleetID string) ([]model.Webhook, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []model.Webhook{}
	for _, w := range s.webhooks {
		if w.FleetID == fleetID {
			out = append(out, w)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func (s *Store) ListEnabledWebhooks(_ context.Context, fleetID, event string) ([]model.Webhook, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []model.Webhook{}
	for _, w := range s.webhooks {
		if w.FleetID != fleetID || !w.Enabled {
			continue
		}
		for _, e := range w.Events {
			if e == event {
				out = append(out, w)
				break
			}
		}
	}
	return out, nil
}

func (s *Store) UpdateWebhook(_ context.Context, w model.Webhook) (model.Webhook, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cur, ok := s.webhooks[w.ID]
	if !ok || cur.FleetID != w.FleetID {
		return model.Webhook{}, store.ErrNotFound
	}
	for _, existing := range s.webhooks {
		if existing.ID != w.ID && existing.FleetID == w.FleetID && existing.Name == w.Name {
			return model.Webhook{}, store.ErrConflict
		}
	}
	cur.Name = w.Name
	cur.URL = w.URL
	if w.Secret != "" {
		// Empty secret on update means "leave existing"; non-empty
		// rotates. Mirrors the dns_providers pattern.
		cur.Secret = w.Secret
	}
	cur.HasSecret = cur.Secret != ""
	cur.Events = append([]string(nil), w.Events...)
	cur.Enabled = w.Enabled
	s.webhooks[w.ID] = cur
	return cur, nil
}

func (s *Store) DeleteWebhook(_ context.Context, fleetID string, id int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	w, ok := s.webhooks[id]
	if !ok || w.FleetID != fleetID {
		return store.ErrNotFound
	}
	delete(s.webhooks, id)
	return nil
}

func (s *Store) EnqueueWebhookJob(_ context.Context, j model.WebhookJob) (model.WebhookJob, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.webhookJobSeq++
	j.ID = s.webhookJobSeq
	j.Status = "pending"
	j.Attempts = 0
	j.CreatedAt = time.Now().UTC()
	if j.NextRunAt.IsZero() {
		j.NextRunAt = j.CreatedAt
	}
	s.webhookJobs[j.ID] = j
	return j, nil
}

func (s *Store) ClaimNextWebhookJob(_ context.Context) (model.WebhookJob, model.Webhook, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UTC()
	var picked *model.WebhookJob
	for id, j := range s.webhookJobs {
		if j.Status != "pending" {
			continue
		}
		if j.NextRunAt.After(now) {
			continue
		}
		jc := j
		picked = &jc
		picked.Status = "running"
		s.webhookJobs[id] = *picked
		break
	}
	if picked == nil {
		return model.WebhookJob{}, model.Webhook{}, store.ErrNotFound
	}
	w, ok := s.webhooks[picked.WebhookID]
	if !ok {
		return model.WebhookJob{}, model.Webhook{}, store.ErrNotFound
	}
	return *picked, w, nil
}

func (s *Store) FinishWebhookJob(_ context.Context, id int64, status, lastErr string, nextRunAt time.Time, attempts int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	j, ok := s.webhookJobs[id]
	if !ok {
		return store.ErrNotFound
	}
	j.Status = status
	j.LastError = lastErr
	j.Attempts = attempts
	j.NextRunAt = nextRunAt
	if status == "succeeded" || status == "failed" {
		t := time.Now().UTC()
		j.FinishedAt = &t
	}
	s.webhookJobs[id] = j
	return nil
}

// --- Revisions ---

func (s *Store) CreateRevision(_ context.Context, r model.Revision, makePublished bool) (model.Revision, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, ok := s.fleets[r.FleetID]
	if !ok {
		return model.Revision{}, store.ErrNotFound
	}
	for _, existing := range s.revisions {
		if existing.FleetID == r.FleetID && existing.Number == r.Number {
			return model.Revision{}, store.ErrConflict
		}
	}
	s.revisionSeq++
	r.ID = s.revisionSeq
	if r.GeneratedAt.IsZero() {
		r.GeneratedAt = time.Now().UTC()
	}
	if len(r.SourceProxyHosts) == 0 {
		r.SourceProxyHosts = []byte("[]")
	}
	if len(r.SourceCerts) == 0 {
		r.SourceCerts = []byte("[]")
	}
	s.revisions[r.ID] = r
	if makePublished {
		f.PublishedRevisionID = &r.ID
		s.fleets[r.FleetID] = f
	}
	return r, nil
}

func (s *Store) GetRevision(_ context.Context, fleetID string, number int) (model.Revision, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, r := range s.revisions {
		if r.FleetID == fleetID && r.Number == number {
			return r, nil
		}
	}
	return model.Revision{}, store.ErrNotFound
}

func (s *Store) ListRevisions(_ context.Context, fleetID string) ([]model.Revision, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []model.Revision{}
	for _, r := range s.revisions {
		if r.FleetID == fleetID {
			// don't blow up the list payload with full configs
			cleaned := r
			cleaned.CompiledConfig = nil
			cleaned.SourceProxyHosts = nil
			out = append(out, cleaned)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Number > out[j].Number })
	return out, nil
}

func (s *Store) GetPublishedRevision(_ context.Context, fleetID string) (model.Revision, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, ok := s.fleets[fleetID]
	if !ok {
		return model.Revision{}, store.ErrNotFound
	}
	if f.PublishedRevisionID == nil {
		return model.Revision{}, store.ErrNotFound
	}
	r, ok := s.revisions[*f.PublishedRevisionID]
	if !ok {
		return model.Revision{}, store.ErrNotFound
	}
	return r, nil
}

func (s *Store) SetPublishedRevision(_ context.Context, fleetID string, revisionID int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, ok := s.fleets[fleetID]
	if !ok {
		return store.ErrNotFound
	}
	r, ok := s.revisions[revisionID]
	if !ok || r.FleetID != fleetID {
		return store.ErrNotFound
	}
	f.PublishedRevisionID = &revisionID
	s.fleets[fleetID] = f
	return nil
}

func (s *Store) NextRevisionNumber(_ context.Context, fleetID string) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.fleets[fleetID]; !ok {
		return 0, store.ErrNotFound
	}
	max := 0
	for _, r := range s.revisions {
		if r.FleetID == fleetID && r.Number > max {
			max = r.Number
		}
	}
	return max + 1, nil
}

// --- Certificates ---

func (s *Store) CreateCertificate(_ context.Context, c model.Certificate) (model.Certificate, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.fleets[c.FleetID]; !ok {
		return model.Certificate{}, store.ErrNotFound
	}
	for _, existing := range s.certificates {
		if existing.FleetID == c.FleetID && existing.Name == c.Name {
			return model.Certificate{}, store.ErrConflict
		}
	}
	s.certificateSeq++
	c.ID = s.certificateSeq
	if c.CreatedAt.IsZero() {
		c.CreatedAt = time.Now().UTC()
	}
	s.certificates[c.ID] = c
	return c, nil
}

func (s *Store) GetCertificate(_ context.Context, fleetID string, id int64) (model.Certificate, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.certificates[id]
	if !ok || c.FleetID != fleetID {
		return model.Certificate{}, store.ErrNotFound
	}
	return c, nil
}

func (s *Store) ListCertificates(_ context.Context, fleetID string) ([]model.Certificate, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []model.Certificate{}
	for _, c := range s.certificates {
		if c.FleetID == fleetID {
			out = append(out, c)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func (s *Store) DeleteCertificate(_ context.Context, fleetID string, id int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.certificates[id]
	if !ok || c.FleetID != fleetID {
		return store.ErrNotFound
	}
	delete(s.certificates, id)
	return nil
}

func (s *Store) ListAllACMECertificates(_ context.Context) ([]model.Certificate, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []model.Certificate{}
	for _, c := range s.certificates {
		if c.Source == "acme" {
			out = append(out, c)
		}
	}
	return out, nil
}

func (s *Store) UpdateCertificateMaterial(_ context.Context, c model.Certificate) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	cur, ok := s.certificates[c.ID]
	if !ok || cur.FleetID != c.FleetID {
		return store.ErrNotFound
	}
	cur.CertPEM = c.CertPEM
	cur.KeyPEM = c.KeyPEM
	cur.Fingerprint = c.Fingerprint
	cur.Subject = c.Subject
	cur.Issuer = c.Issuer
	cur.DNSNames = append([]string(nil), c.DNSNames...)
	cur.NotBefore = c.NotBefore
	cur.NotAfter = c.NotAfter
	s.certificates[c.ID] = cur
	return nil
}

// --- ACME accounts ---

func (s *Store) UpsertACMEAccount(_ context.Context, a model.ACMEAccount) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.fleets[a.FleetID]; !ok {
		return store.ErrNotFound
	}
	if a.CreatedAt.IsZero() {
		a.CreatedAt = time.Now().UTC()
	}
	s.acmeAccounts[a.FleetID] = a
	return nil
}

func (s *Store) GetACMEAccount(_ context.Context, fleetID string) (model.ACMEAccount, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, ok := s.acmeAccounts[fleetID]
	if !ok {
		return model.ACMEAccount{}, store.ErrNotFound
	}
	return a, nil
}

func (s *Store) DeleteACMEAccount(_ context.Context, fleetID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.acmeAccounts[fleetID]; !ok {
		return store.ErrNotFound
	}
	delete(s.acmeAccounts, fleetID)
	return nil
}

// --- DNS providers ---

func (s *Store) CreateDNSProvider(_ context.Context, d model.DNSProvider) (model.DNSProvider, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.fleets[d.FleetID]; !ok {
		return model.DNSProvider{}, store.ErrNotFound
	}
	for _, existing := range s.dnsProviders {
		if existing.FleetID == d.FleetID && existing.Name == d.Name {
			return model.DNSProvider{}, store.ErrConflict
		}
	}
	s.dnsProviderSeq++
	d.ID = s.dnsProviderSeq
	if d.CreatedAt.IsZero() {
		d.CreatedAt = time.Now().UTC()
	}
	s.dnsProviders[d.ID] = d
	return d, nil
}

func (s *Store) GetDNSProvider(_ context.Context, fleetID string, id int64) (model.DNSProvider, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, ok := s.dnsProviders[id]
	if !ok || d.FleetID != fleetID {
		return model.DNSProvider{}, store.ErrNotFound
	}
	return d, nil
}

func (s *Store) GetDNSProviderByName(_ context.Context, fleetID, name string) (model.DNSProvider, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, d := range s.dnsProviders {
		if d.FleetID == fleetID && d.Name == name {
			return d, nil
		}
	}
	return model.DNSProvider{}, store.ErrNotFound
}

func (s *Store) ListDNSProviders(_ context.Context, fleetID string) ([]model.DNSProvider, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []model.DNSProvider{}
	for _, d := range s.dnsProviders {
		if d.FleetID == fleetID {
			out = append(out, d)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func (s *Store) DeleteDNSProvider(_ context.Context, fleetID string, id int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, ok := s.dnsProviders[id]
	if !ok || d.FleetID != fleetID {
		return store.ErrNotFound
	}
	delete(s.dnsProviders, id)
	return nil
}

// AssertConfigEqual is a small helper for tests; it's defined here so
// tests across packages don't all reimplement the same comparison.
func AssertConfigEqual(a, b []byte) bool {
	return bytes.Equal(bytes.TrimSpace(a), bytes.TrimSpace(b))
}
