-- 001_init.sql
-- Phase 1 schema: fleets, agents, agent_tokens, proxy_hosts, config_revisions.

CREATE TABLE IF NOT EXISTS fleets (
    id          TEXT        PRIMARY KEY,
    name        TEXT        NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS agents (
    id                     TEXT        PRIMARY KEY,
    fleet_id               TEXT        NOT NULL REFERENCES fleets(id) ON DELETE CASCADE,
    name                   TEXT        NOT NULL,
    last_heartbeat_at      TIMESTAMPTZ,
    last_revision_seen     INT,
    last_provider_version  TEXT,
    last_traefik_version   TEXT,
    last_error             TEXT,
    created_at             TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS agents_fleet_idx ON agents(fleet_id);

-- Tokens: prefix is non-secret and indexable; secret_hash is sha256 of the
-- random secret half. Token wire format: "tfm_<prefix>_<secret>".
CREATE TABLE IF NOT EXISTS agent_tokens (
    prefix       TEXT        PRIMARY KEY,
    agent_id     TEXT        NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
    secret_hash  BYTEA       NOT NULL,
    name         TEXT,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_used_at TIMESTAMPTZ,
    revoked_at   TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS agent_tokens_agent_idx ON agent_tokens(agent_id);

CREATE TABLE IF NOT EXISTS proxy_hosts (
    id           BIGSERIAL   PRIMARY KEY,
    fleet_id     TEXT        NOT NULL REFERENCES fleets(id) ON DELETE CASCADE,
    name         TEXT        NOT NULL,
    domain       TEXT        NOT NULL,
    upstream_url TEXT        NOT NULL,
    entry_points TEXT[]      NOT NULL DEFAULT ARRAY['web'],
    middlewares  JSONB       NOT NULL DEFAULT '[]'::jsonb,
    enabled      BOOLEAN     NOT NULL DEFAULT TRUE,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (fleet_id, name)
);
CREATE INDEX IF NOT EXISTS proxy_hosts_fleet_idx ON proxy_hosts(fleet_id);

CREATE TABLE IF NOT EXISTS config_revisions (
    id                  BIGSERIAL   PRIMARY KEY,
    fleet_id            TEXT        NOT NULL REFERENCES fleets(id) ON DELETE CASCADE,
    number              INT         NOT NULL,
    compiled_config     JSONB       NOT NULL,
    source_proxy_hosts  JSONB       NOT NULL,
    etag                TEXT        NOT NULL,
    notes               TEXT,
    generated_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (fleet_id, number)
);
CREATE INDEX IF NOT EXISTS config_revisions_fleet_idx ON config_revisions(fleet_id);

-- Forward-reference: each fleet has at most one currently published revision.
ALTER TABLE fleets
    ADD COLUMN IF NOT EXISTS published_revision_id BIGINT
        REFERENCES config_revisions(id) ON DELETE SET NULL;
