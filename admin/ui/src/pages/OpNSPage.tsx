import { useState, useCallback } from "react";
import { apiFetch } from "../api";
import { useToast } from "../useToast";

interface OpnsName {
  txid: string;
  vout: number;
  name: string;
}

interface NameLookupResult {
  name: string;
  origin: string;
  owner: string;
  status: "active" | "pending" | "expired";
}

interface Props {
  identityKey: string | null;
}

export default function OpNSPage({ identityKey }: Props) {
  const { showToast } = useToast();
  
  // Wallet names section
  const [names, setNames] = useState<OpnsName[]>([]);
  const [loading, setLoading] = useState(false);
  const [publishing, setPublishing] = useState<string | null>(null);
  const [crawling, setCrawling] = useState(false);
  
  // Name lookup section
  const [lookupQuery, setLookupQuery] = useState("");
  const [lookupResult, setLookupResult] = useState<NameLookupResult | null>(null);
  const [lookupLoading, setLookupLoading] = useState(false);

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
      showToast("Publish not yet wired up — see console for context", "error");
      console.log("[OpNS] publish requested for:", name);
    } catch (e: any) {
      showToast(e.message || "Publish failed", "error");
    } finally {
      setPublishing(null);
    }
  }, [showToast]);

  const lookupName = useCallback(async () => {
    if (!lookupQuery.trim()) {
      showToast("Enter a name to lookup", "error");
      return;
    }
    
    setLookupLoading(true);
    try {
      // TODO: Wire up to actual OpNS lookup endpoint once available
      // For now, simulate a lookup to show the UI structure
      const mockResult: NameLookupResult = {
        name: lookupQuery,
        origin: "Genesis Block",
        owner: "Unknown",
        status: "pending",
      };
      setLookupResult(mockResult);
      showToast(`Found name: ${lookupQuery}`);
    } catch (e: any) {
      showToast(e.message || "Failed to lookup name", "error");
      setLookupResult(null);
    } finally {
      setLookupLoading(false);
    }
  }, [lookupQuery, showToast]);

  return (
    <div className="page">
      <div className="opns-hero">
        <h2>OpNS Name Management</h2>
        <p>Discover, lookup, and publish OpNS names</p>
      </div>

      <div className="grid-2">
        {/* Name Lookup Section */}
        <div className="card">
          <div className="card-header">
            <h3>Lookup Name in System</h3>
          </div>
          <p className="card-description">
            Find an existing OpNS name and view its origin
          </p>
          <div style={{ display: "flex", gap: "0.5rem", marginBottom: "1rem" }}>
            <input
              type="text"
              value={lookupQuery}
              onChange={(e) => setLookupQuery(e.target.value)}
              placeholder="Enter name (e.g., myname)"
              style={{ flex: 1 }}
              onKeyDown={(e) => e.key === "Enter" && lookupName()}
            />
            <button
              className="primary"
              onClick={lookupName}
              disabled={lookupLoading}
            >
              {lookupLoading ? "Searching..." : "Lookup"}
            </button>
          </div>
          
          {lookupResult && (
            <div className="lookup-result">
              <div className="lookup-row">
                <span className="lookup-label">Name:</span>
                <span className="lookup-value">{lookupResult.name}</span>
              </div>
              <div className="lookup-row">
                <span className="lookup-label">Origin:</span>
                <span className="lookup-value">{lookupResult.origin}</span>
              </div>
              <div className="lookup-row">
                <span className="lookup-label">Owner:</span>
                <span className="lookup-value">{lookupResult.owner}</span>
              </div>
              <div className="lookup-row">
                <span className="lookup-label">Status:</span>
                <span className={`lookup-status ${lookupResult.status}`}>
                  {lookupResult.status}
                </span>
              </div>
            </div>
          )}
        </div>

        {/* My Names Section */}
        <div className="card">
          <div className="card-header">
            <h3>My Wallet Names</h3>
            <div>
              <span className="badge">{names.length} name{names.length !== 1 ? "s" : ""}</span>
              <button
                className="secondary"
                onClick={discoverNames}
                disabled={loading || !identityKey}
                style={{ marginLeft: "0.5rem" }}
              >
                {loading ? "Searching..." : "Discover"}
              </button>
            </div>
          </div>
          <p className="card-description">
            Find OpNS names in your wallet to publish
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
              Populate the name tree
            </span>
          </div>

          <div className="item-list">
            {names.length === 0 ? (
              <div className="empty-state">
                {identityKey
                  ? 'Click "Discover" to find OpNS ordinals in your wallet'
                  : "Connect wallet to discover names"}
              </div>
            ) : (
              names.map((n) => (
                <div key={`${n.txid}_${n.vout}`} className="list-item">
                  <span className="item-name">{n.name}</span>
                  <button
                    className="primary"
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
      </div>
    </div>
  );
}
