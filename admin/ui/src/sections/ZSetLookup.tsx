import { useState } from "react";
import { apiFetch } from "../api";

interface ZSetItem {
  value: string;
  score: number;
}

interface Props {
  showToast: (msg: string, type?: "success" | "error") => void;
}

export default function ZSetLookup({ showToast }: Props) {
  const [input, setInput] = useState("");
  const [key, setKey] = useState("");
  const [items, setItems] = useState<ZSetItem[]>([]);
  const [count, setCount] = useState(0);
  const [visible, setVisible] = useState(false);
  const [loading, setLoading] = useState(false);

  async function lookup() {
    const val = input.trim();
    if (!val) { showToast("Please enter a sorted set key", "error"); return; }
    setVisible(true);
    setLoading(true);
    try {
      const res = await apiFetch(`/zset/${encodeURIComponent(val)}`);
      if (!res.ok) throw new Error((await res.json()).error || "Failed to lookup");
      const data = await res.json();
      setKey(data.key);
      setCount(data.count);
      setItems(data.items || []);
    } catch (e: any) {
      setItems([]);
      setCount(0);
      showToast(e.message, "error");
    } finally {
      setLoading(false);
    }
  }

  return (
    <div className="card full-width">
      <div className="card-header">
        <h2>Sorted Set Lookup</h2>
      </div>
      <p className="card-description">Look up any sorted set by key (e.g., q:tok:abc123, z:ev:own:address)</p>
      <div className="input-group">
        <input type="text" value={input} onChange={e => setInput(e.target.value)}
          onKeyDown={e => e.key === "Enter" && lookup()}
          placeholder="Enter sorted set key (e.g., q:tok:abc123...)" />
        <button onClick={lookup}>Lookup</button>
      </div>
      {visible && (
        <div>
          <div style={{ display: "flex", justifyContent: "space-between", alignItems: "center", marginBottom: "0.5rem" }}>
            <span className="item-name">{key}</span>
            <span className="badge">{count} item{count !== 1 ? "s" : ""}</span>
          </div>
          <div className="item-list">
            {loading ? <div className="loading">Loading...</div> :
              items.length === 0 ? <div className="empty-state">No items found</div> :
              items.map((item, i) => (
                <div key={i} className="list-item">
                  <span className="item-name" style={{ wordBreak: "break-all" }}>{item.value}</span>
                  <span className="badge" style={{ whiteSpace: "nowrap" }}>score: {item.score}</span>
                </div>
              ))}
          </div>
        </div>
      )}
    </div>
  );
}
