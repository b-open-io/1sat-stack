import { Badge } from "@/components/ui/badge";
import { formatSats } from "@/lib/utils";
import type { IndexedOutput } from "@1sat/types";

export function FundingSection({ funding, totalBsv }: { funding: IndexedOutput[]; totalBsv: number }) {
  if (funding.length === 0) return null;
  return (
    <div className="border border-green-500/20 bg-green-500/5 p-4 rounded-lg">
      <div className="flex items-center gap-2 mb-2">
        <span className="h-2 w-2 rounded-full bg-green-500" />
        <span className="text-sm font-semibold text-green-500">BSV Funding</span>
      </div>
      <div className="flex items-baseline justify-between">
        <div>
          <div className="text-2xl font-bold text-green-500">{formatSats(totalBsv)} sats</div>
          <div className="text-xs text-muted-foreground">{(totalBsv / 100_000_000).toFixed(8)} BSV</div>
        </div>
        <Badge variant="secondary">{funding.length} UTXO{funding.length !== 1 ? "s" : ""}</Badge>
      </div>
    </div>
  );
}

export function OrdinalsSection({ ordinals }: { ordinals: IndexedOutput[] }) {
  if (ordinals.length === 0) return null;
  return (
    <div className="border border-blue-500/20 bg-blue-500/5 p-4 rounded-lg">
      <div className="flex items-center gap-2 mb-2">
        <span className="h-2 w-2 rounded-full bg-blue-500" />
        <span className="text-sm font-semibold text-blue-500">Ordinals</span>
      </div>
      <div className="flex items-baseline justify-between">
        <span className="text-sm text-muted-foreground">
          {ordinals.length} inscription{ordinals.length !== 1 ? "s" : ""}
        </span>
        <Badge variant="secondary">{ordinals.length}</Badge>
      </div>
    </div>
  );
}

export function Bsv21Section({ tokens }: { tokens: IndexedOutput[] }) {
  if (tokens.length === 0) return null;
  return (
    <div className="border border-purple-500/20 bg-purple-500/5 p-4 rounded-lg">
      <div className="flex items-center gap-2 mb-2">
        <span className="h-2 w-2 rounded-full bg-purple-500" />
        <span className="text-sm font-semibold text-purple-500">BSV-21 Tokens</span>
      </div>
      <div className="flex items-baseline justify-between">
        <span className="text-sm text-muted-foreground">
          {tokens.length} token output{tokens.length !== 1 ? "s" : ""}
        </span>
        <Badge variant="secondary">{tokens.length}</Badge>
      </div>
    </div>
  );
}

export function Bsv20Section({ tokens }: { tokens: IndexedOutput[] }) {
  if (tokens.length === 0) return null;
  return (
    <div className="border border-muted/30 bg-muted/10 p-4 rounded-lg">
      <div className="flex items-center gap-2 mb-2">
        <span className="h-2 w-2 rounded-full bg-muted-foreground" />
        <span className="text-sm font-semibold text-muted-foreground">BSV-20 Tokens</span>
      </div>
      <p className="text-xs text-muted-foreground">
        {tokens.length} BSV-20 token{tokens.length !== 1 ? "s" : ""} found. Cannot be swept automatically.
      </p>
    </div>
  );
}
