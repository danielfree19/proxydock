-- Phase 7: per-upstream health checks for HTTP proxy hosts.
--
-- The keys we read at compile time:
--   path                (required to enable the health check)
--   interval            (Traefik duration, e.g. "10s")
--   timeout             (Traefik duration)
--   scheme              ("http" / "https" — overrides the upstream's scheme)
--   hostname            (Host header sent to the upstream)
--   port                (port to probe; defaults to the upstream's port)
--   followRedirects     (bool)
--
-- Empty `health_check` (`{}`) leaves the loadBalancer.healthCheck field
-- off the compiled config, matching pre-Phase-7 behaviour.
ALTER TABLE proxy_hosts
  ADD COLUMN health_check JSONB NOT NULL DEFAULT '{}'::jsonb;
