import { useState, useEffect, useCallback } from "react";
import { apiFetch } from "../api";

interface WorkerStatus {
  token_id: string;
  fee_address: string;
  queue_depth: number;
  started_at: string;
}

interface Props {
  showToast: (msg: string, type?: "success" | "error") => void;
}

function formatUptime(startedAt: string): string {
  const diff = Math.floor((Date.now() - new Date(startedAt).getTime()) / 1000);
  if (diff < 60) return `${diff}s`;
  if (diff < 3600) return `${Math.floor(diff / 60)}m`;
  if (diff < 86400) return `${Math.floor(diff / 3600)}h ${Math.floor((diff % 3600) / 60)}m`;
  return `${Math.floor(diff / 86400)}d ${Math.floor((diff % 86400) / 3600)}h`;
}

export default function Workers({ showToast }: Props) {
  const [workers, setWorkers] = useState<WorkerStatus[]>([]);
  const [loading, setLoading] = useState(true);

  const load = useCallback(async () => {
    try {
      const res = await apiFetch("/bsv21/workers");
      setWorkers(await res.json());
    } catch {
      showToast("Failed to load workers", "error");
    } finally {
      setLoading(false);
    }
  }, [showToast]);

  useEffect(() => {
    load();
    const interval = setInterval(load, 10000);
    return () => clearInterval(interval);
  }, [load]);

  return (
    <div className="card full-width">
      <div className="card-header">
        <h2>BSV21 Token Workers</h2>
        <div>
          <span className="badge success">{workers.length} worker{workers.length !== 1 ? "s" : ""}</span>
          <button className="secondary" onClick={() => { setLoading(true); load(); }} style={{ marginLeft: "0.5rem" }}>Refresh</button>
        </div>
      </div>
      <p className="card-description">Active token workers processing BSV21 transactions</p>
      <div className="item-list">
        {loading ? <div className="loading">Loading...</div> :
          workers.length === 0 ? <div className="empty-state">No active workers</div> :
          workers.map(w => (
            <div key={w.token_id} className="list-item">
              <div style={{ flex: 1, minWidth: 0 }}>
                <div className="item-name" style={{ marginBottom: "0.25rem" }}>{w.token_id}</div>
                <div style={{ fontSize: "0.75rem", color: "var(--text-secondary)" }}>
                  Fee: {w.fee_address} · Up: {formatUptime(w.started_at)}
                </div>
              </div>
              <span className={`badge ${w.queue_depth > 100 ? "warning" : w.queue_depth > 0 ? "" : "success"}`}>
                {w.queue_depth} queued
              </span>
            </div>
          ))}
      </div>
    </div>
  );
}
