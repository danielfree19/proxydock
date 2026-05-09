import { useCallback, useEffect, useState } from "react";

// Tiny fetch hook: avoids pulling in TanStack Query for Phase 3.
// Returns { data, error, loading, refetch }. The fetcher must be stable
// (wrap in useCallback at the call site) — that's what useEffect tracks.

export function useFetch<T>(fetcher: () => Promise<T>): {
  data: T | undefined;
  error: string | undefined;
  loading: boolean;
  refetch: () => void;
} {
  const [data, setData] = useState<T | undefined>();
  const [error, setError] = useState<string | undefined>();
  const [loading, setLoading] = useState(true);
  const [tick, setTick] = useState(0);

  const refetch = useCallback(() => setTick((t) => t + 1), []);

  useEffect(() => {
    let cancelled = false;
    setLoading(true);
    setError(undefined);
    fetcher()
      .then((d) => {
        if (!cancelled) {
          setData(d);
          setLoading(false);
        }
      })
      .catch((e: unknown) => {
        if (!cancelled) {
          setError(e instanceof Error ? e.message : String(e));
          setLoading(false);
        }
      });
    return () => {
      cancelled = true;
    };
  }, [fetcher, tick]);

  return { data, error, loading, refetch };
}
