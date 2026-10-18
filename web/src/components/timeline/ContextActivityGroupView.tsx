import { createMemo, createSignal, For, Show, type JSX } from "solid-js";
import type { MessageInfo, MessagePart } from "../../services/sse-types";
import type { MessageTurn } from "../../store/workspace";
import { openFileInInspector } from "../../store/workspace";
import { FileIcon, Icon, Spinner, TextShimmer, ToolStatusTitle } from "../icons";
import { extractTargetFile, kiloToolIconName, resolveToolInput } from "./tool-card-helpers";
import { t as tI18n } from "../../i18n";

function basename(p: string): string {
  const parts = p.split("/").filter(Boolean);
  return parts.length ? parts[parts.length - 1] : p;
}

export function isTodoToolPart(part: MessagePart): boolean {
  return part.type === "tool" && part.tool.toLowerCase() === "todowrite";
}

export function isContextToolPart(part: MessagePart): part is Extract<MessagePart, { type: "tool" }> {
  if (part.type !== "tool") return false;
  if (part.state.status === "error") return false;
  const t = part.tool.toLowerCase();
  return (
    t === "read" ||
    t.endsWith("_read") ||
    t === "grep" ||
    t === "glob" ||
    t === "list" ||
    t === "ls"
  );
}

export function isExplorationPart(part: MessagePart): boolean {
  if (part.type === "reasoning") return true;
  return isContextToolPart(part);
}

export type TurnRenderItem =
  | { kind: "single"; part: MessagePart }
  | { kind: "activity-group"; parts: MessagePart[] };

export function groupAssistantParts(parts: MessagePart[]): TurnRenderItem[] {
  const items: TurnRenderItem[] = [];
  let buffer: MessagePart[] = [];

  const flushBuffer = () => {
    if (buffer.length === 0) return;
    items.push({ kind: "activity-group", parts: buffer });
    buffer = [];
  };

  for (const part of parts) {
    if (isWorkPart(part)) {
      buffer.push(part);
    } else if (isVisibleTextPart(part)) {
      flushBuffer();
      items.push({ kind: "single", part });
    }
  }
  flushBuffer();
  return items;
}

export function isVisibleTextPart(
  part: MessagePart
): part is Extract<MessagePart, { type: "text" }> {
  return part.type === "text" && part.text.trim().length > 0;
}

export function isWorkPart(part: MessagePart): boolean {
  if (part.type === "reasoning") {
    return part.time?.end == null || Boolean(part.text && part.text.trim().length > 0);
  }
  return part.type === "tool";
}

export interface MergedTurnResolution {
  effectiveParts: MessagePart[];
  carriedTurnIDs: string[];
  isVisuallyEmpty: boolean;
  isFirstVisualTurnInCycle: boolean;
  isCycleEnd: boolean;
}

/**
 * Aggregates contiguous work parts (`reasoning` + `tool`) across consecutive
 * assistant turns within the same user prompt cycle so intermediate tool-call
 * turns collapse into a single unified Execution Activity Bar instead of
 * rendering dozens of separate rows.
 */
export function resolveMergedTurnParts(
  allTurns: MessageTurn[],
  globalIndex: number,
  minStartIndex = 0
): MergedTurnResolution {
  let cycleStart = globalIndex;
  while (cycleStart > 0 && allTurns[cycleStart - 1]?.info.role === "assistant") {
    cycleStart--;
  }

  let carriedWork: MessagePart[] = [];
  let carriedTurnIDs: string[] = [];
  let hasPriorVisualTurnInCycle = false;

  for (let i = cycleStart; i <= globalIndex; i++) {
    const t = allTurns[i];
    if (!t || t.info.role !== "assistant") continue;

    const isCycleEnd =
      i === allTurns.length - 1 || allTurns[i + 1]?.info.role !== "assistant";
    const isFlushTurn = isCycleEnd || Boolean(t.info.error);

    let lastTextPartIdx = -1;
    for (let pIdx = t.parts.length - 1; pIdx >= 0; pIdx--) {
      if (isVisibleTextPart(t.parts[pIdx])) {
        lastTextPartIdx = pIdx;
        break;
      }
    }

    if (isFlushTurn) {
      if (i === globalIndex) {
        const effectiveParts =
          carriedWork.length > 0 ? [...carriedWork, ...t.parts] : t.parts;
        const hasRenderable = effectiveParts.some(
          (p) => isWorkPart(p) || isVisibleTextPart(p)
        );
        return {
          effectiveParts,
          carriedTurnIDs,
          isVisuallyEmpty: !hasRenderable && !t.info.error,
          isFirstVisualTurnInCycle: !hasPriorVisualTurnInCycle,
          isCycleEnd,
        };
      }
      if (
        i >= minStartIndex &&
        (carriedWork.length > 0 ||
          t.parts.some((p) => isWorkPart(p) || isVisibleTextPart(p)) ||
          Boolean(t.info.error))
      ) {
        hasPriorVisualTurnInCycle = true;
      }
      carriedWork = [];
      carriedTurnIDs = [];
      continue;
    }

    if (lastTextPartIdx === -1) {
      const ownWork = t.parts.filter(isWorkPart);
      if (i === globalIndex) {
        return {
          effectiveParts: [],
          carriedTurnIDs: [],
          isVisuallyEmpty: true,
          isFirstVisualTurnInCycle: !hasPriorVisualTurnInCycle,
          isCycleEnd: false,
        };
      }
      if (ownWork.length > 0) {
        carriedWork.push(...ownWork);
      }
      carriedTurnIDs.push(t.info.id);
    } else {
      const headSlice = t.parts.slice(0, lastTextPartIdx + 1);
      const tailWork = t.parts.slice(lastTextPartIdx + 1).filter(isWorkPart);
      if (i === globalIndex) {
        return {
          effectiveParts:
            carriedWork.length > 0 ? [...carriedWork, ...headSlice] : headSlice,
          carriedTurnIDs,
          isVisuallyEmpty: false,
          isFirstVisualTurnInCycle: !hasPriorVisualTurnInCycle,
          isCycleEnd: false,
        };
      }
      if (i >= minStartIndex) {
        hasPriorVisualTurnInCycle = true;
      }
      carriedWork = [...tailWork];
      carriedTurnIDs = tailWork.length > 0 ? [t.info.id] : [];
    }
  }

  return {
    effectiveParts: allTurns[globalIndex]?.parts ?? [],
    carriedTurnIDs: [],
    isVisuallyEmpty: false,
    isFirstVisualTurnInCycle: true,
    isCycleEnd: true,
  };
}

/**
 * Returns true when the latest assistant cycle ends with an active work group
 * (no visible text part after the last tool/reasoning part), meaning the inline
 * `<ContextActivityGroupView isLiveTail />` is already serving as the live bottom
 * status bar.
 */
export function hasLiveTailWorkGroup(
  cycleTurns: Array<{ info: MessageInfo; parts: MessagePart[] }>
): boolean {
  if (cycleTurns.length === 0) return false;
  const lastTurn = cycleTurns[cycleTurns.length - 1];
  if (lastTurn.info.error) return false;

  const { effectiveParts } = resolveMergedTurnParts(cycleTurns, cycleTurns.length - 1);
  for (let i = effectiveParts.length - 1; i >= 0; i--) {
    const p = effectiveParts[i];
    if (isVisibleTextPart(p)) return false;
    if (isWorkPart(p)) return true;
  }
  return false;
}

function summarizeContextTool(part: Extract<MessagePart, { type: "tool" }>): {
  action: string;
  target: string;
  filePath?: string;
  detail?: string;
  pills: string[];
} {
  const t = part.tool.toLowerCase();
  const input = resolveToolInput(part);
  const pills: string[] = [];
  if (typeof input.offset === "number") pills.push(`offset=${input.offset}`);
  if (typeof input.limit === "number") pills.push(`limit=${input.limit}`);
  if (typeof input.include === "string" && input.include) pills.push(`include=${input.include}`);

  if (t.includes("read")) {
    const file = extractTargetFile(part) || "";
    return {
      action: tI18n("toolCard.actionRead"),
      target: basename(file) || file || "…",
      filePath: file || undefined,
      detail: file.includes("/") ? file.slice(0, file.lastIndexOf("/")) : undefined,
      pills,
    };
  }
  if (t === "grep") {
    return {
      action: tI18n("toolCard.actionSearch"),
      target: typeof input.pattern === "string" && input.pattern ? `"${input.pattern}"` : "…",
      detail: typeof input.path === "string" ? basename(input.path) : undefined,
      pills,
    };
  }
  if (t === "glob") {
    return {
      action: tI18n("toolCard.actionFind"),
      target: typeof input.pattern === "string" && input.pattern ? input.pattern : "…",
      detail: typeof input.path === "string" ? basename(input.path) : undefined,
      pills,
    };
  }
  const dir = typeof input.path === "string" && input.path ? basename(input.path) : ".";
  return {
    action: tI18n("toolCard.actionList"),
    target: dir,
    pills,
  };
}

export default function ContextActivityGroupView(props: {
  parts: MessagePart[];
  renderFallbackPart: (part: MessagePart) => JSX.Element;
  isLiveTail?: boolean;
}) {
  const isRunning = createMemo(() =>
    props.isLiveTail || props.parts.some((p) => {
      if (p.type === "reasoning") return p.time?.end == null;
      if (p.type === "tool") return p.state.status === "pending" || p.state.status === "running";
      return false;
    })
  );

  const [open, setOpen] = createSignal(false);

  const counts = createMemo(() => {
    let reads = 0;
    let searches = 0;
    let lists = 0;
    let thoughts = 0;
    for (const p of props.parts) {
      if (p.type === "reasoning") {
        thoughts++;
      } else if (p.type === "tool") {
        const t = p.tool.toLowerCase();
        if (t.includes("read")) reads++;
        else if (t === "list" || t === "ls") lists++;
        else searches++;
      }
    }
    const tokens: string[] = [];
    if (reads > 0) tokens.push(tI18n("timeline.reads", { count: reads }));
    if (searches > 0) tokens.push(tI18n("timeline.searches", { count: searches }));
    if (lists > 0) tokens.push(tI18n("timeline.lists", { count: lists }));
    if (thoughts > 0) tokens.push(tI18n("timeline.thoughts", { count: thoughts }));
    return tokens.join(", ");
  });

  const targetPreview = createMemo(() => {
    const names: string[] = [];
    for (const p of props.parts) {
      if (p.type !== "tool") continue;
      const info = summarizeContextTool(p);
      if (info.target && info.target !== "…" && !names.includes(info.target)) {
        names.push(info.target);
      }
    }
    if (names.length === 0) return "";
    const head = names.slice(0, 3).join(", ");
    return names.length > 3 ? `${head} +${names.length - 3}` : head;
  });

  const rollingContextTools = createMemo(() =>
    props.parts
      .filter((p): p is Extract<MessagePart, { type: "tool" }> => p.type === "tool")
      .slice(-5)
  );

  return (
    <div>
      <button
        type="button"
        onClick={() => setOpen((v) => !v)}
        class="group/activity inline-flex max-w-full items-center gap-2 rounded-xl border border-transparent hover:border-[var(--border-subtle)] hover:bg-[var(--bg-inset)] px-2.5 py-1.5 text-[13px] text-[var(--text-muted)] hover:text-[var(--text-primary)] transition-all cursor-pointer"
      >
        <Show
          when={isRunning()}
          fallback={
            <span class="text-[var(--text-muted)] group-hover/activity:text-[var(--accent-primary)] inline-flex items-center shrink-0">
              <Icon name="magnifying-glass-menu" size="small" />
            </span>
          }
        >
          <span class="text-[var(--accent-primary)] inline-flex items-center shrink-0">
            <Spinner class="w-4 h-4" />
          </span>
        </Show>

        <span
          class={
            isRunning()
              ? "font-medium text-[var(--accent-primary)] shrink-0"
              : "font-medium text-[var(--text-secondary)] shrink-0"
          }
        >
          <ToolStatusTitle
            active={isRunning()}
            activeText={tI18n("toolCard.gatheringContext")}
            doneText={tI18n("toolCard.gatheredContext")}
          />
        </span>

        <Show when={counts()}>
          <span class="text-[12px] text-[var(--text-muted)] shrink-0">· {counts()}</span>
        </Show>

        <Show when={!open() && targetPreview()}>
          <span class="hidden sm:inline font-mono text-[11.5px] text-[var(--text-faint)] truncate max-w-[340px]">
            ({targetPreview()})
          </span>
        </Show>

        <span class="text-[var(--text-faint)] inline-flex items-center shrink-0">
          <Icon name={open() ? "chevron-down" : "chevron-right"} size="small" />
        </span>
      </button>

      <Show when={isRunning() && !open() && rollingContextTools().length > 0}>
        <div
          onClick={() => setOpen(true)}
          class="mt-1 ml-2 max-h-[115px] overflow-hidden rounded-xl border border-[var(--border-subtle)] bg-[var(--bg-inset)]/65 px-3 py-2 space-y-1 cursor-pointer"
          style={{
            "-webkit-mask-image": "linear-gradient(to bottom, transparent 0%, black 22%, black 100%)",
            "mask-image": "linear-gradient(to bottom, transparent 0%, black 22%, black 100%)",
          }}
        >
          <For each={rollingContextTools()}>
            {(toolPart) => {
              const info = createMemo(() => summarizeContextTool(toolPart));
              const partRunning = () =>
                toolPart.state.status === "pending" || toolPart.state.status === "running";
              return (
                <div class="flex items-center gap-2 text-[12.5px] kilo-rolling-row min-w-0">
                  <Show
                    when={info().filePath}
                    fallback={
                      <Icon
                        name={kiloToolIconName(toolPart.tool)}
                        size="small"
                        class="text-[var(--text-faint)] shrink-0"
                      />
                    }
                  >
                    {(fp) => (
                      <FileIcon node={{ path: fp(), type: "file" }} class="w-3.5 h-3.5 shrink-0" />
                    )}
                  </Show>
                  <span class="text-[var(--text-muted)] shrink-0">
                    <TextShimmer text={info().action} active={partRunning()} />
                  </span>
                  <span class="font-mono text-[12px] font-medium text-[var(--text-primary)] truncate">
                    {info().target}
                  </span>
                  <Show when={info().detail}>
                    <span class="font-mono text-[11.5px] text-[var(--text-faint)] truncate">
                      {info().detail}/
                    </span>
                  </Show>
                </div>
              );
            }}
          </For>
        </div>
      </Show>

      <Show when={open()}>
        <div class="mt-1.5 ml-2 border-l-2 border-[var(--border-subtle)] pl-3.5 space-y-1">
          <For each={props.parts}>
            {(part) => (
              <Show when={isContextToolPart(part)} fallback={props.renderFallbackPart(part)}>
                {(_) => {
                  const toolPart = part as Extract<MessagePart, { type: "tool" }>;
                  const info = createMemo(() => summarizeContextTool(toolPart));
                  return (
                    <div
                      onClick={() => {
                        const fp = info().filePath;
                        if (fp) openFileInInspector(fp);
                      }}
                      class={`flex items-center justify-between gap-2 rounded-lg px-2.5 py-1.5 text-[12.5px] hover:bg-[var(--bg-hover)] transition-colors ${
                        info().filePath ? "cursor-pointer" : ""
                      }`}
                    >
                      <div class="flex items-center gap-2 min-w-0">
                        <Show
                          when={info().filePath}
                          fallback={
                            <Icon
                              name={kiloToolIconName(toolPart.tool)}
                              size="small"
                              class="text-[var(--text-muted)] shrink-0"
                            />
                          }
                        >
                          {(fp) => (
                            <FileIcon
                              node={{ path: fp(), type: "file" }}
                              class="w-3.5 h-3.5 shrink-0"
                            />
                          )}
                        </Show>
                        <span class="text-[var(--text-secondary)] shrink-0">{info().action}</span>
                        <span class="font-mono text-[12px] font-medium text-[var(--text-primary)] hover:text-[var(--accent-primary)] truncate">
                          {info().target}
                        </span>
                        <Show when={info().detail}>
                          <span class="hidden sm:inline font-mono text-[11.5px] text-[var(--text-faint)] truncate">
                            {info().detail}/
                          </span>
                        </Show>
                      </div>
                      <div class="flex items-center gap-1.5 shrink-0">
                        <For each={info().pills}>
                          {(pill) => (
                            <span class="rounded bg-[var(--bg-inset)] px-1.5 py-0.5 font-mono text-[11px] text-[var(--text-muted)]">
                              {pill}
                            </span>
                          )}
                        </For>
                      </div>
                    </div>
                  );
                }}
              </Show>
            )}
          </For>
        </div>
      </Show>
    </div>
  );
}
