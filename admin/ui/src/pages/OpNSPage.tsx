import { useState, useCallback } from "react";
import { toast } from "sonner";
import { toastError } from "@/lib/utils";
import { adminServices } from "@/lib/services";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { PrivateKey } from "@bsv/sdk";
import type { WalletOutput } from "@bsv/sdk";
import type { IndexedOutput } from "@1sat/types";
import {
  sweepOrdinals,
  prepareSweepInputs,
  opnsRegister,
  opnsDeregister,
  type OneSatContext,
} from "@1sat/actions";

// ============================================================================
// Types
// ============================================================================

interface DiscoveredName {
  outpoint: string;
  name: string;
  satoshis: number;
}

interface WalletName {
  output: WalletOutput;
  name: string;
  published: boolean;
}

interface NameLookupResult {
  name: string;
  origin: string;
  owner: string;
  status: "active" | "pending" | "expired";
}

interface MineResult {
  outpoint: string;
  domain: string;
}

interface Props {
  identityKey: string | null;
}

// ============================================================================
// Helpers
// ============================================================================

function getServerBase(): string {
  const adminIdx = window.location.pathname.indexOf("/admin");
  const basePath = adminIdx >= 0 ? window.location.pathname.substring(0, adminIdx) : "";
  return `${window.location.origin}${basePath}`;
}

function buildContext(): OneSatContext {
  const cwi = window.CWI;
  if (!cwi) throw new Error("No wallet connected");
  return {
    wallet: cwi,
    services: adminServices as any,
    chain: "main",
  };
}

function parseNameFromTags(tags?: string[]): string {
  if (!tags) return "unknown";
  const nameTag = tags.find(t => t.startsWith("name:"));
  if (nameTag) return nameTag.slice(5);
  const inscTag = tags.find(t => t.startsWith("insc:"));
  if (inscTag) {
    try {
      return JSON.parse(inscTag.slice(5))?.name ?? "unknown";
    } catch { /* ignore */ }
  }
  return "unknown";
}

function parseNameFromCustomInstructions(ci?: string): string {
  if (!ci) return "unknown";
  try {
    return JSON.parse(ci).name ?? "unknown";
  } catch {
    return "unknown";
  }
}

function isPublished(tags?: string[]): boolean {
  return tags?.includes("opns:published") ?? false;
}

// ============================================================================
// Component
// ============================================================================

export default function OpNSPage({ identityKey }: Props) {
  // Import section state
  const [wif, setWif] = useState("");
  const [importAddress, setImportAddress] = useState<string | null>(null);
  const [discoveredNames, setDiscoveredNames] = useState<DiscoveredName[]>([]);
  const [syncing, setSyncing] = useState(false);
  const [syncProgress, setSyncProgress] = useState<string | null>(null);
  const [sweeping, setSweeping] = useState<string | null>(null);

  // Wallet inventory state
  const [walletNames, setWalletNames] = useState<WalletName[]>([]);
  const [loadingWallet, setLoadingWallet] = useState(false);
  const [publishing, setPublishing] = useState<string | null>(null);

  // Lookup card state
  const [lookupQuery, setLookupQuery] = useState("");
  const [lookupResult, setLookupResult] = useState<NameLookupResult | null>(null);
  const [lookupLoading, setLookupLoading] = useState(false);

  // Mine card state
  const [mineQuery, setMineQuery] = useState("");
  const [mineResult, setMineResult] = useState<MineResult | null>(null);
  const [mineTaken, setMineTaken] = useState(false);
  const [mineLoading, setMineLoading] = useState(false);

  // Crawl state
  const [crawling, setCrawling] = useState(false);

  // ========== Import from External Wallet ==========

  const discoverExternalNames = useCallback(async () => {
    if (!wif.trim()) {
      toastError("Paste a WIF private key");
      return;
    }

    setSyncing(true);
    setSyncProgress("Deriving address...");
    setDiscoveredNames([]);
    setImportAddress(null);

    try {
      const pk = PrivateKey.fromWif(wif.trim());
      const address = pk.toPublicKey().toAddress();
      setImportAddress(address);

      // Trigger owner sync via SSE
      setSyncProgress("Syncing address...");
      const base = getServerBase();
      await new Promise<void>((resolve, reject) => {
        const es = new EventSource(`${base}/owner/${address}/txos?refresh=true&limit=1`);
        es.onmessage = (ev) => {
          try {
            const msg = JSON.parse(ev.data);
            if (msg.phase === "done" || msg.phase === "error") {
              es.close();
              if (msg.phase === "error") {
                reject(new Error(msg.error || "Sync failed"));
              } else {
                resolve();
              }
            } else if (msg.phase === "fetch" || msg.phase === "ingest") {
              setSyncProgress(`${msg.phase}: ${msg.processed ?? 0}/${msg.total ?? "?"}`);
            }
          } catch {
            // Not JSON progress, might be txo data — ignore
          }
        };
        es.onerror = () => {
          es.close();
          resolve(); // SSE close on complete is normal
        };
      });

      // Search for unspent OPNS outputs owned by this address
      setSyncProgress("Searching for OPNS names...");
      const searchUrl = new URL(`${base}/txo/search`);
      searchUrl.searchParams.append("key", `own:${address}`);
      searchUrl.searchParams.append("key", "type:application/op-ns");
      searchUrl.searchParams.set("join", "intersect");
      searchUrl.searchParams.set("unspent", "true");
      searchUrl.searchParams.set("events", "true");
      searchUrl.searchParams.set("sats", "true");

      const res = await fetch(searchUrl.toString());
      if (!res.ok) throw new Error(`Search failed: ${res.statusText}`);

      const results: IndexedOutput[] = await res.json();
      if (!results || results.length === 0) {
        setDiscoveredNames([]);
        setSyncProgress(null);
        toast.info("No OPNS names found on this address");
        return;
      }

      // Extract names from indexed name: events
      const names: DiscoveredName[] = results.map(r => {
        const nameEvent = r.events?.find((e: string) => e.startsWith("name:"));
        return {
          outpoint: r.outpoint,
          name: nameEvent ? nameEvent.slice(5) : "unknown",
          satoshis: r.satoshis ?? 1,
        };
      });

      setDiscoveredNames(names);
      setSyncProgress(null);
      toast.success(`Found ${names.length} OPNS name(s)`);
    } catch (e: any) {
      console.error("[OpNS] discover error:", e);
      toastError(e.message || "Failed to discover names");
      setSyncProgress(null);
    } finally {
      setSyncing(false);
    }
  }, [wif]);

  const sweepName = useCallback(async (name: DiscoveredName) => {
    if (!wif.trim()) {
      toastError("WIF required for sweep");
      return;
    }

    setSweeping(name.outpoint);
    try {
      const ctx = buildContext();

      // Prepare sweep input by fetching BEEF and extracting locking script
      const utxos: IndexedOutput[] = [{
        outpoint: name.outpoint,
        score: 0,
        satoshis: name.satoshis,
      }];
      const sweepInputs = await prepareSweepInputs(ctx, utxos);

      // Add metadata for proper basket/tag assignment
      const result = await sweepOrdinals.execute(ctx, {
        inputs: sweepInputs.map(si => ({
          ...si,
          contentType: "application/op-ns",
          name: name.name,
        })),
        wif: wif.trim(),
      });

      if (result.error) {
        throw new Error(result.error);
      }

      toast.success(`Swept "${name.name}" — txid: ${result.txid?.slice(0, 8)}...`);
      setDiscoveredNames(prev => prev.filter(n => n.outpoint !== name.outpoint));
    } catch (e: any) {
      console.error("[OpNS] sweep error:", e);
      toastError(e.message || "Sweep failed");
    } finally {
      setSweeping(null);
    }
  }, [wif]);

  // ========== Wallet Inventory ==========

  const loadWalletNames = useCallback(async () => {
    setLoadingWallet(true);
    try {
      const cwi = window.CWI;
      if (!cwi) {
        toastError("No wallet connected");
        return;
      }

      const result = await cwi.listOutputs({
        basket: "opns",
        include: "entire transactions",
        includeTags: true,
        includeCustomInstructions: true,
      });

      const names: WalletName[] = (result.outputs ?? []).map((o: WalletOutput) => ({
        output: o,
        name: parseNameFromCustomInstructions(o.customInstructions) !== "unknown"
          ? parseNameFromCustomInstructions(o.customInstructions)
          : parseNameFromTags(o.tags),
        published: isPublished(o.tags),
      }));

      setWalletNames(names);
      if (names.length === 0) {
        toast.info("No OPNS names in wallet");
      }
    } catch (e: any) {
      console.error("[OpNS] loadWalletNames error:", e);
      toastError(e.message || "Failed to load wallet names");
    } finally {
      setLoadingWallet(false);
    }
  }, []);

  const publishName = useCallback(async (wn: WalletName) => {
    setPublishing(wn.output.outpoint);
    try {
      const ctx = buildContext();
      const cwi = window.CWI;
      if (!cwi) throw new Error("No wallet connected");

      const listResult = await cwi.listOutputs({
        basket: "opns",
        include: "entire transactions",
        includeTags: true,
        includeCustomInstructions: true,
      });

      const result = await opnsRegister.execute(ctx, {
        ordinal: wn.output,
        inputBEEF: listResult.BEEF ? Array.from(listResult.BEEF) : [],
      });

      if (result.error) throw new Error(result.error);
      toast.success(`Published "${wn.name}" — txid: ${result.txid?.slice(0, 8)}...`);
      await loadWalletNames();
    } catch (e: any) {
      console.error("[OpNS] publish error:", e);
      toastError(e.message || "Publish failed");
    } finally {
      setPublishing(null);
    }
  }, [loadWalletNames]);

  const unpublishName = useCallback(async (wn: WalletName) => {
    setPublishing(wn.output.outpoint);
    try {
      const ctx = buildContext();
      const cwi = window.CWI;
      if (!cwi) throw new Error("No wallet connected");

      const listResult = await cwi.listOutputs({
        basket: "opns",
        include: "entire transactions",
        includeTags: true,
        includeCustomInstructions: true,
      });

      const result = await opnsDeregister.execute(ctx, {
        ordinal: wn.output,
        inputBEEF: listResult.BEEF ? Array.from(listResult.BEEF) : [],
      });

      if (result.error) throw new Error(result.error);
      toast.success(`Unpublished "${wn.name}" — txid: ${result.txid?.slice(0, 8)}...`);
      await loadWalletNames();
    } catch (e: any) {
      console.error("[OpNS] unpublish error:", e);
      toastError(e.message || "Unpublish failed");
    } finally {
      setPublishing(null);
    }
  }, [loadWalletNames]);

  // ========== Lookup & Mine ==========

  const triggerCrawl = useCallback(async () => {
    setCrawling(true);
    try {
      const adminIdx = window.location.pathname.indexOf("/admin");
      const basePath = adminIdx >= 0 ? window.location.pathname.substring(0, adminIdx + "/admin".length) : "";
      const res = await fetch(`${window.location.origin}${basePath}/api/opns/crawl`, { method: "POST" });
      const data = await res.json();
      if (!res.ok) throw new Error(data.error || res.statusText);
      toast.success(data.message || "Crawl started");
    } catch (e: any) {
      toastError(e.message || "Failed to start crawl");
    } finally {
      setCrawling(false);
    }
  }, []);

  const lookupName = useCallback(async () => {
    if (!lookupQuery.trim()) {
      toastError("Enter a name to lookup");
      return;
    }

    setLookupLoading(true);
    try {
      const base = getServerBase();
      const res = await fetch(`${base}/opns/origin/${encodeURIComponent(lookupQuery.trim())}`);
      if (res.status === 404) {
        setLookupResult(null);
        toast.info("Name not registered");
        return;
      }
      if (!res.ok) {
        const data = await res.json().catch(() => ({}));
        throw new Error(data.error || res.statusText);
      }
      const data = await res.json();
      setLookupResult({
        name: data.name,
        origin: data.outpoint,
        owner: "Unknown",
        status: "active",
      });
    } catch (e: any) {
      toastError(e.message || "Failed to lookup name");
      setLookupResult(null);
    } finally {
      setLookupLoading(false);
    }
  }, [lookupQuery]);

  const lookupMine = useCallback(async () => {
    if (!mineQuery.trim()) {
      toastError("Enter a name to check");
      return;
    }

    setMineLoading(true);
    setMineResult(null);
    setMineTaken(false);
    try {
      const base = getServerBase();
      const res = await fetch(`${base}/opns/mine/${encodeURIComponent(mineQuery.trim())}`);
      if (res.status === 404) {
        setMineTaken(true);
        return;
      }
      if (!res.ok) {
        const data = await res.json().catch(() => ({}));
        throw new Error(data.error || res.statusText);
      }
      const data = await res.json();
      setMineResult(data);
    } catch (e: any) {
      toastError(e.message || "Failed to check mine status");
    } finally {
      setMineLoading(false);
    }
  }, [mineQuery]);

  const statusColor = (status: string) => {
    switch (status) {
      case "active": return "bg-success/20 text-success";
      case "pending": return "bg-warning/20 text-warning";
      case "expired": return "bg-destructive/20 text-destructive";
      default: return "bg-muted/20 text-muted-foreground";
    }
  };

  return (
    <div className="space-y-6">
      <div className="rounded-lg border border-border bg-gradient-to-br from-secondary to-card p-8 text-center">
        <h2 className="text-2xl font-semibold mb-1">OpNS Name Management</h2>
        <p className="text-muted-foreground text-sm">Import, manage, and publish OpNS names</p>
      </div>

      {/* Import from External Wallet */}
      <Card>
        <CardHeader className="pb-2">
          <CardTitle>Import from External Wallet</CardTitle>
          <CardDescription>Paste a WIF to discover OPNS names on that address, then sweep them into your BRC-100 wallet</CardDescription>
        </CardHeader>
        <CardContent>
          <div className="flex gap-2 mb-4">
            <Input
              type="password"
              value={wif}
              onChange={(e) => setWif(e.target.value)}
              placeholder="Paste WIF private key"
              onKeyDown={(e) => e.key === "Enter" && discoverExternalNames()}
            />
            <Button onClick={discoverExternalNames} disabled={syncing || !wif.trim()}>
              {syncing ? "Syncing..." : "Discover"}
            </Button>
          </div>

          {importAddress && (
            <div className="text-xs text-muted-foreground font-mono mb-3">
              Address: {importAddress}
            </div>
          )}

          {syncProgress && (
            <div className="text-xs text-muted-foreground mb-3">{syncProgress}</div>
          )}

          {discoveredNames.length > 0 && (
            <div className="space-y-2">
              {discoveredNames.map((n) => (
                <div key={n.outpoint} className="flex items-center justify-between rounded-md bg-secondary p-3">
                  <div>
                    <span className="font-mono text-sm">{n.name}</span>
                    <span className="text-xs text-muted-foreground ml-2">{n.outpoint.slice(0, 12)}...</span>
                  </div>
                  <Button
                    size="sm"
                    onClick={() => sweepName(n)}
                    disabled={sweeping === n.outpoint}
                  >
                    {sweeping === n.outpoint ? "Sweeping..." : "Sweep"}
                  </Button>
                </div>
              ))}
            </div>
          )}
        </CardContent>
      </Card>

      {/* My Wallet Names */}
      <Card>
        <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
          <div>
            <CardTitle>My Wallet Names</CardTitle>
            <CardDescription>OPNS names in your BRC-100 wallet</CardDescription>
          </div>
          <div className="flex items-center gap-2">
            <Badge variant="secondary">{walletNames.length} name{walletNames.length !== 1 ? "s" : ""}</Badge>
            <Button
              variant="secondary"
              size="sm"
              onClick={loadWalletNames}
              disabled={loadingWallet || !identityKey}
            >
              {loadingWallet ? "Loading..." : "Refresh"}
            </Button>
          </div>
        </CardHeader>
        <CardContent>
          {identityKey && (
            <div className="text-xs text-muted-foreground font-mono mb-3">
              Identity: {identityKey.slice(0, 8)}...{identityKey.slice(-8)}
            </div>
          )}

          <div className="space-y-2">
            {walletNames.length === 0 ? (
              <p className="text-center py-8 text-muted-foreground">
                {identityKey
                  ? 'Click "Refresh" to load names from your wallet'
                  : "Connect wallet to view names"}
              </p>
            ) : (
              walletNames.map((wn) => (
                <div key={wn.output.outpoint} className="flex items-center justify-between rounded-md bg-secondary p-3">
                  <div className="flex items-center gap-2">
                    <span className="font-mono text-sm">{wn.name}</span>
                    {wn.published && (
                      <Badge className="border-0 uppercase text-xs bg-success/20 text-success">
                        published
                      </Badge>
                    )}
                  </div>
                  <div className="flex gap-2">
                    {!wn.published ? (
                      <Button
                        size="sm"
                        onClick={() => publishName(wn)}
                        disabled={publishing === wn.output.outpoint}
                      >
                        {publishing === wn.output.outpoint ? "Publishing..." : "Publish"}
                      </Button>
                    ) : (
                      <Button
                        size="sm"
                        variant="secondary"
                        onClick={() => unpublishName(wn)}
                        disabled={publishing === wn.output.outpoint}
                      >
                        {publishing === wn.output.outpoint ? "Unpublishing..." : "Unpublish"}
                      </Button>
                    )}
                  </div>
                </div>
              ))
            )}
          </div>
        </CardContent>
      </Card>

      {/* Lookup & Mine Cards */}
      <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-6">
        <Card>
          <CardHeader className="pb-2">
            <CardTitle>Lookup Name in System</CardTitle>
          </CardHeader>
          <CardContent>
            <CardDescription className="mb-4">Find an existing OpNS name and view its origin</CardDescription>
            <div className="flex gap-2 mb-4">
              <Input
                value={lookupQuery}
                onChange={(e) => setLookupQuery(e.target.value)}
                placeholder="Enter name (e.g., myname)"
                onKeyDown={(e) => e.key === "Enter" && lookupName()}
              />
              <Button onClick={lookupName} disabled={lookupLoading}>
                {lookupLoading ? "Searching..." : "Lookup"}
              </Button>
            </div>

            {lookupResult && (
              <div className="rounded-md border border-border bg-secondary p-4 space-y-2">
                <div className="flex justify-between text-sm">
                  <span className="text-muted-foreground">Name:</span>
                  <span className="font-mono">{lookupResult.name}</span>
                </div>
                <div className="flex justify-between text-sm">
                  <span className="text-muted-foreground">Origin:</span>
                  <span className="font-mono">{lookupResult.origin}</span>
                </div>
                <div className="flex justify-between text-sm">
                  <span className="text-muted-foreground">Owner:</span>
                  <span className="font-mono">{lookupResult.owner}</span>
                </div>
                <div className="flex justify-between text-sm items-center">
                  <span className="text-muted-foreground">Status:</span>
                  <Badge className={`border-0 uppercase text-xs ${statusColor(lookupResult.status)}`}>
                    {lookupResult.status}
                  </Badge>
                </div>
              </div>
            )}
          </CardContent>
        </Card>

        <Card>
          <CardHeader className="pb-2">
            <CardTitle>Mine Lookup</CardTitle>
          </CardHeader>
          <CardContent>
            <CardDescription className="mb-4">Check if a name can be mined and find the parent mine output</CardDescription>
            <div className="flex gap-2 mb-4">
              <Input
                value={mineQuery}
                onChange={(e) => setMineQuery(e.target.value)}
                placeholder="Enter name to check"
                onKeyDown={(e) => e.key === "Enter" && lookupMine()}
              />
              <Button onClick={lookupMine} disabled={mineLoading}>
                {mineLoading ? "Checking..." : "Check"}
              </Button>
            </div>

            {mineTaken && (
              <div className="rounded-md border border-border bg-secondary p-4 text-sm text-muted-foreground">
                Name already mined or no mine data found.
              </div>
            )}

            {mineResult && (
              <div className="rounded-md border border-border bg-secondary p-4 space-y-2">
                <div className="flex justify-between text-sm">
                  <span className="text-muted-foreground">Mined Prefix:</span>
                  <span className="font-mono">{mineResult.domain}</span>
                </div>
                <div className="flex justify-between text-sm">
                  <span className="text-muted-foreground">Outpoint:</span>
                  <span className="font-mono text-xs break-all">{mineResult.outpoint}</span>
                </div>
              </div>
            )}
          </CardContent>
        </Card>

        <Card>
          <CardHeader className="pb-2">
            <CardTitle>OpNS Tree Sync</CardTitle>
          </CardHeader>
          <CardContent>
            <CardDescription className="mb-4">Trigger a genesis crawl to populate the OPNS name tree</CardDescription>
            <Button
              variant="secondary"
              onClick={triggerCrawl}
              disabled={crawling}
            >
              {crawling ? "Starting..." : "Sync OpNS Tree"}
            </Button>
          </CardContent>
        </Card>
      </div>
    </div>
  );
}
