import { createEffect, createMemo, createSignal, Index, onCleanup, onMount, Show } from "solid-js";
import { BarChart3, RefreshCw } from "lucide-solid";
import {
  computeDailyDevPulseStats,
  isDailyPulseSyncing,
  openDiffInInspector,
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

export default function DevPulsePopover() {
  const [open, setOpen] = createSignal(false);
  let triggerRef: HTMLDivElement | undefined;
  let popoverRef: HTMLDivElement | undefined;

  const stats = createMemo(() => computeDailyDevPulseStats());

  const modelsUsed = createMemo(
    () => stats().modelsUsed.slice(0, 5),
    [],
    {
      equals: (prev, next) =>
        prev.length === next.length &&
        prev.every(
          (item, i) =>
            item.providerID === next[i].providerID &&
            item.modelID === next[i].modelID &&
            item.totalTokens === next[i].totalTokens
        ),
    }
  );

  onMount(() => {
    const timer = setInterval(() => {
      if (typeof document !== "undefined" && document.hidden) return;
      void refreshDailyDevPulse(false);
    }, 45_000);
    onCleanup(() => clearInterval(timer));
  });

  createEffect(() => {
    const dir = workspace.rootDir;
    if (dir) void refreshDailyDevPulse(false);
  });

  createEffect(() => {
    if (!open()) return;
    void refreshDailyDevPulse(false);

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
      <button
        type="button"
        onClick={() => setOpen((v) => !v)}
        class={`inline-flex items-center gap-1.5 rounded-lg px-2 py-1 text-[12px] transition-colors cursor-pointer ${
          open()
            ? "bg-[var(--bg-hover)] text-[var(--text-primary)]"
            : "text-[var(--text-secondary)] hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)]"
        }`}
        title={t("header.pulseTooltip")}
      >
        <BarChart3 size={13.5} class="text-[var(--accent-primary)] shrink-0" />
        <Show when={stats().totalTokens > 0}>
          <span class="font-mono text-[11.5px] font-medium text-[var(--text-secondary)]">
            {formatCompactTokens(stats().totalTokens)}
          </span>
        </Show>
      </button>

      <Show when={open()}>
        <div
          ref={popoverRef}
          class="absolute right-0 top-full mt-2 z-50 w-[264px] rounded-xl border border-[var(--border-strong)] bg-[var(--bg-elevated)] p-3.5 shadow-xl text-xs space-y-3 select-text"
        >
          {/* Header */}
          <div class="flex items-center justify-between">
            <span class="font-medium text-[12.5px] text-[var(--text-primary)]">
              {t("header.pulseTitle")}
            </span>
            <button
              type="button"
              onClick={() => void refreshDailyDevPulse(true)}
              class="inline-flex items-center justify-center h-6 w-6 rounded-md text-[var(--text-muted)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-hover)] transition-colors cursor-pointer"
              title={t("header.pulseRefreshTooltip")}
            >
              <RefreshCw
                size={12}
                class={isDailyPulseSyncing() ? "animate-spin text-[var(--accent-primary)]" : ""}
              />
            </button>
          </div>

          {/* 2x2 Minimal Metrics */}
          <div class="grid grid-cols-2 gap-2">
            <div class="rounded-lg bg-[var(--bg-inset)]/60 px-2.5 py-2">
              <div class="text-[11px] text-[var(--text-muted)]">{t("header.pulseTodayTokens")}</div>
              <div
                class="mt-1 font-mono text-[16px] font-semibold text-[var(--text-primary)] leading-tight"
                title={stats().totalTokens.toLocaleString()}
              >
                {formatCompactTokens(stats().totalTokens)}
              </div>
            </div>

            <div class="rounded-lg bg-[var(--bg-inset)]/60 px-2.5 py-2">
              <div class="text-[11px] text-[var(--text-muted)]">{t("header.pulseTodayCost")}</div>
              <div class="mt-1 font-mono text-[16px] font-semibold text-[var(--text-primary)] leading-tight">
                {formatUsd(stats().totalCost)}
              </div>
            </div>

            <button
              type="button"
              onClick={() => {
                setOpen(false);
                openDiffInInspector();
              }}
              class="rounded-lg bg-[var(--bg-inset)]/60 hover:bg-[var(--bg-hover)] px-2.5 py-2 text-left transition-colors cursor-pointer"
            >
              <div class="text-[11px] text-[var(--text-muted)]">{t("header.pulseLinesAdded")}</div>
              <div class="mt-1 font-mono text-[16px] font-semibold text-emerald-500 leading-tight">
                +{stats().totalAdditions.toLocaleString()}
              </div>
            </button>

            <button
              type="button"
              onClick={() => {
                setOpen(false);
                openDiffInInspector();
              }}
              class="rounded-lg bg-[var(--bg-inset)]/60 hover:bg-[var(--bg-hover)] px-2.5 py-2 text-left transition-colors cursor-pointer"
            >
              <div class="text-[11px] text-[var(--text-muted)]">{t("header.pulseLinesDeleted")}</div>
              <div class="mt-1 font-mono text-[16px] font-semibold text-rose-500 leading-tight">
                -{stats().totalDeletions.toLocaleString()}
              </div>
            </button>
          </div>

          {/* Compact Models List */}
          <Show when={modelsUsed().length > 0}>
            <div class="pt-2.5 border-t border-[var(--border-subtle)] space-y-1">
              <Index each={modelsUsed()}>
                {(m) => (
                  <div class="flex items-center justify-between gap-2 px-1 py-1 text-[11.5px]">
                    <div class="flex items-center gap-1.5 min-w-0 flex-1">
                      <ProviderIcon id={m().providerID} class="w-3.5 h-3.5 shrink-0" />
                      <span class="font-mono truncate text-[var(--text-secondary)]">{m().modelID}</span>
                    </div>
                    <span class="font-mono text-[11px] text-[var(--text-muted)] shrink-0">
                      {formatCompactTokens(m().totalTokens)}
                    </span>
                  </div>
                )}
              </Index>
            </div>
          </Show>
        </div>
      </Show>
    </div>
  );
}
