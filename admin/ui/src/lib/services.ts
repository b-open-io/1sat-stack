import { Beef } from "@bsv/sdk";

/**
 * Derives the server base path (e.g. "/1sat") from the current URL.
 */
function getServerBase(): string {
  const adminIdx = window.location.pathname.indexOf("/admin");
  const basePath = adminIdx >= 0 ? window.location.pathname.substring(0, adminIdx) : "";
  return `${window.location.origin}${basePath}`;
}

/**
 * Minimal services adapter for actions that need getBeefForTxid and overlay.submit.
 * Used by sweep and opns actions in the admin UI.
 */
export const adminServices = {
  async getBeefForTxid(txid: string): Promise<Beef> {
    const res = await fetch(`${getServerBase()}/beef/${txid}`);
    if (!res.ok) {
      throw new Error(`Failed to fetch BEEF for ${txid}: ${res.statusText}`);
    }
    const bytes = new Uint8Array(await res.arrayBuffer());
    return Beef.fromBinary(Array.from(bytes));
  },

  overlay: {
    async submit(
      beef: Uint8Array | number[],
      topics: string[],
    ): Promise<{ status: string; txid?: string; message?: string }> {
      const beefArray = beef instanceof Uint8Array ? Array.from(beef) : beef;
      const res = await fetch(`${getServerBase()}/overlay/submit`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ beef: beefArray, topics }),
      });
      if (!res.ok) {
        throw new Error(`Overlay submit failed: ${res.statusText}`);
      }
      return res.json();
    },
  },

  ordfs: {
    async getMetadata(outpoint: string) {
      const res = await fetch(`${getServerBase()}/ordfs/${outpoint}`, {
        method: "HEAD",
      });
      const contentType = res.headers.get("content-type") ?? "application/octet-stream";
      const origin = res.headers.get("x-origin") ?? undefined;
      const mapHeader = res.headers.get("x-map");
      const map = mapHeader ? JSON.parse(mapHeader) : undefined;
      return { contentType, origin, map };
    },

    async bulkMetadata(outpoints: string[]): Promise<Record<string, any>> {
      const res = await fetch(`${getServerBase()}/ordfs/metadata`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ outpoints }),
      });
      if (!res.ok) {
        throw new Error(`Bulk metadata failed: ${res.statusText}`);
      }
      return res.json();
    },
  },
};
