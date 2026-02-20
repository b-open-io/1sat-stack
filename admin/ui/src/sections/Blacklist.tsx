import { useState, useEffect, useCallback } from "react";
import { apiFetch } from "../api";

interface Props {
  showToast: (msg: string, type?: "success" | "error") => void;
}

export default function Blacklist({ showToast }: Props) {
  const [tokens, setTokens] = useState<string[]>([]);
  const [input, setInput] = useState("");
  const [loading, setLoading] = useState(true);

  const load = useCallback(async () => {
    try {
      const res = await apiFetch("/blacklist");
      setTokens(await res.json());
    } catch {
      showToast("Failed to load blacklist", "error");
    } finally {
      setLoading(false);
    }
  }, [showToast]);

  useEffect(() => { load(); }, [load]);

  async function add() {
    const val = input.trim();
    if (!val) { showToast("Please enter a token ID", "error"); return; }
    try {
      const res = await apiFetch("/blacklist", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ topic: val }),
      });
      if (!res.ok) throw new Error((await res.json()).error || "Failed");
      setInput("");
      showToast(`Added "${val}" to blacklist`);
      load();
    } catch (e: any) {
      showToast(e.message, "error");
    }
  }

  async function remove(tokenId: string) {
    try {
      const res = await apiFetch(`/blacklist/${encodeURIComponent(tokenId)}`, { method: "DELETE" });
      if (!res.ok) throw new Error((await res.json()).error || "Failed");
      showToast(`Removed "${tokenId}" from blacklist`);
      load();
    } catch (e: any) {
      showToast(e.message, "error");
    }
  }

  return (
    <div className="card">
      <div className="card-header">
        <h2>BSV21 Token Blacklist</h2>
        <span className="badge warning">{tokens.length} token{tokens.length !== 1 ? "s" : ""}</span>
      </div>
      <p className="card-description">BSV21 tokens that will not be activated, even with positive balance</p>
      <div className="input-group">
        <input type="text" value={input} onChange={e => setInput(e.target.value)}
          onKeyDown={e => e.key === "Enter" && add()}
          placeholder="Enter token ID (e.g., abc123...)" />
        <button onClick={add}>Add</button>
      </div>
      <div className="item-list">
        {loading ? <div className="loading">Loading...</div> :
          tokens.length === 0 ? <div className="empty-state">No tokens</div> :
          tokens.map(t => (
            <div key={t} className="list-item">
              <span className="item-name">{t}</span>
              <button onClick={() => remove(t)}>Remove</button>
            </div>
          ))}
      </div>
    </div>
  );
}
