import { useState } from "react";
import type { Middleware } from "../types";

// MIDDLEWARE_TYPES drives the type dropdown. Keep it in sync with the
// compiler's `supportedMiddlewareTypes` set in
// `apps/api/internal/compiler/compiler.go`.
export const MIDDLEWARE_TYPES = [
  "headers",
  "redirectScheme",
  "stripPrefix",
  "basicAuth",
  "forwardAuth",
  "rateLimit",
  "ipAllowList",
  "retry",
  "compress",
  "circuitBreaker",
  "chain",
] as const;

// middlewareSkeleton returns a starter Config object for a freshly-
// added middleware row, so users see a working template instead of
// "{}". Operators can then edit fields to taste.
export function middlewareSkeleton(t: string): Record<string, unknown> {
  switch (t) {
    case "stripPrefix":
      return { prefixes: ["/api"] };
    case "redirectScheme":
      return { scheme: "https", permanent: true };
    case "basicAuth":
      return { users: ["alice:$2y$05$<bcrypt-hash>"] };
    case "headers":
      return { customResponseHeaders: { "X-Frame-Options": "DENY" } };
    case "forwardAuth":
      // Default to oauth2-proxy's standard endpoint + headers; users
      // running a different OIDC proxy (vouch, traefik-forward-auth)
      // change `address` and the response headers.
      return {
        address: "http://oauth2-proxy:4180/oauth2/auth",
        trustForwardHeader: true,
        authResponseHeaders: [
          "X-Auth-Request-User",
          "X-Auth-Request-Email",
          "X-Auth-Request-Groups",
        ],
      };
    case "rateLimit":
      return { average: 100, burst: 200 };
    case "ipAllowList":
      return { sourceRange: ["10.0.0.0/8", "192.168.0.0/16"] };
    case "retry":
      return { attempts: 3, initialInterval: "100ms" };
    case "compress":
      return {};
    case "circuitBreaker":
      return { expression: "NetworkErrorRatio() > 0.50" };
    case "chain":
      // chain.middlewares are the *raw* names of other middlewares on
      // the same host (compiler resolves them to the per-host mangled
      // names at compile time).
      return { middlewares: ["mw1", "mw2"] };
    default:
      return {};
  }
}

// MiddlewareEditor is one row in a list — a name input, a type
// dropdown, a JSON config textarea, and a remove button. Used by both
// the proxy host form and the middleware template form.
export function MiddlewareEditor({
  value,
  onChange,
  onRemove,
}: {
  value: Middleware;
  onChange: (m: Middleware) => void;
  onRemove: () => void;
}) {
  const [configText, setConfigText] = useState(
    JSON.stringify(value.config ?? {}, null, 2),
  );
  const [parseErr, setParseErr] = useState<string | undefined>();

  function applyConfigText(text: string) {
    setConfigText(text);
    try {
      const parsed = text.trim() === "" ? {} : JSON.parse(text);
      setParseErr(undefined);
      onChange({ ...value, config: parsed });
    } catch (e) {
      setParseErr(e instanceof Error ? e.message : String(e));
    }
  }

  // Switching middleware type swaps in a fresh skeleton — the JSON
  // shape is completely different per type, so retaining the old text
  // would just produce a parse/validate error on save.
  function applyTypeChange(nextType: string) {
    const skeleton = middlewareSkeleton(nextType);
    setConfigText(JSON.stringify(skeleton, null, 2));
    setParseErr(undefined);
    onChange({ ...value, type: nextType, config: skeleton });
  }

  return (
    <div
      style={{
        border: "1px solid var(--border)",
        borderRadius: 6,
        padding: 12,
        marginTop: 8,
      }}
    >
      <div className="row">
        <div>
          <label>Name</label>
          <input
            type="text"
            value={value.name}
            onChange={(e) => onChange({ ...value, name: e.target.value })}
          />
        </div>
        <div>
          <label>Type</label>
          <select
            value={value.type}
            onChange={(e) => applyTypeChange(e.target.value)}
          >
            {MIDDLEWARE_TYPES.map((t) => (
              <option key={t}>{t}</option>
            ))}
          </select>
        </div>
        <div className="shrink" style={{ alignSelf: "flex-end" }}>
          <button type="button" className="ghost" onClick={onRemove}>
            Remove
          </button>
        </div>
      </div>
      <div style={{ marginTop: 12 }}>
        <label>Config (JSON)</label>
        <textarea
          rows={4}
          value={configText}
          onChange={(e) => applyConfigText(e.target.value)}
          style={{ fontFamily: "var(--mono)", fontSize: 12 }}
        />
        {parseErr && (
          <div className="error" style={{ marginTop: 8 }}>
            JSON: {parseErr}
          </div>
        )}
      </div>
    </div>
  );
}
