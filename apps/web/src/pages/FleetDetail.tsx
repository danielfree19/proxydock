import { useCallback, useState } from "react";
import { Link, useParams } from "react-router-dom";
import type { ACMEJob, DNSProvider } from "../types";
import { useEffect } from "react";
import {
  createAgent,
  createCertificate,
  createDNSProvider,
  createWebhook,
  deleteAgent,
  deleteCertificate,
  deleteDNSProvider,
  deleteMiddlewareTemplate,
  deleteProxyHost,
  deleteWebhook,
  getACMEAccount,
  getACMEJob,
  getFleet,
  getRevision,
  listACMEJobs,
  listAgents,
  listCertificates,
  listDNSProviders,
  listMiddlewareTemplates,
  listProxyHosts,
  listRevisions,
  listWebhooks,
  publishRevision,
  registerACMEAccount,
  requestACMECertificate,
  rollbackRevision,
} from "../api";
import { useFetch } from "../components/useFetch";
import { Crumbs, Empty, ErrorBox, fmtAgo, fmtTime } from "../components/Bits";

type Tab =
  | "agents"
  | "proxy_hosts"
  | "library"
  | "revisions"
  | "certificates"
  | "jobs"
  | "webhooks";

export function FleetDetailPage() {
  const { fleetId = "" } = useParams<{ fleetId: string }>();
  const [tab, setTab] = useState<Tab>("proxy_hosts");

  const fleetFetch = useCallback(() => getFleet(fleetId), [fleetId]);
  const fleetQ = useFetch(fleetFetch);

  return (
    <>
      <Crumbs items={[{ label: "Fleets", to: "/" }, { label: fleetId }]} />

      <div className="toolbar">
        <h2>
          {fleetQ.data ? fleetQ.data.name : fleetId}{" "}
          <span className="mono muted" style={{ fontSize: 12 }}>
            {fleetId}
          </span>
        </h2>
        <div className="spacer" />
        {fleetQ.data?.published_revision_id ? (
          <span className="tag">
            published #{fleetQ.data.published_revision_id}
          </span>
        ) : (
          <span className="tag muted">no revision published</span>
        )}
      </div>

      {fleetQ.error && <ErrorBox>{fleetQ.error}</ErrorBox>}

      <SyncBanner
        fleetId={fleetId}
        publishedRevId={fleetQ.data?.published_revision_id ?? null}
        onPublished={fleetQ.refetch}
      />

      <div className="toolbar">
        <button
          className={tab === "proxy_hosts" ? "" : "ghost"}
          onClick={() => setTab("proxy_hosts")}
        >
          Proxy hosts
        </button>
        <button
          className={tab === "agents" ? "" : "ghost"}
          onClick={() => setTab("agents")}
        >
          Agents
        </button>
        <button
          className={tab === "library" ? "" : "ghost"}
          onClick={() => setTab("library")}
        >
          Library
        </button>
        <button
          className={tab === "revisions" ? "" : "ghost"}
          onClick={() => setTab("revisions")}
        >
          Revisions
        </button>
        <button
          className={tab === "certificates" ? "" : "ghost"}
          onClick={() => setTab("certificates")}
        >
          Certificates
        </button>
        <button
          className={tab === "jobs" ? "" : "ghost"}
          onClick={() => setTab("jobs")}
        >
          Jobs
        </button>
        <button
          className={tab === "webhooks" ? "" : "ghost"}
          onClick={() => setTab("webhooks")}
        >
          Webhooks
        </button>
      </div>

      {tab === "proxy_hosts" && <ProxyHostsTab fleetId={fleetId} />}
      {tab === "agents" && (
        <AgentsTab
          fleetId={fleetId}
          publishedRevId={fleetQ.data?.published_revision_id ?? null}
        />
      )}
      {tab === "revisions" && (
        <RevisionsTab fleetId={fleetId} onPublishOrRollback={fleetQ.refetch} />
      )}
      {tab === "library" && <LibraryTab fleetId={fleetId} />}
      {tab === "certificates" && <CertificatesTab fleetId={fleetId} />}
      {tab === "jobs" && <JobsTab fleetId={fleetId} />}
      {tab === "webhooks" && <WebhooksTab fleetId={fleetId} />}
    </>
  );
}

// RevisionPair shows an agent's currently-fetched revision next to
// the fleet's published revision. Equal → green; behind/ahead → warn;
// no current value → just the expected (agent hasn't polled yet).
export function RevisionPair({
  current,
  expected,
}: {
  current: number | null;
  expected: number | null;
}) {
  if (!expected) {
    return current ? (
      <span className="tag">#{current}</span>
    ) : (
      <span className="muted">—</span>
    );
  }
  if (current == null) {
    return (
      <span title="agent hasn't polled yet">
        <span className="muted">—</span>
        <span className="muted" style={{ marginLeft: 6 }}>
          → expected #{expected}
        </span>
      </span>
    );
  }
  if (current === expected) {
    return <span className="tag success">#{current}</span>;
  }
  return (
    <span title={`agent is on #${current}; manager expects #${expected}`}>
      <span className="tag warn">#{current}</span>
      <span className="muted" style={{ margin: "0 4px" }}>→</span>
      <span className="tag">#{expected}</span>
    </span>
  );
}

// WebhooksTab — list, create, and remove fleet-scoped outgoing
// webhooks. Phase 7.
function WebhooksTab({ fleetId }: { fleetId: string }) {
  const fetcher = useCallback(() => listWebhooks(fleetId), [fleetId]);
  const { data, error, loading, refetch } = useFetch(fetcher);

  const [showNew, setShowNew] = useState(false);
  const [name, setName] = useState("");
  const [url, setUrl] = useState("");
  const [secret, setSecret] = useState("");
  const [events, setEvents] = useState<string[]>(["revision_published"]);
  const [submitErr, setSubmitErr] = useState<string | undefined>();

  async function onCreate(e: React.FormEvent) {
    e.preventDefault();
    setSubmitErr(undefined);
    try {
      await createWebhook(fleetId, { name, url, secret, events });
      setName("");
      setUrl("");
      setSecret("");
      setEvents(["revision_published"]);
      setShowNew(false);
      refetch();
    } catch (e) {
      setSubmitErr(e instanceof Error ? e.message : String(e));
    }
  }

  async function onDelete(id: number, name: string) {
    if (!confirm(`Delete webhook "${name}"?`)) return;
    try {
      await deleteWebhook(fleetId, id);
      refetch();
    } catch (e) {
      alert(e instanceof Error ? e.message : String(e));
    }
  }

  function toggleEvent(ev: string) {
    setEvents((cur) =>
      cur.includes(ev) ? cur.filter((e) => e !== ev) : [...cur, ev],
    );
  }

  const allEvents = [
    "revision_published",
    "revision_rolled_back",
    "acme_certificate_issued",
  ];

  return (
    <>
      <div className="toolbar">
        <h3 style={{ margin: 0 }}>Webhooks</h3>
        <div className="spacer" />
        <button onClick={() => setShowNew((s) => !s)}>
          {showNew ? "Cancel" : "New webhook"}
        </button>
      </div>

      {showNew && (
        <form className="card" onSubmit={onCreate}>
          <div className="row">
            <div>
              <label>Name</label>
              <input
                type="text"
                value={name}
                onChange={(e) => setName(e.target.value)}
                placeholder="slack-ops"
                required
              />
            </div>
            <div style={{ flex: 2 }}>
              <label>URL</label>
              <input
                type="text"
                value={url}
                onChange={(e) => setUrl(e.target.value)}
                placeholder="https://hooks.example.com/..."
                required
              />
            </div>
          </div>
          <div className="row" style={{ marginTop: 8 }}>
            <div style={{ flex: 1 }}>
              <label>HMAC secret (optional)</label>
              <input
                type="password"
                value={secret}
                onChange={(e) => setSecret(e.target.value)}
                placeholder="X-Webhook-Signature: sha256=…"
              />
              <p className="muted" style={{ fontSize: 11, marginTop: 4 }}>
                Receivers verify <span className="mono">X-Webhook-Signature</span>{" "}
                against the shared secret to confirm authenticity.
              </p>
            </div>
            <div style={{ flex: 1 }}>
              <label>Events</label>
              {allEvents.map((ev) => (
                <label key={ev} style={{ display: "block", fontSize: 12 }}>
                  <input
                    type="checkbox"
                    checked={events.includes(ev)}
                    onChange={() => toggleEvent(ev)}
                  />{" "}
                  <span className="mono">{ev}</span>
                </label>
              ))}
            </div>
          </div>
          {submitErr && (
            <div className="error" style={{ marginTop: 12 }}>
              {submitErr}
            </div>
          )}
          <div className="toolbar" style={{ marginTop: 12 }}>
            <div className="spacer" />
            <button type="submit">Create</button>
          </div>
        </form>
      )}

      {loading && <div className="muted">Loading…</div>}
      {error && <ErrorBox>{error}</ErrorBox>}
      {data && data.length === 0 && (
        <Empty>
          No webhooks. Add one to receive POST notifications when a
          revision is published or rolled back.
        </Empty>
      )}
      {data && data.length > 0 && (
        <div className="card" style={{ padding: 0 }}>
          <table>
            <thead>
              <tr>
                <th>Name</th>
                <th>URL</th>
                <th>Events</th>
                <th>HMAC</th>
                <th>Status</th>
                <th />
              </tr>
            </thead>
            <tbody>
              {data.map((w) => (
                <tr key={w.id}>
                  <td className="mono">{w.name}</td>
                  <td className="mono" style={{ fontSize: 12 }}>
                    {w.url}
                  </td>
                  <td className="mono" style={{ fontSize: 12 }}>
                    {w.events.join(", ")}
                  </td>
                  <td>
                    {w.has_secret ? (
                      <span className="tag success">signed</span>
                    ) : (
                      <span className="muted">unsigned</span>
                    )}
                  </td>
                  <td>
                    {w.enabled ? (
                      <span className="tag success">enabled</span>
                    ) : (
                      <span className="tag warn">disabled</span>
                    )}
                  </td>
                  <td style={{ textAlign: "right" }}>
                    <button
                      className="ghost"
                      onClick={() => onDelete(w.id, w.name)}
                    >
                      Delete
                    </button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </>
  );
}

// LibraryTab — list/edit fleet-scoped middleware templates. Apply-by-
// copy semantics; the proxy host form pulls from this list via the
// `Apply template` dropdown.
function LibraryTab({ fleetId }: { fleetId: string }) {
  const fetcher = useCallback(() => listMiddlewareTemplates(fleetId), [fleetId]);
  const { data, error, loading, refetch } = useFetch(fetcher);

  async function onDelete(id: number, name: string) {
    if (!confirm(`Delete template "${name}"? Hosts that already applied it keep their copy.`)) return;
    try {
      await deleteMiddlewareTemplate(fleetId, id);
      refetch();
    } catch (e) {
      alert(e instanceof Error ? e.message : String(e));
    }
  }

  return (
    <>
      <div className="toolbar">
        <h3 style={{ margin: 0 }}>Middleware library</h3>
        <div className="spacer" />
        <Link
          to={`/fleets/${encodeURIComponent(fleetId)}/middleware-templates/new`}
          className="button"
        >
          New template
        </Link>
      </div>
      {loading && <div className="muted">Loading…</div>}
      {error && <ErrorBox>{error}</ErrorBox>}
      {data && data.length === 0 && (
        <Empty>
          No templates yet. Create one to capture a reusable middleware chain
          (e.g. <span className="mono">standard-oidc</span>); proxy hosts can
          then apply it from the host form.
        </Empty>
      )}
      {data && data.length > 0 && (
        <div className="card" style={{ padding: 0 }}>
          <table>
            <thead>
              <tr>
                <th>Name</th>
                <th>Description</th>
                <th>Types</th>
                <th>Updated</th>
                <th />
              </tr>
            </thead>
            <tbody>
              {data.map((tpl) => (
                <tr key={tpl.id}>
                  <td>
                    <Link
                      to={`/fleets/${encodeURIComponent(fleetId)}/middleware-templates/${tpl.id}`}
                      className="mono"
                    >
                      {tpl.name}
                    </Link>
                  </td>
                  <td className="muted">{tpl.description || "—"}</td>
                  <td className="mono" style={{ fontSize: 12 }}>
                    {tpl.middlewares.map((mw) => mw.type).join(", ") || "—"}
                  </td>
                  <td className="muted" title={fmtTime(tpl.updated_at)}>
                    {fmtAgo(tpl.updated_at)}
                  </td>
                  <td style={{ textAlign: "right" }}>
                    <button
                      className="ghost"
                      onClick={() => onDelete(tpl.id, tpl.name)}
                    >
                      Delete
                    </button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </>
  );
}

// SyncBanner detects "you have unpublished changes" by comparing the
// published revision's generated_at against the latest proxy host
// edit. Surfaces a one-click Publish so users don't have to dig into
// the Revisions tab. Certificate uploads / deletes are not tracked
// here yet — ACME renewals auto-publish, and manual cert ops are
// rare enough that the banner staleness is acceptable.
function SyncBanner({
  fleetId,
  publishedRevId,
  onPublished,
}: {
  fleetId: string;
  publishedRevId: number | null;
  onPublished: () => void;
}) {
  const hostsFetch = useCallback(() => listProxyHosts(fleetId), [fleetId]);
  const hostsQ = useFetch(hostsFetch);

  const revFetch = useCallback(
    () =>
      publishedRevId
        ? getRevision(fleetId, publishedRevId)
        : Promise.resolve(null),
    [fleetId, publishedRevId],
  );
  const revQ = useFetch(revFetch);

  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | undefined>();

  async function onPublish() {
    setBusy(true);
    setErr(undefined);
    try {
      await publishRevision(fleetId);
      onPublished();
      hostsQ.refetch();
      revQ.refetch();
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  }

  if (!hostsQ.data) return null;

  // No published revision yet — different message, same CTA.
  if (!publishedRevId) {
    return (
      <div className="notice" style={{ marginBottom: 12 }}>
        <strong>No revision published yet.</strong> Agents won't serve any
        traffic until you publish.
        <button
          onClick={onPublish}
          disabled={busy}
          style={{ marginLeft: 12 }}
        >
          {busy ? "Publishing…" : "Publish initial revision"}
        </button>
        {err && (
          <div className="error" style={{ marginTop: 8 }}>
            {err}
          </div>
        )}
      </div>
    );
  }

  // Wait for the published revision so we can compare timestamps.
  if (!revQ.data) return null;

  const publishedAt = new Date(revQ.data.generated_at).getTime();
  const stale = hostsQ.data.filter(
    (h) => new Date(h.updated_at).getTime() > publishedAt,
  );

  if (stale.length === 0) {
    return (
      <div
        className="notice"
        style={{
          marginBottom: 12,
          background: "transparent",
          border: "1px solid var(--success, #2a6)",
        }}
      >
        <span className="tag success">in sync</span> All proxy hosts match
        published revision <span className="mono">#{publishedRevId}</span>{" "}
        ({fmtAgo(revQ.data.generated_at)}).
      </div>
    );
  }

  return (
    <div className="notice warn" style={{ marginBottom: 12 }}>
      <strong>{stale.length}</strong> proxy host{stale.length === 1 ? "" : "s"}{" "}
      edited since revision <span className="mono">#{publishedRevId}</span> was
      published — agents are still serving the old config.
      <div className="muted" style={{ fontSize: 12, marginTop: 4 }}>
        Pending: {stale.map((h) => h.name).join(", ")}
      </div>
      <button
        onClick={onPublish}
        disabled={busy}
        style={{ marginTop: 8 }}
      >
        {busy ? "Publishing…" : `Publish revision`}
      </button>
      {err && (
        <div className="error" style={{ marginTop: 8 }}>
          {err}
        </div>
      )}
    </div>
  );
}

function ProxyHostsTab({ fleetId }: { fleetId: string }) {
  const fetcher = useCallback(() => listProxyHosts(fleetId), [fleetId]);
  const { data, error, loading, refetch } = useFetch(fetcher);

  async function onDelete(id: number, name: string) {
    if (!confirm(`Delete proxy host "${name}"?`)) return;
    try {
      await deleteProxyHost(fleetId, id);
      refetch();
    } catch (e) {
      alert(e instanceof Error ? e.message : String(e));
    }
  }

  return (
    <>
      <div className="toolbar">
        <h3 style={{ margin: 0 }}>Proxy hosts</h3>
        <div className="spacer" />
        <Link to={`/fleets/${encodeURIComponent(fleetId)}/proxy-hosts/new`}>
          <button>Add proxy host</button>
        </Link>
      </div>
      {loading && <div className="muted">Loading…</div>}
      {error && <ErrorBox>{error}</ErrorBox>}
      {data && data.length === 0 && (
        <Empty>
          No proxy hosts. Add one, then publish a revision so agents pick it up.
        </Empty>
      )}
      {data && data.length > 0 && (
        <div className="card" style={{ padding: 0 }}>
          <table>
            <thead>
              <tr>
                <th>Name</th>
                <th>Proto</th>
                <th>Domain / SNI</th>
                <th>Upstream</th>
                <th>Entry points</th>
                <th>Selector</th>
                <th>Middlewares</th>
                <th>TLS</th>
                <th>Status</th>
                <th />
              </tr>
            </thead>
            <tbody>
              {data.map((p) => (
                <tr key={p.id}>
                  <td>
                    <Link
                      to={`/fleets/${encodeURIComponent(fleetId)}/proxy-hosts/${p.id}`}
                      className="mono"
                    >
                      {p.name}
                    </Link>
                  </td>
                  <td>
                    <span className="tag mono" style={{ fontSize: 11 }}>
                      {(p.protocol || "http").toUpperCase()}
                    </span>
                  </td>
                  <td className="mono">
                    {p.protocol === "udp" ? (
                      <span className="muted">—</span>
                    ) : (
                      p.domain
                    )}
                  </td>
                  <td className="mono muted">{p.upstream_url}</td>
                  <td className="mono">{p.entry_points.join(", ")}</td>
                  <td className="mono" style={{ fontSize: 12 }}>
                    {p.label_selector ? (
                      p.label_selector
                    ) : (
                      <span className="muted">all agents</span>
                    )}
                  </td>
                  <td>
                    {p.middlewares.length === 0 ? (
                      <span className="muted">—</span>
                    ) : (
                      p.middlewares.map((m) => (
                        <span key={m.name} className="tag" style={{ marginRight: 4 }}>
                          {m.type}
                        </span>
                      ))
                    )}
                  </td>
                  <td>
                    {p.tls ? (
                      <span className="tag success">on</span>
                    ) : (
                      <span className="muted">—</span>
                    )}
                  </td>
                  <td>
                    {p.enabled ? (
                      <span className="tag success">enabled</span>
                    ) : (
                      <span className="tag muted">disabled</span>
                    )}
                  </td>
                  <td style={{ textAlign: "right" }}>
                    <button className="ghost" onClick={() => onDelete(p.id, p.name)}>
                      Delete
                    </button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </>
  );
}

function AgentsTab({
  fleetId,
  publishedRevId,
}: {
  fleetId: string;
  publishedRevId: number | null;
}) {
  const fetcher = useCallback(() => listAgents(fleetId), [fleetId]);
  const { data, error, loading, refetch } = useFetch(fetcher);
  const [showNew, setShowNew] = useState(false);
  const [newId, setNewId] = useState("");
  const [newName, setNewName] = useState("");
  const [submitErr, setSubmitErr] = useState<string | undefined>();

  async function onCreate(e: React.FormEvent) {
    e.preventDefault();
    if (!newId || !newName) return;
    setSubmitErr(undefined);
    try {
      await createAgent(fleetId, newId, newName);
      setNewId("");
      setNewName("");
      setShowNew(false);
      refetch();
    } catch (e) {
      setSubmitErr(e instanceof Error ? e.message : String(e));
    }
  }

  async function onDelete(id: string) {
    if (!confirm(`Delete agent "${id}"? Tokens are revoked along with it.`))
      return;
    try {
      await deleteAgent(id);
      refetch();
    } catch (e) {
      alert(e instanceof Error ? e.message : String(e));
    }
  }

  return (
    <>
      <div className="toolbar">
        <h3 style={{ margin: 0 }}>Agents</h3>
        <div className="spacer" />
        <button onClick={() => setShowNew((s) => !s)}>
          {showNew ? "Cancel" : "Register agent"}
        </button>
      </div>

      {showNew && (
        <form className="card" onSubmit={onCreate}>
          <div className="row">
            <div>
              <label>Agent ID</label>
              <input
                type="text"
                value={newId}
                onChange={(e) => setNewId(e.target.value)}
                placeholder="traefik-3"
                required
              />
            </div>
            <div>
              <label>Display name</label>
              <input
                type="text"
                value={newName}
                onChange={(e) => setNewName(e.target.value)}
                placeholder="Traefik node 3"
                required
              />
            </div>
            <div className="shrink" style={{ alignSelf: "flex-end" }}>
              <button type="submit">Register</button>
            </div>
          </div>
          {submitErr && (
            <div className="error" style={{ marginTop: 12 }}>
              {submitErr}
            </div>
          )}
        </form>
      )}

      {loading && <div className="muted">Loading…</div>}
      {error && <ErrorBox>{error}</ErrorBox>}
      {data && data.length === 0 && (
        <Empty>
          No agents registered yet. Click <em>Register agent</em> to add one,
          then mint a bearer token from the agent detail page.
        </Empty>
      )}
      {data && data.length > 0 && (
        <div className="card" style={{ padding: 0 }}>
          <table>
            <thead>
              <tr>
                <th>ID</th>
                <th>Name</th>
                <th>Labels</th>
                <th>Last heartbeat</th>
                <th>Revision (current → expected)</th>
                <th>Provider</th>
                <th>Last error</th>
                <th />
              </tr>
            </thead>
            <tbody>
              {data.map((a) => (
                <tr key={a.id}>
                  <td>
                    <Link to={`/agents/${encodeURIComponent(a.id)}`} className="mono">
                      {a.id}
                    </Link>
                  </td>
                  <td>{a.name}</td>
                  <td className="mono" style={{ fontSize: 12 }}>
                    {a.labels && a.labels.length > 0 ? (
                      a.labels.map((l) => (
                        <span key={l} className="tag" style={{ marginRight: 4 }}>
                          {l}
                        </span>
                      ))
                    ) : (
                      <span className="muted">—</span>
                    )}
                  </td>
                  <td title={fmtTime(a.last_heartbeat_at)}>
                    {fmtAgo(a.last_heartbeat_at)}
                  </td>
                  <td>
                    <RevisionPair
                      current={a.last_revision_seen ?? null}
                      expected={publishedRevId}
                    />
                  </td>
                  <td className="mono muted">
                    {a.last_provider_version || "—"}
                  </td>
                  <td>
                    {a.last_error ? (
                      <span className="tag danger">{a.last_error}</span>
                    ) : (
                      <span className="muted">—</span>
                    )}
                  </td>
                  <td style={{ textAlign: "right" }}>
                    <button className="ghost" onClick={() => onDelete(a.id)}>
                      Delete
                    </button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </>
  );
}

function RevisionsTab({
  fleetId,
  onPublishOrRollback,
}: {
  fleetId: string;
  onPublishOrRollback: () => void;
}) {
  const fetcher = useCallback(() => listRevisions(fleetId), [fleetId]);
  const { data, error, loading, refetch } = useFetch(fetcher);
  const [notes, setNotes] = useState("");
  const [busy, setBusy] = useState(false);
  const [opErr, setOpErr] = useState<string | undefined>();

  async function onPublish() {
    setBusy(true);
    setOpErr(undefined);
    try {
      await publishRevision(fleetId, notes || undefined);
      setNotes("");
      refetch();
      onPublishOrRollback();
    } catch (e) {
      setOpErr(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  }

  async function onRollback(num: number) {
    if (!confirm(`Roll back to revision #${num}? A fresh revision will be created and published.`))
      return;
    try {
      await rollbackRevision(fleetId, num);
      refetch();
      onPublishOrRollback();
    } catch (e) {
      alert(e instanceof Error ? e.message : String(e));
    }
  }

  return (
    <>
      <div className="card">
        <h3 style={{ marginTop: 0 }}>Publish a new revision</h3>
        <p className="muted" style={{ marginTop: 0 }}>
          Compiles the current proxy hosts into a fresh revision and marks it
          as the published one. Agents pick it up on their next poll.
        </p>
        <div className="row">
          <input
            type="text"
            placeholder="Notes (optional)"
            value={notes}
            onChange={(e) => setNotes(e.target.value)}
          />
          <div className="shrink">
            <button onClick={onPublish} disabled={busy}>
              Publish
            </button>
          </div>
        </div>
        {opErr && (
          <div className="error" style={{ marginTop: 12 }}>
            {opErr}
          </div>
        )}
      </div>

      <div className="toolbar">
        <h3 style={{ margin: 0 }}>History</h3>
        <div className="spacer" />
      </div>

      {loading && <div className="muted">Loading…</div>}
      {error && <ErrorBox>{error}</ErrorBox>}
      {data && data.length === 0 && <Empty>No revisions published yet.</Empty>}
      {data && data.length > 0 && (
        <div className="card" style={{ padding: 0 }}>
          <table>
            <thead>
              <tr>
                <th>Number</th>
                <th>ETag</th>
                <th>Notes</th>
                <th>Generated</th>
                <th>Diff</th>
                <th />
              </tr>
            </thead>
            <tbody>
              {data.map((r, idx) => {
                // The list comes back number-desc, so the entry "below"
                // this row in the array is the previous revision in
                // chronological order.
                const prev = data[idx + 1];
                return (
                  <tr key={r.id}>
                    <td>
                      <Link
                        to={`/fleets/${encodeURIComponent(fleetId)}/revisions/${r.number}`}
                        className="mono"
                      >
                        #{r.number}
                      </Link>
                    </td>
                    <td className="mono muted">{r.etag}</td>
                    <td>{r.notes || <span className="muted">—</span>}</td>
                    <td className="muted">{fmtTime(r.generated_at)}</td>
                    <td>
                      {prev ? (
                        <Link
                          to={`/fleets/${encodeURIComponent(fleetId)}/revisions/${prev.number}/diff/${r.number}`}
                          className="mono"
                        >
                          vs #{prev.number}
                        </Link>
                      ) : (
                        <span className="muted">—</span>
                      )}
                    </td>
                    <td style={{ textAlign: "right" }}>
                      <button className="ghost" onClick={() => onRollback(r.number)}>
                        Roll back
                      </button>
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
      )}
    </>
  );
}


function CertificatesTab({ fleetId }: { fleetId: string }) {
  const fetcher = useCallback(() => listCertificates(fleetId), [fleetId]);
  const { data, error, loading, refetch } = useFetch(fetcher);
  const [showNew, setShowNew] = useState(false);
  const [name, setName] = useState("");
  const [certPem, setCertPem] = useState("");
  const [keyPem, setKeyPem] = useState("");
  const [busy, setBusy] = useState(false);
  const [submitErr, setSubmitErr] = useState<string | undefined>();

  async function onCreate(e: React.FormEvent) {
    e.preventDefault();
    setBusy(true);
    setSubmitErr(undefined);
    try {
      await createCertificate(fleetId, {
        name,
        cert_pem: certPem,
        key_pem: keyPem,
      });
      setName("");
      setCertPem("");
      setKeyPem("");
      setShowNew(false);
      refetch();
    } catch (e) {
      setSubmitErr(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  }

  async function onDelete(id: number, name: string) {
    if (!confirm("Delete certificate \"" + name + "\"? Revisions referencing it stay frozen, but new revisions will lose it from the pool."))
      return;
    try {
      await deleteCertificate(fleetId, id);
      refetch();
    } catch (e) {
      alert(e instanceof Error ? e.message : String(e));
    }
  }

  return (
    <>
      <ACMEPanel fleetId={fleetId} onIssued={refetch} />

      <div className="toolbar">
        <h3 style={{ margin: 0 }}>Certificates</h3>
        <div className="spacer" />
        <button onClick={() => setShowNew((s) => !s)}>
          {showNew ? "Cancel" : "Upload certificate"}
        </button>
      </div>
      <p className="muted" style={{ marginTop: -8 }}>
        PEM certificate + key are pooled per fleet. Traefik picks a cert by SNI
        for any router with TLS enabled. Private keys are stored in plaintext —
        Phase 5 hardening adds column-level encryption.
      </p>

      {showNew && (
        <form className="card" onSubmit={onCreate}>
          <div>
            <label>Name</label>
            <input
              type="text"
              value={name}
              onChange={(e) => setName(e.target.value)}
              placeholder="example.com-2026"
              required
            />
          </div>
          <div style={{ marginTop: 12 }}>
            <label>Certificate (PEM, full chain)</label>
            <textarea
              rows={6}
              value={certPem}
              onChange={(e) => setCertPem(e.target.value)}
              placeholder="-----BEGIN CERTIFICATE-----..."
              required
              style={{ fontFamily: "var(--mono)", fontSize: 12 }}
            />
          </div>
          <div style={{ marginTop: 12 }}>
            <label>Private key (PEM)</label>
            <textarea
              rows={6}
              value={keyPem}
              onChange={(e) => setKeyPem(e.target.value)}
              placeholder="-----BEGIN PRIVATE KEY-----..."
              required
              style={{ fontFamily: "var(--mono)", fontSize: 12 }}
            />
          </div>
          {submitErr && (
            <div className="error" style={{ marginTop: 12 }}>
              {submitErr}
            </div>
          )}
          <div className="toolbar" style={{ marginTop: 12 }}>
            <div className="spacer" />
            <button type="submit" disabled={busy}>
              Upload
            </button>
          </div>
        </form>
      )}

      {loading && <div className="muted">Loading…</div>}
      {error && <ErrorBox>{error}</ErrorBox>}
      {data && data.length === 0 && (
        <Empty>
          No certificates uploaded yet. Add one to enable TLS on a proxy host.
        </Empty>
      )}
      {data && data.length > 0 && (
        <div className="card" style={{ padding: 0 }}>
          <table>
            <thead>
              <tr>
                <th>Name</th>
                <th>Source</th>
                <th>Subject</th>
                <th>DNS names</th>
                <th>Expiry</th>
                <th>Fingerprint</th>
                <th />
              </tr>
            </thead>
            <tbody>
              {data.map((c) => {
                const exp = new Date(c.not_after);
                const days = Math.floor((exp.getTime() - Date.now()) / 86400000);
                const expClass =
                  days < 0
                    ? "tag danger"
                    : days < 14
                    ? "tag warn"
                    : "tag success";
                return (
                  <tr key={c.id}>
                    <td className="mono">{c.name}</td>
                    <td>
                      {c.source === "acme" ? (
                        <span className="tag">ACME</span>
                      ) : (
                        <span className="tag muted">upload</span>
                      )}
                    </td>
                    <td className="muted">{c.subject}</td>
                    <td className="mono">{c.dns_names.join(", ") || "—"}</td>
                    <td>
                      <span className={expClass}>
                        {days < 0
                          ? "expired " + (-days) + "d ago"
                          : "expires in " + days + "d"}
                      </span>{" "}
                      <span className="muted" style={{ fontSize: 11 }}>
                        {fmtTime(c.not_after)}
                      </span>
                    </td>
                    <td className="mono muted" title={c.fingerprint}>
                      {c.fingerprint.slice(0, 23)}…
                    </td>
                    <td style={{ textAlign: "right" }}>
                      <button className="ghost" onClick={() => onDelete(c.id, c.name)}>
                        Delete
                      </button>
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
      )}
    </>
  );
}

// ACMEPanel composes three independent sections that each own only the
// state for their own form. Before the split this component held 16
// useStates and three async submit handlers in one body — easy to break
// on edit, hard to reason about. Now each sub-component is its own
// concern; only the fetch state for `account` and `providers` lives at
// this level because both children read it.
function ACMEPanel({ fleetId, onIssued }: { fleetId: string; onIssued: () => void }) {
  const accountFetch = useCallback(
    () =>
      getACMEAccount(fleetId).catch((e) => {
        // Treat 404 as "no account yet"; rethrow anything else.
        if (e instanceof Error && e.message.startsWith("HTTP 404")) return null;
        throw e;
      }),
    [fleetId],
  );
  const dnsFetch = useCallback(() => listDNSProviders(fleetId), [fleetId]);
  const account = useFetch(accountFetch);
  const providers = useFetch(dnsFetch);

  return (
    <div className="card">
      <h3 style={{ margin: 0, marginBottom: 12 }}>ACME (DNS-01)</h3>
      <p className="muted" style={{ marginTop: 0 }}>
        Lets the manager request certificates from any ACME CA via DNS-01. Each
        fleet has one account; one or more named DNS providers handle the
        challenge records.
      </p>

      <ACMEAccountSection
        fleetId={fleetId}
        loading={account.loading}
        error={account.error}
        data={account.data ?? null}
        refetch={account.refetch}
      />

      <DNSProvidersSection
        fleetId={fleetId}
        loading={providers.loading}
        error={providers.error}
        data={providers.data ?? []}
        refetch={providers.refetch}
      />

      <IssueCertSection
        fleetId={fleetId}
        hasAccount={!!account.data}
        providers={providers.data ?? []}
        onIssued={onIssued}
      />
    </div>
  );
}

function sectionHeader(label: string) {
  return (
    <h4
      style={{
        marginTop: 16,
        marginBottom: 4,
        fontSize: 12,
        color: "var(--muted)",
        textTransform: "uppercase",
        letterSpacing: 0.6,
      }}
    >
      {label}
    </h4>
  );
}

function ACMEAccountSection({
  fleetId,
  loading,
  error,
  data,
  refetch,
}: {
  fleetId: string;
  loading: boolean;
  error: string | undefined;
  data: { contact_email: string; directory_url: string } | null;
  refetch: () => void;
}) {
  const [show, setShow] = useState(false);
  const [dir, setDir] = useState("https://acme-v02.api.letsencrypt.org/directory");
  const [email, setEmail] = useState("");
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | undefined>();

  async function onSubmit(e: React.FormEvent) {
    e.preventDefault();
    setBusy(true);
    setErr(undefined);
    try {
      await registerACMEAccount(fleetId, { directory_url: dir, contact_email: email });
      setShow(false);
      refetch();
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  }

  return (
    <>
      {sectionHeader("Account")}
      {error && <ErrorBox>{error}</ErrorBox>}
      {loading && <div className="muted">Loading…</div>}
      {!loading && data && (
        <p className="mono" style={{ fontSize: 12 }}>
          {data.contact_email} @ {data.directory_url}
        </p>
      )}
      {!loading && !data && (
        <>
          <p className="muted">No account registered yet.</p>
          <button onClick={() => setShow((s) => !s)}>
            {show ? "Cancel" : "Register account"}
          </button>
          {show && (
            <form onSubmit={onSubmit} style={{ marginTop: 12 }}>
              <div className="row">
                <div>
                  <label>Directory URL</label>
                  <input
                    type="text"
                    value={dir}
                    onChange={(e) => setDir(e.target.value)}
                    required
                  />
                </div>
                <div>
                  <label>Contact email</label>
                  <input
                    type="text"
                    value={email}
                    onChange={(e) => setEmail(e.target.value)}
                    required
                  />
                </div>
                <div className="shrink" style={{ alignSelf: "flex-end" }}>
                  <button type="submit" disabled={busy}>
                    Register
                  </button>
                </div>
              </div>
              {err && (
                <div className="error" style={{ marginTop: 8 }}>
                  {err}
                </div>
              )}
            </form>
          )}
        </>
      )}
    </>
  );
}

// dnsConfigSkeletons gives users a working starting point for each
// provider type. Used by both the create form and (eventually) any
// future "edit DNS provider" UI.
const dnsConfigSkeletons: Record<string, string> = {
  pebble: '{"base_url":"http://challtestsrv:8055"}',
  cloudflare: '{"api_token":"<scoped Zone.DNS:Edit token>","zone_name":"example.com"}',
  route53:
    '{"zone_name":"example.com","region":"us-east-1","access_key":"<optional, leave out to use IAM role>","secret_key":""}',
};

function DNSProvidersSection({
  fleetId,
  loading,
  error,
  data,
  refetch,
}: {
  fleetId: string;
  loading: boolean;
  error: string | undefined;
  data: DNSProvider[];
  refetch: () => void;
}) {
  const [show, setShow] = useState(false);
  const [name, setName] = useState("");
  const [type, setType] = useState("pebble");
  const [config, setConfig] = useState(dnsConfigSkeletons.pebble);
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | undefined>();

  function onChangeType(t: string) {
    setType(t);
    setConfig(dnsConfigSkeletons[t] ?? "{}");
  }

  async function onSubmit(e: React.FormEvent) {
    e.preventDefault();
    setBusy(true);
    setErr(undefined);
    let cfg: unknown = {};
    try {
      cfg = JSON.parse(config);
    } catch (e) {
      setErr("config JSON: " + (e instanceof Error ? e.message : String(e)));
      setBusy(false);
      return;
    }
    try {
      await createDNSProvider(fleetId, { name, type, config: cfg });
      setShow(false);
      setName("");
      setConfig(dnsConfigSkeletons[type] ?? "{}");
      refetch();
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  }

  async function onDelete(id: number, providerName: string) {
    if (!confirm("Delete DNS provider \"" + providerName + "\"?")) return;
    try {
      await deleteDNSProvider(fleetId, id);
      refetch();
    } catch (e) {
      alert(e instanceof Error ? e.message : String(e));
    }
  }

  return (
    <>
      {sectionHeader("DNS providers")}
      {error && <ErrorBox>{error}</ErrorBox>}
      {loading && <div className="muted">Loading…</div>}
      {!loading && data.length === 0 && (
        <p className="muted">No DNS providers configured yet.</p>
      )}
      {data.length > 0 && (
        <table>
          <thead>
            <tr>
              <th>Name</th>
              <th>Type</th>
              <th />
            </tr>
          </thead>
          <tbody>
            {data.map((p) => (
              <tr key={p.id}>
                <td className="mono">{p.name}</td>
                <td>{p.type}</td>
                <td style={{ textAlign: "right" }}>
                  <button className="ghost" onClick={() => onDelete(p.id, p.name)}>
                    Delete
                  </button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
      <button
        className="ghost"
        onClick={() => setShow((s) => !s)}
        style={{ marginTop: 8 }}
      >
        {show ? "Cancel" : "Add DNS provider"}
      </button>
      {show && (
        <form onSubmit={onSubmit} style={{ marginTop: 12 }}>
          <div className="row">
            <div>
              <label>Name</label>
              <input
                type="text"
                value={name}
                onChange={(e) => setName(e.target.value)}
                placeholder="primary"
                required
              />
            </div>
            <div>
              <label>Type</label>
              <select value={type} onChange={(e) => onChangeType(e.target.value)}>
                <option value="pebble">pebble (test)</option>
                <option value="cloudflare">cloudflare</option>
                <option value="route53">route53 (AWS)</option>
              </select>
            </div>
          </div>
          <div style={{ marginTop: 12 }}>
            <label>Config (JSON)</label>
            <textarea
              rows={4}
              value={config}
              onChange={(e) => setConfig(e.target.value)}
              style={{ fontFamily: "var(--mono)", fontSize: 12 }}
            />
          </div>
          {err && (
            <div className="error" style={{ marginTop: 8 }}>
              {err}
            </div>
          )}
          <div className="toolbar" style={{ marginTop: 8 }}>
            <div className="spacer" />
            <button type="submit" disabled={busy}>
              Save
            </button>
          </div>
        </form>
      )}
    </>
  );
}

function IssueCertSection({
  fleetId,
  hasAccount,
  providers,
  onIssued,
}: {
  fleetId: string;
  hasAccount: boolean;
  providers: DNSProvider[];
  onIssued: () => void;
}) {
  const [name, setName] = useState("");
  const [domains, setDomains] = useState("");
  const [provider, setProvider] = useState("");
  const [submitting, setSubmitting] = useState(false);
  const [err, setErr] = useState<string | undefined>();
  // tracking is the job we last submitted, polled until terminal.
  const [tracking, setTracking] = useState<ACMEJob | undefined>();

  const canIssue = hasAccount && providers.length > 0;

  async function onSubmit(e: React.FormEvent) {
    e.preventDefault();
    setSubmitting(true);
    setErr(undefined);
    try {
      const job = await requestACMECertificate(fleetId, {
        name,
        dns_names: domains
          .split(",")
          .map((s) => s.trim())
          .filter(Boolean),
        dns_provider: provider,
      });
      setName("");
      setDomains("");
      setTracking(job);
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
    } finally {
      setSubmitting(false);
    }
  }

  // Poll the tracked job once a second until it reaches a terminal
  // state. The interval is generous: ACME flows take 5–30 s and we
  // don't want to hammer the manager.
  useEffect(() => {
    if (!tracking) return;
    if (tracking.status !== "pending" && tracking.status !== "running") return;
    const id = tracking.id;
    let cancelled = false;
    const t = window.setInterval(() => {
      getACMEJob(id)
        .then((j) => {
          if (cancelled) return;
          setTracking(j);
          if (j.status === "succeeded") {
            onIssued();
          }
        })
        .catch(() => {
          // Transient errors get a single retry on the next tick.
        });
    }, 1500);
    return () => {
      cancelled = true;
      window.clearInterval(t);
    };
  }, [tracking, onIssued]);

  return (
    <>
      {sectionHeader("Issue a certificate")}
      {!canIssue && (
        <p className="muted">
          Register an ACME account and add at least one DNS provider to issue
          a certificate.
        </p>
      )}
      {canIssue && (
        <form onSubmit={onSubmit}>
          <div className="row">
            <div>
              <label>Cert name</label>
              <input
                type="text"
                value={name}
                onChange={(e) => setName(e.target.value)}
                placeholder="example.com"
                required
              />
            </div>
            <div>
              <label>DNS names (comma separated)</label>
              <input
                type="text"
                value={domains}
                onChange={(e) => setDomains(e.target.value)}
                placeholder="example.com, www.example.com"
                required
              />
            </div>
            <div>
              <label>DNS provider</label>
              <select
                value={provider}
                onChange={(e) => setProvider(e.target.value)}
                required
              >
                <option value="">Select…</option>
                {providers.map((p) => (
                  <option key={p.id} value={p.name}>
                    {p.name} ({p.type})
                  </option>
                ))}
              </select>
            </div>
            <div className="shrink" style={{ alignSelf: "flex-end" }}>
              <button type="submit" disabled={submitting}>
                {submitting ? "Submitting…" : "Request"}
              </button>
            </div>
          </div>
          {err && (
            <div className="error" style={{ marginTop: 8 }}>
              {err}
            </div>
          )}
          {tracking && (
            <JobStatusInline job={tracking} onClose={() => setTracking(undefined)} />
          )}
        </form>
      )}
    </>
  );
}

function jobStatusTag(status: ACMEJob["status"]) {
  switch (status) {
    case "succeeded":
      return <span className="tag success">succeeded</span>;
    case "failed":
      return <span className="tag danger">failed</span>;
    case "running":
      return <span className="tag warn">running…</span>;
    default:
      return <span className="tag muted">pending</span>;
  }
}

function JobStatusInline({ job, onClose }: { job: ACMEJob; onClose: () => void }) {
  const terminal = job.status === "succeeded" || job.status === "failed";
  return (
    <div
      className={job.status === "failed" ? "error" : "notice"}
      style={{ marginTop: 12 }}
    >
      <div className="row">
        <div>
          <strong>Job #{job.id}</strong> {jobStatusTag(job.status)}
        </div>
        <div className="shrink">
          {terminal && (
            <button type="button" className="ghost" onClick={onClose}>
              Dismiss
            </button>
          )}
        </div>
      </div>
      {job.error && (
        <pre style={{ marginTop: 8, fontSize: 12, whiteSpace: "pre-wrap" }}>
          {job.error}
        </pre>
      )}
    </div>
  );
}

function JobsTab({ fleetId }: { fleetId: string }) {
  const fetcher = useCallback(() => listACMEJobs(fleetId), [fleetId]);
  const { data, error, loading, refetch } = useFetch(fetcher);

  // Refetch every 3 s while any job is in a non-terminal state.
  const hasInFlight = data
    ? data.some((j) => j.status === "pending" || j.status === "running")
    : false;
  useEffect(() => {
    if (!hasInFlight) return;
    const t = window.setInterval(() => refetch(), 3000);
    return () => window.clearInterval(t);
  }, [hasInFlight, refetch]);

  return (
    <>
      <div className="toolbar">
        <h3 style={{ margin: 0 }}>ACME jobs</h3>
        <div className="spacer" />
      </div>
      <p className="muted" style={{ marginTop: -8 }}>
        Async issuance and renewal jobs run by the manager. The list refreshes
        automatically while anything is in flight.
      </p>
      {loading && <div className="muted">Loading…</div>}
      {error && <ErrorBox>{error}</ErrorBox>}
      {data && data.length === 0 && (
        <Empty>
          No jobs yet. Request an ACME cert from the Certificates tab to enqueue
          one.
        </Empty>
      )}
      {data && data.length > 0 && (
        <div className="card" style={{ padding: 0 }}>
          <table>
            <thead>
              <tr>
                <th>ID</th>
                <th>Cert name</th>
                <th>DNS names</th>
                <th>Provider</th>
                <th>Status</th>
                <th>Started</th>
                <th>Finished</th>
              </tr>
            </thead>
            <tbody>
              {data.map((j) => (
                <tr key={j.id}>
                  <td className="mono">#{j.id}</td>
                  <td>{j.name}</td>
                  <td className="mono">{j.dns_names.join(", ")}</td>
                  <td className="mono">{j.dns_provider}</td>
                  <td>
                    {jobStatusTag(j.status)}
                    {j.error && (
                      <span
                        className="muted"
                        style={{ fontSize: 11, marginLeft: 6 }}
                        title={j.error}
                      >
                        {j.error.slice(0, 60)}
                        {j.error.length > 60 ? "…" : ""}
                      </span>
                    )}
                  </td>
                  <td className="muted">{fmtTime(j.started_at ?? null)}</td>
                  <td className="muted">{fmtTime(j.finished_at ?? null)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </>
  );
}
