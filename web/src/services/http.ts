// web/src/services/http.ts
let activeGatewayToken = "";

export function setGatewayToken(token?: string | null): void {
  activeGatewayToken = typeof token === "string" ? token.trim() : "";
}

export function getGatewayToken(): string {
  return activeGatewayToken;
}

export function withDir(path: string, directory?: string): string {
  if (!directory) return path;
  const sep = path.includes("?") ? "&" : "?";
  return `${path}${sep}directory=${encodeURIComponent(directory)}`;
}

export async function requestJSON<T>(url: string, init?: RequestInit): Promise<T> {
  const headers: Record<string, string> = {
    "Content-Type": "application/json",
    ...(activeGatewayToken ? { "X-Hoxi-Token": activeGatewayToken } : {}),
    ...((init?.headers as Record<string, string>) || {}),
  };
  const res = await fetch(url, {
    ...init,
    headers,
  });
  if (!res.ok) {
    const text = await res.text();
    throw new Error(`HTTP ${res.status}: ${text}`);
  }
  if (res.status === 204) return undefined as T;
  return res.json();
}
