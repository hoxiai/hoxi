// web/src/components/timeline/MessageStream.tsx
import {
  createEffect,
  createMemo,
  createRenderEffect,
  createSignal,
  For,
  on,
  onCleanup,
  onMount,
  Show,
  untrack,
} from "solid-js";
import type { SessionStatus, ToolPart } from "../../services/sse-types";
import type { MessageTurn } from "../../store/workspace";
import {
  appSettings,
  cancelQueuedEditInComposer,
  ensureFullSessionHistory,
  findLatestUserTurnID,
  hasOlderSessionHistory,
  imageViewerOpen,
  isSessionTaskActive,
  loadOlderSessionHistory,
  isUserTurnTaskActive as isUserTurnTaskActiveInStore,
  sessionDirectory,
  stopSessionTask,
  timelineFollowRequest,
  wasQueuedTurnShown,
  workspace,
} from "../../store/workspace";
import { Icon } from "../icons";
import {
  hasLiveTailRenderedOutput,
  isTodoTool,
  LiveExecutionControlBar,
  resolveActiveCycleStepLabel,
  SessionRetryCard,
} from "./message-stream-parts";
import {
  AssistantTurnItem,
  countUserMessages,
  createTurnWindow,
  EarlierTurnsBanner,
  filterVisibleTurnsByRevert,
  TOP_SCROLL_THRESHOLD_PX,
} from "./turn-window";
import { UserTurnItem } from "./UserTurnItem";
import TaskTimelineBar from "./TaskTimelineBar";
import { SubagentSessionBanner } from "./AgentStackPill";
import QueuedPromptsList from "./QueuedPromptsList";
import { SessionSkeleton } from "./SessionSkeleton";
import { t } from "../../i18n";
import {
  createTimelineScroll,
  isNestedScrollableWheelTarget,
  isScrollbarGutterPointerDown,
} from "./timeline-scroll";
import { createTranscriptControls } from "./transcript-controls";
import { createReadingPositionTracker } from "./reading-position";
import { createSessionScroll } from "./session-scroll";
import { createTailHeightHold } from "./tail-hold";
import { resolveActiveExecutionCycle } from "./context-activity-turns";
import { createMessageNavigation } from "./message-navigation";

export default function MessageStream(props: { hidden?: boolean }) {
  let scroller: HTMLDivElement | undefined;
  let content: HTMLDivElement | undefined;
  const scroll = createTimelineScroll(() => scroller, () => appSettings.autoScrollStream);
  const transcriptControls = createTranscriptControls(() => workspace.activeSessionID);
  const [searchOpen, setSearchOpen] = createSignal(false);
  const [activeMatchTurnID, setActiveMatchTurnID] = createSignal<string | null>(null);
  const [navigationRevision, setNavigationRevision] = createSignal(0);

  const [retainedSessionID, setRetainedSessionID] = createSignal<string | null>(
    workspace.activeSessionID
  );
  createEffect(() => {
    if (workspace.activeSessionID) {
      setRetainedSessionID(workspace.activeSessionID);
    }
  });
  const renderedSessionID = () => workspace.activeSessionID ?? retainedSessionID();

  const activeSessionRevert = createMemo(() => {
    const id = renderedSessionID();
    return id ? workspace.sessions.find((s) => s.id === id)?.revert : undefined;
  });

  const turns = createMemo(() => {
    const id = renderedSessionID();
    const raw = id ? workspace.messagesBySession[id] ?? [] : [];
    return filterVisibleTurnsByRevert(raw, activeSessionRevert());
  });

  const turnIDs = createMemo(
    () => turns().map((turn) => turn.info.id),
    [],
    { equals: (a, b) => a.length === b.length && a.every((v, i) => v === b[i]) }
  );

  const assistantTurnIDs = createMemo(
    () =>
      turns()
        .filter((turn) => turn.info.role === "assistant")
        .map((turn) => turn.info.id),
    [],
    { equals: (a, b) => a.length === b.length && a.every((v, i) => v === b[i]) }
  );

  const turnWindow = createTurnWindow(renderedSessionID, turns, {
    hasOlder: () => hasOlderSessionHistory(renderedSessionID()),
    loadOlder: (around) => {
      const id = renderedSessionID();
      return id ? loadOlderSessionHistory(id, undefined, around) : Promise.resolve(0);
    },
    loadAll: () => {
      const id = renderedSessionID();
      return id ? ensureFullSessionHistory(id) : Promise.resolve();
    },
    onTopPrepend: (delta) => {
      scroll.noteTopPrepend(delta);
      transcriptControls.noteTopPrepend(delta);
    },
  });
  // Transcript search must cover turns that are not paged in yet.
  createEffect(() => {
    const id = renderedSessionID();
    if (!searchOpen() || !id) return;
    untrack(() => void ensureFullSessionHistory(id).catch(() => {}));
  });
  const readingPosition = createReadingPositionTracker({
    scroller: () => scroller,
    content: () => content,
    sessionID: renderedSessionID,
    assistantTurnIDs,
  });

  const activeStatus = createMemo<SessionStatus>(() => {
    const id = workspace.activeSessionID;
    return (id && workspace.statusBySession[id]) || { type: "idle" };
  });

  const isSessionBusy = createMemo(() => {
    const st = activeStatus();
    return st.type === "busy" || st.type === "retry";
  });

  const latestUserTurnID = createMemo(() => findLatestUserTurnID(turns()));
  // Scans back only to the newest user turn; shared by every assistant footer.
  const latestUserIndex = createMemo(() => {
    const list = turns();
    for (let i = list.length - 1; i >= 0; i--) if (list[i].info.role === "user") return i;
    return -1;
  });

  const isUserTurnTaskActive = (turn: MessageTurn, index: number): boolean => {
    if (turn.info.id !== latestUserTurnID()) return false;
    const sessionID = workspace.activeSessionID;
    return isUserTurnTaskActiveInStore(turns(), index, {
      busy: isSessionBusy(),
      interrupted: Boolean(sessionID && workspace.interruptedBySession[sessionID]),
      latestUserTurnID: latestUserTurnID(),
    });
  };

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

  const activeCycle = createMemo(() => resolveActiveExecutionCycle(turns()));

  // Keep the bottom live execution status bar continuously visible ("不断档") while busy
  const showThinkingPlaceholder = createMemo(() => {
    if (activeStatus().type !== "busy") return false;
    const list = turns();
    if (list.length === 0) return false;

    const last = list[list.length - 1];
    if (last.info.role === "assistant" && last.info.error) return false;

    const cycle = activeCycle();
    if (!cycle) return false;
    if (hasLiveTailRenderedOutput(cycle.cycleTurns)) return false;

    return true;
  });

  const tailHold = createTailHeightHold({
    sessionID: renderedSessionID,
    waiting: showThinkingPlaceholder,
    busy: isSessionBusy,
  });

  const cycleStartedAt = createMemo(() => {
    const cycle = activeCycle();
    if (!cycle) return undefined;
    return cycle.userTurn.info.time?.created || cycle.cycleTurns[0]?.info.time?.created;
  });

  const EMPTY_CYCLE_META = {
    count: 0,
    steps: [] as ToolPart[],
    activeStepLabel: undefined as string | undefined,
  };

  const executionCycleMeta = createMemo(() => {
    if (!showThinkingPlaceholder()) return EMPTY_CYCLE_META;
    const cycle = activeCycle();
    if (!cycle) return EMPTY_CYCLE_META;
    const { cycleTurns } = cycle;
    const steps: ToolPart[] = [];
    let completedCount = 0;
    for (const turn of cycleTurns) {
      for (const part of turn.parts) {
        if (part.type === "tool" && !isTodoTool(part)) {
          steps.push(part);
          if (part.state.status === "completed") completedCount++;
        }
      }
    }
    return {
      count: completedCount,
      steps,
      activeStepLabel: resolveActiveCycleStepLabel(cycleTurns),
    };
  });

  const sessionScroll = createSessionScroll({
    sessionID: renderedSessionID,
    turnIDs,
    loading: () => {
      const id = renderedSessionID();
      return isLoadingSession() || Boolean(id && workspace.loadingMessagesBySession[id]);
    },
    scroller: () => scroller,
    content: () => content,
    scroll,
    ensureTurnVisible: turnWindow.ensureTurnVisible,
    shouldFollowBottom: isSessionTaskActive,
  });
  let promptFollowFrame: number | undefined;

  onCleanup(() => {
    if (promptFollowFrame !== undefined) cancelAnimationFrame(promptFollowFrame);
  });

  // A prompt the user just sent (or queued), or selecting an active-task session,
  // always brings the live tail back into view; automatic queue drains do not.
  // Mark followTail before child render effects so any previous activity group
  // folds in the same render pass before jumpToBottom() reads layout.
  createRenderEffect(on(timelineFollowRequest, (request) => {
    const sessionID = request?.sessionID;
    if (!sessionID || sessionID !== workspace.activeSessionID) return;
    scroll.resume();
  }, { defer: true }));

  createEffect(on(timelineFollowRequest, (request) => {
    const sessionID = request?.sessionID;
    if (!sessionID || sessionID !== workspace.activeSessionID) return;
    sessionScroll.followToBottom();
    queueMicrotask(() => {
      if (workspace.activeSessionID === sessionID) scroll.jumpToBottom();
    });
    // The optimistic turn and the first assistant activity are committed in
    // separate reactive updates; re-anchor once more after the next layout pass.
    if (promptFollowFrame !== undefined) cancelAnimationFrame(promptFollowFrame);
    promptFollowFrame = requestAnimationFrame(() => {
      promptFollowFrame = undefined;
      if (workspace.activeSessionID === sessionID) scroll.jumpToBottom();
    });
  }, { defer: true }));

  createEffect(() => {
    void isSessionBusy();
    void showThinkingPlaceholder();
    void turns().length;
    if (appSettings.autoScrollStream && scroll.isFollowing()) {
      scroll.schedule();
    }
  });

  let runwayFrame: number | undefined;
  onCleanup(() => {
    if (runwayFrame !== undefined) cancelAnimationFrame(runwayFrame);
  });
  createEffect(() => {
    void renderedSessionID();
    void isLoadingSession();
    const count = turnWindow.visibleTurns().length;
    if (count === 0 || props.hidden || isLoadingSession()) return;
    if (runwayFrame !== undefined) cancelAnimationFrame(runwayFrame);
    runwayFrame = requestAnimationFrame(() => {
      runwayFrame = undefined;
      if (!scroller || props.hidden || isLoadingSession() || sessionScroll.isRestoring()) return;
      turnWindow.ensureTopRunway(scroller, scroll.isFollowing());
      if (appSettings.autoScrollStream && scroll.isFollowing()) {
        scroll.jumpToBottom();
      }
    });
  });

  const onScroll = () => {
    if (!scroller) return;
    readingPosition.schedule();
    transcriptControls.onScroll(scroller.scrollTop);
    if (sessionScroll.isRestoring()) return;
    const paused = scroll.onScroll();
    if (paused || scroller.scrollTop <= TOP_SCROLL_THRESHOLD_PX) {
      turnWindow.handleTopScroll(scroller);
    }
    sessionScroll.capture();
  };

  const { scrollToTurn } = createMessageNavigation({
    sessionID: () => workspace.activeSessionID,
    directory: () => sessionDirectory(workspace.activeSessionID),
    hidden: () => props.hidden,
    turns,
    scroller: () => scroller,
    interrupt: sessionScroll.interrupt,
    pause: scroll.pause,
    ensureTurnVisible: turnWindow.ensureTurnVisible,
    onNavigate: () => setNavigationRevision((value) => value + 1),
  });

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
        !ev.defaultPrevented &&
        !searchOpen() &&
        !imageViewerOpen() &&
        !appSettings.modalOpen &&
        !workspace.arenaModalOpen &&
        ev.key === "Escape" &&
        cancelQueuedEditInComposer(workspace.activeSessionID)
      ) {
        ev.preventDefault();
        return;
      }
      if (
        isSessionBusy() &&
        !ev.defaultPrevented &&
        !searchOpen() &&
        !imageViewerOpen() &&
        !appSettings.modalOpen &&
        !workspace.arenaModalOpen &&
        (ev.key === "Escape" || ((ev.metaKey || ev.ctrlKey) && ev.key === "."))
      ) {
        ev.preventDefault();
        void stopSessionTask(workspace.activeSessionID);
      }
    };
    window.addEventListener("keydown", onWindowKeyDown);
    onCleanup(() => {
      window.removeEventListener("keydown", onWindowKeyDown);
    });
    // Scrollbar drags can be released outside the transcript.
    const endPointerScroll = () => scroll.setPointerScrolling(false);
    window.addEventListener("pointerup", endPointerScroll);
    window.addEventListener("pointercancel", endPointerScroll);
    onCleanup(() => {
      window.removeEventListener("pointerup", endPointerScroll);
      window.removeEventListener("pointercancel", endPointerScroll);
    });
  });

  const isEmpty = () => turns().length === 0 && pendingForSession().length === 0;

  return (
    <div class="relative flex flex-1 min-h-0 flex-col" style={{ display: props.hidden ? "none" : undefined }}>
      <SubagentSessionBanner />
      {/* No container-type here: in WebKit a size container above the scroller lays out
          its subtree before new nodes get styles, clamping the bottom-pinned scrollTop. */}
      <div class="relative flex-1 min-h-0">
        <TaskTimelineBar
          sessionID={workspace.activeSessionID}
           turns={turns()}
           readingTurnID={readingPosition.readingTurnID()}
          activeMatchTurnID={activeMatchTurnID()}
          onJumpToTurn={(msgID, partID) => {
            setActiveMatchTurnID(msgID);
            scrollToTurn(msgID, partID);
          }}
          onToggleSearch={() => setSearchOpen((v) => !v)}
          searchOpen={searchOpen()}
          controlsVisible={transcriptControls.visible()}
          onCloseSearch={() => {
            setSearchOpen(false);
            setActiveMatchTurnID(null);
          }}
          onSearchMatch={(msgID, partID) => {
            setActiveMatchTurnID(msgID);
            if (msgID) scrollToTurn(msgID, partID);
          }}
          onWheel={(event) => {
            transcriptControls.markIntent();
            if (!scroller) return;
            if (event.deltaY < 0) {
              if (
                scroller.scrollTop > 0 ||
                turnWindow.hiddenCount() > 0 ||
                hasOlderSessionHistory(renderedSessionID())
              ) {
                sessionScroll.interrupt();
                scroll.markUserScrollInput();
                scroll.pause();
              }
              turnWindow.handleTopScroll(scroller);
            } else if (event.deltaY > 1) {
              scroll.markUserScrollInput();
            }
            scroller.scrollTop += event.deltaY;
          }}
        />
    <div
      ref={scroller}
      onScroll={onScroll}
      onWheel={(event) => {
        transcriptControls.markIntent();
        if (!scroller) return;
        if (isNestedScrollableWheelTarget(scroller, event.target, event.deltaX, event.deltaY)) {
          return;
        }
        // Stop following before the async scroll lands, or the next resize re-anchors.
        if (event.deltaY < 0) {
          if (
            scroller.scrollTop > 0 ||
            turnWindow.hiddenCount() > 0 ||
            hasOlderSessionHistory(renderedSessionID())
          ) {
            sessionScroll.interrupt();
            scroll.markUserScrollInput();
            scroll.pause();
          }
          turnWindow.handleTopScroll(scroller);
        }
      }}
      onTouchStart={() => scroll.setPointerScrolling(true)}
      onTouchEnd={() => scroll.setPointerScrolling(false)}
      onTouchCancel={() => scroll.setPointerScrolling(false)}
      onTouchMove={() => {
        sessionScroll.interrupt();
        transcriptControls.markIntent();
        scroll.markUserScrollInput();
        if (scroller) turnWindow.handleTopScroll(scroller);
      }}
      onPointerDown={(event) => {
        transcriptControls.startPointer();
        if (scroller && isScrollbarGutterPointerDown(scroller, event)) {
          sessionScroll.interrupt();
          scroll.setPointerScrolling(true);
        }
      }}
      onPointerUp={() => {
        transcriptControls.endPointer();
        if (isSessionBusy() && scroller && typeof window.getSelection === "function") {
          const selection = window.getSelection();
          if (
            selection &&
            !selection.isCollapsed &&
            selection.anchorNode &&
            scroller.contains(selection.anchorNode)
          ) {
            scroll.pause();
          }
        }
      }}
      onPointerCancel={() => transcriptControls.endPointer()}
      onKeyDown={(event) => {
        if (["ArrowUp", "ArrowDown", "PageUp", "PageDown", "Home", "End", " "].includes(event.key)) {
          transcriptControls.markIntent();
          if (["ArrowUp", "PageUp", "Home"].includes(event.key) || (event.key === " " && event.shiftKey)) {
            sessionScroll.interrupt();
            scroll.markUserScrollInput();
            scroll.pause();
            if (scroller) turnWindow.handleTopScroll(scroller);
          }
        }
      }}
      class="timeline-result-scroll relative h-full min-h-0 overflow-y-auto overflow-x-hidden"
    >

      {/* Top Soft Scroll Fade (Kilo style) */}
      <div
        aria-hidden="true"
        class="pointer-events-none sticky top-0 z-10 -mb-6 h-6 w-full shrink-0 bg-gradient-to-b from-[var(--bg-panel)] to-transparent"
      />

      <div
        ref={content}
        class="mx-auto flex w-full max-w-[calc(var(--thread-max-width)+3rem)] min-h-full flex-col px-6 pt-8 pb-12"
      >
        <Show
          when={!isEmpty()}
          fallback={
            <Show when={isLoadingSession()}>
              <div class="my-auto flex w-full flex-1 flex-col items-center justify-center hoxi-fade-enter">
                <SessionSkeleton />
              </div>
            </Show>
          }
        >
          <div ref={tailHold.list} class="flex flex-col">
            <EarlierTurnsBanner
              hiddenMessageCount={turnWindow.hiddenMessageCount()}
              totalCount={countUserMessages(turns())}
              hasOlderHistory={hasOlderSessionHistory(renderedSessionID())}
              onLoadBatch={() => turnWindow.loadEarlierBatch(scroller)}
              onLoadAll={() => turnWindow.loadAllEarlier(scroller)}
            />

            <For each={turnWindow.visibleTurns()}>
              {(turn, idx) => {
                const globalIndex = createMemo(() => turnWindow.globalIndex(idx()));
                const enterClass = idx() > 0 && turn.info.id.startsWith("optimistic_") &&
                  !wasQueuedTurnShown(turn.info.id) ? "hoxi-item-enter " : "";
                return (
                  <Show
                    when={turn.info.role === "user"}
                    fallback={
                      <AssistantTurnItem
                        turn={turn}
                        globalIndex={globalIndex()}
                        allTurns={turns()}
                        windowStartIndex={turnWindow.mergeStartIndex()}
                        isSessionBusy={isSessionBusy()}
                        activeMatchTurnID={activeMatchTurnID()}
                        navigationRevision={navigationRevision()}
                        isHighlighted={activeMatchTurnID() === turn.info.id}
                        cycleStartedAt={cycleStartedAt()}
                        latestUserIndex={latestUserIndex()}
                      />
                    }
                  >
                    <UserTurnItem
                      turn={turn}
                      index={idx()}
                      globalIndex={globalIndex()}
                      allTurns={turns()}
                      enterClass={enterClass}
                      isSessionBusy={isSessionBusy()}
                      isTaskActive={isUserTurnTaskActive(turn, globalIndex())}
                      isHighlighted={activeMatchTurnID() === turn.info.id}
                    />
                  </Show>
                );
              }}
            </For>

            {/* Live Execution Control Bar (Thinking / Executing + Pause / Stop / Resume) */}
            <div ref={tailHold.row} class="flex flex-col">
              <LiveExecutionControlBar
                sessionID={workspace.activeSessionID}
                isSessionBusy={isSessionBusy()}
                showThinking={showThinkingPlaceholder()}
                awaitingApproval={pendingForSession().length > 0}
                completedSteps={executionCycleMeta().count}
                steps={executionCycleMeta().steps}
                startedAt={cycleStartedAt()}
                activeStepLabel={executionCycleMeta().activeStepLabel}
                lastTurnRole={turns()[turns().length - 1]?.info.role}
              />
            </div>
            <div aria-hidden="true" style={{ height: `${tailHold.spacer()}px` }} />

            {/* Queued Follow-Up Prompts (Send Now | Edit | Delete) */}
            <QueuedPromptsList
              sessionID={workspace.activeSessionID}
              onSendNow={() => {
                const sessionID = workspace.activeSessionID;
                sessionScroll.interrupt();
                // Protect the queue removal from being interpreted as scrolling up.
                scroll.jumpToBottom();
                queueMicrotask(() => {
                  if (workspace.activeSessionID === sessionID) scroll.jumpToBottom();
                });
              }}
            />

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
             onClick={() => { sessionScroll.interrupt(); scroll.smoothToBottom(); }}
            title={t("timeline.scrollToBottom")}
            aria-label={t("timeline.scrollToBottom")}
            aria-hidden={!scroll.awayFromBottom()}
            disabled={!scroll.awayFromBottom()}
            tabIndex={scroll.awayFromBottom() ? 0 : -1}
             class="absolute bottom-3 left-1/2 z-20 flex h-9 w-9 -translate-x-1/2 items-center justify-center rounded-full border border-[var(--border-subtle)] bg-[var(--bg-elevated)] text-[var(--text-secondary)] shadow-lg transition-[opacity,translate,scale,background-color,color,box-shadow] duration-300 ease-in-out motion-safe:enabled:hover:-translate-y-0.5 motion-safe:enabled:hover:scale-105 enabled:hover:shadow-xl enabled:hover:bg-[var(--bg-hover)] enabled:hover:text-[var(--text-primary)] motion-safe:enabled:active:translate-y-0 motion-safe:enabled:active:scale-95 focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-[var(--accent-primary)] motion-reduce:transition-none motion-reduce:translate-y-0 motion-reduce:scale-100 cursor-pointer"
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
