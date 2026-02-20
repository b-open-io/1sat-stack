import { useState, useEffect, useCallback, useRef } from "react";
import { apiFetch } from "../api";

interface ProgressItem {
  id: string;
  block: number;
}

interface Props {
  showToast: (msg: string, type?: "success" | "error") => void;
}

export default function Progress({ showToast }: Props) {
  const [items, setItems] = useState<ProgressItem[]>([]);
  const [loading, setLoading] = useState(true);
  const inputRefs = useRef<Record<string, HTMLInputElement | null>>({});

  const load = useCallback(async () => {
    try {
      const res = await apiFetch("/progress");
      setItems(await res.json());
    } catch {
      showToast("Failed to load progress", "error");
    } finally {
      setLoading(false);
    }
  }, [showToast]);

  useEffect(() => { load(); }, [load]);

  async function update(id: string) {
    const el = inputRefs.current[id];
    if (!el) return;
    const block = parseFloat(el.value);
    if (isNaN(block)) { showToast("Please enter a valid block height", "error"); return; }
    try {
      const res = await apiFetch(`/progress/${encodeURIComponent(id)}`, {
        method: "PUT",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ block }),
      });
      if (!res.ok) throw new Error((await res.json()).error || "Failed");
      showToast(`Updated "${id}" to block ${Math.floor(block).toLocaleString()}`);
    } catch (e: any) {
      showToast(e.message, "error");
      load();
    }
  }

  async function remove(id: string) {
    if (!confirm(`Delete progress for "${id}"?`)) return;
    try {
      const res = await apiFetch(`/progress/${encodeURIComponent(id)}`, { method: "DELETE" });
      if (!res.ok) throw new Error((await res.json()).error || "Failed");
      showToast(`Deleted "${id}"`);
      load();
    } catch (e: any) {
      showToast(e.message, "error");
    }
  }

  return (
    <div className="card full-width">
      <div className="card-header">
        <h2>Sync Progress</h2>
        <div>
          <span className="badge">{items.length} entr{items.length !== 1 ? "ies" : "y"}</span>
          <button className="secondary" onClick={() => { setLoading(true); load(); }} style={{ marginLeft: "0.5rem" }}>Refresh</button>
        </div>
      </div>
      <p className="card-description">Sync progress tracking for subscriptions and addresses</p>
      <div className="item-list">
        {loading ? <div className="loading">Loading...</div> :
          items.length === 0 ? <div className="empty-state">No progress entries</div> :
          items.map(item => (
            <div key={item.id} className="list-item">
              <span className="item-name">{item.id}</span>
              <div style={{ display: "flex", alignItems: "center", gap: "0.5rem" }}>
                <input type="number" defaultValue={Math.floor(item.block)}
                  ref={el => { inputRefs.current[item.id] = el; }}
                  style={{ width: 120, padding: "0.25rem 0.5rem", fontSize: "0.875rem" }}
                  onClick={e => e.stopPropagation()} />
                <button className="secondary" style={{ padding: "0.25rem 0.5rem", fontSize: "0.75rem" }}
                  onClick={e => { e.stopPropagation(); update(item.id); }}>Update</button>
                <button style={{ padding: "0.25rem 0.5rem", fontSize: "0.75rem" }}
                  onClick={e => { e.stopPropagation(); remove(item.id); }}>Delete</button>
              </div>
            </div>
          ))}
      </div>
    </div>
  );
}
