import { createEffect, createSignal, onCleanup, Show, type Accessor } from "solid-js";
import { Check, RefreshCw, WifiOff } from "lucide-solid";
import { t } from "../../i18n";

interface NetworkStatusBannerProps {
  online: Accessor<boolean>;
  connected: Accessor<boolean>;
  streamStarted: Accessor<boolean>;
  onRetry: () => void;
}

type NetworkState = "idle" | "offline" | "reconnecting" | "recovered";

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

  const state = (): NetworkState => {
    if (!props.online()) return "offline";
    if (showReconnect()) return "reconnecting";
    if (showRecovered()) return "recovered";
    return "idle";
  };

  const title = () => {
    switch (state()) {
      case "offline":
        return t("network.offlineTitle");
      case "reconnecting":
        return t("network.reconnectingTitle");
      case "recovered":
        return t("network.recoveredTitle");
      default:
        return "";
    }
  };

  const description = () => {
    switch (state()) {
      case "offline":
        return t("network.offlineDescription");
      case "reconnecting":
        return t("network.reconnectingDescription");
      default:
        return "";
    }
  };

  return (
    <Show when={state() !== "idle"}>
      <div
        role="status"
        aria-live="polite"
        title={description() || undefined}
        style={{ "--wails-draggable": "no-drag" } as any}
        class={`inline-flex shrink-0 items-center gap-1.5 rounded-full border px-2.5 py-1 text-[11px] font-medium transition-colors ${
          state() === "recovered"
            ? "border-emerald-500/30 bg-emerald-500/10 text-emerald-600 dark:text-emerald-400"
            : "border-amber-500/30 bg-amber-500/10 text-amber-600 dark:text-amber-400"
        }`}
      >
        <Show
          when={state() === "recovered"}
          fallback={<WifiOff size={12} class="shrink-0" />}
        >
          <Check size={12} class="shrink-0" />
        </Show>
        <span class="whitespace-nowrap">{title()}</span>
        <Show when={props.online() && showReconnect()}>
          <button
            type="button"
            onClick={props.onRetry}
            class="inline-flex shrink-0 items-center gap-1 rounded-full px-1.5 py-0.5 text-[11px] font-medium transition-opacity hover:opacity-70"
          >
            <RefreshCw size={11} />
            {t("network.retry")}
          </button>
        </Show>
      </div>
    </Show>
  );
}
