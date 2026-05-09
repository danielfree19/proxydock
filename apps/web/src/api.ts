import { getAdminToken } from "./components/auth";
import type {
  ACMEAccount,
  ACMEIssueRequest,
  ACMEJob,
  Agent,
  AgentConfigPreview,
  AgentToken,
  AuditEntry,
  DiscoveryResult,
  Certificate,
  CertificateInput,
  DNSProvider,
  Fleet,
  MiddlewareTemplate,
  MiddlewareTemplateInput,
  MintTokenResponse,
  ProxyHost,
  ProxyHostInput,
  Revision,
  Webhook,
  WebhookInput,
} from "./types";

// All UI calls go through this thin wrapper. The base path is "/" in
// production (the manager-api serves the bundle from the same origin)
// and the Vite dev server proxies /api/* to the manager during dev.

const BASE = "";

class ApiError extends Error {
  constructor(public status: number, public body: string) {
    super(`HTTP ${status}: ${body || "(empty)"}`);
  }
}

async function request<T>(
  method: string,
  path: string,
  body?: unknown,
): Promise<T> {
  const headers: Record<string, string> = {};
  if (body !== undefined) headers["Content-Type"] = "application/json";
  const tok = getAdminToken();
  if (tok) headers["Authorization"] = "Bearer " + tok;
  const init: RequestInit = {
    method,
    headers,
    body: body !== undefined ? JSON.stringify(body) : undefined,
  };
  const res = await fetch(BASE + path, init);
  if (!res.ok) {
    const text = await res.text();
    throw new ApiError(res.status, text);
  }
  if (res.status === 204) return undefined as T;
  // Some endpoints return 304; the UI never asks for those, so any
  // non-2xx success is treated as JSON.
  return (await res.json()) as T;
}

// verifyAdminToken hits a tiny admin-only endpoint to confirm the
// token is valid before persisting it (used by the login page).
export async function verifyAdminToken(token: string): Promise<void> {
  const res = await fetch(BASE + "/api/v1/admin/whoami", {
    headers: { Authorization: "Bearer " + token },
  });
  if (!res.ok) {
    const text = await res.text();
    throw new ApiError(res.status, text);
  }
}

export { ApiError };

// --- Fleets ---
export const listFleets = () =>
  request<{ fleets: Fleet[] }>("GET", "/api/v1/fleets").then((r) => r.fleets);

export const createFleet = (id: string, name: string) =>
  request<Fleet>("POST", "/api/v1/fleets", { id, name });

export const getFleet = (id: string) =>
  request<Fleet>("GET", `/api/v1/fleets/${encodeURIComponent(id)}`);

export const deleteFleet = (id: string) =>
  request<void>("DELETE", `/api/v1/fleets/${encodeURIComponent(id)}`);

// --- Agents ---
export const listAgents = (fleetId: string) =>
  request<{ agents: Agent[] }>(
    "GET",
    `/api/v1/fleets/${encodeURIComponent(fleetId)}/agents`,
  ).then((r) => r.agents);

export const createAgent = (fleetId: string, id: string, name: string) =>
  request<Agent>(
    "POST",
    `/api/v1/fleets/${encodeURIComponent(fleetId)}/agents`,
    { id, name },
  );

export const getAgent = (id: string) =>
  request<Agent>("GET", `/api/v1/agents/${encodeURIComponent(id)}`);

export const deleteAgent = (id: string) =>
  request<void>("DELETE", `/api/v1/agents/${encodeURIComponent(id)}`);

export const updateAgentLabels = (id: string, labels: string[]) =>
  request<Agent>(
    "PUT",
    `/api/v1/agents/${encodeURIComponent(id)}/labels`,
    { labels },
  );

export const getAgentConfigPreview = (id: string) =>
  request<AgentConfigPreview>(
    "GET",
    `/api/v1/agents/${encodeURIComponent(id)}/config-preview`,
  );

// --- Service discovery (Phase 7) ---
export const discoverServices = () =>
  request<DiscoveryResult>("GET", "/api/v1/discover/services");

// --- Webhooks (Phase 7) ---
export const listWebhooks = (fleetId: string) =>
  request<{ webhooks: Webhook[] }>(
    "GET",
    `/api/v1/fleets/${encodeURIComponent(fleetId)}/webhooks`,
  ).then((r) => r.webhooks);

export const createWebhook = (fleetId: string, input: WebhookInput) =>
  request<Webhook>(
    "POST",
    `/api/v1/fleets/${encodeURIComponent(fleetId)}/webhooks`,
    input,
  );

export const updateWebhook = (
  fleetId: string,
  id: number,
  input: WebhookInput,
) =>
  request<Webhook>(
    "PUT",
    `/api/v1/fleets/${encodeURIComponent(fleetId)}/webhooks/${id}`,
    input,
  );

export const deleteWebhook = (fleetId: string, id: number) =>
  request<void>(
    "DELETE",
    `/api/v1/fleets/${encodeURIComponent(fleetId)}/webhooks/${id}`,
  );

// --- Tokens ---
export const listTokens = (agentId: string) =>
  request<{ tokens: AgentToken[] }>(
    "GET",
    `/api/v1/agents/${encodeURIComponent(agentId)}/tokens`,
  ).then((r) => r.tokens);

export const mintToken = (agentId: string, name: string) =>
  request<MintTokenResponse>(
    "POST",
    `/api/v1/agents/${encodeURIComponent(agentId)}/tokens`,
    { name },
  );

export const revokeToken = (agentId: string, prefix: string) =>
  request<void>(
    "POST",
    `/api/v1/agents/${encodeURIComponent(agentId)}/tokens/${encodeURIComponent(prefix)}/revoke`,
  );

// --- Proxy hosts ---
export const listProxyHosts = (fleetId: string) =>
  request<{ proxy_hosts: ProxyHost[] }>(
    "GET",
    `/api/v1/fleets/${encodeURIComponent(fleetId)}/proxy_hosts`,
  ).then((r) => r.proxy_hosts);

export const createProxyHost = (fleetId: string, input: ProxyHostInput) =>
  request<ProxyHost>(
    "POST",
    `/api/v1/fleets/${encodeURIComponent(fleetId)}/proxy_hosts`,
    input,
  );

export const getProxyHost = (fleetId: string, phId: number) =>
  request<ProxyHost>(
    "GET",
    `/api/v1/fleets/${encodeURIComponent(fleetId)}/proxy_hosts/${phId}`,
  );

export const updateProxyHost = (
  fleetId: string,
  phId: number,
  input: ProxyHostInput,
) =>
  request<ProxyHost>(
    "PUT",
    `/api/v1/fleets/${encodeURIComponent(fleetId)}/proxy_hosts/${phId}`,
    input,
  );

export const deleteProxyHost = (fleetId: string, phId: number) =>
  request<void>(
    "DELETE",
    `/api/v1/fleets/${encodeURIComponent(fleetId)}/proxy_hosts/${phId}`,
  );

// --- Middleware templates (Phase 7) ---
export const listMiddlewareTemplates = (fleetId: string) =>
  request<{ middleware_templates: MiddlewareTemplate[] }>(
    "GET",
    `/api/v1/fleets/${encodeURIComponent(fleetId)}/middleware_templates`,
  ).then((r) => r.middleware_templates);

export const getMiddlewareTemplate = (fleetId: string, id: number) =>
  request<MiddlewareTemplate>(
    "GET",
    `/api/v1/fleets/${encodeURIComponent(fleetId)}/middleware_templates/${id}`,
  );

export const createMiddlewareTemplate = (
  fleetId: string,
  input: MiddlewareTemplateInput,
) =>
  request<MiddlewareTemplate>(
    "POST",
    `/api/v1/fleets/${encodeURIComponent(fleetId)}/middleware_templates`,
    input,
  );

export const updateMiddlewareTemplate = (
  fleetId: string,
  id: number,
  input: MiddlewareTemplateInput,
) =>
  request<MiddlewareTemplate>(
    "PUT",
    `/api/v1/fleets/${encodeURIComponent(fleetId)}/middleware_templates/${id}`,
    input,
  );

export const deleteMiddlewareTemplate = (fleetId: string, id: number) =>
  request<void>(
    "DELETE",
    `/api/v1/fleets/${encodeURIComponent(fleetId)}/middleware_templates/${id}`,
  );

// --- Revisions ---
export const listRevisions = (fleetId: string) =>
  request<{ revisions: Revision[] }>(
    "GET",
    `/api/v1/fleets/${encodeURIComponent(fleetId)}/revisions`,
  ).then((r) => r.revisions);

export const getRevision = (fleetId: string, number: number) =>
  request<Revision>(
    "GET",
    `/api/v1/fleets/${encodeURIComponent(fleetId)}/revisions/${number}`,
  );

export const publishRevision = (fleetId: string, notes?: string) =>
  request<Revision>(
    "POST",
    `/api/v1/fleets/${encodeURIComponent(fleetId)}/revisions`,
    notes ? { notes } : undefined,
  );

export const rollbackRevision = (fleetId: string, number: number) =>
  request<Revision>(
    "POST",
    `/api/v1/fleets/${encodeURIComponent(fleetId)}/revisions/${number}/rollback`,
  );

// --- Certificates ---
export const listCertificates = (fleetId: string) =>
  request<{ certificates: Certificate[] }>(
    "GET",
    `/api/v1/fleets/${encodeURIComponent(fleetId)}/certificates`,
  ).then((r) => r.certificates);

export const createCertificate = (fleetId: string, input: CertificateInput) =>
  request<Certificate>(
    "POST",
    `/api/v1/fleets/${encodeURIComponent(fleetId)}/certificates`,
    input,
  );

export const deleteCertificate = (fleetId: string, certId: number) =>
  request<void>(
    "DELETE",
    `/api/v1/fleets/${encodeURIComponent(fleetId)}/certificates/${certId}`,
  );

// --- ACME ---
export const getACMEAccount = (fleetId: string) =>
  request<ACMEAccount>(
    "GET",
    `/api/v1/fleets/${encodeURIComponent(fleetId)}/acme/account`,
  );

export const registerACMEAccount = (
  fleetId: string,
  body: { directory_url: string; contact_email: string },
) =>
  request<ACMEAccount>(
    "POST",
    `/api/v1/fleets/${encodeURIComponent(fleetId)}/acme/account`,
    body,
  );

export const listDNSProviders = (fleetId: string) =>
  request<{ dns_providers: DNSProvider[] }>(
    "GET",
    `/api/v1/fleets/${encodeURIComponent(fleetId)}/dns_providers`,
  ).then((r) => r.dns_providers);

export const createDNSProvider = (
  fleetId: string,
  body: { name: string; type: string; config: unknown },
) =>
  request<DNSProvider>(
    "POST",
    `/api/v1/fleets/${encodeURIComponent(fleetId)}/dns_providers`,
    body,
  );

export const deleteDNSProvider = (fleetId: string, id: number) =>
  request<void>(
    "DELETE",
    `/api/v1/fleets/${encodeURIComponent(fleetId)}/dns_providers/${id}`,
  );

// requestACMECertificate enqueues an issuance job. Phase 5b returns
// 202 Accepted with the job; the caller polls getACMEJob until it
// reaches a terminal status.
export const requestACMECertificate = (
  fleetId: string,
  body: ACMEIssueRequest,
) =>
  request<ACMEJob>(
    "POST",
    `/api/v1/fleets/${encodeURIComponent(fleetId)}/certificates/acme`,
    body,
  );

export const getACMEJob = (id: number) =>
  request<ACMEJob>("GET", `/api/v1/jobs/${id}`);

export const listACMEJobs = (fleetId: string) =>
  request<{ jobs: ACMEJob[] }>(
    "GET",
    `/api/v1/fleets/${encodeURIComponent(fleetId)}/jobs`,
  ).then((r) => r.jobs);

// --- Admin tokens ---
export interface AdminTokenMetadata {
  prefix: string;
  name?: string;
  created_at: string;
  last_used_at?: string | null;
  revoked_at?: string | null;
}

export const listAdminTokens = () =>
  request<{ admin_tokens: AdminTokenMetadata[] }>("GET", "/api/v1/admin/tokens").then(
    (r) => r.admin_tokens,
  );

export const mintAdminToken = (name: string) =>
  request<{ token: string; metadata: AdminTokenMetadata }>(
    "POST",
    "/api/v1/admin/tokens",
    { name },
  );

export const revokeAdminToken = (prefix: string) =>
  request<void>(
    "POST",
    `/api/v1/admin/tokens/${encodeURIComponent(prefix)}/revoke`,
  );

// --- Audit log ---
export interface AuditQuery {
  fleetId?: string; // "global" filters to entries with no fleet_id
  before?: number;
  limit?: number;
}

export const listAuditEntries = (q: AuditQuery = {}) => {
  const sp = new URLSearchParams();
  if (q.fleetId !== undefined) sp.set("fleet_id", q.fleetId);
  if (q.before !== undefined) sp.set("before", String(q.before));
  if (q.limit !== undefined) sp.set("limit", String(q.limit));
  const qs = sp.toString();
  const path = "/api/v1/admin/audit" + (qs ? "?" + qs : "");
  return request<{ entries: AuditEntry[] }>("GET", path).then((r) => r.entries);
};
