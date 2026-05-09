-- 006_labels.sql
-- Phase 5b per-agent labels:
--   * agents.labels — list of "key=value" strings the operator
--     attaches to each agent (e.g. {"region=us-east","tier=prod"}).
--   * proxy_hosts.label_selector — comma-separated "key=value"
--     requirements; agents whose labels satisfy every requirement
--     receive the host. Empty selector matches every agent.
--   * config_revisions.source_certs — snapshot of the fleet's
--     certificates at publish time. The agent endpoint re-runs the
--     compiler per-agent on the published source state; freezing the
--     certs alongside the hosts keeps that compilation correct under
--     a later rotation.

ALTER TABLE agents
    ADD COLUMN IF NOT EXISTS labels TEXT[] NOT NULL DEFAULT '{}';

ALTER TABLE proxy_hosts
    ADD COLUMN IF NOT EXISTS label_selector TEXT NOT NULL DEFAULT '';

ALTER TABLE config_revisions
    ADD COLUMN IF NOT EXISTS source_certs TEXT NOT NULL DEFAULT '[]';
