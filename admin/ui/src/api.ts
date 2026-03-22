import { AuthFetch } from "@bsv/sdk";
import type { WalletInterface } from "@bsv/sdk";

let authFetch: AuthFetch | null = null;

const adminIdx = window.location.pathname.indexOf("/admin");
const API_BASE =
  window.location.origin +
  (adminIdx >= 0 ? window.location.pathname.substring(0, adminIdx + "/admin".length) : "") +
  "/api";

const SETUP_BASE =
  window.location.origin +
  (adminIdx >= 0 ? window.location.pathname.substring(0, adminIdx + "/admin".length) : "") +
  "/setup";

export function setWallet(w: WalletInterface | null): void {
  authFetch = w ? new AuthFetch(w) : null;
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
  if (authFetch) {
    const res = await authFetch.fetch(url, { method: "POST" });
    if (!res.ok) {
      const data = await res.json().catch(() => ({}));
      throw new Error(data.error || "Setup failed");
    }
    return res.json();
  }
  const res = await fetch(url, { method: "POST" });
  if (!res.ok) {
    const data = await res.json().catch(() => ({}));
    throw new Error(data.error || "Setup failed");
  }
  return res.json();
}

export async function checkAccess(): Promise<{ status: string; admin?: boolean }> {
  const url = `${SETUP_BASE}/check`;
  if (authFetch) {
    const res = await authFetch.fetch(url, { method: "GET" });
    if (!res.ok) throw new Error("Failed to check access");
    return res.json();
  }
  const res = await fetch(url);
  if (!res.ok) throw new Error("Failed to check access");
  return res.json();
}

export async function getConfig(): Promise<Record<string, string>> {
  const res = await apiFetch("/config");
  if (!res.ok) throw new Error("Failed to load config");
  return res.json();
}

export async function saveConfig(values: Record<string, string>): Promise<void> {
  const res = await apiFetch("/config", {
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(values),
  });
  if (!res.ok) throw new Error("Failed to save config");
}

export async function requestAccess(name: string): Promise<{ message: string }> {
  const url = `${SETUP_BASE}/request`;
  if (authFetch) {
    const res = await authFetch.fetch(url, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ name }),
    });
    if (!res.ok) {
      const data = await res.json().catch(() => ({}));
      throw new Error(data.error || "Request failed");
    }
    return res.json();
  }
  const res = await fetch(url, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ name }),
  });
  if (!res.ok) {
    const data = await res.json().catch(() => ({}));
    throw new Error(data.error || "Request failed");
  }
  return res.json();
}
