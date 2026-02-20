import { useState, useEffect, useCallback } from "react";
import { apiFetch } from "../api";

interface Props {
  showToast: (msg: string, type?: "success" | "error") => void;
}

export default function Topics({ showToast }: Props) {
  const [topics, setTopics] = useState<string[]>([]);
  const [loading, setLoading] = useState(true);

  const load = useCallback(async () => {
    try {
      const res = await apiFetch("/topics/active");
      setTopics(await res.json());
    } catch {
      showToast("Failed to load topics", "error");
    } finally {
      setLoading(false);
    }
  }, [showToast]);

  useEffect(() => { load(); }, [load]);

  return (
    <div className="card">
      <div className="card-header">
        <h2>Active Topics</h2>
        <div>
          <span className="badge">{topics.length} topic{topics.length !== 1 ? "s" : ""}</span>
          <button className="secondary" onClick={() => { setLoading(true); load(); }} style={{ marginLeft: "0.5rem" }}>Refresh</button>
        </div>
      </div>
      <p className="card-description">Currently active topic managers in the overlay engine</p>
      <div className="item-list">
        {loading ? <div className="loading">Loading...</div> :
          topics.length === 0 ? <div className="empty-state">No topics</div> :
          topics.map(t => (
            <div key={t} className="list-item">
              <span className="item-name">{t}</span>
            </div>
          ))}
      </div>
    </div>
  );
}
