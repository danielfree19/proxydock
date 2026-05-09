-- 008_audit_log.sql
-- Append-only audit log of admin actions.
--
-- One row per mutating admin request. Reads are not logged — they
-- would dominate the table and rarely matter for forensics.
--
-- actor is either:
--   * "bootstrap"          when the env-bootstrap token authorized the call
--   * "admin:<prefix>"     when an admin_tokens row authorized it
--   * "system"             when an internal job (e.g. ACME renewal) writes
--
-- fleet_id is best-effort extracted from the request path; nil for
-- global endpoints like POST /api/v1/fleets or admin token mgmt.

CREATE TABLE IF NOT EXISTS audit_log (
    id         BIGSERIAL   PRIMARY KEY,
    actor      TEXT        NOT NULL,
    method     TEXT        NOT NULL,
    path       TEXT        NOT NULL,
    status     INT         NOT NULL,
    fleet_id   TEXT        REFERENCES fleets(id) ON DELETE SET NULL,
    summary    TEXT        NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS audit_log_created_idx ON audit_log(created_at DESC);
CREATE INDEX IF NOT EXISTS audit_log_fleet_idx   ON audit_log(fleet_id, created_at DESC);
