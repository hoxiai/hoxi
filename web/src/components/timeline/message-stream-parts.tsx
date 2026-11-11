// web/src/components/timeline/message-stream-parts.tsx
import { createMemo, createSignal, For, Match, Show, Switch } from "solid-js";
import ThoughtBox from "./ThoughtBox";
import ToolCard from "./ToolCard";
import MarkdownView from "./MarkdownView";
import {
  expandInlineThinkingParts,
  extractVisibleAssistantText,
} from "./context-activity-parts";
import ContextActivityGroupComponent, {
  groupAssistantParts,
  hasLiveTailRenderedOutput,
  hasLiveTailWorkGroup,
  isContextToolPart as isContextTool,
  isExplorationPart,
  isTodoToolPart as isTodoTool,
  resolveMergedTurnParts,
  stabilizeTurnRenderItems,
  type TurnRenderItem,
} from "./ContextActivityGroupView";
import type { MessageInfo, MessagePart } from "../../services/sse-types";
import { openAttachmentInInspector, openImagePreview, workspace } from "../../store/workspace";
import { FileIcon, Icon } from "../icons";
import { t } from "../../i18n";
import { AssistantErrorCard, SessionRetryCard } from "./assistant-error-card";
import {
  ActionIconButton,
  extractCommand,
  extractDescription,
  extractTargetFile,
  formatToolDisplayName,
  getFilename,
  hasDistinctShellDescription,
  isShellTool,
  type ToolPart,
} from "./tool-card-helpers";
import { analyzeShellCommand } from "./shell-command-summary";
import ExecutionWaitingIndicator from "./ExecutionWaitingIndicator";
import StalledNotice from "./StalledNotice";
import { AssistantTurnFooter } from "./AssistantTurnFooter";

export {
  ActionIconButton,
  AssistantErrorCard,
  AssistantTurnFooter,
  groupAssistantParts,
  hasLiveTailRenderedOutput,
  hasLiveTailWorkGroup,
  isContextTool,
  isExplorationPart,
  isTodoTool,
  resolveMergedTurnParts,
  SessionRetryCard,
  stabilizeTurnRenderItems,
  type TurnRenderItem,
};

export function resolveActiveCycleStepLabel(
  cycleTurns: Array<{ info: MessageInfo; parts: MessagePart[] }>
): string | undefined {
  const runningTools: ToolPart[] = [];
  for (const turn of cycleTurns) {
    for (const part of turn.parts) {
      if (
        part.type === "tool" &&
        !isTodoTool(part) &&
        (part.state.status === "pending" || part.state.status === "running")
      ) {
        runningTools.push(part as ToolPart);
      }
    }
  }
  if (runningTools.length > 0) {
    const active = runningTools[runningTools.length - 1];
    const toolName = formatToolDisplayName(active.tool, active);
    const file = extractTargetFile(active);
    const cmd = extractCommand(active);
    const desc = extractDescription(active);
    const shellSummary = isShellTool(active.tool)
      ? hasDistinctShellDescription(desc, cmd)
        ? desc
        : analyzeShellCommand(cmd).summary
      : "";
    const detail = file ? getFilename(file) : shellSummary || (cmd ? `$ ${cmd}` : desc || "");
    const base = detail ? `${toolName} · ${detail}` : toolName;
    return runningTools.length > 1 ? `${base} (+${runningTools.length - 1})` : base;
  }
  const lastTurn = cycleTurns[cycleTurns.length - 1];
  if (lastTurn && !lastTurn.info.time?.completed) {
    const hasOpenReasoning = lastTurn.parts.some((p) => {
      if (p.type === "reasoning") return !p.time?.end;
      if (p.type === "text" && p.text && /^\s*</.test(p.text)) {
        return expandInlineThinkingParts([p]).some((ep) => ep.type === "reasoning" && !ep.time?.end);
      }
      return false;
    });
    if (hasOpenReasoning) return t("timeline.thinking");
  }
  return undefined;
}

export function basename(p: string): string {
  const parts = p.split("/").filter(Boolean);
  return parts.length ? parts[parts.length - 1] : "this project";
}

export function ContextActivityGroupView(props: {
  parts: MessagePart[];
  isLiveTail?: boolean;
  isActiveGroup?: boolean;
  startedAt?: number;
  revealKey?: string;
  defaultOpenReasoning?: boolean;
}) {
  return (
    <ContextActivityGroupComponent
      parts={props.parts}
      isLiveTail={props.isLiveTail}
      isActiveGroup={props.isActiveGroup}
      startedAt={props.startedAt}
      revealKey={props.revealKey}
      defaultOpenReasoning={props.defaultOpenReasoning}
      renderFallbackPart={(part, revealKey, defaultOpenReasoning) => (
        <PartView
          part={part}
          revealKey={revealKey}
          streaming={props.isLiveTail}
          defaultOpenReasoning={defaultOpenReasoning}
          inActivityGroup={true}
        />
      )}
    />
  );
}

/** `streaming` marks the live tail of a busy session so its text paces and fades in softly. */
export function PartView(props: {
  part: MessagePart;
  revealKey?: string;
  streaming?: boolean;
  defaultOpenReasoning?: boolean;
  inActivityGroup?: boolean;
}) {
  const mayHaveInlineThinking = createMemo(() => {
    const raw = props.part;
    if (!raw || raw.type !== "text") return false;
    const text = raw.text ?? "";
    const firstNonWs = text.search(/\S/);
    return firstNonWs < 0 || text.charCodeAt(firstNonWs) === 60;
  });

  const expanded = createMemo<MessagePart[]>((prev) => {
    const raw = props.part;
    if (!raw) return prev && prev.length === 0 ? prev : [];
    if (!mayHaveInlineThinking()) {
      return prev && prev.length === 1 && prev[0] === raw ? prev : [raw];
    }
    const next = expandInlineThinkingParts([raw]);
    if (
      prev &&
      prev.length === next.length &&
      prev.every((item, idx) => item === next[idx])
    ) {
      return prev;
    }
    return next;
  });

  return (
    <div data-part-id={props.part?.id}>
      <For each={expanded()}>
        {(part, partIdx) => (
          <Switch>
            <Match when={part.type === "reasoning"}>
              <ThoughtBox
                part={part as Extract<MessagePart, { type: "reasoning" }>}
                revealKey={props.revealKey}
                defaultOpen={props.defaultOpenReasoning}
              />
            </Match>
            <Match when={part.type === "tool"}>
              <ToolCard part={part as Extract<MessagePart, { type: "tool" }>} />
            </Match>
            <Match when={part.type === "file"}>
              <AssistantFilePartView part={part as Extract<MessagePart, { type: "file" }>} />
            </Match>
            <Match when={part.type === "text"}>
              <div class={props.inActivityGroup ? "py-1 text-[13px] text-[var(--text-secondary)]" : undefined}>
                <MarkdownView
                  content={
                    mayHaveInlineThinking()
                      ? extractVisibleAssistantText((part as Extract<MessagePart, { type: "text" }>).text)
                      : (part as Extract<MessagePart, { type: "text" }>).text
                  }
                  density={props.inActivityGroup ? "compact" : "chat"}
                  streaming={Boolean(props.streaming) && partIdx() === expanded().length - 1}
                />
              </div>
            </Match>
          </Switch>
        )}
      </For>
    </div>
  );
}

function AssistantFilePartView(props: { part: Extract<MessagePart, { type: "file" }> }) {
  const isImg = createMemo(() => {
    const fp = props.part;
    return (
      (fp.mime || "").toLowerCase().startsWith("image/") ||
      /^data:image\//i.test(fp.url || "") ||
      /\.(png|jpe?g|webp|gif|bmp|tiff?|svg|heic|heif|avif)$/i.test(fp.filename || "")
    );
  });

  return (
    <Show
      when={isImg()}
      fallback={
        <a
          href={props.part.url}
          target="_blank"
          rel="noreferrer"
          download={props.part.filename || "attachment"}
          onClick={(e) => {
            if (e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) return;
            if (openAttachmentInInspector(props.part.url, props.part.filename)) {
              e.preventDefault();
            }
          }}
          class="my-1 inline-flex max-w-[320px] items-center gap-2 rounded-xl border border-[var(--border-subtle)] bg-[var(--bg-elevated)] px-3 py-2 text-[12px] text-[var(--text-primary)] hover:border-[var(--accent-border)] transition-colors"
        >
          <FileIcon node={{ path: props.part.filename || "file.bin", type: "file" }} class="h-4 w-4 shrink-0" />
          <span class="truncate font-mono">{props.part.filename || "attachment"}</span>
        </a>
      }
    >
      <div class="group/genimg relative my-1.5 inline-block max-w-full sm:max-w-[520px]">
        <button
          type="button"
          onClick={() =>
            openImagePreview({
              src: props.part.url,
              alt: props.part.filename || "generated-image",
            })
          }
          title={t("timeline.previewImageTooltip")}
          class="block max-w-full overflow-hidden rounded-xl border border-[var(--border-subtle)] bg-[var(--bg-inset)] cursor-zoom-in transition-colors hover:border-[var(--accent-border)]"
        >
          <img
            src={props.part.url}
            alt={props.part.filename || "generated-image"}
            loading="lazy"
            decoding="async"
            class="block max-h-[420px] max-w-full object-contain"
          />
        </button>
        <a
          href={props.part.url}
          download={props.part.filename || "generated.png"}
          onClick={(e) => e.stopPropagation()}
          title={t("timeline.downloadImage")}
          class="absolute right-2 bottom-2 inline-flex items-center gap-1 rounded-lg border border-[var(--border-subtle)] bg-[var(--bg-panel)]/90 px-2 py-1 text-[11px] font-medium text-[var(--text-secondary)] hover:text-[var(--text-primary)] hover:border-[var(--accent-border)] opacity-0 group-hover/genimg:opacity-100 transition-opacity backdrop-blur-sm"
        >
          <Icon name="download" size="small" />
        </a>
      </div>
    </Show>
  );
}

const USER_PROMPT_FOLD_LINES = 10;
const USER_PROMPT_FOLD_CHARS = 600;
const MAX_EXPANDED_USER_PROMPTS = 512;

// Survives session-switch remounts so restored reading positions keep their layout.
const expandedUserPrompts = new Set<string>();

export function UserTextPartView(props: {
  part: Extract<MessagePart, { type: "text" }>;
  forceExpand?: boolean;
}) {
  const [expanded, setExpandedSignal] = createSignal(expandedUserPrompts.has(props.part.id));
  const setExpanded = (next: boolean) => {
    if (next) {
      expandedUserPrompts.delete(props.part.id);
      if (expandedUserPrompts.size >= MAX_EXPANDED_USER_PROMPTS) {
        const oldest = expandedUserPrompts.values().next().value;
        if (oldest !== undefined) expandedUserPrompts.delete(oldest);
      }
      expandedUserPrompts.add(props.part.id);
    } else {
      expandedUserPrompts.delete(props.part.id);
    }
    setExpandedSignal(next);
  };
  let containerRef: HTMLDivElement | undefined;

  const lineCount = createMemo(() => {
    const text = props.part.text || "";
    let count = 1;
    for (let i = 0; i < text.length; i++) {
      if (text.charCodeAt(i) === 10) count++;
    }
    return count;
  });

  const isCollapsible = createMemo(
    () =>
      lineCount() > USER_PROMPT_FOLD_LINES ||
      (props.part.text?.length ?? 0) > USER_PROMPT_FOLD_CHARS
  );

  const isExpanded = createMemo(() => expanded() || Boolean(props.forceExpand));

  const toggleExpanded = () => {
    const next = !expanded();
    if (!next && containerRef) {
      const scroller = containerRef.closest(".timeline-result-scroll") as HTMLElement | null;
      if (scroller) {
        const containerRect = containerRef.getBoundingClientRect();
        const scrollerRect = scroller.getBoundingClientRect();
        if (containerRect.top < scrollerRect.top) {
          const offsetDelta = containerRect.top - scrollerRect.top - 12;
          setExpanded(false);
          scroller.scrollTop = Math.max(0, scroller.scrollTop + offsetDelta);
          return;
        }
      }
    }
    setExpanded(next);
  };

  return (
    <Show when={Boolean(props.part.text)}>
      <div ref={containerRef}>
        <div
          data-part-id={props.part.id}
          class={`whitespace-pre-wrap break-words leading-[1.65] text-[var(--text-primary)] selectable-text ${
            isCollapsible() && !isExpanded()
              ? "max-h-[196px] overflow-hidden [mask-image:linear-gradient(to_bottom,black_65%,transparent_100%)]"
              : ""
          }`}
          style={{ "font-size": "var(--app-chat-text-size)" }}
        >
          {props.part.text}
        </div>
        <Show when={isCollapsible() && !props.forceExpand}>
          <button
            type="button"
            onClick={toggleExpanded}
            class="mt-1 inline-flex items-center gap-1 rounded px-1.5 py-0.5 text-[12px] font-medium text-[var(--text-muted)] hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)] transition-colors cursor-pointer select-none"
          >
            <Icon
              name="chevron-down"
              size="small"
              class={`transition-transform duration-150 ${expanded() ? "rotate-180" : ""}`}
            />
            <span>
              {expanded()
                ? t("timeline.collapsePrompt")
                : lineCount() > USER_PROMPT_FOLD_LINES
                  ? t("timeline.expandPromptLines", { count: lineCount() })
                  : t("timeline.expandPrompt")}
            </span>
          </button>
        </Show>
      </div>
    </Show>
  );
}

export function formatMessageTime(ts?: number): string {
  if (!ts) return "";
  try {
    return new Date(ts).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" });
  } catch {
    return "";
  }
}

export function LiveExecutionControlBar(props: {
  sessionID: string | null;
  isSessionBusy: boolean;
  showThinking: boolean;
  awaitingApproval?: boolean;
  completedSteps?: number;
  steps?: import("../../services/sse-types").ToolPart[];
  startedAt?: number;
  activeStepLabel?: string;
  lastTurnRole?: string;
}) {
  return (
    <>
      <Show when={props.showThinking}>
        <ExecutionWaitingIndicator
          awaitingApproval={props.awaitingApproval}
          completedSteps={props.completedSteps ?? 0}
          steps={props.steps ?? []}
          startedAt={props.startedAt}
          activeStepLabel={props.activeStepLabel}
        />
      </Show>

      <Show when={props.isSessionBusy && props.sessionID}>
        {(id) => <StalledNotice stalledSince={workspace.stalledSinceBySession[id()]} />}
      </Show>
    </>
  );
}
