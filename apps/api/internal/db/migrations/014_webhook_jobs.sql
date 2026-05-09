-- Phase 7: webhook delivery queue. Mirror of acme_jobs's pattern —
-- FOR UPDATE SKIP LOCKED claim, exponential backoff on failure, max
-- attempts before status flips to 'failed'. Worker lives in the same
-- process as the manager-api (cmd/manager-api/webhook_worker.go).
CREATE TABLE webhook_jobs (
  id          BIGSERIAL PRIMARY KEY,
  webhook_id  BIGINT NOT NULL REFERENCES webhooks(id) ON DELETE CASCADE,
  payload     TEXT NOT NULL,
  status      TEXT NOT NULL DEFAULT 'pending',
  attempts    INT NOT NULL DEFAULT 0,
  next_run_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  last_error  TEXT NOT NULL DEFAULT '',
  created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
  finished_at TIMESTAMPTZ
);

-- Hot index used by the worker's claim query.
CREATE INDEX webhook_jobs_pending_idx
  ON webhook_jobs (next_run_at)
  WHERE status = 'pending';
