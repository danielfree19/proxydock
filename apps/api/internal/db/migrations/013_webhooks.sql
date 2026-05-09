-- Phase 7: outgoing webhooks fired on revision publish / rollback /
-- ACME renewal. Mirrors the dns_providers + acme_jobs split: a config
-- table holds the destinations, a separate jobs table tracks delivery
-- attempts so we can retry without losing events on transient errors.
--
-- The `secret` column holds an HMAC key that webhook receivers can use
-- to verify request authenticity (X-Webhook-Signature: sha256=<hmac>).
-- Stored encrypted via cryptokit when MANAGER_API_ENCRYPTION_KEY is
-- set — same prefix-tagged scheme as dns_providers.config.
CREATE TABLE webhooks (
  id          BIGSERIAL PRIMARY KEY,
  fleet_id    TEXT NOT NULL REFERENCES fleets(id) ON DELETE CASCADE,
  name        TEXT NOT NULL,
  url         TEXT NOT NULL,
  secret      TEXT NOT NULL DEFAULT '',
  events      TEXT[] NOT NULL DEFAULT ARRAY['revision_published'],
  enabled     BOOLEAN NOT NULL DEFAULT TRUE,
  created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (fleet_id, name)
);

CREATE INDEX webhooks_fleet_idx ON webhooks (fleet_id);
