import { PrivateKey } from "@bsv/sdk";
import type { IndexedOutput } from "@1sat/types";
import { getServices } from "./services";

export interface EnrichedOrdinal extends IndexedOutput {
  origin?: string;
  contentType?: string;
  name?: string;
  contentUrl: string;
}

export interface TokenBalance {
  tokenId: string;
  symbol?: string;
  icon: string;
  decimals: number;
  totalAmount: bigint;
  outputs: IndexedOutput[];
}

export interface ScannedAssets {
  funding: IndexedOutput[];
  ordinals: EnrichedOrdinal[];
  bsv21Tokens: TokenBalance[];
  bsv20Tokens: IndexedOutput[];
  locked: IndexedOutput[];
  totalBsv: number;
}

export interface ScanProgress {
  phase: string;
  detail?: string;
}

export function deriveAddress(wif: string): string {
  return PrivateKey.fromWif(wif.trim()).toPublicKey().toAddress();
}

/** Extract event value by prefix, e.g. getEvent(events, "origin:") */
function getEvent(events: string[], prefix: string): string | undefined {
  const e = events.find((e) => e.startsWith(prefix));
  return e ? e.slice(prefix.length) : undefined;
}

/** Get all events matching a prefix */
function getEvents(events: string[], prefix: string): string[] {
  return events.filter((e) => e.startsWith(prefix)).map((e) => e.slice(prefix.length));
}

function enrichOrdinal(out: IndexedOutput): EnrichedOrdinal {
  const events = out.events ?? [];
  const origin = getEvent(events, "origin:");
  const types = getEvents(events, "type:");
  const contentType = types.find((t) => t.includes("/")) ?? types[0];
  const name = getEvent(events, "name:");
  const contentUrl = getServices().ordfs.getContentUrl(origin ?? out.outpoint);

  return { ...out, origin, contentType, name, contentUrl };
}

function groupBsv21Tokens(outputs: IndexedOutput[]): TokenBalance[] {
  const groups = new Map<string, { outputs: IndexedOutput[]; totalAmount: bigint }>();

  for (const out of outputs) {
    const events = out.events ?? [];
    const tokenId = getEvent(events, "bsv21:");
    if (!tokenId) continue;

    const amtStr = getEvent(events, "amt:");
    const amount = amtStr ? BigInt(amtStr) : 0n;

    let group = groups.get(tokenId);
    if (!group) {
      group = { outputs: [], totalAmount: 0n };
      groups.set(tokenId, group);
    }
    group.outputs.push(out);
    group.totalAmount += amount;
  }

  const balances: TokenBalance[] = [];
  for (const [tokenId, group] of groups) {
    balances.push({
      tokenId,
      icon: getServices().ordfs.getContentUrl(tokenId),
      decimals: 0,
      totalAmount: group.totalAmount,
      outputs: group.outputs,
    });
  }
  return balances;
}

function categorizeOutputs(outputs: IndexedOutput[]): ScannedAssets {
  const funding: IndexedOutput[] = [];
  const rawOrdinals: IndexedOutput[] = [];
  const bsv21Raw: IndexedOutput[] = [];
  const bsv20Tokens: IndexedOutput[] = [];
  const locked: IndexedOutput[] = [];

  for (const out of outputs) {
    const events = out.events ?? [];
    const sats = out.satoshis ?? 0;

    if (events.some((e) => e.startsWith("bsv21:"))) {
      bsv21Raw.push(out);
      continue;
    }

    if (events.some((e) => e.startsWith("lock:"))) {
      locked.push(out);
      continue;
    }

    if (events.some((e) => e === "type:application/bsv-20" || e === "type:Token")) {
      bsv20Tokens.push(out);
      continue;
    }

    if (sats === 1) {
      rawOrdinals.push(out);
      continue;
    }

    if (sats > 1) {
      funding.push(out);
    }
  }

  return {
    funding,
    ordinals: rawOrdinals.map(enrichOrdinal),
    bsv21Tokens: groupBsv21Tokens(bsv21Raw),
    bsv20Tokens,
    locked,
    totalBsv: funding.reduce((sum, o) => sum + (o.satoshis ?? 0), 0),
  };
}

export async function scanAddress(
  address: string,
  onProgress?: (p: ScanProgress) => void,
): Promise<ScannedAssets> {
  const services = getServices();

  // Phase 1: Trigger owner sync via SSE stream
  onProgress?.({ phase: "sync", detail: "Syncing address..." });
  for await (const event of services.owner.getTxos(address, { refresh: true, limit: 1 })) {
    if (event.type === "sync") {
      const p = event.data;
      onProgress?.({
        phase: "sync",
        detail: `${p.phase}: ${p.processed ?? 0}/${p.total ?? "?"}`,
      });
    } else if (event.type === "done" || event.type === "error") {
      break;
    }
  }

  // Phase 2: Search for all unspent outputs
  onProgress?.({ phase: "search", detail: "Searching for assets..." });
  const allOutputs = await services.txo.search(`own:${address}`, {
    unspent: true,
    events: true,
    sats: true,
    limit: 0,
  });

  // Phase 3: Categorize
  onProgress?.({ phase: "categorize", detail: "Categorizing assets..." });
  return categorizeOutputs(allOutputs);
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
    locked: allResults.flatMap((r) => r.locked),
    totalBsv: allResults.reduce((sum, r) => sum + r.totalBsv, 0),
  };
}
