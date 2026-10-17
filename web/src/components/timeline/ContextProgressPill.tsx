// web/src/components/timeline/ContextProgressPill.tsx
import { createMemo, createSignal, Show } from "solid-js";
import { Minimize2, LoaderCircle } from "lucide-solid";
import { hoxiAPI } from "../../services/api";
import {
  activeModelInfo,
  loadSessionMessages,
  sessionDirectory,
  workspace,
} from "../../store/workspace";
import { t } from "../../i18n";

function formatCompactNumber(n: number): string {
  if (n >= 1_000_000) return `${(n / 1_000_000).toFixed(1)}M`;
  if (n >= 1_000) return `${(n / 1_000).toFixed(1)}k`;
  return String(n);
}

/**
 * Ported from kilocode ContextProgress.tsx & TaskUsage.tsx:
 * Displays a live circular SVG progress ring of context window utilization,
 * cache read/write breakdown, cumulative session cost, and 1-click 8-Dim Compaction.
 */
export default function ContextProgressPill() {
  const [open, setOpen] = createSignal(false);
  const [compacting, setCompacting] = createSignal(false);

  const stats = createMemo(() => {
    const id = workspace.activeSessionID;
    if (!id) return null;
    const turns = workspace.messagesBySession[id] ?? [];
    let latestInput = 0;
    let latestOutput = 0;
    let latestReasoning = 0;
    let latestCacheRead = 0;
    let latestCacheWrite = 0;
    let totalCost = 0;
    let hasTokens = false;

    for (const turn of turns) {
      if (typeof turn.info.cost === "number") {
        totalCost += turn.info.cost;
      }
      const t = turn.info.tokens;
      if (t && (t.input > 0 || t.output > 0 || (t.cache?.read ?? 0) > 0)) {
        hasTokens = true;
        latestInput = t.input || 0;
        latestOutput = t.output || 0;
        latestReasoning = t.reasoning || 0;
        latestCacheRead = t.cache?.read || 0;
        latestCacheWrite = t.cache?.write || 0;
      }
    }

    if (!hasTokens) return null;

    const used =
      latestInput + latestOutput + latestReasoning + latestCacheRead + latestCacheWrite;
    const modelMeta = activeModelInfo();
    const limit = modelMeta?.limit?.context || 200_000;
    const percent = Math.min(100, Math.round((used / limit) * 100));

    return {
      used,
      limit,
      percent,
      input: latestInput,
      output: latestOutput,
      reasoning: latestReasoning,
      cacheRead: latestCacheRead,
      cacheWrite: latestCacheWrite,
      totalCost,
    };
  });

  const ringColor = createMemo(() => {
    const p = stats()?.percent ?? 0;
    if (p >= 85) return "var(--status-danger)";
    if (p >= 65) return "var(--status-warning)";
    return "var(--accent-primary)";
  });

  const onCompact = async () => {
    const id = workspace.activeSessionID;
    const model = workspace.selectedModel;
    if (!id || !model || compacting()) return;
    setCompacting(true);
    try {
      await hoxiAPI.summarizeSession(
        id,
        model.providerID,
        model.modelID,
        sessionDirectory(id)
      );
      await loadSessionMessages(id);
      setOpen(false);
    } catch (err) {
      console.error("summarizeSession failed:", err);
    } finally {
      setCompacting(false);
    }
  };

  const radius = 6;
  const circumference = 2 * Math.PI * radius;

  return (
    <Show when={stats()}>
      {(s) => (
        <div class="relative inline-flex items-center">
          <button
            type="button"
            onClick={() => setOpen((v) => !v)}
            class="inline-flex items-center gap-1.5 rounded-full px-2.5 py-1 text-[11px] font-mono text-[var(--text-muted)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-hover)] transition-colors cursor-pointer"
            title={t("timeline.contextTooltip")}
          >
            {/* Kilo ProgressCircle SVG */}
            <svg width="15" height="15" viewBox="0 0 16 16" class="-rotate-90 shrink-0">
              <circle
                cx="8"
                cy="8"
                r={radius}
                fill="none"
                stroke="var(--border-strong)"
                stroke-width="2"
              />
              <circle
                cx="8"
                cy="8"
                r={radius}
                fill="none"
                stroke={ringColor()}
                stroke-width="2"
                stroke-linecap="round"
                stroke-dasharray={String(circumference)}
                stroke-dashoffset={String(
                  circumference - (s().percent / 100) * circumference
                )}
              />
            </svg>
            <span>{s().percent}%</span>
            <span class="hidden sm:inline text-[var(--text-faint)]">
              · {formatCompactNumber(s().used)}
            </span>
            <Show when={s().totalCost > 0}>
              <span class="hidden md:inline text-[var(--text-faint)]">
                · ${s().totalCost.toFixed(3)}
              </span>
            </Show>
          </button>

          {/* Breakdown Popover */}
          <Show when={open()}>
            <div class="absolute bottom-full left-0 mb-2 w-64 rounded-xl border border-[var(--border-strong)] bg-[var(--bg-elevated)] p-3 shadow-xl z-40 text-xs space-y-2">
              <div class="flex items-center justify-between font-semibold text-[var(--text-primary)]">
                <span>
                  {t("timeline.contextWatermark")}
                </span>
                <span class="font-mono text-[11px] text-[var(--accent-primary)]">
                  {formatCompactNumber(s().used)} / {formatCompactNumber(s().limit)} ({s().percent}%)
                </span>
              </div>

              <div class="space-y-1 border-t border-[var(--border-subtle)] pt-2 font-mono text-[11px] text-[var(--text-secondary)]">
                <div class="flex justify-between">
                  <span>{t("timeline.inputTokens")}</span>
                  <span>{s().input.toLocaleString()}</span>
                </div>
                <div class="flex justify-between">
                  <span>{t("timeline.outputTokens")}</span>
                  <span>{s().output.toLocaleString()}</span>
                </div>
                <Show when={s().reasoning > 0}>
                  <div class="flex justify-between">
                    <span>{t("timeline.reasoningTokens")}</span>
                    <span>{s().reasoning.toLocaleString()}</span>
                  </div>
                </Show>
                <Show when={s().cacheRead > 0 || s().cacheWrite > 0}>
                  <div class="flex justify-between text-[var(--ok-text)]">
                    <span>{t("timeline.cacheReadTokens")}</span>
                    <span>{s().cacheRead.toLocaleString()}</span>
                  </div>
                </Show>
                <Show when={s().totalCost > 0}>
                  <div class="flex justify-between border-t border-[var(--border-subtle)] pt-1 font-semibold text-[var(--text-primary)]">
                    <span>{t("timeline.sessionCost")}</span>
                    <span>${s().totalCost.toFixed(4)}</span>
                  </div>
                </Show>
              </div>

              <button
                type="button"
                disabled={compacting() || !workspace.selectedModel}
                onClick={() => void onCompact()}
                class="w-full inline-flex items-center justify-center gap-1.5 rounded-lg border border-[var(--accent-border)] bg-[var(--accent-surface)] px-2.5 py-1.5 text-[11.5px] font-medium text-[var(--accent-text)] hover:opacity-90 disabled:opacity-50 transition-opacity cursor-pointer"
              >
                <Show
                  when={!compacting()}
                  fallback={<LoaderCircle size={12} class="animate-spin" />}
                >
                  <Minimize2 size={12} />
                </Show>
                <span>
                  {t("timeline.compactAction")}
                </span>
              </button>
            </div>
          </Show>
        </div>
      )}
    </Show>
  );
}
