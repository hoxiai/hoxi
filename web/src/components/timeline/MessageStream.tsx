// web/src/components/timeline/MessageStream.tsx
import {
  createEffect,
  createMemo,
  createSignal,
  For,
  Index,
  on,
  onCleanup,
  onMount,
  Show,
  untrack,
} from "solid-js";
import type { MessagePart, SessionStatus, ToolPart } from "../../services/sse-types";
import type { MessageTurn } from "../../store/workspace";
import {
  appSettings,
  canForkSession,
  canRevertSession,
  ensureFullSessionHistory,
  findLatestUserTurnID,
  forkFromMessage,
  hasOlderSessionHistory,
  imageViewerOpen,
  loadOlderSessionHistory,
  openImagePreview,
  isUserTurnTaskActive as isUserTurnTaskActiveInStore,
  resolveTurnSkillStatus,
  revertToHere,
  stopSessionTask,
  timelineFollowRequest,
  workspace,
} from "../../store/workspace";
import { FileIcon, Icon } from "../icons";
import {
  ActionIconButton,
  formatMessageTime,
  hasLiveTailRenderedOutput,
  isTodoTool,
  LiveExecutionControlBar,
  resolveActiveCycleStepLabel,
  SessionRetryCard,
  UserTextPartView,
} from "./message-stream-parts";
import {
  AssistantTurnItem,
  countUserMessages,
  createTurnWindow,
  EarlierTurnsBanner,
  filterVisibleTurnsByRevert,
} from "./turn-window";
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
import { resolveActiveExecutionCycle, resolveTurnNavigationTarget } from "./context-activity-turns";

export default function MessageStream(props: { hidden?: boolean }) {
  let scroller: HTMLDivElement | undefined;
  let content: HTMLDivElement | undefined;
  const scroll = createTimelineScroll(() => scroller, () => appSettings.autoScrollStream);
  const transcriptControls = createTranscriptControls(() => workspace.activeSessionID);
  const [copiedUserMsgId, setCopiedUserMsgId] = createSignal<string | null>(null);
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

  const activeSession = createMemo(() => {
    const id = renderedSessionID();
    return id ? workspace.sessions.find((s) => s.id === id) : undefined;
  });

  const turns = createMemo(() => {
    const id = renderedSessionID();
    const raw = id ? workspace.messagesBySession[id] ?? [] : [];
    return filterVisibleTurnsByRevert(raw, activeSession()?.revert);
  });

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
    sessionID: () => workspace.activeSessionID,
    turnIDs: () => turns().map((turn) => turn.info.id),
    loading: () => isLoadingSession(),
    scroller: () => scroller,
    content: () => content,
    scroll,
    ensureTurnVisible: turnWindow.ensureTurnVisible,
  });
  let promptFollowFrame: number | undefined;

  onCleanup(() => {
    if (promptFollowFrame !== undefined) cancelAnimationFrame(promptFollowFrame);
  });

  // A prompt the user just sent (or queued) always brings the live tail back into
  // view, even when they had scrolled up; automatic queue drains do not.
  createEffect(on(timelineFollowRequest, (request) => {
    const sessionID = request?.sessionID;
    if (!sessionID || sessionID !== workspace.activeSessionID) return;
    sessionScroll.interrupt();
    scroll.jumpToBottom();
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

  const onScroll = () => {
    if (!scroller) return;
    readingPosition.schedule();
    transcriptControls.onScroll(scroller.scrollTop);
    if (sessionScroll.isRestoring()) return;
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

  const scrollToTurn = (messageID: string, partID?: string) => {
    sessionScroll.interrupt();
    scroll.pause();
    const targetID = resolveTurnNavigationTarget(turns(), messageID, partID);
    setNavigationRevision((value) => value + 1);
    turnWindow.ensureTurnVisible(targetID);
    queueMicrotask(() => {
      if (!scroller) return;
      const partEl = partID
        ? scroller.querySelector(`[data-part-id="${CSS.escape(partID)}"]`)
        : null;
      const el =
        partEl ?? scroller.querySelector(`[data-turn-id="${CSS.escape(targetID)}"]`);
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
        !imageViewerOpen() &&
        !appSettings.modalOpen &&
        !workspace.arenaModalOpen &&
        (ev.key === "Escape" || ((ev.metaKey || ev.ctrlKey) && ev.key === "."))
      ) {
        ev.preventDefault();
        void stopSessionTask(workspace.activeSessionID);
      }
    };
    const onFocusMessage = (event: Event) => {
      const messageID = (event as CustomEvent<{ messageID?: string }>).detail?.messageID;
      if (!messageID) return;
      scrollToTurn(messageID);
      setTimeout(() => scrollToTurn(messageID), 450);
    };
    window.addEventListener("keydown", onWindowKeyDown);
    window.addEventListener("hoxi:focus-message", onFocusMessage);
    onCleanup(() => {
      window.removeEventListener("keydown", onWindowKeyDown);
      window.removeEventListener("hoxi:focus-message", onFocusMessage);
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
      <div class="relative flex-1 min-h-0" style={{ "container-type": "inline-size" }}>
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
            if (event.deltaY < -1 && scroller.scrollTop > 0) {
              sessionScroll.interrupt();
              scroll.markUserScrollInput();
              scroll.pause();
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
        if (event.deltaY < -1 && scroller.scrollTop > 0) {
          sessionScroll.interrupt();
          scroll.markUserScrollInput();
          scroll.pause();
        }
      }}
      onTouchStart={() => scroll.setPointerScrolling(true)}
      onTouchEnd={() => scroll.setPointerScrolling(false)}
      onTouchCancel={() => scroll.setPointerScrolling(false)}
      onTouchMove={() => {
        sessionScroll.interrupt();
        transcriptControls.markIntent();
        scroll.markUserScrollInput();
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
              <div class="hoxi-fade-enter">
                <SessionSkeleton />
              </div>
            </Show>
          }
        >
          <div class="flex flex-col">
            <EarlierTurnsBanner
              hiddenMessageCount={turnWindow.hiddenMessageCount()}
              totalCount={countUserMessages(turns())}
              hasOlderHistory={hasOlderSessionHistory(renderedSessionID())}
              onLoadBatch={() => turnWindow.loadEarlierBatch(scroller)}
              onLoadAll={() => turnWindow.loadAllEarlier(scroller)}
            />

            <For each={turnWindow.visibleTurns()}>
              {(turn, idx) => {
                const globalIndex = () => turnWindow.globalIndex(idx());
                const enterClass = turn.info.id.startsWith("optimistic_") ? "hoxi-item-enter " : "";
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
                    {/* Kilo-style Right-Aligned User Message Bubble */}
                    <div
                      data-turn-id={turn.info.id}
                      class={`${enterClass}group/turn flex flex-col items-end rounded-xl transition-colors ${
                        idx() === 0 ? "mt-0" : "mt-6"
                      } ${
                        activeMatchTurnID() === turn.info.id
                          ? "ring-2 ring-[var(--accent-primary)] p-1.5"
                          : ""
                      }`}
                    >
                      <div class="max-w-[88%] rounded-lg bg-[var(--bg-inset)]/70 px-3.5 py-2.5 space-y-2">
                        <Show
                          when={resolveTurnSkillStatus(
                            turns(),
                            globalIndex(),
                            isSessionBusy()
                          )}
                        >
                          {(skillInfo) => {
                            const statusLabel = () => {
                              const st = skillInfo().status;
                              const name = skillInfo().skill.name;
                              switch (st) {
                                case "requested":
                                  return `${name} · ${t("skills.statusRequested")}`;
                                case "loading":
                                  return `${name} · ${t("skills.statusLoading")}`;
                                case "loaded":
                                  return `${name} · ${t("skills.statusLoaded")}`;
                                case "error":
                                  return `${name} · ${t("skills.statusError")}`;
                                case "unconfirmed":
                                  return `${name} · ${t("skills.statusUnconfirmed")}`;
                              }
                            };
                            const statusClass = () => {
                              switch (skillInfo().status) {
                                case "loaded":
                                  return "border-emerald-500/30 bg-emerald-500/10 text-emerald-500";
                                case "error":
                                  return "border-[var(--danger-border)] bg-[var(--danger-surface)] text-[var(--danger-text)]";
                                case "unconfirmed":
                                  return "border-[var(--border-subtle)] bg-[var(--bg-panel)] text-[var(--text-muted)]";
                                default:
                                  return "border-[var(--accent-border)] bg-[var(--accent-surface)] text-[var(--accent-text)]";
                              }
                            };
                            return (
                              <div class="flex items-center">
                                <span
                                  class={`inline-flex items-center gap-1.5 rounded-md border px-2 py-0.5 font-mono text-[11px] font-medium ${statusClass()}`}
                                >
                                  <Icon name="brain" size="small" class="shrink-0" />
                                  <span>{statusLabel()}</span>
                                </span>
                              </div>
                            );
                          }}
                        </Show>
                        <Index each={turn.parts}>
                          {(part) => (
                            <>
                              <Show when={part().type === "file"}>
                                {(() => {
                                  const fp = part() as Extract<MessagePart, { type: "file" }>;
                                  const isImg =
                                    (fp.mime || "").toLowerCase().startsWith("image/") ||
                                    /^data:image\//i.test(fp.url || "") ||
                                    /\.(png|jpe?g|webp|gif|bmp|tiff?|svg|heic|heif|avif)$/i.test(
                                      fp.filename || ""
                                    );
                                  const extLabel =
                                    (fp.mime || "").toLowerCase() === "application/pdf" ||
                                    /\.pdf$/i.test(fp.filename || "")
                                      ? "PDF"
                                      : ((fp.filename || "").split(".").pop() || "FILE")
                                          .toUpperCase()
                                          .slice(0, 4);
                                  const isSheet =
                                    /\.(xlsx|xls|ods|csv|tsv)$/i.test(fp.filename || "") ||
                                    (fp.mime || "").includes("spreadsheet") ||
                                    (fp.mime || "").includes("excel") ||
                                    (fp.mime || "").includes("csv");
                                  const badgeColor = isSheet
                                    ? "text-emerald-500"
                                    : extLabel === "PDF"
                                      ? "text-rose-500"
                                      : "text-[var(--accent-primary)]";
                                  return (
                                    <Show
                                      when={isImg}
                                      fallback={
                                        <a
                                          data-part-id={fp.id}
                                          href={fp.url}
                                          target="_blank"
                                          rel="noreferrer"
                                          download={fp.filename || "attachment"}
                                          class="inline-flex items-center gap-2 rounded-lg border border-[var(--border-subtle)] bg-[var(--bg-panel)] px-2.5 py-1.5 text-[12px] text-[var(--text-primary)] hover:border-[var(--accent-border)] transition-colors max-w-[260px]"
                                        >
                                          <FileIcon
                                            node={{
                                              path: fp.filename || "file.txt",
                                              type: "file",
                                            }}
                                            class="h-4 w-4 shrink-0"
                                          />
                                          <span class="truncate font-mono text-[12px]">
                                            {fp.filename || "attachment"}
                                          </span>
                                          <span class={`ml-auto shrink-0 rounded bg-[var(--bg-inset)] px-1.5 py-0.5 font-mono text-[11px] font-semibold ${badgeColor}`}>
                                            {extLabel}
                                          </span>
                                        </a>
                                      }
                                    >
                                      <button
                                        type="button"
                                        data-part-id={fp.id}
                                        onClick={() =>
                                          openImagePreview({
                                            src: fp.url,
                                            alt: fp.filename || "attachment",
                                          })
                                        }
                                        title={t("timeline.previewImageTooltip")}
                                        class="block max-w-[260px] overflow-hidden rounded-lg border border-[var(--border-subtle)] cursor-zoom-in transition-colors hover:border-[var(--accent-border)]"
                                      >
                                        <img
                                          src={fp.url}
                                          alt={fp.filename || "attachment"}
                                          class="max-h-48 w-auto object-contain block"
                                        />
                                      </button>
                                    </Show>
                                  );
                                })()}
                              </Show>
                              <Show when={part().type === "text"}>
                                <UserTextPartView
                                  part={part() as Extract<MessagePart, { type: "text" }>}
                                  forceExpand={activeMatchTurnID() === turn.info.id}
                                />
                              </Show>
                            </>
                          )}
                        </Index>
                      </div>

                      {/* Kilo-style User Message Hover Actions & Metadata */}
                      <div class="mt-1 flex items-center justify-end gap-1 px-1 text-[12px] text-[var(--text-faint)] opacity-0 group-hover/turn:opacity-100 transition-opacity">
                        <Show when={formatMessageTime(turn.info.time?.created)}>
                          <span class="mr-1 font-mono text-[12px]">{formatMessageTime(turn.info.time?.created)}</span>
                        </Show>

                        <ActionIconButton
                          label={copiedUserMsgId() === turn.info.id ? t("common.copied") : t("common.copy")}
                          onClick={() => void onCopyUserMessage(turn.info.id, turn.parts)}
                        >
                          <Show
                            when={copiedUserMsgId() === turn.info.id}
                            fallback={<Icon name="copy" size="small" />}
                          >
                            <Icon name="check" size="small" class="text-[var(--ok-text)]" />
                          </Show>
                        </ActionIconButton>

                        <Show when={isUserTurnTaskActive(turn, globalIndex())}>
                          <ActionIconButton
                            disabled={!canForkSession(workspace.activeSessionID)}
                            label={
                              !canForkSession(workspace.activeSessionID)
                                ? t("timeline.runnerFeatureUnsupported")
                                : t("timeline.forkBtn")
                            }
                            onClick={() => void onForkFromMessage(turn.info.id)}
                          >
                            <Icon name="fork" size="small" />
                          </ActionIconButton>

                          <ActionIconButton
                            disabled={!canRevertSession(workspace.activeSessionID)}
                            variant="warn"
                            label={
                              !canRevertSession(workspace.activeSessionID)
                                ? isSessionBusy()
                                  ? t("timeline.revertWhileBusy")
                                  : t("timeline.runnerFeatureUnsupported")
                                : t("timeline.revertBtn")
                            }
                            onClick={() => void onRevertToMessage(turn.info.id)}
                          >
                            <Icon name="arrow-undo-down" size="small" />
                          </ActionIconButton>
                        </Show>
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
                awaitingApproval={pendingForSession().length > 0}
                completedSteps={executionCycleMeta().count}
                steps={executionCycleMeta().steps}
                startedAt={cycleStartedAt()}
                activeStepLabel={executionCycleMeta().activeStepLabel}
                lastTurnRole={turns()[turns().length - 1]?.info.role}
              />

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
