// Package postgres is the production-backed Store implementation.
//
// Errors are normalized into the package-level sentinels in the parent
// store package (ErrNotFound, ErrConflict, ErrTokenRevoked) so handlers
// can map them to HTTP statuses without caring about driver specifics.
package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/danielfree19/proxydock/apps/api/internal/cryptokit"
	"github.com/danielfree19/proxydock/apps/api/internal/model"
	"github.com/danielfree19/proxydock/apps/api/internal/store"
)

type Store struct {
	pool   *pgxpool.Pool
	cipher *cryptokit.Cipher
}

// New wraps a pgx pool as a Store. cipher may be nil, in which case
// Reads of legacy plaintext rows still work but writes do not encrypt.
// Production deployments should always supply a cipher.
func New(pool *pgxpool.Pool, cipher *cryptokit.Cipher) *Store {
	return &Store{pool: pool, cipher: cipher}
}

// encStr / decStr / decStrBytes wrap cryptokit operations so the rest
// of this file reads cleanly. They return errors only when the underlying
// cipher fails on a tagged ciphertext.
func (s *Store) encStr(plain string) (string, error) {
	if s.cipher == nil {
		return plain, nil
	}
	return s.cipher.Encrypt(plain)
}

func (s *Store) decStr(stored string) (string, error) {
	return s.cipher.Decrypt(stored)
}

func (s *Store) encBytes(plain []byte) ([]byte, error) {
	if s.cipher == nil {
		return plain, nil
	}
	return s.cipher.EncryptBytes(plain)
}

func (s *Store) decBytes(stored []byte) ([]byte, error) {
	return s.cipher.DecryptBytes(stored)
}

// mapErr translates pgx / pg errors into the store sentinel set.
//
// Anything that isn't a known constraint violation falls through as the
// raw pgx error so the caller still sees the original message in logs.
func mapErr(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return store.ErrNotFound
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "23505":
			return fmt.Errorf("%w: %s", store.ErrConflict, pgErr.ConstraintName)
		case "23503":
			return fmt.Errorf("%w: %s", store.ErrNotFound, pgErr.ConstraintName)
		}
	}
	return err
}

// --- Fleets ---

func (s *Store) CreateFleet(ctx context.Context, f model.Fleet) (model.Fleet, error) {
	if f.CreatedAt.IsZero() {
		f.CreatedAt = time.Now().UTC()
	}
	row := s.pool.QueryRow(ctx, `
		INSERT INTO fleets (id, name, created_at)
		VALUES ($1, $2, $3)
		RETURNING id, name, published_revision_id, created_at`,
		f.ID, f.Name, f.CreatedAt)
	if err := scanFleet(row, &f); err != nil {
		return model.Fleet{}, mapErr(err)
	}
	return f, nil
}

func (s *Store) GetFleet(ctx context.Context, id string) (model.Fleet, error) {
	var f model.Fleet
	row := s.pool.QueryRow(ctx, `
		SELECT id, name, published_revision_id, created_at
		FROM fleets WHERE id = $1`, id)
	if err := scanFleet(row, &f); err != nil {
		return model.Fleet{}, mapErr(err)
	}
	return f, nil
}

func (s *Store) ListFleets(ctx context.Context) ([]model.Fleet, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, name, published_revision_id, created_at
		FROM fleets ORDER BY id`)
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()
	out := []model.Fleet{}
	for rows.Next() {
		var f model.Fleet
		if err := scanFleet(rows, &f); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

func (s *Store) DeleteFleet(ctx context.Context, id string) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM fleets WHERE id = $1`, id)
	if err != nil {
		return mapErr(err)
	}
	if tag.RowsAffected() == 0 {
		return store.ErrNotFound
	}
	return nil
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanFleet(r rowScanner, f *model.Fleet) error {
	var pubID *int64
	if err := r.Scan(&f.ID, &f.Name, &pubID, &f.CreatedAt); err != nil {
		return err
	}
	f.PublishedRevisionID = pubID
	return nil
}

// --- Agents ---

func (s *Store) CreateAgent(ctx context.Context, a model.Agent) (model.Agent, error) {
	if a.CreatedAt.IsZero() {
		a.CreatedAt = time.Now().UTC()
	}
	if a.Labels == nil {
		a.Labels = []string{}
	}
	row := s.pool.QueryRow(ctx, `
		INSERT INTO agents (id, fleet_id, name, labels, created_at)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING `+agentSelectInner,
		a.ID, a.FleetID, a.Name, a.Labels, a.CreatedAt)
	if err := scanAgent(row, &a); err != nil {
		return model.Agent{}, mapErr(err)
	}
	return a, nil
}

func (s *Store) GetAgent(ctx context.Context, id string) (model.Agent, error) {
	var a model.Agent
	row := s.pool.QueryRow(ctx, agentSelect+` WHERE id = $1`, id)
	if err := scanAgent(row, &a); err != nil {
		return model.Agent{}, mapErr(err)
	}
	return a, nil
}

func (s *Store) ListAgents(ctx context.Context, fleetID string) ([]model.Agent, error) {
	rows, err := s.pool.Query(ctx, agentSelect+` WHERE fleet_id = $1 ORDER BY id`, fleetID)
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()
	out := []model.Agent{}
	for rows.Next() {
		var a model.Agent
		if err := scanAgent(rows, &a); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *Store) DeleteAgent(ctx context.Context, id string) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM agents WHERE id = $1`, id)
	if err != nil {
		return mapErr(err)
	}
	if tag.RowsAffected() == 0 {
		return store.ErrNotFound
	}
	return nil
}

func (s *Store) UpdateAgentLabels(ctx context.Context, agentID string, labels []string) error {
	if labels == nil {
		labels = []string{}
	}
	tag, err := s.pool.Exec(ctx,
		`UPDATE agents SET labels = $2 WHERE id = $1`, agentID, labels)
	if err != nil {
		return mapErr(err)
	}
	if tag.RowsAffected() == 0 {
		return store.ErrNotFound
	}
	return nil
}

func (s *Store) UpdateAgentHeartbeat(ctx context.Context, agentID string, hb store.HeartbeatUpdate) error {
	var lastErr *string
	if hb.LastError != "" {
		v := hb.LastError
		lastErr = &v
	}
	tag, err := s.pool.Exec(ctx, `
		UPDATE agents
		SET last_heartbeat_at = now(),
		    last_revision_seen = $2,
		    last_provider_version = $3,
		    last_traefik_version = $4,
		    last_error = $5
		WHERE id = $1`,
		agentID, hb.CurrentRevision, hb.ProviderVersion, hb.TraefikVersion, lastErr)
	if err != nil {
		return mapErr(err)
	}
	if tag.RowsAffected() == 0 {
		return store.ErrNotFound
	}
	return nil
}

const agentSelectInner = `id, fleet_id, name, labels,
	       last_heartbeat_at, last_revision_seen,
	       last_provider_version, last_traefik_version, last_error,
	       created_at`

const agentSelect = `SELECT ` + agentSelectInner + ` FROM agents`

func scanAgent(r rowScanner, a *model.Agent) error {
	if err := r.Scan(
		&a.ID, &a.FleetID, &a.Name, &a.Labels,
		&a.LastHeartbeatAt, &a.LastRevisionSeen,
		&a.LastProviderVersion, &a.LastTraefikVersion, &a.LastError,
		&a.CreatedAt,
	); err != nil {
		return err
	}
	if a.Labels == nil {
		a.Labels = []string{}
	}
	return nil
}

// --- Tokens ---

func (s *Store) MintToken(ctx context.Context, agentID, name, prefix string, secretHash []byte) (model.AgentToken, error) {
	row := s.pool.QueryRow(ctx, `
		INSERT INTO agent_tokens (prefix, agent_id, secret_hash, name)
		VALUES ($1, $2, $3, NULLIF($4, ''))
		RETURNING prefix, agent_id, COALESCE(name, ''), created_at, last_used_at, revoked_at`,
		prefix, agentID, secretHash, name)
	var t model.AgentToken
	if err := row.Scan(&t.Prefix, &t.AgentID, &t.Name, &t.CreatedAt, &t.LastUsedAt, &t.RevokedAt); err != nil {
		return model.AgentToken{}, mapErr(err)
	}
	return t, nil
}

func (s *Store) ListTokens(ctx context.Context, agentID string) ([]model.AgentToken, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT prefix, agent_id, COALESCE(name, ''), created_at, last_used_at, revoked_at
		FROM agent_tokens WHERE agent_id = $1 ORDER BY created_at`, agentID)
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()
	out := []model.AgentToken{}
	for rows.Next() {
		var t model.AgentToken
		if err := rows.Scan(&t.Prefix, &t.AgentID, &t.Name, &t.CreatedAt, &t.LastUsedAt, &t.RevokedAt); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (s *Store) RevokeToken(ctx context.Context, agentID, prefix string) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE agent_tokens SET revoked_at = now()
		WHERE prefix = $1 AND agent_id = $2 AND revoked_at IS NULL`, prefix, agentID)
	if err != nil {
		return mapErr(err)
	}
	if tag.RowsAffected() == 0 {
		return store.ErrNotFound
	}
	return nil
}

func (s *Store) LookupToken(ctx context.Context, prefix string) (store.TokenRecord, []byte, error) {
	row := s.pool.QueryRow(ctx, `
		SELECT t.prefix, t.agent_id, COALESCE(t.name, ''), t.created_at, t.last_used_at, t.revoked_at,
		       t.secret_hash,
		       a.id, a.fleet_id, a.name, a.last_heartbeat_at, a.last_revision_seen,
		       a.last_provider_version, a.last_traefik_version, a.last_error, a.created_at
		FROM agent_tokens t JOIN agents a ON a.id = t.agent_id
		WHERE t.prefix = $1`, prefix)
	var (
		t    model.AgentToken
		hash []byte
		a    model.Agent
	)
	err := row.Scan(
		&t.Prefix, &t.AgentID, &t.Name, &t.CreatedAt, &t.LastUsedAt, &t.RevokedAt,
		&hash,
		&a.ID, &a.FleetID, &a.Name, &a.LastHeartbeatAt, &a.LastRevisionSeen,
		&a.LastProviderVersion, &a.LastTraefikVersion, &a.LastError, &a.CreatedAt,
	)
	if err != nil {
		return store.TokenRecord{}, nil, mapErr(err)
	}
	if t.RevokedAt != nil {
		return store.TokenRecord{}, nil, store.ErrTokenRevoked
	}
	return store.TokenRecord{Token: t, Agent: a, FleetID: a.FleetID}, hash, nil
}

func (s *Store) TouchToken(ctx context.Context, prefix string) error {
	_, err := s.pool.Exec(ctx, `UPDATE agent_tokens SET last_used_at = now() WHERE prefix = $1`, prefix)
	return mapErr(err)
}

// --- Proxy hosts ---

func (s *Store) CreateProxyHost(ctx context.Context, p model.ProxyHost) (model.ProxyHost, error) {
	mw, err := json.Marshal(orEmptyMiddlewares(p.Middlewares))
	if err != nil {
		return model.ProxyHost{}, err
	}
	urls := normalizeUpstreams(p.UpstreamURL, p.UpstreamURLs)
	hc, err := json.Marshal(orEmptyHealthCheck(p.HealthCheck))
	if err != nil {
		return model.ProxyHost{}, err
	}
	row := s.pool.QueryRow(ctx, `
		INSERT INTO proxy_hosts (fleet_id, name, protocol, domain, upstream_url, upstream_urls, sticky_session, health_check, entry_points, middlewares, tls, label_selector, enabled)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8::jsonb, $9, $10::jsonb, $11, $12, $13)
		RETURNING `+proxyHostCols,
		p.FleetID, p.Name, defaultProtocol(p.Protocol),
		p.Domain, firstOrEmpty(urls), urls, p.StickySession, hc,
		orWebEntryPoints(p.EntryPoints), mw, p.TLS, p.LabelSelector, p.Enabled)
	if err := scanProxyHost(row, &p); err != nil {
		return model.ProxyHost{}, mapErr(err)
	}
	return p, nil
}

func (s *Store) GetProxyHost(ctx context.Context, fleetID string, id int64) (model.ProxyHost, error) {
	var p model.ProxyHost
	row := s.pool.QueryRow(ctx,
		`SELECT `+proxyHostCols+` FROM proxy_hosts WHERE fleet_id = $1 AND id = $2`,
		fleetID, id)
	if err := scanProxyHost(row, &p); err != nil {
		return model.ProxyHost{}, mapErr(err)
	}
	return p, nil
}

func (s *Store) ListProxyHosts(ctx context.Context, fleetID string) ([]model.ProxyHost, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT `+proxyHostCols+` FROM proxy_hosts WHERE fleet_id = $1 ORDER BY name`,
		fleetID)
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()
	out := []model.ProxyHost{}
	for rows.Next() {
		var p model.ProxyHost
		if err := scanProxyHost(rows, &p); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) UpdateProxyHost(ctx context.Context, p model.ProxyHost) (model.ProxyHost, error) {
	mw, err := json.Marshal(orEmptyMiddlewares(p.Middlewares))
	if err != nil {
		return model.ProxyHost{}, err
	}
	urls := normalizeUpstreams(p.UpstreamURL, p.UpstreamURLs)
	hc, err := json.Marshal(orEmptyHealthCheck(p.HealthCheck))
	if err != nil {
		return model.ProxyHost{}, err
	}
	row := s.pool.QueryRow(ctx, `
		UPDATE proxy_hosts SET
		    name = $3,
		    protocol = $4,
		    domain = $5,
		    upstream_url = $6,
		    upstream_urls = $7,
		    sticky_session = $8,
		    health_check = $9::jsonb,
		    entry_points = $10,
		    middlewares = $11::jsonb,
		    tls = $12,
		    label_selector = $13,
		    enabled = $14,
		    updated_at = now()
		WHERE fleet_id = $1 AND id = $2
		RETURNING `+proxyHostCols,
		p.FleetID, p.ID, p.Name, defaultProtocol(p.Protocol),
		p.Domain, firstOrEmpty(urls), urls, p.StickySession, hc,
		orWebEntryPoints(p.EntryPoints), mw, p.TLS, p.LabelSelector, p.Enabled)
	if err := scanProxyHost(row, &p); err != nil {
		return model.ProxyHost{}, mapErr(err)
	}
	return p, nil
}

func (s *Store) DeleteProxyHost(ctx context.Context, fleetID string, id int64) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM proxy_hosts WHERE fleet_id = $1 AND id = $2`, fleetID, id)
	if err != nil {
		return mapErr(err)
	}
	if tag.RowsAffected() == 0 {
		return store.ErrNotFound
	}
	return nil
}

const proxyHostCols = `id, fleet_id, name, protocol, domain, upstream_url, upstream_urls, sticky_session, health_check, entry_points, middlewares, tls, label_selector, enabled, created_at, updated_at`

// --- Middleware templates ---

const mwTemplateCols = `id, fleet_id, name, description, middlewares, created_at, updated_at`

func (s *Store) CreateMiddlewareTemplate(ctx context.Context, t model.MiddlewareTemplate) (model.MiddlewareTemplate, error) {
	mw, err := json.Marshal(orEmptyMiddlewares(t.Middlewares))
	if err != nil {
		return model.MiddlewareTemplate{}, err
	}
	row := s.pool.QueryRow(ctx, `
		INSERT INTO middleware_templates (fleet_id, name, description, middlewares)
		VALUES ($1, $2, $3, $4::jsonb)
		RETURNING `+mwTemplateCols,
		t.FleetID, t.Name, t.Description, mw)
	if err := scanMWTemplate(row, &t); err != nil {
		return model.MiddlewareTemplate{}, mapErr(err)
	}
	return t, nil
}

func (s *Store) GetMiddlewareTemplate(ctx context.Context, fleetID string, id int64) (model.MiddlewareTemplate, error) {
	var t model.MiddlewareTemplate
	row := s.pool.QueryRow(ctx,
		`SELECT `+mwTemplateCols+` FROM middleware_templates WHERE fleet_id = $1 AND id = $2`,
		fleetID, id)
	if err := scanMWTemplate(row, &t); err != nil {
		return model.MiddlewareTemplate{}, mapErr(err)
	}
	return t, nil
}

func (s *Store) ListMiddlewareTemplates(ctx context.Context, fleetID string) ([]model.MiddlewareTemplate, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT `+mwTemplateCols+` FROM middleware_templates WHERE fleet_id = $1 ORDER BY name`,
		fleetID)
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()
	out := []model.MiddlewareTemplate{}
	for rows.Next() {
		var t model.MiddlewareTemplate
		if err := scanMWTemplate(rows, &t); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (s *Store) UpdateMiddlewareTemplate(ctx context.Context, t model.MiddlewareTemplate) (model.MiddlewareTemplate, error) {
	mw, err := json.Marshal(orEmptyMiddlewares(t.Middlewares))
	if err != nil {
		return model.MiddlewareTemplate{}, err
	}
	row := s.pool.QueryRow(ctx, `
		UPDATE middleware_templates SET
		    name = $3,
		    description = $4,
		    middlewares = $5::jsonb,
		    updated_at = now()
		WHERE fleet_id = $1 AND id = $2
		RETURNING `+mwTemplateCols,
		t.FleetID, t.ID, t.Name, t.Description, mw)
	if err := scanMWTemplate(row, &t); err != nil {
		return model.MiddlewareTemplate{}, mapErr(err)
	}
	return t, nil
}

func (s *Store) DeleteMiddlewareTemplate(ctx context.Context, fleetID string, id int64) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM middleware_templates WHERE fleet_id = $1 AND id = $2`, fleetID, id)
	if err != nil {
		return mapErr(err)
	}
	if tag.RowsAffected() == 0 {
		return store.ErrNotFound
	}
	return nil
}

// --- Webhooks (Phase 7) ---

const webhookCols = `id, fleet_id, name, url, secret, events, enabled, created_at`

func (s *Store) CreateWebhook(ctx context.Context, w model.Webhook) (model.Webhook, error) {
	if w.Events == nil || len(w.Events) == 0 {
		w.Events = []string{"revision_published"}
	}
	encSecret, err := s.encStr(w.Secret)
	if err != nil {
		return model.Webhook{}, err
	}
	row := s.pool.QueryRow(ctx, `
		INSERT INTO webhooks (fleet_id, name, url, secret, events, enabled)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING `+webhookCols,
		w.FleetID, w.Name, w.URL, encSecret, w.Events, w.Enabled)
	if err := scanWebhook(s, row, &w); err != nil {
		return model.Webhook{}, mapErr(err)
	}
	return w, nil
}

func (s *Store) GetWebhook(ctx context.Context, fleetID string, id int64) (model.Webhook, error) {
	var w model.Webhook
	row := s.pool.QueryRow(ctx,
		`SELECT `+webhookCols+` FROM webhooks WHERE fleet_id = $1 AND id = $2`,
		fleetID, id)
	if err := scanWebhook(s, row, &w); err != nil {
		return model.Webhook{}, mapErr(err)
	}
	return w, nil
}

func (s *Store) ListWebhooks(ctx context.Context, fleetID string) ([]model.Webhook, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT `+webhookCols+` FROM webhooks WHERE fleet_id = $1 ORDER BY name`, fleetID)
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()
	out := []model.Webhook{}
	for rows.Next() {
		var w model.Webhook
		if err := scanWebhook(s, rows, &w); err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

func (s *Store) ListEnabledWebhooks(ctx context.Context, fleetID, event string) ([]model.Webhook, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT `+webhookCols+` FROM webhooks WHERE fleet_id = $1 AND enabled = TRUE AND $2 = ANY(events)`,
		fleetID, event)
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()
	out := []model.Webhook{}
	for rows.Next() {
		var w model.Webhook
		if err := scanWebhook(s, rows, &w); err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

func (s *Store) UpdateWebhook(ctx context.Context, w model.Webhook) (model.Webhook, error) {
	// Empty Secret on update means "leave existing" — mirrors the
	// dns_providers convention. Non-empty rotates.
	var row pgx.Row
	if w.Secret == "" {
		row = s.pool.QueryRow(ctx, `
			UPDATE webhooks SET name=$3, url=$4, events=$5, enabled=$6
			WHERE fleet_id=$1 AND id=$2
			RETURNING `+webhookCols,
			w.FleetID, w.ID, w.Name, w.URL, w.Events, w.Enabled)
	} else {
		encSecret, encErr := s.encStr(w.Secret)
		if encErr != nil {
			return model.Webhook{}, encErr
		}
		row = s.pool.QueryRow(ctx, `
			UPDATE webhooks SET name=$3, url=$4, secret=$5, events=$6, enabled=$7
			WHERE fleet_id=$1 AND id=$2
			RETURNING `+webhookCols,
			w.FleetID, w.ID, w.Name, w.URL, encSecret, w.Events, w.Enabled)
	}
	if err := scanWebhook(s, row, &w); err != nil {
		return model.Webhook{}, mapErr(err)
	}
	return w, nil
}

func (s *Store) DeleteWebhook(ctx context.Context, fleetID string, id int64) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM webhooks WHERE fleet_id=$1 AND id=$2`, fleetID, id)
	if err != nil {
		return mapErr(err)
	}
	if tag.RowsAffected() == 0 {
		return store.ErrNotFound
	}
	return nil
}

func scanWebhook(s *Store, r rowScanner, w *model.Webhook) error {
	var encSecret string
	if err := r.Scan(&w.ID, &w.FleetID, &w.Name, &w.URL, &encSecret, &w.Events, &w.Enabled, &w.CreatedAt); err != nil {
		return err
	}
	plaintext, err := s.decStr(encSecret)
	if err != nil {
		return fmt.Errorf("decrypt webhook secret: %w", err)
	}
	w.Secret = plaintext
	w.HasSecret = plaintext != ""
	if w.Events == nil {
		w.Events = []string{}
	}
	return nil
}

// --- Webhook jobs ---

const webhookJobCols = `id, webhook_id, payload, status, attempts, next_run_at, last_error, created_at, finished_at`

func (s *Store) EnqueueWebhookJob(ctx context.Context, j model.WebhookJob) (model.WebhookJob, error) {
	row := s.pool.QueryRow(ctx, `
		INSERT INTO webhook_jobs (webhook_id, payload)
		VALUES ($1, $2)
		RETURNING `+webhookJobCols,
		j.WebhookID, j.Payload)
	if err := scanWebhookJob(row, &j); err != nil {
		return model.WebhookJob{}, mapErr(err)
	}
	return j, nil
}

func (s *Store) ClaimNextWebhookJob(ctx context.Context) (model.WebhookJob, model.Webhook, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return model.WebhookJob{}, model.Webhook{}, err
	}
	defer tx.Rollback(ctx)

	var j model.WebhookJob
	// FOR UPDATE SKIP LOCKED lets multiple worker replicas share the
	// queue without external coordination — same shape as acme_jobs.
	row := tx.QueryRow(ctx, `
		SELECT `+webhookJobCols+`
		FROM webhook_jobs
		WHERE status = 'pending' AND next_run_at <= now()
		ORDER BY next_run_at ASC
		LIMIT 1
		FOR UPDATE SKIP LOCKED`)
	if err := scanWebhookJob(row, &j); err != nil {
		return model.WebhookJob{}, model.Webhook{}, mapErr(err)
	}
	_, err = tx.Exec(ctx, `UPDATE webhook_jobs SET status = 'running' WHERE id = $1`, j.ID)
	if err != nil {
		return model.WebhookJob{}, model.Webhook{}, err
	}

	var w model.Webhook
	wRow := tx.QueryRow(ctx, `SELECT `+webhookCols+` FROM webhooks WHERE id = $1`, j.WebhookID)
	if err := scanWebhook(s, wRow, &w); err != nil {
		return model.WebhookJob{}, model.Webhook{}, mapErr(err)
	}

	if err := tx.Commit(ctx); err != nil {
		return model.WebhookJob{}, model.Webhook{}, err
	}
	j.Status = "running"
	return j, w, nil
}

func (s *Store) FinishWebhookJob(ctx context.Context, id int64, status, lastErr string, nextRunAt time.Time, attempts int) error {
	finishedClause := ""
	if status == "succeeded" || status == "failed" {
		finishedClause = ", finished_at = now()"
	}
	tag, err := s.pool.Exec(ctx, `
		UPDATE webhook_jobs SET status=$2, last_error=$3, next_run_at=$4, attempts=$5`+finishedClause+`
		WHERE id=$1`,
		id, status, lastErr, nextRunAt, attempts)
	if err != nil {
		return mapErr(err)
	}
	if tag.RowsAffected() == 0 {
		return store.ErrNotFound
	}
	return nil
}

func scanWebhookJob(r rowScanner, j *model.WebhookJob) error {
	var finishedAt *time.Time
	if err := r.Scan(&j.ID, &j.WebhookID, &j.Payload, &j.Status, &j.Attempts,
		&j.NextRunAt, &j.LastError, &j.CreatedAt, &finishedAt); err != nil {
		return err
	}
	j.FinishedAt = finishedAt
	return nil
}

func scanMWTemplate(r rowScanner, t *model.MiddlewareTemplate) error {
	var mwBytes []byte
	if err := r.Scan(
		&t.ID, &t.FleetID, &t.Name, &t.Description, &mwBytes,
		&t.CreatedAt, &t.UpdatedAt,
	); err != nil {
		return err
	}
	if len(mwBytes) == 0 {
		t.Middlewares = []model.Middleware{}
		return nil
	}
	if err := json.Unmarshal(mwBytes, &t.Middlewares); err != nil {
		return fmt.Errorf("decode template middlewares: %w", err)
	}
	if t.Middlewares == nil {
		t.Middlewares = []model.Middleware{}
	}
	return nil
}

// defaultProtocol normalizes the empty string to "http" so legacy
// rows (and any caller that forgets the field) round-trip cleanly
// through the CHECK constraint.
func defaultProtocol(p string) string {
	if p == "" {
		return "http"
	}
	return p
}

func scanProxyHost(r rowScanner, p *model.ProxyHost) error {
	var mwBytes, hcBytes []byte
	if err := r.Scan(
		&p.ID, &p.FleetID, &p.Name, &p.Protocol, &p.Domain,
		&p.UpstreamURL, &p.UpstreamURLs, &p.StickySession, &hcBytes,
		&p.EntryPoints, &mwBytes, &p.TLS, &p.LabelSelector, &p.Enabled,
		&p.CreatedAt, &p.UpdatedAt,
	); err != nil {
		return err
	}
	if p.UpstreamURLs == nil {
		p.UpstreamURLs = []string{}
	}
	if len(hcBytes) > 0 {
		if err := json.Unmarshal(hcBytes, &p.HealthCheck); err != nil {
			return fmt.Errorf("decode health_check: %w", err)
		}
	}
	if len(mwBytes) == 0 {
		p.Middlewares = []model.Middleware{}
		return nil
	}
	if err := json.Unmarshal(mwBytes, &p.Middlewares); err != nil {
		return fmt.Errorf("decode middlewares: %w", err)
	}
	if p.Middlewares == nil {
		p.Middlewares = []model.Middleware{}
	}
	return nil
}

func orEmptyHealthCheck(hc map[string]any) map[string]any {
	if hc == nil {
		return map[string]any{}
	}
	return hc
}

// normalizeUpstreams resolves the legacy single-URL field against the
// new array. If the array is non-empty it wins. Otherwise we promote
// the single URL into a one-element array. Always returns a non-nil
// slice so the postgres TEXT[] column gets `'{}'` rather than NULL.
func normalizeUpstreams(legacy string, urls []string) []string {
	out := make([]string, 0, len(urls)+1)
	for _, u := range urls {
		if u != "" {
			out = append(out, u)
		}
	}
	if len(out) == 0 && legacy != "" {
		out = append(out, legacy)
	}
	return out
}

func firstOrEmpty(urls []string) string {
	if len(urls) == 0 {
		return ""
	}
	return urls[0]
}

func orEmptyMiddlewares(mw []model.Middleware) []model.Middleware {
	if mw == nil {
		return []model.Middleware{}
	}
	return mw
}

func orWebEntryPoints(ep []string) []string {
	if len(ep) == 0 {
		return []string{"web"}
	}
	return ep
}

// --- Revisions ---

func (s *Store) CreateRevision(ctx context.Context, r model.Revision, makePublished bool) (model.Revision, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return model.Revision{}, mapErr(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if r.GeneratedAt.IsZero() {
		r.GeneratedAt = time.Now().UTC()
	}
	if len(r.SourceProxyHosts) == 0 {
		r.SourceProxyHosts = []byte("[]")
	}
	if len(r.SourceCerts) == 0 {
		r.SourceCerts = []byte("[]")
	}
	row := tx.QueryRow(ctx, `
		INSERT INTO config_revisions
		    (fleet_id, number, compiled_config, source_proxy_hosts, source_certs, etag, notes, signature, signature_alg, generated_at)
		VALUES ($1, $2, $3, $4, $5, $6, NULLIF($7, ''), $8, $9, $10)
		RETURNING `+revisionCols,
		r.FleetID, r.Number, string(r.CompiledConfig),
		string(r.SourceProxyHosts), string(r.SourceCerts),
		r.ETag, r.Notes, r.Signature, r.SignatureAlg, r.GeneratedAt)
	if err := scanRevision(row, &r); err != nil {
		return model.Revision{}, mapErr(err)
	}
	if makePublished {
		if _, err := tx.Exec(ctx,
			`UPDATE fleets SET published_revision_id = $1 WHERE id = $2`,
			r.ID, r.FleetID); err != nil {
			return model.Revision{}, mapErr(err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return model.Revision{}, mapErr(err)
	}
	return r, nil
}

func (s *Store) GetRevision(ctx context.Context, fleetID string, number int) (model.Revision, error) {
	var r model.Revision
	row := s.pool.QueryRow(ctx,
		`SELECT `+revisionCols+` FROM config_revisions WHERE fleet_id = $1 AND number = $2`,
		fleetID, number)
	if err := scanRevision(row, &r); err != nil {
		return model.Revision{}, mapErr(err)
	}
	return r, nil
}

func (s *Store) ListRevisions(ctx context.Context, fleetID string) ([]model.Revision, error) {
	// list view excludes the heavy config bytes
	rows, err := s.pool.Query(ctx, `
		SELECT id, fleet_id, number, etag, COALESCE(notes, ''),
		       COALESCE(signature, ''), COALESCE(signature_alg, ''),
		       generated_at
		FROM config_revisions WHERE fleet_id = $1 ORDER BY number DESC`, fleetID)
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()
	out := []model.Revision{}
	for rows.Next() {
		var r model.Revision
		if err := rows.Scan(&r.ID, &r.FleetID, &r.Number, &r.ETag, &r.Notes,
			&r.Signature, &r.SignatureAlg, &r.GeneratedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) GetPublishedRevision(ctx context.Context, fleetID string) (model.Revision, error) {
	var r model.Revision
	row := s.pool.QueryRow(ctx, `
		SELECT `+prefixedRevisionCols+`
		FROM config_revisions r JOIN fleets f ON f.published_revision_id = r.id
		WHERE f.id = $1`, fleetID)
	if err := scanRevision(row, &r); err != nil {
		return model.Revision{}, mapErr(err)
	}
	return r, nil
}

func (s *Store) SetPublishedRevision(ctx context.Context, fleetID string, revisionID int64) error {
	// confirm revision belongs to fleet
	var ok bool
	if err := s.pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM config_revisions WHERE id = $1 AND fleet_id = $2)`,
		revisionID, fleetID).Scan(&ok); err != nil {
		return mapErr(err)
	}
	if !ok {
		return store.ErrNotFound
	}
	tag, err := s.pool.Exec(ctx,
		`UPDATE fleets SET published_revision_id = $1 WHERE id = $2`,
		revisionID, fleetID)
	if err != nil {
		return mapErr(err)
	}
	if tag.RowsAffected() == 0 {
		return store.ErrNotFound
	}
	return nil
}

func (s *Store) NextRevisionNumber(ctx context.Context, fleetID string) (int, error) {
	var ok bool
	if err := s.pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM fleets WHERE id = $1)`, fleetID).Scan(&ok); err != nil {
		return 0, mapErr(err)
	}
	if !ok {
		return 0, store.ErrNotFound
	}
	var n *int
	if err := s.pool.QueryRow(ctx,
		`SELECT MAX(number) FROM config_revisions WHERE fleet_id = $1`, fleetID).Scan(&n); err != nil {
		return 0, mapErr(err)
	}
	if n == nil {
		return 1, nil
	}
	return *n + 1, nil
}

// --- Certificates ---

func (s *Store) CreateCertificate(ctx context.Context, c model.Certificate) (model.Certificate, error) {
	if c.CreatedAt.IsZero() {
		c.CreatedAt = time.Now().UTC()
	}
	if c.Source == "" {
		c.Source = "upload"
	}
	encKey, err := s.encStr(c.KeyPEM)
	if err != nil {
		return model.Certificate{}, fmt.Errorf("encrypt key_pem: %w", err)
	}
	row := s.pool.QueryRow(ctx, `
		INSERT INTO certificates
		    (fleet_id, name, cert_pem, key_pem, fingerprint, subject, issuer, dns_names, not_before, not_after, source, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
		RETURNING `+certCols,
		c.FleetID, c.Name, c.CertPEM, encKey, c.Fingerprint,
		c.Subject, c.Issuer, c.DNSNames, c.NotBefore, c.NotAfter, c.Source, c.CreatedAt)
	if err := s.scanCertificate(row, &c); err != nil {
		return model.Certificate{}, mapErr(err)
	}
	return c, nil
}

func (s *Store) UpdateCertificateMaterial(ctx context.Context, c model.Certificate) error {
	encKey, err := s.encStr(c.KeyPEM)
	if err != nil {
		return fmt.Errorf("encrypt key_pem: %w", err)
	}
	tag, err := s.pool.Exec(ctx, `
		UPDATE certificates SET
		    cert_pem = $3, key_pem = $4, fingerprint = $5,
		    subject = $6, issuer = $7, dns_names = $8,
		    not_before = $9, not_after = $10
		WHERE fleet_id = $1 AND id = $2`,
		c.FleetID, c.ID, c.CertPEM, encKey, c.Fingerprint,
		c.Subject, c.Issuer, c.DNSNames, c.NotBefore, c.NotAfter)
	if err != nil {
		return mapErr(err)
	}
	if tag.RowsAffected() == 0 {
		return store.ErrNotFound
	}
	return nil
}

func (s *Store) ListAllACMECertificates(ctx context.Context) ([]model.Certificate, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT `+certCols+` FROM certificates WHERE source = 'acme' ORDER BY fleet_id, name`)
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()
	out := []model.Certificate{}
	for rows.Next() {
		var c model.Certificate
		if err := s.scanCertificate(rows, &c); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Store) GetCertificate(ctx context.Context, fleetID string, id int64) (model.Certificate, error) {
	var c model.Certificate
	row := s.pool.QueryRow(ctx,
		`SELECT `+certCols+` FROM certificates WHERE fleet_id = $1 AND id = $2`,
		fleetID, id)
	if err := s.scanCertificate(row, &c); err != nil {
		return model.Certificate{}, mapErr(err)
	}
	return c, nil
}

func (s *Store) ListCertificates(ctx context.Context, fleetID string) ([]model.Certificate, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT `+certCols+` FROM certificates WHERE fleet_id = $1 ORDER BY name`,
		fleetID)
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()
	out := []model.Certificate{}
	for rows.Next() {
		var c model.Certificate
		if err := s.scanCertificate(rows, &c); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Store) DeleteCertificate(ctx context.Context, fleetID string, id int64) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM certificates WHERE fleet_id = $1 AND id = $2`, fleetID, id)
	if err != nil {
		return mapErr(err)
	}
	if tag.RowsAffected() == 0 {
		return store.ErrNotFound
	}
	return nil
}

const certCols = `id, fleet_id, name, cert_pem, key_pem, fingerprint, subject, issuer, dns_names, not_before, not_after, source, created_at`

// scanCertificate reads every column from certCols and decrypts the
// key_pem column on the way out, so callers always see plaintext.
//
// All callers in this package need the secret material (the compiler
// emits it into revisions, the agent endpoint serves the revision back
// to agents). The handler layer is responsible for stripping KeyPEM
// from API responses before they leave the process.
func (s *Store) scanCertificate(r rowScanner, c *model.Certificate) error {
	if err := r.Scan(
		&c.ID, &c.FleetID, &c.Name, &c.CertPEM, &c.KeyPEM,
		&c.Fingerprint, &c.Subject, &c.Issuer, &c.DNSNames,
		&c.NotBefore, &c.NotAfter, &c.Source, &c.CreatedAt,
	); err != nil {
		return err
	}
	plain, err := s.decStr(c.KeyPEM)
	if err != nil {
		return fmt.Errorf("decrypt key_pem (cert id=%d): %w", c.ID, err)
	}
	c.KeyPEM = plain
	return nil
}

// --- ACME accounts ---

func (s *Store) UpsertACMEAccount(ctx context.Context, a model.ACMEAccount) error {
	if a.CreatedAt.IsZero() {
		a.CreatedAt = time.Now().UTC()
	}
	encKey, err := s.encStr(a.AccountKeyPEM)
	if err != nil {
		return fmt.Errorf("encrypt account_key_pem: %w", err)
	}
	_, err = s.pool.Exec(ctx, `
		INSERT INTO acme_accounts
		    (fleet_id, directory_url, contact_email, account_key_pem, account_url, created_at)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (fleet_id) DO UPDATE SET
		    directory_url = EXCLUDED.directory_url,
		    contact_email = EXCLUDED.contact_email,
		    account_key_pem = EXCLUDED.account_key_pem,
		    account_url = EXCLUDED.account_url`,
		a.FleetID, a.DirectoryURL, a.ContactEmail, encKey, a.AccountURL, a.CreatedAt)
	return mapErr(err)
}

func (s *Store) GetACMEAccount(ctx context.Context, fleetID string) (model.ACMEAccount, error) {
	var a model.ACMEAccount
	row := s.pool.QueryRow(ctx, `
		SELECT fleet_id, directory_url, contact_email, account_key_pem, account_url, created_at
		FROM acme_accounts WHERE fleet_id = $1`, fleetID)
	if err := row.Scan(&a.FleetID, &a.DirectoryURL, &a.ContactEmail,
		&a.AccountKeyPEM, &a.AccountURL, &a.CreatedAt); err != nil {
		return model.ACMEAccount{}, mapErr(err)
	}
	plain, err := s.decStr(a.AccountKeyPEM)
	if err != nil {
		return model.ACMEAccount{}, fmt.Errorf("decrypt account_key_pem (fleet=%s): %w", fleetID, err)
	}
	a.AccountKeyPEM = plain
	return a, nil
}

func (s *Store) DeleteACMEAccount(ctx context.Context, fleetID string) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM acme_accounts WHERE fleet_id = $1`, fleetID)
	if err != nil {
		return mapErr(err)
	}
	if tag.RowsAffected() == 0 {
		return store.ErrNotFound
	}
	return nil
}

// --- DNS providers ---

func (s *Store) CreateDNSProvider(ctx context.Context, d model.DNSProvider) (model.DNSProvider, error) {
	if d.CreatedAt.IsZero() {
		d.CreatedAt = time.Now().UTC()
	}
	encCfg, err := s.encBytes(d.Config)
	if err != nil {
		return model.DNSProvider{}, fmt.Errorf("encrypt dns config: %w", err)
	}
	row := s.pool.QueryRow(ctx, `
		INSERT INTO dns_providers (fleet_id, name, type, config, created_at)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING `+dnsProviderCols,
		d.FleetID, d.Name, d.Type, string(encCfg), d.CreatedAt)
	if err := s.scanDNSProvider(row, &d); err != nil {
		return model.DNSProvider{}, mapErr(err)
	}
	return d, nil
}

func (s *Store) GetDNSProvider(ctx context.Context, fleetID string, id int64) (model.DNSProvider, error) {
	var d model.DNSProvider
	row := s.pool.QueryRow(ctx,
		`SELECT `+dnsProviderCols+` FROM dns_providers WHERE fleet_id = $1 AND id = $2`,
		fleetID, id)
	if err := s.scanDNSProvider(row, &d); err != nil {
		return model.DNSProvider{}, mapErr(err)
	}
	return d, nil
}

func (s *Store) GetDNSProviderByName(ctx context.Context, fleetID, name string) (model.DNSProvider, error) {
	var d model.DNSProvider
	row := s.pool.QueryRow(ctx,
		`SELECT `+dnsProviderCols+` FROM dns_providers WHERE fleet_id = $1 AND name = $2`,
		fleetID, name)
	if err := s.scanDNSProvider(row, &d); err != nil {
		return model.DNSProvider{}, mapErr(err)
	}
	return d, nil
}

func (s *Store) ListDNSProviders(ctx context.Context, fleetID string) ([]model.DNSProvider, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT `+dnsProviderCols+` FROM dns_providers WHERE fleet_id = $1 ORDER BY name`,
		fleetID)
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()
	out := []model.DNSProvider{}
	for rows.Next() {
		var d model.DNSProvider
		if err := s.scanDNSProvider(rows, &d); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func (s *Store) DeleteDNSProvider(ctx context.Context, fleetID string, id int64) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM dns_providers WHERE fleet_id = $1 AND id = $2`, fleetID, id)
	if err != nil {
		return mapErr(err)
	}
	if tag.RowsAffected() == 0 {
		return store.ErrNotFound
	}
	return nil
}

const dnsProviderCols = `id, fleet_id, name, type, config, created_at`

// --- Admin tokens ---

func (s *Store) MintAdminToken(ctx context.Context, name, prefix string, secretHash []byte) (model.AdminToken, error) {
	row := s.pool.QueryRow(ctx, `
		INSERT INTO admin_tokens (prefix, name, secret_hash)
		VALUES ($1, NULLIF($2, ''), $3)
		RETURNING prefix, COALESCE(name, ''), created_at, last_used_at, revoked_at`,
		prefix, name, secretHash)
	var t model.AdminToken
	if err := row.Scan(&t.Prefix, &t.Name, &t.CreatedAt, &t.LastUsedAt, &t.RevokedAt); err != nil {
		return model.AdminToken{}, mapErr(err)
	}
	return t, nil
}

func (s *Store) ListAdminTokens(ctx context.Context) ([]model.AdminToken, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT prefix, COALESCE(name, ''), created_at, last_used_at, revoked_at
		FROM admin_tokens ORDER BY created_at`)
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()
	out := []model.AdminToken{}
	for rows.Next() {
		var t model.AdminToken
		if err := rows.Scan(&t.Prefix, &t.Name, &t.CreatedAt, &t.LastUsedAt, &t.RevokedAt); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (s *Store) LookupAdminToken(ctx context.Context, prefix string) (model.AdminToken, []byte, error) {
	row := s.pool.QueryRow(ctx, `
		SELECT prefix, COALESCE(name, ''), created_at, last_used_at, revoked_at, secret_hash
		FROM admin_tokens WHERE prefix = $1`, prefix)
	var t model.AdminToken
	var hash []byte
	if err := row.Scan(&t.Prefix, &t.Name, &t.CreatedAt, &t.LastUsedAt, &t.RevokedAt, &hash); err != nil {
		return model.AdminToken{}, nil, mapErr(err)
	}
	if t.RevokedAt != nil {
		return model.AdminToken{}, nil, store.ErrTokenRevoked
	}
	return t, hash, nil
}

func (s *Store) TouchAdminToken(ctx context.Context, prefix string) error {
	_, err := s.pool.Exec(ctx, `UPDATE admin_tokens SET last_used_at = now() WHERE prefix = $1`, prefix)
	return mapErr(err)
}

func (s *Store) RevokeAdminToken(ctx context.Context, prefix string) error {
	tag, err := s.pool.Exec(ctx,
		`UPDATE admin_tokens SET revoked_at = now() WHERE prefix = $1 AND revoked_at IS NULL`, prefix)
	if err != nil {
		return mapErr(err)
	}
	if tag.RowsAffected() == 0 {
		return store.ErrNotFound
	}
	return nil
}

// --- ACME jobs ---

const acmeJobCols = `id, fleet_id, name, dns_names, dns_provider, status, error, cert_id, created_at, started_at, finished_at`

func scanACMEJob(r rowScanner, j *model.ACMEJob) error {
	var status string
	if err := r.Scan(
		&j.ID, &j.FleetID, &j.Name, &j.DNSNames, &j.DNSProvider,
		&status, &j.Error, &j.CertID,
		&j.CreatedAt, &j.StartedAt, &j.FinishedAt,
	); err != nil {
		return err
	}
	j.Status = model.ACMEJobStatus(status)
	return nil
}

func (s *Store) CreateACMEJob(ctx context.Context, j model.ACMEJob) (model.ACMEJob, error) {
	if j.CreatedAt.IsZero() {
		j.CreatedAt = time.Now().UTC()
	}
	if j.Status == "" {
		j.Status = model.ACMEJobPending
	}
	row := s.pool.QueryRow(ctx, `
		INSERT INTO acme_jobs (fleet_id, name, dns_names, dns_provider, status, created_at)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING `+acmeJobCols,
		j.FleetID, j.Name, j.DNSNames, j.DNSProvider, string(j.Status), j.CreatedAt)
	if err := scanACMEJob(row, &j); err != nil {
		return model.ACMEJob{}, mapErr(err)
	}
	return j, nil
}

func (s *Store) GetACMEJob(ctx context.Context, id int64) (model.ACMEJob, error) {
	var j model.ACMEJob
	row := s.pool.QueryRow(ctx,
		`SELECT `+acmeJobCols+` FROM acme_jobs WHERE id = $1`, id)
	if err := scanACMEJob(row, &j); err != nil {
		return model.ACMEJob{}, mapErr(err)
	}
	return j, nil
}

func (s *Store) ListACMEJobs(ctx context.Context, fleetID string, limit int) ([]model.ACMEJob, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.pool.Query(ctx,
		`SELECT `+acmeJobCols+` FROM acme_jobs WHERE fleet_id = $1 ORDER BY id DESC LIMIT $2`,
		fleetID, limit)
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()
	out := []model.ACMEJob{}
	for rows.Next() {
		var j model.ACMEJob
		if err := scanACMEJob(rows, &j); err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, rows.Err()
}

// ClaimNextACMEJob picks one pending job and atomically transitions
// it to running. The SKIP LOCKED clause lets multiple worker replicas
// drain the queue concurrently without coordinating via Postgres locks.
func (s *Store) ClaimNextACMEJob(ctx context.Context) (model.ACMEJob, error) {
	var j model.ACMEJob
	row := s.pool.QueryRow(ctx, `
		UPDATE acme_jobs
		SET status = 'running', started_at = now()
		WHERE id = (
		    SELECT id FROM acme_jobs
		    WHERE status = 'pending'
		    ORDER BY id
		    LIMIT 1
		    FOR UPDATE SKIP LOCKED
		)
		RETURNING `+acmeJobCols)
	if err := scanACMEJob(row, &j); err != nil {
		return model.ACMEJob{}, mapErr(err)
	}
	return j, nil
}

func (s *Store) MarkACMEJobSucceeded(ctx context.Context, id, certID int64) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE acme_jobs
		SET status = 'succeeded', cert_id = $2, error = '', finished_at = now()
		WHERE id = $1`, id, certID)
	if err != nil {
		return mapErr(err)
	}
	if tag.RowsAffected() == 0 {
		return store.ErrNotFound
	}
	return nil
}

func (s *Store) MarkACMEJobFailed(ctx context.Context, id int64, errMsg string) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE acme_jobs
		SET status = 'failed', error = $2, finished_at = now()
		WHERE id = $1`, id, errMsg)
	if err != nil {
		return mapErr(err)
	}
	if tag.RowsAffected() == 0 {
		return store.ErrNotFound
	}
	return nil
}

// --- Audit log ---

func (s *Store) AppendAuditEntry(ctx context.Context, e model.AuditEntry) error {
	if e.CreatedAt.IsZero() {
		e.CreatedAt = time.Now().UTC()
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO audit_log (actor, method, path, status, fleet_id, summary, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		e.Actor, e.Method, e.Path, e.Status, e.FleetID, e.Summary, e.CreatedAt)
	return mapErr(err)
}

func (s *Store) ListAuditEntries(ctx context.Context, q store.AuditQuery) ([]model.AuditEntry, error) {
	limit := q.Limit
	if limit <= 0 || limit > 500 {
		limit = 100
	}

	// Three filter shapes: any/global/specific; pgx parameters can't
	// switch a column reference, so we branch the SQL.
	var (
		rows interface {
			Next() bool
			Scan(...any) error
			Close()
			Err() error
		}
		err error
	)
	switch {
	case q.FleetID == nil:
		rows, err = s.pool.Query(ctx, `
			SELECT id, actor, method, path, status, fleet_id, COALESCE(summary, ''), created_at
			FROM audit_log
			WHERE ($1 = 0 OR id < $1)
			ORDER BY id DESC
			LIMIT $2`, q.BeforeID, limit)
	case *q.FleetID == "":
		rows, err = s.pool.Query(ctx, `
			SELECT id, actor, method, path, status, fleet_id, COALESCE(summary, ''), created_at
			FROM audit_log
			WHERE fleet_id IS NULL AND ($1 = 0 OR id < $1)
			ORDER BY id DESC
			LIMIT $2`, q.BeforeID, limit)
	default:
		rows, err = s.pool.Query(ctx, `
			SELECT id, actor, method, path, status, fleet_id, COALESCE(summary, ''), created_at
			FROM audit_log
			WHERE fleet_id = $1 AND ($2 = 0 OR id < $2)
			ORDER BY id DESC
			LIMIT $3`, *q.FleetID, q.BeforeID, limit)
	}
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()

	out := []model.AuditEntry{}
	for rows.Next() {
		var e model.AuditEntry
		if err := rows.Scan(&e.ID, &e.Actor, &e.Method, &e.Path, &e.Status,
			&e.FleetID, &e.Summary, &e.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func (s *Store) scanDNSProvider(r rowScanner, d *model.DNSProvider) error {
	var cfg []byte
	if err := r.Scan(&d.ID, &d.FleetID, &d.Name, &d.Type, &cfg, &d.CreatedAt); err != nil {
		return err
	}
	plain, err := s.decBytes(cfg)
	if err != nil {
		return fmt.Errorf("decrypt dns config (id=%d): %w", d.ID, err)
	}
	d.Config = plain
	return nil
}

const revisionCols = `id, fleet_id, number, compiled_config, source_proxy_hosts, source_certs, etag, COALESCE(notes, ''), COALESCE(signature, ''), COALESCE(signature_alg, ''), generated_at`
const prefixedRevisionCols = `r.id, r.fleet_id, r.number, r.compiled_config, r.source_proxy_hosts, r.source_certs, r.etag, COALESCE(r.notes, ''), COALESCE(r.signature, ''), COALESCE(r.signature_alg, ''), r.generated_at`

func scanRevision(r rowScanner, rev *model.Revision) error {
	var compiled, source, sourceCerts []byte
	if err := r.Scan(
		&rev.ID, &rev.FleetID, &rev.Number,
		&compiled, &source, &sourceCerts,
		&rev.ETag, &rev.Notes, &rev.Signature, &rev.SignatureAlg,
		&rev.GeneratedAt,
	); err != nil {
		return err
	}
	rev.CompiledConfig = json.RawMessage(compiled)
	rev.SourceProxyHosts = json.RawMessage(source)
	rev.SourceCerts = json.RawMessage(sourceCerts)
	return nil
}
