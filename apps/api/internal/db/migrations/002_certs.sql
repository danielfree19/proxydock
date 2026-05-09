-- 002_certs.sql
-- Phase 4: uploaded TLS certificates per fleet, plus a per-proxy-host
-- "use TLS" flag that the compiler turns into router.tls = {}.

CREATE TABLE IF NOT EXISTS certificates (
    id           BIGSERIAL   PRIMARY KEY,
    fleet_id     TEXT        NOT NULL REFERENCES fleets(id) ON DELETE CASCADE,
    name         TEXT        NOT NULL,

    -- Sensitive at rest; Phase 5 hardening adds column-level encryption.
    cert_pem     TEXT        NOT NULL,
    key_pem      TEXT        NOT NULL,

    -- Parsed leaf-certificate metadata, materialized so the UI can
    -- render expiry without re-parsing on every request.
    fingerprint  TEXT        NOT NULL,
    subject      TEXT        NOT NULL,
    issuer       TEXT        NOT NULL,
    dns_names    TEXT[]      NOT NULL,
    not_before   TIMESTAMPTZ NOT NULL,
    not_after    TIMESTAMPTZ NOT NULL,

    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (fleet_id, name)
);
CREATE INDEX IF NOT EXISTS certificates_fleet_idx ON certificates(fleet_id);

ALTER TABLE proxy_hosts
    ADD COLUMN IF NOT EXISTS tls BOOLEAN NOT NULL DEFAULT FALSE;
