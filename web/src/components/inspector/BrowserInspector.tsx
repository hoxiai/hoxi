import { createEffect, createMemo, createSignal, For, onCleanup, Show } from "solid-js";
import { ArrowLeft, ArrowRight, Plus, RefreshCw, X } from "lucide-solid";
import { browserAPI, browserPoint, type BrowserAction, type BrowserScope, type BrowserState } from "../../services/browser";
import { sessionDirectory, workspace } from "../../store/workspace";
import { t } from "../../i18n";

interface Connection {
  scope: BrowserScope;
  controller: AbortController;
  revision: number;
  pending: number;
  queue: Promise<void>;
}

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
  const button = "shrink-0 rounded p-1.5 hover:bg-[var(--bg-hover)] disabled:opacity-40 cursor-pointer";
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

  function send(action: BrowserAction) {
    const connection = current;
    if (!connection || !valid(connection)) return;
    connection.revision++;
    connection.pending++;
    setBusy(true);
    setError("");
    // Preserve click/type/key ordering even when the network is slower than typing.
    connection.queue = connection.queue.then(async () => {
      if (!valid(connection)) return;
      try {
        const next = await browserAPI.action(connection.scope, action, connection.controller.signal);
        if (valid(connection)) {
          applyState(next);
          if (["navigate", "tab_new", "tab_select", "tab_close", "close"].includes(action.action)) replaceFrame(null);
        }
      } catch (err) {
        if (valid(connection)) setError(String(err));
      } finally {
        connection.revision++;
        connection.pending--;
        if (valid(connection)) setBusy(connection.pending > 0);
      }
    });
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
      scope: { ...active }, controller: new AbortController(), revision: 0, pending: 0, queue: Promise.resolve(),
    };
    current = connection;
    let timer: ReturnType<typeof setTimeout> | undefined;
    async function poll() {
      const revision = connection.revision;
      try {
        const next = await browserAPI.state(connection.scope, connection.controller.signal);
        if (!valid(connection)) return;
        if (revision === connection.revision && !connection.pending) applyState(next);
        const blob = await browserAPI.frame(connection.scope, connection.controller.signal);
        if (!valid(connection)) return;
        if (revision === connection.revision && !connection.pending) {
          replaceFrame(blob ? URL.createObjectURL(blob) : null);
        }
        setPollError("");
      } catch (err) {
        if (valid(connection)) setPollError(String(err));
      } finally {
        if (valid(connection)) timer = setTimeout(poll, 500);
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
      <Show when={scope()} fallback={<p class="p-5 text-[var(--text-muted)]">{t("inspector.browser.noSession")}</p>}>
        <div class="flex flex-wrap items-center gap-1 p-2 border-b border-[var(--border-subtle)]">
          <button class={button} disabled={!running() || busy()} title={t("inspector.browser.back")} aria-label={t("inspector.browser.back")} onClick={() => send({ action: "back" })}><ArrowLeft size={14} /></button>
          <button class={button} disabled={!running() || busy()} title={t("inspector.browser.forward")} aria-label={t("inspector.browser.forward")} onClick={() => send({ action: "forward" })}><ArrowRight size={14} /></button>
          <button class={button} disabled={!running() || busy()} title={t("inspector.browser.reload")} aria-label={t("inspector.browser.reload")} onClick={() => send({ action: "reload" })}><RefreshCw size={14} /></button>
          <form class="flex flex-1 min-w-32 gap-1" onSubmit={(event) => {
            event.preventDefault();
            editingURL = false;
            send({ action: "navigate", url: url().trim() || "about:blank" });
          }}>
            <input class="min-w-0 w-full rounded border border-[var(--border-subtle)] bg-transparent px-2 py-1.5" aria-label={t("inspector.browser.url")} placeholder={t("inspector.browser.url")} value={url()} onFocus={() => { editingURL = true; }} onBlur={() => { editingURL = false; }} onInput={(event) => setURL(event.currentTarget.value)} />
            <button class={button} disabled={busy()} type="submit">{t("inspector.browser.go")}</button>
          </form>
          <button class={button} disabled={busy()} onClick={() => send(running() ? { action: "close" } : { action: "navigate", url: url().trim() || "about:blank" })}>{t(running() ? "inspector.browser.close" : "inspector.browser.start")}</button>
        </div>
        <Show when={running()}>
          <div class="flex items-center gap-1 p-2 border-b border-[var(--border-subtle)]">
            <div class="flex flex-1 min-w-0 overflow-x-auto gap-1" role="tablist" aria-label={t("inspector.browser.tabs")}>
              <For each={state()?.tabs}>{(tab) => (
                <div class="flex shrink-0 items-center rounded border border-[var(--border-subtle)]" classList={{ "bg-[var(--accent-surface)]": state()?.tabID === tab.id }}>
                  <button role="tab" aria-selected={state()?.tabID === tab.id} class="max-w-36 truncate p-1.5 cursor-pointer" disabled={busy()} title={tab.url} onClick={() => send({ action: "tab_select", tabID: tab.id })}>{tab.title || tab.url || t("inspector.browser.untitled")}</button>
                  <button class={button} disabled={busy()} aria-label={t("inspector.browser.closeTab")} onClick={() => send({ action: "tab_close", tabID: tab.id })}><X size={12} /></button>
                </div>
              )}</For>
            </div>
            <button class={button} disabled={busy()} title={t("inspector.browser.newTab")} aria-label={t("inspector.browser.newTab")} onClick={() => send({ action: "tab_new", url: "about:blank" })}><Plus size={14} /></button>
          </div>
          <label class="flex items-center gap-2 p-2 cursor-pointer" title={t("inspector.browser.manualHint")}>
            <input type="checkbox" checked={state()?.manual ?? false} disabled={busy()} onChange={(event) => send({ action: "control", manual: event.currentTarget.checked })} />
            {t("inspector.browser.manual")}
          </label>
          <p class="px-2 pb-2 text-[var(--text-muted)]">{t(state()?.manual ? "inspector.browser.manualHint" : "inspector.browser.agentHint")}</p>
        </Show>
        <Show when={error() || pollError()}><p role="alert" class="p-2 text-red-500 break-words">{t("inspector.browser.error", { error: error() || pollError() })}</p></Show>
        <div class="flex-1 min-h-0 overflow-auto p-2">
          <Show when={frame()} fallback={<p class="p-4 text-center text-[var(--text-muted)]">{t(busy() ? "inspector.browser.working" : running() ? "inspector.browser.waiting" : "inspector.browser.empty")}</p>}>
            <div class="relative">
              <img src={frame()!} alt={t("inspector.browser.viewport")} draggable={false} class="block w-full h-auto cursor-crosshair" onClick={(event) => {
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
