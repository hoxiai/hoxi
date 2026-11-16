import { Show } from "solid-js";
import { t } from "../../i18n";
import type { BrowserLoadPhase, BrowserState, BrowserStreamPhase } from "../../store/workspace";

const LABELS = {
  idle: "inspector.browser.statusIdle", starting: "inspector.browser.statusStarting",
  loading: "inspector.browser.statusLoading", slow: "inspector.browser.statusSlow",
  failed: "inspector.browser.statusFailed", stopped: "inspector.browser.statusStopped",
  connecting: "inspector.browser.statusConnecting", reconnecting: "inspector.browser.statusReconnecting",
  delayed: "inspector.browser.statusDelayed", ready: "inspector.browser.statusReady",
  disconnected: "inspector.browser.statusDisconnected",
} as const;

export function browserPhaseLabel(phase: BrowserLoadPhase): string { return t(LABELS[phase]); }

export default function BrowserStatusBar(props: {
  phase: BrowserLoadPhase;
  stream: BrowserStreamPhase;
  navigation?: BrowserState["navigation"];
  manual: boolean;
  onRetry: () => void;
  disabled: boolean;
}) {
  const canRetry = () => ["failed", "stopped", "reconnecting", "delayed", "disconnected"].includes(props.phase);
  const spinning = () => ["starting", "loading", "slow", "connecting", "reconnecting"].includes(props.phase);
  const detail = () => props.navigation?.error === "navigation_timeout" ? t("inspector.browser.navigationTimeout") :
    props.navigation?.error || ((props.navigation?.httpStatus ?? 0) >= 400 ? t("inspector.browser.httpError", { status: props.navigation!.httpStatus! }) : "");
  return <div class="flex min-h-8 shrink-0 items-center gap-2 border-b border-[var(--border-subtle)] px-2 py-1 text-[11px] text-[var(--text-muted)]">
    <span class="h-2 w-2 shrink-0 rounded-full" classList={{ "animate-pulse bg-[var(--accent-primary)]": spinning(), "bg-red-500": props.phase === "failed", "bg-emerald-500": props.phase === "ready", "bg-[var(--text-faint)]": !spinning() && props.phase !== "failed" && props.phase !== "ready" }} />
    <span role="status" aria-live="polite" class="min-w-0 flex-1 truncate" title={detail()}>
      {browserPhaseLabel(props.phase)}<Show when={detail()}> · {detail()}</Show>
    </span>
    <Show when={props.stream === "reconnecting" && ["loading", "slow", "failed", "stopped"].includes(props.phase)}>
      <span>{t("inspector.browser.statusReconnecting")}</span>
    </Show>
    <span title={t(props.manual ? "inspector.browser.manualHint" : "inspector.browser.verificationHint")} classList={{ "text-[var(--accent-primary)]": props.manual }}>
      {t(props.manual ? "inspector.browser.youControl" : "inspector.browser.aiCanControl")}
    </span>
    <Show when={canRetry()}><button type="button" disabled={props.disabled} class="shrink-0 cursor-pointer rounded border border-[var(--border-subtle)] px-2 py-0.5 hover:bg-[var(--bg-hover)] disabled:opacity-40" onClick={props.onRetry}>{t("inspector.browser.retry")}</button></Show>
  </div>;
}
