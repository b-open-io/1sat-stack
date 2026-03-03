import { useState, useCallback } from "react";
import { apiFetch } from "../api";

interface Props {
  showToast: (msg: string, type?: "success" | "error") => void;
  identityKey: string | null;
}

interface OpnsName {
  txid: string;
  vout: number;
  name: string;
}

export default function OpNS({ showToast, identityKey }: Props) {
  const [names, setNames] = useState<OpnsName[]>([]);
  const [loading, setLoading] = useState(false);
  const [publishing, setPublishing] = useState<string | null>(null);
  const [crawling, setCrawling] = useState(false);

  const triggerCrawl = useCallback(async () => {
    setCrawling(true);
    try {
      const res = await apiFetch("/opns/crawl", { method: "POST" });
      const data = await res.json();
      if (!res.ok) throw new Error(data.error || res.statusText);
      showToast(data.message || "Crawl started");
    } catch (e: any) {
      showToast(e.message || "Failed to start crawl", "error");
    } finally {
      setCrawling(false);
    }
  }, [showToast]);

  const discoverNames = useCallback(async () => {
    setLoading(true);
    try {
      // TODO: Determine the right discovery mechanism.
      // Options: wallet listOutputs, 1sat indexer owner endpoint, or server overlay.
      const cwi = (window as any).CWI;
      if (!cwi) {
        showToast("No wallet connected", "error");
        return;
      }

      const result = await cwi.listOutputs({
        basket: "default",
        include: "locking scripts",
      });

      showToast(`listOutputs returned ${result?.outputs?.length ?? 0} outputs — inspect console`);
      console.log("[OpNS] listOutputs result:", result);

      // TODO: Filter for OpNS ordinals (application/op-ns content type)
      setNames([]);
    } catch (e: any) {
      console.error("[OpNS] discoverNames error:", e);
      showToast(e.message || "Failed to discover names", "error");
    } finally {
      setLoading(false);
    }
  }, [showToast]);

  const publishName = useCallback(async (name: OpnsName) => {
    setPublishing(name.name);
    try {
      // TODO: Wire up opnsRegister action from @1sat/actions
      showToast("Publish not yet wired up — see console for context", "error");
      console.log("[OpNS] publish requested for:", name);
    } catch (e: any) {
      showToast(e.message || "Publish failed", "error");
    } finally {
      setPublishing(null);
    }
  }, [showToast]);

  return (
    <div className="card">
      <div className="card-header">
        <h2>OpNS Names</h2>
        <div>
          <span className="badge">{names.length} name{names.length !== 1 ? "s" : ""}</span>
          <button
            className="secondary"
            onClick={discoverNames}
            disabled={loading || !identityKey}
            style={{ marginLeft: "0.5rem" }}
          >
            {loading ? "Searching..." : "Discover My Names"}
          </button>
        </div>
      </div>
      <p className="card-description">
        Publish your OpNS names to bind your identity key and enable paymail
      </p>

      {identityKey && (
        <div style={{ fontSize: "0.75rem", color: "#888", marginBottom: "0.75rem", fontFamily: "monospace" }}>
          Identity: {identityKey.slice(0, 8)}...{identityKey.slice(-8)}
        </div>
      )}

      <div style={{ marginBottom: "0.75rem" }}>
        <button
          className="secondary"
          onClick={triggerCrawl}
          disabled={crawling}
        >
          {crawling ? "Starting..." : "Sync OpNS Tree"}
        </button>
        <span style={{ fontSize: "0.75rem", color: "#888", marginLeft: "0.5rem" }}>
          One-time genesis crawl to populate the OpNS mine tree
        </span>
      </div>

      <div className="item-list">
        {names.length === 0 ? (
          <div className="empty-state">
            {identityKey
              ? 'Click "Discover My Names" to find OpNS ordinals in your wallet'
              : "Connect wallet to discover your OpNS names"}
          </div>
        ) : (
          names.map((n) => (
            <div key={`${n.txid}_${n.vout}`} className="list-item">
              <span className="item-name">{n.name}</span>
              <button
                className="danger"
                onClick={() => publishName(n)}
                disabled={publishing === n.name}
              >
                {publishing === n.name ? "Publishing..." : "Publish"}
              </button>
            </div>
          ))
        )}
      </div>
    </div>
  );
}
