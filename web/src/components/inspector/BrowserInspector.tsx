import { createEffect, createMemo, createSignal, For, onCleanup, Show, untrack } from "solid-js";
import { ArrowLeft, ArrowRight, Plus, Power, RefreshCw, X } from "lucide-solid";
import { browserAPI, browserPoint, type BrowserAction, type BrowserScope, type BrowserState } from "../../services/browser";
import { sessionDirectory, setWorkspace, workspace } from "../../store/workspace";
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
  fetchingFrame: boolean;
  lastFrameAt: number;
  pendingScroll: PendingScroll | null;
  pendingType: string;
  flushScheduled: boolean;
  queue: Promise<void>;
}

const NAV_ACTIONS = new Set<BrowserAction["action"]>([
  "navigate", "back", "forward", "reload", "tab_new", "tab_select", "tab_close", "close", "control",
]);
const TAB_EPOCH_ACTIONS = new Set<BrowserAction["action"]>([
  "tab_new", "tab_select", "tab_close", "close",
]);

export default function BrowserInspector() {
  const [state, setState] = createSignal<BrowserState | null>(null);
  const [frame, setFrame] = createSignal<string | null>(null);
  const [url, setURL] = createSignal("");
  const [error, setError] = createSignal("");
  const [pollError, setPollError] = createSignal("");
  const [busy, setBusy] = createSignal(false);
  const [visible, setVisible] = createSignal(!document.hidden);
  const scope = createMemo(() => {
    const sessionID = workspace.activeSessionID;
    const directory = sessionID ? sessionDirectory(sessionID) : undefined;
    return sessionID && directory ? { sessionID, directory } : null;
  });
  let current: Connection | null = null;
  let input: HTMLTextAreaElement | undefined;
  let composing = false;
  let editingURL = false;
  const iconBtn = "shrink-0 rounded p-1 hover:bg-[var(--bg-hover)] disabled:opacity-40 cursor-pointer transition-colors";
  const running = () => Boolean(state()?.tabs.length);
  const visibility = () => setVisible(!document.hidden);
  document.addEventListener("visibilitychange", visibility);
  onCleanup(() => document.removeEventListener("visibilitychange", visibility));

  function replaceFrame(next: string | null) {
    const previous = frame();
    setFrame(next);
    if (previous) URL.revokeObjectURL(previous);
  }

  function valid(connection: Connection) {
    const active = scope();
    return current === connection && !connection.controller.signal.aborted &&
      active?.sessionID === connection.scope.sessionID && active?.directory === connection.scope.directory;
  }

  function applyState(next: BrowserState) {
    setState(next);
    if (!editingURL) setURL(next.url || "");
    if (!next.tabs.length) replaceFrame(null);
  }

  async function pullFrame(connection: Connection) {
    if (!valid(connection) || connection.fetchingFrame || !running()) return;
    connection.fetchingFrame = true;
    const epoch = connection.tabEpoch;
    try {
      const blob = await browserAPI.frame(connection.scope, connection.controller.signal);
      if (!valid(connection) || epoch !== connection.tabEpoch) return;
      if (blob) {
        replaceFrame(URL.createObjectURL(blob));
        connection.lastFrameAt = performance.now();
      } else if (!running()) {
        replaceFrame(null);
      }
      setPollError("");
    } catch (err) {
      if (valid(connection)) setPollError(String(err));
    } finally {
      connection.fetchingFrame = false;
    }
  }

  function enqueueAction(connection: Connection, action: BrowserAction) {
    if (!valid(connection)) return;
    const isNav = NAV_ACTIONS.has(action.action);
    if (TAB_EPOCH_ACTIONS.has(action.action)) connection.tabEpoch++;
    connection.pending++;
    if (isNav) {
      connection.navPending++;
      setBusy(true);
    }
    setError("");
    connection.queue = connection.queue.then(async () => {
      if (!valid(connection)) return;
      try {
        const next = await browserAPI.action(connection.scope, action, connection.controller.signal);
        if (valid(connection)) {
          applyState(next);
          if (action.action === "close") replaceFrame(null);
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
          const now = performance.now();
          if (action.action !== "close" && action.action !== "control" &&
            (connection.pending === 0 || now - connection.lastFrameAt >= 120)) {
            void pullFrame(connection);
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
    const connection = current;
    if (!connection || !valid(connection)) return;
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
    setURL(pendingURL);
    send({ action: "navigate", url: pendingURL });
  }

  createEffect(() => {
    const active = scope();
    const enabled = visible() && workspace.inspectorOpen && workspace.inspectorTab === "browser";
    setState(null);
    setURL("");
    setError("");
    setPollError("");
    setBusy(false);
    editingURL = false;
    composing = false;
    if (input) input.value = "";
    if (!active || !enabled) return;
    const connection: Connection = {
      scope: { ...active },
      controller: new AbortController(),
      tabEpoch: 0,
      pending: 0,
      navPending: 0,
      fetchingFrame: false,
      lastFrameAt: 0,
      pendingScroll: null,
      pendingType: "",
      flushScheduled: false,
      queue: Promise.resolve(),
    };
    current = connection;
    untrack(consumePendingBrowserURL);
    let timer: ReturnType<typeof setTimeout> | undefined;
    async function poll() {
      try {
        if (connection.pending === 0) {
          const next = await browserAPI.state(connection.scope, connection.controller.signal);
          if (!valid(connection)) return;
          if (connection.pending === 0) applyState(next);
        }
        if (running()) {
          await pullFrame(connection);
        }
        setPollError("");
      } catch (err) {
        if (valid(connection)) setPollError(String(err));
      } finally {
        if (valid(connection)) timer = setTimeout(poll, running() ? 250 : 600);
      }
    }
    void poll();
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

  function keydown(event: KeyboardEvent) {
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
            editingURL = false;
            send({ action: "navigate", url: url().trim() || "about:blank" });
          }}>
            <input class="min-w-0 flex-1 rounded border border-[var(--border-subtle)] bg-transparent px-2 py-1 text-xs focus:outline-none focus:border-[var(--accent-primary)]" aria-label={t("inspector.browser.url")} placeholder={t("inspector.browser.url")} value={url()} onFocus={() => { editingURL = true; }} onBlur={() => { editingURL = false; }} onInput={(event) => setURL(event.currentTarget.value)} />
            <button class="shrink-0 rounded px-1.5 py-1 text-[11px] hover:bg-[var(--bg-hover)] disabled:opacity-40 cursor-pointer" disabled={busy()} type="submit">{t("inspector.browser.go")}</button>
          </form>
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
            when={running()}
            fallback={
              <button class="shrink-0 rounded border border-[var(--border-subtle)] px-2 py-1 text-[11px] hover:bg-[var(--bg-hover)] disabled:opacity-40 cursor-pointer" disabled={busy()} onClick={() => send({ action: "navigate", url: url().trim() || "about:blank" })}>
                {t("inspector.browser.start")}
              </button>
            }
          >
            <button class={`${iconBtn} text-[var(--text-muted)] hover:text-red-400`} disabled={busy()} title={t("inspector.browser.close")} aria-label={t("inspector.browser.close")} onClick={() => send({ action: "close" })}>
              <Power size={13} />
            </button>
          </Show>
        </div>
        <Show when={running()}>
          <div class="flex items-center gap-1 px-2 py-0.5 border-b border-[var(--border-subtle)]">
            <div class="flex flex-1 min-w-0 overflow-x-auto no-scrollbar gap-1" role="tablist" aria-label={t("inspector.browser.tabs")}>
              <For each={state()?.tabs}>{(tab) => (
                <div class="flex shrink-0 items-center rounded border border-[var(--border-subtle)] text-[11px]" classList={{ "bg-[var(--accent-surface)] text-[var(--accent-primary)]": state()?.tabID === tab.id }}>
                  <button role="tab" aria-selected={state()?.tabID === tab.id} class="max-w-32 truncate px-1.5 py-0.5 cursor-pointer" disabled={busy()} title={tab.url} onClick={() => send({ action: "tab_select", tabID: tab.id })}>{tab.title || tab.url || t("inspector.browser.untitled")}</button>
                  <button class="shrink-0 rounded p-0.5 hover:bg-[var(--bg-hover)] disabled:opacity-40 cursor-pointer" disabled={busy()} aria-label={t("inspector.browser.closeTab")} onClick={() => send({ action: "tab_close", tabID: tab.id })}><X size={11} /></button>
                </div>
              )}</For>
            </div>
            <button class={iconBtn} disabled={busy()} title={t("inspector.browser.newTab")} aria-label={t("inspector.browser.newTab")} onClick={() => send({ action: "tab_new", url: "about:blank" })}><Plus size={13} /></button>
          </div>
        </Show>
        <Show when={error() || pollError()}><p role="alert" class="px-2 py-1 text-[11px] text-red-500 break-words border-b border-[var(--border-subtle)]">{t("inspector.browser.error", { error: error() || pollError() })}</p></Show>
        <div class="inspector-scroll flex-1 min-h-0 overflow-auto p-1">
          <Show when={frame()} fallback={<p class="p-4 text-center text-[var(--text-muted)]">{t(busy() ? "inspector.browser.working" : running() ? "inspector.browser.waiting" : "inspector.browser.empty")}</p>}>
            <div class="relative">
              <img src={frame()!} alt={t("inspector.browser.viewport")} draggable={false} class="block w-full h-auto rounded-sm cursor-crosshair select-none" onClick={(event) => {
                const point = browserPoint(event.currentTarget.getBoundingClientRect(), event.clientX, event.clientY);
                if (point) { send({ action: "click", ...point }); input?.focus({ preventScroll: true }); }
              }} onWheel={(event) => {
                event.preventDefault();
                const point = browserPoint(event.currentTarget.getBoundingClientRect(), event.clientX, event.clientY);
                const unit = event.deltaMode === 1 ? 16 : event.deltaMode === 2 ? 800 : 1;
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
