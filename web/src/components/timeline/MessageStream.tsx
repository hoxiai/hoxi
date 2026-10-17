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
  appSettings,
  forkFromMessage,
  revertToHere,
  stopSessionTask,
  submitSessionPrompt,
  workspace,
} from "../../store/workspace";
import { Icon } from "../icons";
import {
  basename,
  formatMessageTime,
  isTodoTool,
  LiveExecutionControlBar,
  SessionRetryCard,
} from "./message-stream-parts";
import {
  AssistantTurnItem,
  createTurnWindow,
  EarlierTurnsBanner,
  filterVisibleTurnsByRevert,
  turnContentVisibilityClass,
} from "./turn-window";
import TaskTimelineBar from "./TaskTimelineBar";
import TranscriptSearchBar from "./TranscriptSearchBar";
import QueuedPromptsList from "./QueuedPromptsList";
import { t } from "../../i18n";

export default function MessageStream() {
  let scroller: HTMLDivElement | undefined;
  let stickToBottom = true;
  const [launchingPrompt, setLaunchingPrompt] = createSignal(false);
  const [copiedUserMsgId, setCopiedUserMsgId] = createSignal<string | null>(null);
  const [searchOpen, setSearchOpen] = createSignal(false);
  const [activeMatchTurnID, setActiveMatchTurnID] = createSignal<string | null>(null);

  const activeSession = createMemo(() => {
    const id = workspace.activeSessionID;
    return id ? workspace.sessions.find((s) => s.id === id) : undefined;
  });

  const turns = createMemo(() => {
    const id = workspace.activeSessionID;
    const raw = id ? workspace.messagesBySession[id] ?? [] : [];
    return filterVisibleTurnsByRevert(raw, activeSession()?.revert);
  });

  const turnWindow = createTurnWindow(() => workspace.activeSessionID, turns);

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
    if (activeStatus().type !== "busy") return false;
    const list = turns();
    if (list.length === 0) return true;

    // Check if any assistant turn currently has a running/pending tool or open reasoning part
    for (let i = list.length - 1; i >= 0; i--) {
      const turn = list[i];
      if (turn.info.role !== "assistant" || turn.info.time?.completed) continue;
      const hasRunningWork = turn.parts.some(
        (p) =>
          !isTodoTool(p) &&
          ((p.type === "tool" &&
            (p.state.status === "pending" || p.state.status === "running")) ||
            (p.type === "reasoning" && !p.time?.end))
      );
      if (hasRunningWork) return false;
    }

    const last = list[list.length - 1];
    if (last.info.role === "user") return true;
    if (last.info.error) return false;
    if (last.info.time?.completed) return true;
    const visible = last.parts.filter((p) => !isTodoTool(p));
    if (visible.length === 0) return true;
    const hasActivePart = visible.some((p) => {
      if (p.type === "reasoning") return !p.time?.end;
      if (p.type === "tool") {
        return p.state.status === "pending" || p.state.status === "running";
      }
      if (p.type === "text") return p.text.trim().length > 0;
      return false;
    });
    return !hasActivePart;
  });

  // Reactivity key that changes as tokens stream in or prompts are queued, driving auto-scroll.
  const streamKey = createMemo(() => {
    const list = turns();
    const id = workspace.activeSessionID;
    const queuedLen = id ? (workspace.queuedPromptsBySession[id]?.length ?? 0) : 0;
    const last = list[list.length - 1];
    let n = list.length * 1_000_000 + queuedLen * 100_000;
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
    if (!appSettings.autoScrollStream || !stickToBottom || !scroller) return;
    queueMicrotask(() => {
      if (scroller && stickToBottom) scroller.scrollTop = scroller.scrollHeight;
    });
  });

  const onScroll = () => {
    if (!scroller) return;
    const distance = scroller.scrollHeight - scroller.scrollTop - scroller.clientHeight;
    stickToBottom = distance < 64;
    if (!stickToBottom) {
      turnWindow.handleTopScroll(scroller);
    }
  };

  const onRevertToMessage = async (messageID: string) => {
    const id = workspace.activeSessionID;
    if (!id) return;
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
    turnWindow.ensureTurnVisible(messageID);
    queueMicrotask(() => {
      if (!scroller) return;
      const el = scroller.querySelector(`[data-turn-id="${CSS.escape(messageID)}"]`);
      if (el instanceof HTMLElement) {
        stickToBottom = false;
        el.scrollIntoView({ behavior: "smooth", block: "center" });
      }
    });
  };

  onMount(() => {
    const onWindowKeyDown = (ev: KeyboardEvent) => {
      if ((ev.metaKey || ev.ctrlKey) && ev.key.toLowerCase() === "f") {
        if (turns().length > 0) {
          ev.preventDefault();
          setSearchOpen(true);
        }
        return;
      }
      if (
        isSessionBusy() &&
        !ev.defaultPrevented &&
        !searchOpen() &&
        !workspace.providerModalOpen &&
        !workspace.arenaModalOpen &&
        (ev.key === "Escape" || ((ev.metaKey || ev.ctrlKey) && ev.key === "."))
      ) {
        ev.preventDefault();
        void stopSessionTask(workspace.activeSessionID);
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
      class="relative flex-1 min-h-0 overflow-y-auto overflow-x-hidden"
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

      <div class="mx-auto flex w-full max-w-[var(--thread-max-width)] min-h-full flex-col px-6 pt-8 pb-8">
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
              <p class="mt-2 max-w-lg text-[13.5px] text-[var(--text-muted)]">
                {t("timeline.welcomeSubtitle")}
              </p>

              <div class="mt-7 flex flex-wrap items-center justify-center gap-2 max-w-[50rem]">
                <For each={quickSuggestions()}>
                  {(suggestion) => (
                    <button
                      type="button"
                      disabled={launchingPrompt()}
                      onClick={() => void onRunSuggestion(suggestion)}
                      class="inline-flex items-center gap-1.5 rounded-full border border-[var(--border-subtle)] bg-[var(--bg-elevated)] hover:bg-[var(--bg-hover)] hover:border-[var(--border-strong)] px-3.5 py-1.5 text-[13px] text-[var(--text-secondary)] hover:text-[var(--text-primary)] transition-all cursor-pointer disabled:opacity-50"
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
          <div class="flex flex-col">
            <EarlierTurnsBanner
              hiddenCount={turnWindow.hiddenCount()}
              totalCount={turns().length}
              onLoadBatch={() => turnWindow.loadEarlierBatch(scroller)}
              onLoadAll={() => turnWindow.loadAllEarlier(scroller)}
            />

            <For each={turnWindow.visibleTurns()}>
              {(turn, idx) => {
                const globalIndex = () => turnWindow.startIndex() + idx();
                return (
                  <Show
                    when={turn.info.role === "user"}
                    fallback={
                      <AssistantTurnItem
                        turn={turn}
                        globalIndex={globalIndex()}
                        allTurns={turns()}
                        isSessionBusy={isSessionBusy()}
                        isHighlighted={activeMatchTurnID() === turn.info.id}
                      />
                    }
                  >
                    {/* Kilo-style Right-Aligned User Message Bubble */}
                    <div
                      data-turn-id={turn.info.id}
                      class={`group/turn flex flex-col items-end rounded-xl transition-colors ${turnContentVisibilityClass(
                        globalIndex(),
                        turns().length
                      )} ${
                        idx() === 0 ? "mt-0" : "mt-7"
                      } ${
                        activeMatchTurnID() === turn.info.id
                          ? "ring-2 ring-[var(--accent-primary)] p-1.5"
                          : ""
                      }`}
                    >
                      <div class="max-w-[85%] rounded-2xl bg-[var(--bg-elevated)] border border-[var(--border-subtle)] px-4 py-3 shadow-2xs space-y-2">
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
                                <div
                                  class="whitespace-pre-wrap break-words leading-[1.65] text-[var(--text-primary)] selectable-text"
                                  style={{ "font-size": "var(--app-chat-text-size)" }}
                                >
                                  {(part() as Extract<MessagePart, { type: "text" }>).text}
                                </div>
                              </Show>
                            </>
                          )}
                        </Index>
                      </div>

                      {/* Kilo-style User Message Hover Actions & Metadata */}
                      <div class="mt-1 flex items-center justify-end gap-2 px-1 text-[12px] text-[var(--text-faint)] opacity-0 group-hover/turn:opacity-100 transition-opacity">
                        <Show when={formatMessageTime(turn.info.time?.created)}>
                          <span class="font-mono text-[11.5px]">{formatMessageTime(turn.info.time?.created)}</span>
                        </Show>

                        <button
                          type="button"
                          class="inline-flex items-center gap-1 rounded-md px-1.5 py-0.5 text-[12px] text-[var(--text-muted)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-hover)] transition-colors cursor-pointer"
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
                          class="inline-flex items-center gap-1 rounded-md px-1.5 py-0.5 text-[12px] text-[var(--text-muted)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-hover)] transition-colors cursor-pointer"
                          onClick={() => void onForkFromMessage(turn.info.id)}
                          title={t("timeline.forkTooltip")}
                        >
                          <Icon name="fork" size="small" />
                          <span>{t("timeline.forkBtn")}</span>
                        </button>

                        <button
                          type="button"
                          class="inline-flex items-center gap-1 rounded-md px-1.5 py-0.5 text-[12px] text-[var(--text-muted)] hover:text-[var(--warn-text)] hover:bg-[var(--warn-surface)] transition-colors cursor-pointer"
                          onClick={() => void onRevertToMessage(turn.info.id)}
                          title={t("timeline.revertTooltip")}
                        >
                          <Icon name="arrow-undo-down" size="small" />
                          <span>{t("timeline.revertBtn")}</span>
                        </button>
                      </div>
                    </div>
                  </Show>
                );
              }}
            </For>

            {/* Live Execution Control Bar (Thinking / Executing + Pause / Stop / Resume) */}
            <LiveExecutionControlBar
              sessionID={workspace.activeSessionID}
              isSessionBusy={isSessionBusy()}
              showThinking={showThinkingPlaceholder()}
              lastTurnRole={turns()[turns().length - 1]?.info.role}
            />

            {/* Queued Follow-Up Prompts (Send Now | Edit | Delete) */}
            <QueuedPromptsList sessionID={workspace.activeSessionID} />

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

