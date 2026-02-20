import { AuthFetch } from "@bsv/sdk";

let authFetch: AuthFetch | null = null;
let identityKey: string | null = null;

const API_BASE = window.location.pathname.replace(/\/$/, "") + "/api";

export function isWalletAvailable(): boolean {
  return typeof window.CWI !== "undefined" && window.CWI !== null;
}

export function initAuthFetch(): boolean {
  if (!isWalletAvailable()) return false;
  authFetch = new AuthFetch(window.CWI!);
  return true;
}

export async function fetchIdentityKey(): Promise<string | null> {
  if (!window.CWI) return null;
  try {
    const result = await window.CWI.getPublicKey({ identityKey: true });
    identityKey = result.publicKey;
    return identityKey;
  } catch (e) {
    console.error("Failed to get identity key:", e);
    return null;
  }
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
