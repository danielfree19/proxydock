// Pure TS helpers for diffing two snapshots of a fleet's source state.
//
// We don't pull in a diff library: snapshots are bounded (10s-100s of
// proxy hosts at most), and the entire surface we care about is
// "list of records keyed by name with a small struct of fields".

import type { Certificate, Middleware, ProxyHost } from "../types";

export type DiffStatus = "added" | "removed" | "modified" | "unchanged";

export interface FieldChange {
  field: string;
  before: unknown;
  after: unknown;
}

export interface ProxyHostDiff {
  name: string;
  status: DiffStatus;
  before?: ProxyHost;
  after?: ProxyHost;
  changes?: FieldChange[];
}

export interface CertificateDiff {
  name: string;
  status: DiffStatus;
  before?: Certificate;
  after?: Certificate;
  changes?: FieldChange[];
}

// proxyHostFields lists every field we surface in a per-host diff.
// Order matters — that's the order the UI renders.
const proxyHostFields = [
  "domain",
  "upstream_url",
  "entry_points",
  "middlewares",
  "tls",
  "label_selector",
  "enabled",
] as const;

export function diffProxyHosts(
  before: ProxyHost[],
  after: ProxyHost[],
): ProxyHostDiff[] {
  const byName = (xs: ProxyHost[]) =>
    new Map(xs.map((h) => [h.name, h] as const));
  const a = byName(before);
  const b = byName(after);

  // Names are the union, sorted for deterministic output.
  const names = Array.from(new Set([...a.keys(), ...b.keys()])).sort();

  return names.map((name): ProxyHostDiff => {
    const x = a.get(name);
    const y = b.get(name);
    if (x && !y) return { name, status: "removed", before: x };
    if (!x && y) return { name, status: "added", after: y };
    if (!x || !y) {
      // unreachable, keeps TS happy
      return { name, status: "unchanged" };
    }
    const changes = diffFields(x, y, proxyHostFields);
    if (changes.length === 0) {
      return { name, status: "unchanged", before: x, after: y };
    }
    return { name, status: "modified", before: x, after: y, changes };
  });
}

export function diffCertificates(
  before: Certificate[],
  after: Certificate[],
): CertificateDiff[] {
  const byName = (xs: Certificate[]) =>
    new Map(xs.map((c) => [c.name, c] as const));
  const a = byName(before);
  const b = byName(after);
  const names = Array.from(new Set([...a.keys(), ...b.keys()])).sort();

  return names.map((name): CertificateDiff => {
    const x = a.get(name);
    const y = b.get(name);
    if (x && !y) return { name, status: "removed", before: x };
    if (!x && y) return { name, status: "added", after: y };
    if (!x || !y) return { name, status: "unchanged" };
    const changes: FieldChange[] = [];
    if (x.fingerprint !== y.fingerprint) {
      // Fingerprint is the canonical identity of the cert material —
      // rotating a cert under the same name is the case to flag.
      changes.push({
        field: "fingerprint",
        before: x.fingerprint,
        after: y.fingerprint,
      });
    }
    if (x.not_after !== y.not_after) {
      changes.push({ field: "not_after", before: x.not_after, after: y.not_after });
    }
    if ((x.dns_names ?? []).join(",") !== (y.dns_names ?? []).join(",")) {
      changes.push({ field: "dns_names", before: x.dns_names, after: y.dns_names });
    }
    return changes.length
      ? { name, status: "modified", before: x, after: y, changes }
      : { name, status: "unchanged", before: x, after: y };
  });
}

// diffFields returns one FieldChange per differing key. Comparison is
// deep: middlewares (an array of objects) and entry_points (an array
// of strings) are compared by stable JSON serialization.
function diffFields<T>(x: T, y: T, fields: readonly (keyof T)[]): FieldChange[] {
  const out: FieldChange[] = [];
  for (const f of fields) {
    if (!equal(x[f], y[f])) {
      out.push({ field: String(f), before: x[f], after: y[f] });
    }
  }
  return out;
}

function equal(a: unknown, b: unknown): boolean {
  if (a === b) return true;
  if (Array.isArray(a) && Array.isArray(b)) {
    if (a.length !== b.length) return false;
    for (let i = 0; i < a.length; i++) {
      if (!equal(a[i], b[i])) return false;
    }
    return true;
  }
  if (a && b && typeof a === "object" && typeof b === "object") {
    const ak = Object.keys(a as object).sort();
    const bk = Object.keys(b as object).sort();
    if (ak.length !== bk.length) return false;
    for (let i = 0; i < ak.length; i++) {
      if (ak[i] !== bk[i]) return false;
      if (!equal((a as Record<string, unknown>)[ak[i]],
                 (b as Record<string, unknown>)[bk[i]])) return false;
    }
    return true;
  }
  return false;
}

// Helper for the page: format a single FieldChange value for display.
// Strings are inlined; arrays/objects render as compact JSON.
export function formatValue(v: unknown): string {
  if (v === undefined || v === null) return "—";
  if (typeof v === "string") return v;
  if (typeof v === "number" || typeof v === "boolean") return String(v);
  if (Array.isArray(v)) {
    if (v.length === 0) return "[]";
    if (v.every((x) => typeof x === "string")) return v.join(", ");
    return JSON.stringify(v);
  }
  return JSON.stringify(v);
}

// Convenience: count summary of a diff.
export function diffSummary(diffs: { status: DiffStatus }[]): {
  added: number;
  removed: number;
  modified: number;
  unchanged: number;
} {
  return diffs.reduce(
    (acc, d) => {
      acc[d.status] += 1;
      return acc;
    },
    { added: 0, removed: 0, modified: 0, unchanged: 0 },
  );
}

// parseSourceArray safely turns a Revision.source_* field (which the
// API serializes as a JSON-encoded array, but our types model as
// `unknown` because we don't want consumers to assume the shape) into
// the typed array our diff helpers expect.
export function parseSourceArray<T>(raw: unknown): T[] {
  if (Array.isArray(raw)) return raw as T[];
  if (typeof raw === "string") {
    try {
      const v = JSON.parse(raw);
      return Array.isArray(v) ? (v as T[]) : [];
    } catch {
      return [];
    }
  }
  return [];
}

// Re-export Middleware so the diff page doesn't have to import from
// two paths just to render middleware lists.
export type { Middleware };
