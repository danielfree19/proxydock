-- Phase 7: middleware library.
--
-- Stores per-fleet named middleware chains that operators can apply to
-- proxy hosts as a starting point. Apply-by-copy semantics: applying a
-- template deep-copies its `middlewares` array into the host. Editing
-- the template afterwards does NOT mutate hosts that already applied
-- it — operators can still reach in and tweak per-host without losing
-- diff safety.
CREATE TABLE middleware_templates (
  id          BIGSERIAL PRIMARY KEY,
  fleet_id    TEXT NOT NULL REFERENCES fleets(id) ON DELETE CASCADE,
  name        TEXT NOT NULL,
  description TEXT NOT NULL DEFAULT '',
  middlewares JSONB NOT NULL DEFAULT '[]'::jsonb,
  created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (fleet_id, name)
);

CREATE INDEX middleware_templates_fleet_idx ON middleware_templates (fleet_id);
