import { useState } from "react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { formatSats, formatTokenAmount } from "@/lib/utils";
import type { EnrichedOrdinal, TokenBalance } from "@/lib/scanner";
import type { IndexedOutput } from "@1sat/types";

const ORDINALS_PER_PAGE = 20;

// ============================================================================
// Ordinal Card
// ============================================================================

function isImageType(ct: string): boolean {
  return ct.startsWith("image/") && ct !== "image/svg+xml";
}

function isIframeType(ct: string): boolean {
  return ct === "image/svg+xml" || ct.startsWith("text/html");
}

function OrdinalCard({
  ordinal,
  isSelected,
  onToggle,
}: {
  ordinal: EnrichedOrdinal;
  isSelected: boolean;
  onToggle: () => void;
}) {
  const ct = ordinal.contentType ?? "";
  const isImage = isImageType(ct);
  const isIframe = isIframeType(ct);
  const subtype = ct.includes("/") ? ct.split("/")[1] : ct;

  return (
    <div
      className={`relative p-2 rounded-lg border cursor-pointer transition-all ${
        isSelected
          ? "border-blue-500 bg-blue-500/10 ring-1 ring-blue-500/30"
          : "border-border/50 hover:border-border bg-black/20"
      }`}
      onClick={onToggle}
    >
      {/* Checkbox */}
      <div className="absolute top-1.5 right-1.5 z-10">
        <div
          className={`w-4 h-4 rounded border-2 flex items-center justify-center text-[10px] ${
            isSelected
              ? "bg-blue-500 border-blue-500 text-white"
              : "border-muted-foreground/40"
          }`}
        >
          {isSelected && "\u2713"}
        </div>
      </div>

      {/* Thumbnail */}
      <div className="w-full aspect-square mb-1.5 rounded overflow-hidden bg-black/30 flex items-center justify-center">
        {isIframe ? (
          <iframe
            src={ordinal.contentUrl}
            title={ordinal.name || "Ordinal"}
            className="w-full h-full border-0 pointer-events-none"
            sandbox="allow-scripts"
            loading="lazy"
          />
        ) : isImage ? (
          <img
            src={ordinal.contentUrl}
            alt={ordinal.name || "Ordinal"}
            className="w-full h-full object-cover"
            loading="lazy"
          />
        ) : ct ? (
          <span className="text-muted-foreground text-lg">{"\uD83D\uDCC4"}</span>
        ) : (
          <span className="text-muted-foreground text-lg">{"\u25C6"}</span>
        )}
      </div>

      {/* Content type badge */}
      {subtype && (
        <div className="mb-1">
          <span className="px-1 py-0.5 text-[9px] rounded bg-blue-500/20 text-blue-400 truncate">
            {subtype}
          </span>
        </div>
      )}

      {/* Name or outpoint */}
      {ordinal.name ? (
        <div className="text-[10px] text-foreground truncate font-medium" title={ordinal.name}>
          {ordinal.name}
        </div>
      ) : (
        <div className="text-[9px] text-muted-foreground truncate font-mono">
          {ordinal.outpoint.substring(0, 8)}...
        </div>
      )}
    </div>
  );
}

// ============================================================================
// Funding Section
// ============================================================================

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

// ============================================================================
// Ordinals Section (with selection + pagination)
// ============================================================================

export function OrdinalsSection({
  ordinals,
  selectedOrdinals,
  onToggle,
  onSelectAll,
  onDeselectAll,
}: {
  ordinals: EnrichedOrdinal[];
  selectedOrdinals: Set<string>;
  onToggle: (outpoint: string) => void;
  onSelectAll: () => void;
  onDeselectAll: () => void;
}) {
  const [page, setPage] = useState(0);

  if (ordinals.length === 0) return null;

  const totalPages = Math.ceil(ordinals.length / ORDINALS_PER_PAGE);
  const start = page * ORDINALS_PER_PAGE;
  const pageItems = ordinals.slice(start, start + ORDINALS_PER_PAGE);

  return (
    <div className="border border-blue-500/20 bg-blue-500/5 p-4 rounded-lg">
      <div className="flex items-start justify-between mb-3">
        <div>
          <div className="flex items-center gap-2 mb-1">
            <span className="h-2 w-2 rounded-full bg-blue-500" />
            <span className="text-sm font-semibold text-blue-500">Ordinals</span>
          </div>
          <div className="text-xs text-muted-foreground">
            {ordinals.length} inscription{ordinals.length !== 1 ? "s" : ""}
            {selectedOrdinals.size > 0 && (
              <span className="text-blue-400 ml-1">({selectedOrdinals.size} selected)</span>
            )}
          </div>
        </div>
        <div className="flex gap-2">
          <Button variant="outline" size="sm" className="h-7 text-[11px]" onClick={onSelectAll}>
            Select All
          </Button>
          <Button
            variant="outline"
            size="sm"
            className="h-7 text-[11px]"
            onClick={onDeselectAll}
            disabled={selectedOrdinals.size === 0}
          >
            Deselect
          </Button>
        </div>
      </div>

      {/* Grid */}
      <div className="grid grid-cols-3 sm:grid-cols-4 md:grid-cols-5 gap-2 mb-3">
        {pageItems.map((ord) => (
          <OrdinalCard
            key={ord.outpoint}
            ordinal={ord}
            isSelected={selectedOrdinals.has(ord.outpoint)}
            onToggle={() => onToggle(ord.outpoint)}
          />
        ))}
      </div>

      {/* Pagination */}
      {totalPages > 1 && (
        <div className="flex items-center justify-center gap-4">
          <Button
            variant="ghost"
            size="sm"
            className="text-xs"
            onClick={() => setPage(page - 1)}
            disabled={page === 0}
          >
            Prev
          </Button>
          <span className="text-xs text-muted-foreground">
            Page {page + 1} of {totalPages}
          </span>
          <Button
            variant="ghost"
            size="sm"
            className="text-xs"
            onClick={() => setPage(page + 1)}
            disabled={page >= totalPages - 1}
          >
            Next
          </Button>
        </div>
      )}
    </div>
  );
}

// ============================================================================
// BSV-21 Tokens Section
// ============================================================================

export function Bsv21Section({ tokens }: { tokens: TokenBalance[] }) {
  if (tokens.length === 0) return null;
  return (
    <div className="border border-purple-500/20 bg-purple-500/5 p-4 rounded-lg">
      <div className="flex items-center gap-2 mb-3">
        <span className="h-2 w-2 rounded-full bg-purple-500" />
        <span className="text-sm font-semibold text-purple-500">BSV-21 Tokens</span>
      </div>
      <div className="space-y-3">
        {tokens.map((tb) => (
          <div
            key={tb.tokenId}
            className="flex items-center justify-between p-3 bg-black/20 rounded-lg border border-purple-500/10"
          >
            <div className="flex items-center gap-3">
              <img
                src={tb.icon}
                alt={tb.symbol || "Token"}
                className="w-8 h-8 rounded-full object-cover"
                onError={(e) => {
                  (e.target as HTMLImageElement).style.display = "none";
                }}
              />
              <div>
                <div className="font-medium text-foreground">
                  {tb.symbol || tb.tokenId.slice(0, 8) + "..."}
                </div>
                <div className="text-xs text-muted-foreground">
                  {formatTokenAmount(tb.totalAmount.toString(), tb.decimals)}{" "}
                  {tb.symbol || ""}
                </div>
              </div>
            </div>
            <Badge variant="secondary">
              {tb.outputs.length} output{tb.outputs.length !== 1 ? "s" : ""}
            </Badge>
          </div>
        ))}
      </div>
    </div>
  );
}

// ============================================================================
// BSV-20 Section (display only)
// ============================================================================

export function Bsv20Section({ tokens }: { tokens: IndexedOutput[] }) {
  if (tokens.length === 0) return null;
  return (
    <div className="border border-muted/30 bg-muted/10 p-4 rounded-lg">
      <div className="flex items-center gap-2 mb-2">
        <span className="h-2 w-2 rounded-full bg-muted-foreground" />
        <span className="text-sm font-semibold text-muted-foreground">BSV-20 Tokens</span>
      </div>
      <p className="text-xs text-muted-foreground mb-2">Cannot be swept automatically.</p>
      <div className="flex flex-wrap gap-2">
        {tokens.slice(0, 10).map((o) => {
          const tickEvent = o.events?.find((e) => e.startsWith("tick:"));
          const tick = tickEvent ? tickEvent.slice(5) : "Token";
          return (
            <span key={o.outpoint} className="px-2 py-1 text-xs rounded bg-muted/30 text-muted-foreground">
              {tick}
            </span>
          );
        })}
        {tokens.length > 10 && (
          <span className="text-xs text-muted-foreground">+{tokens.length - 10} more</span>
        )}
      </div>
    </div>
  );
}

// ============================================================================
// Locked Section (display only)
// ============================================================================

export function LockedSection({ locked }: { locked: IndexedOutput[] }) {
  if (locked.length === 0) return null;
  return (
    <div className="border border-yellow-500/20 bg-yellow-500/5 p-4 rounded-lg">
      <div className="flex items-center gap-2 mb-2">
        <span className="h-2 w-2 rounded-full bg-yellow-500" />
        <span className="text-sm font-semibold text-yellow-500">Locked Outputs</span>
      </div>
      <p className="text-xs text-muted-foreground">
        {locked.length} locked output{locked.length !== 1 ? "s" : ""}. These are in contracts and cannot be swept directly.
      </p>
    </div>
  );
}
