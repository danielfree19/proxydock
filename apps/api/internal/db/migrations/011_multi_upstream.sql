-- Phase 7: multiple upstreams per proxy host + sticky-session toggle.
--
-- We add an array column rather than a join table because the manager
-- already snapshots proxy host JSON into config_revisions.source_proxy_hosts
-- on publish — keeping upstreams inline preserves that snapshot
-- semantics with zero schema gymnastics.
--
-- The legacy `upstream_url` TEXT column stays for one release as a
-- denormalized copy of `upstream_urls[1]`. Older API clients still
-- post/read it; the compiler prefers `upstream_urls` when populated.
-- A follow-up migration drops `upstream_url` once all clients are
-- known to have rolled forward.
ALTER TABLE proxy_hosts
  ADD COLUMN upstream_urls TEXT[] NOT NULL DEFAULT '{}',
  ADD COLUMN sticky_session BOOLEAN NOT NULL DEFAULT FALSE;

-- Backfill the array from the existing single-URL column. After this
-- runs, every row has at least one entry (or the empty default if
-- upstream_url was NULL/blank, which shouldn't happen but we tolerate).
UPDATE proxy_hosts
SET upstream_urls = ARRAY[upstream_url]
WHERE upstream_url IS NOT NULL
  AND upstream_url <> ''
  AND cardinality(upstream_urls) = 0;
