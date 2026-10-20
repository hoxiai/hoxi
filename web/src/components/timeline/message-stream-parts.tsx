// web/src/components/timeline/message-stream-parts.tsx
import { createMemo, createSignal, Match, onCleanup, Show, Switch } from "solid-js";
import ThoughtBox from "./ThoughtBox";
import ToolCard from "./ToolCard";
import MarkdownView from "./MarkdownView";
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
import {
  canRevertSession,
  isUserAbortError,
   resumePausedSession,
  retryLastUserPrompt,
  revertToHere,
  workspace,
} from "../../store/workspace";
import { ObserverAuditBadge } from "../inspector/ObserverAuditBanner";
import { Icon } from "../icons";
import { t } from "../../i18n";
import { copyMessageLink } from "./session-export";
import { AssistantErrorCard, SessionRetryCard } from "./assistant-error-card";
import {
  ActionIconButton,
  extractCommand,
  extractDescription,
  extractTargetFile,
  formatToolDisplayName,
  getFilename,
  type ToolPart,
} from "./tool-card-helpers";
import {
  collectCycleChangedFiles,
  deriveMarkdownDownloadFilename,
} from "./tool-card-state";
import ExecutionWaitingIndicator from "./ExecutionWaitingIndicator";
import StalledNotice from "./StalledNotice";
import ChangedFilesSummary from "./ChangedFilesSummary";
import TurnTelemetryPopover from "./TurnTelemetryPopover";


export {
  ActionIconButton,
  AssistantErrorCard,
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
    const detail = file ? getFilename(file) : cmd ? `$ ${cmd}` : desc || "";
    const base = detail ? `${toolName} · ${detail}` : toolName;
    return runningTools.length > 1 ? `${base} (+${runningTools.length - 1})` : base;
  }
  const lastTurn = cycleTurns[cycleTurns.length - 1];
  if (lastTurn && !lastTurn.info.time?.completed) {
    const hasOpenReasoning = lastTurn.parts.some(
      (p) => p.type === "reasoning" && !p.time?.end
    );
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
  startedAt?: number;
  revealKey?: string;
}) {
  return (
    <ContextActivityGroupComponent
      parts={props.parts}
      isLiveTail={props.isLiveTail}
      startedAt={props.startedAt}
      revealKey={props.revealKey}
      renderFallbackPart={(part, revealKey) => (
        <PartView part={part} revealKey={revealKey} />
      )}
    />
  );
}

export function PartView(props: { part: MessagePart; revealKey?: string }) {
  const part = () => props.part;
  return (
    <div data-part-id={part()?.id}>
      <Switch>
        <Match when={part()?.type === "reasoning"}>
          <ThoughtBox
            part={part() as Extract<MessagePart, { type: "reasoning" }>}
            revealKey={props.revealKey}
          />
        </Match>
        <Match when={part()?.type === "tool"}>
          <ToolCard part={part() as Extract<MessagePart, { type: "tool" }>} />
        </Match>
        <Match when={part()?.type === "text"}>
          <div>
            <MarkdownView content={(part() as Extract<MessagePart, { type: "text" }>).text} />
          </div>
        </Match>
      </Switch>
    </div>
  );
}

const USER_PROMPT_FOLD_LINES = 10;
const USER_PROMPT_FOLD_CHARS = 600;

export function UserTextPartView(props: {
  part: Extract<MessagePart, { type: "text" }>;
  forceExpand?: boolean;
}) {
  const [expanded, setExpanded] = createSignal(false);
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

export function AssistantTurnFooter(props: {
  info: MessageInfo;
  parts: MessagePart[];
  turns?: Array<{ info: MessageInfo; parts: MessagePart[] }>;
  turnIndex?: number;
  isSessionBusy?: boolean;
}) {
  const [copied, setCopied] = createSignal(false);
  const [linkCopied, setLinkCopied] = createSignal(false);
  const [downloaded, setDownloaded] = createSignal(false);
  const [resuming, setResuming] = createSignal(false);
  let copyTimer: ReturnType<typeof setTimeout> | undefined;
  let linkTimer: ReturnType<typeof setTimeout> | undefined;
  let downloadTimer: ReturnType<typeof setTimeout> | undefined;
  let revokeTimer: ReturnType<typeof setTimeout> | undefined;
  let activeBlobURL: string | undefined;

  onCleanup(() => {
    if (copyTimer !== undefined) clearTimeout(copyTimer);
    if (linkTimer !== undefined) clearTimeout(linkTimer);
    if (downloadTimer !== undefined) clearTimeout(downloadTimer);
    if (revokeTimer !== undefined) clearTimeout(revokeTimer);
    if (activeBlobURL) {
      URL.revokeObjectURL(activeBlobURL);
      activeBlobURL = undefined;
    }
  });

  const isLatestTurn = createMemo(() => {
    if (!props.turns || typeof props.turnIndex !== "number") return true;
    if (props.turnIndex === props.turns.length - 1) return true;
    if (!props.info.time?.completed && !props.info.error) {
      return props.turns.slice(props.turnIndex + 1).every((t) => t.info.role === "user");
    }
    return false;
  });

  // Commit actions belong to the current exchange only; historical cards keep
  // their revert/view buttons but must not offer to commit today's tree.
  const isLatestExchange = createMemo(() => {
    if (!props.turns || typeof props.turnIndex !== "number") return true;
    let lastUserIndex = -1;
    props.turns.forEach((turn, index) => {
      if (turn.info.role === "user") lastUserIndex = index;
    });
    return props.turnIndex > lastUserIndex;
  });

  const interruptMode = createMemo<"paused" | "stopped" | null>(() => {
    const sessionInterrupt = workspace.interruptedBySession[props.info.sessionID];
    if (isLatestTurn() && sessionInterrupt) {
      return sessionInterrupt;
    }
    if (isUserAbortError(props.info)) {
      return sessionInterrupt === "paused" ? "paused" : "stopped";
    }
    return null;
  });

  const isTurnExecuting = createMemo(() =>
    Boolean(props.isSessionBusy && !interruptMode() && isLatestTurn())
  );

  const cycleSummary = createMemo(() => {
    if (isTurnExecuting()) return null;
    if (props.info.error && !isUserAbortError(props.info)) return null;
    const list = props.turns;
    const idx = props.turnIndex;

    if (list && typeof idx === "number") {
      const next = list[idx + 1];
      // Intermediate step in a multi-step assistant response cycle
      if (next && next.info.role === "assistant") return null;
      if (
        !next &&
        !props.info.time?.completed &&
        props.parts.length === 0 &&
        !interruptMode()
      ) {
        return null;
      }

      let startIdx = idx;
      while (startIdx > 0 && list[startIdx - 1]?.info.role === "assistant") {
        startIdx--;
      }
      const cycleTurns = list.slice(startIdx, idx + 1);
      const created = cycleTurns[0]?.info.time?.created || props.info.time?.created;
      const completed = props.info.time?.completed;
      let toolCount = 0;
      let providerID = props.info.providerID || "";
      let modelID = props.info.modelID || "";
      let inputTokens = 0;
      let outputTokens = 0;
      let reasoningTokens = 0;
      let cacheReadTokens = 0;
      let cost = 0;
      const textChunks: string[] = [];

      for (const step of cycleTurns) {
        if (step.info.providerID) providerID = step.info.providerID;
        if (step.info.modelID) modelID = step.info.modelID;
        inputTokens += step.info.tokens?.input ?? 0;
        outputTokens += step.info.tokens?.output ?? 0;
        reasoningTokens += step.info.tokens?.reasoning ?? 0;
        cacheReadTokens += step.info.tokens?.cache?.read ?? 0;
        cost += step.info.cost ?? 0;
        for (const p of step.parts) {
          if (p.type === "tool" && !isTodoTool(p)) toolCount++;
        }
      }

      const ownTexts = props.parts
        .filter((p): p is Extract<MessagePart, { type: "text" }> => p.type === "text")
        .map((p) => p.text.trim())
        .filter(Boolean);

      if (ownTexts.length > 0) {
        textChunks.push(...ownTexts);
      } else {
        for (let i = cycleTurns.length - 1; i >= 0; i--) {
          const stepTexts = cycleTurns[i].parts
            .filter((p): p is Extract<MessagePart, { type: "text" }> => p.type === "text")
            .map((p) => p.text.trim())
            .filter(Boolean);
          if (stepTexts.length > 0) {
            textChunks.push(...stepTexts);
            break;
          }
        }
      }

      const revertMessageID =
        (startIdx > 0 && list[startIdx - 1]?.info.role === "user"
          ? list[startIdx - 1]?.info.id
          : cycleTurns[0]?.info.id) || props.info.id;

      return {
        created,
        completed,
        toolCount,
        providerID,
        modelID,
        inputTokens,
        outputTokens,
        reasoningTokens,
        cacheReadTokens,
        cost,
        finalText: textChunks.join("\n\n").trim(),
        changedFiles: collectCycleChangedFiles(cycleTurns, props.info.sessionID),
        revertMessageID,
      };
    }

    const texts = props.parts
      .filter((p): p is Extract<MessagePart, { type: "text" }> => p.type === "text")
      .map((t) => t.text)
      .join("\n\n")
      .trim();
    const toolCount = props.parts.filter((p) => p.type === "tool" && !isTodoTool(p)).length;
    return {
      created: props.info.time?.created,
      completed: props.info.time?.completed,
      toolCount,
      providerID: props.info.providerID || "",
      modelID: props.info.modelID || "",
      inputTokens: props.info.tokens?.input ?? 0,
      outputTokens: props.info.tokens?.output ?? 0,
      reasoningTokens: props.info.tokens?.reasoning ?? 0,
      cacheReadTokens: props.info.tokens?.cache?.read ?? 0,
      cost: props.info.cost ?? 0,
      finalText: texts,
      changedFiles: collectCycleChangedFiles(
        [{ info: props.info, parts: props.parts }],
        props.info.sessionID
      ),
      revertMessageID: props.info.id,
    };
  });

  const durationLabel = createMemo(() => {
    const summary = cycleSummary();
    if (!summary?.created || !summary?.completed || summary.completed <= summary.created) {
      return "";
    }
    const sec = (summary.completed - summary.created) / 1000;
    if (sec < 0.05) return "";
    return sec < 60 ? `${sec.toFixed(1)}s` : `${Math.floor(sec / 60)}m ${Math.round(sec % 60)}s`;
  });

  const copyResponse = async () => {
    const text = cycleSummary()?.finalText;
    if (!text) return;
    try {
      await navigator.clipboard.writeText(text);
      setCopied(true);
      if (copyTimer !== undefined) clearTimeout(copyTimer);
      copyTimer = setTimeout(() => setCopied(false), 1800);
    } catch {
      /* ignore */
    }
  };

  const copyLink = async () => {
    const ok = await copyMessageLink(props.info.sessionID, props.info.id);
    if (!ok) return;
    setLinkCopied(true);
    if (linkTimer !== undefined) clearTimeout(linkTimer);
    linkTimer = setTimeout(() => setLinkCopied(false), 1800);
  };

  const downloadMarkdown = () => {
    const text = cycleSummary()?.finalText;
    if (!text) return;
    try {
      if (activeBlobURL) {
        URL.revokeObjectURL(activeBlobURL);
        activeBlobURL = undefined;
      }
      if (revokeTimer !== undefined) clearTimeout(revokeTimer);
      const content = text.endsWith("\n") ? text : `${text}\n`;
      const blob = new Blob([content], { type: "text/markdown;charset=utf-8" });
      const url = URL.createObjectURL(blob);
      activeBlobURL = url;
      const link = document.createElement("a");
      link.href = url;
      link.download = deriveMarkdownDownloadFilename(text, props.info.sessionID);
      document.body.appendChild(link);
      link.click();
      link.remove();
      setDownloaded(true);
      if (downloadTimer !== undefined) clearTimeout(downloadTimer);
      downloadTimer = setTimeout(() => setDownloaded(false), 1800);
      revokeTimer = setTimeout(() => {
        if (activeBlobURL === url) activeBlobURL = undefined;
        URL.revokeObjectURL(url);
      }, 60_000);
    } catch {
      /* ignore */
    }
  };

  const onResume = async () => {
    if (resuming()) return;
    setResuming(true);
    try {
      await resumePausedSession(props.info.sessionID);
    } finally {
      setResuming(false);
    }
  };

  return (
    <Show when={cycleSummary()}>
      {(summary) => (
        <Show when={!isTurnExecuting()}>
          <div class="mt-4 space-y-2 select-none">
            <ChangedFilesSummary
              files={summary().changedFiles}
              commitSessionID={isLatestExchange() ? props.info.sessionID : undefined}
              onUndo={
                canRevertSession(props.info.sessionID)
                  ? () => void revertToHere(props.info.sessionID, summary().revertMessageID)
                  : undefined
              }
            />

            <div class="flex items-center gap-2 flex-wrap text-[12px] text-[var(--text-faint)]">

              <Show when={summary().finalText.length > 0}>
                <ActionIconButton
                  label={copied() ? t("common.copied") : t("common.copy")}
                  onClick={() => void copyResponse()}
                >
                  <Show when={copied()} fallback={<Icon name="copy" size="small" />}>
                    <Icon name="check" size="small" class="text-[var(--ok-text)]" />
                  </Show>
                </ActionIconButton>

                <ActionIconButton
                  label={
                    downloaded()
                      ? t("timeline.downloadedMarkdown")
                      : t("timeline.downloadMarkdown")
                  }
                  onClick={downloadMarkdown}
                >
                  <Show when={downloaded()} fallback={<Icon name="download" size="small" />}>
                    <Icon name="check" size="small" class="text-[var(--ok-text)]" />
                  </Show>
                </ActionIconButton>

                <ActionIconButton
                  label={linkCopied() ? t("common.copied") : t("commandPalette.actionCopySessionLink")}
                  onClick={() => void copyLink()}
                >
                  <Show when={linkCopied()} fallback={<Icon name="link" size="small" />}>
                    <Icon name="check" size="small" class="text-[var(--ok-text)]" />
                  </Show>
                </ActionIconButton>
              </Show>

              <Show when={interruptMode()}>
                {(mode) => (
                  <span
                    class={`inline-flex items-center gap-1 font-medium ${
                      mode() === "paused"
                        ? "text-[var(--warn-text)]"
                        : "text-[var(--danger-text)]"
                    }`}
                  >
                    <span class="font-mono text-[11px] leading-none">
                      {mode() === "paused" ? "⏸" : "■"}
                    </span>
                    <span>
                      {mode() === "paused"
                        ? t("timeline.turnPaused")
                        : t("timeline.turnStopped")}
                    </span>
                  </span>
                )}
              </Show>

              <Show when={durationLabel()}>
                <TurnTelemetryPopover
                  durationLabel={durationLabel()}
                  durationMs={Math.max(
                    0,
                    (summary().completed || 0) - (summary().created || 0)
                  )}
                  providerID={summary().providerID}
                  modelID={summary().modelID}
                  toolCount={summary().toolCount}
                  inputTokens={summary().inputTokens}
                  outputTokens={summary().outputTokens}
                  reasoningTokens={summary().reasoningTokens}
                  cacheReadTokens={summary().cacheReadTokens}
                  cost={summary().cost}
                />
              </Show>

              <Show when={isLatestTurn() && interruptMode() && !props.isSessionBusy}>
                <button
                  type="button"
                  disabled={resuming()}
                  onClick={() => void onResume()}
                  title={t("timeline.resumeTooltip")}
                  class="inline-flex items-center gap-1 rounded-md px-2 py-0.5 text-[12px] font-medium text-[var(--accent-primary)] hover:bg-[var(--accent-surface)] disabled:opacity-50 transition-colors cursor-pointer"
                >
                  <span class="text-[11px] leading-none">▶</span>
                  <span>{t("timeline.resume")}</span>
                </button>

                <button
                  type="button"
                  onClick={() => void retryLastUserPrompt(props.info.sessionID)}
                  class="inline-flex items-center gap-1 rounded-md px-2 py-0.5 text-[12px] text-[var(--text-muted)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-hover)] transition-colors cursor-pointer"
                >
                  <span>↺</span>
                  <span>{t("timeline.retryTask")}</span>
                </button>
              </Show>

              <Show when={isLatestTurn()}>
                <ObserverAuditBadge sessionID={props.info.sessionID} />
              </Show>
            </div>
          </div>
        </Show>
      )}
    </Show>

  );
}

export function LiveExecutionControlBar(props: {
  sessionID: string | null;
  isSessionBusy: boolean;
  showThinking: boolean;
  completedSteps?: number;
  steps?: import("../../services/sse-types").ToolPart[];
  startedAt?: number;
  activeStepLabel?: string;
  lastTurnRole?: string;
}) {
  const [busyAction, setBusyAction] = createSignal(false);

  const interruptedMode = createMemo(() => {
    const id = props.sessionID;
    return id ? workspace.interruptedBySession[id] : undefined;
  });

  const onResume = async () => {
    if (!props.sessionID || busyAction()) return;
    setBusyAction(true);
    try {
      await resumePausedSession(props.sessionID);
    } finally {
      setBusyAction(false);
    }
  };

  return (
    <>
      <Show when={props.showThinking}>
        <ExecutionWaitingIndicator
          completedSteps={props.completedSteps ?? 0}
          steps={props.steps ?? []}
          startedAt={props.startedAt}
          activeStepLabel={props.activeStepLabel}
        />
      </Show>

      <Show when={props.isSessionBusy && props.sessionID}>
        {(id) => <StalledNotice stalledSince={workspace.stalledSinceBySession[id()]} />}
      </Show>

      <Show
        when={
          !props.isSessionBusy &&
          interruptedMode() &&
          props.lastTurnRole === "user"
        }
      >
        <div class="mt-3 flex items-center gap-2 flex-wrap px-1 text-[12px] text-[var(--text-faint)] select-none">
          <span
            class={`inline-flex items-center gap-1 font-medium ${
              interruptedMode() === "paused"
                ? "text-[var(--warn-text)]"
                : "text-[var(--danger-text)]"
            }`}
          >
            <span class="font-mono text-[11px] leading-none">
              {interruptedMode() === "paused" ? "⏸" : "■"}
            </span>
            <span>
              {interruptedMode() === "paused"
                ? t("timeline.turnPaused")
                : t("timeline.turnStopped")}
            </span>
          </span>

          <button
            type="button"
            disabled={busyAction()}
            onClick={() => void onResume()}
            title={t("timeline.resumeTooltip")}
            class="inline-flex items-center gap-1 rounded-md px-2 py-0.5 text-[12px] font-medium text-[var(--accent-primary)] hover:bg-[var(--accent-surface)] disabled:opacity-50 transition-colors cursor-pointer"
          >
            <span class="text-[11px] leading-none">▶</span>
            <span>{t("timeline.resume")}</span>
          </button>

          <button
            type="button"
            onClick={() => void retryLastUserPrompt(props.sessionID)}
            class="inline-flex items-center gap-1 rounded-md px-2 py-0.5 text-[12px] text-[var(--text-muted)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-hover)] transition-colors cursor-pointer"
          >
            <span>↺</span>
            <span>{t("timeline.retryTask")}</span>
          </button>
        </div>
      </Show>
    </>
  );
}
