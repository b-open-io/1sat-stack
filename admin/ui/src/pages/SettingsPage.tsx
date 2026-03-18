import { useState } from "react";
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
} from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { cn } from "@/lib/utils";

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
  | "sync"
  | "auth"
  | "tuning";

// ─── Content panels ───────────────────────────────────────────────────────────

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
            "Wallet",
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
      filesystem: { type: "filesystem", path: "~/.1sat/beef" },
      redis: { type: "redis", url: "redis://localhost:6379/0" },
      badger: { type: "badger", path: "~/.1sat/beef-badger" },
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
                placeholder="~/.1sat/beef"
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
                placeholder="~/.1sat/beef-badger"
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

function StoragePanel() {
  const [storeProvider, setStoreProvider] = useState<"badger" | "redis">("badger");
  const [storePath, setStorePath] = useState("~/.1sat/store");
  const [beefChain, setBeefChain] = useState<BeefProvider[]>([
    { type: "lru", size: "100mb" },
    { type: "filesystem", path: "~/.1sat/beef" },
    { type: "junglebus" },
  ]);
  const [pubsubProvider, setPubsubProvider] = useState<"channels" | "redis">("channels");
  const [pubsubBuffer, setPubsubBuffer] = useState("1000");
  const [pubsubRedisUrl, setPubsubRedisUrl] = useState("");
  const [ordfsRedisUrl, setOrdfsRedisUrl] = useState("");
  const [walletDb, setWalletDb] = useState("~/.1sat/wallet.sqlite");
  const [chaintracksPath, setChaintracksPath] = useState("~/.1sat/chaintracks");
  const [arcadeDb, setArcadeDb] = useState("~/.1sat/arcade/arcade.db");

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
            placeholder={storeProvider === "badger" ? "~/.1sat/store" : "redis://localhost:6379/0"}
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
        <p className="text-[11px] text-muted-foreground">Content serving is always enabled.</p>
        <FieldRow label="Redis cache URL" hint="Leave blank to disable Redis caching for ORDFS.">
          <Input value={ordfsRedisUrl} onChange={(e) => setOrdfsRedisUrl(e.target.value)} placeholder="redis://localhost:6379/1" className="font-mono text-xs h-8" />
        </FieldRow>
      </SectionCard>

      <SectionCard>
        <SectionHeading>Databases</SectionHeading>
        <p className="text-[11px] text-muted-foreground">Set during initial setup. Restart required after any change.</p>
        <FieldRow label="Wallet" badge={<RestartBadge />}>
          <Input value={walletDb} onChange={(e) => setWalletDb(e.target.value)} className="font-mono text-xs h-8" />
        </FieldRow>
        <FieldRow label="Chaintracks" badge={<RestartBadge />}>
          <Input value={chaintracksPath} onChange={(e) => setChaintracksPath(e.target.value)} className="font-mono text-xs h-8" />
        </FieldRow>
        <FieldRow label="Arcade" badge={<RestartBadge />}>
          <Input value={arcadeDb} onChange={(e) => setArcadeDb(e.target.value)} className="font-mono text-xs h-8" />
        </FieldRow>
      </SectionCard>
    </div>
  );
}

const PARSE_TAGS = [
  { id: "p2pkh", label: "P2PKH", description: "Standard pay-to-public-key-hash outputs" },
  { id: "inscription", label: "Inscriptions", description: "1Sat Ordinal inscriptions" },
  { id: "bsv21", label: "BSV21", description: "BSV21 fungible token protocol" },
  { id: "bap", label: "BAP", description: "Bitcoin Attestation Protocol identities" },
  { id: "bsocial", label: "BSocial", description: "Social protocol (posts, likes, follows)" },
  { id: "opns", label: "OPNS", description: "Ordinal Public Name System" },
  { id: "ordlock", label: "OrdLock", description: "Ordinal listing/locking protocol" },
  { id: "map", label: "MAP", description: "Magic Attribute Protocol metadata" },
  { id: "sigma", label: "Sigma", description: "Sigma signature protocol" },
];

function IndexerPanel() {
  const [activeTags, setActiveTags] = useState<string[]>(
    PARSE_TAGS.map((t) => t.id) // all on by default
  );
  const [verbose, setVerbose] = useState(false);
  const [logLevel, setLogLevel] = useState("info");

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

function BapPanel({
  enabled,
  onToggle,
}: {
  enabled: boolean;
  onToggle: (v: boolean) => void;
}) {
  const [subId, setSubId] = useState("");
  const [fromBlock, setFromBlock] = useState("0");
  const [concurrency, setConcurrency] = useState("4");
  const [batchSize, setBatchSize] = useState("100");

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
        <div className="grid grid-cols-3 gap-3">
          <FieldRow label="From block">
            <Input value={fromBlock} onChange={(e) => setFromBlock(e.target.value)} className="font-mono text-xs h-8" />
          </FieldRow>
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

function OpnsPanel({
  enabled,
  onToggle,
}: {
  enabled: boolean;
  onToggle: (v: boolean) => void;
}) {
  const [subId, setSubId] = useState("");
  const [fromBlock, setFromBlock] = useState("0");
  const [concurrency, setConcurrency] = useState("4");
  const [batchSize, setBatchSize] = useState("100");
  const [paymailEnabled, setPaymailEnabled] = useState(false);

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
        <div className="grid grid-cols-3 gap-3">
          <FieldRow label="From block">
            <Input value={fromBlock} onChange={(e) => setFromBlock(e.target.value)} className="font-mono text-xs h-8" />
          </FieldRow>
          <FieldRow label="Concurrency">
            <Input value={concurrency} onChange={(e) => setConcurrency(e.target.value)} className="font-mono text-xs h-8" />
          </FieldRow>
          <FieldRow label="Batch size">
            <Input value={batchSize} onChange={(e) => setBatchSize(e.target.value)} className="font-mono text-xs h-8" />
          </FieldRow>
        </div>
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

function Bsv21Panel({
  enabled,
  onToggle,
}: {
  enabled: boolean;
  onToggle: (v: boolean) => void;
}) {
  const [subId, setSubId] = useState("");
  const [fromBlock, setFromBlock] = useState("0");
  const [concurrency, setConcurrency] = useState("4");
  const [batchSize, setBatchSize] = useState("100");
  const [whitelist, setWhitelist] = useState<string[]>([]);
  const [blacklist, setBlacklist] = useState<string[]>([]);

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
          <FieldRow label="From block">
            <Input value={fromBlock} onChange={(e) => setFromBlock(e.target.value)} className="font-mono text-xs h-8" />
          </FieldRow>
          <FieldRow label="Concurrency">
            <Input value={concurrency} onChange={(e) => setConcurrency(e.target.value)} className="font-mono text-xs h-8" />
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

function BsocialPanel({
  enabled,
  onToggle,
}: {
  enabled: boolean;
  onToggle: (v: boolean) => void;
}) {
  const [subId, setSubId] = useState("");
  const [fromBlock, setFromBlock] = useState("0");
  const [concurrency, setConcurrency] = useState("4");
  const [batchSize, setBatchSize] = useState("100");
  const [mongoUrl, setMongoUrl] = useState("");

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
        <div className="grid grid-cols-3 gap-3">
          <FieldRow label="From block">
            <Input value={fromBlock} onChange={(e) => setFromBlock(e.target.value)} className="font-mono text-xs h-8" />
          </FieldRow>
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
  enabled,
  onToggle,
}: {
  enabled: boolean;
  onToggle: (v: boolean) => void;
}) {
  const [storagePath, setStoragePath] = useState("~/.1sat/store");
  const [concurrency, setConcurrency] = useState("4");
  const [fromBlock, setFromBlock] = useState("0");
  const [batchSize, setBatchSize] = useState("100");

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
        <FieldRow label="Storage path">
          <Input value={storagePath} onChange={(e) => setStoragePath(e.target.value)} className="font-mono text-xs h-8" />
        </FieldRow>
        <div className="grid grid-cols-3 gap-3">
          <FieldRow label="From block">
            <Input value={fromBlock} onChange={(e) => setFromBlock(e.target.value)} className="font-mono text-xs h-8" />
          </FieldRow>
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

function OverlayEnginePanel() {
  const [engineStorage, setEngineStorage] = useState<"sqlite" | "postgres">("sqlite");
  const [engineStoragePath, setEngineStoragePath] = useState("~/.1sat/overlay.db");
  const [p2pEnabled, setP2pEnabled] = useState(false);
  const [p2pPort, setP2pPort] = useState("9000");
  const [p2pDhtMode, setP2pDhtMode] = useState("auto");
  const [bootstrapPeers, setBootstrapPeers] = useState("");

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
            placeholder={engineStorage === "sqlite" ? "~/.1sat/overlay.db" : "postgres://..."}
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

function SyncPanel() {
  const [jbUrl, setJbUrl] = useState("https://junglebus.gorillapool.io");
  const [jbToken, setJbToken] = useState("");
  const [indexerSubIds, setIndexerSubIds] = useState("");
  const [indexerFromBlock, setIndexerFromBlock] = useState("0");
  const [indexerConcurrency, setIndexerConcurrency] = useState("4");
  const [indexerBatchSize, setIndexerBatchSize] = useState("100");
  const [indexerMempool, setIndexerMempool] = useState(true);
  const [ownerSync, setOwnerSync] = useState(false);

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
        <div className="grid grid-cols-3 gap-3">
          <FieldRow label="From block">
            <Input value={indexerFromBlock} onChange={(e) => setIndexerFromBlock(e.target.value)} className="font-mono text-xs h-8" />
          </FieldRow>
          <FieldRow label="Concurrency">
            <Input value={indexerConcurrency} onChange={(e) => setIndexerConcurrency(e.target.value)} className="font-mono text-xs h-8" />
          </FieldRow>
          <FieldRow label="Batch size">
            <Input value={indexerBatchSize} onChange={(e) => setIndexerBatchSize(e.target.value)} className="font-mono text-xs h-8" />
          </FieldRow>
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

function AuthPanel() {
  const [authMode, setAuthMode] = useState<"local" | "authenticated">("local");
  const [apiKey, setApiKey] = useState("");
  const [showApiKey, setShowApiKey] = useState(false);
  const [sessionTTL, setSessionTTL] = useState("24h");

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


function TuningPanel() {
  const [defaultConcurrency, setDefaultConcurrency] = useState("4");
  const [pageSize, setPageSize] = useState("100");
  const [pollDelay, setPollDelay] = useState("500ms");

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

  const [bapEnabled, setBapEnabled] = useState(false);
  const [opnsEnabled, setOpnsEnabled] = useState(false);
  const [bsv21Enabled, setBsv21Enabled] = useState(false);
  const [bsocialEnabled, setBsocialEnabled] = useState(false);
  const [ordlockEnabled, setOrdlockEnabled] = useState(false);

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
    { type: "item", id: "sync", label: "Sync", icon: RefreshCw },
    { type: "item", id: "auth", label: "Auth", icon: Shield },
    { type: "item", id: "tuning", label: "Tuning", icon: Cpu },
  ];

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
            <Button size="sm">Save changes</Button>
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
          {activeSection === "storage" && <StoragePanel />}
          {activeSection === "indexer" && <IndexerPanel />}
          {activeSection === "overlays" && <OverlayEnginePanel />}
          {activeSection === "overlay-bap" && <BapPanel enabled={bapEnabled} onToggle={setBapEnabled} />}
          {activeSection === "overlay-opns" && <OpnsPanel enabled={opnsEnabled} onToggle={setOpnsEnabled} />}
          {activeSection === "overlay-bsv21" && <Bsv21Panel enabled={bsv21Enabled} onToggle={setBsv21Enabled} />}
          {activeSection === "overlay-bsocial" && <BsocialPanel enabled={bsocialEnabled} onToggle={setBsocialEnabled} />}
          {activeSection === "overlay-ordlock" && <OrdlockPanel enabled={ordlockEnabled} onToggle={setOrdlockEnabled} />}
          {activeSection === "sync" && <SyncPanel />}
          {activeSection === "auth" && <AuthPanel />}
          {activeSection === "tuning" && <TuningPanel />}
        </div>
      </div>
    </div>
  );
}
