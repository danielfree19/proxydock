import { useCallback, useState } from "react";
import { listAuditEntries } from "../api";
import { useFetch } from "../components/useFetch";
import { Empty, ErrorBox, fmtTime } from "../components/Bits";

// Audit page — append-only log of admin actions. Reads (GET) are not
// recorded by the backend, so this page is a faithful view of every
// state-changing call any admin made.
export function AuditPage() {
  const [filter, setFilter] = useState<string>("all");
  const fetcher = useCallback(
    () =>
      listAuditEntries(
        filter === "all"
          ? {}
          : filter === "global"
          ? { fleetId: "global" }
          : { fleetId: filter },
      ),
    [filter],
  );
  const { data, error, loading, refetch } = useFetch(fetcher);

  return (
    <>
      <div className="toolbar">
        <h2 style={{ margin: 0 }}>Audit log</h2>
        <div className="spacer" />
        <select value={filter} onChange={(e) => setFilter(e.target.value)}>
          <option value="all">All entries</option>
          <option value="global">Global only (no fleet)</option>
          {/* Per-fleet entries: keyed by what the operator types. The
              dropdown stays minimal in Phase 5b; a smarter version
              would prefill options from a fleet list. */}
        </select>
        <input
          type="text"
          placeholder="Filter by fleet id"
          value={filter !== "all" && filter !== "global" ? filter : ""}
          onChange={(e) => setFilter(e.target.value || "all")}
          style={{ maxWidth: 200 }}
        />
        <button className="ghost" onClick={refetch}>
          Refresh
        </button>
      </div>
      <p className="muted" style={{ marginTop: -8 }}>
        Every authenticated admin mutation. Reads are intentionally not
        recorded — they would dominate the table.
      </p>

      {loading && <div className="muted">Loading…</div>}
      {error && <ErrorBox>{error}</ErrorBox>}
      {data && data.length === 0 && (
        <Empty>No entries yet. Mutate something — create a fleet, edit a proxy host — and reload.</Empty>
      )}
      {data && data.length > 0 && (
        <div className="card" style={{ padding: 0 }}>
          <table>
            <thead>
              <tr>
                <th>Time</th>
                <th>Actor</th>
                <th>Method</th>
                <th>Path</th>
                <th>Status</th>
                <th>Fleet</th>
              </tr>
            </thead>
            <tbody>
              {data.map((e) => (
                <tr key={e.id}>
                  <td className="muted" style={{ whiteSpace: "nowrap" }}>
                    {fmtTime(e.created_at)}
                  </td>
                  <td className="mono">{e.actor}</td>
                  <td className="mono">{e.method}</td>
                  <td className="mono" style={{ wordBreak: "break-all" }}>{e.path}</td>
                  <td>
                    <span className={statusClass(e.status)}>{e.status}</span>
                  </td>
                  <td className="mono">
                    {e.fleet_id ? e.fleet_id : <span className="muted">—</span>}
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

function statusClass(status: number): string {
  if (status >= 500) return "tag danger";
  if (status >= 400) return "tag warn";
  if (status >= 200 && status < 400) return "tag success";
  return "tag muted";
}
