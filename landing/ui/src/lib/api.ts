const API_BASE = `${window.location.origin}/1sat`;

export async function fetchHealth(): Promise<{
  status: string;
  version?: string;
  uptime?: number;
  height?: number;
}> {
  const res = await fetch(`${API_BASE}/health`);
  return res.json();
}

export async function fetchCapabilities(): Promise<string[]> {
  const res = await fetch(`${API_BASE}/capabilities`);
  return res.json();
}
