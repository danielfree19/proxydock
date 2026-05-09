import { useCallback, useState } from "react";
import { Link } from "react-router-dom";
import { createFleet, deleteFleet, listFleets } from "../api";
import { useFetch } from "../components/useFetch";
import { Empty, ErrorBox, fmtTime } from "../components/Bits";

export function FleetsPage() {
  const fetcher = useCallback(() => listFleets(), []);
  const { data, error, loading, refetch } = useFetch(fetcher);
  const [showNew, setShowNew] = useState(false);
  const [newId, setNewId] = useState("");
  const [newName, setNewName] = useState("");
  const [busy, setBusy] = useState(false);
  const [submitErr, setSubmitErr] = useState<string | undefined>();

  async function onCreate(e: React.FormEvent) {
    e.preventDefault();
    if (!newId || !newName) return;
    setBusy(true);
    setSubmitErr(undefined);
    try {
      await createFleet(newId, newName);
      setNewId("");
      setNewName("");
      setShowNew(false);
      refetch();
    } catch (e) {
      setSubmitErr(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  }

  async function onDelete(id: string) {
    if (!confirm(`Delete fleet "${id}"? This also removes its agents and revisions.`))
      return;
    try {
      await deleteFleet(id);
      refetch();
    } catch (e) {
      alert(e instanceof Error ? e.message : String(e));
    }
  }

  return (
    <>
      <div className="toolbar">
        <h2>Fleets</h2>
        <div className="spacer" />
        <button onClick={() => setShowNew((s) => !s)}>
          {showNew ? "Cancel" : "New fleet"}
        </button>
      </div>

      {showNew && (
        <form className="card" onSubmit={onCreate}>
          <div className="row">
            <div>
              <label>ID</label>
              <input
                type="text"
                value={newId}
                onChange={(e) => setNewId(e.target.value)}
                placeholder="homelab"
                required
              />
            </div>
            <div>
              <label>Name</label>
              <input
                type="text"
                value={newName}
                onChange={(e) => setNewName(e.target.value)}
                placeholder="Homelab"
                required
              />
            </div>
            <div className="shrink" style={{ alignSelf: "flex-end" }}>
              <button disabled={busy} type="submit">
                Create
              </button>
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
        <Empty>No fleets yet. Click <em>New fleet</em> to create one.</Empty>
      )}
      {data && data.length > 0 && (
        <div className="card" style={{ padding: 0 }}>
          <table>
            <thead>
              <tr>
                <th>ID</th>
                <th>Name</th>
                <th>Published rev</th>
                <th>Created</th>
                <th />
              </tr>
            </thead>
            <tbody>
              {data.map((f) => (
                <tr key={f.id}>
                  <td>
                    <Link to={`/fleets/${encodeURIComponent(f.id)}`} className="mono">
                      {f.id}
                    </Link>
                  </td>
                  <td>{f.name}</td>
                  <td>
                    {f.published_revision_id ? (
                      <span className="tag">#{f.published_revision_id}</span>
                    ) : (
                      <span className="tag muted">none</span>
                    )}
                  </td>
                  <td className="muted">{fmtTime(f.created_at)}</td>
                  <td style={{ textAlign: "right" }}>
                    <button className="ghost" onClick={() => onDelete(f.id)}>
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
