import { AuthFetch } from "@bsv/sdk";

let authFetch: AuthFetch | null = null;
let identityKey: string | null = null;

const adminIdx = window.location.pathname.indexOf("/admin");
const API_BASE =
  window.location.origin +
  (adminIdx >= 0 ? window.location.pathname.substring(0, adminIdx + "/admin".length) : "") +
  "/api";

export function isWalletAvailable(): boolean {
  return typeof window.CWI !== "undefined" && window.CWI !== null;
}

export async function connectWallet(): Promise<string> {
  if (!isWalletAvailable()) throw new Error("No wallet detected");
  await window.CWI!.waitForAuthentication({});
  const result = await window.CWI!.getPublicKey({ identityKey: true });
  identityKey = result.publicKey;
  authFetch = new AuthFetch(window.CWI!);
  return identityKey;
}

export function getIdentityKey(): string | null {
  return identityKey;
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
  const res = await fetch(`${API_BASE}/status`);
  if (!res.ok) throw new Error("Failed to check setup status");
  return res.json();
}

export async function performSetup(): Promise<{ message: string }> {
  const res = await apiFetch("/setup", { method: "POST" });
  if (!res.ok) {
    const data = await res.json().catch(() => ({}));
    throw new Error(data.error || "Setup failed");
  }
  return res.json();
}
