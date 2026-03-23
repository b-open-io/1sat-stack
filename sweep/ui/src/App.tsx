import { useCallback, useState } from "react";
import { Toaster, toast } from "sonner";
import { ArrowDown, RefreshCw } from "lucide-react";
import { Button } from "@/components/ui/button";
import { ConnectWallet } from "@/components/connect-wallet";
import { WifInput } from "@/components/wif-input";
import {
  FundingSection,
  OrdinalsSection,
  Bsv21Section,
  Bsv20Section,
  LockedSection,
} from "@/components/asset-preview";
import { SweepProgress } from "@/components/sweep-progress";
import { deriveAddress, scanAddresses, type ScannedAssets } from "@/lib/scanner";
import { executeSweep, type SweepResult } from "@/lib/sweeper";
import { getWallet } from "@/lib/wallet";

type AppState = "connect" | "input" | "scanning" | "preview" | "sweeping" | "complete";

export default function App() {
  const [state, setState] = useState<AppState>("connect");
  const [walletConnected, setWalletConnected] = useState(false);
  const [scanning, setScanning] = useState(false);
  const [scanProgress, setScanProgress] = useState("");
  const [assets, setAssets] = useState<ScannedAssets | null>(null);
  const [wifs, setWifs] = useState<{ pay: string; ord: string } | null>(null);
  const [sweeping, setSweeping] = useState(false);
  const [sweepProgress, setSweepProgress] = useState("");
  const [sweepResult, setSweepResult] = useState<SweepResult | null>(null);
  const [selectedOrdinals, setSelectedOrdinals] = useState<Set<string>>(new Set());
  const [sweepAmount, setSweepAmount] = useState<number | null>(null);

  const handleToggleOrdinal = useCallback((outpoint: string) => {
    setSelectedOrdinals((prev) => {
      const next = new Set(prev);
      if (next.has(outpoint)) next.delete(outpoint);
      else next.add(outpoint);
      return next;
    });
  }, []);

  const handleSelectAll = useCallback(() => {
    if (assets) setSelectedOrdinals(new Set(assets.ordinals.map((o) => o.outpoint)));
  }, [assets]);

  const handleDeselectAll = useCallback(() => {
    setSelectedOrdinals(new Set());
  }, []);

  const handleScan = useCallback(async (payWif: string, ordWif: string) => {
    setScanning(true);
    setState("scanning");
    setAssets(null);
    setSweepResult(null);
    setSelectedOrdinals(new Set());
    setSweepAmount(null);
    setWifs({ pay: payWif, ord: ordWif });

    try {
      const payAddr = deriveAddress(payWif);
      const ordAddr = deriveAddress(ordWif);

      const result = await scanAddresses(
        [payAddr, ordAddr],
        (p) => setScanProgress(p.detail ?? p.phase),
      );

      setAssets(result);
      setSelectedOrdinals(new Set(result.ordinals.map((o) => o.outpoint)));

      const total = result.funding.length + result.ordinals.length +
        result.bsv21Tokens.reduce((n, t) => n + t.outputs.length, 0) +
        result.bsv20Tokens.length + result.locked.length;
      if (total === 0) {
        toast.info("No assets found at legacy addresses");
      }
      setState("preview");
    } catch (e) {
      toast.error(e instanceof Error ? e.message : "Scan failed");
      setState("input");
    } finally {
      setScanning(false);
    }
  }, []);

  const handleSweep = useCallback(async () => {
    const wallet = getWallet();
    if (!wallet || !wifs || !assets) return;

    setSweeping(true);
    setState("sweeping");

    try {
      const selectedOrdinalOutputs = assets.ordinals.filter((o) =>
        selectedOrdinals.has(o.outpoint),
      );
      const bsv21Outputs = assets.bsv21Tokens.flatMap((t) => t.outputs);

      // Select funding UTXOs: if amount specified, walk linearly until we have enough
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
        wif: wifs.pay,
        funding: selectedFunding,
        ordinals: selectedOrdinalOutputs,
        bsv21Tokens: bsv21Outputs,
        amount: sweepAmount ?? undefined,
        onProgress: setSweepProgress,
      });

      setSweepResult(result);
      setState("complete");

      if (result.errors.length === 0) {
        toast.success("Sweep complete!");
      } else {
        toast.warning("Sweep completed with some errors");
      }
    } catch (e) {
      toast.error(e instanceof Error ? e.message : "Sweep failed");
      setState("preview");
    } finally {
      setSweeping(false);
    }
  }, [wifs, assets, selectedOrdinals, sweepAmount]);

  const handleReset = useCallback(() => {
    setAssets(null);
    setSweepResult(null);
    setWifs(null);
    setSelectedOrdinals(new Set());
    setSweepAmount(null);
    setState(walletConnected ? "input" : "connect");
  }, [walletConnected]);

  const sweepableCount = assets
    ? assets.funding.length + selectedOrdinals.size +
      assets.bsv21Tokens.reduce((n, t) => n + t.outputs.length, 0)
    : 0;

  return (
    <div className="min-h-screen bg-background text-foreground">
      <Toaster position="top-right" />
      <div className="mx-auto max-w-lg p-4 space-y-4 py-12">
        <div className="text-center space-y-2 mb-8">
          <h1 className="text-3xl font-bold tracking-tight">1Sat Sweep</h1>
          <p className="text-sm text-muted-foreground">
            Sweep legacy assets into your BRC-100 wallet
          </p>
        </div>

        <ConnectWallet
          onConnected={() => { setWalletConnected(true); setState("input"); }}
          onDisconnected={() => { setWalletConnected(false); setState("connect"); }}
          connected={walletConnected}
        />

        {walletConnected && state !== "sweeping" && state !== "complete" && (
          <>
            <div className="flex justify-center">
              <ArrowDown className="h-4 w-4 text-muted-foreground" />
            </div>
            <WifInput
              onScan={handleScan}
              scanning={scanning}
              disabled={!walletConnected}
            />
          </>
        )}

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
            />
            <OrdinalsSection
              ordinals={assets.ordinals}
              selectedOrdinals={selectedOrdinals}
              onToggle={handleToggleOrdinal}
              onSelectAll={handleSelectAll}
              onDeselectAll={handleDeselectAll}
            />
            <Bsv21Section tokens={assets.bsv21Tokens} />
            <Bsv20Section tokens={assets.bsv20Tokens} />
            <LockedSection locked={assets.locked} />

            {sweepableCount > 0 && state === "preview" && (
              <Button onClick={handleSweep} className="w-full h-12 text-base" size="lg">
                Sweep {sweepableCount} Asset{sweepableCount !== 1 ? "s" : ""}
              </Button>
            )}
          </div>
        )}

        <SweepProgress
          sweeping={sweeping}
          progress={sweepProgress}
          result={sweepResult}
        />

        {state === "complete" && (
          <Button variant="outline" onClick={handleReset} className="w-full gap-2">
            <RefreshCw className="h-4 w-4" />
            Sweep Another Wallet
          </Button>
        )}
      </div>
    </div>
  );
}
