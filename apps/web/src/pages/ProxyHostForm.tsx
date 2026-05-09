import { useCallback, useEffect, useState } from "react";
import { useNavigate, useParams } from "react-router-dom";
import {
  createProxyHost,
  getProxyHost,
  updateProxyHost,
} from "../api";
import type {
  HealthCheck,
  Middleware,
  ProxyHostInput,
  ProxyHostProtocol,
} from "../types";
import { Crumbs, ErrorBox } from "../components/Bits";
import { useFetch } from "../components/useFetch";
import {
  MiddlewareEditor,
  middlewareSkeleton,
} from "../components/MiddlewareEditor";
import { discoverServices, listMiddlewareTemplates } from "../api";
import type { DiscoveredService, ProxyHostProtocol as _Proto } from "../types";

// DiscoverInline lives next to the Upstream URL label. Hidden when
// the manager doesn't expose discovery (503). Clicking opens a
// dropdown listing running containers; picking one fills the URL.
function DiscoverInline({
  protocol,
  onPick,
}: {
  protocol: _Proto;
  onPick: (url: string) => void;
}) {
  const [open, setOpen] = useState(false);
  const [services, setServices] = useState<DiscoveredService[] | null>(null);
  const [err, setErr] = useState<string | undefined>();
  const [loading, setLoading] = useState(false);
  const [available, setAvailable] = useState(true);

  async function load() {
    setLoading(true);
    setErr(undefined);
    try {
      const r = await discoverServices();
      setServices(r.services);
    } catch (e) {
      const msg = e instanceof Error ? e.message : String(e);
      // 503 = discovery disabled. Hide the button entirely.
      if (msg.includes("503") || msg.toLowerCase().includes("not enabled")) {
        setAvailable(false);
      } else {
        setErr(msg);
      }
    } finally {
      setLoading(false);
    }
  }

  function pick(svc: DiscoveredService, port: number) {
    const target = `${svc.ip}:${port}`;
    const url = protocol === "http" ? `http://${target}` : target;
    onPick(url);
    setOpen(false);
  }

  if (!available) return null;
  return (
    <span style={{ float: "right", fontWeight: "normal" }}>
      <button
        type="button"
        className="ghost"
        style={{ fontSize: 11, padding: "2px 8px" }}
        onClick={() => {
          if (!open && !services) load();
          setOpen((o) => !o);
        }}
      >
        {open ? "Hide" : "Discover…"}
      </button>
      {open && (
        <div
          className="card"
          style={{ marginTop: 4, fontWeight: "normal" }}
        >
          {loading && <div className="muted">Loading…</div>}
          {err && <div className="error">{err}</div>}
          {services && services.length === 0 && (
            <div className="muted">No reachable containers found.</div>
          )}
          {services && services.length > 0 && (
            <table>
              <thead>
                <tr>
                  <th>Name</th>
                  <th>Image</th>
                  <th>Network</th>
                  <th>IP</th>
                  <th>Port</th>
                </tr>
              </thead>
              <tbody>
                {services.flatMap((svc) =>
                  (svc.ports && svc.ports.length > 0
                    ? svc.ports
                    : [0]
                  ).map((port) => (
                    <tr key={`${svc.id}-${svc.network ?? ""}-${port}`}>
                      <td className="mono">{svc.name}</td>
                      <td className="mono muted" style={{ fontSize: 11 }}>
                        {svc.image}
                      </td>
                      <td className="mono">{svc.network ?? "—"}</td>
                      <td className="mono">{svc.ip}</td>
                      <td>
                        {port > 0 ? (
                          <button
                            type="button"
                            onClick={() => pick(svc, port)}
                            style={{ padding: "2px 8px", fontSize: 11 }}
                          >
                            {port}
                          </button>
                        ) : (
                          <span className="muted">no exposed ports</span>
                        )}
                      </td>
                    </tr>
                  )),
                )}
              </tbody>
            </table>
          )}
        </div>
      )}
    </span>
  );
}

// HealthCheckSection is the optional Traefik healthCheck block.
// Default-collapsed when the existing host has no health check; only
// the path field is required for the compiler to emit the block.
function HealthCheckSection({
  value,
  onChange,
}: {
  value: HealthCheck;
  onChange: (hc: HealthCheck) => void;
}) {
  const [open, setOpen] = useState(!!value.path);
  const set = (key: keyof HealthCheck, v: unknown) =>
    onChange({ ...value, [key]: v });

  return (
    <div style={{ marginTop: 12 }}>
      <h3 style={{ marginBottom: 4 }}>
        Health check{" "}
        <button
          type="button"
          className="ghost"
          style={{ fontSize: 12, marginLeft: 8 }}
          onClick={() => setOpen((o) => !o)}
        >
          {open ? "Hide" : value.path ? "Edit" : "Configure"}
        </button>
      </h3>
      {open && (
        <div
          style={{
            border: "1px solid var(--border)",
            borderRadius: 6,
            padding: 12,
          }}
        >
          <p className="muted" style={{ fontSize: 11, marginTop: 0 }}>
            Traefik probes each upstream and removes failing servers from
            the load balancer until they recover. Leave path blank to
            disable.
          </p>
          <div className="row">
            <div style={{ flex: 2 }}>
              <label>Path</label>
              <input
                type="text"
                value={value.path ?? ""}
                onChange={(e) => set("path", e.target.value)}
                placeholder="/healthz"
              />
            </div>
            <div>
              <label>Interval</label>
              <input
                type="text"
                value={value.interval ?? ""}
                onChange={(e) => set("interval", e.target.value)}
                placeholder="10s"
              />
            </div>
            <div>
              <label>Timeout</label>
              <input
                type="text"
                value={value.timeout ?? ""}
                onChange={(e) => set("timeout", e.target.value)}
                placeholder="3s"
              />
            </div>
          </div>
          <div className="row" style={{ marginTop: 8 }}>
            <div>
              <label>Scheme override</label>
              <input
                type="text"
                value={value.scheme ?? ""}
                onChange={(e) => set("scheme", e.target.value)}
                placeholder="http or https"
              />
            </div>
            <div>
              <label>Hostname (Host header)</label>
              <input
                type="text"
                value={value.hostname ?? ""}
                onChange={(e) => set("hostname", e.target.value)}
              />
            </div>
            <div>
              <label>Port</label>
              <input
                type="number"
                value={value.port ?? ""}
                onChange={(e) =>
                  set("port", e.target.value ? Number(e.target.value) : undefined)
                }
              />
            </div>
          </div>
          <label
            style={{ display: "block", marginTop: 8, fontSize: 12 }}
          >
            <input
              type="checkbox"
              checked={value.followRedirects ?? false}
              onChange={(e) => set("followRedirects", e.target.checked)}
            />{" "}
            Follow redirects
          </label>
        </div>
      )}
    </div>
  );
}

// ApplyTemplate is the dropdown above the middlewares list that lets
// operators pull in a saved chain. Apply-by-copy: we deep-clone the
// template's middlewares and append them to the host. The host then
// owns those rows; later edits to the template do NOT propagate.
function ApplyTemplate({
  fleetId,
  onApply,
}: {
  fleetId: string;
  onApply: (mws: Middleware[]) => void;
}) {
  const fetcher = useCallback(
    () => listMiddlewareTemplates(fleetId),
    [fleetId],
  );
  const q = useFetch(fetcher);
  const [pickerOpen, setPickerOpen] = useState(false);

  if (!q.data || q.data.length === 0) return null;
  return (
    <div style={{ marginTop: 8, marginBottom: 4 }}>
      <button
        type="button"
        className="ghost"
        onClick={() => setPickerOpen((s) => !s)}
      >
        {pickerOpen ? "Cancel" : "Apply template…"}
      </button>
      {pickerOpen && (
        <div className="card" style={{ marginTop: 8 }}>
          <table>
            <thead>
              <tr>
                <th>Name</th>
                <th>Description</th>
                <th>Types</th>
                <th />
              </tr>
            </thead>
            <tbody>
              {q.data.map((tpl) => (
                <tr key={tpl.id}>
                  <td className="mono">{tpl.name}</td>
                  <td className="muted">{tpl.description || "—"}</td>
                  <td className="mono" style={{ fontSize: 12 }}>
                    {tpl.middlewares.map((mw) => mw.type).join(", ")}
                  </td>
                  <td style={{ textAlign: "right" }}>
                    <button
                      type="button"
                      onClick={() => {
                        // Deep clone so subsequent edits to the host's
                        // middlewares don't mutate the cached template.
                        onApply(
                          JSON.parse(JSON.stringify(tpl.middlewares)),
                        );
                        setPickerOpen(false);
                      }}
                    >
                      Apply
                    </button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
          <p className="muted" style={{ fontSize: 11, marginTop: 8 }}>
            Templates are copied on apply — later edits to the template
            won't affect this host.
          </p>
        </div>
      )}
    </div>
  );
}

export function ProxyHostFormPage() {
  const { fleetId = "", phId } = useParams<{ fleetId: string; phId?: string }>();
  const isNew = phId === undefined || phId === "new";
  const numericId = isNew ? null : Number(phId);
  const navigate = useNavigate();

  // Pre-fill from server if we're editing an existing host.
  const fetcher = useCallback(
    () => (numericId !== null ? getProxyHost(fleetId, numericId) : Promise.resolve(null)),
    [fleetId, numericId],
  );
  const existing = useFetch(fetcher);

  const [name, setName] = useState("");
  const [protocol, setProtocol] = useState<ProxyHostProtocol>("http");
  const [domain, setDomain] = useState("");
  // upstreamUrls is the authoritative list. The form starts with one
  // empty row; existing hosts populate it from `upstream_urls` (or
  // legacy `upstream_url` for hosts created before Phase 7).
  const [upstreamUrls, setUpstreamUrls] = useState<string[]>([""]);
  const [stickySession, setStickySession] = useState(false);
  const [entryPoints, setEntryPoints] = useState("web");
  const [tls, setTls] = useState(false);
  const [labelSelector, setLabelSelector] = useState("");
  const [enabled, setEnabled] = useState(true);
  const [middlewares, setMiddlewares] = useState<Middleware[]>([]);
  const [healthCheck, setHealthCheck] = useState<HealthCheck>({});
  const [busy, setBusy] = useState(false);
  const [submitErr, setSubmitErr] = useState<string | undefined>();

  useEffect(() => {
    if (existing.data) {
      setName(existing.data.name);
      setProtocol((existing.data.protocol as ProxyHostProtocol) || "http");
      setDomain(existing.data.domain);
      const urls =
        existing.data.upstream_urls && existing.data.upstream_urls.length > 0
          ? existing.data.upstream_urls
          : existing.data.upstream_url
            ? [existing.data.upstream_url]
            : [""];
      setUpstreamUrls(urls);
      setStickySession(existing.data.sticky_session ?? false);
      setEntryPoints(existing.data.entry_points.join(","));
      setTls(existing.data.tls);
      setLabelSelector(existing.data.label_selector ?? "");
      setEnabled(existing.data.enabled);
      setMiddlewares(existing.data.middlewares);
      setHealthCheck(existing.data.health_check ?? {});
    }
  }, [existing.data]);

  async function onSubmit(e: React.FormEvent) {
    e.preventDefault();
    setBusy(true);
    setSubmitErr(undefined);
    const cleanedUrls = upstreamUrls.map((u) => u.trim()).filter(Boolean);
    const body: ProxyHostInput = {
      name,
      protocol,
      domain,
      upstream_urls: cleanedUrls,
      // Keep the legacy single-URL field populated as a copy of the
      // first row so older clients that still read `upstream_url`
      // don't break. The backend keeps both in sync.
      upstream_url: cleanedUrls[0] ?? "",
      // sticky_session is HTTP-only; backend ignores it for tcp/udp
      // but sending false everywhere keeps the JSON shape consistent.
      sticky_session: protocol === "http" ? stickySession : false,
      entry_points: entryPoints
        .split(",")
        .map((s) => s.trim())
        .filter(Boolean),
      // Middlewares + TLS are HTTP-only constructs; sending them with
      // tcp/udp confuses the backend validator. Drop them client-side.
      middlewares: protocol === "http" ? middlewares : [],
      // health_check is HTTP-only too. Strip empty fields so we
      // store {} instead of {path: ""} which the compiler treats
      // differently.
      health_check:
        protocol === "http" && healthCheck.path
          ? healthCheck
          : undefined,
      tls: protocol === "udp" ? false : tls,
      label_selector: labelSelector.trim(),
      enabled,
    };
    try {
      if (isNew) {
        await createProxyHost(fleetId, body);
      } else {
        await updateProxyHost(fleetId, numericId!, body);
      }
      navigate(`/fleets/${encodeURIComponent(fleetId)}`);
    } catch (e) {
      setSubmitErr(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  }

  function addMiddleware() {
    setMiddlewares((m) => [
      ...m,
      { name: "mw" + (m.length + 1), type: "headers", config: middlewareSkeleton("headers") },
    ]);
  }
  function removeMiddleware(idx: number) {
    setMiddlewares((m) => m.filter((_, i) => i !== idx));
  }
  function updateMiddleware(idx: number, next: Middleware) {
    setMiddlewares((m) => m.map((mw, i) => (i === idx ? next : mw)));
  }

  if (!isNew && existing.loading) return <div className="muted">Loading…</div>;
  if (!isNew && existing.error) return <ErrorBox>{existing.error}</ErrorBox>;

  return (
    <>
      <Crumbs
        items={[
          { label: "Fleets", to: "/" },
          { label: fleetId, to: `/fleets/${encodeURIComponent(fleetId)}` },
          { label: isNew ? "New proxy host" : `Proxy host #${phId}` },
        ]}
      />

      <h2>{isNew ? "New proxy host" : `Edit ${name || phId}`}</h2>

      <form className="card" onSubmit={onSubmit}>
        <div className="row">
          <div>
            <label>Name</label>
            <input
              type="text"
              value={name}
              onChange={(e) => setName(e.target.value)}
              placeholder="whoami"
              required
            />
          </div>
          <div>
            <label>Protocol</label>
            <select
              value={protocol}
              onChange={(e) => setProtocol(e.target.value as ProxyHostProtocol)}
            >
              <option value="http">HTTP / HTTPS</option>
              <option value="tcp">TCP (HostSNI)</option>
              <option value="udp">UDP</option>
            </select>
          </div>
          {protocol !== "udp" && (
            <div>
              <label>{protocol === "tcp" ? "HostSNI" : "Domain"}</label>
              <input
                type="text"
                value={domain}
                onChange={(e) => setDomain(e.target.value)}
                placeholder={
                  protocol === "tcp"
                    ? 'tls.example.com  or  *  for catch-all'
                    : "whoami.example.com"
                }
                required
              />
            </div>
          )}
        </div>

        <div className="row" style={{ marginTop: 12 }}>
          <div style={{ flex: 2 }}>
            <label>
              {protocol === "http" ? "Upstream URLs" : "Upstream addresses"}{" "}
              <DiscoverInline
                protocol={protocol}
                onPick={(url) =>
                  setUpstreamUrls((cur) => {
                    // First click fills the empty first row; subsequent
                    // clicks append a row so users can add multiple
                    // upstreams from the discover modal.
                    if (cur.length === 1 && cur[0].trim() === "") return [url];
                    return [...cur, url];
                  })
                }
              />
            </label>
            {upstreamUrls.map((u, i) => (
              <div
                key={i}
                style={{ display: "flex", gap: 8, marginBottom: 4 }}
              >
                <input
                  type="text"
                  value={u}
                  onChange={(e) =>
                    setUpstreamUrls((cur) =>
                      cur.map((v, idx) => (idx === i ? e.target.value : v)),
                    )
                  }
                  placeholder={
                    protocol === "http"
                      ? "http://whoami:80"
                      : protocol === "tcp"
                      ? "backend:6379"
                      : "nameserver:53"
                  }
                  required={i === 0}
                  style={{ flex: 1 }}
                />
                {upstreamUrls.length > 1 && (
                  <button
                    type="button"
                    className="ghost"
                    onClick={() =>
                      setUpstreamUrls((cur) => cur.filter((_, idx) => idx !== i))
                    }
                  >
                    Remove
                  </button>
                )}
              </div>
            ))}
            <button
              type="button"
              className="ghost"
              onClick={() => setUpstreamUrls((cur) => [...cur, ""])}
              style={{ fontSize: 12 }}
            >
              + Add upstream
            </button>
            {protocol !== "http" && (
              <p className="muted" style={{ fontSize: 11, marginTop: 4 }}>
                {protocol.toUpperCase()} backends use bare{" "}
                <span className="mono">host:port</span>. A
                <span className="mono"> {protocol}://</span> scheme is
                accepted but not required.
              </p>
            )}
            {protocol === "http" && upstreamUrls.filter((u) => u.trim()).length > 1 && (
              <label style={{ display: "block", marginTop: 8, fontSize: 12 }}>
                <input
                  type="checkbox"
                  checked={stickySession}
                  onChange={(e) => setStickySession(e.target.checked)}
                />{" "}
                Sticky sessions (cookie-based; same client → same upstream)
              </label>
            )}
          </div>
          <div>
            <label>Entry points (comma separated)</label>
            <input
              type="text"
              value={entryPoints}
              onChange={(e) => setEntryPoints(e.target.value)}
            />
          </div>
        </div>

        <div style={{ marginTop: 12 }}>
          <label>Label selector</label>
          <input
            type="text"
            value={labelSelector}
            onChange={(e) => setLabelSelector(e.target.value)}
            placeholder="region=us, tier=prod  (empty = all agents)"
          />
          <p className="muted" style={{ fontSize: 11, marginTop: 4 }}>
            Comma-separated <span className="mono">key=value</span>{" "}
            requirements. Each agent's labels must satisfy every requirement
            for the host to land on that agent. Empty matches every agent.
          </p>
        </div>

        {protocol !== "udp" && (
          <div style={{ marginTop: 12 }}>
            <label>
              <input
                type="checkbox"
                checked={tls}
                onChange={(e) => setTls(e.target.checked)}
              />{" "}
              {protocol === "http"
                ? "TLS (router opts into TLS; Traefik selects a cert from the fleet pool by SNI)"
                : "TLS termination (without this the connection passes through; Traefik routes by SNI but doesn't decrypt)"}
            </label>
          </div>
        )}

        <div style={{ marginTop: 4 }}>
          <label>
            <input
              type="checkbox"
              checked={enabled}
              onChange={(e) => setEnabled(e.target.checked)}
            />{" "}
            Enabled (disabled hosts are dropped at compile time)
          </label>
        </div>

        {protocol === "http" && (
          <HealthCheckSection value={healthCheck} onChange={setHealthCheck} />
        )}

        {protocol === "http" && (
          <>
            <h3>Middlewares</h3>
            <ApplyTemplate
              fleetId={fleetId}
              onApply={(mws) => setMiddlewares((cur) => [...cur, ...mws])}
            />
            {middlewares.length === 0 && (
              <p className="muted">None. Add one if you need redirects, headers, basic auth, or path stripping.</p>
            )}
            {middlewares.map((mw, i) => (
              <MiddlewareEditor
                key={i}
                value={mw}
                onChange={(next) => updateMiddleware(i, next)}
                onRemove={() => removeMiddleware(i)}
              />
            ))}
            <button type="button" className="ghost" onClick={addMiddleware} style={{ marginTop: 8 }}>
              Add middleware
            </button>
          </>
        )}

        {submitErr && (
          <div className="error" style={{ marginTop: 16 }}>
            {submitErr}
          </div>
        )}

        <div className="toolbar" style={{ marginTop: 16 }}>
          <div className="spacer" />
          <button type="button" className="ghost" onClick={() => navigate(-1)}>
            Cancel
          </button>
          <button type="submit" disabled={busy}>
            {isNew ? "Create" : "Save"}
          </button>
        </div>
      </form>
    </>
  );
}

