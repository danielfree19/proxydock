-- 003_acme.sql
-- Phase 4b: control-plane ACME issuance.
--
--   * One ACME account per fleet (account key + directory URL pinned per fleet).
--   * Multiple named DNS-01 providers per fleet (e.g. "primary",
--     "secondary"); the type column picks the implementation, config is
--     a type-specific JSON bag.
--   * certificates.source distinguishes uploaded from ACME-issued certs
--     so the renewal goroutine knows which ones it's allowed to re-issue.

CREATE TABLE IF NOT EXISTS acme_accounts (
    fleet_id        TEXT        PRIMARY KEY REFERENCES fleets(id) ON DELETE CASCADE,
    directory_url   TEXT        NOT NULL,
    contact_email   TEXT        NOT NULL,
    account_key_pem TEXT        NOT NULL,
    account_url     TEXT        NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS dns_providers (
    id         BIGSERIAL   PRIMARY KEY,
    fleet_id   TEXT        NOT NULL REFERENCES fleets(id) ON DELETE CASCADE,
    name       TEXT        NOT NULL,
    type       TEXT        NOT NULL,
    config     JSONB       NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (fleet_id, name)
);
CREATE INDEX IF NOT EXISTS dns_providers_fleet_idx ON dns_providers(fleet_id);

ALTER TABLE certificates
    ADD COLUMN IF NOT EXISTS source TEXT NOT NULL DEFAULT 'upload';
