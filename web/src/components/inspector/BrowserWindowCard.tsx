import { Show } from "solid-js";
import { AppWindow, Eye, EyeOff, Hand, Sparkles } from "lucide-solid";
import type { createBrowserController } from "../../store/workspace";
import { t } from "../../i18n";

interface Props {
  browser: ReturnType<typeof createBrowserController>;
}

export default function BrowserWindowCard(props: Props) {
  const b = () => props.browser;
  const state = () => b().state();
  const modeBtn = (active: boolean) =>
    `flex-1 rounded px-2 py-1 text-[11px] font-medium transition-colors cursor-pointer disabled:opacity-40 ${
      active
        ? "bg-[var(--accent-surface)] text-[var(--accent-primary)] border border-[var(--accent-primary)]"
        : "border border-[var(--border-subtle)] text-[var(--text-muted)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-hover)]"
    }`;

  return (
    <div class="flex flex-col border-b border-[var(--border-subtle)]">
      <div class="flex items-center gap-1 px-2 py-1.5 border-b border-[var(--border-subtle)]">
        <button
          type="button"
          class={modeBtn(b().windowMode() && !b().pairing())}
          disabled={b().busy()}
          aria-pressed={b().windowMode() && !b().pairing()}
          onClick={() => b().switchMode(true)}
        >
          {t("inspector.browser.modeWindow")}
        </button>
        <button
          type="button"
          class={modeBtn(b().chromeLinked() || b().pairing())}
          disabled={b().busy()}
          aria-pressed={b().chromeLinked() || b().pairing()}
          onClick={() => void b().connectExistingChrome()}
        >
          {t("inspector.browser.modeChrome")}
        </button>
        <button
          type="button"
          class={modeBtn(b().headlessMode() && !b().pairing())}
          disabled={b().busy()}
          aria-pressed={b().headlessMode() && !b().pairing()}
          onClick={() => b().switchMode(false)}
        >
          {t("inspector.browser.modeHeadless")}
        </button>
      </div>

      <Show when={b().windowMode() && !b().pairing()}>
        <p class="px-2 py-1 text-[11px] text-[var(--text-muted)]">
          {t("inspector.browser.persistentLoginHint")}
        </p>
      </Show>
      <Show when={b().headlessMode() && !b().pairing()}>
        <p class="px-2 py-1 text-[11px] text-[var(--text-muted)]">
          {t("inspector.browser.temporaryLoginHint")}
        </p>
      </Show>
      <Show when={b().chromeLinked()}>
        <p class="px-2 py-1 text-[11px] text-[var(--text-muted)]">
          {t(state()?.connected === false ? "inspector.browser.chromeDisconnected" : "inspector.browser.chromeConnected")}
        </p>
        <p class="px-2 pb-1 text-[11px] text-[var(--text-muted)]">
          {t("inspector.browser.chromeNativeFiles")}
        </p>
      </Show>

      <Show when={b().pairCode()}>
        <div class="m-2 flex flex-col gap-2 rounded border border-[var(--border-subtle)] p-3">
          <p>{t("inspector.browser.chromePairHint")}</p>
          <p class="text-[11px] text-[var(--text-secondary)]">{t("inspector.browser.chromeQuickConnectHint")}</p>
          <p class="text-[var(--text-muted)]">{t("inspector.browser.chromeInstallHint")}</p>
          <input
            class="w-full rounded border border-[var(--border-subtle)] bg-transparent p-2 font-mono selectable-text"
            readOnly
            value={b().pairCode()}
            aria-label={t("inspector.browser.chromeCode")}
            onFocus={event => event.currentTarget.select()}
          />
          <div class="flex items-center justify-between gap-2">
            <button
              class="rounded border border-[var(--border-subtle)] px-2 py-1 hover:bg-[var(--bg-hover)] disabled:opacity-40 cursor-pointer"
              disabled={b().pairSeconds() === 0}
              onClick={() => void b().copyChromeCode()}
            >
              {t(b().pairCopied() ? "inspector.browser.copied" : "inspector.browser.copyChromeCode")}
            </button>
            <span role="status">
              {b().pairSeconds()
                ? t("inspector.browser.chromeCodeExpires", { seconds: b().pairSeconds() })
                : t("inspector.browser.chromeCodeExpired")}
            </span>
          </div>
        </div>
      </Show>

      <Show when={b().running() && (b().windowMode() || b().chromeLinked())}>
        <div class="mx-2 my-1.5 flex flex-col gap-2 rounded-lg border border-[var(--border-subtle)] bg-[var(--bg-hover)] p-2.5">
          <div class="flex items-start justify-between gap-2">
            <div class="min-w-0 flex-1">
              <p class="truncate font-medium text-[var(--text-primary)]">
                {state()?.title || t("inspector.browser.windowModeTitle")}
              </p>
              <p class="truncate text-[11px] text-[var(--text-muted)]">
                {state()?.url || t("inspector.browser.windowModeHint")}
              </p>
            </div>
            <span
              class={`shrink-0 rounded px-1.5 py-0.5 text-[10px] font-medium ${
                state()?.manual
                  ? "bg-[var(--accent-surface)] text-[var(--accent-primary)]"
                  : "bg-[var(--bg-panel)] text-[var(--text-secondary)]"
              }`}
            >
              {t(state()?.manual ? "inspector.browser.youControl" : "inspector.browser.aiCanControl")}
            </span>
          </div>
          <p class="text-[11px] leading-relaxed text-[var(--text-muted)]">
            {t("inspector.browser.windowModeHint")}
          </p>
          <div class="flex flex-wrap items-center gap-1.5">
            <button
              type="button"
              class="inline-flex items-center gap-1.5 rounded bg-[var(--accent-primary)] px-2.5 py-1 text-[11px] font-medium text-white hover:opacity-90 disabled:opacity-40 cursor-pointer"
              disabled={b().busy()}
              onClick={() => b().focusWindow()}
            >
              <AppWindow size={12} />
              {t("inspector.browser.focusWindow")}
            </button>
            <button
              type="button"
              class="inline-flex items-center gap-1.5 rounded border border-[var(--border-subtle)] bg-[var(--bg-panel)] px-2.5 py-1 text-[11px] hover:bg-[var(--bg-hover)] disabled:opacity-40 cursor-pointer"
              disabled={b().busy() || b().stopping()}
              onClick={() => b().send({ action: "control", manual: !state()?.manual })}
            >
              <Show when={state()?.manual} fallback={<Hand size={12} />}>
                <Sparkles size={12} />
              </Show>
              {t(state()?.manual ? "inspector.browser.giveBackAI" : "inspector.browser.takeOver")}
            </button>
            <button
              type="button"
              class="inline-flex items-center gap-1.5 rounded border border-[var(--border-subtle)] bg-[var(--bg-panel)] px-2.5 py-1 text-[11px] text-[var(--text-secondary)] hover:text-[var(--text-primary)] cursor-pointer"
              aria-pressed={b().previewEnabled()}
              onClick={() => b().togglePreview()}
            >
              <Show when={b().previewEnabled()} fallback={<Eye size={12} />}>
                <EyeOff size={12} />
              </Show>
              {t(b().previewEnabled() ? "inspector.browser.hidePreview" : "inspector.browser.showPreview")}
            </button>
          </div>
        </div>
      </Show>
    </div>
  );
}
