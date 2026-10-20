// web/src/store/browser.ts
import {
  browserAPI,
  browserPoint,
  connectBrowserStream,
  type BrowserAction,
  type BrowserFrame,
  type BrowserScope,
  type BrowserState,
} from "../services/browser";

export {
  browserPoint,
  connectBrowserStream,
  type BrowserAction,
  type BrowserFrame,
  type BrowserScope,
  type BrowserState,
};

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
