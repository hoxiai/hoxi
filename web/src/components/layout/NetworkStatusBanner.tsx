import { createEffect, createSignal, onCleanup, Show, type Accessor } from "solid-js";
import { Check, RefreshCw, WifiOff } from "lucide-solid";
import { t } from "../../i18n";

interface NetworkStatusBannerProps {
  online: Accessor<boolean>;
  connected: Accessor<boolean>;
  streamStarted: Accessor<boolean>;
  onRetry: () => void;
}

export default function NetworkStatusBanner(props: NetworkStatusBannerProps) {
  const [showReconnect, setShowReconnect] = createSignal(false);
  const [showRecovered, setShowRecovered] = createSignal(false);
  let reconnectTimer: ReturnType<typeof setTimeout> | undefined;
  let recoveredTimer: ReturnType<typeof setTimeout> | undefined;
  let wasDisconnected = false;

  createEffect(() => {
    const shouldReconnect = props.online() && props.streamStarted() && !props.connected();
    if (reconnectTimer) clearTimeout(reconnectTimer);
    if (!shouldReconnect) {
      setShowReconnect(false);
      return;
    }
    reconnectTimer = setTimeout(() => setShowReconnect(true), 2500);
  });

  createEffect(() => {
    if (props.streamStarted() && !props.connected()) {
      wasDisconnected = true;
    } else if (props.connected() && wasDisconnected) {
      wasDisconnected = false;
      setShowRecovered(true);
      if (recoveredTimer) clearTimeout(recoveredTimer);
      recoveredTimer = setTimeout(() => setShowRecovered(false), 2500);
    }
  });

  onCleanup(() => {
    if (reconnectTimer) clearTimeout(reconnectTimer);
    if (recoveredTimer) clearTimeout(recoveredTimer);
  });

  return (
    <Show when={!props.online() || showReconnect() || showRecovered()}>
      <div
        role="status"
        aria-live="polite"
        class={`fixed top-14 left-1/2 z-[200] flex w-[calc(100vw-24px)] max-w-[620px] -translate-x-1/2 items-center gap-3 rounded-xl border px-3.5 py-2.5 shadow-lg backdrop-blur-xl sm:px-4 ${
          !props.online() || showReconnect()
            ? "border-amber-500/30 bg-[var(--bg-elevated)]/95 text-[var(--text-primary)]"
            : "border-emerald-500/30 bg-[var(--bg-elevated)]/95 text-[var(--text-primary)]"
        }`}
      >
        <Show when={!props.online() || showReconnect()} fallback={<Check size={16} class="shrink-0 text-emerald-500" />}>
          <WifiOff size={16} class="shrink-0 text-amber-500" />
        </Show>
        <div class="min-w-0 flex-1">
          <div class="text-[12px] font-semibold">
            {!props.online()
              ? t("network.offlineTitle")
              : showReconnect()
                ? t("network.reconnectingTitle")
                : t("network.recoveredTitle")}
          </div>
          <Show when={!props.online()}>
            <div class="mt-0.5 text-[11px] leading-relaxed text-[var(--text-secondary)]">
              {t("network.offlineDescription")}
            </div>
          </Show>
          <Show when={showReconnect()}>
            <div class="mt-0.5 text-[11px] leading-relaxed text-[var(--text-secondary)]">
              {t("network.reconnectingDescription")}
            </div>
          </Show>
        </div>
        <Show when={props.online() && showReconnect()}>
          <button
            type="button"
            onClick={props.onRetry}
            class="inline-flex shrink-0 items-center gap-1.5 rounded-lg border border-[var(--border-subtle)] px-2.5 py-1.5 text-[11px] font-medium text-[var(--text-primary)] transition-colors hover:bg-[var(--bg-hover)]"
          >
            <RefreshCw size={12} />
            {t("network.retry")}
          </button>
        </Show>
      </div>
    </Show>
  );
}
