// web/src/components/timeline/message-stream-parts.tsx
import { createEffect, createMemo, createSignal, onCleanup, Show } from "solid-js";
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
import type { MessageInfo, MessagePart, SessionStatus } from "../../services/sse-types";
import { Icon } from "../icons";

export { groupAssistantParts, isContextTool, isExplorationPart, isTodoTool, type TurnRenderItem };

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

/**
 * Ported from @opencode-ai/session-ui SessionRetry
 * Live 1-second countdown card when a session status is `retry` (e.g. rate limit / transient network backoff).
 */
export function SessionRetryCard(props: { status: Extract<SessionStatus, { type: "retry" }> }) {
  const [seconds, setSeconds] = createSignal(
    Math.max(0, Math.round((props.status.next - Date.now()) / 1000))
  );

  createEffect(() => {
    const update = () => {
      setSeconds(Math.max(0, Math.round((props.status.next - Date.now()) / 1000)));
    };
    update();
    const timer = setInterval(update, 1000);
    onCleanup(() => clearInterval(timer));
  });

  return (
    <div class="my-2 flex items-start gap-2.5 rounded-xl border border-[var(--warn-border)] bg-[var(--warn-surface)] px-3.5 py-2.5 text-[12.5px] text-[var(--warn-text)]">
      <Icon name="warning" size="small" class="mt-0.5 shrink-0" />
      <div class="min-w-0 flex-1 space-y-0.5">
        <div class="font-medium">
          Retrying in {seconds()}s{" "}
          <span class="font-mono text-[11px] opacity-80">(Attempt #{props.status.attempt})</span>
        </div>
        <Show when={props.status.message}>
          <div class="text-[11.5px] opacity-90 break-words font-mono">{props.status.message}</div>
        </Show>
      </div>
    </div>
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
        <div class="my-2 inline-flex items-center gap-2 rounded-lg border border-[var(--border-subtle)] bg-[var(--bg-inset)] px-3 py-1.5 font-mono text-[11px] text-[var(--text-secondary)]">
          <Icon name="code-lines" size="small" class="text-[var(--text-muted)]" />
          <span>patch {(part() as any).hash?.slice(0, 8)}</span>
          <span class="text-[var(--text-faint)]">·</span>
          <span>{((part() as any).files || []).length} files updated</span>
        </div>
      );
    case "text":
      return (
        <div class="my-2">
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

export function AssistantTurnFooter(props: { info: MessageInfo; parts: MessagePart[] }) {
  const [copied, setCopied] = createSignal(false);

  const finalText = createMemo(() => {
    const texts = props.parts.filter(
      (p): p is Extract<MessagePart, { type: "text" }> => p.type === "text"
    );
    return texts.map((t) => t.text).join("\n\n").trim();
  });

  const durationLabel = createMemo(() => {
    const { created, completed } = props.info.time || {};
    if (!created || !completed || completed <= created) return "";
    const sec = (completed - created) / 1000;
    return sec < 60 ? `${sec.toFixed(1)}s` : `${Math.floor(sec / 60)}m ${Math.round(sec % 60)}s`;
  });

  const copyResponse = async () => {
    const text = finalText();
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
    <Show when={finalText().length > 0 && props.info.time?.completed}>
      <div class="mt-1.5 flex items-center gap-2 text-[11px] text-[var(--text-faint)] opacity-80 hover:opacity-100 transition-opacity">
        <button
          type="button"
          onClick={() => void copyResponse()}
          title={copied() ? "Copied" : "Copy response"}
          class="inline-flex items-center gap-1 rounded-md px-1.5 py-0.5 text-[var(--text-muted)] hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)] transition-colors cursor-pointer"
        >
          <Show when={copied()} fallback={<Icon name="copy" size="small" />}>
            <Icon name="check" size="small" class="text-[var(--ok-text)]" />
          </Show>
          <span>{copied() ? "Copied" : "Copy"}</span>
        </button>

        <Show when={props.info.agent}>
          <span class="capitalize">{props.info.agent}</span>
        </Show>

        <Show when={props.info.modelID}>
          <span>·</span>
          <span class="font-mono">{props.info.modelID}</span>
        </Show>

        <Show when={durationLabel()}>
          <span>·</span>
          <span class="font-mono tabular-nums">{durationLabel()}</span>
        </Show>
      </div>
    </Show>
  );
}
