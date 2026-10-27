// web/src/store/browser.ts
import {
  browserAPI,
  browserPoint,
  connectBrowserStream,
  observeBrowserZoom,
  observeBrowserInputCancellation,
  type BrowserAction,
  type BrowserFrame,
  type BrowserScope,
  type BrowserState,
} from "../services/browser";

export {
  browserPoint,
  connectBrowserStream,
  observeBrowserZoom,
  observeBrowserInputCancellation,
  type BrowserAction,
  type BrowserFrame,
  type BrowserScope,
  type BrowserState,
};

export async function releaseBrowserPointer(scope: BrowserScope): Promise<void> {
  try { await browserAPI.action(scope, { action: "pointer", phase: "cancel" }, AbortSignal.timeout(5000)); }
  catch { /* The backend also releases abandoned presses on a bounded timer. */ }
}

// Address-bar input is more permissive than the browser API's explicit URLs.
// Keep normalization on the UI side; agent requests retain strict validation.
export function normalizeBrowserAddress(raw: string): string | null {
  const address = raw.trim();
  if (!address || address === "about:blank") return "about:blank";
  try {
    let target: URL;
    if (/^https?:\/\//i.test(address)) {
      target = new URL(address);
    } else {
      const authority = address.split(/[/?#]/, 1)[0];
      const hasPort = /^(?:[^:]+|\[[\da-f:]+\]):\d+$/i.test(authority);
      if (/^[a-z][a-z\d+.-]*:/i.test(address) && !hasPort) return null;
      target = new URL(`https:${address.startsWith("//") ? address : `//${address}`}`);
      const host = target.hostname.toLowerCase();
      const local = host === "localhost" || host.endsWith(".localhost") ||
        /^(?:\d+\.){3}\d+$/.test(host) || host.startsWith("[");
      if (!local && !host.includes(".") && !hasPort) return null;
      if (local || hasPort) target.protocol = "http:";
    }
    if (!["http:", "https:"].includes(target.protocol) || !target.hostname || target.username || target.password) return null;
    return target.href;
  } catch {
    return null;
  }
}

export function fetchBrowserState(
  scope: BrowserScope,
  signal: AbortSignal
): Promise<BrowserState> {
  return browserAPI.state(scope, signal);
}

export function executeBrowserAction(
  scope: BrowserScope,
  action: BrowserAction,
  signal: AbortSignal
): Promise<BrowserState> {
  return browserAPI.action(scope, action, signal);
}

export function createChromePairing(scope: BrowserScope, signal: AbortSignal) {
  return browserAPI.pair(scope, signal);
}
