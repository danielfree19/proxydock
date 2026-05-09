import { NavLink, Outlet, useNavigate } from "react-router-dom";
import { clearAdminToken } from "./auth";

export function Layout() {
  const navigate = useNavigate();
  function onSignOut() {
    clearAdminToken();
    navigate("/login", { replace: true });
  }
  return (
    <div className="layout">
      <aside className="sidebar">
        <h1>Traefik Fleet Manager</h1>
        <nav>
          <NavLink to="/" end>
            Fleets
          </NavLink>
          <NavLink to="/admin/audit">Audit log</NavLink>
          <NavLink to="/admin/tokens">Admin tokens</NavLink>
        </nav>
        <div style={{ marginTop: 24 }}>
          <button className="ghost" onClick={onSignOut} style={{ width: "100%" }}>
            Sign out
          </button>
        </div>
        <p className="muted" style={{ fontSize: 11, marginTop: 24 }}>
          Phase 5: admin auth, encrypted secrets at rest, signed
          revisions. Manager API at <span className="mono">/api/v1</span>.
        </p>
      </aside>
      <main className="main">
        <Outlet />
      </main>
    </div>
  );
}
