import { useCallback, useEffect, useState } from "react";
import { useNavigate, useParams } from "react-router-dom";
import {
  createMiddlewareTemplate,
  getMiddlewareTemplate,
  updateMiddlewareTemplate,
} from "../api";
import type { Middleware, MiddlewareTemplateInput } from "../types";
import { Crumbs, ErrorBox } from "../components/Bits";
import { useFetch } from "../components/useFetch";
import {
  MiddlewareEditor,
  middlewareSkeleton,
} from "../components/MiddlewareEditor";

// Phase 7 — fleet-scoped middleware library.
// Reuses the same MiddlewareEditor component the proxy host form uses,
// plus the same skeleton helper, so adding a new middleware type only
// touches one place.
export function MiddlewareTemplateFormPage() {
  const { fleetId = "", tplId } = useParams<{ fleetId: string; tplId?: string }>();
  const isNew = tplId === undefined || tplId === "new";
  const numericId = isNew ? null : Number(tplId);
  const navigate = useNavigate();

  const fetcher = useCallback(
    () =>
      numericId !== null
        ? getMiddlewareTemplate(fleetId, numericId)
        : Promise.resolve(null),
    [fleetId, numericId],
  );
  const existing = useFetch(fetcher);

  const [name, setName] = useState("");
  const [description, setDescription] = useState("");
  const [middlewares, setMiddlewares] = useState<Middleware[]>([]);
  const [busy, setBusy] = useState(false);
  const [submitErr, setSubmitErr] = useState<string | undefined>();

  useEffect(() => {
    if (existing.data) {
      setName(existing.data.name);
      setDescription(existing.data.description);
      setMiddlewares(existing.data.middlewares);
    }
  }, [existing.data]);

  async function onSubmit(e: React.FormEvent) {
    e.preventDefault();
    setBusy(true);
    setSubmitErr(undefined);
    const body: MiddlewareTemplateInput = { name, description, middlewares };
    try {
      if (isNew) {
        await createMiddlewareTemplate(fleetId, body);
      } else {
        await updateMiddlewareTemplate(fleetId, numericId!, body);
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
          { label: isNew ? "New template" : `Template ${name || tplId}` },
        ]}
      />

      <h2>{isNew ? "New middleware template" : `Edit ${name || tplId}`}</h2>

      <form className="card" onSubmit={onSubmit}>
        <div className="row">
          <div>
            <label>Name</label>
            <input
              type="text"
              value={name}
              onChange={(e) => setName(e.target.value)}
              placeholder="standard-oidc"
              required
            />
          </div>
          <div style={{ flex: 2 }}>
            <label>Description</label>
            <input
              type="text"
              value={description}
              onChange={(e) => setDescription(e.target.value)}
              placeholder="oauth2-proxy front + standard X-Frame headers"
            />
          </div>
        </div>

        <h3>Middlewares</h3>
        {middlewares.length === 0 && (
          <p className="muted">
            None yet. Add one — same shape as the proxy host middleware editor.
          </p>
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
