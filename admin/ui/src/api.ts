import { AuthFetch, WalletClient } from "@bsv/sdk";
import type { WalletInterface } from "@bsv/sdk";
import { createWebCWI } from "@1sat/wallet";

let wallet: WalletInterface | null = null;
let authFetch: AuthFetch | null = null;
let identityKey: string | null = null;
let destroyCWI: (() => void) | null = null;

const adminIdx = window.location.pathname.indexOf("/admin");
const API_BASE =
  window.location.origin +
  (adminIdx >= 0 ? window.location.pathname.substring(0, adminIdx + "/admin".length) : "") +
  "/api";

const SETUP_BASE =
  window.location.origin +
  (adminIdx >= 0 ? window.location.pathname.substring(0, adminIdx + "/admin".length) : "") +
  "/setup";

export async function connectWallet(): Promise<string> {
  const client = new WalletClient("auto");
  await client.connectToSubstrate();
  wallet = client;
  await wallet.waitForAuthentication({});
  const result = await wallet.getPublicKey({ identityKey: true });
  identityKey = result.publicKey;
  authFetch = new AuthFetch(wallet);
  return identityKey;
}

export function getWallet(): WalletInterface | null {
  return wallet;
}

export function getIdentityKey(): string | null {
  return identityKey;
}

export async function connectOneSatWallet(): Promise<string> {
  const { wallet: w, destroy } = createWebCWI();
  wallet = w;
  destroyCWI = destroy;
  await w.waitForAuthentication({});
  const result = await w.getPublicKey({ identityKey: true });
  identityKey = result.publicKey;
  authFetch = new AuthFetch(w);
  return identityKey;
}

export function disconnectWallet(): void {
  if (destroyCWI) {
    destroyCWI();
    destroyCWI = null;
  }
  wallet = null;
  authFetch = null;
  identityKey = null;
}

export async function apiFetch(
  path: string,
  options?: RequestInit,
): Promise<Response> {
  const url = `${API_BASE}${path}`;
  if (authFetch) {
    return authFetch.fetch(url, {
      method: options?.method,
      headers: options?.headers as Record<string, string>,
      body: options?.body,
    });
  }
  return fetch(url, options);
}

export async function getSetupStatus(): Promise<{ configured: boolean }> {
  const res = await fetch(`${SETUP_BASE}/status`);
  if (!res.ok) throw new Error("Failed to check setup status");
  return res.json();
}

export async function performSetup(): Promise<{ message: string }> {
  const url = SETUP_BASE;
  let res: Response;
  if (authFetch) {
    res = await authFetch.fetch(url, { method: "POST" });
  } else {
    res = await fetch(url, { method: "POST" });
  }
  if (!res.ok) {
    const data = await res.json().catch(() => ({}));
    throw new Error(data.error || "Setup failed");
  }
  return res.json();
}

export async function checkAccess(): Promise<{ status: string; admin?: boolean }> {
  const url = `${SETUP_BASE}/check`;
  let res: Response;
  if (authFetch) {
    res = await authFetch.fetch(url, { method: "GET" });
  } else {
    res = await fetch(url);
  }
  if (!res.ok) throw new Error("Failed to check access");
  return res.json();
}

export async function requestAccess(name: string): Promise<{ message: string }> {
  const url = `${SETUP_BASE}/request`;
  let res: Response;
  if (authFetch) {
    res = await authFetch.fetch(url, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ name }),
    });
  } else {
    res = await fetch(url, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ name }),
    });
  }
  if (!res.ok) {
    const data = await res.json().catch(() => ({}));
    throw new Error(data.error || "Request failed");
  }
  return res.json();
}
