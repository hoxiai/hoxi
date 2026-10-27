import { createEffect, For, onCleanup, Show } from "solid-js";
import { ArrowLeft, ArrowRight, ExternalLink, Globe, Plus, Power, RefreshCw, X } from "lucide-solid";
import { createBrowserController, openExternalURL, normalizeBrowserAddress } from "../../store/workspace";
import { t } from "../../i18n";
import BrowserPreview from "./BrowserPreview";
import BrowserStatusBar, { browserPhaseLabel } from "./BrowserStatusBar";

export default function BrowserInspector() {
  const browser = createBrowserController();
  const scope = browser.scope;
  const state = browser.state;
  const frame = browser.frame;
  const url = browser.url;
  const setURL = browser.setURL;
  const error = browser.error;
  const setError = browser.setError;
  const pollError = browser.pollError;
  const busy = browser.busy;
  const pairCode = browser.pairCode;
  const running = browser.running;
  const chromeLinked = browser.chromeLinked;
  const pairing = browser.pairing;
  const send = browser.send;
  const pairChrome = browser.pairChrome;
  const typeText = browser.typeText;
  const navigateAddress = browser.navigateAddress;
  const isFrameCurrent = browser.isFrameCurrent;
  let input: HTMLTextAreaElement | undefined;
  let composing = false;
  let composingURL = false;
  const iconBtn = "shrink-0 rounded p-1 hover:bg-[var(--bg-hover)] disabled:opacity-40 cursor-pointer transition-colors";
  createEffect(() => {
    scope();
    composing = composingURL = false;
    if (input) input.value = "";
  });

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
        browser.setViewport(previous => previous?.width === next.width && previous.height === next.height && previous.deviceScaleFactor === next.deviceScaleFactor ? previous : next);
      }, 100);
    });
    observer.observe(element);
    onCleanup(() => { observer.disconnect(); clearTimeout(timer); });
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
          <Show when={browser.canStop()} fallback={<button class={iconBtn} disabled={!running() || busy()} title={t("inspector.browser.reload")} aria-label={t("inspector.browser.reload")} onClick={() => send({ action: "reload" })}><RefreshCw size={13} class={busy() ? "animate-spin" : ""} /></button>}>
            <button class={iconBtn} disabled={browser.stopping()} title={t("inspector.browser.stopLoading")} aria-label={t("inspector.browser.stopLoading")} onClick={() => void browser.stopLoading()}><X size={13} /></button>
          </Show>
          <form class="flex flex-1 min-w-0 items-center gap-1" onSubmit={(event) => {
            event.preventDefault();
            if (!composingURL) navigateAddress();
          }} onKeyDown={(event) => event.stopPropagation()}>
            <input class="min-w-0 flex-1 rounded border border-[var(--border-subtle)] bg-transparent px-2 py-1 text-xs focus:outline-none focus:border-[var(--accent-primary)]" aria-label={t("inspector.browser.url")} placeholder={t("inspector.browser.url")} value={url()} autocapitalize="off" autocomplete="off" spellcheck={false}
              onFocus={() => { browser.setEditingURL(true); }} onBlur={() => { browser.setEditingURL(false); }} onInput={(event) => { browser.setEditingURL(true); setURL(event.currentTarget.value); }}
              onCompositionStart={() => { composingURL = true; }} onCompositionEnd={() => { composingURL = false; }}
              onKeyDown={(event) => {
                event.stopPropagation();
                if (event.key !== "Enter" || event.isComposing || composingURL || event.keyCode === 229) return;
                // Submit explicitly: native WebViews need not perform implicit form submission.
                event.preventDefault();
                if (!composingURL) navigateAddress(event.currentTarget.value);
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
            <button type="button"
              class={`flex shrink-0 items-center gap-1 rounded border px-1.5 py-0.5 text-[11px] cursor-pointer select-none transition-colors ${
                state()?.manual
                  ? "border-[var(--accent-primary)] bg-[var(--accent-surface)] text-[var(--accent-primary)]"
                  : "border-[var(--border-subtle)] text-[var(--text-muted)] hover:text-[var(--text-primary)]"
              }`}
              title={t(state()?.manual ? "inspector.browser.manualHint" : "inspector.browser.agentHint")}
              aria-pressed={state()?.manual ?? false}
              disabled={busy() || browser.stopping()}
              onClick={() => send({ action: "control", manual: !state()?.manual })}
            >
              {t(state()?.manual ? "inspector.browser.giveBackAI" : "inspector.browser.takeOver")}
            </button>
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
        <Show when={running() || busy() || error() || pollError() || chromeLinked()}>
          <BrowserStatusBar phase={browser.phase()} stream={browser.streamPhase()} navigation={state()?.navigation}
            manual={state()?.manual ?? false} disabled={busy() || browser.stopping()} onRetry={browser.retry} />
        </Show>
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
                <p role="status">{browserPhaseLabel(browser.phase())}</p>
              }>
                <div class="flex h-16 w-16 items-center justify-center rounded-2xl border border-[var(--border-subtle)] bg-[var(--bg-hover)]">
                  <Globe size={32} strokeWidth={1.5} aria-hidden="true" />
                </div>
                <Show when={state()?.idleClosed && state()?.idleMinutes}>
                  <p role="status" class="max-w-64 text-xs leading-relaxed text-[var(--text-secondary)]">{t("inspector.browser.idleClosed", { minutes: state()!.idleMinutes! })}</p>
                </Show>
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
              <BrowserPreview image={frame()!} width={browser.frameSize().width} height={browser.frameSize().height}
                identity={`${scope()?.sessionID}:${scope()?.directory}:${state()?.tabID}`}
                isCurrent={isFrameCurrent()}
                onAction={send} onKeyboardFocus={() => input?.focus({ preventScroll: true })} />
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
