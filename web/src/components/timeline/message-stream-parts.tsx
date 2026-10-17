// web/src/components/timeline/message-stream-parts.tsx
import { createMemo, createSignal, Show } from "solid-js";
import ThoughtBox from "./ThoughtBox";
import ToolCard from "./ToolCard";
import MarkdownView from "./MarkdownView";
import ContextActivityGroupComponent, {
  groupAssistantParts,
  isContextToolPart as isContextTool,
  isExplorationPart,
  isTodoToolPart as isTodoTool,
  type TurnRenderItem,
} from "./ContextActivityGroupView";
import type { MessageInfo, MessagePart } from "../../services/sse-types";
import {
  isUserAbortError,
  resumePausedSession,
  retryLastUserPrompt,
  workspace,
} from "../../store/workspace";
import { ObserverAuditBadge } from "../inspector/ObserverAuditBanner";
import { Icon, Spinner, TextShimmer } from "../icons";
import { t } from "../../i18n";
import { AssistantErrorCard, SessionRetryCard } from "./assistant-error-card";

export {
  AssistantErrorCard,
  groupAssistantParts,
  isContextTool,
  isExplorationPart,
  isTodoTool,
  SessionRetryCard,
  type TurnRenderItem,
};

export function basename(p: string): string {
  const parts = p.split("/").filter(Boolean);
  return parts.length ? parts[parts.length - 1] : "this project";
}

export function ContextActivityGroupView(props: { parts: MessagePart[] }) {
  return (
    <ContextActivityGroupComponent
      parts={props.parts}
      renderFallbackPart={(part) => <PartView part={part} />}
    />
  );
}

export function PartView(props: { part: MessagePart }) {
  const part = () => props.part;
  switch (part().type) {
    case "reasoning":
      return <ThoughtBox part={part() as Extract<MessagePart, { type: "reasoning" }>} />;
    case "tool":
      if (isTodoTool(part())) return null;
      return <ToolCard part={part() as Extract<MessagePart, { type: "tool" }>} />;
    case "patch":
      return (
        <div class="inline-flex items-center gap-2 self-start rounded-lg border border-[var(--border-subtle)] bg-[var(--bg-inset)] px-3 py-1.5 font-mono text-[11px] text-[var(--text-secondary)]">
          <Icon name="code-lines" size="small" class="text-[var(--text-muted)]" />
          <span>patch {(part() as any).hash?.slice(0, 8)}</span>
          <span class="text-[var(--text-faint)]">·</span>
          <span>{t("timeline.filesUpdatedCount", { count: ((part() as any).files || []).length })}</span>
        </div>
      );
    case "text":
      return (
        <div>
          <MarkdownView content={(part() as Extract<MessagePart, { type: "text" }>).text} />
        </div>
      );
    default:
      return null;
  }
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
  const [resuming, setResuming] = createSignal(false);

  const isLatestTurn = createMemo(() => {
    if (!props.turns || typeof props.turnIndex !== "number") return true;
    return props.turnIndex === props.turns.length - 1;
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

  const cycleSummary = createMemo(() => {
    if (props.info.error && !isUserAbortError(props.info)) return null;
    const list = props.turns;
    const idx = props.turnIndex;

    if (list && typeof idx === "number") {
      const next = list[idx + 1];
      // Intermediate step in a multi-step assistant response cycle
      if (next && next.info.role === "assistant") return null;
      // Latest turn in session: wait until session is no longer busy
      if (!next && props.isSessionBusy) return null;
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
      const textChunks: string[] = [];

      for (const step of cycleTurns) {
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

      return {
        created,
        completed,
        toolCount,
        finalText: textChunks.join("\n\n").trim(),
      };
    }

    if (!props.info.time?.completed && !interruptMode()) return null;
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
      finalText: texts,
    };
  });

  const durationLabel = createMemo(() => {
    const summary = cycleSummary();
    if (!summary?.created || !summary?.completed || summary.completed <= summary.created) {
      return "";
    }
    const sec = (summary.completed - summary.created) / 1000;
    return sec < 60 ? `${sec.toFixed(1)}s` : `${Math.floor(sec / 60)}m ${Math.round(sec % 60)}s`;
  });

  const copyResponse = async () => {
    const text = cycleSummary()?.finalText;
    if (!text) return;
    try {
      await navigator.clipboard.writeText(text);
      setCopied(true);
      setTimeout(() => setCopied(false), 1800);
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
        <div class="mt-2 flex items-center gap-2 flex-wrap text-[12px] text-[var(--text-faint)] select-none">
          <Show
            when={interruptMode()}
            fallback={
              <span class="inline-flex items-center gap-1 text-[var(--ok-text)]/85">
                <Icon name="circle-check" size="small" />
                <span>{t("timeline.turnCompleted")}</span>
              </span>
            }
          >
            {(mode) => (
              <span
                class={`inline-flex items-center gap-1 font-medium ${
                  mode() === "paused" ? "text-[var(--warn-text)]" : "text-[var(--danger-text)]"
                }`}
              >
                <span class="font-mono text-[10px] leading-none">
                  {mode() === "paused" ? "⏸" : "■"}
                </span>
                <span>
                  {mode() === "paused" ? t("timeline.turnPaused") : t("timeline.turnStopped")}
                </span>
              </span>
            )}
          </Show>

          <Show when={durationLabel()}>
            <span>·</span>
            <span class="font-mono tabular-nums text-[var(--text-muted)]">
              {durationLabel()}
            </span>
          </Show>

          <Show when={summary().toolCount > 0}>
            <span>·</span>
            <span class="font-mono tabular-nums text-[var(--text-muted)]">
              {t("timeline.stepsCompleted", { count: summary().toolCount })}
            </span>
          </Show>

          <Show when={isLatestTurn() && interruptMode() && !props.isSessionBusy}>
            <button
              type="button"
              disabled={resuming()}
              onClick={() => void onResume()}
              title={t("timeline.resumeTooltip")}
              class="inline-flex items-center gap-1 rounded-md px-2 py-0.5 text-[12px] font-medium text-[var(--accent-primary)] hover:bg-[var(--accent-surface)] disabled:opacity-50 transition-colors cursor-pointer"
            >
              <span class="text-[10px] leading-none">▶</span>
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

          <Show when={summary().finalText.length > 0}>
            <button
              type="button"
              onClick={() => void copyResponse()}
              title={copied() ? t("common.copied") : t("timeline.copyResponse")}
              class="ml-auto inline-flex items-center gap-1 rounded-md px-1.5 py-0.5 text-[12px] text-[var(--text-muted)] hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)] transition-colors cursor-pointer"
            >
              <Show when={copied()} fallback={<Icon name="copy" size="small" />}>
                <Icon name="check" size="small" class="text-[var(--ok-text)]" />
              </Show>
              <span>{copied() ? t("common.copied") : t("common.copy")}</span>
            </button>
          </Show>
        </div>
      )}
    </Show>
  );
}

export function LiveExecutionControlBar(props: {
  sessionID: string | null;
  isSessionBusy: boolean;
  showThinking: boolean;
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
        <div class="mt-4 flex items-center gap-2 px-1 text-[13px] text-[var(--accent-primary)]">
          <Spinner class="w-4 h-4 shrink-0" />
          <TextShimmer text={t("timeline.thinking")} active={true} class="font-medium" />
        </div>
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
            <span class="font-mono text-[10px] leading-none">
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
            <span class="text-[10px] leading-none">▶</span>
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
