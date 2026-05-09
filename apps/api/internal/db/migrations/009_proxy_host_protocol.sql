-- 009_proxy_host_protocol.sql
-- Phase 6: extend proxy_hosts to cover Traefik's TCP and UDP entry
-- points alongside HTTP. The new column defaults to 'http' so existing
-- rows keep their semantics; values 'tcp' and 'udp' switch the
-- compiler into the matching code path.
--
-- Field semantics by protocol:
--   * http: domain → Host(`...`) router rule. middlewares + tls are
--     applied at the router level.
--   * tcp:  domain → HostSNI(`...`) router rule. domain="*" emits a
--     catch-all (HostSNI(`*`)). tls=true terminates TLS at the proxy
--     using a cert from the fleet's pool; tls=false implies SNI
--     passthrough. Middlewares are ignored (Traefik's TCP middleware
--     set is small and rarely used; we'll add support if needed).
--   * udp:  domain is ignored (UDP has no SNI / rule mechanism). Each
--     UDP entry point can host one router; the compiler refuses to
--     emit two UDP routers on the same entry point.

ALTER TABLE proxy_hosts
    ADD COLUMN IF NOT EXISTS protocol TEXT NOT NULL DEFAULT 'http';

ALTER TABLE proxy_hosts
    ADD CONSTRAINT proxy_hosts_protocol_check
    CHECK (protocol IN ('http', 'tcp', 'udp'));
