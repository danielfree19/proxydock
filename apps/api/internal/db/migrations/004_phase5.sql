-- 004_phase5.sql
-- Phase 5 hardening:
--   * admin_tokens — bearer tokens that authorize the admin API
--     (mirroring agent_tokens shape).
--   * signature columns on config_revisions for ed25519-signed
--     revisions.
--   * dns_providers.config is dropped from JSONB to TEXT so we can
--     transparently substitute the column value with a versioned
--     ciphertext envelope.

CREATE TABLE IF NOT EXISTS admin_tokens (
    prefix       TEXT        PRIMARY KEY,
    name         TEXT,
    secret_hash  BYTEA       NOT NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_used_at TIMESTAMPTZ,
    revoked_at   TIMESTAMPTZ
);

ALTER TABLE config_revisions
    ADD COLUMN IF NOT EXISTS signature      TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS signature_alg  TEXT NOT NULL DEFAULT '';

ALTER TABLE dns_providers
    ALTER COLUMN config TYPE TEXT USING config::text;
