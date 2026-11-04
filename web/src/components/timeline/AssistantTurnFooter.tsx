// web/src/components/timeline/AssistantTurnFooter.tsx
import { createMemo, createSignal, onCleanup, Show } from "solid-js";
import { hasLeakedReasoningToolMarkup, normalizeReasoningText } from "./ThoughtBox";
import { extractVisibleAssistantText } from "./context-activity-parts";
import { isTodoToolPart as isTodoTool } from "./ContextActivityGroupView";
import type { MessageInfo, MessagePart } from "../../services/sse-types";
import {
  canRevertSession,
  isUserAbortError,
  resumePausedSession,
  retryLastUserPrompt,
  revertToHere,
  workspace,
} from "../../store/workspace";
import { Icon } from "../icons";
import { t } from "../../i18n";
import { copyMessageLink } from "./session-export";
import { ActionIconButton } from "./tool-card-helpers";
import {
  collectCycleChangedFiles,
  deriveMarkdownDownloadFilename,
} from "./tool-card-state";
import { formatElapsedSeconds } from "./duration-label";
import ChangedFilesSummary from "./ChangedFilesSummary";
import SandboxMergePromptCard from "./SandboxMergePromptCard";
import TurnTelemetryPopover from "./TurnTelemetryPopover";

export function AssistantTurnFooter(props: {
  info: MessageInfo;
  parts: MessagePart[];
  turns?: Array<{ info: MessageInfo; parts: MessagePart[] }>;
  turnIndex?: number;
  /** Newest user turn index; avoids rescanning every turn per footer. */
  latestUserIndex?: number;
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

  // Commit belongs to the latest exchange only; historical cards keep their
  // revert/view actions but must not offer to commit today's tree.
  const isLatestExchange = createMemo(() => {
    if (!props.turns || typeof props.turnIndex !== "number") return true;
    if (typeof props.latestUserIndex === "number") return props.turnIndex > props.latestUserIndex;
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

  const isTruncatedTurn = createMemo(() => {
    if (interruptMode() || !props.info.time?.completed || props.info.error) return false;
    if (props.info.finish === "length") return true;
    const hasTools = props.parts.some((p) => p.type === "tool");
    const hasVisibleText = props.parts.some(
      (p) => p.type === "text" && Boolean(extractVisibleAssistantText(p.text).trim())
    );
    if (hasTools || hasVisibleText) return false;
    return props.parts.some(
      (p) => p.type === "reasoning" && hasLeakedReasoningToolMarkup(p.text)
    );
  });

  const isTurnExecuting = createMemo(() =>
    Boolean(props.isSessionBusy && !interruptMode() && isLatestTurn())
  );

  const fallbackReasoningText = () =>
    props.parts
      .filter((p): p is Extract<MessagePart, { type: "reasoning" }> => p.type === "reasoning")
      .map((p) => normalizeReasoningText(p.text))
      .filter(Boolean)
      .join("\n\n")
      .trim();

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
        props.parts.length === 0
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
        .map((p) => extractVisibleAssistantText(p.text).trim())
        .filter(Boolean);

      if (ownTexts.length > 0) {
        textChunks.push(...ownTexts);
      } else {
        for (let i = cycleTurns.length - 1; i >= 0; i--) {
          const stepTexts = cycleTurns[i].parts
            .filter((p): p is Extract<MessagePart, { type: "text" }> => p.type === "text")
            .map((p) => extractVisibleAssistantText(p.text).trim())
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
        finalText: textChunks.join("\n\n").trim() || fallbackReasoningText(),
        changedFiles: collectCycleChangedFiles(cycleTurns, props.info.sessionID),
        revertMessageID,
      };
    }

    const texts = props.parts
      .filter((p): p is Extract<MessagePart, { type: "text" }> => p.type === "text")
      .map((t) => extractVisibleAssistantText(t.text).trim())
      .filter(Boolean)
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
      finalText: texts || fallbackReasoningText(),
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
    return formatElapsedSeconds(sec);
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

  const showTruncatedControls = createMemo(
    () => !interruptMode() && isTruncatedTurn()
  );

  return (
    <Show when={cycleSummary()}>
      {(summary) => (
        <Show
          when={
            !isTurnExecuting() &&
            (summary().changedFiles.length > 0 ||
              summary().finalText.length > 0 ||
              Boolean(durationLabel()) ||
              showTruncatedControls())
          }
        >
          <div class="mt-4 space-y-2 select-none">
            <ChangedFilesSummary
              files={summary().changedFiles}
              onUndo={
                canRevertSession(props.info.sessionID)
                  ? () => void revertToHere(props.info.sessionID, summary().revertMessageID)
                  : undefined
              }
            />

            <SandboxMergePromptCard
              active={isLatestExchange() && !interruptMode()}
              sessionID={props.info.sessionID}
              messageID={props.info.id}
              changedFiles={summary().changedFiles}
            />

            <div class="flex items-center gap-2 flex-wrap text-[12px] text-[var(--text-faint)]">
              <Show when={showTruncatedControls()}>
                <span class="inline-flex items-center gap-1 font-medium text-[var(--warn-text)]">
                  <span class="font-mono text-[11px] leading-none">⏸</span>
                  <span>{t("timeline.turnTruncated")}</span>
                </span>
              </Show>

              <Show when={isLatestTurn() && showTruncatedControls() && !props.isSessionBusy}>
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

              <div class="pointer-events-none flex items-center gap-2 opacity-0 transition-opacity duration-150 motion-reduce:transition-none group-hover/turn:pointer-events-auto group-hover/turn:opacity-100 focus-within:pointer-events-auto focus-within:opacity-100 [@media(hover:none)]:pointer-events-auto [@media(hover:none)]:opacity-100">
                <Show when={summary().finalText.length > 0}>
                  <div class="flex items-center gap-1">
                    <ActionIconButton label={copied() ? t("common.copied") : t("common.copy")} onClick={() => void copyResponse()}>
                      <Show when={copied()} fallback={<Icon name="copy" size="small" />}>
                        <Icon name="check" size="small" class="text-[var(--ok-text)]" />
                      </Show>
                    </ActionIconButton>
                    <ActionIconButton label={downloaded() ? t("timeline.downloadedMarkdown") : t("timeline.downloadMarkdown")} onClick={downloadMarkdown}>
                      <Show when={downloaded()} fallback={<Icon name="download" size="small" />}>
                        <Icon name="check" size="small" class="text-[var(--ok-text)]" />
                      </Show>
                    </ActionIconButton>
                    <ActionIconButton label={linkCopied() ? t("common.copied") : t("commandPalette.actionCopySessionLink")} onClick={() => void copyLink()}>
                      <Show when={linkCopied()} fallback={<Icon name="link" size="small" />}>
                        <Icon name="check" size="small" class="text-[var(--ok-text)]" />
                      </Show>
                    </ActionIconButton>
                  </div>
                </Show>

                <Show when={durationLabel()}>
                  <TurnTelemetryPopover
                    durationLabel={durationLabel()}
                    durationMs={Math.max(0, (summary().completed || 0) - (summary().created || 0))}
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
              </div>
            </div>
          </div>
        </Show>
      )}
    </Show>
  );
}
