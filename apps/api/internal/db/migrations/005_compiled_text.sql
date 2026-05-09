-- 005_compiled_text.sql
-- Phase 5 signed revisions: compiled_config must be byte-preserving so
-- the manager-side signature still verifies when the bytes come back
-- out of Postgres. JSONB silently normalizes whitespace and key order;
-- TEXT keeps exactly what was written.
--
-- source_proxy_hosts is migrated alongside it for consistency. It isn't
-- signed, but having the two columns share a type keeps the scan code
-- simpler.

ALTER TABLE config_revisions
    ALTER COLUMN compiled_config TYPE TEXT USING compiled_config::text;

ALTER TABLE config_revisions
    ALTER COLUMN source_proxy_hosts TYPE TEXT USING source_proxy_hosts::text;
