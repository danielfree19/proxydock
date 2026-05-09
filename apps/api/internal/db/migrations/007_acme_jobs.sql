-- 007_acme_jobs.sql
-- Phase 5b async ACME issuance.
--
-- Each row is a request to issue (or — later — renew) one cert.
-- Status transitions: pending → running → (succeeded|failed). The
-- worker claims rows with FOR UPDATE SKIP LOCKED so multiple manager
-- replicas can share a queue without coordinating outside Postgres.

CREATE TABLE IF NOT EXISTS acme_jobs (
    id           BIGSERIAL   PRIMARY KEY,
    fleet_id     TEXT        NOT NULL REFERENCES fleets(id) ON DELETE CASCADE,
    name         TEXT        NOT NULL,
    dns_names    TEXT[]      NOT NULL,
    dns_provider TEXT        NOT NULL,
    status       TEXT        NOT NULL,
    error        TEXT        NOT NULL DEFAULT '',
    cert_id      BIGINT      REFERENCES certificates(id) ON DELETE SET NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    started_at   TIMESTAMPTZ,
    finished_at  TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS acme_jobs_fleet_idx  ON acme_jobs(fleet_id);
CREATE INDEX IF NOT EXISTS acme_jobs_status_idx ON acme_jobs(status);
