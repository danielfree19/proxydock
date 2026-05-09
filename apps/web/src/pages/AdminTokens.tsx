import { useCallback, useState } from "react";
import {
  listAdminTokens,
  mintAdminToken as mintAdminTokenAPI,
  revokeAdminToken as revokeAdminTokenAPI,
} from "../api";
import { useFetch } from "../components/useFetch";
import { Empty, ErrorBox, fmtAgo, fmtTime } from "../components/Bits";

export function AdminTokensPage() {
  const fetcher = useCallback(() => listAdminTokens(), []);
  const { data, error, loading, refetch } = useFetch(fetcher);
  const [name, setName] = useState("");
  const [issued, setIssued] = useState<string | undefined>();
  const [busy, setBusy] = useState(false);
  const [opErr, setOpErr] = useState<string | undefined>();

  async function onMint() {
    setBusy(true);
    setOpErr(undefined);
    try {
      const r = await mintAdminTokenAPI(name || "manual");
      setIssued(r.token);
      setName("");
      refetch();
    } catch (e) {
      setOpErr(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  }

  async function onRevoke(prefix: string) {
    if (!confirm("Revoke admin token \"" + prefix + "…\"?")) return;
    try {
      await revokeAdminTokenAPI(prefix);
      refetch();
    } catch (e) {
      alert(e instanceof Error ? e.message : String(e));
    }
  }

  return (
    <>
      <h2>Admin tokens</h2>
      <p className="muted" style={{ marginTop: -8 }}>
        Tokens that authorize the admin API. The bootstrap token from
        <span className="mono"> MANAGER_API_BOOTSTRAP_ADMIN_TOKEN</span> is
        not listed here — it lives in the operator's environment, not the
        database.
      </p>

      <div className="card">
        {issued && (
          <div className="token-banner">
            <strong>New admin token — copy now, it won't be shown again:</strong>
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
              Mint admin token
            </button>
          </div>
        </div>
        {opErr && <div className="error" style={{ marginTop: 12 }}>{opErr}</div>}
      </div>

      {loading && <div className="muted">Loading…</div>}
      {error && <ErrorBox>{error}</ErrorBox>}
      {data && data.length === 0 && (
        <Empty>
          No admin tokens yet — mint one before unsetting the bootstrap token.
        </Empty>
      )}
      {data && data.length > 0 && (
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
              {data.map((t) => (
                <tr key={t.prefix}>
                  <td className="mono">{t.prefix}</td>
                  <td>{t.name || <span className="muted">—</span>}</td>
                  <td className="muted">{fmtTime(t.created_at)}</td>
                  <td className="muted">{fmtAgo(t.last_used_at)}</td>
                  <td>
                    {t.revoked_at ? (
                      <span className="tag danger">revoked</span>
                    ) : (
                      <span className="tag success">active</span>
                    )}
                  </td>
                  <td style={{ textAlign: "right" }}>
                    {!t.revoked_at && (
                      <button className="ghost" onClick={() => onRevoke(t.prefix)}>
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
