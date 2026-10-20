// web/src/components/timeline/ContextProgressPill.tsx
import { createEffect, createMemo, createSignal, onCleanup, Show } from "solid-js";
import { Presence } from "../motion/Presence";
import { Minimize2, LoaderCircle } from "lucide-solid";
import { hoxiAPI } from "../../services/api";
import {
  activeModelInfo,
  isAutoModel,
  isSessionBusy,
  loadSessionMessages,
  resolveAutoModelTarget,
  sessionDirectory,
  workspace,
} from "../../store/workspace";
import { t } from "../../i18n";

function formatCompactNumber(n: number): string {
  if (n >= 1_000_000) return `${(n / 1_000_000).toFixed(1).replace(/\.0$/, "")}M`;
  if (n >= 1_000) return `${(n / 1_000).toFixed(1).replace(/\.0$/, "")}k`;
  return String(n);
}

function formatTokenValue(n: number): string {
  return n > 0 ? formatCompactNumber(n) : "—";
}

/**
 * Ported from kilocode ContextProgress.tsx & TaskUsage.tsx:
 * Displays a live circular SVG progress ring of context window utilization,
 * cache read/write breakdown, cumulative session cost, and 1-click 8-Dim Compaction.
 */
export default function ContextProgressPill(props: { minPercent?: number }) {
  const [open, setOpen] = createSignal(false);
  const [compacting, setCompacting] = createSignal(false);
  const [notice, setNotice] = createSignal("");
  let containerRef: HTMLDivElement | undefined;

  createEffect(() => {
    if (!open()) return;
    const handlePointerDown = (e: PointerEvent) => {
      if (containerRef && !containerRef.contains(e.target as Node)) {
        setOpen(false);
      }
    };
    const handleKeyDown = (e: KeyboardEvent) => {
      if (e.key === "Escape") setOpen(false);
    };
    window.addEventListener("pointerdown", handlePointerDown);
    window.addEventListener("keydown", handleKeyDown);
    onCleanup(() => {
      window.removeEventListener("pointerdown", handlePointerDown);
      window.removeEventListener("keydown", handleKeyDown);
    });
  });

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

    for (const turn of turns) {
      if (typeof turn.info.cost === "number") {
        totalCost += turn.info.cost;
      }
      const t = turn.info.tokens;
      if (t && (t.input > 0 || t.output > 0 || (t.cache?.read ?? 0) > 0)) {
        latestInput = t.input || 0;
        latestOutput = t.output || 0;
        latestReasoning = t.reasoning || 0;
        latestCacheRead = t.cache?.read || 0;
        latestCacheWrite = t.cache?.write || 0;
      }
    }

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

  const visibleStats = createMemo(() => {
    const current = stats();
    if (!current) return null;
    const threshold = props.minPercent ?? 70;
    return current.percent >= threshold || open() ? current : null;
  });

  const usageColor = createMemo(() => {
    const p = stats()?.percent ?? 0;
    if (p >= 85) return "var(--status-danger)";
    if (p >= 65) return "var(--status-warning)";
    return "var(--accent-primary)";
  });

  const onCompact = async () => {
    const id = workspace.activeSessionID;
    const selected = workspace.selectedModel;
    if (!id || !selected || compacting() || isSessionBusy(id)) return;
    const model = isAutoModel(selected)
      ? resolveAutoModelTarget(selected, { advance: false })
      : selected;
    if (!model) {
      setNotice(t("timeline.contextCompactUnavailable"));
      return;
    }
    setNotice("");
    setCompacting(true);
    try {
      const compacted = await hoxiAPI.summarizeSession(
        id,
        model.providerID,
        model.modelID,
        sessionDirectory(id)
      );
      if (!compacted) {
        setNotice(t("timeline.contextCompactUnavailable"));
        return;
      }
      await loadSessionMessages(id);
      setOpen(false);
    } catch (err) {
      console.error("summarizeSession failed:", err);
      setNotice(t("timeline.contextCompactFailed"));
    } finally {
      setCompacting(false);
    }
  };

  const radius = 6;
  const circumference = 2 * Math.PI * radius;

  return (
    <Show when={visibleStats()}>
      {(s) => (
        <div ref={containerRef} class="relative inline-flex items-center">
          <button
            type="button"
            onClick={() => {
              setOpen((v) => !v);
              setNotice("");
            }}
            class="inline-flex items-center gap-1 rounded-full px-2 py-1 text-[12px] font-mono tabular-nums text-[var(--text-muted)] transition-colors hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)] cursor-pointer"
            title={`${t("timeline.contextTooltip")} · ${formatCompactNumber(s().used)} / ${formatCompactNumber(s().limit)} (${s().percent}%)`}
            aria-haspopup="dialog"
            aria-expanded={open()}
          >
            {/* Kilo ProgressCircle SVG */}
            <svg width="14" height="14" viewBox="0 0 16 16" class="-rotate-90 shrink-0">
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
                stroke-width="2"
                stroke-linecap="round"
                style={{ stroke: usageColor(), transition: "stroke 300ms ease-out" }}
                stroke-dasharray={String(circumference)}
                stroke-dashoffset={String(
                  circumference - (s().percent / 100) * circumference
                )}
              />
            </svg>
            <span>{s().percent}%</span>
          </button>

          {/* Breakdown Popover */}
          <Presence when={open()}>
            {(state) => (
            <div
              data-state={state()}
              style={{ "--hoxi-origin": "bottom right" }}
              class="hoxi-popover absolute bottom-full right-0 z-40 mb-2.5 w-[18rem] select-text rounded-2xl border border-[var(--border-strong)] bg-[var(--bg-panel)] p-4 text-xs shadow-[var(--composer-shadow)]"
              role="dialog"
              aria-label={t("timeline.contextWatermark")}
            >
              <div class="text-[11px] font-medium tracking-[0.04em] text-[var(--text-faint)]">
                {t("timeline.contextWatermark")}
              </div>

              <div class="mt-2.5 flex items-baseline gap-1.5">
                <span class="font-mono text-[19px] font-medium leading-none tracking-tight tabular-nums text-[var(--text-primary)]">
                  {formatCompactNumber(s().used)}
                </span>
                <span class="font-mono text-[11px] tabular-nums text-[var(--text-faint)]">
                  / {formatCompactNumber(s().limit)}
                </span>
              </div>

              <div class="mt-3 h-[3px] w-full overflow-hidden rounded-full bg-[var(--bg-inset)]">
                <div
                  class="h-full rounded-full"
                  style={{
                    width: `${s().percent}%`,
                    "background-color": usageColor(),
                    transition: "width 500ms ease-out, background-color 300ms ease-out",
                  }}
                />
              </div>

              <div class="mt-4">
                <div class="mb-2 flex items-baseline justify-between gap-3">
                  <span class="text-[11px] font-medium tracking-[0.04em] text-[var(--text-faint)]">
                    {t("timeline.contextTokens")}
                  </span>
                  <span class="text-[11px] text-[var(--text-faint)]">
                    {t("timeline.contextThisTurn")}
                  </span>
                </div>
                <dl class="grid grid-cols-2 gap-x-4 gap-y-2 text-[11px]">
                  <div class="flex items-baseline justify-between gap-2">
                    <dt class="text-[var(--text-muted)]">{t("timeline.contextInput")}</dt>
                    <dd class="font-mono tabular-nums text-[var(--text-secondary)]">
                      {formatTokenValue(s().input)}
                    </dd>
                  </div>
                  <div class="flex items-baseline justify-between gap-2">
                    <dt class="text-[var(--text-muted)]">{t("timeline.contextOutput")}</dt>
                    <dd class="font-mono tabular-nums text-[var(--text-secondary)]">
                      {formatTokenValue(s().output)}
                    </dd>
                  </div>
                  <div class="flex items-baseline justify-between gap-2">
                    <dt class="text-[var(--text-muted)]">{t("timeline.contextReasoning")}</dt>
                    <dd class="font-mono tabular-nums text-[var(--text-secondary)]">
                      {formatTokenValue(s().reasoning)}
                    </dd>
                  </div>
                  <div class="flex items-baseline justify-between gap-2">
                    <dt class="text-[var(--text-muted)]">{t("timeline.contextCacheRead")}</dt>
                    <dd class="font-mono tabular-nums text-[var(--text-secondary)]">
                      {formatTokenValue(s().cacheRead)}
                    </dd>
                  </div>
                </dl>
              </div>

              <div class="mt-3 flex items-baseline justify-between gap-3 border-t border-[var(--border-subtle)] pt-2.5 text-[11px]">
                <span class="text-[var(--text-muted)]">{t("timeline.sessionCost")}</span>
                <span class="font-mono tabular-nums text-[var(--text-primary)]">
                  {s().totalCost > 0 ? `$${s().totalCost.toFixed(4)}` : "—"}
                </span>
              </div>

              <button
                type="button"
                disabled={
                  compacting() ||
                  !workspace.selectedModel ||
                  s().used === 0 ||
                  isSessionBusy(workspace.activeSessionID)
                }
                aria-busy={compacting()}
                onClick={() => void onCompact()}
                class="mt-4 inline-flex h-8 w-full items-center justify-center gap-1.5 rounded-lg border border-[var(--border-subtle)] bg-transparent text-[12px] text-[var(--text-secondary)] transition-colors duration-150 hover:border-[var(--border-strong)] hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)] disabled:cursor-default disabled:opacity-50 cursor-pointer"
              >
                <Show
                  when={!compacting()}
                  fallback={<LoaderCircle size={12} class="animate-spin" />}
                >
                  <Minimize2 size={12} />
                </Show>
                <span>{t("timeline.compactAction")}</span>
              </button>

              <Show when={notice()}>
                <p
                  role="status"
                  aria-live="polite"
                  class="mt-2.5 text-[11px] leading-relaxed text-[var(--danger-text)]"
                >
                  {notice()}
                </p>
              </Show>
            </div>
            )}
          </Presence>
        </div>
      )}
    </Show>
  );
}
