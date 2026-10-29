import { Show } from "solid-js";
import { AppWindow, CircleHelp, Copy, Check, Eye, EyeOff, Hand, Sparkles } from "lucide-solid";
import type { createBrowserController } from "../../store/workspace";
import { t } from "../../i18n";

interface Props {
  browser: ReturnType<typeof createBrowserController>;
}

export default function BrowserWindowCard(props: Props) {
  const b = () => props.browser;
  const state = () => b().state();
  const modeBtn = (active: boolean) =>
    `flex-1 rounded-md px-2 py-1 text-[11px] font-medium transition-all cursor-pointer disabled:opacity-40 truncate ${
      active
        ? "bg-[var(--bg-panel)] text-[var(--text-primary)] shadow-xs border border-[var(--border-subtle)]"
        : "text-[var(--text-muted)] hover:text-[var(--text-primary)] border border-transparent"
    }`;

  const currentModeTip = () => {
    if (b().chromeLinked() || b().pairing()) {
      const status = state()?.connected === false
        ? t("inspector.browser.chromeDisconnected")
        : t("inspector.browser.chromeConnected");
      return `${status} · ${t("inspector.browser.chromeNativeFiles")}`;
    }
    if (b().windowMode()) {
      return `${t("inspector.browser.persistentLoginHint")} · ${t("inspector.browser.windowModeHint")}`;
    }
    return t("inspector.browser.temporaryLoginHint");
  };

  return (
    <div class="flex flex-col border-b border-[var(--border-subtle)]">
      <div class="flex items-center gap-1.5 px-2 py-1.5">
        <div class="flex flex-1 min-w-0 items-center gap-0.5 rounded-lg bg-[var(--bg-inset)] p-0.5 border border-[var(--border-subtle)]">
          <button
            type="button"
            class={modeBtn(b().windowMode() && !b().pairing())}
            disabled={b().busy()}
            aria-pressed={b().windowMode() && !b().pairing()}
            title={t("inspector.browser.persistentLoginHint")}
            onClick={() => b().switchMode(true)}
          >
            {t("inspector.browser.modeWindow")}
          </button>
          <button
            type="button"
            class={modeBtn(b().chromeLinked() || b().pairing())}
            disabled={b().busy()}
            aria-pressed={b().chromeLinked() || b().pairing()}
            title={t("inspector.browser.chromeConnected")}
            onClick={() => void b().connectExistingChrome()}
          >
            {t("inspector.browser.modeChrome")}
          </button>
          <button
            type="button"
            class={modeBtn(b().headlessMode() && !b().pairing())}
            disabled={b().busy()}
            aria-pressed={b().headlessMode() && !b().pairing()}
            title={t("inspector.browser.temporaryLoginHint")}
            onClick={() => b().switchMode(false)}
          >
            {t("inspector.browser.modeHeadless")}
          </button>
        </div>

        <div class="group relative shrink-0">
          <button
            type="button"
            class="flex h-6 w-6 items-center justify-center rounded-md text-[var(--text-muted)] hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)] cursor-pointer transition-colors"
            title={currentModeTip()}
            aria-label={currentModeTip()}
          >
            <CircleHelp size={13} />
          </button>
          <div
            role="tooltip"
            class="pointer-events-none invisible opacity-0 group-hover:visible group-hover:opacity-100 group-focus-within:visible group-focus-within:opacity-100 transition-opacity duration-150 absolute right-0 top-full mt-1 z-30 w-64 rounded-lg border border-[var(--border-subtle)] bg-[var(--bg-panel)] p-2.5 text-[11px] leading-relaxed text-[var(--text-secondary)] shadow-lg"
          >
            <Show when={b().windowMode() && !b().pairing()}>
              <p class="font-medium text-[var(--text-primary)]">{t("inspector.browser.persistentLoginHint")}</p>
              <p class="mt-1 text-[var(--text-muted)]">{t("inspector.browser.windowModeHint")}</p>
            </Show>
            <Show when={b().headlessMode() && !b().pairing()}>
              <p>{t("inspector.browser.temporaryLoginHint")}</p>
            </Show>
            <Show when={b().chromeLinked() || b().pairing()}>
              <p class="font-medium text-[var(--text-primary)]">
                {t(state()?.connected === false ? "inspector.browser.chromeDisconnected" : "inspector.browser.chromeConnected")}
              </p>
              <p class="mt-1 text-[var(--text-muted)]">{t("inspector.browser.chromeNativeFiles")}</p>
            </Show>
          </div>
        </div>
      </div>

      <Show when={b().pairCode()}>
        <div class="mx-2 mb-2 flex flex-col gap-2 rounded-lg border border-[var(--border-subtle)] bg-[var(--bg-hover)] p-2.5">
          <div class="flex items-start justify-between gap-2">
            <p class="text-[11px] leading-snug text-[var(--text-secondary)]">
              {t("inspector.browser.chromePairHint")}
            </p>
            <div class="group relative shrink-0">
              <button
                type="button"
                class="flex h-5 w-5 items-center justify-center rounded text-[var(--text-muted)] hover:text-[var(--text-primary)] cursor-pointer"
                title={`${t("inspector.browser.chromeQuickConnectHint")} · ${t("inspector.browser.chromeInstallHint")}`}
                aria-label={t("inspector.browser.chromeInstallHint")}
              >
                <CircleHelp size={12} />
              </button>
              <div
                role="tooltip"
                class="pointer-events-none invisible opacity-0 group-hover:visible group-hover:opacity-100 group-focus-within:visible group-focus-within:opacity-100 transition-opacity duration-150 absolute right-0 top-full mt-1 z-30 w-64 rounded-lg border border-[var(--border-subtle)] bg-[var(--bg-panel)] p-2.5 text-[11px] leading-relaxed text-[var(--text-secondary)] shadow-lg"
              >
                <p class="font-medium text-[var(--text-primary)]">{t("inspector.browser.chromeQuickConnectHint")}</p>
                <p class="mt-1 text-[var(--text-muted)]">{t("inspector.browser.chromeInstallHint")}</p>
              </div>
            </div>
          </div>
          <div class="flex items-center gap-1.5">
            <input
              class="min-w-0 flex-1 rounded border border-[var(--border-subtle)] bg-[var(--bg-panel)] px-2 py-1 font-mono text-[11px] selectable-text"
              readOnly
              value={b().pairCode()}
              aria-label={t("inspector.browser.chromeCode")}
              onFocus={event => event.currentTarget.select()}
            />
            <button
              type="button"
              class="inline-flex shrink-0 items-center gap-1 rounded border border-[var(--border-subtle)] bg-[var(--bg-panel)] px-2 py-1 text-[11px] hover:bg-[var(--bg-hover)] disabled:opacity-40 cursor-pointer"
              disabled={b().pairSeconds() === 0}
              onClick={() => void b().copyChromeCode()}
            >
              <Show when={b().pairCopied()} fallback={<Copy size={11} />}>
                <Check size={11} />
              </Show>
              {t(b().pairCopied() ? "inspector.browser.copied" : "inspector.browser.copyChromeCode")}
            </button>
          </div>
          <span role="status" class="text-[10px] text-[var(--text-muted)]">
            {b().pairSeconds()
              ? t("inspector.browser.chromeCodeExpires", { seconds: b().pairSeconds() })
              : t("inspector.browser.chromeCodeExpired")}
          </span>
        </div>
      </Show>

      <Show when={b().running() && (b().windowMode() || b().chromeLinked())}>
        <div class="mx-2 mb-1.5 flex flex-col gap-2 rounded-lg border border-[var(--border-subtle)] bg-[var(--bg-hover)] px-2.5 py-2">
          <div class="flex items-center justify-between gap-2">
            <div class="min-w-0 flex-1" title={t("inspector.browser.windowModeHint")}>
              <p class="truncate text-[11px] font-medium text-[var(--text-primary)]">
                {state()?.title || t("inspector.browser.windowModeTitle")}
              </p>
              <p class="truncate text-[10px] text-[var(--text-muted)]">
                {state()?.url || t("inspector.browser.windowModeHint")}
              </p>
            </div>
            <span
              class={`shrink-0 rounded px-1.5 py-0.5 text-[10px] font-medium ${
                state()?.manual
                  ? "bg-[var(--accent-surface)] text-[var(--accent-primary)]"
                  : "bg-[var(--bg-panel)] text-[var(--text-secondary)]"
              }`}
              title={t(state()?.manual ? "inspector.browser.manualHint" : "inspector.browser.agentHint")}
            >
              {t(state()?.manual ? "inspector.browser.youControl" : "inspector.browser.aiCanControl")}
            </span>
          </div>
          <div class="flex flex-wrap items-center gap-1.5">
            <button
              type="button"
              class="inline-flex items-center gap-1 rounded bg-[var(--accent-primary)] px-2 py-1 text-[11px] font-medium text-white hover:opacity-90 disabled:opacity-40 cursor-pointer"
              disabled={b().busy()}
              onClick={() => b().focusWindow()}
            >
              <AppWindow size={12} />
              {t("inspector.browser.focusWindow")}
            </button>
            <button
              type="button"
              class="inline-flex items-center gap-1 rounded border border-[var(--border-subtle)] bg-[var(--bg-panel)] px-2 py-1 text-[11px] hover:bg-[var(--bg-hover)] disabled:opacity-40 cursor-pointer"
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
              class="inline-flex items-center gap-1 rounded border border-[var(--border-subtle)] bg-[var(--bg-panel)] px-2 py-1 text-[11px] text-[var(--text-secondary)] hover:text-[var(--text-primary)] cursor-pointer"
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
