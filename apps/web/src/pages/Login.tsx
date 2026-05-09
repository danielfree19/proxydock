import { useState } from "react";
import { useNavigate } from "react-router-dom";
import { verifyAdminToken } from "../api";
import { setAdminToken } from "../components/auth";

export function LoginPage() {
  const [token, setToken] = useState("");
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | undefined>();
  const navigate = useNavigate();

  async function onSubmit(e: React.FormEvent) {
    e.preventDefault();
    setBusy(true);
    setErr(undefined);
    try {
      await verifyAdminToken(token.trim());
      setAdminToken(token.trim());
      navigate("/");
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  }

  return (
    <div
      style={{
        display: "flex",
        alignItems: "center",
        justifyContent: "center",
        minHeight: "100vh",
        padding: 24,
      }}
    >
      <form
        className="card"
        onSubmit={onSubmit}
        style={{ width: 420, maxWidth: "100%" }}
      >
        <h2 style={{ marginTop: 0 }}>Traefik Fleet Manager</h2>
        <p className="muted" style={{ marginTop: 0 }}>
          Sign in with an admin token. The bootstrap token is set by the
          operator via the <span className="mono">MANAGER_API_BOOTSTRAP_ADMIN_TOKEN</span>{" "}
          env var; mint a real one from the Admin tokens page once you're in.
        </p>
        <div>
          <label>Admin token</label>
          <input
            type="password"
            value={token}
            onChange={(e) => setToken(e.target.value)}
            placeholder="tfm_..."
            required
            autoFocus
          />
        </div>
        {err && (
          <div className="error" style={{ marginTop: 12 }}>
            {err}
          </div>
        )}
        <div className="toolbar" style={{ marginTop: 12 }}>
          <div className="spacer" />
          <button type="submit" disabled={busy}>
            {busy ? "Signing in…" : "Sign in"}
          </button>
        </div>
      </form>
    </div>
  );
}
