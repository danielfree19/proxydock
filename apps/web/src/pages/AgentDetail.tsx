import { useCallback, useEffect, useState } from "react";
import { Link, useParams } from "react-router-dom";
import {
  getAgent,
  getAgentConfigPreview,
  getFleet,
  listTokens,
  mintToken,
  revokeToken,
  updateAgentLabels,
} from "../api";
import { useFetch } from "../components/useFetch";
import { Crumbs, Empty, ErrorBox, fmtAgo, fmtTime } from "../components/Bits";
import { RevisionPair } from "./FleetDetail";
import type { TraefikSection } from "../types";

export function AgentDetailPage() {
  const { agentId = "" } = useParams<{ agentId: string }>();
  const agentFetch = useCallback(() => getAgent(agentId), [agentId]);
  const tokensFetch = useCallback(() => listTokens(agentId), [agentId]);
  const a = useFetch(agentFetch);
  const t = useFetch(tokensFetch);
  // Pull the parent fleet so we can show "expected vs current" rev. Fetch
  // gates on the agent landing — `useFetch` resolves the null promise
  // immediately when fleet_id is unknown so we never call getFleet("").
  const fleetFetch = useCallback(
    () =>
      a.data?.fleet_id
        ? getFleet(a.data.fleet_id)
        : Promise.resolve(null),
    [a.data?.fleet_id],
  );
  const fleetQ = useFetch(fleetFetch);

  const [name, setName] = useState("");
  const [busy, setBusy] = useState(false);
  const [opErr, setOpErr] = useState<string | undefined>();
  const [issued, setIssued] = useState<string | undefined>();

  async function onMint() {
    setBusy(true);
    setOpErr(undefined);
    try {
      const r = await mintToken(agentId, name || "manual");
      setIssued(r.token);
      setName("");
      t.refetch();
    } catch (e) {
      setOpErr(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  }

  async function onRevoke(prefix: string) {
    if (!confirm(`Revoke token "${prefix}…"? Active agents using it will start failing fetches.`))
      return;
    try {
      await revokeToken(agentId, prefix);
      t.refetch();
    } catch (e) {
      alert(e instanceof Error ? e.message : String(e));
    }
  }

  return (
    <>
      <Crumbs
        items={[
          { label: "Fleets", to: "/" },
          ...(a.data ? [{ label: a.data.fleet_id, to: `/fleets/${encodeURIComponent(a.data.fleet_id)}` }] : []),
          { label: agentId },
        ]}
      />

      <h2>
        {a.data?.name || agentId}{" "}
        <span className="mono muted" style={{ fontSize: 12 }}>{agentId}</span>
      </h2>
      {a.error && <ErrorBox>{a.error}</ErrorBox>}
      {a.data && (
        <div className="card">
          <div className="row">
            <div>
              <label>Fleet</label>
              <div>
                <Link
                  to={`/fleets/${encodeURIComponent(a.data.fleet_id)}`}
                  className="mono"
                >
                  {a.data.fleet_id}
                </Link>
              </div>
            </div>
            <div>
              <label>Last heartbeat</label>
              <div title={fmtTime(a.data.last_heartbeat_at)}>
                {fmtAgo(a.data.last_heartbeat_at)}
              </div>
            </div>
            <div>
              <label>Revision (current → expected)</label>
              <div>
                <RevisionPair
                  current={a.data.last_revision_seen ?? null}
                  expected={fleetQ.data?.published_revision_id ?? null}
                />
              </div>
            </div>
            <div>
              <label>Provider / Traefik</label>
              <div className="mono muted">
                {a.data.last_provider_version || "—"} /{" "}
                {a.data.last_traefik_version || "—"}
              </div>
            </div>
          </div>
          {a.data.last_error && (
            <div className="error" style={{ marginTop: 12 }}>
              Last error: {a.data.last_error}
            </div>
          )}
          <LabelsEditor
            agentId={agentId}
            initial={a.data.labels}
            onSaved={a.refetch}
          />
        </div>
      )}

      <ConfigPanel agentId={agentId} />

      <h3>Tokens</h3>
      <div className="card">
        {issued && (
          <div className="token-banner">
            <strong>New token issued — copy it now, it will not be shown again:</strong>
            <br />
            {issued}
          </div>
        )}
        <div className="row">
          <input
            type="text"
            placeholder="Token name (optional)"
            value={name}
            onChange={(e) => setName(e.target.value)}
          />
          <div className="shrink">
            <button onClick={onMint} disabled={busy}>
              Mint token
            </button>
          </div>
        </div>
        {opErr && (
          <div className="error" style={{ marginTop: 12 }}>
            {opErr}
          </div>
        )}
      </div>

      {t.loading && <div className="muted">Loading…</div>}
      {t.error && <ErrorBox>{t.error}</ErrorBox>}
      {t.data && t.data.length === 0 && (
        <Empty>No tokens yet. Mint one to allow this agent to authenticate.</Empty>
      )}
      {t.data && t.data.length > 0 && (
        <div className="card" style={{ padding: 0 }}>
          <table>
            <thead>
              <tr>
                <th>Prefix</th>
                <th>Name</th>
                <th>Created</th>
                <th>Last used</th>
                <th>Status</th>
                <th />
              </tr>
            </thead>
            <tbody>
              {t.data.map((tok) => (
                <tr key={tok.prefix}>
                  <td className="mono">{tok.prefix}</td>
                  <td>{tok.name || <span className="muted">—</span>}</td>
                  <td className="muted">{fmtTime(tok.created_at)}</td>
                  <td className="muted">{fmtAgo(tok.last_used_at)}</td>
                  <td>
                    {tok.revoked_at ? (
                      <span className="tag danger">revoked</span>
                    ) : (
                      <span className="tag success">active</span>
                    )}
                  </td>
                  <td style={{ textAlign: "right" }}>
                    {!tok.revoked_at && (
                      <button className="ghost" onClick={() => onRevoke(tok.prefix)}>
                        Revoke
                      </button>
                    )}
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

// ConfigPanel mirrors the bytes the agent receives via /config —
// post-label-filter routers, services, and middlewares broken out per
// protocol so operators can see what each agent actually serves
// without having to hold the agent's bearer token.
function ConfigPanel({ agentId }: { agentId: string }) {
  const fetcher = useCallback(() => getAgentConfigPreview(agentId), [agentId]);
  const cfg = useFetch(fetcher);
  const [showRaw, setShowRaw] = useState(false);

  return (
    <>
      <h3>
        Config served{" "}
        <button
          type="button"
          className="ghost"
          style={{ float: "right", fontSize: 12 }}
          onClick={cfg.refetch}
        >
          Refresh
        </button>
      </h3>
      {cfg.loading && <div className="muted">Loading…</div>}
      {cfg.error && <ErrorBox>{cfg.error}</ErrorBox>}
      {cfg.data && (
        <div className="card">
          <div className="row">
            <div>
              <label>Revision</label>
              <div>
                <span className="tag">#{cfg.data.revision}</span>
              </div>
            </div>
            <div>
              <label>ETag</label>
              <div className="mono muted" style={{ fontSize: 12 }}>
                {cfg.data.etag}
              </div>
            </div>
            <div>
              <label>Generated</label>
              <div title={fmtTime(cfg.data.generated_at)}>
                {fmtAgo(cfg.data.generated_at)}
              </div>
            </div>
            <div>
              <label>Signature</label>
              <div className="muted" style={{ fontSize: 12 }}>
                {cfg.data.signature
                  ? cfg.data.signature_alg || "present"
                  : "(none)"}
              </div>
            </div>
          </div>
          <ProtocolBlock title="HTTP" section={cfg.data.config.http} />
          <ProtocolBlock title="TCP" section={cfg.data.config.tcp} />
          <ProtocolBlock title="UDP" section={cfg.data.config.udp} />
          {cfg.data.config.tls?.certificates &&
            cfg.data.config.tls.certificates.length > 0 && (
              <p className="muted" style={{ marginTop: 12, fontSize: 12 }}>
                {cfg.data.config.tls.certificates.length} certificate(s) in the
                TLS pool.
              </p>
            )}
          <div style={{ marginTop: 12 }}>
            <button
              type="button"
              className="ghost"
              onClick={() => setShowRaw((s) => !s)}
            >
              {showRaw ? "Hide raw JSON" : "Show raw JSON"}
            </button>
            {showRaw && (
              <pre
                className="mono"
                style={{
                  marginTop: 8,
                  fontSize: 11,
                  maxHeight: 360,
                  overflow: "auto",
                  background: "var(--surface-alt, #0e1116)",
                  padding: 12,
                  borderRadius: 6,
                }}
              >
                {JSON.stringify(cfg.data.config, null, 2)}
              </pre>
            )}
          </div>
        </div>
      )}
    </>
  );
}

function ProtocolBlock({
  title,
  section,
}: {
  title: string;
  section?: TraefikSection;
}) {
  if (!section) return null;
  const routers = section.routers || {};
  const services = section.services || {};
  const middlewares = section.middlewares || {};
  const routerNames = Object.keys(routers).sort();
  const serviceNames = Object.keys(services).sort();
  const mwNames = Object.keys(middlewares).sort();
  if (
    routerNames.length === 0 &&
    serviceNames.length === 0 &&
    mwNames.length === 0
  ) {
    return null;
  }
  return (
    <div style={{ marginTop: 16 }}>
      <h4 style={{ marginBottom: 4 }}>{title}</h4>
      {routerNames.length > 0 && (
        <table>
          <thead>
            <tr>
              <th>Router</th>
              <th>Entry points</th>
              <th>Rule</th>
              <th>Service</th>
              <th>Middlewares</th>
              <th>TLS</th>
            </tr>
          </thead>
          <tbody>
            {routerNames.map((name) => {
              const r = routers[name];
              return (
                <tr key={name}>
                  <td className="mono">{name}</td>
                  <td className="mono">
                    {(r.entryPoints || []).join(", ") || (
                      <span className="muted">—</span>
                    )}
                  </td>
                  <td className="mono" style={{ fontSize: 12 }}>
                    {r.rule || <span className="muted">(entry-point only)</span>}
                  </td>
                  <td className="mono">{r.service || ""}</td>
                  <td className="mono" style={{ fontSize: 12 }}>
                    {(r.middlewares || []).join(", ") || (
                      <span className="muted">—</span>
                    )}
                  </td>
                  <td>
                    {r.tls !== undefined ? (
                      <span className="tag success">on</span>
                    ) : (
                      <span className="muted">—</span>
                    )}
                  </td>
                </tr>
              );
            })}
          </tbody>
        </table>
      )}
      {serviceNames.length > 0 && (
        <details style={{ marginTop: 8 }}>
          <summary className="muted" style={{ cursor: "pointer" }}>
            Services ({serviceNames.length})
          </summary>
          <table style={{ marginTop: 4 }}>
            <thead>
              <tr>
                <th>Name</th>
                <th>Servers</th>
              </tr>
            </thead>
            <tbody>
              {serviceNames.map((name) => {
                const servers = services[name].loadBalancer?.servers || [];
                return (
                  <tr key={name}>
                    <td className="mono">{name}</td>
                    <td className="mono" style={{ fontSize: 12 }}>
                      {servers
                        .map((s) => s.url || s.address || "")
                        .filter(Boolean)
                        .join(", ")}
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </details>
      )}
      {mwNames.length > 0 && (
        <details style={{ marginTop: 8 }}>
          <summary className="muted" style={{ cursor: "pointer" }}>
            Middlewares ({mwNames.length})
          </summary>
          <table style={{ marginTop: 4 }}>
            <thead>
              <tr>
                <th>Name</th>
                <th>Type</th>
              </tr>
            </thead>
            <tbody>
              {mwNames.map((name) => (
                <tr key={name}>
                  <td className="mono">{name}</td>
                  <td className="mono" style={{ fontSize: 12 }}>
                    {Object.keys(middlewares[name]).join(", ")}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </details>
      )}
    </div>
  );
}

function LabelsEditor({
  agentId,
  initial,
  onSaved,
}: {
  agentId: string;
  initial: string[];
  onSaved: () => void;
}) {
  // Mirror the joined "key=value, key=value" form so the input is
  // copy-pasteable from a Kubernetes-style mental model.
  const [text, setText] = useState(initial.join(", "));
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | undefined>();
  const [savedAt, setSavedAt] = useState<number | undefined>();

  // Sync the textarea if the agent re-fetches (e.g. another tab edited).
  useEffect(() => {
    setText(initial.join(", "));
  }, [initial]);

  async function onSave() {
    setBusy(true);
    setErr(undefined);
    const labels = text
      .split(",")
      .map((s) => s.trim())
      .filter(Boolean);
    try {
      await updateAgentLabels(agentId, labels);
      setSavedAt(Date.now());
      onSaved();
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  }

  return (
    <div style={{ marginTop: 12 }}>
      <label>Labels</label>
      <p className="muted" style={{ fontSize: 12, marginTop: 0 }}>
        Comma-separated <span className="mono">key=value</span> pairs. Used by
        proxy hosts' <em>label selector</em> to target a subset of agents.
      </p>
      <div className="row">
        <input
          type="text"
          value={text}
          onChange={(e) => setText(e.target.value)}
          placeholder="region=us-east, tier=prod"
        />
        <div className="shrink">
          <button onClick={onSave} disabled={busy}>
            {busy ? "Saving…" : "Save labels"}
          </button>
        </div>
      </div>
      {savedAt && !err && (
        <div className="notice" style={{ marginTop: 8 }}>
          Saved. Agents pick up the new label set on their next /config poll.
        </div>
      )}
      {err && (
        <div className="error" style={{ marginTop: 8 }}>
          {err}
        </div>
      )}
    </div>
  );
}
