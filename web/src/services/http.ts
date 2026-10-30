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

const HTTP_ERROR_RE = /^HTTP (\d{3}):\s*([\s\S]*)$/;
const ERROR_TEXT_KEYS = ["error", "message", "msg", "statusMessage", "detail"] as const;

/**
 * Turns an error thrown by requestJSON ("HTTP 403: {\"error\":\"...\"}") into
 * the text a user should read: the server's own message when the body carries
 * one, the plain body otherwise, and `fallback` when there is nothing.
 */
export function errorMessageOf(err: unknown, fallback: string): string {
  const raw = (err instanceof Error ? err.message : typeof err === "string" ? err : "").trim();
  if (!raw) return fallback;
  const match = HTTP_ERROR_RE.exec(raw);
  if (!match) return raw;
  const body = match[2].trim();
  if (!body) return fallback;
  try {
    const parsed: unknown = JSON.parse(body);
    if (parsed && typeof parsed === "object") {
      for (const key of ERROR_TEXT_KEYS) {
        const value = (parsed as Record<string, unknown>)[key];
        if (typeof value === "string" && value.trim()) return value.trim();
        // Some APIs nest it: {"error": {"message": "..."}}.
        if (value && typeof value === "object") {
          const nested = (value as Record<string, unknown>).message;
          if (typeof nested === "string" && nested.trim()) return nested.trim();
        }
      }
      return fallback;
    }
  } catch {
    // Not JSON: the body itself is the message.
  }
  return body.startsWith("<") ? fallback : body;
}
