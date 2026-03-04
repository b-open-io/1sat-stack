import { useState, useCallback } from "react";
import { apiFetch } from "../api";
import { toast } from "sonner";
import { toastError } from "@/lib/utils";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";

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
  const [names, setNames] = useState<OpnsName[]>([]);
  const [loading, setLoading] = useState(false);
  const [publishing, setPublishing] = useState<string | null>(null);
  const [crawling, setCrawling] = useState(false);

  const [lookupQuery, setLookupQuery] = useState("");
  const [lookupResult, setLookupResult] = useState<NameLookupResult | null>(null);
  const [lookupLoading, setLookupLoading] = useState(false);

  const triggerCrawl = useCallback(async () => {
    setCrawling(true);
    try {
      const res = await apiFetch("/opns/crawl", { method: "POST" });
      const data = await res.json();
      if (!res.ok) throw new Error(data.error || res.statusText);
      toast.success(data.message || "Crawl started");
    } catch (e: any) {
      toastError(e.message || "Failed to start crawl");
    } finally {
      setCrawling(false);
    }
  }, []);

  const discoverNames = useCallback(async () => {
    setLoading(true);
    try {
      const cwi = (window as any).CWI;
      if (!cwi) {
        toastError("No wallet connected");
        return;
      }

      const result = await cwi.listOutputs({
        basket: "default",
        include: "locking scripts",
      });

      toast.info(`listOutputs returned ${result?.outputs?.length ?? 0} outputs — inspect console`);
      console.log("[OpNS] listOutputs result:", result);
      setNames([]);
    } catch (e: any) {
      console.error("[OpNS] discoverNames error:", e);
      toastError(e.message || "Failed to discover names");
    } finally {
      setLoading(false);
    }
  }, []);

  const publishName = useCallback(async (name: OpnsName) => {
    setPublishing(name.name);
    try {
      toastError("Publish not yet wired up — see console for context");
      console.log("[OpNS] publish requested for:", name);
    } catch (e: any) {
      toastError(e.message || "Publish failed");
    } finally {
      setPublishing(null);
    }
  }, []);

  const lookupName = useCallback(async () => {
    if (!lookupQuery.trim()) {
      toastError("Enter a name to lookup");
      return;
    }

    setLookupLoading(true);
    try {
      // TODO: Wire up to actual OpNS lookup endpoint once available
      const mockResult: NameLookupResult = {
        name: lookupQuery,
        origin: "Genesis Block",
        owner: "Unknown",
        status: "pending",
      };
      setLookupResult(mockResult);
      toast.success(`Found name: ${lookupQuery}`);
    } catch (e: any) {
      toastError(e.message || "Failed to lookup name");
      setLookupResult(null);
    } finally {
      setLookupLoading(false);
    }
  }, [lookupQuery]);

  const statusColor = (status: string) => {
    switch (status) {
      case "active": return "bg-success/20 text-success";
      case "pending": return "bg-warning/20 text-warning";
      case "expired": return "bg-destructive/20 text-destructive";
      default: return "";
    }
  };

  return (
    <div className="space-y-6">
      <div className="rounded-lg border border-border bg-gradient-to-br from-secondary to-card p-8 text-center">
        <h2 className="text-2xl font-semibold mb-1">OpNS Name Management</h2>
        <p className="text-muted-foreground text-sm">Discover, lookup, and publish OpNS names</p>
      </div>

      <div className="grid grid-cols-1 md:grid-cols-2 gap-6">
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
          <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
            <CardTitle>My Wallet Names</CardTitle>
            <div className="flex items-center gap-2">
              <Badge variant="secondary">{names.length} name{names.length !== 1 ? "s" : ""}</Badge>
              <Button
                variant="secondary"
                size="sm"
                onClick={discoverNames}
                disabled={loading || !identityKey}
              >
                {loading ? "Searching..." : "Discover"}
              </Button>
            </div>
          </CardHeader>
          <CardContent>
            <CardDescription className="mb-4">Find OpNS names in your wallet to publish</CardDescription>

            {identityKey && (
              <div className="text-xs text-muted-foreground font-mono mb-3">
                Identity: {identityKey.slice(0, 8)}...{identityKey.slice(-8)}
              </div>
            )}

            <div className="mb-4">
              <Button
                variant="secondary"
                size="sm"
                onClick={triggerCrawl}
                disabled={crawling}
              >
                {crawling ? "Starting..." : "Sync OpNS Tree"}
              </Button>
              <span className="text-xs text-muted-foreground ml-2">Populate the name tree</span>
            </div>

            <div className="space-y-2">
              {names.length === 0 ? (
                <p className="text-center py-8 text-muted-foreground">
                  {identityKey
                    ? 'Click "Discover" to find OpNS ordinals in your wallet'
                    : "Connect wallet to discover names"}
                </p>
              ) : (
                names.map((n) => (
                  <div key={`${n.txid}_${n.vout}`} className="flex items-center justify-between rounded-md bg-secondary p-3">
                    <span className="font-mono text-sm">{n.name}</span>
                    <Button
                      size="sm"
                      onClick={() => publishName(n)}
                      disabled={publishing === n.name}
                    >
                      {publishing === n.name ? "Publishing..." : "Publish"}
                    </Button>
                  </div>
                ))
              )}
            </div>
          </CardContent>
        </Card>
      </div>
    </div>
  );
}
