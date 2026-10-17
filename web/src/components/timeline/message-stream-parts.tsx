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
import { ProviderHealthBadge } from "../models/ProviderHealthBadge";
import { Icon } from "../icons";
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

  const cycleSummary = createMemo(() => {
    if (props.info.error) return null;
    const list = props.turns;
    const idx = props.turnIndex;

    if (list && typeof idx === "number") {
      const next = list[idx + 1];
      // Intermediate step in a multi-step assistant response cycle
      if (next && next.info.role === "assistant") return null;
      // Latest turn in session: wait until session is no longer busy
      if (!next && props.isSessionBusy) return null;
      if (!next && !props.info.time?.completed && props.parts.length === 0) return null;

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

    if (!props.info.time?.completed) return null;
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

  return (
    <Show when={cycleSummary()}>
      {(summary) => (
        <div class="mt-2.5 pt-2 border-t border-[var(--border-subtle)]/60 flex items-center gap-2 flex-wrap text-[11px] text-[var(--text-muted)]">
          <span class="inline-flex items-center gap-1 rounded-full border border-[var(--ok-border)] bg-[var(--ok-surface)] px-2 py-0.5 text-[11px] font-medium text-[var(--ok-text)]">
            <Icon name="circle-check" size="small" />
            <span>{t("timeline.turnCompleted")}</span>
          </span>

          <Show when={durationLabel()}>
            <span class="font-mono tabular-nums text-[var(--text-secondary)]">
              {durationLabel()}
            </span>
          </Show>

          <Show when={summary().toolCount > 0}>
            <span class="text-[var(--text-faint)]">·</span>
            <span class="font-mono tabular-nums">
              {t("timeline.stepsCompleted", { count: summary().toolCount })}
            </span>
          </Show>

          <Show when={props.info.agent}>
            <span class="text-[var(--text-faint)]">·</span>
            <span class="capitalize">{props.info.agent}</span>
          </Show>

          <Show when={props.info.modelID}>
            <span class="text-[var(--text-faint)]">·</span>
            <span class="font-mono text-[var(--text-faint)]">{props.info.modelID}</span>
          </Show>

          <Show when={props.info.providerID}>
            <ProviderHealthBadge providerID={props.info.providerID!} compact />
          </Show>

          <Show when={summary().finalText.length > 0}>
            <button
              type="button"
              onClick={() => void copyResponse()}
              title={copied() ? t("common.copied") : t("timeline.copyResponse")}
              class="ml-auto inline-flex items-center gap-1 rounded-md px-1.5 py-0.5 text-[var(--text-muted)] hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)] transition-colors cursor-pointer"
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
