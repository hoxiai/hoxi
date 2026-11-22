// web/src/components/settings/PairingQrPanel.tsx
// Shows the LAN pairing QR and optional 6-digit one-time pairing code the
// Hoxi mobile app uses to pair with this computer.
import { createEffect, createSignal, onCleanup, onMount, Show } from "solid-js";
import { Check, Copy, RefreshCw, X } from "lucide-solid";
import { t } from "../../i18n";
import {
  cancelPairingShortCode,
  loadPairingCode,
  pairing,
  publishPairingShortCode,
  rotatePairingToken,
  setPairingShortCodeRelayUrl,
} from "../../store/workspace";
import { HelpTip, SettingGroup } from "./setting-controls";
import { confirmAction } from "./confirm-dialog";

function formatShortCode(code: string): string {
  return code.length === 6 ? `${code.slice(0, 3)} ${code.slice(3)}` : code;
}

export function PairingQrPanel() {
  const [nowMs, setNowMs] = createSignal(Date.now());
  const [copied, setCopied] = createSignal(false);
  let copiedTimer: ReturnType<typeof setTimeout> | undefined;

  onMount(() => {
    void loadPairingCode();
  });

  createEffect(() => {
    const active = pairing.shortCode;
    if (!active) return;
    setNowMs(Date.now());
    const timer = setInterval(() => {
      setNowMs(Date.now());
    }, 1000);
    onCleanup(() => clearInterval(timer));
  });

  onCleanup(() => {
    if (copiedTimer) clearTimeout(copiedTimer);
  });

  const remainingSeconds = () => {
    const active = pairing.shortCode;
    if (!active) return 0;
    return Math.max(0, Math.ceil((active.expiresAtMs - nowMs()) / 1000));
  };

  const onRotate = async () => {
    if (!(await confirmAction(t("pairing.rotateConfirm")))) return;
    void rotatePairingToken();
  };

  const onCopyShortCode = async () => {
    const code = pairing.shortCode?.code;
    if (!code || typeof navigator === "undefined" || !navigator.clipboard) return;
    try {
      await navigator.clipboard.writeText(code);
      setCopied(true);
      if (copiedTimer) clearTimeout(copiedTimer);
      copiedTimer = setTimeout(() => setCopied(false), 1800);
    } catch {
      /* ignore clipboard errors */
    }
  };

  return (
    <SettingGroup title={t("pairing.title")} tip={t("pairing.tip")}>
      <div class="flex flex-col items-center gap-3 px-5 py-6">
        <Show when={pairing.error}>
          <p role="alert" class="text-[12px] text-rose-500">
            {pairing.error}
          </p>
        </Show>

        <Show when={pairing.code}>
          {(code) => (
            <Show
              when={code().available}
              fallback={
                <p class="text-center text-[13px] text-amber-600">
                  {t("pairing.noLanHint")}
                </p>
              }
            >
              <img
                src={code().qrPng}
                alt={t("pairing.qrAlt")}
                class="h-56 w-56 rounded-xl border border-[var(--border-subtle)] bg-white p-2"
              />
              <div class="flex items-center gap-2 text-[13px]">
                <span class="text-[var(--text-secondary)]">{t("pairing.scanHint")}</span>
                <HelpTip text={t("pairing.addressTip", { url: code().url })} />
                <button
                  type="button"
                  class="hoxi-press inline-flex cursor-pointer items-center rounded-lg p-1 text-[var(--text-muted)] hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)] disabled:opacity-50"
                  disabled={pairing.busy}
                  aria-label={t("pairing.refreshAction")}
                  onClick={() => void loadPairingCode()}
                >
                  <RefreshCw size={14} />
                </button>
              </div>
              <button
                type="button"
                class="cursor-pointer text-[12px] text-[var(--text-muted)] underline-offset-2 hover:text-[var(--text-primary)] hover:underline disabled:opacity-50"
                disabled={pairing.busy}
                onClick={onRotate}
              >
                {t("pairing.rotateAction")}
              </button>

              <div class="mt-3 w-full max-w-md border-t border-[var(--border-subtle)] pt-4">
                <div class="mb-2 flex items-center justify-between gap-2">
                  <div class="flex items-center gap-1.5 text-[12px] font-medium text-[var(--text-primary)]">
                    <span>{t("pairing.shortCodeTitle")}</span>
                    <HelpTip text={t("pairing.shortCodeTip")} />
                  </div>
                  <Show when={pairing.shortCode}>
                    <span
                      class={`text-[11px] tabular-nums ${
                        remainingSeconds() > 0
                          ? "text-[var(--text-muted)]"
                          : "text-amber-600"
                      }`}
                    >
                      {remainingSeconds() > 0
                        ? t("pairing.shortCodeCountdown", { seconds: remainingSeconds() })
                        : t("pairing.shortCodeExpired")}
                    </span>
                  </Show>
                </div>

                <div class="flex items-center gap-2">
                  <input
                    type="url"
                    value={pairing.shortCodeRelayUrl}
                    placeholder={t("pairing.shortCodeRelayPlaceholder")}
                    class="h-8 flex-1 rounded-lg border border-[var(--border-subtle)] bg-[var(--bg-surface)] px-2.5 text-[12px] text-[var(--text-primary)] placeholder:text-[var(--text-muted)] focus:border-[var(--accent)] focus:outline-none"
                    disabled={pairing.shortCodeBusy}
                    onInput={(e) => setPairingShortCodeRelayUrl(e.currentTarget.value)}
                  />
                  <button
                    type="button"
                    class="hoxi-press inline-flex h-8 cursor-pointer items-center gap-1.5 rounded-lg border border-[var(--border-subtle)] bg-[var(--bg-surface)] px-3 text-[12px] font-medium text-[var(--text-primary)] hover:bg-[var(--bg-hover)] disabled:opacity-50"
                    disabled={pairing.shortCodeBusy}
                    onClick={() => void publishPairingShortCode()}
                  >
                    <RefreshCw size={12} class={pairing.shortCodeBusy ? "animate-spin" : ""} />
                    <span>
                      {pairing.shortCode
                        ? t("pairing.shortCodeRefresh")
                        : t("pairing.shortCodeGenerate")}
                    </span>
                  </button>
                </div>

                <Show when={pairing.shortCodeError}>
                  <p role="alert" class="mt-2 text-[12px] text-rose-500">
                    {pairing.shortCodeError}
                  </p>
                </Show>

                <Show when={pairing.shortCode}>
                  {(active) => (
                    <div class="mt-3 flex items-center justify-between rounded-xl border border-[var(--border-subtle)] bg-[var(--bg-surface)] px-3.5 py-2.5">
                      <span
                        class={`font-mono text-[22px] font-semibold tabular-nums tracking-[0.22em] ${
                          remainingSeconds() > 0
                            ? "text-[var(--text-primary)]"
                            : "text-[var(--text-muted)] line-through"
                        }`}
                      >
                        {formatShortCode(active().code)}
                      </span>
                      <div class="flex items-center gap-1">
                        <button
                          type="button"
                          class="hoxi-press inline-flex cursor-pointer items-center gap-1 rounded-lg px-2 py-1 text-[11px] text-[var(--text-secondary)] hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)] disabled:opacity-50"
                          disabled={pairing.shortCodeBusy || remainingSeconds() === 0}
                          aria-label={t("pairing.shortCodeCopy")}
                          onClick={() => void onCopyShortCode()}
                        >
                          <Show when={copied()} fallback={<Copy size={13} />}>
                            <Check size={13} class="text-emerald-500" />
                          </Show>
                          <span>
                            {copied() ? t("pairing.shortCodeCopied") : t("pairing.shortCodeCopy")}
                          </span>
                        </button>
                        <button
                          type="button"
                          class="hoxi-press inline-flex cursor-pointer items-center gap-1 rounded-lg px-2 py-1 text-[11px] text-[var(--text-muted)] hover:bg-[var(--bg-hover)] hover:text-rose-500 disabled:opacity-50"
                          disabled={pairing.shortCodeBusy}
                          aria-label={t("pairing.shortCodeCancel")}
                          onClick={() => void cancelPairingShortCode()}
                        >
                          <X size={13} />
                          <span>{t("pairing.shortCodeCancel")}</span>
                        </button>
                      </div>
                    </div>
                  )}
                </Show>
              </div>
            </Show>
          )}
        </Show>

        <Show when={!pairing.code && !pairing.error}>
          <p class="text-[13px] text-[var(--text-muted)]">{t("pairing.loading")}</p>
        </Show>
      </div>
    </SettingGroup>
  );
}
