import { createEffect, createMemo, createSignal, For, onCleanup, Show, untrack } from "solid-js";
import { ArrowLeft, ArrowRight, ExternalLink, Globe, Plus, Power, RefreshCw, X } from "lucide-solid";
import {
  browserPoint,
  connectBrowserStream,
  createChromePairing,
  executeBrowserAction,
  fetchBrowserState,
  normalizeBrowserAddress,
  openExternalURL,
  sessionDirectory,
  setWorkspace,
  workspace,
  type BrowserAction,
  type BrowserScope,
  type BrowserState,
} from "../../store/workspace";
import { t } from "../../i18n";

interface PendingScroll {
  x: number;
  y: number;
  deltaX: number;
  deltaY: number;
}

interface Connection {
  scope: BrowserScope;
  controller: AbortController;
  tabEpoch: number;
  pending: number;
  navPending: number;
  streamDirty: boolean;
  pendingScroll: PendingScroll | null;
  pendingType: string;
  flushScheduled: boolean;
  queue: Promise<void>;
}

const NAV_ACTIONS = new Set<BrowserAction["action"]>([
  "navigate", "back", "forward", "reload", "tab_new", "tab_select", "tab_close", "close", "control",
]);
const TAB_EPOCH_ACTIONS = new Set<BrowserAction["action"]>([
  "tab_new", "tab_select", "tab_close", "resize", "close",
]);

export default function BrowserInspector() {
  const [state, setState] = createSignal<BrowserState | null>(null);
  const [frame, setFrame] = createSignal<string | null>(null);
  const [url, setURL] = createSignal("");
  const [error, setError] = createSignal("");
  const [pollError, setPollError] = createSignal("");
  const [busy, setBusy] = createSignal(false);
  const [pairCode, setPairCode] = createSignal("");
  const [visible, setVisible] = createSignal(!document.hidden);
  const [viewport, setViewport] = createSignal<{ width: number; height: number; deviceScaleFactor: number } | null>(null);
  const [streamGeneration, setStreamGeneration] = createSignal(0);
  let frameSize = { width: 0, height: 0 };
  const scope = createMemo(() => {
    const sessionID = workspace.activeSessionID;
    const directory = sessionID ? sessionDirectory(sessionID) : undefined;
    return sessionID && directory ? { sessionID, directory } : null;
  }, null, { equals: (left, right) => left?.sessionID === right?.sessionID && left?.directory === right?.directory });
  let current: Connection | null = null;
  let input: HTMLTextAreaElement | undefined;
  let composing = false;
  let editingURL = false;
  let composingURL = false;
  let frameEpoch = -1;
  const iconBtn = "shrink-0 rounded p-1 hover:bg-[var(--bg-hover)] disabled:opacity-40 cursor-pointer transition-colors";
  const running = () => Boolean(state()?.tabs.length);
  const chromeLinked = () => state()?.mode === "chrome";
  const pairing = () => Boolean(pairCode() || state()?.pairing);
  const selectedTab = createMemo(() => state()?.tabID);
  const visibility = () => setVisible(!document.hidden);
  document.addEventListener("visibilitychange", visibility);
  onCleanup(() => document.removeEventListener("visibilitychange", visibility));

  function observeViewport(element: HTMLDivElement) {
    let timer: ReturnType<typeof setTimeout> | undefined;
    const observer = new ResizeObserver(([entry]) => {
      clearTimeout(timer);
      const width = Math.min(4096, Math.floor(entry.contentRect.width));
      const height = Math.min(4096, Math.floor(entry.contentRect.height));
      if (width < 1 || height < 1) return;
      // Bound screenshot memory while retaining the panel's aspect ratio.
      const scale = Math.min(1, Math.sqrt(8388608 / (width * height)));
      timer = setTimeout(() => {
        const next = { width: Math.max(1, Math.floor(width * scale)), height: Math.max(1, Math.floor(height * scale)), deviceScaleFactor: 1 };
        next.deviceScaleFactor = Math.max(1, Math.min(2, window.devicePixelRatio || 1,
          Math.sqrt(8388608 / ((next.width + 1) * (next.height + 1)))));
        setViewport(previous => previous?.width === next.width && previous.height === next.height && previous.deviceScaleFactor === next.deviceScaleFactor ? previous : next);
      }, 100);
    });
    observer.observe(element);
    onCleanup(() => { observer.disconnect(); clearTimeout(timer); });
  }

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
    if (!editingURL) setURL(next.url || "");
    if (!next.tabs.length) replaceFrame(null);
  }

  function enqueueAction(connection: Connection, action: BrowserAction) {
    if (!valid(connection)) return;
    const isNav = NAV_ACTIONS.has(action.action);
    if (TAB_EPOCH_ACTIONS.has(action.action)) connection.tabEpoch++;
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
    connection.queue = connection.queue.then(async () => {
      if (!valid(connection)) return;
      try {
        const next = await executeBrowserAction(connection.scope, action, connection.controller.signal);
        if (valid(connection)) {
          applyState(next);
          if (action.action === "close") { replaceFrame(null); setPairCode(""); }
        }
      } catch (err) {
        if (valid(connection)) setError(String(err));
      } finally {
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
  }

  function flushBufferedInputs(connection: Connection) {
    if (!valid(connection)) return;
    if (connection.pendingScroll) {
      const scroll = connection.pendingScroll;
      connection.pendingScroll = null;
      enqueueAction(connection, { action: "scroll", ...scroll });
    }
    if (connection.pendingType) {
      const text = connection.pendingType;
      connection.pendingType = "";
      enqueueAction(connection, { action: "type", text });
    }
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
    setVisible(true);
    const connection = current;
    if (!connection || !valid(connection)) return;
    if (!running() && (action.action === "navigate" || action.action === "tab_new")) {
      const size = viewport();
      if (size) enqueueAction(connection, { action: "resize", ...size });
    }
    if (action.action === "scroll") {
      const dx = action.deltaX ?? 0;
      const dy = action.deltaY ?? 0;
      if (connection.pendingScroll) {
        connection.pendingScroll.x = action.x ?? connection.pendingScroll.x;
        connection.pendingScroll.y = action.y ?? connection.pendingScroll.y;
        connection.pendingScroll.deltaX += dx;
        connection.pendingScroll.deltaY += dy;
      } else {
        connection.pendingScroll = { x: action.x ?? 0, y: action.y ?? 0, deltaX: dx, deltaY: dy };
      }
      scheduleInputFlush(connection);
      return;
    }
    if (action.action === "type") {
      if (connection.pendingScroll) {
        const scroll = connection.pendingScroll;
        connection.pendingScroll = null;
        enqueueAction(connection, { action: "scroll", ...scroll });
      }
      connection.pendingType += action.text ?? "";
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
      if (valid(connection)) setPairCode(result.code);
    } catch (err) {
      if (valid(connection)) setError(String(err));
    } finally {
      connection.pending--;
      if (valid(connection)) setBusy(false);
    }
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
    setPairCode("");
    editingURL = false;
    composingURL = false;
    composing = false;
    if (input) input.value = "";
    if (!active || !enabled) return;
    const connection: Connection = {
      scope: { ...active },
      controller: new AbortController(),
      tabEpoch: 0,
      pending: 0,
      navPending: 0,
      streamDirty: false,
      pendingScroll: null,
      pendingType: "",
      flushScheduled: false,
      queue: Promise.resolve(),
    };
    current = connection;
    createEffect(() => {
      streamGeneration();
      const tabID = selectedTab();
      const base = workspace.wsBaseURL || `${location.protocol === "https:" ? "wss:" : "ws:"}//${location.host}`;
      if (!visible() || !tabID) return;
      if (connection.pending > 0) { connection.streamDirty = true; return; }
      const epoch = connection.tabEpoch;
      let disposed = false;
      let raf = 0;
      let finish: (() => void) | undefined;
      const stop = connectBrowserStream(connection.scope, base, async next => {
        if (disposed || !valid(connection) || epoch !== connection.tabEpoch || next.tabID !== state()?.tabID) return;
        const src = `data:image/jpeg;base64,${next.image}`;
        const image = new Image();
        image.src = src;
        await image.decode();
        if (disposed || !valid(connection) || epoch !== connection.tabEpoch || next.tabID !== state()?.tabID) return;
        await new Promise<void>(resolve => {
          finish = resolve;
          raf = requestAnimationFrame(() => {
            finish = undefined;
            if (!disposed && valid(connection) && epoch === connection.tabEpoch && next.tabID === state()?.tabID) {
              frameSize = { width: next.width, height: next.height };
              frameEpoch = epoch;
              replaceFrame(src);
              setPollError("");
            }
            resolve();
          });
        });
      }, message => { if (!disposed && valid(connection)) setPollError(message); });
      onCleanup(() => { disposed = true; stop(); cancelAnimationFrame(raf); finish?.(); });
    });
    createEffect(() => {
      const size = viewport();
      // An idle panel only remembers its size; navigate/tab_new sends it on startup.
      if (size) untrack(() => { if (running() && !chromeLinked()) enqueueAction(connection, { action: "resize", ...size }); });
    });
    untrack(consumePendingBrowserURL);
    let timer: ReturnType<typeof setTimeout> | undefined;
    async function poll() {
      try {
        if (visible() && connection.pending === 0) {
          const next = await fetchBrowserState(connection.scope, connection.controller.signal);
          if (!valid(connection)) return;
          if (connection.pending === 0) applyState(next);
        }
        setPollError("");
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

  function navigateAddress(address = url()) {
    if (busy() || pairing() || state()?.connected === false || composingURL) return;
    const target = normalizeBrowserAddress(address);
    if (!target) { setError(t("inspector.browser.invalidURL")); return; }
    setURL(target);
    send({ action: "navigate", url: target });
  }

  function keydown(event: KeyboardEvent) {
    // Remote-page keys belong to Chrome, not workspace shortcuts.
    event.stopPropagation();
    if (event.isComposing || composing || event.key === "Process" || event.keyCode === 229) return;
    // Let the textarea receive text and native paste/IME events.
    if ((event.ctrlKey || event.metaKey) && event.key.toLowerCase() === "v") return;
    if (["Control", "Meta", "Alt", "Shift", "Dead"].includes(event.key)) return;
    if (event.key.length === 1 && !event.ctrlKey && !event.metaKey && !event.altKey) return;
    event.preventDefault();
    const modifiers = [event.ctrlKey && "Control", event.metaKey && "Meta", event.altKey && "Alt", event.shiftKey && "Shift"].filter(Boolean);
    send({ action: "key", key: [...modifiers, event.key === " " ? "Space" : event.key].join("+") });
  }

  return (
    <div class="h-full flex flex-col min-w-0 text-[var(--text-primary)] text-xs">
      <Show when={scope()} fallback={<p class="p-4 text-[var(--text-muted)]">{t("inspector.browser.noSession")}</p>}>
        <div class="flex items-center gap-1 px-2 py-1 border-b border-[var(--border-subtle)]">
          <button class={iconBtn} disabled={!running() || busy()} title={t("inspector.browser.back")} aria-label={t("inspector.browser.back")} onClick={() => send({ action: "back" })}><ArrowLeft size={13} /></button>
          <button class={iconBtn} disabled={!running() || busy()} title={t("inspector.browser.forward")} aria-label={t("inspector.browser.forward")} onClick={() => send({ action: "forward" })}><ArrowRight size={13} /></button>
          <button class={iconBtn} disabled={!running() || busy()} title={t("inspector.browser.reload")} aria-label={t("inspector.browser.reload")} onClick={() => send({ action: "reload" })}><RefreshCw size={13} class={busy() ? "animate-spin" : ""} /></button>
          <form class="flex flex-1 min-w-0 items-center gap-1" onSubmit={(event) => {
            event.preventDefault();
            navigateAddress();
          }} onKeyDown={(event) => event.stopPropagation()}>
            <input class="min-w-0 flex-1 rounded border border-[var(--border-subtle)] bg-transparent px-2 py-1 text-xs focus:outline-none focus:border-[var(--accent-primary)]" aria-label={t("inspector.browser.url")} placeholder={t("inspector.browser.url")} value={url()} autocapitalize="off" autocomplete="off" spellcheck={false}
              onFocus={() => { editingURL = true; }} onBlur={() => { editingURL = false; }} onInput={(event) => setURL(event.currentTarget.value)}
              onCompositionStart={() => { composingURL = true; }} onCompositionEnd={() => { composingURL = false; }}
              onKeyDown={(event) => {
                event.stopPropagation();
                if (event.key !== "Enter" || event.isComposing || composingURL || event.keyCode === 229) return;
                // Submit explicitly: native WebViews need not perform implicit form submission.
                event.preventDefault();
                navigateAddress(event.currentTarget.value);
              }} />
            <button class="shrink-0 rounded px-1.5 py-1 text-[11px] hover:bg-[var(--bg-hover)] disabled:opacity-40 cursor-pointer" disabled={busy() || pairing() || state()?.connected === false} type="submit">{t("inspector.browser.go")}</button>
          </form>
          <button
            type="button"
            class={iconBtn}
            disabled={!url().trim() || url().trim() === "about:blank"}
            title={t("inspector.browser.openInExternal")}
            aria-label={t("inspector.browser.openInExternal")}
            onClick={() => {
              const target = normalizeBrowserAddress(url());
              if (!target) {
                setError(t("inspector.browser.invalidURL"));
              } else if (target !== "about:blank") {
                openExternalURL(target);
              }
            }}
          >
            <ExternalLink size={13} />
          </button>
          <Show when={running()}>
            <label
              class={`flex shrink-0 items-center gap-1 rounded border px-1.5 py-0.5 text-[11px] cursor-pointer select-none transition-colors ${
                state()?.manual
                  ? "border-[var(--accent-primary)] bg-[var(--accent-surface)] text-[var(--accent-primary)]"
                  : "border-[var(--border-subtle)] text-[var(--text-muted)] hover:text-[var(--text-primary)]"
              }`}
              title={t(state()?.manual ? "inspector.browser.manualHint" : "inspector.browser.agentHint")}
            >
              <input type="checkbox" class="sr-only" checked={state()?.manual ?? false} disabled={busy()} onChange={(event) => send({ action: "control", manual: event.currentTarget.checked })} />
              {t("inspector.browser.manual")}
            </label>
          </Show>
          <Show
            when={running() || chromeLinked() || pairing()}
            fallback={
              <button class="shrink-0 rounded border border-[var(--border-subtle)] px-2 py-1 text-[11px] hover:bg-[var(--bg-hover)] disabled:opacity-40 cursor-pointer" disabled={busy()} onClick={() => navigateAddress()}>
                {t("inspector.browser.start")}
              </button>
            }
          >
            <button class={`${iconBtn} text-[var(--text-muted)] hover:text-red-400`} disabled={busy()} title={t(chromeLinked() || pairing() ? "inspector.browser.disconnectChrome" : "inspector.browser.close")} aria-label={t(chromeLinked() || pairing() ? "inspector.browser.disconnectChrome" : "inspector.browser.close")} onClick={() => send({ action: "close" })}>
              <Power size={13} />
            </button>
          </Show>
        </div>
        <Show when={!running() && !chromeLinked()}>
          <button class="m-2 rounded border border-[var(--border-subtle)] px-3 py-2 hover:bg-[var(--bg-hover)] disabled:opacity-40 cursor-pointer" disabled={busy()} onClick={() => void pairChrome()}>{t(pairing() ? "inspector.browser.regenerateChromeCode" : "inspector.browser.connectChrome")}</button>
        </Show>
        <Show when={pairCode()}>
          <div class="m-2 flex flex-col gap-2 rounded border border-[var(--border-subtle)] p-3">
            <p>{t("inspector.browser.chromePairHint")}</p>
            <p class="text-[var(--text-muted)]">{t("inspector.browser.chromeInstallHint")}</p>
            <input class="w-full rounded border border-[var(--border-subtle)] bg-transparent p-2 font-mono selectable-text" readOnly value={pairCode()} aria-label={t("inspector.browser.chromeCode")} onFocus={event => event.currentTarget.select()} />
          </div>
        </Show>
        <Show when={chromeLinked()}><p class="px-2 py-1 text-[var(--text-muted)]">{t(state()?.connected === false ? "inspector.browser.chromeDisconnected" : "inspector.browser.chromeConnected")}</p></Show>
        <Show when={running()}>
          <div class="flex items-center gap-1 px-2 py-0.5 border-b border-[var(--border-subtle)]">
            <div class="flex flex-1 min-w-0 overflow-x-auto no-scrollbar gap-1" role="tablist" aria-label={t("inspector.browser.tabs")}>
              <For each={state()?.tabs}>{(tab) => (
                <div class="flex shrink-0 items-center rounded border border-[var(--border-subtle)] text-[11px]" classList={{ "bg-[var(--accent-surface)] text-[var(--accent-primary)]": state()?.tabID === tab.id }}>
                  <button role="tab" aria-selected={state()?.tabID === tab.id} class="max-w-32 truncate px-1.5 py-0.5 cursor-pointer" disabled={busy()} title={tab.url} onClick={() => send({ action: "tab_select", tabID: tab.id })}>{tab.title || tab.url || t("inspector.browser.untitled")}</button>
                  <Show when={!chromeLinked()}><button class="shrink-0 rounded p-0.5 hover:bg-[var(--bg-hover)] disabled:opacity-40 cursor-pointer" disabled={busy()} aria-label={t("inspector.browser.closeTab")} onClick={() => send({ action: "tab_close", tabID: tab.id })}><X size={11} /></button></Show>
                </div>
              )}</For>
            </div>
            <Show when={!chromeLinked()}><button class={iconBtn} disabled={busy()} title={t("inspector.browser.newTab")} aria-label={t("inspector.browser.newTab")} onClick={() => send({ action: "tab_new", url: "about:blank" })}><Plus size={13} /></button></Show>
          </div>
        </Show>
        <Show when={error() || pollError()}><p role="alert" class="px-2 py-1 text-[11px] text-red-500 break-words border-b border-[var(--border-subtle)]">{t("inspector.browser.error", { error: error() || pollError() })}</p></Show>
        <div ref={observeViewport} class="flex-1 min-h-0 overflow-hidden">
          <Show when={frame()} fallback={
            <div class="flex min-h-full flex-col items-center justify-center gap-4 px-6 py-10 text-center text-[var(--text-muted)]">
              <Show when={!busy() && !running() && !pairing() && !chromeLinked()} fallback={
                <p role="status">{t(busy() ? "inspector.browser.working" : "inspector.browser.waiting")}</p>
              }>
                <div class="flex h-16 w-16 items-center justify-center rounded-2xl border border-[var(--border-subtle)] bg-[var(--bg-hover)]">
                  <Globe size={32} strokeWidth={1.5} aria-hidden="true" />
                </div>
                <button
                  class="inline-flex items-center gap-2 rounded-lg bg-[var(--accent-primary)] px-4 py-2 text-xs font-medium text-white hover:opacity-90 focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-[var(--accent-primary)] cursor-pointer transition-opacity"
                  onClick={() => navigateAddress()}
                >
                  <Power size={14} aria-hidden="true" />
                  {t("inspector.browser.start")}
                </button>
                <p class="max-w-64 text-xs leading-relaxed">{t("inspector.browser.empty")}</p>
              </Show>
            </div>
          }>
            <div class="relative">
              <img src={frame()!} alt={t("inspector.browser.viewport")} draggable={false} class="block w-full h-auto rounded-sm cursor-crosshair select-none" onClick={(event) => {
                if (!current || frameEpoch !== current.tabEpoch) return;
                const point = browserPoint(event.currentTarget.getBoundingClientRect(), event.clientX, event.clientY,
                  frameSize);
                if (point) { send({ action: "click", ...point }); input?.focus({ preventScroll: true }); }
              }} onWheel={(event) => {
                event.preventDefault();
                if (!current || frameEpoch !== current.tabEpoch) return;
                const point = browserPoint(event.currentTarget.getBoundingClientRect(), event.clientX, event.clientY,
                  frameSize);
                const unit = event.deltaMode === 1 ? 16 : event.deltaMode === 2 ? frameSize.height : 1;
                if (point) send({ action: "scroll", ...point, deltaX: event.deltaX * unit, deltaY: event.deltaY * unit });
              }} />
              <textarea ref={input} class="absolute left-0 top-0 w-px h-px opacity-0" aria-label={t("inspector.browser.keyboard")} autocapitalize="off" autocomplete="off" spellcheck={false}
                onKeyDown={keydown}
                onCompositionStart={() => { composing = true; }}
                onCompositionEnd={(event) => { composing = false; typeText(event.currentTarget.value); event.currentTarget.value = ""; }}
                onInput={(event) => { if (!composing && !(event as InputEvent).isComposing) { typeText(event.currentTarget.value); event.currentTarget.value = ""; } }}
                onPaste={(event) => { event.preventDefault(); typeText(event.clipboardData?.getData("text/plain") || ""); }} />
            </div>
          </Show>
        </div>
      </Show>
    </div>
  );
}
