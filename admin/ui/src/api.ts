import { AuthFetch } from "@bsv/sdk";

let authFetch: AuthFetch | null = null;
let identityKey: string | null = null;

const adminIdx = window.location.pathname.indexOf("/admin");
const API_BASE =
  window.location.origin +
  (adminIdx >= 0 ? window.location.pathname.substring(0, adminIdx + "/admin".length) : "") +
  "/api";

const SETUP_BASE =
  window.location.origin +
  (adminIdx >= 0 ? window.location.pathname.substring(0, adminIdx + "/admin".length) : "") +
  "/setup";

export function isWalletAvailable(): boolean {
  return typeof window.CWI !== "undefined" && window.CWI !== null;
}

export async function connectWallet(): Promise<string> {
  if (!isWalletAvailable()) throw new Error("No wallet detected");
  await window.CWI!.waitForAuthentication({});
  const result = await window.CWI!.getPublicKey({ identityKey: true });
  identityKey = result.publicKey;
  console.log("Creating AuthFetch with wallet:", window.CWI);
  authFetch = new AuthFetch(window.CWI!);
  console.log("AuthFetch created:", authFetch);
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
  const res = await fetch(`${SETUP_BASE}/status`);
  if (!res.ok) throw new Error("Failed to check setup status");
  return res.json();
}

export async function performSetup(): Promise<{ message: string }> {
  console.log("performSetup called, authFetch:", authFetch);
  const url = SETUP_BASE;  // SETUP_BASE already includes /setup
  console.log("Setup URL:", url);
  let res: Response;
  if (authFetch) {
    console.log("Using authFetch");
    res = await authFetch.fetch(url, { method: "POST" });
  } else {
    console.log("authFetch is null, using regular fetch");
    res = await fetch(url, { method: "POST" });
  }
  console.log("Response:", res.status);
  if (!res.ok) {
    const data = await res.json().catch(() => ({}));
    throw new Error(data.error || "Setup failed");
  }
  return res.json();
}
