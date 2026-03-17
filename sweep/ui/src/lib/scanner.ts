import { PrivateKey } from "@bsv/sdk";
import type { IndexedOutput } from "@1sat/types";

export interface ScannedAssets {
  funding: IndexedOutput[];
  ordinals: IndexedOutput[];
  bsv21Tokens: IndexedOutput[];
  bsv20Tokens: IndexedOutput[];
  totalBsv: number;
}

export interface ScanProgress {
  phase: string;
  detail?: string;
}

export function deriveAddress(wif: string): string {
  return PrivateKey.fromWif(wif.trim()).toPublicKey().toAddress();
}

function getServerBase(): string {
  const sweepIdx = window.location.pathname.indexOf("/sweep");
  const basePath = sweepIdx >= 0
    ? window.location.pathname.substring(0, sweepIdx)
    : "";
  return `${window.location.origin}${basePath}`;
}

export async function scanAddress(
  address: string,
  onProgress?: (p: ScanProgress) => void,
): Promise<ScannedAssets> {
  const base = getServerBase();

  // Phase 1: Trigger owner sync via SSE
  onProgress?.({ phase: "sync", detail: "Syncing address..." });
  await new Promise<void>((resolve, reject) => {
    const es = new EventSource(`${base}/owner/${address}/txos?refresh=true&limit=1`);
    es.onmessage = (ev) => {
      try {
        const msg = JSON.parse(ev.data);
        if (msg.phase === "done" || msg.phase === "error") {
          es.close();
          if (msg.phase === "error") reject(new Error(msg.error || "Sync failed"));
          else resolve();
        } else if (msg.phase === "fetch" || msg.phase === "ingest") {
          onProgress?.({
            phase: "sync",
            detail: `${msg.phase}: ${msg.processed ?? 0}/${msg.total ?? "?"}`,
          });
        }
      } catch {
        // ignore non-JSON
      }
    };
    es.onerror = () => {
      es.close();
      resolve();
    };
  });

  // Phase 2: Search for all unspent outputs owned by this address
  onProgress?.({ phase: "search", detail: "Searching for assets..." });
  const searchUrl = new URL(`${base}/txo/search`);
  searchUrl.searchParams.append("key", `own:${address}`);
  searchUrl.searchParams.set("unspent", "true");
  searchUrl.searchParams.set("events", "true");
  searchUrl.searchParams.set("sats", "true");

  const res = await fetch(searchUrl.toString());
  if (!res.ok) throw new Error(`Search failed: ${res.statusText}`);
  const results: IndexedOutput[] = await res.json();

  // Phase 3: Categorize
  onProgress?.({ phase: "categorize", detail: "Categorizing assets..." });
  return categorizeOutputs(results);
}

function categorizeOutputs(outputs: IndexedOutput[]): ScannedAssets {
  const funding: IndexedOutput[] = [];
  const ordinals: IndexedOutput[] = [];
  const bsv21Tokens: IndexedOutput[] = [];
  const bsv20Tokens: IndexedOutput[] = [];

  for (const out of outputs) {
    const events = out.events ?? [];
    const types = events
      .filter((e) => e.startsWith("type:"))
      .map((e) => e.slice(5));

    if (types.some((t) => t.includes("bsv21") || t.includes("bsv-21"))) {
      bsv21Tokens.push(out);
    } else if (types.some((t) => t.includes("bsv20") || t.includes("bsv-20"))) {
      bsv20Tokens.push(out);
    } else if (
      events.some((e) => e.startsWith("origin:")) ||
      types.some((t) => t.includes("inscription") || t.includes("ord"))
    ) {
      ordinals.push(out);
    } else {
      funding.push(out);
    }
  }

  return {
    funding,
    ordinals,
    bsv21Tokens,
    bsv20Tokens,
    totalBsv: funding.reduce((sum, o) => sum + (o.satoshis ?? 0), 0),
  };
}

export async function scanAddresses(
  addresses: string[],
  onProgress?: (p: ScanProgress) => void,
): Promise<ScannedAssets> {
  const unique = [...new Set(addresses)];
  const allResults: ScannedAssets[] = [];

  for (const addr of unique) {
    onProgress?.({ phase: "sync", detail: `Scanning ${addr.slice(0, 8)}...` });
    allResults.push(await scanAddress(addr, onProgress));
  }

  return {
    funding: allResults.flatMap((r) => r.funding),
    ordinals: allResults.flatMap((r) => r.ordinals),
    bsv21Tokens: allResults.flatMap((r) => r.bsv21Tokens),
    bsv20Tokens: allResults.flatMap((r) => r.bsv20Tokens),
    totalBsv: allResults.reduce((sum, r) => sum + r.totalBsv, 0),
  };
}
