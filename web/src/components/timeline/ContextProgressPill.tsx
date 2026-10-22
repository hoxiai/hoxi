// web/src/components/timeline/ContextProgressPill.tsx
import { createEffect, createMemo, createSignal, For, onCleanup, Show } from "solid-js";
import { Presence } from "../motion/Presence";
import { Minimize2, LoaderCircle } from "lucide-solid";
import type { MessagePart } from "../../services/sse-types";
import {
  compactSessionContext,
  contextLimitForSelection,
  isAutoModel,
  isSessionBusy,
  resolveAutoModelTarget,
  workspace,
} from "../../store/workspace";
import { t } from "../../i18n";
import { formatCompactNumber } from "../../i18n/format";

function formatTokenValue(n: number): string {
  return n > 0 ? formatCompactNumber(n, { maxDecimals: 1 }) : "—";
}

/** Rough token estimate from a part's text; the model's per-turn totals stay authoritative. */
function estimatePartTokens(part: MessagePart): number {
  let chars = 0;
  switch (part.type) {
    case "text":
    case "reasoning":
      chars = part.text?.length ?? 0;
      break;
    case "tool": {
      const st = part.state;
      try {
        chars = JSON.stringify(st.input ?? {}).length;
      } catch {
        // unserializable input: keep the estimate at zero
      }
      if (st.status === "completed") chars += (st.output ?? "").length;
      if (st.status === "error") chars += (st.error ?? "").length;
      break;
    }
    case "file":
      chars = (part.filename?.length ?? 0) + (part.url?.length ?? 0);
      break;
    case "patch":
      chars = (part.files ?? []).join(",").length;
      break;
  }
  return Math.ceil(chars / 4);
}

function contributorLabel(part: MessagePart, role: "user" | "assistant"): string {
  switch (part.type) {
    case "text":
      return role === "user"
        ? t("timeline.contextPartUser")
        : t("timeline.contextPartAssistant");
    case "reasoning":
      return t("timeline.contextPartReasoning");
    case "tool":
      return `${t("timeline.contextPartTool")} · ${part.tool}`;
    case "file":
      return t("timeline.contextPartFile");
    case "patch":
      return t("timeline.contextPartPatch");
    default:
      return "";
  }
}

/**
 * Ported from kilocode ContextProgress.tsx & TaskUsage.tsx:
 * Displays a live circular SVG progress ring of context window utilization,
 * cache read/write breakdown, cumulative session cost, and 1-click 8-Dim Compaction.
 */
export default function ContextProgressPill() {
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

  const contextLimit = createMemo(() => {
    const selected = workspace.selectedModel;
    // Prefer the window the session is actually running on: a session started on
    // one model and continued on another must not report against the new model.
    const id = workspace.activeSessionID;
    const turns = id ? workspace.messagesBySession[id] ?? [] : [];
    for (let i = turns.length - 1; i >= 0; i--) {
      const info = turns[i]?.info;
      if (info?.providerID && info?.modelID) {
        return contextLimitForSelection(info.providerID, info.modelID);
      }
    }
    return contextLimitForSelection(selected?.providerID, selected?.modelID);
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
    let latestTotal = 0;
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
        latestTotal = t.total || 0;
      }
    }

    const used =
      latestTotal > 0
        ? latestTotal
        : latestInput + latestOutput + latestCacheRead + latestCacheWrite;
    const limit = contextLimit();
    const rawPercent = limit > 0 ? Math.round((used / limit) * 100) : 0;
    const percent = Math.min(100, rawPercent);

    return {
      used,
      limit,
      percent,
      overLimit: rawPercent > 100,
      input: latestInput,
      output: latestOutput,
      reasoning: latestReasoning,
      cacheRead: latestCacheRead,
      cacheWrite: latestCacheWrite,
      totalCost,
    };
  });

  const usageColor = createMemo(() => {
    const s = stats();
    if (!s) return "var(--accent-primary)";
    if (s.overLimit || s.percent >= 85) return "var(--status-danger)";
    if (s.percent >= 65) return "var(--status-warning)";
    return "var(--accent-primary)";
  });

  // A session's biggest single parts, so a token spike can be traced to one item.
  // Local estimate only: the engine's own system prompt and tool schemas are not
  // visible here, so this is a hint, not an authoritative breakdown.
  const topContributors = createMemo(() => {
    const id = workspace.activeSessionID;
    if (!id) return [];
    const turns = workspace.messagesBySession[id] ?? [];
    const items: Array<{ key: string; label: string; tokens: number }> = [];
    for (const turn of turns) {
      for (const part of turn.parts) {
        const tokens = estimatePartTokens(part);
        if (tokens <= 0) continue;
        items.push({ key: part.id, label: contributorLabel(part, turn.info.role), tokens });
      }
    }
    items.sort((a, b) => b.tokens - a.tokens);
    return items.slice(0, 3);
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
      const compacted = await compactSessionContext(id, model);
      if (!compacted) {
        setNotice(t("timeline.contextCompactUnavailable"));
        return;
      }
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
    <Show when={stats()}>
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

              <Show when={s().overLimit}>
                <p role="status" class="mt-2 text-[11px] leading-relaxed text-[var(--danger-text)]">
                  {t("timeline.contextOverLimitHint")}
                </p>
              </Show>
              <Show when={!s().overLimit && s().percent >= 85}>
                <p role="status" class="mt-2 text-[11px] leading-relaxed text-[var(--status-warning)]">
                  {t("timeline.contextNearLimitHint", { percent: s().percent })}
                </p>
              </Show>

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

              <Show when={topContributors().length > 0}>
                <div class="mt-4">
                  <div class="mb-2 flex items-baseline justify-between gap-3">
                    <span class="text-[11px] font-medium tracking-[0.04em] text-[var(--text-faint)]">
                      {t("timeline.contextTopContributors")}
                    </span>
                    <span class="text-[11px] text-[var(--text-faint)]">
                      {t("timeline.contextEstimated")}
                    </span>
                  </div>
                  <ul class="space-y-1.5 text-[11px]">
                    <For each={topContributors()}>
                      {(item) => (
                        <li class="flex items-baseline justify-between gap-2">
                          <span class="truncate text-[var(--text-muted)]">{item.label}</span>
                          <span class="font-mono tabular-nums text-[var(--text-secondary)]">
                            ~{formatCompactNumber(item.tokens)}
                          </span>
                        </li>
                      )}
                    </For>
                  </ul>
                </div>
              </Show>

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
