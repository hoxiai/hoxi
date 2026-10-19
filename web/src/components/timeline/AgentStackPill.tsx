// web/src/components/timeline/AgentStackPill.tsx
import { createEffect, createMemo, createSignal, For, onCleanup, Show, untrack } from "solid-js";
import {
  ArrowLeft, Bot, CheckCircle2, ChevronDown, ChevronUp, ExternalLink,
  LoaderCircle, RotateCcw, ShieldAlert, Square, X,
} from "lucide-solid";
import { hoxiAPI } from "../../services/api";
import {
  markSessionInterrupted, resumeAllInterruptedSubagents, resumeSubagentTask,
  selectSession, sessionDirectory, workspace,
} from "../../store/workspace";
import { DiffChanges, Spinner } from "../icons";
import { t } from "../../i18n";
import {
  collectSubagentsForSession, resolveParentSessionID, resolveRootSessionID,
  SubagentSessionBanner, type SubagentStackItem,
} from "./SubagentSessionBanner";

export {
  collectSubagentsForSession, resolveParentSessionID, resolveRootSessionID,
  SubagentSessionBanner, type SubagentStackItem,
};

export default function AgentStackPill() {
  const [expanded, setExpanded] = createSignal(false);
  const [stoppingID, setStoppingID] = createSignal<string | null>(null);
  const [resumingID, setResumingID] = createSignal<string | null>(null);
  const [dismissedSigBySession, setDismissedSigBySession] = createSignal<Record<string, string>>({});
  const [justFinishedBySession, setJustFinishedBySession] = createSignal<Record<string, boolean>>({});
  const wasRunningSessions = new Set<string>();
  const finishTimers = new Map<string, ReturnType<typeof setTimeout>>();

  onCleanup(() => {
    for (const timer of finishTimers.values()) clearTimeout(timer);
    finishTimers.clear();
  });

  const rootID = createMemo(() => resolveRootSessionID(workspace.activeSessionID));
  const isViewingSubagent = createMemo(
    () => Boolean(workspace.activeSessionID && rootID() && workspace.activeSessionID !== rootID())
  );
  const subagents = createMemo<SubagentStackItem[]>(() => collectSubagentsForSession(rootID()));
  const runningCount = createMemo(() => subagents().filter((s) => s.status === "running").length);
  const interruptedList = createMemo(() => subagents().filter((s) => s.status === "interrupted"));
  const attentionCount = createMemo(() => subagents().filter((s) => s.needsAttention).length);
  const totalDiffStats = createMemo(() =>
    subagents().reduce(
      (acc, s) => ({ additions: acc.additions + s.additions, deletions: acc.deletions + s.deletions }),
      { additions: 0, deletions: 0 }
    )
  );
  const activeRunningSub = createMemo(() =>
    subagents().find((s) => s.status === "running" && s.currentStepLabel)
  );
  const stackSignature = createMemo(() =>
    subagents().map((s) => `${s.sessionID}:${s.status}`).join("|")
  );
  const isActionable = createMemo(() => {
    const rid = rootID();
    const sessionInterrupted = Boolean(rid && workspace.interruptedBySession[rid]);
    return (
      runningCount() > 0 ||
      attentionCount() > 0 ||
      isViewingSubagent() ||
      (sessionInterrupted && interruptedList().length > 0)
    );
  });
  const isAllDone = createMemo(() => subagents().length > 0 && !isActionable());
  const isDismissed = createMemo(() => {
    const rid = rootID();
    if (!rid || runningCount() > 0 || isViewingSubagent()) return false;
    return dismissedSigBySession()[rid] === stackSignature();
  });

  const clearFinishTimer = (rid: string) => {
    const existing = finishTimers.get(rid);
    if (existing) {
      clearTimeout(existing);
      finishTimers.delete(rid);
    }
  };

  const dismissStack = () => {
    const rid = rootID();
    const sig = stackSignature();
    if (!rid || !sig) return;
    clearFinishTimer(rid);
    setExpanded(false);
    setJustFinishedBySession((prev) => ({ ...prev, [rid]: false }));
    setDismissedSigBySession((prev) => ({ ...prev, [rid]: sig }));
  };

  createEffect(() => {
    const rid = rootID();
    if (!rid) return;
    if (runningCount() > 0) {
      wasRunningSessions.add(rid);
      clearFinishTimer(rid);
      if (untrack(() => justFinishedBySession()[rid])) {
        setJustFinishedBySession((prev) => ({ ...prev, [rid]: false }));
      }
      if (untrack(() => dismissedSigBySession()[rid])) {
        setDismissedSigBySession((prev) => {
          const next = { ...prev };
          delete next[rid];
          return next;
        });
      }
      return;
    }
    if (wasRunningSessions.has(rid)) {
      wasRunningSessions.delete(rid);
      clearFinishTimer(rid);
      if (!untrack(() => isActionable())) {
        setJustFinishedBySession((prev) => ({ ...prev, [rid]: true }));
        const timer = setTimeout(() => {
          finishTimers.delete(rid);
          setExpanded(false);
          setJustFinishedBySession((prev) => ({ ...prev, [rid]: false }));
        }, 3000);
        finishTimers.set(rid, timer);
      }
    }
  });

  const shouldShowStack = createMemo(() => {
    const rid = rootID();
    if (!rid || subagents().length === 0 || isDismissed()) return false;
    if (isActionable()) return true;
    return Boolean(justFinishedBySession()[rid]);
  });

  const stopSubagent = async (subID: string) => {
    if (stoppingID()) return;
    setStoppingID(subID);
    markSessionInterrupted(subID, "stopped");
    try {
      await hoxiAPI.abortSession(subID, sessionDirectory(subID));
    } catch (err) {
      console.error("abort subagent failed:", err);
    } finally {
      setStoppingID(null);
    }
  };

  const stopAllRunning = async () => {
    const running = subagents().filter((s) => s.status === "running");
    if (running.length === 0 || stoppingID()) return;
    setStoppingID("all");
    for (const s of running) markSessionInterrupted(s.sessionID, "stopped");
    try {
      await Promise.all(
        running.map((s) =>
          hoxiAPI.abortSession(s.sessionID, sessionDirectory(s.sessionID)).catch(() => {})
        )
      );
    } finally {
      setStoppingID(null);
    }
  };

  const handleResumeOne = async (sub: SubagentStackItem) => {
    const pid = rootID();
    if (!pid || resumingID()) return;
    setResumingID(sub.sessionID);
    try {
      if (isViewingSubagent()) await selectSession(pid);
      await resumeSubagentTask({
        parentSessionID: pid,
        childSessionID: sub.sessionID,
        agentType: sub.agentType,
        description: sub.description,
      });
    } finally {
      setResumingID(null);
    }
  };

  const handleResumeAll = async () => {
    const pid = rootID();
    const list = interruptedList();
    if (!pid || list.length === 0 || resumingID()) return;
    setResumingID("all");
    try {
      if (isViewingSubagent()) await selectSession(pid);
      await resumeAllInterruptedSubagents(
        pid,
        list.map((s) => ({
          childSessionID: s.sessionID,
          agentType: s.agentType,
          description: s.description,
        }))
      );
    } finally {
      setResumingID(null);
    }
  };

  createEffect(() => {
    if (!expanded()) return;
    const onPointerDown = (e: PointerEvent) => {
      if (containerRef && !containerRef.contains(e.target as Node)) setExpanded(false);
    };
    const onKeyDown = (e: KeyboardEvent) => {
      if (e.key === "Escape") setExpanded(false);
    };
    document.addEventListener("pointerdown", onPointerDown, true);
    document.addEventListener("keydown", onKeyDown);
    onCleanup(() => {
      document.removeEventListener("pointerdown", onPointerDown, true);
      document.removeEventListener("keydown", onKeyDown);
    });
  });

  let containerRef: HTMLDivElement | undefined;

  return (
    <Show when={shouldShowStack()}>
      <div ref={containerRef} class="relative inline-flex items-center">
        {/* Compact Pill Trigger */}
        <div
          class={`inline-flex items-center gap-1.5 rounded-full border px-2.5 py-1 text-[11.5px] transition-colors ${
            expanded()
              ? "border-[var(--accent-border)] bg-[var(--accent-surface)]/70 text-[var(--text-primary)]"
              : "border-[var(--border-subtle)] bg-[var(--bg-inset)]/90 text-[var(--text-secondary)] hover:bg-[var(--bg-hover)]"
          }`}
        >
          <button
            type="button"
            onClick={() => setExpanded((v) => !v)}
            class="inline-flex items-center gap-1.5 text-left min-w-0 cursor-pointer"
          >
            <Show
              when={runningCount() > 0}
              fallback={<Bot size={12.5} class="text-[var(--accent-primary)] shrink-0" />}
            >
              <span class="text-[var(--accent-primary)] shrink-0 inline-flex items-center">
                <Spinner class="w-3 h-3" />
              </span>
            </Show>

            <span class="font-medium text-[var(--text-primary)] shrink-0">
              {t("timeline.subagentStack")} ·{" "}
              {runningCount() > 0
                ? t("timeline.subagentsRunning", { count: runningCount() })
                : interruptedList().length > 0
                  ? t("timeline.subagentsInterrupted", { count: interruptedList().length })
                  : t("timeline.subagentsDone", { count: subagents().length })}
            </span>

            <Show when={!expanded() && activeRunningSub()?.currentStepLabel}>
              <span class="hidden sm:inline font-mono text-[11px] text-[var(--text-muted)] truncate max-w-[160px]">
                · {activeRunningSub()!.currentStepLabel}
              </span>
            </Show>

            <Show
              when={totalDiffStats().additions > 0 || totalDiffStats().deletions > 0}
            >
              <span class="shrink-0">
                <DiffChanges
                  changes={{
                    additions: totalDiffStats().additions,
                    deletions: totalDiffStats().deletions,
                  }}
                />
              </span>
            </Show>

            <Show when={attentionCount() > 0}>
              <span class="inline-flex items-center gap-1 rounded-full bg-[var(--warn-surface)] px-1.5 py-0.2 text-[10px] font-medium text-[var(--warn-text)] shrink-0">
                <ShieldAlert size={10} />
                {t("timeline.subagentsWaiting", { count: attentionCount() })}
              </span>
            </Show>

            <span class="text-[var(--text-muted)] inline-flex items-center">
              {expanded() ? <ChevronDown size={12} /> : <ChevronUp size={12} />}
            </span>
          </button>

          <Show when={isViewingSubagent() && rootID()}>
            {(mainID) => (
              <button
                type="button"
                onClick={() => void selectSession(mainID())}
                class="inline-flex items-center gap-0.5 rounded-full bg-[var(--accent-surface)] px-2 py-0.5 text-[10px] font-medium text-[var(--accent-primary)] hover:opacity-90 cursor-pointer"
              >
                <ArrowLeft size={10} />
                <span>{t("timeline.backToMainSessionShort")}</span>
              </button>
            )}
          </Show>

          <Show when={isAllDone()}>
            <button
              type="button"
              onClick={(e) => {
                e.stopPropagation();
                dismissStack();
              }}
              class="p-0.5 rounded-full text-[var(--text-muted)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-hover)] transition-colors cursor-pointer"
              title={t("common.close")}
            >
              <X size={12} />
            </button>
          </Show>
        </div>

        {/* Upward Floating Popover */}
        <Show when={expanded()}>
          <div class="absolute left-1/2 -translate-x-1/2 bottom-[calc(100%+6px)] z-40 w-[min(560px,calc(100vw-32px))] rounded-xl border border-[var(--border-subtle)] bg-[var(--bg-elevated)]/98 backdrop-blur-xl p-2.5 shadow-2xl animate-in fade-in zoom-in-95 duration-150">
            <div class="flex items-center justify-between gap-2 border-b border-[var(--border-subtle)] pb-2 mb-2 text-[11.5px]">
              <div class="flex items-center gap-2 font-medium text-[var(--text-primary)]">
                <Bot size={13} class="text-[var(--accent-primary)]" />
                <span>
                  {t("timeline.subagentStack")} ({subagents().length})
                </span>
              </div>
              <div class="flex items-center gap-1.5">
                <Show when={interruptedList().length > 0 && runningCount() === 0}>
                  <button
                    type="button"
                    disabled={!!resumingID()}
                    onClick={() => void handleResumeAll()}
                    class="inline-flex items-center gap-1 rounded-md border border-[var(--accent-border)] bg-[var(--accent-surface)] px-2 py-0.5 text-[10.5px] font-medium text-[var(--accent-primary)] hover:opacity-90 cursor-pointer"
                    title={t("timeline.resumeSubagentTooltip")}
                  >
                    <RotateCcw size={10} />
                    <span>
                      {interruptedList().length > 1
                        ? t("timeline.resumeAllSubagentsBtn", { count: interruptedList().length })
                        : t("timeline.resumeSubagentBtn")}
                    </span>
                  </button>
                </Show>

                <Show when={runningCount() > 1}>
                  <button
                    type="button"
                    disabled={!!stoppingID()}
                    onClick={() => void stopAllRunning()}
                    class="inline-flex items-center gap-1 rounded-md bg-[var(--danger-surface)] px-2 py-0.5 text-[10.5px] font-medium text-[var(--danger-text)] hover:opacity-90 cursor-pointer"
                  >
                    <Square size={9} class="fill-current" />
                    <span>{t("timeline.stopAllSubagents")}</span>
                  </button>
                </Show>

                <button
                  type="button"
                  onClick={() => setExpanded(false)}
                  class="p-0.5 rounded text-[var(--text-muted)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-hover)] cursor-pointer"
                  title={t("common.close")}
                >
                  <X size={13} />
                </button>
              </div>
            </div>

            <div class="space-y-1.5 max-h-56 overflow-y-auto">
              <For each={subagents()}>
                {(sub) => {
                  const isCurrent = () => workspace.activeSessionID === sub.sessionID;
                  return (
                    <div
                      onClick={() => {
                        setExpanded(false);
                        if (!isCurrent()) void selectSession(sub.sessionID);
                      }}
                      class={`flex items-center justify-between gap-2 rounded-lg px-2.5 py-1.5 text-[11.5px] transition-colors ${
                        isCurrent()
                          ? "border border-[var(--accent-border)] bg-[var(--accent-surface)]/30"
                          : "border border-transparent bg-[var(--bg-panel)]/70 hover:bg-[var(--bg-hover)]/60 cursor-pointer"
                      }`}
                    >
                      <div class="flex items-center gap-2 min-w-0 flex-1">
                        <Show
                          when={sub.status === "running"}
                          fallback={
                            <CheckCircle2
                              size={12}
                              class={
                                sub.status === "error"
                                  ? "text-[var(--danger-text)] shrink-0"
                                  : sub.status === "interrupted"
                                    ? "text-[var(--warn-text)] shrink-0"
                                    : "text-[var(--ok-text)] shrink-0"
                              }
                            />
                          }
                        >
                          <span class="text-[var(--accent-primary)] shrink-0 inline-flex items-center">
                            <Spinner class="w-3 h-3" />
                          </span>
                        </Show>
                        <span class="rounded bg-[var(--bg-inset)] px-1.5 py-0.5 font-mono text-[10px] text-[var(--text-muted)] shrink-0">
                          {sub.agentType}
                        </span>
                        <span class="truncate text-[var(--text-primary)]" title={sub.titleTooltip}>
                          {sub.title}
                        </span>
                        <Show when={sub.currentStepLabel}>
                          <span class="font-mono text-[10.5px] text-[var(--text-muted)] truncate max-w-[160px]">
                            · {sub.currentStepLabel}
                          </span>
                        </Show>
                        <Show when={sub.stepCount > 0}>
                          <span class="rounded bg-[var(--bg-inset)] px-1.5 py-0.2 font-mono text-[10px] text-[var(--text-faint)] shrink-0">
                            {t("timeline.stepsCompleted", { count: sub.stepCount })}
                          </span>
                        </Show>
                        <Show when={sub.additions > 0 || sub.deletions > 0}>
                          <span class="shrink-0">
                            <DiffChanges
                              changes={{ additions: sub.additions, deletions: sub.deletions }}
                            />
                          </span>
                        </Show>
                        <Show when={sub.status === "interrupted"}>
                          <span class="rounded bg-[var(--warn-surface)] px-1.5 py-0.2 text-[9.5px] font-medium text-[var(--warn-text)] shrink-0">
                            {t("timeline.subagentInterruptedBadge")}
                          </span>
                        </Show>
                      </div>

                      <div
                        class="flex items-center gap-1.5 shrink-0"
                        onClick={(e) => e.stopPropagation()}
                      >
                        <Show when={sub.status === "interrupted"}>
                          <button
                            type="button"
                            disabled={!!resumingID()}
                            onClick={() => void handleResumeOne(sub)}
                            class="inline-flex items-center gap-1 rounded border border-[var(--accent-border)] bg-[var(--accent-surface)] px-1.5 py-0.5 text-[10px] font-medium text-[var(--accent-primary)] hover:opacity-90 cursor-pointer"
                            title={t("timeline.resumeSubagentTooltip")}
                          >
                            <RotateCcw size={9.5} />
                            <span>{t("timeline.resumeSubagentBtn")}</span>
                          </button>
                        </Show>

                        <Show when={sub.status === "running"}>
                          <button
                            type="button"
                            disabled={!!stoppingID()}
                            onClick={() => void stopSubagent(sub.sessionID)}
                            class="inline-flex items-center gap-1 rounded px-1.5 py-0.5 text-[10.5px] text-[var(--danger-text)] hover:bg-[var(--danger-surface)] cursor-pointer"
                            title={t("timeline.stopSubagentTooltip")}
                          >
                            <Show
                              when={stoppingID() !== sub.sessionID}
                              fallback={<LoaderCircle size={10} class="animate-spin" />}
                            >
                              <Square size={9} class="fill-current" />
                            </Show>
                          </button>
                        </Show>

                        <Show
                          when={!isCurrent()}
                          fallback={
                            <span class="rounded bg-[var(--accent-surface)] px-1.5 py-0.5 text-[10px] font-medium text-[var(--accent-primary)]">
                              {t("timeline.viewingSubagentBadge")}
                            </span>
                          }
                        >
                          <button
                            type="button"
                            onClick={() => {
                              setExpanded(false);
                              void selectSession(sub.sessionID);
                            }}
                            class="inline-flex items-center gap-1 rounded px-1.5 py-0.5 text-[10.5px] font-medium text-[var(--accent-primary)] hover:bg-[var(--accent-surface)] cursor-pointer"
                          >
                            <span>
                              {isViewingSubagent()
                                ? t("timeline.switchSubagent")
                                : t("timeline.openSubagent")}
                            </span>
                            <ExternalLink size={10} />
                          </button>
                        </Show>
                      </div>
                    </div>
                  );
                }}
              </For>
            </div>
          </div>
        </Show>
      </div>
    </Show>
  );
}
