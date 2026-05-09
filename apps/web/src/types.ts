// Mirrors apps/api/internal/model. Field names match the JSON tags on
// the Go side; only fields the UI actually consumes are listed.

export interface Fleet {
  id: string;
  name: string;
  published_revision_id?: number | null;
  created_at: string;
}

export interface Agent {
  id: string;
  fleet_id: string;
  name: string;
  labels: string[];
  last_heartbeat_at?: string | null;
  last_revision_seen?: number | null;
  last_provider_version?: string | null;
  last_traefik_version?: string | null;
  last_error?: string | null;
  created_at: string;
}

export interface AgentToken {
  prefix: string;
  agent_id: string;
  name?: string;
  created_at: string;
  last_used_at?: string | null;
  revoked_at?: string | null;
}

export interface Middleware {
  name: string;
  type: "headers" | "redirectScheme" | "stripPrefix" | "basicAuth" | string;
  config?: Record<string, unknown>;
}

export interface MiddlewareTemplate {
  id: number;
  fleet_id: string;
  name: string;
  description: string;
  middlewares: Middleware[];
  created_at: string;
  updated_at: string;
}

export interface MiddlewareTemplateInput {
  name: string;
  description?: string;
  middlewares: Middleware[];
}

export type ProxyHostProtocol = "http" | "tcp" | "udp";

export interface HealthCheck {
  path?: string;
  interval?: string;
  timeout?: string;
  scheme?: string;
  hostname?: string;
  port?: number;
  followRedirects?: boolean;
}

export interface ProxyHost {
  id: number;
  fleet_id: string;
  name: string;
  protocol: ProxyHostProtocol;
  domain: string;
  upstream_url: string;
  upstream_urls: string[];
  sticky_session: boolean;
  health_check?: HealthCheck;
  entry_points: string[];
  middlewares: Middleware[];
  tls: boolean;
  label_selector: string;
  enabled: boolean;
  created_at: string;
  updated_at: string;
}

export interface Certificate {
  id: number;
  fleet_id: string;
  name: string;
  cert_pem?: string;
  fingerprint: string;
  subject: string;
  issuer: string;
  dns_names: string[];
  not_before: string;
  not_after: string;
  source: "upload" | "acme" | string;
  created_at: string;
}

export interface CertificateInput {
  name: string;
  cert_pem: string;
  key_pem: string;
}

export interface ACMEAccount {
  fleet_id: string;
  directory_url: string;
  contact_email: string;
  account_url: string;
  created_at: string;
}

export interface DNSProvider {
  id: number;
  fleet_id: string;
  name: string;
  type: string;
  config?: unknown;
  created_at: string;
}

export interface ACMEIssueRequest {
  name: string;
  dns_names: string[];
  dns_provider: string;
}

export interface AuditEntry {
  id: number;
  actor: string;
  method: string;
  path: string;
  status: number;
  fleet_id?: string | null;
  summary?: string;
  created_at: string;
}

export type ACMEJobStatus = "pending" | "running" | "succeeded" | "failed";

export interface ACMEJob {
  id: number;
  fleet_id: string;
  name: string;
  dns_names: string[];
  dns_provider: string;
  status: ACMEJobStatus;
  error?: string;
  cert_id?: number | null;
  created_at: string;
  started_at?: string | null;
  finished_at?: string | null;
}

export interface Revision {
  id: number;
  fleet_id: string;
  number: number;
  etag: string;
  notes?: string;
  generated_at: string;
  // Only populated by GET /revisions/{n}, not by the list endpoint.
  compiled_config?: unknown;
  source_proxy_hosts?: unknown;
}

export interface ProxyHostInput {
  name: string;
  protocol?: ProxyHostProtocol;
  domain: string;
  upstream_url?: string;
  upstream_urls?: string[];
  sticky_session?: boolean;
  health_check?: HealthCheck;
  entry_points?: string[];
  middlewares?: Middleware[];
  tls?: boolean;
  label_selector?: string;
  enabled?: boolean;
}

export interface Webhook {
  id: number;
  fleet_id: string;
  name: string;
  url: string;
  has_secret: boolean;
  events: string[];
  enabled: boolean;
  created_at: string;
}

export interface WebhookInput {
  name: string;
  url: string;
  secret?: string;
  events: string[];
  enabled?: boolean;
}

export interface MintTokenResponse {
  token: string;
  metadata: AgentToken;
}

// DiscoveredService is one upstream candidate from the manager's
// service discovery provider (Phase 7, Docker socket).
export interface DiscoveredService {
  id: string;
  name: string;
  image?: string;
  ip: string;
  network?: string;
  ports?: number[];
}

export interface DiscoveryResult {
  provider: string;
  services: DiscoveredService[];
}

// AgentConfigPreview is the admin-visible mirror of what the agent
// receives via /config — same compile path, post-label-filter.
export interface AgentConfigPreview {
  fleet_id: string;
  agent_id: string;
  revision: number;
  etag: string;
  generated_at: string;
  signature?: string;
  signature_alg?: string;
  // Traefik dynamic config: { http?, tcp?, udp?, tls? } each with
  // routers/services/middlewares maps.
  config: TraefikDynamicConfig;
}

export interface TraefikDynamicConfig {
  http?: TraefikSection;
  tcp?: TraefikSection;
  udp?: TraefikSection;
  tls?: { certificates?: unknown[] };
}

export interface TraefikSection {
  routers?: Record<string, TraefikRouter>;
  services?: Record<string, TraefikService>;
  middlewares?: Record<string, Record<string, unknown>>;
}

export interface TraefikRouter {
  rule?: string;
  entryPoints?: string[];
  service?: string;
  middlewares?: string[];
  tls?: unknown;
}

export interface TraefikService {
  loadBalancer?: {
    servers?: { url?: string; address?: string }[];
  };
}
