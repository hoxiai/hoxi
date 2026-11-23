// web/src/store/browser.ts
import { errorMessageOf } from "../services/http";
import { t, type TxKey } from "../i18n";
import {
  browserAPI,
  browserPoint,
  connectBrowserStream,
  observeBrowserZoom,
  observeBrowserInputCancellation,
  observeBrowserVisibility,
  decodeBrowserFrame,
  type BrowserAction,
  type BrowserActionResult,
  type BrowserContext,
  type BrowserFrame,
  type BrowserScope,
  type BrowserState,
} from "../services/browser";

export {
  browserPoint,
  connectBrowserStream,
  observeBrowserZoom,
  observeBrowserInputCancellation,
  observeBrowserVisibility,
  decodeBrowserFrame,
  type BrowserAction,
  type BrowserActionResult,
  type BrowserContext,
  type BrowserFrame,
  type BrowserScope,
  type BrowserState,
};

export async function releaseBrowserPointer(scope: BrowserScope): Promise<void> {
  try { await browserAPI.action(scope, { action: "pointer", phase: "cancel" }, AbortSignal.timeout(5000)); }
  catch { /* The backend also releases abandoned presses on a bounded timer. */ }
}

export type BrowserStreamPhase = "connecting" | "live" | "reconnecting" | "delayed";
export type BrowserLoadPhase = "idle" | "starting" | "loading" | "slow" | "failed" | "stopped" | "connecting" | "reconnecting" | "delayed" | "ready" | "disconnected";

export function browserLoadPhase(state: BrowserState | null, busy: boolean, stream: BrowserStreamPhase, error: string, now: number, previewEnabled = true): BrowserLoadPhase {
  if (state?.connected === false) return "disconnected";
  if (error || state?.navigation?.status === "failed") return "failed";
  if (!state?.tabs.length) return busy ? "starting" : "idle";
  if (state.navigation?.status === "loading") {
    return state.navigation.startedAt && now - state.navigation.startedAt >= 15000 ? "slow" : "loading";
  }
  if (state.navigation?.status === "stopped") return "stopped";
  if (!previewEnabled) return "ready";
  return stream === "live" ? "ready" : stream;
}

export function requiresBrowserTakeover(action: BrowserAction): boolean {
  return ["click", "type", "key", "wheel", "zoom", "zoom_reset", "context", "copy", "paste", "upload_cancel"].includes(action.action) || (action.action === "pointer" && action.phase === "down");
}

import type { SurfaceMode } from "./layout";

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

export function displayBrowserAddress(raw: string | null | undefined): string {
  const address = (raw || "").trim();
  return !address || address === "about:blank" ? "" : address;
}

export function resolveBrowserInspectorSessionID(
  mode: SurfaceMode,
  activeBrowserScopeID: string | null,
  activeSessionID: string | null
): string | null {
  return (mode === "work" ? activeBrowserScopeID : null) || activeSessionID;
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
): Promise<BrowserActionResult> {
  return browserAPI.action(scope, action, signal);
}

export function createChromePairing(scope: BrowserScope, signal: AbortSignal) {
  return browserAPI.pair(scope, signal);
}

export type ChromeConnectPlan = "already_connected" | "close_then_pair" | "pair";

export function resolveChromeConnectPlan(state: BrowserState | null): ChromeConnectPlan {
  if (!state) return "close_then_pair";
  if (state.mode === "chrome") {
    return state.connected === false ? "close_then_pair" : "already_connected";
  }
  return state.tabs.length > 0 ? "close_then_pair" : "pair";
}

const BROWSER_ERROR_KEYS: Record<string, TxKey> = {
  "disconnect the current browser before pairing Chrome": "inspector.browser.errorDisconnectBeforePair",
  "Chrome extension bridge is unavailable": "inspector.browser.errorBridgeUnavailable",
  "cannot create Chrome connection code": "inspector.browser.errorCannotCreatePairCode",
  "Chrome pairing pending; connect the extension or cancel pairing first": "inspector.browser.errorPairingPending",
  "Chrome not connected": "inspector.browser.errorChromeNotConnected",
  "browser not started": "inspector.browser.errorBrowserNotStarted",
  "browser not started; navigate to a URL first": "inspector.browser.errorBrowserNotStarted",
  "browser not running": "inspector.browser.errorBrowserNotStarted",
  "no browser tab open for this session; navigate to a URL first": "inspector.browser.errorNoTabOpen",
  "manual control is active": "inspector.browser.errorManualControlActive",
  "use the native Chrome file dialog": "inspector.browser.errorUseNativeChromeDialog",
  "use Chrome's native upload and download dialogs for Chrome Link": "inspector.browser.errorUseNativeChromeDialog",
  "reload Hoxi Browser Link to reset page zoom": "inspector.browser.errorReloadChromeExtension",
  "reload Hoxi Browser Link in chrome://extensions to use the clipboard and context menu": "inspector.browser.errorReloadChromeExtension",
  "reload Hoxi Browser Link in chrome://extensions to use loading controls": "inspector.browser.errorReloadChromeExtension",
  "reload Hoxi Browser Link to use browser diagnostics": "inspector.browser.errorReloadChromeExtension",
  "reload Hoxi Browser Link in chrome://extensions to use browser diagnostics": "inspector.browser.errorReloadChromeExtension",
  "take manual control before uploading": "inspector.browser.errorTakeManualControlBeforeUpload",
  "page dialog expired": "inspector.browser.errorPageDialogExpired",
  "cannot answer page dialog": "inspector.browser.errorCannotAnswerPageDialog",
  "download not found in this browser": "inspector.browser.errorDownloadNotFound",
  "native save dialog requires the desktop app": "inspector.browser.errorNativeSaveRequiresDesktop",
};

export function formatBrowserError(err: unknown): string {
  const message = errorMessageOf(err, t("inspector.browser.errorUnknown"));
  const key = BROWSER_ERROR_KEYS[message];
  return key ? t(key) : message;
}
