import { useState, useEffect, useCallback } from "react";
import { fetchHealth, fetchCapabilities } from "@/lib/api";
import {
  connectWallet,
  disconnectWallet,
  getIdentityKey,
  isConnected,
} from "@/lib/wallet";

const OVERLAY_MAP: Record<string, string> = {
  bsv21: "BSV21 Tokens",
  bap: "BAP Identity",
  opns: "OPNS Domains",
  market: "Marketplace",
  bsocial: "BSocial",
};

function formatUptime(seconds?: number): string {
  if (seconds == null) return "---";
  const d = Math.floor(seconds / 86400);
  const h = Math.floor((seconds % 86400) / 3600);
  const m = Math.floor((seconds % 3600) / 60);
  const parts: string[] = [];
  if (d > 0) parts.push(`${d}d`);
  if (h > 0 || d > 0) parts.push(`${h}h`);
  parts.push(`${m}m`);
  return parts.join(" ");
}

function truncateKey(key: string): string {
  return key.length > 16 ? `${key.slice(0, 8)}...${key.slice(-8)}` : key;
}

export default function App() {
  const [health, setHealth] = useState<{
    status: string;
    version?: string;
    uptime?: number;
    height?: number;
  } | null>(null);
  const [overlays, setOverlays] = useState<string[] | null>(null);
  const [connected, setConnected] = useState(isConnected());
  const [identityKey, setIdentityKey] = useState<string | null>(
    getIdentityKey()
  );

  useEffect(() => {
    fetchHealth()
      .then(setHealth)
      .catch(() => setHealth({ status: "unreachable" }));
    fetchCapabilities()
      .then(setOverlays)
      .catch(() => setOverlays([]));
  }, []);

  const handleConnect = useCallback(async () => {
    if (connected) {
      disconnectWallet();
      setConnected(false);
      setIdentityKey(null);
    } else {
      try {
        await connectWallet();
        setConnected(true);
        setIdentityKey(getIdentityKey());
      } catch {
        /* wallet unavailable */
      }
    }
  }, [connected]);

  const activeOverlays = overlays
    ? overlays.filter((c) => c in OVERLAY_MAP)
    : null;

  return (
    <div className="max-w-3xl mx-auto px-6 py-8">
      {/* Header */}
      <div className="flex items-center justify-between pb-4 border-b border-[var(--color-border)]">
        <span className="text-base font-bold text-[var(--color-text-primary)]">
          1sat-stack
        </span>
        <button
          onClick={handleConnect}
          className="text-xs px-3 py-1 border border-[var(--color-border)] text-[var(--color-interactive)] hover:bg-[var(--color-bg-secondary)] cursor-pointer"
        >
          {connected && identityKey
            ? truncateKey(identityKey)
            : "connect"}
        </button>
      </div>

      {/* System Status */}
      <div className="py-4 border-b border-[var(--color-border)]">
        <div className="text-xs text-[var(--color-text-muted)] mb-2">
          ├─ system
        </div>
        <div className="grid grid-cols-3 gap-4 text-sm">
          <div>
            <div className="text-[10px] text-[var(--color-text-muted)] uppercase tracking-wider">
              height
            </div>
            <div className="text-[var(--color-text-primary)]">
              {health?.height?.toLocaleString() ?? "---"}
            </div>
          </div>
          <div>
            <div className="text-[10px] text-[var(--color-text-muted)] uppercase tracking-wider">
              uptime
            </div>
            <div className="text-[var(--color-text-primary)]">
              {formatUptime(health?.uptime)}
            </div>
          </div>
          <div>
            <div className="text-[10px] text-[var(--color-text-muted)] uppercase tracking-wider">
              version
            </div>
            <div className="text-[var(--color-text-primary)]">
              {health?.version ?? "---"}
            </div>
          </div>
        </div>
      </div>

      {/* Active Overlays */}
      <div className="py-4 border-b border-[var(--color-border)]">
        <div className="text-xs text-[var(--color-text-muted)] mb-2">
          ├─ overlays
        </div>
        {activeOverlays === null ? (
          <div className="text-sm text-[var(--color-text-secondary)]">
            loading...
          </div>
        ) : activeOverlays.length === 0 ? (
          <div className="text-sm text-[var(--color-text-muted)]">
            no overlays active
          </div>
        ) : (
          <div className="space-y-1">
            {activeOverlays.map((key) => (
              <div key={key} className="flex items-center gap-2 text-sm">
                <span className="text-[var(--color-status-green)]">●</span>
                <span className="text-[var(--color-text-primary)]">
                  {OVERLAY_MAP[key]}
                </span>
                <span className="text-[var(--color-text-muted)]">{key}</span>
              </div>
            ))}
          </div>
        )}
      </div>

      {/* Tools & Navigation */}
      <div className="py-4 border-b border-[var(--color-border)]">
        <div className="text-xs text-[var(--color-text-muted)] mb-2">
          ├─ navigate
        </div>
        <div className="grid grid-cols-2 gap-y-1 gap-x-8 text-sm">
          <a
            href="./sweep/"
            className="text-[var(--color-interactive)] hover:underline"
          >
            sweep
          </a>
          <a
            href="/1sat/docs"
            className="text-[var(--color-interactive)] hover:underline"
          >
            api docs
          </a>
          <a
            href="./admin/"
            className="text-[var(--color-interactive)] hover:underline"
          >
            admin
          </a>
          <a
            href="https://github.com/b-open-io/1sat-stack"
            target="_blank"
            rel="noopener noreferrer"
            className="text-[var(--color-interactive)] hover:underline"
          >
            github ↗
          </a>
        </div>
      </div>

      {/* Explorer Placeholder */}
      <div className="py-4">
        <div className="text-xs text-[var(--color-text-muted)] mb-2">
          └─ explorer
        </div>
        <div className="text-sm text-[var(--color-text-muted)]">
          overlay explorer coming soon
        </div>
      </div>
    </div>
  );
}
