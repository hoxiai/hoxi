// web/src/components/timeline/TaskTimelineBar.tsx
import { createEffect, createMemo, createSignal, For, on, onCleanup, onMount, Show } from "solid-js";
import { ListTree, Search } from "lucide-solid";
import type { MessagePart, ToolPart } from "../../services/sse-types";
import type { MessageTurn } from "../../store/workspace";
import "./task-timeline.css";
import TranscriptSearchBar from "./TranscriptSearchBar";
import { formatToolDisplayName } from "./tool-card-helpers";
import { t } from "../../i18n";

export interface ActivityBarSegment {
  id: string;
  messageID: string;
  kind: "reasoning" | "explore" | "edit" | "shell" | "task" | "text" | "error";
  color: string;
  label: string;
  heightPct: number;
  duration?: number;
}

function classifyPart(
  part: MessagePart,
  isTurnCompleted: boolean
): {
  kind: ActivityBarSegment["kind"];
  color: string;
  label: string;
  weight: number;
} | null {
  if (part.type === "reasoning") {
    if (!isTurnCompleted && !part.time?.end) {
      return {
        kind: "reasoning",
        color: "#a78bfa",
        label: t("timeline.thinking"),
        weight: 60,
      };
    }
    const len = part.text?.length ?? 0;
    if (len === 0) return null;
    return {
      kind: "reasoning",
      color: "#a78bfa",
      label: t("timeline.activityLength", { label: t("timeline.thought"), count: len }),
      weight: Math.min(100, Math.max(30, Math.round(len / 25))),
    };
  }
  if (part.type === "text") {
    if (!isTurnCompleted) {
      return {
        kind: "text",
        color: "var(--text-muted)",
        label: t("timeline.activityResponse"),
        weight: 55,
      };
    }
    const len = part.text?.length ?? 0;
    if (len === 0) return null;
    return {
      kind: "text",
      color: "var(--text-muted)",
      label: t("timeline.activityLength", { label: t("timeline.activityResponse"), count: len }),
      weight: Math.min(90, Math.max(25, Math.round(len / 30))),
    };
  }
  if (part.type === "tool") {
    const tp = part as ToolPart;
    const name = (tp.tool || "").toLowerCase();
    const displayTool = formatToolDisplayName(tp.tool);
    if (name === "todowrite" || name === "todoread") return null;
    if (tp.state.status === "error") {
      return {
        kind: "error",
        color: "var(--status-danger)",
        label: `${t("toolCard.stateError")}: ${displayTool}`,
        weight: 95,
      };
    }
    if (name === "edit" || name === "write" || name === "patch" || name === "multiedit" || name === "apply_patch") {
      const target = String(tp.state.input?.filePath || tp.state.input?.path || displayTool);
      return {
        kind: "edit",
        color: "#34d399",
        label: `${displayTool}: ${target}`,
        weight: 85,
      };
    }
    if (name === "bash" || name === "shell") {
      const cmd = String(tp.state.input?.command || "bash");
      return {
        kind: "shell",
        color: "#fbbf24",
        label: `${displayTool}: ${cmd}`,
        weight: 75,
      };
    }
    if (name === "task") {
      return {
        kind: "task",
        color: "#38bdf8",
        label: `${displayTool}: ${String(tp.state.input?.description || "task")}`,
        weight: 90,
      };
    }
    return {
      kind: "explore",
      color: "#60a5fa",
      label: `${displayTool}: ${String(tp.state.input?.filePath || tp.state.input?.path || tp.state.input?.pattern || "") || displayTool}`,
      weight: 50,
    };
  }
  return null;
}

/** On-demand transcript navigation and activity history. */
export default function TaskTimelineBar(props: {
  sessionID: string | null;
  turns: MessageTurn[];
  activeMatchTurnID?: string | null;
  readingTurnID?: string | null;
  onJumpToTurn: (messageID: string, partID?: string) => void;
  onToggleSearch: () => void;
  searchOpen: boolean;
  controlsVisible: boolean;
  onCloseSearch: () => void;
  onSearchMatch: (messageID: string | null, partID?: string) => void;
  onWheel?: (event: WheelEvent) => void;
}) {
  const [activeSegment, setActiveSegment] = createSignal<string | null>(null);
  const [interacting, setInteracting] = createSignal(false);
  let rail: HTMLDivElement | undefined;
  let resumeTimer: ReturnType<typeof setTimeout> | undefined;
  onCleanup(() => clearTimeout(resumeTimer));
  const pauseFollowing = () => {
    setInteracting(true);
    clearTimeout(resumeTimer);
    resumeTimer = setTimeout(() => setInteracting(false), 1500);
  };
  const handleRailWheel = (event: WheelEvent) => {
    pauseFollowing();
    const target = event.currentTarget as HTMLElement | null;
    if (target && activeSegment() && target.scrollHeight > target.clientHeight + 1) {
      const canScrollUp = event.deltaY < 0 && target.scrollTop > 0;
      const canScrollDown =
        event.deltaY > 0 && target.scrollTop + target.clientHeight < target.scrollHeight - 1;
      if (canScrollUp || canScrollDown) {
        return;
      }
    }
    if (props.onWheel) {
      event.preventDefault();
      props.onWheel(event);
    }
  };
  const [now, setNow] = createSignal(Date.now());
  createEffect(() => {
    setNow(Date.now());
    const timer = setInterval(() => setNow(Date.now()), 1000);
    onCleanup(() => clearInterval(timer));
  });
  const [navOpen, setNavOpen] = createSignal(false);
  let actions: HTMLDivElement | undefined;
  let navButton: HTMLButtonElement | undefined;
  const onRailKeyDown = (event: KeyboardEvent) => {
    if (event.key === "Escape" && (navOpen() || props.searchOpen)) {
      event.preventDefault();
      event.stopPropagation();
      const button = navButton;
      setNavOpen(false);
      props.onCloseSearch();
      button?.focus();
    }
  };
  createEffect(on(() => props.sessionID, () => {
    setNavOpen(false);
    setActiveSegment(null);
    props.onCloseSearch();
  }));
  createEffect(() => {
    if (props.searchOpen) setNavOpen(false);
  });
  onMount(() => {
    const dismiss = (event: PointerEvent) => {
      if (actions && !actions.contains(event.target as Node) && !rail?.contains(event.target as Node)) {
        setNavOpen(false);
        props.onCloseSearch();
      }
    };
    document.addEventListener("pointerdown", dismiss);
    onCleanup(() => {
      document.removeEventListener("pointerdown", dismiss);
    });
  });
  const segments = createMemo<ActivityBarSegment[]>(() => {
    const out: ActivityBarSegment[] = [];
    const list = props.turns;
    for (let i = 0; i < list.length; i++) {
      const turn = list[i];
      if (turn.info.role === "user") continue;
      const isCompleted = Boolean(turn.info.time?.completed) && i < list.length - 1;
      const turnSegs: ActivityBarSegment[] = [];
      for (const part of turn.parts) {
        const meta = classifyPart(part, isCompleted);
        if (!meta) continue;
        const time: { start: number; end?: number } | undefined = part.type === "reasoning" ? part.time
          : part.type === "tool" && part.state.status !== "pending" ? part.state.time : undefined;
        const duration = time ? Math.max(0, (time.end ?? now()) - time.start) : undefined;
        turnSegs.push({
          id: part.id,
          messageID: turn.info.id,
          kind: meta.kind,
          color: meta.color,
          label: meta.label,
          heightPct: meta.weight,
          duration,
        });
      }
      out.push(...turnSegs);
    }
    return out;
  });

  const maxDuration = createMemo(() => Math.max(1, ...segments().map((seg) => seg.duration ?? 0)));
  const focusedSegment = createMemo(() => segments().filter((seg) => seg.messageID === props.readingTurnID).at(-1)?.id);
  const detailSegment = createMemo(() => segments().find((seg) => seg.id === activeSegment()));
  const [detailTop, setDetailTop] = createSignal(0);
  const showDetail = (id: string, element: HTMLButtonElement) => {
    setActiveSegment(id);
    const bounds = element.getBoundingClientRect();
    setDetailTop(bounds.top + bounds.height / 2 - (rail?.getBoundingClientRect().top ?? 0));
  };
  createEffect(() => {
    const id = focusedSegment();
    if (interacting() || activeSegment()) return;
    queueMicrotask(() => {
      const node = rail?.querySelector<HTMLButtonElement>(`[data-segment-id="${CSS.escape(id ?? "")}"]`);
      const scroller = node?.parentElement;
      if (node && scroller) {
        const top = node.offsetTop;
        if (top < scroller.scrollTop || top + node.offsetHeight > scroller.scrollTop + scroller.clientHeight) {
          scroller.scrollTop = top - scroller.clientHeight / 2;
        }
      }
    });
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
    <Show when={props.turns.length > 0}>
      <nav onKeyDown={onRailKeyDown} class="task-transcript-navigation" aria-label={t("timeline.timelineLabel")}>
        <div ref={actions} class="task-activity-actions"
          classList={{ "is-visible": props.controlsVisible || navOpen() || props.searchOpen }}>
          <TranscriptSearchBar open={props.searchOpen} turns={props.turns}
            onClose={props.onCloseSearch} onActiveTurnChange={props.onSearchMatch} />
          <button type="button" onClick={() => { setNavOpen(false); props.onToggleSearch(); }}
            class="task-activity-icon" classList={{ "is-active": props.searchOpen }}
            aria-label={t("timeline.searchTooltip")} aria-expanded={props.searchOpen}
            title={t("timeline.searchTooltip")}>
            <Search size={13} />
          </button>
          <Show when={userTurns().length > 0}>
            <button
              ref={navButton}
              type="button"
              onClick={() => {
                props.onCloseSearch();
                setNavOpen((v) => !v);
              }}
              class="task-activity-icon" classList={{ "is-active": navOpen() }}
              aria-label={`${t("timeline.turnsMenuTooltip")} (${t("timeline.turnsCount", { count: userTurns().length })})`}
              aria-expanded={navOpen()}
              title={t("timeline.turnsCount", { count: userTurns().length })}
            >
              <ListTree size={13} />
            </button>
          </Show>

          {/* MessageNav Dropdown */}
          <Show when={navOpen()}>
            <div class="task-activity-menu rounded-xl border border-[var(--border-strong)] bg-[var(--bg-elevated)] p-1.5 shadow-xl">
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
        <div ref={rail} class="task-reading-rail" aria-label={t("timeline.timelineLabel")}
          onMouseLeave={() => setActiveSegment(null)}>
          <div class="task-reading-nodes" onWheel={handleRailWheel} onPointerDown={pauseFollowing}
            onTouchMove={pauseFollowing} onKeyDown={pauseFollowing} onScroll={() => setActiveSegment(null)}>
            <For each={segments()}>{(seg) => {
              const highlighted = () => (activeSegment() ?? focusedSegment()) === seg.id;
              return <button type="button" class="task-reading-node" data-segment-id={seg.id}
                aria-label={seg.label} aria-current={focusedSegment() === seg.id ? "step" : undefined}
                onMouseEnter={(event) => showDetail(seg.id, event.currentTarget)}
                onFocus={(event) => showDetail(seg.id, event.currentTarget)} onBlur={() => setActiveSegment(null)}
                onClick={() => props.onJumpToTurn(seg.messageID, seg.id)}>
                <span style={{ background: highlighted() ? seg.color : "var(--text-faint)",
                  width: highlighted() ? `${seg.duration === undefined ? 45 : 20 + 80 * Math.sqrt(seg.duration / maxDuration())}%` : "30%",
                  opacity: highlighted() ? 1 : 0.4 }} />
              </button>;
            }}</For>
          </div>
          <Show when={detailSegment()}>{(seg) => <div class="task-reading-detail" style={{ top: `${detailTop()}px` }}>
            {seg().label}
            <Show when={seg().duration !== undefined}><span class="task-activity-history-duration">{((seg().duration ?? 0) / 1000).toFixed(1)}s</span></Show>
          </div>}</Show>
        </div>
      </nav>
    </Show>
  );
}
