import { useCallback } from "react";
import { useParams } from "react-router-dom";
import { getRevision } from "../api";
import { Crumbs, ErrorBox, fmtTime } from "../components/Bits";
import { useFetch } from "../components/useFetch";

export function RevisionDetailPage() {
  const { fleetId = "", number = "" } = useParams<{
    fleetId: string;
    number: string;
  }>();
  const num = Number(number);
  const fetcher = useCallback(() => getRevision(fleetId, num), [fleetId, num]);
  const { data, error, loading } = useFetch(fetcher);

  return (
    <>
      <Crumbs
        items={[
          { label: "Fleets", to: "/" },
          { label: fleetId, to: `/fleets/${encodeURIComponent(fleetId)}` },
          { label: `Revision #${number}` },
        ]}
      />
      <h2>Revision #{number}</h2>
      {loading && <div className="muted">Loading…</div>}
      {error && <ErrorBox>{error}</ErrorBox>}
      {data && (
        <>
          <div className="card">
            <div className="row">
              <div>
                <label>ETag</label>
                <div className="mono">{data.etag}</div>
              </div>
              <div>
                <label>Generated</label>
                <div className="muted">{fmtTime(data.generated_at)}</div>
              </div>
              <div>
                <label>Notes</label>
                <div>{data.notes || <span className="muted">—</span>}</div>
              </div>
            </div>
          </div>
          <h3>Compiled config</h3>
          <pre>{JSON.stringify(data.compiled_config, null, 2)}</pre>
          <h3>Source proxy hosts (snapshot at publish time)</h3>
          <pre>{JSON.stringify(data.source_proxy_hosts, null, 2)}</pre>
        </>
      )}
    </>
  );
}
