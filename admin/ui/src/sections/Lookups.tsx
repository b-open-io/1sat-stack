import { useState, useEffect, useCallback } from "react";
import { apiFetch } from "../api";

interface Props {
  showToast: (msg: string, type?: "success" | "error") => void;
}

export default function Lookups({ showToast }: Props) {
  const [lookups, setLookups] = useState<string[]>([]);
  const [loading, setLoading] = useState(true);

  const load = useCallback(async () => {
    try {
      const res = await apiFetch("/lookups/active");
      setLookups(await res.json());
    } catch {
      showToast("Failed to load lookups", "error");
    } finally {
      setLoading(false);
    }
  }, [showToast]);

  useEffect(() => { load(); }, [load]);

  return (
    <div className="card">
      <div className="card-header">
        <h2>Lookup Services</h2>
        <div>
          <span className="badge">{lookups.length} service{lookups.length !== 1 ? "s" : ""}</span>
          <button className="secondary" onClick={() => { setLoading(true); load(); }} style={{ marginLeft: "0.5rem" }}>Refresh</button>
        </div>
      </div>
      <p className="card-description">Currently active lookup service providers in the overlay engine</p>
      <div className="item-list">
        {loading ? <div className="loading">Loading...</div> :
          lookups.length === 0 ? <div className="empty-state">No services</div> :
          lookups.map(l => (
            <div key={l} className="list-item">
              <span className="item-name">{l}</span>
            </div>
          ))}
      </div>
    </div>
  );
}
