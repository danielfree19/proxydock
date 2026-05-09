import { useCallback } from "react";
import { Link, useParams } from "react-router-dom";
import { getRevision } from "../api";
import { Crumbs, ErrorBox, fmtTime } from "../components/Bits";
import { useFetch } from "../components/useFetch";
import {
  diffCertificates,
  diffProxyHosts,
  diffSummary,
  formatValue,
  parseSourceArray,
  type CertificateDiff,
  type ProxyHostDiff,
} from "../components/diff";
import type { Certificate, ProxyHost } from "../types";

export function RevisionDiffPage() {
  const { fleetId = "", from = "", to = "" } = useParams<{
    fleetId: string;
    from: string;
    to: string;
  }>();
  const fromN = Number(from);
  const toN = Number(to);

  const fromFetch = useCallback(() => getRevision(fleetId, fromN), [fleetId, fromN]);
  const toFetch = useCallback(() => getRevision(fleetId, toN), [fleetId, toN]);
  const a = useFetch(fromFetch);
  const b = useFetch(toFetch);

  const loading = a.loading || b.loading;
  const error = a.error || b.error;

  return (
    <>
      <Crumbs
        items={[
          { label: "Fleets", to: "/" },
          { label: fleetId, to: `/fleets/${encodeURIComponent(fleetId)}` },
          { label: `Diff #${from} → #${to}` },
        ]}
      />
      <h2>
        Compare revision <span className="mono">#{from}</span> →{" "}
        <span className="mono">#{to}</span>
      </h2>

      {loading && <div className="muted">Loading…</div>}
      {error && <ErrorBox>{error}</ErrorBox>}
      {a.data && b.data && (
        <DiffBody fleetId={fleetId} fromRev={a.data} toRev={b.data} />
      )}
    </>
  );
}

function DiffBody({
  fleetId,
  fromRev,
  toRev,
}: {
  fleetId: string;
  fromRev: { number: number; generated_at: string; source_proxy_hosts?: unknown; source_certs?: unknown };
  toRev: { number: number; generated_at: string; source_proxy_hosts?: unknown; source_certs?: unknown };
}) {
  const beforeHosts = parseSourceArray<ProxyHost>(fromRev.source_proxy_hosts);
  const afterHosts = parseSourceArray<ProxyHost>(toRev.source_proxy_hosts);
  const beforeCerts = parseSourceArray<Certificate>(fromRev.source_certs);
  const afterCerts = parseSourceArray<Certificate>(toRev.source_certs);

  const hostDiffs = diffProxyHosts(beforeHosts, afterHosts);
  const certDiffs = diffCertificates(beforeCerts, afterCerts);
  const hostSummary = diffSummary(hostDiffs);
  const certSummary = diffSummary(certDiffs);

  const noChange = hostSummary.added === 0 && hostSummary.removed === 0 && hostSummary.modified === 0
    && certSummary.added === 0 && certSummary.removed === 0 && certSummary.modified === 0;

  return (
    <>
      <div className="card">
        <div className="row">
          <div>
            <label>Before</label>
            <div>
              <Link
                to={`/fleets/${encodeURIComponent(fleetId)}/revisions/${fromRev.number}`}
                className="mono"
              >
                #{fromRev.number}
              </Link>{" "}
              <span className="muted">{fmtTime(fromRev.generated_at)}</span>
            </div>
          </div>
          <div>
            <label>After</label>
            <div>
              <Link
                to={`/fleets/${encodeURIComponent(fleetId)}/revisions/${toRev.number}`}
                className="mono"
              >
                #{toRev.number}
              </Link>{" "}
              <span className="muted">{fmtTime(toRev.generated_at)}</span>
            </div>
          </div>
          <div>
            <label>Proxy hosts</label>
            <div>
              <SummaryTags s={hostSummary} />
            </div>
          </div>
          <div>
            <label>Certificates</label>
            <div>
              <SummaryTags s={certSummary} />
            </div>
          </div>
        </div>
      </div>

      {noChange && (
        <div className="notice">
          No changes between <span className="mono">#{fromRev.number}</span> and{" "}
          <span className="mono">#{toRev.number}</span>. (The compiled config or
          its signature may still differ if the manager re-signed or
          re-encoded.)
        </div>
      )}

      <h3>Proxy hosts</h3>
      <ProxyHostDiffList items={hostDiffs} />

      <h3>Certificates</h3>
      <CertificateDiffList items={certDiffs} />
    </>
  );
}

function SummaryTags({
  s,
}: {
  s: { added: number; removed: number; modified: number; unchanged: number };
}) {
  return (
    <span style={{ display: "inline-flex", gap: 4 }}>
      <span className="tag success">+{s.added}</span>
      <span className="tag danger">−{s.removed}</span>
      <span className="tag warn">~{s.modified}</span>
      <span className="tag muted">·{s.unchanged}</span>
    </span>
  );
}

function statusTag(s: ProxyHostDiff["status"]) {
  switch (s) {
    case "added":
      return <span className="tag success">added</span>;
    case "removed":
      return <span className="tag danger">removed</span>;
    case "modified":
      return <span className="tag warn">modified</span>;
    default:
      return <span className="tag muted">unchanged</span>;
  }
}

function ProxyHostDiffList({ items }: { items: ProxyHostDiff[] }) {
  // Hide unchanged rows by default — operators are usually scanning
  // for what *changed*, and the unchanged count is in the summary.
  const visible = items.filter((i) => i.status !== "unchanged");
  if (visible.length === 0) {
    return <p className="muted">No proxy host changes.</p>;
  }
  return (
    <div className="card" style={{ padding: 0 }}>
      <table>
        <thead>
          <tr>
            <th>Name</th>
            <th>Status</th>
            <th>Domain</th>
            <th>Upstream</th>
            <th>Changes</th>
          </tr>
        </thead>
        <tbody>
          {visible.map((d) => (
            <tr key={d.name}>
              <td className="mono">{d.name}</td>
              <td>{statusTag(d.status)}</td>
              <td className="mono">
                {d.after?.domain ?? d.before?.domain ?? "—"}
              </td>
              <td className="mono muted">
                {d.after?.upstream_url ?? d.before?.upstream_url ?? "—"}
              </td>
              <td>
                {d.status === "modified" && d.changes ? (
                  <FieldChanges changes={d.changes} />
                ) : d.status === "added" ? (
                  <span className="muted">—</span>
                ) : d.status === "removed" ? (
                  <span className="muted">—</span>
                ) : null}
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

function CertificateDiffList({ items }: { items: CertificateDiff[] }) {
  const visible = items.filter((i) => i.status !== "unchanged");
  if (visible.length === 0) {
    return <p className="muted">No certificate changes.</p>;
  }
  return (
    <div className="card" style={{ padding: 0 }}>
      <table>
        <thead>
          <tr>
            <th>Name</th>
            <th>Status</th>
            <th>Subject / DNS names</th>
            <th>Changes</th>
          </tr>
        </thead>
        <tbody>
          {visible.map((d) => (
            <tr key={d.name}>
              <td className="mono">{d.name}</td>
              <td>{statusTag(d.status)}</td>
              <td className="mono muted">
                {(d.after?.subject ?? d.before?.subject) || "—"}
                <br />
                <span style={{ fontSize: 11 }}>
                  {(d.after?.dns_names ?? d.before?.dns_names ?? []).join(", ")}
                </span>
              </td>
              <td>
                {d.status === "modified" && d.changes ? (
                  <FieldChanges changes={d.changes} />
                ) : (
                  <span className="muted">—</span>
                )}
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

function FieldChanges({
  changes,
}: {
  changes: { field: string; before: unknown; after: unknown }[];
}) {
  return (
    <ul style={{ margin: 0, paddingLeft: 16, fontSize: 12 }}>
      {changes.map((c) => (
        <li key={c.field}>
          <span className="mono">{c.field}</span>:{" "}
          <span className="mono" style={{ color: "var(--danger)" }}>
            {formatValue(c.before)}
          </span>{" "}
          →{" "}
          <span className="mono" style={{ color: "var(--success)" }}>
            {formatValue(c.after)}
          </span>
        </li>
      ))}
    </ul>
  );
}
