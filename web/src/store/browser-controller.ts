import { createEffect, createMemo, createSignal, onCleanup, untrack } from "solid-js";
import { BrowserInputBuffer } from "./browser-input";
import { workspace, setWorkspace } from "./workspace-state";
import { sessionDirectory } from "./session-query";
import { t } from "../i18n";
import { createBrowserClipboard } from "./browser-clipboard";
import { createBrowserWorkflows } from "./browser-workflows";
import { createBrowserDeveloper, browserViewportPresets } from "./browser-developer";
import { writeDesktopClipboard } from "../services/desktop-events";
import {
  connectBrowserStream, createChromePairing, executeBrowserAction, fetchBrowserState,
  normalizeBrowserAddress, releaseBrowserPointer, observeBrowserVisibility, decodeBrowserFrame,
  browserLoadPhase, requiresBrowserTakeover,
  type BrowserAction, type BrowserActionResult, type BrowserScope, type BrowserState, type BrowserStreamPhase,
} from "./browser";

interface Connection {
  scope: BrowserScope;
  controller: AbortController;
  tabEpoch: number;
  pageEpoch: number;
  pending: number;
  navPending: number;
  streamDirty: boolean;
  inputs: BrowserInputBuffer;
  pointerUsed: boolean;
  manualRequested: boolean;
  revision: number;
  flushScheduled: boolean;
  queue: Promise<void>;
}

const NAV_ACTIONS = new Set<BrowserAction["action"]>([
  "navigate", "back", "forward", "reload", "tab_new", "tab_select", "tab_close", "close", "control", "mode",
]);
const TAB_EPOCH_ACTIONS = new Set<BrowserAction["action"]>([
  "tab_new", "tab_select", "tab_close", "resize", "close", "mode",
]);

export function createBrowserController() {
  const [state, setState] = createSignal<BrowserState | null>(null);
  const [frame, setFrame] = createSignal<string | null>(null);
  const [url, setURL] = createSignal("");
  const [error, setError] = createSignal("");
  const [pollError, setPollError] = createSignal("");
  const [busy, setBusy] = createSignal(false);
  const [stopping, setStopping] = createSignal(false);
  const [streamPhase, setStreamPhase] = createSignal<BrowserStreamPhase>("connecting");
  const [now, setNow] = createSignal(Date.now());
  const [requestedURL, setRequestedURL] = createSignal("");
  const [pairCode, setPairCode] = createSignal("");
  const [pairExpires, setPairExpires] = createSignal(0);
  const [pairCopied, setPairCopied] = createSignal(false);
  const [visible, setVisible] = createSignal(true);
  const [previewEnabled, setPreviewEnabled] = createSignal(false);
  const [layoutViewport, setViewport] = createSignal<{ width: number; height: number; deviceScaleFactor: number } | null>(null);
  const [streamGeneration, setStreamGeneration] = createSignal(0);
  let frameSize = { width: 0, height: 0 };
  const scope = createMemo(() => {
    const sessionID = workspace.activeSessionID;
    const directory = sessionID ? sessionDirectory(sessionID) : undefined;
    return sessionID && directory ? { sessionID, directory } : null;
  }, null, { equals: (left, right) => left?.sessionID === right?.sessionID && left?.directory === right?.directory });
  let current: Connection | null = null;
  let editingURL = false;
  let frameEpoch = -1;
  const running = () => Boolean(state()?.tabs.length);
  const chromeLinked = () => state()?.mode === "chrome";
  const windowMode = () => {
    const currentState = state();
    return currentState ? currentState.mode === "window" : true;
  };
  const headlessMode = () => !windowMode() && !chromeLinked();
  const effectivePreview = () => headlessMode() || previewEnabled();
  const pairing = () => Boolean(pairCode() || state()?.pairing);
  const selectedTab = createMemo(() => state()?.tabID);
  onCleanup(observeBrowserVisibility(setVisible));

  function replaceFrame(next: string | null) {
    const previous = frame();
    setFrame(next);
    if (previous?.startsWith("blob:")) URL.revokeObjectURL(previous);
  }

  function valid(connection: Connection) {
    const active = scope();
    return current === connection && !connection.controller.signal.aborted &&
      active?.sessionID === connection.scope.sessionID && active?.directory === connection.scope.directory;
  }

  function applyState(next: BrowserState) {
    if (next.mode === "chrome") setPairCode("");
    if (state()?.tabID !== next.tabID) replaceFrame(null);
    setState(next);
    if (!editingURL) {
      const address = next.navigation?.status === "loading" || next.navigation?.status === "failed" ? next.navigation.url : next.url;
      if (address) setURL(address);
    }
    if (!next.tabs.length) replaceFrame(null);
  }

  function enqueueAction(connection: Connection, action: BrowserAction, guard = () => true): Promise<BrowserActionResult | undefined> | undefined {
    if (!valid(connection)) return;
    if (action.action !== "resize") connection.revision++;
    const isNav = NAV_ACTIONS.has(action.action);
    if (TAB_EPOCH_ACTIONS.has(action.action)) connection.tabEpoch++;
    if (["navigate", "back", "forward", "reload", "tab_new", "tab_select", "tab_close", "close"].includes(action.action) ||
      (action.action === "control" && action.manual === false)) connection.pageEpoch++;
    connection.pending++;
    if (TAB_EPOCH_ACTIONS.has(action.action)) {
      connection.streamDirty = true;
      setStreamGeneration(value => value + 1);
    }
    if (isNav) {
      connection.navPending++;
      setBusy(true);
    }
    // The error banner itself changes the viewport height; the resulting
    // layout-driven resize must not dismiss an error the user has not seen.
    if (action.action !== "resize") setError("");
    const result = connection.queue.then(async () => {
      try {
        if (!valid(connection) || !guard()) return;
        if (requiresBrowserTakeover(action) && !state()?.manual) throw new Error(t("inspector.browser.takeoverFailed"));
        const next = await executeBrowserAction(connection.scope, action, connection.controller.signal);
        if (valid(connection)) {
          // Selection text is a transient reply, never part of reactive state.
          const { clipboardText: _text, clipboardError: _error, context: _context, ...page } = next;
          applyState(page);
          if (action.action === "close" || action.action === "mode") { replaceFrame(null); setPairCode(""); }
          if (guard()) return next;
        }
      } catch (err) {
        if (valid(connection)) setError(String(err));
      } finally {
        if (action.action === "control") connection.manualRequested = false;
        connection.pending--;
        if (isNav) {
          connection.navPending--;
          if (valid(connection)) setBusy(connection.navPending > 0);
        }
        if (valid(connection)) {
          flushBufferedInputs(connection);
          if (connection.pending === 0 && connection.streamDirty) {
            connection.streamDirty = false;
            setStreamGeneration(value => value + 1);
          }
        }
      }
    });
    connection.queue = result.then(() => {});
    return result;
  }

  function pageIdentity() {
    // Read reactive sources even before the first connection is attached.
    const active = scope(), page = state();
    const connection = current;
    if (!active || !connection || !valid(connection)) return "";
    return JSON.stringify([connection.scope, connection.pageEpoch, connection.tabEpoch, page?.tabID, page?.url, page?.navigation?.startedAt]);
  }

  async function requestPageAction(action: BrowserAction) {
    const connection = current;
    if (!connection || !isFrameCurrent()) return;
    const identity = pageIdentity();
    const guard = () => pageIdentity() === identity;
    flushBufferedInputs(connection);
    if (!state()?.manual && !connection.manualRequested) {
      connection.manualRequested = true;
      enqueueAction(connection, { action: "control", manual: true }, guard);
    }
    return enqueueAction(connection, action, guard);
  }

  const clipboard = createBrowserClipboard({ identity: pageIdentity, isCurrent: isFrameCurrent,
    request: requestPageAction, send, pageURL: () => state()?.url || "", onError: setError });
  const workflows = createBrowserWorkflows({ scope, tab: () => state()?.tabID,
    enabled: () => workspace.inspectorOpen && workspace.inspectorTab === "browser" && visible() && headlessMode(),
    request: requestPageAction, send, error: setError });
  const developer = createBrowserDeveloper({ scope, tab: () => state()?.tabID, frame,
    enabled: () => workspace.inspectorOpen && workspace.inspectorTab === "browser" && visible(), error: setError });
  const viewport = createMemo(() => {
    const mode = developer.viewportMode();
    return mode === "fit" ? layoutViewport() : browserViewportPresets[mode];
  });

  function flushBufferedInputs(connection: Connection) {
    if (!valid(connection)) return;
    for (const action of connection.inputs.drain()) enqueueAction(connection, action);
  }

  function scheduleInputFlush(connection: Connection) {
    if (connection.flushScheduled || connection.pending > 0) return;
    connection.flushScheduled = true;
    requestAnimationFrame(() => {
      connection.flushScheduled = false;
      if (valid(connection) && connection.pending === 0) {
        flushBufferedInputs(connection);
      }
    });
  }

  function send(action: BrowserAction) {
    // A user gesture proves the panel is interactive even when WKWebView's
    // visibility state lags behind showing/restoring the native window.
    const release = action.action === "pointer" && (action.phase === "cancel" || action.phase === "up");
    if (action.action !== "pointer" || action.phase !== "cancel") setVisible(true);
    const connection = current;
    // Releases are never dropped, or the page would stay pressed.
    if (!connection || !valid(connection) || (stopping() && !release)) return;
    if (requiresBrowserTakeover(action) && !state()?.manual && !connection.manualRequested) {
      connection.manualRequested = true;
      flushBufferedInputs(connection);
      enqueueAction(connection, { action: "control", manual: true });
    }
    if (["navigate", "back", "forward", "reload"].includes(action.action)) {
      action = { ...action, async: !chromeLinked() || state()?.navigationControls === true };
      if (action.url) setRequestedURL(action.url);
    }
    if (!running() && headlessMode() && (action.action === "navigate" || action.action === "tab_new")) {
      const size = viewport();
      if (size) enqueueAction(connection, { action: "resize", ...size });
    }
    if (action.action === "pointer" && action.phase === "down") connection.pointerUsed = true;
    if (connection.inputs.add(action)) {
      scheduleInputFlush(connection);
      return;
    }
    flushBufferedInputs(connection);
    enqueueAction(connection, action);
  }

  function consumePendingBrowserURL() {
    const pendingURL = workspace.inspectedBrowserURL;
    if (!pendingURL || !current || !valid(current)) return;
    setWorkspace("inspectedBrowserURL", null);
    editingURL = false;
    const target = normalizeBrowserAddress(pendingURL);
    if (!target) {
      setURL(pendingURL);
      setError(t("inspector.browser.invalidURL"));
      return;
    }
    setURL(target);
    send({ action: "navigate", url: target });
  }

  async function pairChrome() {
    const connection = current;
    if (!connection || !valid(connection) || connection.pending > 0) return;
    connection.pending++;
    setBusy(true);
    setError("");
    try {
      const result = await createChromePairing(connection.scope, connection.controller.signal);
      if (valid(connection)) { setPairCode(result.code); setPairExpires(Date.now() + result.expiresIn * 1000); setPairCopied(false); }
    } catch (err) {
      if (valid(connection)) setError(String(err));
    } finally {
      connection.pending--;
      if (valid(connection)) setBusy(false);
    }
  }

  async function copyChromeCode() {
    const connection = current;
    if (!connection || !valid(connection) || Date.now() >= pairExpires()) return;
    try { await writeDesktopClipboard(pairCode()); if (valid(connection)) setPairCopied(true); }
    catch { if (valid(connection)) setError(t("inspector.browser.clipboardUnavailable")); }
  }

  async function connectExistingChrome() {
    const connection = current;
    if (!connection || !valid(connection) || busy()) return;
    if (running()) await enqueueAction(connection, { action: "close" });
    if (valid(connection)) await pairChrome();
  }

  createEffect(() => {
    const active = scope();
    // Visibility only pauses background observation, never the command channel.
    const enabled = workspace.inspectorOpen && workspace.inspectorTab === "browser";
    setState(null);
    setURL("");
    setError("");
    setPollError("");
    setBusy(false);
    setStopping(false);
    setRequestedURL("");
    setStreamPhase("connecting");
    setPairCode(""); setPairCopied(false); setPairExpires(0);
    editingURL = false;
    if (!active || !enabled) return;
    const connection: Connection = {
      scope: { ...active },
      controller: new AbortController(),
      tabEpoch: 0,
      pageEpoch: 0,
      pending: 0,
      navPending: 0,
      streamDirty: false,
      inputs: new BrowserInputBuffer(),
      pointerUsed: false,
      manualRequested: false,
      revision: 0,
      flushScheduled: false,
      queue: Promise.resolve(),
    };
    current = connection;
    createEffect(() => {
      streamGeneration();
      const tabID = selectedTab();
      const showPreview = effectivePreview();
      const base = workspace.wsBaseURL || `${location.protocol === "https:" ? "wss:" : "ws:"}//${location.host}`;
      if (!visible() || !tabID || !showPreview) {
        replaceFrame(null);
        return;
      }
      if (connection.pending > 0) { connection.streamDirty = true; return; }
      const epoch = connection.tabEpoch;
      let disposed = false;
      let raf = 0;
      let finish: (() => void) | undefined;
      let receivedFrame = false;
      setStreamPhase(untrack(frame) ? "reconnecting" : "connecting");
      const frameTimer = setTimeout(() => {
        if (!disposed && valid(connection) && !receivedFrame) setStreamPhase("delayed");
      }, 8000);
      const stop = connectBrowserStream(connection.scope, base, async next => {
        if (disposed || !valid(connection) || epoch !== connection.tabEpoch || next.tabID !== state()?.tabID) return;
        const src = await decodeBrowserFrame(next);
        if (disposed || !valid(connection) || epoch !== connection.tabEpoch || next.tabID !== state()?.tabID) return;
        await new Promise<void>(resolve => {
          finish = resolve;
          raf = requestAnimationFrame(() => {
            finish = undefined;
            if (!disposed && valid(connection) && epoch === connection.tabEpoch && next.tabID === state()?.tabID) {
              frameSize = { width: next.width, height: next.height };
              frameEpoch = epoch;
              replaceFrame(src);
              receivedFrame = true;
              clearTimeout(frameTimer);
              setStreamPhase("live");
            }
            resolve();
          });
        });
      }, () => { if (!disposed && valid(connection)) setStreamPhase("reconnecting"); }, status => {
        if (!disposed && valid(connection) && status === "reconnecting") setStreamPhase("reconnecting");
      });
      onCleanup(() => { disposed = true; clearTimeout(frameTimer); stop(); cancelAnimationFrame(raf); finish?.(); });
    });
    createEffect(() => {
      const size = viewport();
      // An idle panel only remembers its size; navigate/tab_new sends it on startup.
      if (size) untrack(() => { if (running() && headlessMode()) enqueueAction(connection, { action: "resize", ...size }); });
    });
    untrack(consumePendingBrowserURL);
    let timer: ReturnType<typeof setTimeout> | undefined;
    async function poll() {
      try {
        if (visible() && connection.pending === 0) {
          const revision = connection.revision;
          const next = await fetchBrowserState(connection.scope, connection.controller.signal);
          if (!valid(connection)) return;
          if (connection.pending === 0 && connection.revision === revision) applyState(next);
        }
        if (valid(connection)) { setPollError(""); setNow(Date.now()); }
      } catch (err) {
        if (valid(connection)) setPollError(String(err));
      } finally {
        if (valid(connection)) timer = setTimeout(poll, 1000);
      }
    }
    // Polling must not subscribe this connection lifecycle to browser state.
    untrack(() => { void poll(); });
    onCleanup(() => {
      connection.controller.abort();
      if (connection.pointerUsed) void connection.queue.finally(() => releaseBrowserPointer(connection.scope));
      clearTimeout(timer);
      if (current === connection) current = null;
      replaceFrame(null);
    });
  });

  createEffect(() => {
    if (workspace.inspectedBrowserURL) {
      consumePendingBrowserURL();
    }
  });

  function typeText(text: string) {
    if (text) send({ action: "type", text });
  }

  function isFrameCurrent() {
    return current !== null && frameEpoch === current.tabEpoch && streamPhase() === "live" && !stopping();
  }

  function navigateAddress(address = url()) {
    if (busy() || pairing() || state()?.connected === false) return;
    const target = normalizeBrowserAddress(address);
    if (!target) { setError(t("inspector.browser.invalidURL")); return; }
    setURL(target);
    editingURL = false;
    send({ action: "navigate", url: target });
  }

  const phase = createMemo(() => browserLoadPhase(state(), busy(), streamPhase(), error() || pollError(), now(), effectivePreview()));
  const loading = () => state()?.navigation?.status === "loading";
  const canStop = () => running() && (!chromeLinked() || state()?.navigationControls === true) && (loading() || busy());

  async function stopLoading() {
    const connection = current;
    if (!connection || !valid(connection) || stopping()) return;
    setStopping(true);
    connection.revision++;
    try {
      await connection.queue;
      if (!valid(connection)) return;
      const next = await executeBrowserAction(connection.scope, { action: "stop_loading" }, connection.controller.signal);
      if (valid(connection)) { applyState(next); setError(""); }
    } catch (err) { if (valid(connection)) setError(String(err)); }
    finally { if (valid(connection)) setStopping(false); }
  }

  function retry() {
    setError(""); setPollError("");
    const nav = state()?.navigation;
    if (!running() || nav?.status === "failed" || nav?.status === "stopped") {
      navigateAddress(nav?.url || requestedURL() || url());
    } else {
      setStreamGeneration(value => value + 1);
    }
  }

  return { scope, state, frame, url, setURL, error, setError, pollError, busy, pairCode,
    running, chromeLinked, windowMode, headlessMode, previewEnabled, effectivePreview,
    togglePreview: () => setPreviewEnabled(value => !value),
    switchMode: (window: boolean) => send({ action: "mode", window }),
    focusWindow: () => send({ action: "focus" }),
    pairing, send, pairChrome, copyChromeCode, pairCopied,
    pairSeconds: () => Math.max(0, Math.ceil((pairExpires() - now()) / 1000)), connectExistingChrome,
    typeText, navigateAddress, isFrameCurrent,
    frameSize: () => frameSize, setViewport, setEditingURL: (value: boolean) => { editingURL = value; },
    phase, streamPhase, loading, stopping, canStop, stopLoading, retry, clipboard, workflows, developer,
  };
}
