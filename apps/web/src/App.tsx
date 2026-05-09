import { Navigate, Route, Routes, useLocation } from "react-router-dom";
import { Layout } from "./components/Layout";
import { getAdminToken } from "./components/auth";
import { FleetsPage } from "./pages/Fleets";
import { FleetDetailPage } from "./pages/FleetDetail";
import { AgentDetailPage } from "./pages/AgentDetail";
import { ProxyHostFormPage } from "./pages/ProxyHostForm";
import { MiddlewareTemplateFormPage } from "./pages/MiddlewareTemplateForm";
import { RevisionDetailPage } from "./pages/RevisionDetail";
import { RevisionDiffPage } from "./pages/RevisionDiff";
import { AdminTokensPage } from "./pages/AdminTokens";
import { AuditPage } from "./pages/Audit";
import { LoginPage } from "./pages/Login";

export default function App() {
  return (
    <Routes>
      <Route path="/login" element={<LoginPage />} />
      <Route element={<Protected><Layout /></Protected>}>
        <Route path="/" element={<FleetsPage />} />
        <Route path="/fleets/:fleetId" element={<FleetDetailPage />} />
        <Route
          path="/fleets/:fleetId/proxy-hosts/new"
          element={<ProxyHostFormPage />}
        />
        <Route
          path="/fleets/:fleetId/proxy-hosts/:phId"
          element={<ProxyHostFormPage />}
        />
        <Route
          path="/fleets/:fleetId/middleware-templates/new"
          element={<MiddlewareTemplateFormPage />}
        />
        <Route
          path="/fleets/:fleetId/middleware-templates/:tplId"
          element={<MiddlewareTemplateFormPage />}
        />
        <Route
          path="/fleets/:fleetId/revisions/:number"
          element={<RevisionDetailPage />}
        />
        <Route
          path="/fleets/:fleetId/revisions/:from/diff/:to"
          element={<RevisionDiffPage />}
        />
        <Route path="/agents/:agentId" element={<AgentDetailPage />} />
        <Route path="/admin/tokens" element={<AdminTokensPage />} />
        <Route path="/admin/audit" element={<AuditPage />} />
        <Route path="*" element={<NotFound />} />
      </Route>
    </Routes>
  );
}

// Protected redirects to /login when no admin token is set in
// sessionStorage. Re-rendering after sign-in is handled by the route
// state change in LoginPage's navigate("/").
function Protected({ children }: { children: React.ReactNode }) {
  const loc = useLocation();
  if (!getAdminToken()) {
    return <Navigate to="/login" replace state={{ from: loc.pathname }} />;
  }
  return <>{children}</>;
}

function NotFound() {
  return (
    <div className="empty">
      Not found. <a href="/">Back to fleets.</a>
    </div>
  );
}
