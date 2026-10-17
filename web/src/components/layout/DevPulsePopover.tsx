// web/src/components/layout/DevPulsePopover.tsx
import { createEffect, createMemo, createSignal, For, onCleanup, onMount, Show } from "solid-js";
import {
  BarChart3,
  ChevronDown,
  Coins,
  FileCode2,
  KeyRound,
  RefreshCw,
  Zap,
} from "lucide-solid";
import {
  computeDailyDevPulseStats,
  isDailyPulseSyncing,
  openDiffInInspector,
  openProviderModal,
  refreshDailyDevPulse,
  workspace,
} from "../../store/workspace";
import { ProviderIcon } from "../icons";
import { t } from "../../i18n";

function formatCompactTokens(n: number): string {
  if (!n || n <= 0) return "0";
  if (n >= 1_000_000) return `${(n / 1_000_000).toFixed(2)}M`;
  if (n >= 10_000) return `${(n / 1_000).toFixed(1)}k`;
  if (n >= 1_000) return `${(n / 1_000).toFixed(2)}k`;
  return n.toLocaleString();
}

function formatUsd(amount: number): string {
  if (!amount || amount <= 0) return "$0.00";
  if (amount >= 0.01) return `$${amount.toFixed(2)}`;
  if (amount >= 0.001) return `$${amount.toFixed(3)}`;
  return "<$0.001";
}

function formatNetLoc(net: number): string {
  return net > 0 ? `+${net.toLocaleString()}` : net.toLocaleString();
}

export default function DevPulsePopover() {
  const [open, setOpen] = createSignal(false);
  let triggerRef: HTMLDivElement | undefined;
  let popoverRef: HTMLDivElement | undefined;

  const stats = createMemo(() => computeDailyDevPulseStats());

  onMount(() => {
    void refreshDailyDevPulse(false);
    const timer = setInterval(() => void refreshDailyDevPulse(false), 20_000);
    onCleanup(() => clearInterval(timer));
  });

  createEffect(() => {
    const _dir = workspace.rootDir;
    const _len = workspace.sessions.length;
    if (_dir || _len >= 0) void refreshDailyDevPulse(false);
  });

  createEffect(() => {
    if (!open()) return;
    void refreshDailyDevPulse(true);

    const handlePointerDown = (ev: MouseEvent) => {
      const target = ev.target as Node;
      if (popoverRef && !popoverRef.contains(target) && triggerRef && !triggerRef.contains(target)) {
        setOpen(false);
      }
    };
    const handleKeyDown = (ev: KeyboardEvent) => {
      if (ev.key === "Escape") setOpen(false);
    };
    window.addEventListener("pointerdown", handlePointerDown);
    window.addEventListener("keydown", handleKeyDown);
    onCleanup(() => {
      window.removeEventListener("pointerdown", handlePointerDown);
      window.removeEventListener("keydown", handleKeyDown);
    });
  });

  return (
    <div
      ref={triggerRef}
      class="relative inline-flex items-center"
      style={{ "--wails-draggable": "no-drag" } as any}
    >
      {/* Clean Header Pill */}
      <button
        type="button"
        onClick={() => setOpen((v) => !v)}
        class={`inline-flex items-center gap-1.5 rounded-full border px-2.5 py-1 text-[11px] transition-colors cursor-pointer ${
          open()
            ? "border-[var(--accent-border)] bg-[var(--accent-surface)] text-[var(--text-primary)]"
            : "border-transparent bg-[var(--bg-inset)] hover:bg-[var(--bg-hover)] text-[var(--text-secondary)] hover:text-[var(--text-primary)]"
        }`}
        title={t("header.pulseTooltip")}
      >
        <BarChart3 size={11.5} class="text-[var(--accent-primary)] shrink-0" />
        <Show when={stats().totalTokens > 0} fallback={<span>{t("header.pulseBtnLabel")}</span>}>
          <span class="font-mono font-medium text-[var(--text-primary)]">
            {formatCompactTokens(stats().totalTokens)}
          </span>
          <Show when={stats().totalCost > 0}>
            <span class="text-[var(--text-faint)]">·</span>
            <span class="font-mono text-[var(--text-secondary)]">
              {formatUsd(stats().totalCost)}
            </span>
          </Show>
        </Show>
        <ChevronDown size={11} class="opacity-65 shrink-0" />
      </button>

      {/* Clean, Spacious Daily Stats Popover */}
      <Show when={open()}>
        <div
          ref={popoverRef}
          class="absolute right-0 top-full mt-2 z-50 w-[348px] rounded-2xl border border-[var(--border-strong)] bg-[var(--bg-elevated)] p-4 shadow-2xl text-xs space-y-3.5 select-text"
        >
          {/* Header */}
          <div class="flex items-center justify-between">
            <div class="flex items-center gap-2">
              <span class="font-semibold text-[13px] text-[var(--text-primary)]">
                {t("header.pulseTitle")}
              </span>
              <span class="rounded-md bg-[var(--bg-inset)] px-1.5 py-0.5 font-mono text-[10.5px] text-[var(--text-muted)]">
                {stats().dateKey}
              </span>
            </div>
            <button
              type="button"
              onClick={() => void refreshDailyDevPulse(true)}
              class="inline-flex items-center justify-center h-6 w-6 rounded-lg text-[var(--text-muted)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-hover)] transition-colors cursor-pointer"
              title={t("header.pulseRefreshTooltip")}
            >
              <RefreshCw
                size={12}
                class={isDailyPulseSyncing() ? "animate-spin text-[var(--accent-primary)]" : ""}
              />
            </button>
          </div>

          {/* 2x2 Core Stat Cards */}
          <div class="grid grid-cols-2 gap-2.5">
            {/* 1. Today's Tokens */}
            <div class="rounded-xl border border-[var(--border-subtle)] bg-[var(--bg-inset)]/60 p-3">
              <div class="flex items-center justify-between text-[11px] text-[var(--text-muted)]">
                <span>{t("header.pulseTodayTokens")}</span>
                <Zap size={12} class="text-[var(--accent-primary)]" />
              </div>
              <div
                class="mt-1.5 font-mono text-[20px] font-bold tracking-tight text-[var(--text-primary)] leading-none"
                title={stats().totalTokens.toLocaleString()}
              >
                {formatCompactTokens(stats().totalTokens)}
              </div>
              <div class="mt-2 font-mono text-[10.5px] text-[var(--text-muted)] truncate">
                {t("header.pulseCacheHitRate", { rate: stats().cacheHitRate })} ·{" "}
                {t("header.pulseModelTurns", { count: stats().todayTurnsCount })}
              </div>
            </div>

            {/* 2. Today's Spend */}
            <div class="rounded-xl border border-[var(--border-subtle)] bg-[var(--bg-inset)]/60 p-3">
              <div class="flex items-center justify-between text-[11px] text-[var(--text-muted)]">
                <span>{t("header.pulseTodayCost")}</span>
                <Coins size={12} class="text-amber-500" />
              </div>
              <div class="mt-1.5 font-mono text-[20px] font-bold tracking-tight text-[var(--text-primary)] leading-none">
                {formatUsd(stats().totalCost)}
              </div>
              <div class="mt-2 font-mono text-[10.5px] text-[var(--text-muted)] truncate">
                {t("header.pulseLegendInput", { val: formatCompactTokens(stats().inputTokens) })} ·{" "}
                {t("header.pulseLegendOutput", {
                  val: formatCompactTokens(stats().outputTokens + stats().reasoningTokens),
                })}
              </div>
            </div>

            {/* 3. Lines Added */}
            <button
              type="button"
              onClick={() => {
                setOpen(false);
                openDiffInInspector();
              }}
              class="rounded-xl border border-[var(--border-subtle)] bg-[var(--bg-inset)]/60 hover:bg-[var(--bg-hover)] p-3 text-left transition-colors cursor-pointer"
            >
              <div class="flex items-center justify-between text-[11px] text-[var(--text-muted)]">
                <span>{t("header.pulseLinesAdded")}</span>
                <FileCode2 size={12} class="text-emerald-500" />
              </div>
              <div class="mt-1.5 font-mono text-[20px] font-bold tracking-tight text-emerald-500 leading-none">
                +{stats().totalAdditions.toLocaleString()}
              </div>
              <div class="mt-2 font-mono text-[10.5px] text-[var(--text-muted)] truncate">
                {t("header.pulseCommitsToday", { count: stats().todayCommits })}
              </div>
            </button>

            {/* 4. Lines Deleted */}
            <button
              type="button"
              onClick={() => {
                setOpen(false);
                openDiffInInspector();
              }}
              class="rounded-xl border border-[var(--border-subtle)] bg-[var(--bg-inset)]/60 hover:bg-[var(--bg-hover)] p-3 text-left transition-colors cursor-pointer"
            >
              <div class="flex items-center justify-between text-[11px] text-[var(--text-muted)]">
                <span>{t("header.pulseLinesDeleted")}</span>
                <FileCode2 size={12} class="text-rose-500" />
              </div>
              <div class="mt-1.5 font-mono text-[20px] font-bold tracking-tight text-rose-500 leading-none">
                -{stats().totalDeletions.toLocaleString()}
              </div>
              <div class="mt-2 font-mono text-[10.5px] text-[var(--text-muted)] truncate">
                {t("header.pulseLinesDeletedSub", {
                  net: formatNetLoc(stats().netLines),
                  files: stats().changedFilesCount,
                })}
              </div>
            </button>
          </div>

          {/* Models Used Today (Clean & Minimal) */}
          <Show when={stats().modelsUsed.length > 0}>
            <div class="space-y-1.5 pt-0.5">
              <div class="flex items-center justify-between px-0.5 text-[11px] text-[var(--text-muted)]">
                <span>{t("header.pulseSectionModels")}</span>
                <Show when={stats().toolCallsTotal > 0}>
                  <span class="font-mono text-[10.5px]">
                    {t("header.pulseMetricTools")}: {stats().toolSuccessRate}% ({stats().toolCallsTotal})
                  </span>
                </Show>
              </div>
              <div class="space-y-1">
                <For each={stats().modelsUsed}>
                  {(m) => (
                    <div class="flex items-center justify-between gap-2 rounded-xl bg-[var(--bg-inset)]/50 px-3 py-2 text-[11.5px]">
                      <div class="flex items-center gap-2 min-w-0 flex-1">
                        <ProviderIcon id={m.providerID} class="w-3.5 h-3.5 shrink-0" />
                        <span class="font-mono truncate text-[var(--text-primary)]">{m.modelID}</span>
                      </div>
                      <div class="flex items-center gap-2.5 shrink-0 font-mono text-[11px] text-[var(--text-muted)]">
                        <span class="text-[var(--text-secondary)] font-medium">
                          {formatCompactTokens(m.totalTokens)}
                        </span>
                        <span class="text-amber-500 w-12 text-right">{formatUsd(m.cost)}</span>
                      </div>
                    </div>
                  )}
                </For>
              </div>
            </div>
          </Show>

          {/* Footer: Quick link to Models & Providers Configuration */}
          <div class="pt-2.5 border-t border-[var(--border-subtle)] flex items-center justify-between gap-2">
            <span class="text-[11px] text-[var(--text-muted)]">
              {t("header.pulseConnectedProviders", { count: workspace.connectedProviders.length })}
            </span>
            <button
              type="button"
              onClick={() => {
                setOpen(false);
                openProviderModal("providers");
              }}
              class="inline-flex items-center gap-1.5 rounded-lg border border-[var(--border-strong)] bg-[var(--bg-panel)] hover:bg-[var(--bg-hover)] px-2.5 py-1 text-[11px] font-medium text-[var(--text-primary)] transition-colors cursor-pointer"
            >
              <KeyRound size={11} class="text-[var(--accent-primary)]" />
              <span>{t("header.pulseConfigureModelsBtn")}</span>
            </button>
          </div>
        </div>
      </Show>
    </div>
  );
}
