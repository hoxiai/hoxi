// web/src/components/timeline/MessageStream.tsx
import {
  createEffect,
  createMemo,
  createSignal,
  For,
  Index,
  onCleanup,
  onMount,
  Show,
} from "solid-js";
import type { MessagePart, SessionStatus } from "../../services/sse-types";
import {
  forkFromMessage,
  revertToHere,
  submitSessionPrompt,
  workspace,
} from "../../store/workspace";
import { Icon, Spinner, TextShimmer } from "../icons";
import {
  AssistantErrorCard,
  AssistantTurnFooter,
  basename,
  ContextActivityGroupView,
  formatMessageTime,
  groupAssistantParts,
  isTodoTool,
  PartView,
  SessionRetryCard,
} from "./message-stream-parts";
import TaskTimelineBar from "./TaskTimelineBar";
import TranscriptSearchBar from "./TranscriptSearchBar";
import { t } from "../../i18n";

export default function MessageStream() {
  let scroller: HTMLDivElement | undefined;
  let stickToBottom = true;
  const [launchingPrompt, setLaunchingPrompt] = createSignal(false);
  const [copiedUserMsgId, setCopiedUserMsgId] = createSignal<string | null>(null);
  const [searchOpen, setSearchOpen] = createSignal(false);
  const [activeMatchTurnID, setActiveMatchTurnID] = createSignal<string | null>(null);

  const turns = createMemo(() => {
    const id = workspace.activeSessionID;
    return id ? workspace.messagesBySession[id] ?? [] : [];
  });

  const activeStatus = createMemo<SessionStatus>(() => {
    const id = workspace.activeSessionID;
    return (id && workspace.statusBySession[id]) || { type: "idle" };
  });

  const isSessionBusy = createMemo(() => {
    const st = activeStatus();
    return st.type === "busy" || st.type === "retry";
  });

  const pendingForSession = createMemo(() => {
    const id = workspace.activeSessionID;
    return id ? workspace.pendingPermissions.filter((p) => p.sessionID === id) : [];
  });

  const projectName = createMemo(() => basename(workspace.rootDir));

  const welcomeTitleParts = createMemo(() => {
    const full = t("timeline.welcomeQuestion", { project: "\u0000" });
    const [before = "", after = ""] = full.split("\u0000");
    return { before, after };
  });

  const quickSuggestions = createMemo(() => [
    t("timeline.welcomePrompt1"),
    t("timeline.welcomePrompt2"),
    t("timeline.welcomePrompt3"),
  ]);

  // Whether we should show Kilo's bottom `Thinking...` shimmer indicator
  const showThinkingPlaceholder = createMemo(() => {
    if (!isSessionBusy()) return false;
    const list = turns();
    if (list.length === 0) return true;
    const last = list[list.length - 1];
    if (last.info.role === "user") return true;
    if (last.info.error) return false;
    // Assistant turn exists but has no visible parts yet
    const visible = last.parts.filter((p) => !isTodoTool(p));
    return visible.length === 0;
  });

  // Reactivity key that changes as tokens stream in, driving auto-scroll.
  const streamKey = createMemo(() => {
    const list = turns();
    const last = list[list.length - 1];
    let n = list.length * 1_000_000;
    if (last) {
      n += last.parts.length * 1000;
      if (last.info.error) n += 500;
      for (const part of last.parts) {
        if (part.type === "text" || part.type === "reasoning") n += part.text.length;
      }
    }
    return n;
  });

  createEffect(() => {
    streamKey();
    if (!stickToBottom || !scroller) return;
    queueMicrotask(() => {
      if (scroller && stickToBottom) scroller.scrollTop = scroller.scrollHeight;
    });
  });

  const onScroll = () => {
    if (!scroller) return;
    const distance = scroller.scrollHeight - scroller.scrollTop - scroller.clientHeight;
    stickToBottom = distance < 64;
  };

  const onRevertToMessage = async (messageID: string) => {
    const id = workspace.activeSessionID;
    if (!id) return;
    if (!confirm(t("timeline.revertConfirm"))) return;
    try {
      await revertToHere(id, messageID);
    } catch (err) {
      console.error("revert failed:", err);
    }
  };

  const onForkFromMessage = async (messageID: string) => {
    const id = workspace.activeSessionID;
    if (!id) return;
    try {
      await forkFromMessage(id, messageID);
    } catch (err) {
      console.error("fork failed:", err);
    }
  };

  const onCopyUserMessage = async (messageID: string, parts: MessagePart[]) => {
    const text = parts
      .filter((p): p is Extract<MessagePart, { type: "text" }> => p.type === "text")
      .map((p) => p.text)
      .join("\n")
      .trim();
    if (!text) return;
    try {
      await navigator.clipboard.writeText(text);
      setCopiedUserMsgId(messageID);
      setTimeout(() => {
        if (copiedUserMsgId() === messageID) setCopiedUserMsgId(null);
      }, 1600);
    } catch {
      /* ignore */
    }
  };

  const onRunSuggestion = async (prompt: string) => {
    if (launchingPrompt()) return;
    setLaunchingPrompt(true);
    try {
      await submitSessionPrompt(prompt, workspace.activeSessionID);
    } catch (err) {
      console.error("suggestion prompt failed:", err);
    } finally {
      setLaunchingPrompt(false);
    }
  };

  const scrollToTurn = (messageID: string) => {
    if (!scroller) return;
    const el = scroller.querySelector(`[data-turn-id="${CSS.escape(messageID)}"]`);
    if (el instanceof HTMLElement) {
      stickToBottom = false;
      el.scrollIntoView({ behavior: "smooth", block: "center" });
    }
  };

  onMount(() => {
    const onWindowKeyDown = (ev: KeyboardEvent) => {
      if ((ev.metaKey || ev.ctrlKey) && ev.key.toLowerCase() === "f") {
        if (turns().length > 0) {
          ev.preventDefault();
          setSearchOpen(true);
        }
      }
    };
    window.addEventListener("keydown", onWindowKeyDown);
    onCleanup(() => window.removeEventListener("keydown", onWindowKeyDown));
  });

  const isEmpty = () => turns().length === 0 && pendingForSession().length === 0;

  return (
    <div
      ref={scroller}
      onScroll={onScroll}
      class="relative flex-1 min-h-0 overflow-y-auto overflow-x-hidden scroll-smooth"
    >
      {/* Kilo TaskTimeline + MessageNav Turn Jump + TranscriptSearch */}
      <TaskTimelineBar
        turns={turns()}
        activeMatchTurnID={activeMatchTurnID()}
        onJumpToTurn={(msgID) => {
          setActiveMatchTurnID(msgID);
          scrollToTurn(msgID);
        }}
        onToggleSearch={() => setSearchOpen((v) => !v)}
        searchOpen={searchOpen()}
      />

      <TranscriptSearchBar
        open={searchOpen()}
        turns={turns()}
        onClose={() => {
          setSearchOpen(false);
          setActiveMatchTurnID(null);
        }}
        onActiveTurnChange={(msgID) => {
          setActiveMatchTurnID(msgID);
          if (msgID) scrollToTurn(msgID);
        }}
      />

      {/* Top Soft Scroll Fade (Kilo style) */}
      <div
        aria-hidden="true"
        class="pointer-events-none sticky top-0 z-10 -mb-6 h-6 w-full shrink-0 bg-gradient-to-b from-[var(--bg-panel)] to-transparent"
      />

      <div class="mx-auto flex w-full max-w-[48rem] min-h-full flex-col px-6 pt-8 pb-8">
        <Show
          when={!isEmpty()}
          fallback={
            /* Kilo-style Spacious Welcome Hero */
            <div class="my-auto flex flex-col items-center justify-center py-12 text-center">
              <div class="mb-5 flex h-14 w-14 items-center justify-center rounded-2xl border border-[var(--accent-border)] bg-[var(--accent-surface)] text-[var(--accent-primary)] shadow-xs">
                <svg viewBox="0 0 64 64" fill="none" class="h-8 w-8">
                  <path d="M32 8L52 21H44L32 13L20 21H12L32 8Z" fill="currentColor" />
                  <rect x="20" y="25" width="6.5" height="12" rx="3.25" fill="currentColor" />
                  <rect x="37.5" y="25" width="6.5" height="12" rx="3.25" fill="currentColor" />
                  <path
                    d="M19 42.5C23 50 41 50 45 42.5"
                    stroke="currentColor"
                    stroke-width="5.5"
                    stroke-linecap="round"
                  />
                </svg>
              </div>

              <h1 class="text-[22px] font-medium tracking-tight text-[var(--text-primary)]">
                {welcomeTitleParts().before}
                <span class="border-b border-dotted border-[var(--text-muted)] font-semibold">
                  {projectName()}
                </span>
                {welcomeTitleParts().after}
              </h1>
              <p class="mt-2 max-w-md text-[13px] text-[var(--text-muted)]">
                {t("timeline.welcomeSubtitle")}
              </p>

              <div class="mt-7 flex flex-wrap items-center justify-center gap-2 max-w-[40rem]">
                <For each={quickSuggestions()}>
                  {(suggestion) => (
                    <button
                      type="button"
                      disabled={launchingPrompt()}
                      onClick={() => void onRunSuggestion(suggestion)}
                      class="inline-flex items-center gap-1.5 rounded-full border border-[var(--border-subtle)] bg-[var(--bg-elevated)] hover:bg-[var(--bg-hover)] hover:border-[var(--border-strong)] px-3.5 py-1.5 text-[12.5px] text-[var(--text-secondary)] hover:text-[var(--text-primary)] transition-all cursor-pointer disabled:opacity-50"
                    >
                      <span>{suggestion}</span>
                      <Icon name="arrow-up" size="small" class="rotate-45 text-[var(--text-faint)]" />
                    </button>
                  )}
                </For>
              </div>
            </div>
          }
        >
          <div class="flex flex-col gap-y-6">
            <For each={turns()}>
              {(turn) => (
                <Show
                  when={turn.info.role === "user"}
                  fallback={
                    /* Kilo-style Open Assistant Turn with Grouped Exploration Steps */
                    <div
                      data-turn-id={turn.info.id}
                      class={`px-1 rounded-xl transition-all ${
                        activeMatchTurnID() === turn.info.id
                          ? "ring-2 ring-[var(--accent-primary)] bg-[var(--accent-surface)]/20 p-2"
                          : ""
                      }`}
                    >
                      <For each={groupAssistantParts(turn.parts)}>
                        {(item) =>
                          item.kind === "activity-group" ? (
                            <ContextActivityGroupView parts={item.parts} />
                          ) : (
                            <PartView part={item.part} />
                          )
                        }
                      </For>
                      <AssistantErrorCard info={turn.info} />
                      <AssistantTurnFooter info={turn.info} parts={turn.parts} />
                    </div>
                  }
                >
                  {/* Kilo-style Right-Aligned User Message Bubble */}
                  <div
                    data-turn-id={turn.info.id}
                    class={`group/turn flex flex-col items-end rounded-xl transition-all ${
                      activeMatchTurnID() === turn.info.id
                        ? "ring-2 ring-[var(--accent-primary)] p-1.5"
                        : ""
                    }`}
                  >
                    <div class="max-w-[85%] rounded-2xl bg-[var(--bg-elevated)] border border-[var(--border-subtle)] px-4 py-2.5 shadow-2xs space-y-2">
                      <Index each={turn.parts}>
                        {(part) => (
                          <>
                            <Show when={part().type === "file"}>
                              <div class="overflow-hidden rounded-lg border border-[var(--border-subtle)] max-w-[260px]">
                                <img
                                  src={(part() as Extract<MessagePart, { type: "file" }>).url}
                                  alt={
                                    (part() as Extract<MessagePart, { type: "file" }>).filename ||
                                    "attachment"
                                  }
                                  class="max-h-48 w-auto object-contain block"
                                />
                              </div>
                            </Show>
                            <Show when={part().type === "text"}>
                              <div class="whitespace-pre-wrap break-words text-[14px] leading-relaxed text-[var(--text-primary)] selectable-text">
                                {(part() as Extract<MessagePart, { type: "text" }>).text}
                              </div>
                            </Show>
                          </>
                        )}
                      </Index>
                    </div>

                    {/* Kilo-style User Message Hover Actions & Metadata */}
                    <div class="mt-1 flex items-center justify-end gap-2 px-1 text-[11px] text-[var(--text-faint)] opacity-0 group-hover/turn:opacity-100 transition-opacity">
                      <Show when={formatMessageTime(turn.info.time?.created)}>
                        <span class="font-mono">{formatMessageTime(turn.info.time?.created)}</span>
                      </Show>

                      <button
                        type="button"
                        class="inline-flex items-center gap-1 rounded-md px-1.5 py-0.5 text-[11px] text-[var(--text-muted)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-hover)] transition-colors cursor-pointer"
                        onClick={() => void onCopyUserMessage(turn.info.id, turn.parts)}
                        title={t("timeline.copyPromptTooltip")}
                      >
                        <Icon
                          name={copiedUserMsgId() === turn.info.id ? "check" : "copy"}
                          size="small"
                        />
                        <span>{copiedUserMsgId() === turn.info.id ? t("common.copied") : t("common.copy")}</span>
                      </button>

                      <button
                        type="button"
                        class="inline-flex items-center gap-1 rounded-md px-1.5 py-0.5 text-[11px] text-[var(--text-muted)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-hover)] transition-colors cursor-pointer"
                        onClick={() => void onForkFromMessage(turn.info.id)}
                        title={t("timeline.forkTooltip")}
                      >
                        <Icon name="fork" size="small" />
                        <span>{t("timeline.forkBtn")}</span>
                      </button>

                      <button
                        type="button"
                        class="inline-flex items-center gap-1 rounded-md px-1.5 py-0.5 text-[11px] text-[var(--text-muted)] hover:text-[var(--warn-text)] hover:bg-[var(--warn-surface)] transition-colors cursor-pointer"
                        onClick={() => void onRevertToMessage(turn.info.id)}
                        title={t("timeline.revertTooltip")}
                      >
                        <Icon name="arrow-undo-down" size="small" />
                        <span>{t("timeline.revertBtn")}</span>
                      </button>
                    </div>
                  </div>
                </Show>
              )}
            </For>

            {/* Kilo Thinking Placeholder before first visible part arrives */}
            <Show when={showThinkingPlaceholder()}>
              <div class="px-1 py-1 flex items-center gap-2.5 text-[13px] text-[var(--accent-primary)]">
                <Spinner class="w-4 h-4" />
                <TextShimmer text="Thinking..." active={true} class="font-medium" />
              </div>
            </Show>

            {/* Kilo SessionRetry Countdown Card */}
            <Show when={activeStatus().type === "retry"}>
              <SessionRetryCard
                status={activeStatus() as Extract<SessionStatus, { type: "retry" }>}
              />
            </Show>
          </div>
        </Show>
      </div>
    </div>
  );
}
