import type { ReactNode } from "react";

export function Crumbs({ items }: { items: { label: string; to?: string }[] }) {
  return (
    <div className="crumbs">
      {items.map((it, i) => (
        <span key={i}>
          {it.to ? <a href={it.to}>{it.label}</a> : it.label}
          {i < items.length - 1 ? " / " : null}
        </span>
      ))}
    </div>
  );
}

export function ErrorBox({ children }: { children: ReactNode }) {
  return <div className="error">{children}</div>;
}

export function Empty({ children }: { children: ReactNode }) {
  return <div className="empty">{children}</div>;
}

// Format an ISO timestamp into something compact and human-friendly.
export function fmtTime(iso?: string | null): string {
  if (!iso) return "—";
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return iso;
  return d.toLocaleString();
}

// Time-ago in coarse units for "last heartbeat 12s ago" labels.
export function fmtAgo(iso?: string | null): string {
  if (!iso) return "never";
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return iso;
  const secs = Math.max(0, Math.floor((Date.now() - d.getTime()) / 1000));
  if (secs < 60) return `${secs}s ago`;
  if (secs < 3600) return `${Math.floor(secs / 60)}m ago`;
  if (secs < 86400) return `${Math.floor(secs / 3600)}h ago`;
  return `${Math.floor(secs / 86400)}d ago`;
}
