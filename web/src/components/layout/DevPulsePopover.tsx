// web/src/components/layout/DevPulsePopover.tsx
import { createEffect, createMemo, createSignal, For, onCleanup, onMount, Show } from "solid-js";
import {
  Activity,
  BarChart3,
  ChevronDown,
  Coins,
  Cpu,
  FileCode2,
  GitCommitHorizontal,
  KeyRound,
  RefreshCw,
  Sparkles,
  Wrench,
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
  if (amount >= 10) return `$${amount.toFixed(2)}`;
  if (amount >= 0.1) return `$${amount.toFixed(3)}`;
  if (amount >= 0.001) return `$${amount.toFixed(4)}`;
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

  const codeAddRatio = createMemo(() => {
    const s = stats();
    const total = s.totalAdditions + s.totalDeletions;
    return total <= 0 ? 50 : Math.max(8, Math.min(92, Math.round((s.totalAdditions / total) * 100)));
  });

  const tokenMixPercents = createMemo(() => {
    const s = stats();
    const total = Math.max(1, s.totalTokens);
    return {
      input: Math.round((s.inputTokens / total) * 100),
      cache: Math.round(((s.cacheReadTokens + s.cacheWriteTokens) / total) * 100),
      output: Math.round((s.outputTokens / total) * 100),
      reasoning: Math.round((s.reasoningTokens / total) * 100),
    };
  });

  return (
    <div
      ref={triggerRef}
      class="relative inline-flex items-center"
      style={{ "--wails-draggable": "no-drag" } as any}
    >
      {/* Top-Right Developer Pulse Trigger Pill */}
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
            <span class="font-mono text-[10.5px] text-[var(--text-muted)]">
              {formatUsd(stats().totalCost)}
            </span>
          </Show>
        </Show>
        <Show when={stats().totalAdditions > 0 || stats().totalDeletions > 0}>
          <span class="inline-flex items-center gap-1 rounded-full bg-[var(--bg-panel)]/80 px-1.5 py-[0.5px] font-mono text-[10px]">
            <span class="text-emerald-500 font-medium">+{stats().totalAdditions}</span>
            <span class="text-rose-500 font-medium">-{stats().totalDeletions}</span>
          </span>
        </Show>
        <ChevronDown size={11} class="opacity-65 shrink-0" />
      </button>

      {/* Developer Daily Pulse Popover */}
      <Show when={open()}>
        <div
          ref={popoverRef}
          class="absolute right-0 top-full mt-2 z-50 w-[396px] rounded-2xl border border-[var(--border-strong)] bg-[var(--bg-elevated)] p-3.5 shadow-2xl text-xs space-y-3 select-text"
        >
          {/* Header */}
          <div class="flex items-center justify-between">
            <div class="flex items-center gap-2">
              <div class="flex h-6 w-6 items-center justify-center rounded-lg bg-[var(--accent-surface)] text-[var(--accent-primary)]">
                <Activity size={13} />
              </div>
              <span class="font-semibold text-[13px] text-[var(--text-primary)]">
                {t("header.pulseTitle")}
              </span>
              <span class="rounded-md bg-[var(--bg-inset)] px-1.5 py-0.5 font-mono text-[10px] text-[var(--text-muted)]">
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

          {/* 2x2 Primary Hero Cards: Tokens, Cost, +Additions, -Deletions */}
          <div class="grid grid-cols-2 gap-2">
            <div class="rounded-xl border border-[var(--border-subtle)] bg-[var(--bg-inset)]/70 p-2.5">
              <div class="flex items-center justify-between text-[10.5px] text-[var(--text-muted)]">
                <span>{t("header.pulseTodayTokens")}</span>
                <Zap size={11.5} class="text-[var(--accent-primary)]" />
              </div>
              <div
                class="mt-1 font-mono text-[18px] font-bold tracking-tight text-[var(--text-primary)] leading-tight"
                title={stats().totalTokens.toLocaleString()}
              >
                {formatCompactTokens(stats().totalTokens)}
              </div>
              <div class="mt-1 font-mono text-[10px] text-[var(--text-muted)] truncate">
                {t("header.pulseTokensSub", {
                  input: formatCompactTokens(stats().inputTokens),
                  output: formatCompactTokens(stats().outputTokens),
                  reasoning: formatCompactTokens(stats().reasoningTokens),
                })}
              </div>
            </div>

            <div class="rounded-xl border border-[var(--border-subtle)] bg-[var(--bg-inset)]/70 p-2.5">
              <div class="flex items-center justify-between text-[10.5px] text-[var(--text-muted)]">
                <span>{t("header.pulseTodayCost")}</span>
                <Coins size={11.5} class="text-amber-500" />
              </div>
              <div class="mt-1 font-mono text-[18px] font-bold tracking-tight text-[var(--text-primary)] leading-tight">
                {formatUsd(stats().totalCost)}
              </div>
              <div class="mt-1 font-mono text-[10px] text-[var(--text-muted)] truncate">
                {t("header.pulseCostSub", {
                  saved: formatUsd(stats().savedByCache),
                  avg: formatUsd(stats().avgCostPerTurn),
                })}
              </div>
            </div>

            <button
              type="button"
              onClick={() => {
                setOpen(false);
                openDiffInInspector();
              }}
              class="rounded-xl border border-[var(--border-subtle)] bg-[var(--bg-inset)]/70 hover:bg-[var(--bg-hover)] p-2.5 text-left transition-colors cursor-pointer"
            >
              <div class="flex items-center justify-between text-[10.5px] text-[var(--text-muted)]">
                <span>{t("header.pulseLinesAdded")}</span>
                <FileCode2 size={11.5} class="text-emerald-500" />
              </div>
              <div class="mt-1 font-mono text-[18px] font-bold tracking-tight text-emerald-500 leading-tight">
                +{stats().totalAdditions.toLocaleString()}
              </div>
              <div class="mt-1 font-mono text-[10px] text-[var(--text-muted)] truncate">
                {t("header.pulseLinesAddedSub", {
                  uncommitted: stats().uncommittedAdditions.toLocaleString(),
                  committed: stats().committedAdditions.toLocaleString(),
                })}
              </div>
            </button>

            <button
              type="button"
              onClick={() => {
                setOpen(false);
                openDiffInInspector();
              }}
              class="rounded-xl border border-[var(--border-subtle)] bg-[var(--bg-inset)]/70 hover:bg-[var(--bg-hover)] p-2.5 text-left transition-colors cursor-pointer"
            >
              <div class="flex items-center justify-between text-[10.5px] text-[var(--text-muted)]">
                <span>{t("header.pulseLinesDeleted")}</span>
                <FileCode2 size={11.5} class="text-rose-500" />
              </div>
              <div class="mt-1 font-mono text-[18px] font-bold tracking-tight text-rose-500 leading-tight">
                -{stats().totalDeletions.toLocaleString()}
              </div>
              <div class="mt-1 font-mono text-[10px] text-[var(--text-muted)] truncate">
                {t("header.pulseLinesDeletedSub", {
                  net: formatNetLoc(stats().netLines),
                  files: stats().changedFilesCount,
                })}
              </div>
            </button>
          </div>

          {/* Code Velocity & Git Ratio Bar */}
          <div class="rounded-xl border border-[var(--border-subtle)] bg-[var(--bg-inset)]/40 p-2.5 space-y-2">
            <div class="flex items-center justify-between text-[10.5px]">
              <span class="font-semibold text-[var(--text-secondary)] inline-flex items-center gap-1.5">
                <GitCommitHorizontal size={12} class="text-[var(--accent-primary)]" />
                {t("header.pulseSectionCode")}
              </span>
              <span class="font-mono text-[var(--text-muted)]">
                {t("header.pulseCommitsToday", { count: stats().todayCommits })} ·{" "}
                {t("header.pulseWorkingFiles", { count: stats().changedFilesCount })}
              </span>
            </div>
            <div class="h-2 w-full overflow-hidden rounded-full bg-[var(--bg-inset)] flex">
              <div class="h-full bg-emerald-500 transition-all duration-300" style={{ width: `${codeAddRatio()}%` }} />
              <div class="h-full bg-rose-500 transition-all duration-300" style={{ width: `${100 - codeAddRatio()}%` }} />
            </div>
          </div>

          {/* Token Mix & Prompt Cache Hit Rate */}
          <div class="rounded-xl border border-[var(--border-subtle)] bg-[var(--bg-inset)]/40 p-2.5 space-y-2">
            <div class="flex items-center justify-between text-[10.5px]">
              <span class="font-semibold text-[var(--text-secondary)] inline-flex items-center gap-1.5">
                <Sparkles size={11.5} class="text-[var(--accent-primary)]" />
                {t("header.pulseSectionTokens")}
              </span>
              <div class="flex items-center gap-1.5 font-mono text-[10px]">
                <span class="rounded bg-emerald-500/15 px-1.5 py-0.5 text-emerald-500 font-medium">
                  {t("header.pulseCacheHitRate", { rate: stats().cacheHitRate })}
                </span>
                <Show when={stats().reasoningRatio > 0}>
                  <span class="rounded bg-purple-500/15 px-1.5 py-0.5 text-purple-400 font-medium">
                    {t("header.pulseReasoningRatio", { rate: stats().reasoningRatio })}
                  </span>
                </Show>
              </div>
            </div>
            <div class="h-2 w-full overflow-hidden rounded-full bg-[var(--bg-inset)] flex">
              <div class="h-full bg-indigo-500" style={{ width: `${tokenMixPercents().input}%` }} />
              <div class="h-full bg-emerald-500" style={{ width: `${tokenMixPercents().cache}%` }} />
              <div class="h-full bg-amber-500" style={{ width: `${tokenMixPercents().output}%` }} />
              <div class="h-full bg-purple-500" style={{ width: `${tokenMixPercents().reasoning}%` }} />
            </div>
            <div class="grid grid-cols-4 gap-1 font-mono text-[10px] text-[var(--text-muted)]">
              <span class="inline-flex items-center gap-1 truncate">
                <span class="h-1.5 w-1.5 rounded-full bg-indigo-500 shrink-0" />
                {t("header.pulseLegendInput", { val: formatCompactTokens(stats().inputTokens) })}
              </span>
              <span class="inline-flex items-center gap-1 truncate">
                <span class="h-1.5 w-1.5 rounded-full bg-emerald-500 shrink-0" />
                {t("header.pulseLegendCache", { val: formatCompactTokens(stats().cacheReadTokens + stats().cacheWriteTokens) })}
              </span>
              <span class="inline-flex items-center gap-1 truncate">
                <span class="h-1.5 w-1.5 rounded-full bg-amber-500 shrink-0" />
                {t("header.pulseLegendOutput", { val: formatCompactTokens(stats().outputTokens) })}
              </span>
              <span class="inline-flex items-center gap-1 truncate">
                <span class="h-1.5 w-1.5 rounded-full bg-purple-500 shrink-0" />
                {t("header.pulseLegendReasoning", { val: formatCompactTokens(stats().reasoningTokens) })}
              </span>
            </div>
          </div>

          {/* Engineering Telemetry 2x2 Strip */}
          <div class="space-y-1.5">
            <div class="px-0.5 text-[10.5px] font-semibold text-[var(--text-secondary)] flex items-center gap-1.5">
              <Wrench size={11.5} class="text-[var(--accent-primary)]" />
              <span>{t("header.pulseSectionTelemetry")}</span>
            </div>
            <div class="grid grid-cols-2 gap-1.5">
              <div class="rounded-lg border border-[var(--border-subtle)] bg-[var(--bg-inset)]/40 px-2.5 py-1.5">
                <div class="text-[10px] text-[var(--text-muted)]">{t("header.pulseMetricTurns")}</div>
                <div class="mt-0.5 font-mono text-[12px] font-semibold text-[var(--text-primary)]">
                  {t("header.pulseMetricTurnsVal", { turns: stats().todayTurnsCount, sessions: stats().todaySessionsCount })}
                </div>
                <div class="mt-0.5 font-mono text-[9.5px] text-[var(--text-muted)] truncate">
                  {t("header.pulseMetricQualitySub", { errors: stats().errorTurns, reverts: stats().revertCount })}
                </div>
              </div>
              <div class="rounded-lg border border-[var(--border-subtle)] bg-[var(--bg-inset)]/40 px-2.5 py-1.5">
                <div class="text-[10px] text-[var(--text-muted)]">{t("header.pulseMetricTools")}</div>
                <div class="mt-0.5 font-mono text-[12px] font-semibold text-[var(--text-primary)]">
                  {stats().toolSuccessRate}% ({stats().toolCallsTotal})
                </div>
                <div class="mt-0.5 font-mono text-[9.5px] text-[var(--text-muted)] truncate">
                  {t("header.pulseMetricToolsSub", { edit: stats().editToolCalls, bash: stats().bashToolCalls, read: stats().readSearchToolCalls })}
                </div>
              </div>
              <div class="rounded-lg border border-[var(--border-subtle)] bg-[var(--bg-inset)]/40 px-2.5 py-1.5">
                <div class="text-[10px] text-[var(--text-muted)]">{t("header.pulseMetricSpeed")}</div>
                <div class="mt-0.5 font-mono text-[12px] font-semibold text-[var(--text-primary)]">
                  {stats().avgTps > 0 ? `${stats().avgTps} tok/s` : "—"}
                </div>
                <div class="mt-0.5 font-mono text-[9.5px] text-[var(--text-muted)] truncate">
                  {t("header.pulseMetricSpeedSub", { ttft: stats().avgTtftMs > 0 ? `${(stats().avgTtftMs / 1000).toFixed(1)}s` : "—" })}
                </div>
              </div>
              <div class="rounded-lg border border-[var(--border-subtle)] bg-[var(--bg-inset)]/40 px-2.5 py-1.5">
                <div class="text-[10px] text-[var(--text-muted)]">{t("header.pulseMetricQuality")}</div>
                <div class="mt-0.5 font-mono text-[12px] font-semibold text-[var(--text-primary)]">
                  {stats().activeContextPercent}% ({formatCompactTokens(stats().activeContextUsed)}/{formatCompactTokens(stats().activeContextLimit)})
                </div>
                <div class="mt-0.5 font-mono text-[9.5px] text-[var(--text-muted)] truncate">
                  {stats().avgAuditScore !== null ? `Audit ${stats().avgAuditScore}/100` : t("header.pulseNetLoc", { net: formatNetLoc(stats().netLines) })}
                </div>
              </div>
            </div>
          </div>

          {/* Top Models Used Today */}
          <div class="space-y-1.5">
            <div class="px-0.5 text-[10.5px] font-semibold text-[var(--text-secondary)] flex items-center gap-1.5">
              <Cpu size={11.5} class="text-[var(--accent-primary)]" />
              <span>{t("header.pulseSectionModels")}</span>
            </div>
            <Show
              when={stats().modelsUsed.length > 0}
              fallback={
                <div class="rounded-lg border border-dashed border-[var(--border-subtle)] px-3 py-2 text-center text-[11px] text-[var(--text-faint)]">
                  {t("header.pulseNoModelActivity")}
                </div>
              }
            >
              <div class="space-y-1">
                <For each={stats().modelsUsed}>
                  {(m) => (
                    <div class="flex items-center justify-between gap-2 rounded-lg bg-[var(--bg-inset)]/50 px-2.5 py-1.5 text-[11px]">
                      <div class="flex items-center gap-1.5 min-w-0 flex-1">
                        <ProviderIcon id={m.providerID} class="w-3.5 h-3.5 shrink-0" />
                        <span class="font-mono truncate text-[var(--text-primary)]">{m.modelID}</span>
                      </div>
                      <div class="flex items-center gap-2 shrink-0 font-mono text-[10.5px] text-[var(--text-muted)]">
                        <span>{t("header.pulseModelTurns", { count: m.turns })}</span>
                        <span class="text-[var(--text-secondary)] font-medium">{formatCompactTokens(m.totalTokens)}</span>
                        <span class="text-amber-500">{formatUsd(m.cost)}</span>
                      </div>
                    </div>
                  )}
                </For>
              </div>
            </Show>
          </div>

          {/* Footer: Quick link to Models & Providers Configuration */}
          <div class="pt-2 border-t border-[var(--border-subtle)] flex items-center justify-between gap-2">
            <span class="text-[10.5px] text-[var(--text-muted)]">
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
