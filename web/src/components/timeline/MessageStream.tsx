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
import type { MessagePart, SessionStatus, ToolPart } from "../../services/sse-types";
import { isRunnerSessionID } from "../../services/api";
import {
  appSettings,
  forkFromMessage,
  revertToHere,
  stopSessionTask,
  workspace,
} from "../../store/workspace";
import { Icon } from "../icons";
import {
  formatMessageTime,
  isTodoTool,
  LiveExecutionControlBar,
  resolveActiveCycleStepLabel,
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
import { SubagentSessionBanner } from "./AgentStackPill";
import QueuedPromptsList from "./QueuedPromptsList";
import { SessionSkeleton } from "./SessionSkeleton";
import { t } from "../../i18n";
import { createTimelineScroll } from "./timeline-scroll";
import { createTranscriptControls } from "./transcript-controls";
import { createReadingPositionTracker } from "./reading-position";
import { createSessionScroll } from "./session-scroll";

export default function MessageStream(props: { hidden?: boolean }) {
  let scroller: HTMLDivElement | undefined;
  let content: HTMLDivElement | undefined;
  const scroll = createTimelineScroll(() => scroller, () => appSettings.autoScrollStream);
  const transcriptControls = createTranscriptControls(() => workspace.activeSessionID);
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
  const readingPosition = createReadingPositionTracker({
    scroller: () => scroller,
    content: () => content,
    sessionID: () => workspace.activeSessionID,
    assistantTurnIDs: () =>
      turns()
        .filter((turn) => turn.info.role === "assistant")
        .map((turn) => turn.info.id),
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

  const isLoadingSession = createMemo(() => {
    if (workspace.bootstrappingSessions) return true;
    const id = workspace.activeSessionID;
    if (!id) return false;
    return Boolean(
      workspace.loadingMessagesBySession[id] &&
        workspace.messagesBySession[id] === undefined
    );
  });

  // Keep the bottom live execution status bar continuously visible ("不断档") while busy
  const showThinkingPlaceholder = createMemo(() => {
    if (activeStatus().type !== "busy") return false;
    const list = turns();
    if (list.length === 0) return false;

    let lastUserIdx = -1;
    for (let i = list.length - 1; i >= 0; i--) {
      if (list[i].info.role === "user") {
        lastUserIdx = i;
        break;
      }
    }
    if (lastUserIdx < 0) return false;

    const last = list[list.length - 1];
    if (last.info.role === "assistant" && last.info.error) return false;

    return true;
  });

  const executionCycleMeta = createMemo(() => {
    const list = turns();
    let lastUserIdx = -1;
    for (let i = list.length - 1; i >= 0; i--) {
      if (list[i].info.role === "user") {
        lastUserIdx = i;
        break;
      }
    }
    if (lastUserIdx < 0) {
      return {
        count: 0,
        steps: [] as ToolPart[],
        startedAt: undefined as number | undefined,
        activeStepLabel: undefined as string | undefined,
      };
    }
    const startedAt =
      list[lastUserIdx].info.time?.created || list[lastUserIdx + 1]?.info.time?.created;
    const cycleTurns = list.slice(lastUserIdx + 1);
    const steps: ToolPart[] = [];
    for (const turn of cycleTurns) {
      for (const part of turn.parts) {
        if (part.type === "tool" && !isTodoTool(part)) {
          steps.push(part);
        }
      }
    }
    return {
      count: steps.filter((part) => part.state.status === "completed").length,
      steps,
      startedAt,
      activeStepLabel: resolveActiveCycleStepLabel(cycleTurns),
    };
  });

  const sessionScroll = createSessionScroll({
    sessionID: () => workspace.activeSessionID,
    turnIDs: () => turns().map((turn) => turn.info.id),
    loading: () => isLoadingSession(),
    scroller: () => scroller,
    content: () => content,
    scroll,
    ensureTurnVisible: turnWindow.ensureTurnVisible,
  });
  let previousSessionID: string | null = null;
  let previousPromptID: string | undefined;

  createEffect(() => {
    const sessionID = workspace.activeSessionID;
    const promptID = turns().findLast((turn) => turn.info.role === "user")?.info.id;
    if (sessionID === previousSessionID && promptID !== previousPromptID && promptID?.startsWith("optimistic_")) {
      sessionScroll.interrupt();
      scroll.resume();
    }
    previousSessionID = sessionID;
    previousPromptID = promptID;
  });

  createEffect(() => {
    if (appSettings.autoScrollStream) scroll.schedule();
  });

  const onScroll = () => {
    if (!scroller) return;
    readingPosition.schedule();
    transcriptControls.onScroll(scroller.scrollTop);
    if (scroll.onScroll()) {
      turnWindow.handleTopScroll(scroller);
    }
    sessionScroll.capture();
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

  const scrollToTurn = (messageID: string) => {
    sessionScroll.interrupt();
    scroll.pause();
    turnWindow.ensureTurnVisible(messageID);
    queueMicrotask(() => {
      if (!scroller) return;
      const el = scroller.querySelector(`[data-turn-id="${CSS.escape(messageID)}"]`);
      if (el instanceof HTMLElement) {
        el.scrollIntoView({ behavior: "smooth", block: "center" });
      }
    });
  };

  onMount(() => {
    if (content) onCleanup(scroll.observe(content));
    onCleanup(readingPosition.observe());
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
    <div class="relative flex flex-1 min-h-0 flex-col" style={{ display: props.hidden ? "none" : undefined }}>
      <SubagentSessionBanner />
      <div class="relative flex-1 min-h-0" style={{ "container-type": "inline-size" }}>
        <TaskTimelineBar
          sessionID={workspace.activeSessionID}
           turns={turns()}
           readingTurnID={readingPosition.readingTurnID()}
          activeMatchTurnID={activeMatchTurnID()}
          onJumpToTurn={(msgID) => {
            setActiveMatchTurnID(msgID);
            scrollToTurn(msgID);
          }}
          onToggleSearch={() => setSearchOpen((v) => !v)}
          searchOpen={searchOpen()}
          controlsVisible={transcriptControls.visible()}
          onCloseSearch={() => {
            setSearchOpen(false);
            setActiveMatchTurnID(null);
          }}
          onSearchMatch={(msgID) => {
            setActiveMatchTurnID(msgID);
            if (msgID) scrollToTurn(msgID);
          }}
        />
    <div
      ref={scroller}
      onScroll={onScroll}
      onWheel={() => { sessionScroll.interrupt(); transcriptControls.markIntent(); }}
      onTouchMove={() => { sessionScroll.interrupt(); transcriptControls.markIntent(); }}
      onPointerDown={() => { sessionScroll.interrupt(); transcriptControls.startPointer(); }}
      onPointerUp={() => transcriptControls.endPointer()}
      onPointerCancel={() => transcriptControls.endPointer()}
      onKeyDown={(event) => {
        if (["ArrowUp", "ArrowDown", "PageUp", "PageDown", "Home", "End", " "].includes(event.key)) {
          transcriptControls.markIntent();
          sessionScroll.interrupt();
        }
      }}
      class="relative h-full min-h-0 overflow-y-auto overflow-x-hidden"
    >

      {/* Top Soft Scroll Fade (Kilo style) */}
      <div
        aria-hidden="true"
        class="pointer-events-none sticky top-0 z-10 -mb-6 h-6 w-full shrink-0 bg-gradient-to-b from-[var(--bg-panel)] to-transparent"
      />

      <div ref={content} class="mx-auto flex w-full max-w-[calc(var(--thread-max-width)+3rem)] min-h-full flex-col px-6 pt-8 pb-8">
        <Show
          when={!isEmpty()}
          fallback={
            <Show when={isLoadingSession()}>
              <SessionSkeleton />
            </Show>
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
                          disabled={isRunnerSessionID(workspace.activeSessionID)}
                          class="inline-flex items-center gap-1 rounded-md px-1.5 py-0.5 text-[12px] text-[var(--text-muted)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-hover)] disabled:opacity-40 disabled:cursor-not-allowed transition-colors cursor-pointer"
                          onClick={() => void onForkFromMessage(turn.info.id)}
                          title={isRunnerSessionID(workspace.activeSessionID) ? t("timeline.runnerFeatureUnsupported") : t("timeline.forkTooltip")}
                        >
                          <Icon name="fork" size="small" />
                          <span>{t("timeline.forkBtn")}</span>
                        </button>

                        <button
                          type="button"
                          disabled={isRunnerSessionID(workspace.activeSessionID)}
                          class="inline-flex items-center gap-1 rounded-md px-1.5 py-0.5 text-[12px] text-[var(--text-muted)] hover:text-[var(--warn-text)] hover:bg-[var(--warn-surface)] disabled:opacity-40 disabled:cursor-not-allowed transition-colors cursor-pointer"
                          onClick={() => void onRevertToMessage(turn.info.id)}
                          title={isRunnerSessionID(workspace.activeSessionID) ? t("timeline.runnerFeatureUnsupported") : t("timeline.revertTooltip")}
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
                completedSteps={executionCycleMeta().count}
                steps={executionCycleMeta().steps}
                startedAt={executionCycleMeta().startedAt}
                activeStepLabel={executionCycleMeta().activeStepLabel}
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
          <button
            type="button"
            onClick={() => { sessionScroll.interrupt(); scroll.jumpToBottom(); }}
            title={t("timeline.scrollToBottom")}
            aria-label={t("timeline.scrollToBottom")}
            aria-hidden={!scroll.awayFromBottom()}
            disabled={!scroll.awayFromBottom()}
            tabIndex={scroll.awayFromBottom() ? 0 : -1}
            class="absolute bottom-3 left-1/2 z-20 flex h-9 w-9 -translate-x-1/2 items-center justify-center rounded-full border border-[var(--border-subtle)] bg-[var(--bg-elevated)] text-[var(--text-secondary)] shadow-lg transition-[opacity,translate,scale,background-color,color,box-shadow] duration-200 ease-out motion-safe:enabled:hover:-translate-y-0.5 motion-safe:enabled:hover:scale-105 enabled:hover:shadow-xl enabled:hover:bg-[var(--bg-hover)] enabled:hover:text-[var(--text-primary)] motion-safe:enabled:active:translate-y-0 motion-safe:enabled:active:scale-95 focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-[var(--accent-primary)] motion-reduce:transition-none motion-reduce:translate-y-0 motion-reduce:scale-100 cursor-pointer"
            classList={{
              "opacity-100 translate-y-0 scale-100": scroll.awayFromBottom(),
              "opacity-0 translate-y-2 scale-90 pointer-events-none": !scroll.awayFromBottom(),
            }}
          >
            <Icon name="arrow-down-to-line" />
          </button>
      </div>
    </div>
  );
}
