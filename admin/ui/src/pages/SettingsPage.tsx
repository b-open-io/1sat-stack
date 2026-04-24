import { useState, useEffect, useRef, useMemo } from "react";
import { toast } from "sonner";
import { getConfig, saveConfig, apiFetch } from "@/api";
import { toastError } from "@/lib/utils";
import {
  Server,
  Database,
  RefreshCw,
  Shield,
  Cpu,
  Network,
  AlertTriangle,
  Eye,
  EyeOff,
  Info,
  ScanSearch,
  Plus,
  Trash2,
  GripVertical,
  Droplets,
  ScrollText,
} from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { cn } from "@/lib/utils";
import Logs from "@/sections/Logs";

// ─── Primitives ──────────────────────────────────────────────────────────────

function StatusDot({ active }: { active: boolean }) {
  return (
    <span
      className={cn(
        "inline-block w-2 h-2 rounded-full shrink-0",
        active ? "bg-success" : "bg-muted-foreground/40"
      )}
    />
  );
}

function RestartBadge() {
  return (
    <span className="inline-flex items-center gap-1 text-[10px] font-medium px-1.5 py-0.5 rounded bg-warning/15 text-warning">
      <AlertTriangle className="w-2.5 h-2.5" />
      restart required
    </span>
  );
}

function HotBadge() {
  return (
    <span className="inline-flex items-center gap-1 text-[10px] font-medium px-1.5 py-0.5 rounded bg-success/15 text-success">
      hot-reload
    </span>
  );
}

function Toggle({
  enabled,
  onChange,
  disabled,
}: {
  enabled: boolean;
  onChange: (v: boolean) => void;
  disabled?: boolean;
}) {
  return (
    <button
      type="button"
      role="switch"
      aria-checked={enabled}
      disabled={disabled}
      onClick={() => !disabled && onChange(!enabled)}
      className={cn(
        "relative inline-flex h-5 w-9 shrink-0 items-center rounded-full border-2 border-transparent transition-colors",
        disabled ? "opacity-40 cursor-not-allowed" : "cursor-pointer",
        enabled ? "bg-primary" : "bg-muted"
      )}
    >
      <span
        className={cn(
          "pointer-events-none inline-block h-4 w-4 transform rounded-full bg-white shadow-sm transition-transform",
          enabled ? "translate-x-4" : "translate-x-0"
        )}
      />
    </button>
  );
}

function FieldRow({
  label,
  children,
  badge,
  hint,
}: {
  label: string;
  children: React.ReactNode;
  badge?: React.ReactNode;
  hint?: string;
}) {
  return (
    <div className="space-y-1.5">
      <div className="flex items-center gap-2">
        <label className="text-xs font-medium text-muted-foreground">{label}</label>
        {badge}
      </div>
      {children}
      {hint && <p className="text-[11px] text-muted-foreground/70">{hint}</p>}
    </div>
  );
}

function SectionCard({
  children,
  className,
}: {
  children: React.ReactNode;
  className?: string;
}) {
  return (
    <div className={cn("rounded-xl border border-border bg-card p-5 space-y-4", className)}>
      {children}
    </div>
  );
}

function SectionHeading({ children }: { children: React.ReactNode }) {
  return (
    <h3 className="text-xs font-semibold text-muted-foreground uppercase tracking-wide">
      {children}
    </h3>
  );
}

function PageHeader({ title, description }: { title: string; description: string }) {
  return (
    <div>
      <h2 className="text-sm font-semibold text-foreground">{title}</h2>
      <p className="text-xs text-muted-foreground mt-0.5">{description}</p>
    </div>
  );
}

function SegmentedControl<T extends string>({
  options,
  value,
  onChange,
}: {
  options: { value: T; label: string }[];
  value: T;
  onChange: (v: T) => void;
}) {
  return (
    <div className="flex gap-2">
      {options.map((opt) => (
        <button
          key={opt.value}
          type="button"
          onClick={() => onChange(opt.value)}
          className={cn(
            "flex-1 py-1.5 px-3 text-xs rounded-md border transition-colors",
            value === opt.value
              ? "border-primary bg-primary/10 text-primary"
              : "border-border text-muted-foreground hover:border-muted-foreground/50"
          )}
        >
          {opt.label}
        </button>
      ))}
    </div>
  );
}

function TagInput({
  tags,
  onAdd,
  onRemove,
  placeholder,
  variant = "default",
}: {
  tags: string[];
  onAdd: (tag: string) => void;
  onRemove: (tag: string) => void;
  placeholder?: string;
  variant?: "default" | "destructive";
}) {
  const [input, setInput] = useState("");

  function commit() {
    const val = input.trim();
    if (val && !tags.includes(val)) onAdd(val);
    setInput("");
  }

  return (
    <div className="space-y-1.5">
      <Input
        value={input}
        onChange={(e) => setInput(e.target.value)}
        onKeyDown={(e) => {
          if (e.key === "Enter" || e.key === ",") {
            e.preventDefault();
            commit();
          }
        }}
        placeholder={placeholder ?? "Type and press Enter"}
        className="font-mono text-xs h-8"
      />
      {tags.length > 0 && (
        <div className="flex flex-wrap gap-1.5">
          {tags.map((t) => (
            <span
              key={t}
              className={cn(
                "inline-flex items-center gap-1 text-[11px] font-mono px-2 py-0.5 rounded-md",
                variant === "destructive"
                  ? "bg-destructive/10 text-destructive"
                  : "bg-muted text-foreground"
              )}
            >
              {t.length > 14 ? `${t.slice(0, 12)}…` : t}
              <button
                type="button"
                onClick={() => onRemove(t)}
                className="text-muted-foreground hover:text-destructive leading-none"
              >
                ×
              </button>
            </span>
          ))}
        </div>
      )}
    </div>
  );
}

// ─── Nav definition ───────────────────────────────────────────────────────────

type SectionId =
  | "node"
  | "storage"
  | "indexer"
  | "overlays"
  | "overlay-bap"
  | "overlay-opns"
  | "overlay-bsv21"
  | "overlay-bsocial"
  | "overlay-ordlock"
  | "faucet"
  | "sync"
  | "auth"
  | "tuning"
  | "logs";

// ─── Content panels ───────────────────────────────────────────────────────────

interface FaucetPanelProps {
  enabled: boolean;
  onToggle: (v: boolean) => void;
}

function FaucetPanel({ enabled, onToggle }: FaucetPanelProps) {
  return (
    <div className="space-y-4">
      <PageHeader title="Faucet" description="On-chain faucet service for funding transactions, minting inscriptions, and pushing data." />
      <SectionCard>
        <div className="flex items-center justify-between">
          <div>
            <p className="text-sm font-medium">Enable faucet module</p>
            <p className="text-xs text-muted-foreground mt-0.5">Exposes /faucet API endpoints for creating and managing faucets.</p>
          </div>
          <Toggle enabled={enabled} onChange={onToggle} />
        </div>
      </SectionCard>
      {enabled && (
        <SectionCard>
          <p className="text-sm text-muted-foreground">Faucet admin configuration coming soon. Use the Droplit frontend to manage faucets.</p>
        </SectionCard>
      )}
    </div>
  );
}

function NodePanel() {
  return (
    <div className="space-y-4">
      <PageHeader title="Node" description="Identity and runtime status of this 1Sat Stack instance." />

      <SectionCard>
        <SectionHeading>Identity</SectionHeading>
        <div className="space-y-3">
          <FieldRow label="Public key">
            <div className="font-mono text-xs text-muted-foreground bg-muted/50 rounded-md px-3 py-2 truncate">
              02a3e4b5c6d7e8f9a0b1c2d3e4f5a6b7c8d9e0f1a2b3c4d5e6f7a8b9c0d1e2f3a4
            </div>
          </FieldRow>
          <div className="grid grid-cols-2 gap-3">
            <FieldRow label="Network">
              <div className="font-mono text-xs text-foreground bg-muted/50 rounded-md px-3 py-2">mainnet</div>
            </FieldRow>
            <FieldRow label="Version">
              <div className="font-mono text-xs text-muted-foreground bg-muted/50 rounded-md px-3 py-2">v0.0.0</div>
            </FieldRow>
          </div>
        </div>
      </SectionCard>

      <SectionCard>
        <SectionHeading>Services</SectionHeading>
        <div className="space-y-2">
          {[
            "API server",
            "ORDFS content serving",
            "Chaintracks",
            "BEEF store",
            "PubSub bus",
          ].map((svc) => (
            <div key={svc} className="flex items-center gap-2.5">
              <StatusDot active={true} />
              <span className="text-xs text-foreground">{svc}</span>
            </div>
          ))}
        </div>
      </SectionCard>
    </div>
  );
}

type BeefProvider = { type: "lru"; size: string } | { type: "filesystem"; path: string } | { type: "redis"; url: string } | { type: "badger"; path: string } | { type: "junglebus" } | { type: "store" };

const BEEF_PROVIDER_LABELS: Record<string, string> = {
  lru: "LRU Cache",
  filesystem: "Filesystem",
  redis: "Redis",
  badger: "Badger",
  store: "Store",
  junglebus: "JungleBus",
};

function BeefChainEditor({ chain, onChange }: { chain: BeefProvider[]; onChange: (c: BeefProvider[]) => void }) {
  function addProvider() {
    onChange([...chain, { type: "lru", size: "100mb" }]);
  }
  function removeProvider(i: number) {
    onChange(chain.filter((_, idx) => idx !== i));
  }
  function updateProvider(i: number, p: BeefProvider) {
    const next = [...chain];
    next[i] = p;
    onChange(next);
  }
  function changeType(i: number, type: string) {
    const defaults: Record<string, BeefProvider> = {
      lru: { type: "lru", size: "100mb" },
      filesystem: { type: "filesystem", path: "beef" },
      redis: { type: "redis", url: "redis://localhost:6379/0" },
      badger: { type: "badger", path: "beef-badger" },
      store: { type: "store" },
      junglebus: { type: "junglebus" },
    };
    updateProvider(i, defaults[type] || { type: "lru", size: "100mb" });
  }

  return (
    <div className="space-y-2">
      <p className="text-[11px] text-muted-foreground">
        Providers are checked in order. First hit wins; later entries are fallbacks.
      </p>
      {chain.map((p, i) => (
        <div key={i} className="flex items-start gap-2 rounded-lg border border-border p-3 bg-background/50">
          <GripVertical className="w-3.5 h-3.5 text-muted-foreground/40 mt-2 shrink-0" />
          <div className="flex-1 space-y-2">
            <div className="flex items-center gap-2">
              <select
                value={p.type}
                onChange={(e) => changeType(i, e.target.value)}
                className="text-xs bg-background border border-border rounded px-2 py-1 text-foreground"
              >
                {Object.entries(BEEF_PROVIDER_LABELS).map(([val, label]) => (
                  <option key={val} value={val}>{label}</option>
                ))}
              </select>
              <span className="text-[10px] text-muted-foreground/60">#{i + 1}</span>
            </div>
            {p.type === "lru" && (
              <Input
                value={p.size}
                onChange={(e) => updateProvider(i, { ...p, size: e.target.value })}
                placeholder="100mb"
                className="font-mono text-xs h-7"
              />
            )}
            {p.type === "filesystem" && (
              <Input
                value={p.path}
                onChange={(e) => updateProvider(i, { ...p, path: e.target.value })}
                placeholder="beef"
                className="font-mono text-xs h-7"
              />
            )}
            {p.type === "redis" && (
              <Input
                value={p.url}
                onChange={(e) => updateProvider(i, { ...p, url: e.target.value })}
                placeholder="redis://localhost:6379/0"
                className="font-mono text-xs h-7"
              />
            )}
            {p.type === "badger" && (
              <Input
                value={p.path}
                onChange={(e) => updateProvider(i, { ...p, path: e.target.value })}
                placeholder="beef-badger"
                className="font-mono text-xs h-7"
              />
            )}
          </div>
          <button type="button" onClick={() => removeProvider(i)} className="text-muted-foreground hover:text-destructive mt-1.5">
            <Trash2 className="w-3.5 h-3.5" />
          </button>
        </div>
      ))}
      <button
        type="button"
        onClick={addProvider}
        className="flex items-center gap-1.5 text-xs text-muted-foreground hover:text-foreground transition-colors"
      >
        <Plus className="w-3.5 h-3.5" />
        Add provider
      </button>
    </div>
  );
}

interface StoragePanelProps {
  storeProvider: "badger" | "redis";
  setStoreProvider: (v: "badger" | "redis") => void;
  storePath: string;
  setStorePath: (v: string) => void;
  beefChain: BeefProvider[];
  setBeefChain: (v: BeefProvider[]) => void;
  pubsubProvider: "channels" | "redis";
  setPubsubProvider: (v: "channels" | "redis") => void;
  pubsubBuffer: string;
  setPubsubBuffer: (v: string) => void;
  pubsubRedisUrl: string;
  setPubsubRedisUrl: (v: string) => void;
  ordfsLruSize: string;
  setOrdfsLruSize: React.Dispatch<React.SetStateAction<string>>;
  ordfsRedisUrl: string;
  setOrdfsRedisUrl: React.Dispatch<React.SetStateAction<string>>;
  ordfsRedisTtl: string;
  setOrdfsRedisTtl: React.Dispatch<React.SetStateAction<string>>;
  chaintracksPath: string;
  setChaintracksPath: (v: string) => void;
  arcadeDb: string;
  setArcadeDb: (v: string) => void;
  arcadeBroadcastUrls: string[];
  setArcadeBroadcastUrls: React.Dispatch<React.SetStateAction<string[]>>;
  arcadeDatahubUrls: string[];
  setArcadeDatahubUrls: React.Dispatch<React.SetStateAction<string[]>>;
  arcadeAuthToken: string;
  setArcadeAuthToken: (v: string) => void;
  arcadeTimeout: string;
  setArcadeTimeout: (v: string) => void;
  arcadeLogLevel: string;
  setArcadeLogLevel: (v: string) => void;
}

function StoragePanel({
  storeProvider, setStoreProvider,
  storePath, setStorePath,
  beefChain, setBeefChain,
  pubsubProvider, setPubsubProvider,
  pubsubBuffer, setPubsubBuffer,
  pubsubRedisUrl, setPubsubRedisUrl,
  ordfsLruSize, setOrdfsLruSize,
  ordfsRedisUrl, setOrdfsRedisUrl,
  ordfsRedisTtl, setOrdfsRedisTtl,
  chaintracksPath, setChaintracksPath,
  arcadeDb, setArcadeDb,
  arcadeBroadcastUrls, setArcadeBroadcastUrls,
  arcadeDatahubUrls, setArcadeDatahubUrls,
  arcadeAuthToken, setArcadeAuthToken,
  arcadeTimeout, setArcadeTimeout,
  arcadeLogLevel, setArcadeLogLevel,
}: StoragePanelProps) {
  return (
    <div className="space-y-4">
      <PageHeader title="Storage" description="Infrastructure service configuration. Most changes require a restart." />

      <SectionCard>
        <SectionHeading>Store</SectionHeading>
        <FieldRow label="Provider" badge={<RestartBadge />}>
          <SegmentedControl
            options={[{ value: "badger", label: "Badger" }, { value: "redis", label: "Redis" }]}
            value={storeProvider}
            onChange={setStoreProvider}
          />
        </FieldRow>
        <FieldRow label={storeProvider === "badger" ? "Path" : "Connection string"} badge={<RestartBadge />}>
          <Input
            value={storePath}
            onChange={(e) => setStorePath(e.target.value)}
            className="font-mono text-xs h-8"
            placeholder={storeProvider === "badger" ? "store" : "redis://localhost:6379/0"}
          />
        </FieldRow>
      </SectionCard>

      <SectionCard>
        <SectionHeading>BEEF</SectionHeading>
        <p className="text-[11px] text-muted-foreground">Transaction storage with SPV proofs. Providers form a lookup chain.</p>
        <BeefChainEditor chain={beefChain} onChange={setBeefChain} />
      </SectionCard>

      <SectionCard>
        <SectionHeading>PubSub</SectionHeading>
        <FieldRow label="Provider" badge={<RestartBadge />}>
          <SegmentedControl
            options={[{ value: "channels", label: "In-memory" }, { value: "redis", label: "Redis" }]}
            value={pubsubProvider}
            onChange={setPubsubProvider}
          />
        </FieldRow>
        {pubsubProvider === "channels" ? (
          <FieldRow label="Buffer size" badge={<RestartBadge />}>
            <Input value={pubsubBuffer} onChange={(e) => setPubsubBuffer(e.target.value)} className="font-mono text-xs h-8 max-w-[120px]" />
          </FieldRow>
        ) : (
          <FieldRow label="Redis connection string" badge={<RestartBadge />}>
            <Input value={pubsubRedisUrl} onChange={(e) => setPubsubRedisUrl(e.target.value)} placeholder="redis://localhost:6379/0" className="font-mono text-xs h-8" />
          </FieldRow>
        )}
      </SectionCard>

      <SectionCard>
        <SectionHeading>ORDFS</SectionHeading>
        <p className="text-[11px] text-muted-foreground">Content serving with persistent origin chain store (Badger) and configurable metadata cache.</p>
        <FieldRow label="LRU cache size" hint="Number of parsed output entries to keep in memory." badge={<RestartBadge />}>
          <Input value={ordfsLruSize} onChange={(e) => setOrdfsLruSize(e.target.value)} placeholder="10000" className="font-mono text-xs h-8 max-w-[120px]" />
        </FieldRow>
        <FieldRow label="Redis cache URL" hint="Optional. Adds a Redis layer behind the LRU cache. Leave blank to disable." badge={<RestartBadge />}>
          <Input value={ordfsRedisUrl} onChange={(e) => setOrdfsRedisUrl(e.target.value)} placeholder="redis://localhost:6379/1" className="font-mono text-xs h-8" />
        </FieldRow>
        {ordfsRedisUrl && (
          <FieldRow label="Redis TTL" hint="Cache expiration (e.g., 24h, 720h). Empty = no expiration." badge={<RestartBadge />}>
            <Input value={ordfsRedisTtl} onChange={(e) => setOrdfsRedisTtl(e.target.value)} placeholder="720h" className="font-mono text-xs h-8 max-w-[120px]" />
          </FieldRow>
        )}
      </SectionCard>

      <SectionCard>
        <SectionHeading>Databases</SectionHeading>
        <p className="text-[11px] text-muted-foreground">Set during initial setup. Restart required after any change.</p>
        <FieldRow label="Chaintracks" badge={<RestartBadge />}>
          <Input value={chaintracksPath} onChange={(e) => setChaintracksPath(e.target.value)} className="font-mono text-xs h-8" />
        </FieldRow>
        <FieldRow label="Arcade" badge={<RestartBadge />}>
          <Input value={arcadeDb} onChange={(e) => setArcadeDb(e.target.value)} className="font-mono text-xs h-8" />
        </FieldRow>
      </SectionCard>

      <SectionCard>
        <SectionHeading>Arcade / Teranode</SectionHeading>
        <p className="text-[11px] text-muted-foreground">Broadcast and datahub endpoints for Teranode transaction submission.</p>
        <FieldRow label="Broadcast URLs" hint="One URL per line. Used for submitting transactions." badge={<RestartBadge />}>
          <textarea
            value={arcadeBroadcastUrls.join("\n")}
            onChange={(e) => setArcadeBroadcastUrls(e.target.value.split("\n").map(u => u.trim()))}
            onBlur={(e) => setArcadeBroadcastUrls(e.target.value.split("\n").map(u => u.trim()).filter(Boolean))}
            placeholder="http://mainnet.gorillanode.io:8833"
            className="w-full font-mono text-xs rounded-md border border-input bg-transparent px-3 py-2 min-h-[60px] resize-y"
            rows={Math.max(2, arcadeBroadcastUrls.length + 1)}
          />
        </FieldRow>
        <FieldRow label="DataHub URLs" hint="One URL per line. Used for fetching block/subtree data." badge={<RestartBadge />}>
          <textarea
            value={arcadeDatahubUrls.join("\n")}
            onChange={(e) => setArcadeDatahubUrls(e.target.value.split("\n").map(u => u.trim()))}
            onBlur={(e) => setArcadeDatahubUrls(e.target.value.split("\n").map(u => u.trim()).filter(Boolean))}
            placeholder="https://mainnet.gorillanode.io/api/v1"
            className="w-full font-mono text-xs rounded-md border border-input bg-transparent px-3 py-2 min-h-[60px] resize-y"
            rows={Math.max(2, arcadeDatahubUrls.length + 1)}
          />
        </FieldRow>
        <FieldRow label="Auth token" hint="Optional bearer token for authenticated Teranode access." badge={<RestartBadge />}>
          <Input
            type="password"
            value={arcadeAuthToken}
            onChange={(e) => setArcadeAuthToken(e.target.value)}
            placeholder="(none)"
            className="font-mono text-xs h-8"
          />
        </FieldRow>
        <FieldRow label="Timeout (seconds)" hint="Broadcast request timeout." badge={<RestartBadge />}>
          <Input
            type="number"
            value={arcadeTimeout}
            onChange={(e) => setArcadeTimeout(e.target.value)}
            placeholder="30"
            className="font-mono text-xs h-8 max-w-[120px]"
          />
        </FieldRow>
        <FieldRow label="Log level" hint="Set to debug to see broadcast details." badge={<RestartBadge />}>
          <SegmentedControl
            value={arcadeLogLevel}
            onChange={setArcadeLogLevel}
            options={[
              { label: "Error", value: "error" },
              { label: "Warn", value: "warn" },
              { label: "Info", value: "info" },
              { label: "Debug", value: "debug" },
            ]}
          />
        </FieldRow>
      </SectionCard>
    </div>
  );
}

const PARSE_TAGS = [
  { id: "p2pkh", label: "P2PKH", description: "Standard pay-to-public-key-hash outputs" },
  { id: "insc", label: "Inscriptions", description: "1Sat Ordinal inscriptions" },
  { id: "bsv21", label: "BSV21", description: "BSV21 fungible token protocol" },
  { id: "bap", label: "BAP", description: "Bitcoin Attestation Protocol identities" },
  { id: "bsocial", label: "BSocial", description: "Social protocol (posts, likes, follows)" },
  { id: "opns", label: "OPNS", description: "Ordinal Public Name System" },
  { id: "ordlock", label: "OrdLock", description: "Ordinal listing/locking protocol" },
  { id: "map", label: "MAP", description: "Magic Attribute Protocol metadata" },
  { id: "sigma", label: "Sigma", description: "Sigma signature protocol" },
  { id: "origin", label: "Origin", description: "Ordinal origin resolution and metadata via ORDFS" },
];

interface IndexerPanelProps {
  activeTags: string[];
  setActiveTags: React.Dispatch<React.SetStateAction<string[]>>;
  verbose: boolean;
  setVerbose: (v: boolean) => void;
  logLevel: string;
  setLogLevel: (v: string) => void;
}

function IndexerPanel({ activeTags, setActiveTags, verbose, setVerbose, logLevel, setLogLevel }: IndexerPanelProps) {

  function toggleTag(id: string) {
    setActiveTags((prev) =>
      prev.includes(id) ? prev.filter((t) => t !== id) : [...prev, id]
    );
  }

  return (
    <div className="space-y-4">
      <PageHeader title="Indexer" description="Controls which transaction parsers are active. Determines what data is extracted from transactions." />

      <SectionCard>
        <SectionHeading>Active Parsers</SectionHeading>
        <p className="text-[11px] text-muted-foreground">
          Select which protocols to parse from incoming transactions.
        </p>
        <div className="space-y-1 pt-1">
          {PARSE_TAGS.map((tag) => {
            const active = activeTags.includes(tag.id);
            return (
              <button
                key={tag.id}
                type="button"
                onClick={() => toggleTag(tag.id)}
                className={cn(
                  "w-full flex items-center gap-3 rounded-lg px-3 py-2.5 text-left transition-colors",
                  active ? "bg-primary/8 border border-primary/20" : "border border-transparent hover:bg-muted/40"
                )}
              >
                <div className={cn(
                  "w-4 h-4 rounded border-2 flex items-center justify-center shrink-0 transition-colors",
                  active ? "border-primary bg-primary" : "border-muted-foreground/40"
                )}>
                  {active && <span className="text-white text-[10px] font-bold">✓</span>}
                </div>
                <div className="flex-1 min-w-0">
                  <div className="text-sm font-medium text-foreground">{tag.label}</div>
                  <div className="text-[11px] text-muted-foreground">{tag.description}</div>
                </div>
              </button>
            );
          })}
        </div>
      </SectionCard>

      <SectionCard>
        <SectionHeading>Logging</SectionHeading>
        <div className="flex items-center justify-between">
          <span className="text-xs font-medium text-muted-foreground">Verbose logging</span>
          <Toggle enabled={verbose} onChange={setVerbose} />
        </div>
        <FieldRow label="Log level">
          <SegmentedControl
            options={[
              { value: "debug", label: "Debug" },
              { value: "info", label: "Info" },
              { value: "warn", label: "Warn" },
              { value: "error", label: "Error" },
            ]}
            value={logLevel}
            onChange={setLogLevel}
          />
        </FieldRow>
      </SectionCard>
    </div>
  );
}

function OverlayToggleHeader({
  title,
  description,
  enabled,
  onToggle,
}: {
  title: string;
  description: string;
  enabled: boolean;
  onToggle: (v: boolean) => void;
}) {
  return (
    <div className="flex items-center justify-between">
      <div className="flex items-center gap-3">
        <StatusDot active={enabled} />
        <div>
          <div className="text-sm font-semibold text-foreground">{title}</div>
          <div className="text-xs text-muted-foreground mt-0.5">{description}</div>
        </div>
      </div>
      <Toggle enabled={enabled} onChange={onToggle} />
    </div>
  );
}

function MetricsRow({ metrics }: { metrics: { label: string; value: string | number }[] }) {
  return (
    <div className="flex gap-6 py-1">
      {metrics.map((m) => (
        <div key={m.label}>
          <div className="text-[10px] text-muted-foreground uppercase tracking-wide">{m.label}</div>
          <div className="text-sm font-mono font-medium text-foreground">{m.value}</div>
        </div>
      ))}
    </div>
  );
}

interface OverlayPanelProps {
  enabled: boolean;
  onToggle: (v: boolean) => void;
  subId: string;
  setSubId: (v: string) => void;
  concurrency: string;
  setConcurrency: (v: string) => void;
  batchSize: string;
  setBatchSize: (v: string) => void;
}

function BapPanel({
  enabled, onToggle,
  subId, setSubId,
  concurrency, setConcurrency,
  batchSize, setBatchSize,
}: OverlayPanelProps) {
  return (
    <div className="space-y-4">
      <PageHeader title="BAP" description="Bitcoin Attestation Protocol — identity and attestation indexing." />

      <SectionCard>
        <OverlayToggleHeader
          title="BAP overlay"
          description="Indexes identity and attestation transactions."
          enabled={enabled}
          onToggle={onToggle}
        />
        <MetricsRow metrics={[{ label: "queue depth", value: "0" }, { label: "indexed", value: "—" }]} />
      </SectionCard>

      <SectionCard>
        <SectionHeading>Configuration</SectionHeading>
        <FieldRow label="JungleBus subscription ID">
          <Input value={subId} onChange={(e) => setSubId(e.target.value)} placeholder="sub_..." className="font-mono text-xs h-8" />
        </FieldRow>
        <div className="grid grid-cols-2 gap-3">
          <FieldRow label="Concurrency">
            <Input value={concurrency} onChange={(e) => setConcurrency(e.target.value)} className="font-mono text-xs h-8" />
          </FieldRow>
          <FieldRow label="Batch size">
            <Input value={batchSize} onChange={(e) => setBatchSize(e.target.value)} className="font-mono text-xs h-8" />
          </FieldRow>
        </div>
      </SectionCard>
    </div>
  );
}

interface OpnsPanelProps extends OverlayPanelProps {
  paymailEnabled: boolean;
  setPaymailEnabled: (v: boolean) => void;
}

function OpnsPanel({
  enabled, onToggle,
  subId, setSubId,
  concurrency, setConcurrency,
  batchSize, setBatchSize,
  paymailEnabled, setPaymailEnabled,
}: OpnsPanelProps) {
  const [crawling, setCrawling] = useState(false);
  const [crawlError, setCrawlError] = useState<string | null>(null);
  const [crawlSuccess, setCrawlSuccess] = useState(false);

  async function handleStartCrawl() {
    setCrawling(true);
    setCrawlError(null);
    setCrawlSuccess(false);
    try {
      const res = await apiFetch("/opns/crawl", { method: "POST" });
      if (!res.ok) {
        const data = await res.json().catch(() => ({}));
        throw new Error(data.error || "Failed to start crawl");
      }
      setCrawlSuccess(true);
    } catch (e: any) {
      setCrawlError(e.message);
    } finally {
      setCrawling(false);
    }
  }

  return (
    <div className="space-y-4">
      <PageHeader title="OPNS" description="Ordinal Public Name System — name resolution on BSV." />

      <SectionCard>
        <OverlayToggleHeader
          title="OPNS overlay"
          description="Indexes name registrations and resolutions."
          enabled={enabled}
          onToggle={onToggle}
        />
        <MetricsRow metrics={[{ label: "indexed", value: "—" }]} />
      </SectionCard>

      <SectionCard>
        <SectionHeading>Configuration</SectionHeading>
        <FieldRow label="JungleBus subscription ID">
          <Input value={subId} onChange={(e) => setSubId(e.target.value)} placeholder="sub_..." className="font-mono text-xs h-8" />
        </FieldRow>
        <div className="grid grid-cols-2 gap-3">
          <FieldRow label="Concurrency">
            <Input value={concurrency} onChange={(e) => setConcurrency(e.target.value)} className="font-mono text-xs h-8" />
          </FieldRow>
          <FieldRow label="Batch size">
            <Input value={batchSize} onChange={(e) => setBatchSize(e.target.value)} className="font-mono text-xs h-8" />
          </FieldRow>
        </div>
      </SectionCard>

      <SectionCard>
        <SectionHeading>Genesis Crawl</SectionHeading>
        <p className="text-[11px] text-muted-foreground">
          Index all existing OPNS name registrations from the blockchain. Only needed once on first setup.
        </p>
        <Button
          variant="secondary"
          onClick={handleStartCrawl}
          disabled={crawling}
        >
          {crawling ? "Crawling..." : "Start Genesis Crawl"}
        </Button>
        {crawlError && <p className="text-xs text-destructive">{crawlError}</p>}
        {crawlSuccess && <p className="text-xs text-success">Crawl started successfully</p>}
      </SectionCard>

      <SectionCard>
        <div className="flex items-center justify-between">
          <div>
            <span className="text-sm font-medium text-foreground">Paymail</span>
            <p className="text-xs text-muted-foreground mt-0.5">
              {enabled
                ? "BSV Paymail protocol — requires OPNS for name resolution."
                : "Enable OPNS first to use Paymail."}
            </p>
          </div>
          <Toggle enabled={paymailEnabled} onChange={setPaymailEnabled} disabled={!enabled} />
        </div>
      </SectionCard>
    </div>
  );
}

interface Bsv21PanelProps extends OverlayPanelProps {
  tokenWorkers: string;
  setTokenWorkers: (v: string) => void;
  whitelist: string[];
  setWhitelist: React.Dispatch<React.SetStateAction<string[]>>;
  blacklist: string[];
  setBlacklist: React.Dispatch<React.SetStateAction<string[]>>;
}

function Bsv21Panel({
  enabled, onToggle,
  subId, setSubId,
  concurrency, setConcurrency,
  tokenWorkers, setTokenWorkers,
  batchSize, setBatchSize,
  whitelist, setWhitelist,
  blacklist, setBlacklist,
}: Bsv21PanelProps) {
  return (
    <div className="space-y-4">
      <PageHeader title="BSV21" description="Fungible token protocol — mint, transfer, and index BSV21 tokens." />

      <SectionCard>
        <OverlayToggleHeader
          title="BSV21 overlay"
          description="Indexes fungible token transactions."
          enabled={enabled}
          onToggle={onToggle}
        />
        <MetricsRow metrics={[{ label: "queue depth", value: "0" }, { label: "workers", value: "0" }]} />
      </SectionCard>

      <SectionCard>
        <SectionHeading>Configuration</SectionHeading>
        <FieldRow label="JungleBus subscription ID">
          <Input value={subId} onChange={(e) => setSubId(e.target.value)} placeholder="sub_..." className="font-mono text-xs h-8" />
        </FieldRow>
        <div className="grid grid-cols-3 gap-3">
          <FieldRow label="Dispatch workers">
            <Input value={concurrency} onChange={(e) => setConcurrency(e.target.value)} className="font-mono text-xs h-8" />
          </FieldRow>
          <FieldRow label="Token workers">
            <Input value={tokenWorkers} onChange={(e) => setTokenWorkers(e.target.value)} className="font-mono text-xs h-8" />
          </FieldRow>
          <FieldRow label="Batch size">
            <Input value={batchSize} onChange={(e) => setBatchSize(e.target.value)} className="font-mono text-xs h-8" />
          </FieldRow>
        </div>
      </SectionCard>

      <SectionCard>
        <SectionHeading>Token filters</SectionHeading>
        <FieldRow label="Whitelist" hint="Only these token IDs will be indexed. Empty = all tokens.">
          <TagInput
            tags={whitelist}
            onAdd={(t) => setWhitelist((prev) => [...prev, t])}
            onRemove={(t) => setWhitelist((prev) => prev.filter((x) => x !== t))}
            placeholder="Token ID and press Enter"
          />
        </FieldRow>
        <FieldRow label="Blacklist" hint="These token IDs will be excluded.">
          <TagInput
            tags={blacklist}
            onAdd={(t) => setBlacklist((prev) => [...prev, t])}
            onRemove={(t) => setBlacklist((prev) => prev.filter((x) => x !== t))}
            placeholder="Token ID and press Enter"
            variant="destructive"
          />
        </FieldRow>
      </SectionCard>
    </div>
  );
}

interface BsocialPanelProps extends OverlayPanelProps {
  mongoUrl: string;
  setMongoUrl: (v: string) => void;
}

function BsocialPanel({
  enabled, onToggle,
  subId, setSubId,
  concurrency, setConcurrency,
  batchSize, setBatchSize,
  mongoUrl, setMongoUrl,
}: BsocialPanelProps) {
  return (
    <div className="space-y-4">
      <PageHeader title="BSocial" description="Social protocol — posts, likes, and follows on BSV." />

      <SectionCard>
        <OverlayToggleHeader
          title="BSocial overlay"
          description="Indexes social interaction transactions."
          enabled={enabled}
          onToggle={onToggle}
        />
        <MetricsRow metrics={[{ label: "indexed", value: "—" }]} />
      </SectionCard>

      <SectionCard>
        <SectionHeading>Configuration</SectionHeading>
        <FieldRow label="JungleBus subscription ID">
          <Input value={subId} onChange={(e) => setSubId(e.target.value)} placeholder="sub_..." className="font-mono text-xs h-8" />
        </FieldRow>
        <div className="grid grid-cols-2 gap-3">
          <FieldRow label="Concurrency">
            <Input value={concurrency} onChange={(e) => setConcurrency(e.target.value)} className="font-mono text-xs h-8" />
          </FieldRow>
          <FieldRow label="Batch size">
            <Input value={batchSize} onChange={(e) => setBatchSize(e.target.value)} className="font-mono text-xs h-8" />
          </FieldRow>
        </div>
        <FieldRow
          label="MongoDB URL"
          badge={
            enabled && !mongoUrl ? (
              <span className="text-[10px] text-warning font-medium">required</span>
            ) : undefined
          }
        >
          <Input
            value={mongoUrl}
            onChange={(e) => setMongoUrl(e.target.value)}
            placeholder="mongodb://localhost:27017/bsocial"
            className="font-mono text-xs h-8"
          />
          {enabled && !mongoUrl && (
            <p className="text-[11px] text-warning/80 flex items-center gap-1">
              <AlertTriangle className="w-3 h-3" />
              BSocial requires a MongoDB connection.
            </p>
          )}
        </FieldRow>
      </SectionCard>
    </div>
  );
}

function OrdlockPanel({
  enabled, onToggle,
  subId, setSubId,
  concurrency, setConcurrency,
  batchSize, setBatchSize,
}: OverlayPanelProps) {
  return (
    <div className="space-y-4">
      <PageHeader title="OrdLock" description="Ordinal lock listings — tracks inscribed satoshi market listings." />

      <SectionCard>
        <OverlayToggleHeader
          title="OrdLock overlay"
          description="Indexes lock/unlock transactions for ordinal listings."
          enabled={enabled}
          onToggle={onToggle}
        />
        <MetricsRow metrics={[{ label: "listings", value: "—" }]} />
      </SectionCard>

      <SectionCard>
        <SectionHeading>Configuration</SectionHeading>
        <FieldRow label="JungleBus subscription ID">
          <Input value={subId} onChange={(e) => setSubId(e.target.value)} placeholder="sub_..." className="font-mono text-xs h-8" />
        </FieldRow>
        <div className="grid grid-cols-2 gap-3">
          <FieldRow label="Concurrency">
            <Input value={concurrency} onChange={(e) => setConcurrency(e.target.value)} className="font-mono text-xs h-8" />
          </FieldRow>
          <FieldRow label="Batch size">
            <Input value={batchSize} onChange={(e) => setBatchSize(e.target.value)} className="font-mono text-xs h-8" />
          </FieldRow>
        </div>
      </SectionCard>
    </div>
  );
}

interface OverlayEnginePanelProps {
  engineStorage: "sqlite" | "postgres";
  setEngineStorage: (v: "sqlite" | "postgres") => void;
  engineStoragePath: string;
  setEngineStoragePath: (v: string) => void;
  p2pEnabled: boolean;
  setP2pEnabled: (v: boolean) => void;
  p2pPort: string;
  setP2pPort: (v: string) => void;
  p2pDhtMode: string;
  setP2pDhtMode: (v: string) => void;
  bootstrapPeers: string;
  setBootstrapPeers: (v: string) => void;
}

function OverlayEnginePanel({
  engineStorage, setEngineStorage,
  engineStoragePath, setEngineStoragePath,
  p2pEnabled, setP2pEnabled,
  p2pPort, setP2pPort,
  p2pDhtMode, setP2pDhtMode,
  bootstrapPeers, setBootstrapPeers,
}: OverlayEnginePanelProps) {
  return (
    <div className="space-y-4">
      <PageHeader
        title="Overlay Engine"
        description="Core engine configuration. Auto-enabled when any overlay is active."
      />

      <SectionCard>
        <SectionHeading>Storage</SectionHeading>
        <FieldRow label="Backend" badge={<RestartBadge />}>
          <SegmentedControl
            options={[{ value: "sqlite", label: "SQLite" }, { value: "postgres", label: "Postgres" }]}
            value={engineStorage}
            onChange={setEngineStorage}
          />
        </FieldRow>
        <FieldRow label="Path / connection string" badge={<RestartBadge />}>
          <Input
            value={engineStoragePath}
            onChange={(e) => setEngineStoragePath(e.target.value)}
            className="font-mono text-xs h-8"
            placeholder={engineStorage === "sqlite" ? "overlay" : "postgres://..."}
          />
        </FieldRow>
      </SectionCard>

      <SectionCard>
        <div className="flex items-center justify-between">
          <div>
            <SectionHeading>P2P</SectionHeading>
            <p className="text-xs text-muted-foreground mt-0.5">Peer-to-peer overlay sync via libp2p.</p>
          </div>
          <Toggle enabled={p2pEnabled} onChange={setP2pEnabled} />
        </div>

        {p2pEnabled && (
          <div className="space-y-3 pt-1">
            <div className="grid grid-cols-2 gap-3">
              <FieldRow label="Port">
                <Input value={p2pPort} onChange={(e) => setP2pPort(e.target.value)} className="font-mono text-xs h-8" />
              </FieldRow>
              <FieldRow label="DHT mode">
                <Input value={p2pDhtMode} onChange={(e) => setP2pDhtMode(e.target.value)} className="font-mono text-xs h-8" placeholder="auto / server / client" />
              </FieldRow>
            </div>
            <FieldRow label="Bootstrap peers" hint="One peer multiaddr per line.">
              <textarea
                value={bootstrapPeers}
                onChange={(e) => setBootstrapPeers(e.target.value)}
                rows={3}
                placeholder="/ip4/1.2.3.4/tcp/9000/p2p/QmXxx..."
                className="w-full rounded-md border border-input bg-input px-3 py-2 text-xs font-mono resize-none focus:outline-none focus:ring-1 focus:ring-ring"
              />
            </FieldRow>
          </div>
        )}
      </SectionCard>
    </div>
  );
}

interface SyncPanelProps {
  jbUrl: string;
  setJbUrl: (v: string) => void;
  jbToken: string;
  setJbToken: (v: string) => void;
  indexerSubIds: string;
  setIndexerSubIds: (v: string) => void;
  indexerConcurrency: string;
  setIndexerConcurrency: (v: string) => void;
  indexerBatchSize: string;
  setIndexerBatchSize: (v: string) => void;
  indexerSyncEnabled: boolean;
  setIndexerSyncEnabled: (v: boolean) => void;
  indexerMempool: boolean;
  setIndexerMempool: (v: boolean) => void;
  ownerSync: boolean;
  setOwnerSync: (v: boolean) => void;
}

function SyncPanel({
  jbUrl, setJbUrl,
  jbToken, setJbToken,
  indexerSubIds, setIndexerSubIds,
  indexerConcurrency, setIndexerConcurrency,
  indexerBatchSize, setIndexerBatchSize,
  indexerSyncEnabled, setIndexerSyncEnabled,
  indexerMempool, setIndexerMempool,
  ownerSync, setOwnerSync,
}: SyncPanelProps) {
  return (
    <div className="space-y-4">
      <PageHeader title="Sync" description="JungleBus connection and indexer subscription configuration." />

      <SectionCard>
        <SectionHeading>JungleBus</SectionHeading>
        <FieldRow label="URL">
          <Input value={jbUrl} onChange={(e) => setJbUrl(e.target.value)} className="font-mono text-xs h-8" />
        </FieldRow>
        <FieldRow label="Token" hint="Leave blank for unauthenticated access.">
          <Input type="password" value={jbToken} onChange={(e) => setJbToken(e.target.value)} className="font-mono text-xs h-8" />
        </FieldRow>
      </SectionCard>

      <SectionCard>
        <SectionHeading>Indexer</SectionHeading>
        <FieldRow label="Subscription IDs" hint="One per line.">
          <textarea
            value={indexerSubIds}
            onChange={(e) => setIndexerSubIds(e.target.value)}
            rows={3}
            placeholder={"sub_abc123\nsub_def456"}
            className="w-full rounded-md border border-input bg-input px-3 py-2 text-xs font-mono resize-none focus:outline-none focus:ring-1 focus:ring-ring"
          />
        </FieldRow>
        <div className="grid grid-cols-2 gap-3">
          <FieldRow label="Concurrency">
            <Input value={indexerConcurrency} onChange={(e) => setIndexerConcurrency(e.target.value)} className="font-mono text-xs h-8" />
          </FieldRow>
          <FieldRow label="Batch size">
            <Input value={indexerBatchSize} onChange={(e) => setIndexerBatchSize(e.target.value)} className="font-mono text-xs h-8" />
          </FieldRow>
        </div>
        <div className="flex items-center justify-between pt-1">
          <div>
            <div className="text-xs font-medium text-muted-foreground">Enabled</div>
            <div className="text-[11px] text-muted-foreground/70">Process queued transactions from JungleBus.</div>
          </div>
          <Toggle enabled={indexerSyncEnabled} onChange={setIndexerSyncEnabled} />
        </div>
        <div className="flex items-center justify-between pt-1">
          <div>
            <div className="text-xs font-medium text-muted-foreground">Mempool</div>
            <div className="text-[11px] text-muted-foreground/70">Subscribe to unconfirmed transactions.</div>
          </div>
          <Toggle enabled={indexerMempool} onChange={setIndexerMempool} />
        </div>
      </SectionCard>

      <SectionCard>
        <div className="flex items-center justify-between">
          <div>
            <SectionHeading>Owner sync</SectionHeading>
            <p className="text-xs text-muted-foreground mt-0.5">Track owned addresses via JungleBus. Requires JungleBus.</p>
          </div>
          <Toggle enabled={ownerSync} onChange={setOwnerSync} />
        </div>
      </SectionCard>
    </div>
  );
}

interface AuthPanelProps {
  authMode: "local" | "authenticated";
  setAuthMode: (v: "local" | "authenticated") => void;
  apiKey: string;
  setApiKey: (v: string) => void;
  sessionTTL: string;
  setSessionTTL: (v: string) => void;
}

function AuthPanel({ authMode, setAuthMode, apiKey, setApiKey, sessionTTL, setSessionTTL }: AuthPanelProps) {
  const [showApiKey, setShowApiKey] = useState(false);

  return (
    <div className="space-y-4">
      <PageHeader title="Auth" description="Admin access control and API authentication." />

      <SectionCard>
        <FieldRow label="Auth mode" badge={<RestartBadge />}>
          <SegmentedControl
            options={[{ value: "local", label: "Local" }, { value: "authenticated", label: "Authenticated" }]}
            value={authMode}
            onChange={setAuthMode}
          />
          <p className="text-[11px] text-muted-foreground/70 mt-1">
            {authMode === "local"
              ? "No wallet required. Admin access is unrestricted on localhost."
              : "Wallet-based authentication required for all admin operations."}
          </p>
        </FieldRow>

        <FieldRow label="API key" hint="Used with the X-Api-Key header for programmatic access.">
          <div className="flex gap-2">
            <Input
              type={showApiKey ? "text" : "password"}
              value={apiKey}
              onChange={(e) => setApiKey(e.target.value)}
              placeholder="Generate or paste key"
              className="font-mono text-xs h-8 flex-1"
            />
            <button
              type="button"
              onClick={() => setShowApiKey((v) => !v)}
              className="px-2 text-muted-foreground hover:text-foreground transition-colors"
              aria-label={showApiKey ? "Hide API key" : "Show API key"}
            >
              {showApiKey ? <EyeOff className="w-4 h-4" /> : <Eye className="w-4 h-4" />}
            </button>
          </div>
        </FieldRow>

        <FieldRow label="Session TTL" badge={<HotBadge />}>
          <Input
            value={sessionTTL}
            onChange={(e) => setSessionTTL(e.target.value)}
            placeholder="24h"
            className="font-mono text-xs h-8 max-w-[120px]"
          />
        </FieldRow>
      </SectionCard>
    </div>
  );
}


interface TuningPanelProps {
  defaultConcurrency: string;
  setDefaultConcurrency: (v: string) => void;
  pageSize: string;
  setPageSize: (v: string) => void;
  pollDelay: string;
  setPollDelay: (v: string) => void;
}

function TuningPanel({ defaultConcurrency, setDefaultConcurrency, pageSize, setPageSize, pollDelay, setPollDelay }: TuningPanelProps) {
  return (
    <div className="space-y-4">
      <PageHeader
        title="Tuning"
        description="Worker defaults. These settings are hot-reloadable — changes apply without a restart."
      />

      <SectionCard>
        <FieldRow label="Default concurrency" badge={<HotBadge />} hint="Maximum parallel workers per service.">
          <Input
            value={defaultConcurrency}
            onChange={(e) => setDefaultConcurrency(e.target.value)}
            className="font-mono text-xs h-8 max-w-[120px]"
          />
        </FieldRow>

        <FieldRow label="Page size" badge={<HotBadge />} hint="Transactions per batch fetch.">
          <Input
            value={pageSize}
            onChange={(e) => setPageSize(e.target.value)}
            className="font-mono text-xs h-8 max-w-[120px]"
          />
        </FieldRow>

        <FieldRow label="Poll delay" badge={<HotBadge />} hint="Delay between queue polls (e.g. 500ms, 1s).">
          <Input
            value={pollDelay}
            onChange={(e) => setPollDelay(e.target.value)}
            className="font-mono text-xs h-8 max-w-[120px]"
          />
        </FieldRow>
      </SectionCard>
    </div>
  );
}

// ─── Main page ────────────────────────────────────────────────────────────────

export default function SettingsPage() {
  const [activeSection, setActiveSection] = useState<SectionId>("node");
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const originalConfig = useRef<Record<string, string>>({});

  // Overlay toggles
  const [bapEnabled, setBapEnabled] = useState(false);
  const [opnsEnabled, setOpnsEnabled] = useState(false);
  const [bsv21Enabled, setBsv21Enabled] = useState(false);
  const [bsocialEnabled, setBsocialEnabled] = useState(false);
  const [ordlockEnabled, setOrdlockEnabled] = useState(false);

  // Storage
  const [storeProvider, setStoreProvider] = useState<"badger" | "redis">("badger");
  const [storePath, setStorePath] = useState("store");
  const [beefChain, setBeefChain] = useState<BeefProvider[]>([
    { type: "lru", size: "100mb" },
    { type: "filesystem", path: "beef" },
  ]);
  const [pubsubProvider, setPubsubProvider] = useState<"channels" | "redis">("channels");
  const [pubsubBuffer, setPubsubBuffer] = useState("100");
  const [pubsubRedisUrl, setPubsubRedisUrl] = useState("");
  const [ordfsLruSize, setOrdfsLruSize] = useState("10000");
  const [ordfsRedisUrl, setOrdfsRedisUrl] = useState("");
  const [ordfsRedisTtl, setOrdfsRedisTtl] = useState("");
  const [chaintracksPath, setChaintracksPath] = useState("chaintracks");
  const [arcadeDb, setArcadeDb] = useState("arcade/arcade.db");
  const [arcadeBroadcastUrls, setArcadeBroadcastUrls] = useState<string[]>(["http://mainnet.gorillanode.io:8833"]);
  const [arcadeDatahubUrls, setArcadeDatahubUrls] = useState<string[]>(["https://mainnet.gorillanode.io/api/v1"]);
  const [arcadeAuthToken, setArcadeAuthToken] = useState("");
  const [arcadeTimeout, setArcadeTimeout] = useState("30");
  const [arcadeLogLevel, setArcadeLogLevel] = useState("info");

  // Indexer
  const [activeTags, setActiveTags] = useState<string[]>(PARSE_TAGS.map((t) => t.id));
  const [verbose, setVerbose] = useState(false);
  const [logLevel, setLogLevel] = useState("info");

  // BAP overlay
  const [bapSubId, setBapSubId] = useState("");
  const [bapConcurrency, setBapConcurrency] = useState("8");
  const [bapBatchSize, setBapBatchSize] = useState("1000");

  // OPNS overlay
  const [opnsSubId, setOpnsSubId] = useState("");
  const [opnsConcurrency, setOpnsConcurrency] = useState("8");
  const [opnsBatchSize, setOpnsBatchSize] = useState("1000");
  const [paymailEnabled, setPaymailEnabled] = useState(false);

  // BSV21 overlay
  const [bsv21SubId, setBsv21SubId] = useState("");
  const [bsv21Concurrency, setBsv21Concurrency] = useState("8");
  const [bsv21TokenWorkers, setBsv21TokenWorkers] = useState("8");
  const [bsv21BatchSize, setBsv21BatchSize] = useState("1000");
  const [bsv21Whitelist, setBsv21Whitelist] = useState<string[]>([]);
  const [bsv21Blacklist, setBsv21Blacklist] = useState<string[]>([]);

  // BSocial overlay
  const [bsocialSubId, setBsocialSubId] = useState("");
  const [bsocialConcurrency, setBsocialConcurrency] = useState("8");
  const [bsocialBatchSize, setBsocialBatchSize] = useState("1000");
  const [bsocialMongoUrl, setBsocialMongoUrl] = useState("");

  // OrdLock overlay
  const [ordlockSubId, setOrdlockSubId] = useState("");
  const [ordlockConcurrency, setOrdlockConcurrency] = useState("8");
  const [ordlockBatchSize, setOrdlockBatchSize] = useState("1000");

  // Overlay engine
  const [engineStorage, setEngineStorage] = useState<"sqlite" | "postgres">("sqlite");
  const [engineStoragePath, setEngineStoragePath] = useState("overlay");
  const [p2pEnabled, setP2pEnabled] = useState(false);
  const [p2pPort, setP2pPort] = useState("9000");
  const [p2pDhtMode, setP2pDhtMode] = useState("off");
  const [bootstrapPeers, setBootstrapPeers] = useState("");

  // Sync
  const [jbUrl, setJbUrl] = useState("https://junglebus.gorillapool.io");
  const [jbToken, setJbToken] = useState("");
  const [indexerSubIds, setIndexerSubIds] = useState("");
  const [indexerConcurrency, setIndexerConcurrency] = useState("8");
  const [indexerBatchSize, setIndexerBatchSize] = useState("500");
  const [indexerSyncEnabled, setIndexerSyncEnabled] = useState(false);
  const [indexerMempool, setIndexerMempool] = useState(false);
  const [ownerSync, setOwnerSync] = useState(false);
  const [faucetEnabled, setFaucetEnabled] = useState(false);

  // Auth
  const [authMode, setAuthMode] = useState<"local" | "authenticated">("local");
  const [apiKey, setApiKey] = useState("");
  const [sessionTTL, setSessionTTL] = useState("24h");

  // Tuning
  const [defaultConcurrency, setDefaultConcurrency] = useState("4");
  const [pageSize, setPageSize] = useState("100");
  const [pollDelay, setPollDelay] = useState("500ms");

  // Load config on mount
  useEffect(() => {
    getConfig()
      .then((cfg) => {
        originalConfig.current = { ...cfg };
        const b = (key: string) => cfg[key] === "true";
        const s = (key: string, fallback: string) => cfg[key] ?? fallback;

        // Overlay toggles
        setBapEnabled(b("overlay.bap.enabled"));
        setOpnsEnabled(b("overlay.opns.enabled"));
        setBsv21Enabled(b("overlay.bsv21.enabled"));
        setBsocialEnabled(b("overlay.bsocial.enabled"));
        setOrdlockEnabled(b("overlay.ordlock.enabled"));

        // Storage
        if (cfg["store.provider"] === "badger" || cfg["store.provider"] === "redis") setStoreProvider(cfg["store.provider"]);
        setStorePath(s("store.badger.path", "store"));
        if (cfg["beef.chain"]) {
          try { setBeefChain(JSON.parse(cfg["beef.chain"])); } catch { /* keep default */ }
        }
        if (cfg["pubsub.provider"] === "channels" || cfg["pubsub.provider"] === "redis") setPubsubProvider(cfg["pubsub.provider"]);
        setPubsubBuffer(s("pubsub.channels.buffer_size", "100"));
        setPubsubRedisUrl(s("pubsub.redis.url", ""));
        setOrdfsLruSize(s("ordfs.cache.lru_size", "10000"));
        setOrdfsRedisUrl(s("ordfs.cache.redis_url", ""));
        setOrdfsRedisTtl(s("ordfs.cache.redis_ttl", ""));
        setChaintracksPath(s("chaintracks.path", "chaintracks"));
        setArcadeDb(s("arcade.path", "arcade/arcade.db"));
        if (cfg["arcade.teranode.broadcast_urls"]) {
          const urls = cfg["arcade.teranode.broadcast_urls"].split(",").filter(Boolean);
          if (urls.length > 0) setArcadeBroadcastUrls(urls);
        }
        if (cfg["arcade.teranode.datahub_urls"]) {
          const urls = cfg["arcade.teranode.datahub_urls"].split(",").filter(Boolean);
          if (urls.length > 0) setArcadeDatahubUrls(urls);
        }
        setArcadeAuthToken(s("arcade.teranode.auth_token", ""));
        setArcadeTimeout(s("arcade.teranode.timeout", "30"));
        setArcadeLogLevel(s("arcade.log_level", "info"));

        // Indexer
        if (cfg["indexer.parsers"]) {
          try { setActiveTags(JSON.parse(cfg["indexer.parsers"])); } catch { /* keep default */ }
        }
        setVerbose(b("indexer.verbose"));
        setLogLevel(s("indexer.log_level", "info"));

        // BAP
        setBapSubId(s("overlay.bap.sub_id", ""));
        setBapConcurrency(s("overlay.bap.concurrency", "8"));
        setBapBatchSize(s("overlay.bap.batch_size", "1000"));

        // OPNS
        setOpnsSubId(s("overlay.opns.sub_id", ""));
        setOpnsConcurrency(s("overlay.opns.concurrency", "8"));
        setOpnsBatchSize(s("overlay.opns.batch_size", "1000"));
        setPaymailEnabled(b("overlay.opns.paymail"));

        // BSV21
        setBsv21SubId(s("overlay.bsv21.sub_id", ""));
        setBsv21Concurrency(s("overlay.bsv21.concurrency", "8"));
        setBsv21TokenWorkers(s("overlay.bsv21.token_workers", "8"));
        setBsv21BatchSize(s("overlay.bsv21.batch_size", "1000"));
        if (cfg["overlay.bsv21.whitelist"]) {
          try { setBsv21Whitelist(JSON.parse(cfg["overlay.bsv21.whitelist"])); } catch { /* keep default */ }
        }
        if (cfg["overlay.bsv21.blacklist"]) {
          try { setBsv21Blacklist(JSON.parse(cfg["overlay.bsv21.blacklist"])); } catch { /* keep default */ }
        }

        // BSocial
        setBsocialSubId(s("overlay.bsocial.sub_id", ""));
        setBsocialConcurrency(s("overlay.bsocial.concurrency", "8"));
        setBsocialBatchSize(s("overlay.bsocial.batch_size", "1000"));
        setBsocialMongoUrl(s("overlay.bsocial.mongo_url", ""));

        // OrdLock
        setOrdlockSubId(s("overlay.ordlock.sub_id", ""));
        setOrdlockConcurrency(s("overlay.ordlock.concurrency", "8"));
        setOrdlockBatchSize(s("overlay.ordlock.batch_size", "1000"));

        // Overlay engine
        if (cfg["overlay.engine.storage"] === "sqlite" || cfg["overlay.engine.storage"] === "postgres") setEngineStorage(cfg["overlay.engine.storage"]);
        setEngineStoragePath(s("overlay.engine.storage_path", "overlay"));
        setP2pEnabled(b("overlay.engine.p2p.enabled"));
        setP2pPort(s("overlay.engine.p2p.port", "9000"));
        setP2pDhtMode(s("overlay.engine.p2p.dht_mode", "off"));
        setBootstrapPeers(s("overlay.engine.p2p.bootstrap_peers", ""));

        // Sync
        setJbUrl(s("junglebus.url", "https://junglebus.gorillapool.io"));
        setJbToken(s("junglebus.token", ""));
        setIndexerSubIds(s("indexer.sync.subscription_ids", ""));
        setIndexerConcurrency(s("indexer.sync.concurrency", "8"));
        setIndexerBatchSize(s("indexer.sync.batch_size", "500"));
        setIndexerSyncEnabled(b("indexer.sync.enabled"));
        setIndexerMempool(b("indexer.sync.mempool"));
        setOwnerSync(b("owner.enabled"));
        setFaucetEnabled(b("faucet.enabled"));

        // Auth
        if (cfg["auth.mode"] === "local" || cfg["auth.mode"] === "authenticated") setAuthMode(cfg["auth.mode"]);
        setApiKey(s("auth.api_key", ""));
        setSessionTTL(s("auth.session_ttl", "24h"));

        // Tuning
        setDefaultConcurrency(s("worker.concurrency", "4"));
        setPageSize(s("worker.page_size", "100"));
        setPollDelay(s("worker.poll_delay", "500ms"));
      })
      .catch((err) => toastError(err.message))
      .finally(() => setLoading(false));
  }, []);

  // Keys that require a server restart when changed
  const RESTART_KEYS = new Set([
    "overlay.bap.enabled", "overlay.opns.enabled", "overlay.bsv21.enabled",
    "overlay.bsocial.enabled", "overlay.ordlock.enabled",
    "owner.enabled", "faucet.enabled",
    "store.provider", "store.badger.path", "pubsub.provider",
    "auth.mode", "chaintracks.path", "arcade.path",
    "arcade.teranode.broadcast_urls", "arcade.teranode.datahub_urls",
    "arcade.teranode.auth_token", "arcade.teranode.timeout",
    "beef.chain", "ordfs.cache.lru_size", "ordfs.cache.redis_url", "ordfs.cache.redis_ttl",
    "overlay.engine.storage", "overlay.engine.storage_path",
    "overlay.engine.p2p.enabled", "overlay.engine.p2p.port",
    "overlay.engine.p2p.dht_mode", "overlay.engine.p2p.bootstrap_peers",
    "indexer.parsers",
    "overlay.bap.sub_id", "overlay.bap.concurrency", "overlay.bap.batch_size",
    "overlay.opns.sub_id", "overlay.opns.concurrency", "overlay.opns.batch_size",
    "overlay.bsv21.sub_id", "overlay.bsv21.concurrency", "overlay.bsv21.token_workers", "overlay.bsv21.batch_size",
    "overlay.bsocial.sub_id", "overlay.bsocial.concurrency", "overlay.bsocial.batch_size",
    "overlay.bsocial.mongo_url",
    "overlay.ordlock.sub_id", "overlay.ordlock.concurrency", "overlay.ordlock.batch_size",
    "indexer.sync.subscription_ids", "indexer.sync.concurrency", "indexer.sync.batch_size",
  ]);

  // Build current values map (same shape as handleSave sends)
  const currentValues = useMemo(() => ({
    "overlay.bap.enabled": String(bapEnabled),
    "overlay.opns.enabled": String(opnsEnabled),
    "overlay.bsv21.enabled": String(bsv21Enabled),
    "overlay.bsocial.enabled": String(bsocialEnabled),
    "overlay.ordlock.enabled": String(ordlockEnabled),
    "owner.enabled": String(ownerSync),
    "faucet.enabled": String(faucetEnabled),
    "store.provider": storeProvider,
    "store.badger.path": storePath,
    "pubsub.provider": pubsubProvider,
    "auth.mode": authMode,
    "chaintracks.path": chaintracksPath,
    "arcade.path": arcadeDb,
    "arcade.teranode.broadcast_urls": arcadeBroadcastUrls.join(","),
    "arcade.teranode.datahub_urls": arcadeDatahubUrls.join(","),
    "arcade.teranode.auth_token": arcadeAuthToken,
    "arcade.teranode.timeout": arcadeTimeout,
    "arcade.log_level": arcadeLogLevel,
    "overlay.engine.storage": engineStorage,
    "overlay.engine.storage_path": engineStoragePath,
    "overlay.engine.p2p.enabled": String(p2pEnabled),
    "overlay.engine.p2p.port": p2pPort,
    "overlay.engine.p2p.dht_mode": p2pDhtMode,
    "overlay.engine.p2p.bootstrap_peers": bootstrapPeers,
    "indexer.parsers": JSON.stringify(activeTags),
    "overlay.bap.sub_id": bapSubId,
    "overlay.bap.concurrency": bapConcurrency,
    "overlay.bap.batch_size": bapBatchSize,
    "overlay.opns.sub_id": opnsSubId,
    "overlay.opns.concurrency": opnsConcurrency,
    "overlay.opns.batch_size": opnsBatchSize,
    "overlay.bsv21.sub_id": bsv21SubId,
    "overlay.bsv21.concurrency": bsv21Concurrency,
    "overlay.bsv21.token_workers": bsv21TokenWorkers,
    "overlay.bsv21.batch_size": bsv21BatchSize,
    "overlay.bsocial.sub_id": bsocialSubId,
    "overlay.bsocial.concurrency": bsocialConcurrency,
    "overlay.bsocial.batch_size": bsocialBatchSize,
    "overlay.bsocial.mongo_url": bsocialMongoUrl,
    "overlay.ordlock.sub_id": ordlockSubId,
    "overlay.ordlock.concurrency": ordlockConcurrency,
    "overlay.ordlock.batch_size": ordlockBatchSize,
    "ordfs.cache.lru_size": ordfsLruSize,
    "ordfs.cache.redis_url": ordfsRedisUrl,
    "ordfs.cache.redis_ttl": ordfsRedisTtl,
    "indexer.sync.subscription_ids": indexerSubIds,
    "indexer.sync.concurrency": indexerConcurrency,
    "indexer.sync.batch_size": indexerBatchSize,
  }), [
    bapEnabled, opnsEnabled, bsv21Enabled, bsocialEnabled, ordlockEnabled, ownerSync, faucetEnabled,
    storeProvider, storePath, pubsubProvider, authMode,
    chaintracksPath, arcadeDb, arcadeBroadcastUrls, arcadeDatahubUrls, arcadeAuthToken, arcadeTimeout, arcadeLogLevel,
    engineStorage, engineStoragePath,
    p2pEnabled, p2pPort, p2pDhtMode, bootstrapPeers, activeTags,
    bapSubId, bapConcurrency, bapBatchSize,
    opnsSubId, opnsConcurrency, opnsBatchSize,
    bsv21SubId, bsv21Concurrency, bsv21TokenWorkers, bsv21BatchSize,
    bsocialSubId, bsocialConcurrency, bsocialBatchSize, bsocialMongoUrl,
    ordlockSubId, ordlockConcurrency, ordlockBatchSize,
    ordfsLruSize, ordfsRedisUrl, ordfsRedisTtl,
    indexerSubIds, indexerConcurrency, indexerBatchSize,
  ]);

  const needsRestart = useMemo(() => {
    if (!originalConfig.current || Object.keys(originalConfig.current).length === 0) return false;
    for (const key of RESTART_KEYS) {
      const original = originalConfig.current[key] ?? "";
      const current = currentValues[key as keyof typeof currentValues] ?? "";
      if (original !== current) return true;
    }
    return false;
  }, [currentValues]);

  async function handleSave() {
    setSaving(true);
    try {
      const values: Record<string, string> = {
        // Overlay toggles
        "overlay.bap.enabled": String(bapEnabled),
        "overlay.opns.enabled": String(opnsEnabled),
        "overlay.bsv21.enabled": String(bsv21Enabled),
        "overlay.bsocial.enabled": String(bsocialEnabled),
        "overlay.ordlock.enabled": String(ordlockEnabled),

        // Storage
        "store.provider": storeProvider,
        "store.badger.path": storePath,
        "beef.chain": JSON.stringify(beefChain),
        "pubsub.provider": pubsubProvider,
        "pubsub.channels.buffer_size": pubsubBuffer,
        "pubsub.redis.url": pubsubRedisUrl,
        "ordfs.cache.lru_size": ordfsLruSize,
        "ordfs.cache.redis_url": ordfsRedisUrl,
        "ordfs.cache.redis_ttl": ordfsRedisTtl,
        "chaintracks.path": chaintracksPath,
        "arcade.path": arcadeDb,
        "arcade.teranode.broadcast_urls": arcadeBroadcastUrls.join(","),
        "arcade.teranode.datahub_urls": arcadeDatahubUrls.join(","),
        "arcade.teranode.auth_token": arcadeAuthToken,
        "arcade.teranode.timeout": arcadeTimeout,
        "arcade.log_level": arcadeLogLevel,

        // Indexer
        "indexer.parsers": JSON.stringify(activeTags),
        "indexer.verbose": String(verbose),
        "indexer.log_level": logLevel,

        // BAP
        "overlay.bap.sub_id": bapSubId,
        "overlay.bap.concurrency": bapConcurrency,
        "overlay.bap.batch_size": bapBatchSize,

        // OPNS
        "overlay.opns.sub_id": opnsSubId,
        "overlay.opns.concurrency": opnsConcurrency,
        "overlay.opns.batch_size": opnsBatchSize,
        "overlay.opns.paymail": String(paymailEnabled),

        // BSV21
        "overlay.bsv21.sub_id": bsv21SubId,
        "overlay.bsv21.concurrency": bsv21Concurrency,
        "overlay.bsv21.token_workers": bsv21TokenWorkers,
        "overlay.bsv21.batch_size": bsv21BatchSize,
        "overlay.bsv21.whitelist": JSON.stringify(bsv21Whitelist),
        "overlay.bsv21.blacklist": JSON.stringify(bsv21Blacklist),

        // BSocial
        "overlay.bsocial.sub_id": bsocialSubId,
        "overlay.bsocial.concurrency": bsocialConcurrency,
        "overlay.bsocial.batch_size": bsocialBatchSize,
        "overlay.bsocial.mongo_url": bsocialMongoUrl,

        // OrdLock
        "overlay.ordlock.sub_id": ordlockSubId,
        "overlay.ordlock.concurrency": ordlockConcurrency,
        "overlay.ordlock.batch_size": ordlockBatchSize,

        // Overlay engine
        "overlay.engine.storage": engineStorage,
        "overlay.engine.storage_path": engineStoragePath,
        "overlay.engine.p2p.enabled": String(p2pEnabled),
        "overlay.engine.p2p.port": p2pPort,
        "overlay.engine.p2p.dht_mode": p2pDhtMode,
        "overlay.engine.p2p.bootstrap_peers": bootstrapPeers,

        // Sync
        "junglebus.url": jbUrl,
        "junglebus.token": jbToken,
        "indexer.sync.subscription_ids": indexerSubIds,
        "indexer.sync.concurrency": indexerConcurrency,
        "indexer.sync.batch_size": indexerBatchSize,
        "indexer.sync.enabled": String(indexerSyncEnabled),
        "indexer.sync.mempool": String(indexerMempool),
        "owner.enabled": String(ownerSync),
        "faucet.enabled": String(faucetEnabled),

        // Auth
        "auth.mode": authMode,
        "auth.api_key": apiKey,
        "auth.session_ttl": sessionTTL,

        // Tuning
        "worker.concurrency": defaultConcurrency,
        "worker.page_size": pageSize,
        "worker.poll_delay": pollDelay,
      };

      await saveConfig(values);
      if (needsRestart) {
        toast.success("Settings saved — restarting server...");
        await apiFetch("/restart", { method: "POST" }).catch(() => {});
        // Wait for server to come back
        setTimeout(() => window.location.reload(), 3000);
      } else {
        toast.success("Settings saved");
        originalConfig.current = { ...originalConfig.current, ...values };
      }
    } catch (err) {
      toastError(err instanceof Error ? err.message : "Failed to save config");
    } finally {
      setSaving(false);
    }
  }

  const anyOverlayEnabled = bapEnabled || opnsEnabled || bsv21Enabled || bsocialEnabled || ordlockEnabled;

  type NavItem =
    | { type: "item"; id: SectionId; label: string; icon: React.ElementType; dot?: boolean }
    | { type: "group"; label: string };

  const navItems: NavItem[] = [
    { type: "item", id: "node", label: "Node", icon: Server },
    { type: "item", id: "storage", label: "Storage", icon: Database },
    { type: "item", id: "indexer", label: "Indexer", icon: ScanSearch },
    { type: "item", id: "overlays", label: "Overlays", icon: Network, dot: anyOverlayEnabled },
    { type: "item", id: "overlay-bap", label: "BAP", icon: Network, dot: bapEnabled },
    { type: "item", id: "overlay-opns", label: "OPNS", icon: Network, dot: opnsEnabled },
    { type: "item", id: "overlay-bsv21", label: "BSV21", icon: Network, dot: bsv21Enabled },
    { type: "item", id: "overlay-bsocial", label: "BSocial", icon: Network, dot: bsocialEnabled },
    { type: "item", id: "overlay-ordlock", label: "OrdLock", icon: Network, dot: ordlockEnabled },
    { type: "item", id: "faucet", label: "Faucet", icon: Droplets, dot: faucetEnabled },
    { type: "item", id: "sync", label: "Sync", icon: RefreshCw },
    { type: "item", id: "auth", label: "Auth", icon: Shield },
    { type: "item", id: "tuning", label: "Tuning", icon: Cpu },
    { type: "item", id: "logs", label: "Logs", icon: ScrollText },
  ];

  if (loading) {
    return (
      <div className="min-h-screen bg-background flex items-center justify-center">
        <div className="text-sm text-muted-foreground">Loading configuration...</div>
      </div>
    );
  }

  return (
    <div className="min-h-screen bg-background flex flex-col">
      {/* Top bar */}
      <div className="border-b border-border bg-card/50 px-6 py-4 shrink-0">
        <div className="max-w-5xl mx-auto flex items-center justify-between">
          <div>
            <h1 className="text-base font-semibold text-foreground">Node Settings</h1>
            <p className="text-xs text-muted-foreground mt-0.5">Configure your 1Sat Stack node.</p>
          </div>
          <div className="flex items-center gap-2">
            {anyOverlayEnabled && (
              <span className="flex items-center gap-1.5 text-[11px] text-muted-foreground">
                <Info className="w-3.5 h-3.5" />
                Overlay engine auto-enabled
              </span>
            )}
            <Button size="sm" onClick={handleSave} disabled={saving} variant={needsRestart ? "destructive" : "default"}>
              {saving ? "Saving..." : needsRestart ? "Save & Restart" : "Save changes"}
            </Button>
          </div>
        </div>
      </div>

      <div className="max-w-5xl mx-auto flex gap-0 w-full flex-1 overflow-hidden">
        {/* Left sidebar */}
        <nav className="w-44 shrink-0 border-r border-border pt-4 pb-6 overflow-y-auto">
          <div className="space-y-0.5 px-2">
            {navItems.map((item, i) => {
              if (item.type === "group") {
                return (
                  <div
                    key={`group-${i}`}
                    className="pt-4 pb-1 px-3 text-[10px] font-semibold text-muted-foreground/60 uppercase tracking-widest"
                  >
                    {item.label}
                  </div>
                );
              }

              const isOverlaySub = item.id.startsWith("overlay-");

              return (
                <button
                  key={item.id}
                  type="button"
                  onClick={() => setActiveSection(item.id)}
                  className={cn(
                    "w-full flex items-center gap-2 rounded-lg text-sm transition-colors text-left",
                    isOverlaySub ? "pl-6 pr-3 py-1.5" : "px-3 py-2",
                    activeSection === item.id
                      ? "bg-primary/10 text-primary font-medium"
                      : "text-muted-foreground hover:text-foreground hover:bg-muted/50"
                  )}
                >
                  {item.dot !== undefined && (
                    <StatusDot active={item.dot} />
                  )}
                  <span>{item.label}</span>
                </button>
              );
            })}
          </div>
        </nav>

        {/* Content area */}
        <div className="flex-1 min-w-0 overflow-y-auto p-6">
          {activeSection === "node" && <NodePanel />}
          {activeSection === "storage" && (
            <StoragePanel
              storeProvider={storeProvider} setStoreProvider={setStoreProvider}
              storePath={storePath} setStorePath={setStorePath}
              beefChain={beefChain} setBeefChain={setBeefChain}
              pubsubProvider={pubsubProvider} setPubsubProvider={setPubsubProvider}
              pubsubBuffer={pubsubBuffer} setPubsubBuffer={setPubsubBuffer}
              pubsubRedisUrl={pubsubRedisUrl} setPubsubRedisUrl={setPubsubRedisUrl}
              ordfsLruSize={ordfsLruSize} setOrdfsLruSize={setOrdfsLruSize}
              ordfsRedisUrl={ordfsRedisUrl} setOrdfsRedisUrl={setOrdfsRedisUrl}
              ordfsRedisTtl={ordfsRedisTtl} setOrdfsRedisTtl={setOrdfsRedisTtl}
              chaintracksPath={chaintracksPath} setChaintracksPath={setChaintracksPath}
              arcadeDb={arcadeDb} setArcadeDb={setArcadeDb}
              arcadeBroadcastUrls={arcadeBroadcastUrls} setArcadeBroadcastUrls={setArcadeBroadcastUrls}
              arcadeDatahubUrls={arcadeDatahubUrls} setArcadeDatahubUrls={setArcadeDatahubUrls}
              arcadeAuthToken={arcadeAuthToken} setArcadeAuthToken={setArcadeAuthToken}
              arcadeTimeout={arcadeTimeout} setArcadeTimeout={setArcadeTimeout}
              arcadeLogLevel={arcadeLogLevel} setArcadeLogLevel={setArcadeLogLevel}
            />
          )}
          {activeSection === "indexer" && (
            <IndexerPanel
              activeTags={activeTags} setActiveTags={setActiveTags}
              verbose={verbose} setVerbose={setVerbose}
              logLevel={logLevel} setLogLevel={setLogLevel}
            />
          )}
          {activeSection === "overlays" && (
            <OverlayEnginePanel
              engineStorage={engineStorage} setEngineStorage={setEngineStorage}
              engineStoragePath={engineStoragePath} setEngineStoragePath={setEngineStoragePath}
              p2pEnabled={p2pEnabled} setP2pEnabled={setP2pEnabled}
              p2pPort={p2pPort} setP2pPort={setP2pPort}
              p2pDhtMode={p2pDhtMode} setP2pDhtMode={setP2pDhtMode}
              bootstrapPeers={bootstrapPeers} setBootstrapPeers={setBootstrapPeers}
            />
          )}
          {activeSection === "overlay-bap" && (
            <BapPanel
              enabled={bapEnabled} onToggle={setBapEnabled}
              subId={bapSubId} setSubId={setBapSubId}
              concurrency={bapConcurrency} setConcurrency={setBapConcurrency}
              batchSize={bapBatchSize} setBatchSize={setBapBatchSize}
            />
          )}
          {activeSection === "overlay-opns" && (
            <OpnsPanel
              enabled={opnsEnabled} onToggle={setOpnsEnabled}
              subId={opnsSubId} setSubId={setOpnsSubId}
              concurrency={opnsConcurrency} setConcurrency={setOpnsConcurrency}
              batchSize={opnsBatchSize} setBatchSize={setOpnsBatchSize}
              paymailEnabled={paymailEnabled} setPaymailEnabled={setPaymailEnabled}
            />
          )}
          {activeSection === "overlay-bsv21" && (
            <Bsv21Panel
              enabled={bsv21Enabled} onToggle={setBsv21Enabled}
              subId={bsv21SubId} setSubId={setBsv21SubId}
              concurrency={bsv21Concurrency} setConcurrency={setBsv21Concurrency}
              tokenWorkers={bsv21TokenWorkers} setTokenWorkers={setBsv21TokenWorkers}
              batchSize={bsv21BatchSize} setBatchSize={setBsv21BatchSize}
              whitelist={bsv21Whitelist} setWhitelist={setBsv21Whitelist}
              blacklist={bsv21Blacklist} setBlacklist={setBsv21Blacklist}
            />
          )}
          {activeSection === "overlay-bsocial" && (
            <BsocialPanel
              enabled={bsocialEnabled} onToggle={setBsocialEnabled}
              subId={bsocialSubId} setSubId={setBsocialSubId}
              concurrency={bsocialConcurrency} setConcurrency={setBsocialConcurrency}
              batchSize={bsocialBatchSize} setBatchSize={setBsocialBatchSize}
              mongoUrl={bsocialMongoUrl} setMongoUrl={setBsocialMongoUrl}
            />
          )}
          {activeSection === "overlay-ordlock" && (
            <OrdlockPanel
              enabled={ordlockEnabled} onToggle={setOrdlockEnabled}
              subId={ordlockSubId} setSubId={setOrdlockSubId}
              concurrency={ordlockConcurrency} setConcurrency={setOrdlockConcurrency}
              batchSize={ordlockBatchSize} setBatchSize={setOrdlockBatchSize}
            />
          )}
          {activeSection === "sync" && (
            <SyncPanel
              jbUrl={jbUrl} setJbUrl={setJbUrl}
              jbToken={jbToken} setJbToken={setJbToken}
              indexerSubIds={indexerSubIds} setIndexerSubIds={setIndexerSubIds}
              indexerConcurrency={indexerConcurrency} setIndexerConcurrency={setIndexerConcurrency}
              indexerBatchSize={indexerBatchSize} setIndexerBatchSize={setIndexerBatchSize}
              indexerSyncEnabled={indexerSyncEnabled} setIndexerSyncEnabled={setIndexerSyncEnabled}
              indexerMempool={indexerMempool} setIndexerMempool={setIndexerMempool}
              ownerSync={ownerSync} setOwnerSync={setOwnerSync}
            />
          )}
          {activeSection === "faucet" && (
            <FaucetPanel enabled={faucetEnabled} onToggle={setFaucetEnabled} />
          )}
          {activeSection === "auth" && (
            <AuthPanel
              authMode={authMode} setAuthMode={setAuthMode}
              apiKey={apiKey} setApiKey={setApiKey}
              sessionTTL={sessionTTL} setSessionTTL={setSessionTTL}
            />
          )}
          {activeSection === "tuning" && (
            <TuningPanel
              defaultConcurrency={defaultConcurrency} setDefaultConcurrency={setDefaultConcurrency}
              pageSize={pageSize} setPageSize={setPageSize}
              pollDelay={pollDelay} setPollDelay={setPollDelay}
            />
          )}
          {activeSection === "logs" && <Logs />}
        </div>
      </div>
    </div>
  );
}
