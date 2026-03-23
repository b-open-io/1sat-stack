import { useCallback, useMemo, useState } from "react";
import { Toaster, toast } from "sonner";
import { RefreshCw } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import { Tabs, TabsList, TabsTrigger, TabsContent } from "@/components/ui/tabs";
import { ConnectWallet } from "@/components/connect-wallet";
import { WifInput, type LegacyKeys } from "@/components/wif-input";
import {
  FundingSection,
  OrdinalsSection,
  Bsv21Section,
  Bsv20Section,
  LockedSection,
} from "@/components/asset-preview";
import { OpnsSection } from "@/components/opns-section";
import { SweepProgress } from "@/components/sweep-progress";
import { deriveAddress, scanAddresses, type ScannedAssets } from "@/lib/scanner";
import { executeSweep, type SweepResult } from "@/lib/sweeper";
import { legacySendBsv, legacySendOrdinals, legacyBurnOrdinals } from "@/lib/legacy-send";
import { getWallet } from "@/lib/wallet";

type TabId = "ordinals" | "opns" | "bsv21" | "bsv20" | "locks";

export default function App() {
  const [walletConnected, setWalletConnected] = useState(false);
  const [scanning, setScanning] = useState(false);
  const [scanProgress, setScanProgress] = useState("");
  const [assets, setAssets] = useState<ScannedAssets | null>(null);
  const [legacyKeys, setLegacyKeys] = useState<LegacyKeys | null>(null);
  const [sweeping, setSweeping] = useState(false);
  const [sweepProgress, setSweepProgress] = useState("");
  const [sweepResult, setSweepResult] = useState<SweepResult | null>(null);
  const [selectedOrdinals, setSelectedOrdinals] = useState<Set<string>>(new Set());
  const [selectedOpns, setSelectedOpns] = useState<Set<string>>(new Set());
  const [sweepAmount, setSweepAmount] = useState<number | null>(null);
  const [activeTab, setActiveTab] = useState<TabId>("ordinals");

  // --- Tab visibility ---
  const tabs = useMemo(() => {
    if (!assets) return [];
    const t: { id: TabId; label: string; count: number }[] = [];
    if (assets.ordinals.length > 0) t.push({ id: "ordinals", label: "Ordinals", count: assets.ordinals.length });
    if (assets.opnsNames.length > 0) t.push({ id: "opns", label: "OpNS", count: assets.opnsNames.length });
    if (assets.bsv21Tokens.length > 0) t.push({ id: "bsv21", label: "BSV-21", count: assets.bsv21Tokens.length });
    if (assets.bsv20Tokens.length > 0) t.push({ id: "bsv20", label: "BSV-20", count: assets.bsv20Tokens.length });
    if (assets.locked.length > 0) t.push({ id: "locks", label: "Locks", count: assets.locked.length });
    return t;
  }, [assets]);

  // --- Ordinals selection ---
  const handleToggleOrdinal = useCallback((outpoint: string) => {
    setSelectedOrdinals((prev) => {
      const next = new Set(prev);
      if (next.has(outpoint)) next.delete(outpoint);
      else next.add(outpoint);
      return next;
    });
  }, []);

  const handleSelectAllOrdinals = useCallback(() => {
    if (assets) setSelectedOrdinals(new Set(assets.ordinals.map((o) => o.outpoint)));
  }, [assets]);

  const handleDeselectAllOrdinals = useCallback(() => {
    setSelectedOrdinals(new Set());
  }, []);

  // --- OPNS selection ---
  const handleToggleOpns = useCallback((outpoint: string) => {
    setSelectedOpns((prev) => {
      const next = new Set(prev);
      if (next.has(outpoint)) next.delete(outpoint);
      else next.add(outpoint);
      return next;
    });
  }, []);

  const handleSelectAllOpns = useCallback(() => {
    if (assets) setSelectedOpns(new Set(assets.opnsNames.map((o) => o.outpoint)));
  }, [assets]);

  const handleDeselectAllOpns = useCallback(() => {
    setSelectedOpns(new Set());
  }, []);

  // --- Scan ---
  const handleScan = useCallback(async (keys: LegacyKeys) => {
    setScanning(true);
    setAssets(null);
    setSweepResult(null);
    setSelectedOrdinals(new Set());
    setSelectedOpns(new Set());
    setSweepAmount(null);
    setLegacyKeys(keys);

    try {
      const addresses = [...new Set([
        deriveAddress(keys.payPk),
        deriveAddress(keys.ordPk),
        ...(keys.identityPk ? [deriveAddress(keys.identityPk)] : []),
      ])];

      const result = await scanAddresses(
        addresses,
        (p) => setScanProgress(p.detail ?? p.phase),
      );

      setAssets(result);

      const total = result.funding.length + result.ordinals.length +
        result.opnsNames.length +
        result.bsv21Tokens.reduce((n, t) => n + t.outputs.length, 0) +
        result.bsv20Tokens.length + result.locked.length;
      if (total === 0) {
        toast.info("No assets found at legacy addresses");
      }

      // Auto-select first populated tab
      if (result.ordinals.length > 0) setActiveTab("ordinals");
      else if (result.opnsNames.length > 0) setActiveTab("opns");
      else if (result.bsv21Tokens.length > 0) setActiveTab("bsv21");
      else if (result.bsv20Tokens.length > 0) setActiveTab("bsv20");
      else if (result.locked.length > 0) setActiveTab("locks");
    } catch (e) {
      toast.error(e instanceof Error ? e.message : "Scan failed");
    } finally {
      setScanning(false);
    }
  }, []);

  // --- BSV sweep/send ---
  const handleSweepBsv = useCallback(async () => {
    const wallet = getWallet();
    if (!wallet || !legacyKeys || !assets) return;

    setSweeping(true);

    try {
      let selectedFunding = assets.funding;
      if (sweepAmount !== null) {
        selectedFunding = [];
        let accumulated = 0;
        for (const utxo of assets.funding) {
          selectedFunding.push(utxo);
          accumulated += utxo.satoshis ?? 0;
          if (accumulated >= sweepAmount) break;
        }
      }

      const result = await executeSweep({
        wallet,
        wif: legacyKeys.payPk,
        funding: selectedFunding,
        ordinals: [],
        bsv21Tokens: [],
        amount: sweepAmount ?? undefined,
        onProgress: setSweepProgress,
      });

      setSweepResult(result);

      if (result.errors.length === 0) {
        toast.success("BSV sweep complete!");
      } else {
        toast.warning("BSV sweep completed with errors");
      }
    } catch (e) {
      toast.error(e instanceof Error ? e.message : "BSV sweep failed");
    } finally {
      setSweeping(false);
    }
  }, [legacyKeys, assets, sweepAmount]);

  const handleSendBsv = useCallback(async (destination: string) => {
    if (!legacyKeys || !assets) return;

    setSweeping(true);
    setSweepProgress("Sending BSV...");

    try {
      let selectedFunding = assets.funding;
      if (sweepAmount !== null) {
        selectedFunding = [];
        let accumulated = 0;
        for (const utxo of assets.funding) {
          selectedFunding.push(utxo);
          accumulated += utxo.satoshis ?? 0;
          if (accumulated >= sweepAmount) break;
        }
      }

      const result = await legacySendBsv({
        funding: selectedFunding,
        keys: legacyKeys,
        destination,
        amount: sweepAmount ?? undefined,
      });

      setSweepResult({
        bsvTxid: result.txid,
        ordinalTxids: [],
        bsv21Txids: [],
        errors: [],
      });
      toast.success("BSV sent!");
    } catch (e) {
      toast.error(e instanceof Error ? e.message : "Send failed");
    } finally {
      setSweeping(false);
    }
  }, [legacyKeys, assets, sweepAmount]);

  // --- Ordinals sweep/send ---
  const handleSweepOrdinals = useCallback(async () => {
    const wallet = getWallet();
    if (!wallet || !legacyKeys || !assets) return;

    const selected = assets.ordinals.filter((o) => selectedOrdinals.has(o.outpoint));
    if (selected.length === 0) return;

    setSweeping(true);

    try {
      const result = await executeSweep({
        wallet,
        wif: legacyKeys.payPk,
        funding: [],
        ordinals: selected,
        bsv21Tokens: [],
        onProgress: setSweepProgress,
      });

      setSweepResult(result);

      if (result.errors.length === 0) {
        toast.success(`Swept ${selected.length} ordinal${selected.length !== 1 ? "s" : ""}!`);
      } else {
        toast.warning("Ordinal sweep completed with errors");
      }
    } catch (e) {
      toast.error(e instanceof Error ? e.message : "Ordinal sweep failed");
    } finally {
      setSweeping(false);
    }
  }, [legacyKeys, assets, selectedOrdinals]);

  const handleSendOrdinals = useCallback(async (destination: string) => {
    if (!legacyKeys || !assets) return;

    const selected = assets.ordinals.filter((o) => selectedOrdinals.has(o.outpoint));
    if (selected.length === 0) return;

    setSweeping(true);
    setSweepProgress(`Sending ${selected.length} ordinal${selected.length !== 1 ? "s" : ""}...`);

    try {
      const result = await legacySendOrdinals({
        ordinals: selected,
        funding: assets.funding,
        keys: legacyKeys,
        destination,
      });

      setSweepResult({
        ordinalTxids: [result.txid],
        bsv21Txids: [],
        errors: [],
      });
      toast.success(`Sent ${selected.length} ordinal${selected.length !== 1 ? "s" : ""}!`);
    } catch (e) {
      toast.error(e instanceof Error ? e.message : "Send failed");
    } finally {
      setSweeping(false);
    }
  }, [legacyKeys, assets, selectedOrdinals]);

  // --- OPNS sweep/send (same mechanism as ordinals — they're 1-sat outputs) ---
  const handleSweepOpns = useCallback(async () => {
    const wallet = getWallet();
    if (!wallet || !legacyKeys || !assets) return;

    const selected = assets.opnsNames.filter((o) => selectedOpns.has(o.outpoint));
    if (selected.length === 0) return;

    setSweeping(true);

    try {
      const result = await executeSweep({
        wallet,
        wif: legacyKeys.payPk,
        funding: [],
        ordinals: selected,
        bsv21Tokens: [],
        onProgress: setSweepProgress,
      });

      setSweepResult(result);

      if (result.errors.length === 0) {
        toast.success(`Swept ${selected.length} domain${selected.length !== 1 ? "s" : ""}!`);
      } else {
        toast.warning("OPNS sweep completed with errors");
      }
    } catch (e) {
      toast.error(e instanceof Error ? e.message : "OPNS sweep failed");
    } finally {
      setSweeping(false);
    }
  }, [legacyKeys, assets, selectedOpns]);

  const handleSendOpns = useCallback(async (destination: string) => {
    if (!legacyKeys || !assets) return;

    const selected = assets.opnsNames.filter((o) => selectedOpns.has(o.outpoint));
    if (selected.length === 0) return;

    setSweeping(true);
    setSweepProgress(`Sending ${selected.length} domain${selected.length !== 1 ? "s" : ""}...`);

    try {
      const result = await legacySendOrdinals({
        ordinals: selected,
        funding: assets.funding,
        keys: legacyKeys,
        destination,
      });

      setSweepResult({
        ordinalTxids: [result.txid],
        bsv21Txids: [],
        errors: [],
      });
      toast.success(`Sent ${selected.length} domain${selected.length !== 1 ? "s" : ""}!`);
    } catch (e) {
      toast.error(e instanceof Error ? e.message : "Send failed");
    } finally {
      setSweeping(false);
    }
  }, [legacyKeys, assets, selectedOpns]);

  // --- Ordinals burn ---
  const handleBurnOrdinals = useCallback(async () => {
    if (!legacyKeys || !assets) return;

    const selected = assets.ordinals.filter((o) => selectedOrdinals.has(o.outpoint));
    if (selected.length === 0) return;

    setSweeping(true);
    setSweepProgress(`Burning ${selected.length} ordinal${selected.length !== 1 ? "s" : ""}...`);

    try {
      const result = await legacyBurnOrdinals({
        ordinals: selected,
        funding: assets.funding,
        keys: legacyKeys,
      });

      setSweepResult({
        ordinalTxids: [result.txid],
        bsv21Txids: [],
        errors: [],
      });
      toast.success(`Burned ${selected.length} ordinal${selected.length !== 1 ? "s" : ""}!`);
    } catch (e) {
      toast.error(e instanceof Error ? e.message : "Burn failed");
    } finally {
      setSweeping(false);
    }
  }, [legacyKeys, assets, selectedOrdinals]);

  // --- OPNS burn ---
  const handleBurnOpns = useCallback(async () => {
    if (!legacyKeys || !assets) return;

    const selected = assets.opnsNames.filter((o) => selectedOpns.has(o.outpoint));
    if (selected.length === 0) return;

    setSweeping(true);
    setSweepProgress(`Burning ${selected.length} domain${selected.length !== 1 ? "s" : ""}...`);

    try {
      const result = await legacyBurnOrdinals({
        ordinals: selected,
        funding: assets.funding,
        keys: legacyKeys,
      });

      setSweepResult({
        ordinalTxids: [result.txid],
        bsv21Txids: [],
        errors: [],
      });
      toast.success(`Burned ${selected.length} domain${selected.length !== 1 ? "s" : ""}!`);
    } catch (e) {
      toast.error(e instanceof Error ? e.message : "Burn failed");
    } finally {
      setSweeping(false);
    }
  }, [legacyKeys, assets, selectedOpns]);

  return (
    <div className="min-h-screen bg-background text-foreground">
      <Toaster position="top-right" />
      <div className="mx-auto max-w-lg p-4 space-y-4 py-12">
        <div className="text-center space-y-2 mb-4">
          <h1 className="text-3xl font-bold tracking-tight">1Sat Sweep</h1>
          <p className="text-sm text-muted-foreground">
            Transfer or sweep legacy assets
          </p>
        </div>

        <ConnectWallet
          onConnected={() => setWalletConnected(true)}
          onDisconnected={() => setWalletConnected(false)}
          connected={walletConnected}
        />

        <WifInput
          onScan={handleScan}
          scanning={scanning}
          disabled={sweeping}
        />

        {scanning && (
          <p className="text-sm text-center text-muted-foreground animate-pulse">
            {scanProgress}
          </p>
        )}

        {assets && !sweeping && (
          <div className="space-y-3">
            <FundingSection
              funding={assets.funding}
              totalBsv={assets.totalBsv}
              sweepAmount={sweepAmount}
              onSweepAmountChange={setSweepAmount}
              onSweep={handleSweepBsv}
              onSend={handleSendBsv}
              walletConnected={walletConnected}
            />

            {tabs.length > 0 && (
              <Tabs value={activeTab} onValueChange={(v) => setActiveTab(v as TabId)}>
                <TabsList className="w-full">
                  {tabs.map((tab) => (
                    <TabsTrigger key={tab.id} value={tab.id} className="flex-1 gap-1.5">
                      {tab.label}
                      <Badge variant="secondary" className="text-[10px] px-1.5 py-0">
                        {tab.count}
                      </Badge>
                    </TabsTrigger>
                  ))}
                </TabsList>

                <TabsContent value="ordinals">
                  <OrdinalsSection
                    ordinals={assets.ordinals}
                    selectedOrdinals={selectedOrdinals}
                    onToggle={handleToggleOrdinal}
                    onSelectAll={handleSelectAllOrdinals}
                    onDeselectAll={handleDeselectAllOrdinals}
                    onSweep={handleSweepOrdinals}
                    onSend={handleSendOrdinals}
                    onBurn={handleBurnOrdinals}
                    walletConnected={walletConnected}
                  />
                </TabsContent>

                <TabsContent value="opns">
                  <OpnsSection
                    opnsNames={assets.opnsNames}
                    selectedOpns={selectedOpns}
                    onToggle={handleToggleOpns}
                    onSelectAll={handleSelectAllOpns}
                    onDeselectAll={handleDeselectAllOpns}
                    onSweep={handleSweepOpns}
                    onSend={handleSendOpns}
                    onBurn={handleBurnOpns}
                    walletConnected={walletConnected}
                  />
                </TabsContent>

                <TabsContent value="bsv21">
                  <Bsv21Section tokens={assets.bsv21Tokens} />
                </TabsContent>

                <TabsContent value="bsv20">
                  <Bsv20Section tokens={assets.bsv20Tokens} />
                </TabsContent>

                <TabsContent value="locks">
                  <LockedSection locked={assets.locked} />
                </TabsContent>
              </Tabs>
            )}
          </div>
        )}

        <SweepProgress
          sweeping={sweeping}
          progress={sweepProgress}
          result={sweepResult}
        />

        {sweepResult && !sweeping && (
          <Button variant="outline" onClick={() => setSweepResult(null)} className="w-full gap-2">
            <RefreshCw className="h-4 w-4" />
            Dismiss
          </Button>
        )}
      </div>
    </div>
  );
}
