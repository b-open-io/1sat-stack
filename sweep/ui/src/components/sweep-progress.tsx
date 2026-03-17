import { CheckCircle2, Loader2, AlertTriangle } from "lucide-react";
import type { SweepResult } from "@/lib/sweeper";
import { truncate } from "@/lib/utils";

interface Props {
  sweeping: boolean;
  progress: string;
  result: SweepResult | null;
}

export function SweepProgress({ sweeping, progress, result }: Props) {
  if (sweeping) {
    return (
      <div className="text-center space-y-4 py-8">
        <Loader2 className="h-8 w-8 animate-spin mx-auto text-primary" />
        <p className="text-sm text-muted-foreground animate-pulse">{progress}</p>
        <p className="text-xs text-destructive/80">Do not close this page.</p>
      </div>
    );
  }

  if (!result) return null;

  const hasErrors = result.errors.length > 0;
  const hasTxids = result.bsvTxid || result.ordinalTxids.length > 0 || result.bsv21Txids.length > 0;

  return (
    <div className="space-y-4 py-4">
      <div className="flex items-center gap-2">
        {hasErrors ? (
          <AlertTriangle className="h-5 w-5 text-yellow-500" />
        ) : (
          <CheckCircle2 className="h-5 w-5 text-green-500" />
        )}
        <span className="font-semibold">
          {hasErrors && !hasTxids ? "Sweep Failed" : hasErrors ? "Sweep Completed with Errors" : "Sweep Complete"}
        </span>
      </div>

      {result.bsvTxid && (
        <div className="flex justify-between text-sm border-b border-border/30 pb-2">
          <span className="text-muted-foreground">BSV Sweep</span>
          <code className="text-xs font-mono">{truncate(result.bsvTxid, 12)}</code>
        </div>
      )}
      {result.ordinalTxids.map((txid) => (
        <div key={txid} className="flex justify-between text-sm border-b border-border/30 pb-2">
          <span className="text-muted-foreground">Ordinal Sweep</span>
          <code className="text-xs font-mono">{truncate(txid, 12)}</code>
        </div>
      ))}
      {result.bsv21Txids.map((txid) => (
        <div key={txid} className="flex justify-between text-sm border-b border-border/30 pb-2">
          <span className="text-muted-foreground">Token Sweep</span>
          <code className="text-xs font-mono">{truncate(txid, 12)}</code>
        </div>
      ))}

      {result.errors.map((err) => (
        <p key={err} className="text-xs text-destructive">{err}</p>
      ))}
    </div>
  );
}
