// web/src/components/timeline/TaskTimelineBar.tsx
import { createMemo, createSignal, For, Show } from "solid-js";
import { ListTree, Search } from "lucide-solid";
import type { MessagePart, ToolPart } from "../../services/sse-types";
import type { MessageTurn } from "../../store/workspace";
import { t } from "../../i18n";

export interface ActivityBarSegment {
  id: string;
  messageID: string;
  kind: "reasoning" | "explore" | "edit" | "shell" | "task" | "text" | "error";
  color: string;
  label: string;
  heightPct: number;
}

function classifyPart(part: MessagePart): {
  kind: ActivityBarSegment["kind"];
  color: string;
  label: string;
  weight: number;
} | null {
  if (part.type === "reasoning") {
    const len = part.text?.length ?? 0;
    if (len === 0) return null;
    return {
      kind: "reasoning",
      color: "#a78bfa",
      label: `${t("timeline.thought")} (${Math.max(1, Math.round(len / 100) / 10)}k chars)`,
      weight: Math.min(100, Math.max(30, Math.round(len / 25))),
    };
  }
  if (part.type === "text") {
    const len = part.text?.length ?? 0;
    if (len === 0) return null;
    return {
      kind: "text",
      color: "var(--text-muted)",
      label: `Text (${Math.max(1, Math.round(len / 100) / 10)}k chars)`,
      weight: Math.min(90, Math.max(25, Math.round(len / 30))),
    };
  }
  if (part.type === "tool") {
    const tp = part as ToolPart;
    const name = (tp.tool || "").toLowerCase();
    if (name === "todowrite" || name === "todoread") return null;
    if (tp.state.status === "error") {
      return {
        kind: "error",
        color: "var(--status-danger)",
        label: `Error: ${tp.tool}`,
        weight: 95,
      };
    }
    if (name === "edit" || name === "write" || name === "patch" || name === "multiedit" || name === "apply_patch") {
      const target = String(tp.state.input?.filePath || tp.state.input?.path || tp.tool);
      return {
        kind: "edit",
        color: "#34d399",
        label: `Edit: ${target.split("/").pop() || target}`,
        weight: 85,
      };
    }
    if (name === "bash" || name === "shell") {
      const cmd = String(tp.state.input?.command || "bash").slice(0, 48);
      return {
        kind: "shell",
        color: "#fbbf24",
        label: `Shell: ${cmd}`,
        weight: 75,
      };
    }
    if (name === "task") {
      return {
        kind: "task",
        color: "#38bdf8",
        label: `Subagent: ${String(tp.state.input?.description || "task")}`,
        weight: 90,
      };
    }
    return {
      kind: "explore",
      color: "#60a5fa",
      label: `${tp.tool}: ${String(tp.state.input?.filePath || tp.state.input?.pattern || "").split("/").pop() || tp.tool}`,
      weight: 50,
    };
  }
  return null;
}

/**
 * Ported from kilocode TaskTimeline.tsx & MessageNav.tsx:
 * Horizontal color-grouped activity timeline bar + User Turn quick-jump menu + Search trigger.
 */
export default function TaskTimelineBar(props: {
  turns: MessageTurn[];
  activeMatchTurnID?: string | null;
  onJumpToTurn: (messageID: string) => void;
  onToggleSearch: () => void;
  searchOpen: boolean;
}) {
  const [hoveredSeg, setHoveredSeg] = createSignal<ActivityBarSegment | null>(null);
  const [navOpen, setNavOpen] = createSignal(false);

  const segments = createMemo<ActivityBarSegment[]>(() => {
    const out: ActivityBarSegment[] = [];
    for (const turn of props.turns) {
      if (turn.info.role === "user") continue;
      for (const part of turn.parts) {
        const meta = classifyPart(part);
        if (!meta) continue;
        out.push({
          id: part.id,
          messageID: turn.info.id,
          kind: meta.kind,
          color: meta.color,
          label: meta.label,
          heightPct: meta.weight,
        });
      }
    }
    return out;
  });

  const userTurns = createMemo(() => {
    return props.turns
      .filter((turn) => turn.info.role === "user")
      .map((turn, idx) => {
        const txt = turn.parts
          .filter((p): p is Extract<MessagePart, { type: "text" }> => p.type === "text")
          .map((p) => p.text)
          .join(" ")
          .trim();
        return {
          id: turn.info.id,
          index: idx + 1,
          preview: txt.slice(0, 72) || `#${idx + 1}`,
        };
      });
  });

  return (
    <Show when={segments().length > 0 || userTurns().length > 1}>
      <div class="sticky top-0 z-20 flex items-center justify-between gap-3 border-b border-[var(--border-subtle)] bg-[var(--bg-panel)]/90 px-5 py-1.5 backdrop-blur-md">
        {/* Left: Color-coded SVG/Bar Activity Strip (Kilo TaskTimeline) */}
        <div class="flex items-center gap-2 min-w-0 flex-1">
          <span class="shrink-0 text-[10px] font-semibold uppercase tracking-wider text-[var(--text-faint)]">
            {t("timeline.timelineLabel")}
          </span>

          <div
            class="flex items-end gap-[2px] h-4 overflow-x-auto no-scrollbar py-0.5 max-w-[340px]"
            onMouseLeave={() => setHoveredSeg(null)}
          >
            <For each={segments()}>
              {(seg) => (
                <button
                  type="button"
                  onMouseEnter={() => setHoveredSeg(seg)}
                  onClick={() => props.onJumpToTurn(seg.messageID)}
                  class="w-1.5 shrink-0 rounded-t-[1.5px] transition-all hover:opacity-100 opacity-80 cursor-pointer"
                  style={{
                    height: `${Math.max(25, seg.heightPct)}%`,
                    "background-color": seg.color,
                  }}
                  title={seg.label}
                />
              )}
            </For>
          </div>

          <Show when={hoveredSeg()}>
            {(seg) => (
              <span class="truncate font-mono text-[11px] text-[var(--text-secondary)]">
                {seg().label}
              </span>
            )}
          </Show>
        </div>

        {/* Right: MessageNav Turn Jump Menu & Transcript Search Trigger */}
        <div class="flex items-center gap-1.5 shrink-0 relative">
          <Show when={userTurns().length > 0}>
            <button
              type="button"
              onClick={() => setNavOpen((v) => !v)}
              class="inline-flex items-center gap-1 rounded-md border border-[var(--border-subtle)] bg-[var(--bg-elevated)] px-2 py-0.5 text-[11px] text-[var(--text-secondary)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-hover)] transition-colors cursor-pointer"
              title={t("timeline.turnsMenuTooltip")}
            >
              <ListTree size={11} class="text-[var(--accent-primary)]" />
              <span class="font-mono">
                {t("timeline.turnsCount", { count: userTurns().length })}
              </span>
            </button>
          </Show>

          <button
            type="button"
            onClick={props.onToggleSearch}
            class={`inline-flex items-center gap-1 rounded-md border px-2 py-0.5 text-[11px] transition-colors cursor-pointer ${
              props.searchOpen
                ? "border-[var(--accent-border)] bg-[var(--accent-surface)] text-[var(--accent-text)]"
                : "border-[var(--border-subtle)] bg-[var(--bg-elevated)] text-[var(--text-secondary)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-hover)]"
            }`}
            title={t("timeline.searchTooltip")}
          >
            <Search size={11} />
            <span class="hidden sm:inline">{t("timeline.searchTrigger")}</span>
          </button>

          {/* MessageNav Dropdown */}
          <Show when={navOpen()}>
            <div class="absolute right-0 top-full mt-1.5 w-72 rounded-xl border border-[var(--border-strong)] bg-[var(--bg-elevated)] p-1.5 shadow-xl z-40">
              <div class="px-2 py-1 text-[10px] font-semibold uppercase tracking-wider text-[var(--text-faint)]">
                {t("timeline.turnsMenuTitle")}
              </div>
              <div class="max-h-56 overflow-y-auto space-y-0.5">
                <For each={userTurns()}>
                  {(u) => (
                    <button
                      type="button"
                      onClick={() => {
                        setNavOpen(false);
                        props.onJumpToTurn(u.id);
                      }}
                      class="flex w-full items-center gap-2 rounded-lg px-2 py-1.5 text-left text-[11.5px] text-[var(--text-secondary)] hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)] cursor-pointer"
                    >
                      <span class="shrink-0 rounded bg-[var(--bg-inset)] px-1.5 py-0.5 font-mono text-[10px] text-[var(--text-muted)]">
                        #{u.index}
                      </span>
                      <span class="truncate">{u.preview}</span>
                    </button>
                  )}
                </For>
              </div>
            </div>
          </Show>
        </div>
      </div>
    </Show>
  );
}
