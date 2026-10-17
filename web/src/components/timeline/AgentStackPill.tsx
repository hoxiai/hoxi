// web/src/components/timeline/AgentStackPill.tsx
import { createMemo, createSignal, For, Show } from "solid-js";
import {
  ArrowLeft,
  Bot,
  CheckCircle2,
  ChevronDown,
  ChevronUp,
  ExternalLink,
  LoaderCircle,
  RotateCcw,
  ShieldAlert,
  Square,
} from "lucide-solid";
import { hoxiAPI } from "../../services/api";
import {
  resumeAllInterruptedSubagents,
  resumeSubagentTask,
  selectSession,
  sessionDirectory,
  setWorkspace,
  workspace,
} from "../../store/workspace";
import { Spinner } from "../icons";
import { t } from "../../i18n";
import {
  collectSubagentsForSession,
  resolveParentSessionID,
  resolveRootSessionID,
  SubagentSessionBanner,
  type SubagentStackItem,
} from "./SubagentSessionBanner";

export {
  collectSubagentsForSession,
  resolveParentSessionID,
  resolveRootSessionID,
  SubagentSessionBanner,
  type SubagentStackItem,
};

/**
 * Ported from kilocode AgentStack.tsx & background-agents.ts:
 * Tracks active, interrupted (resumable via task_id), and completed child subagents
 * spawned from the current root session.
 */
export default function AgentStackPill() {
  const [expanded, setExpanded] = createSignal(false);
  const [stoppingID, setStoppingID] = createSignal<string | null>(null);
  const [resumingID, setResumingID] = createSignal<string | null>(null);

  const rootID = createMemo(() => resolveRootSessionID(workspace.activeSessionID));
  const isViewingSubagent = createMemo(
    () => Boolean(workspace.activeSessionID && rootID() && workspace.activeSessionID !== rootID())
  );

  const subagents = createMemo<SubagentStackItem[]>(() =>
    collectSubagentsForSession(rootID())
  );

  const runningCount = createMemo(
    () => subagents().filter((s) => s.status === "running").length
  );

  const interruptedList = createMemo(() =>
    subagents().filter((s) => s.status === "interrupted")
  );

  const attentionCount = createMemo(
    () => subagents().filter((s) => s.needsAttention).length
  );

  const stopSubagent = async (subID: string) => {
    if (stoppingID()) return;
    setStoppingID(subID);
    setWorkspace("statusBySession", subID, { type: "idle" });
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
    for (const s of running) {
      setWorkspace("statusBySession", s.sessionID, { type: "idle" });
    }
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

  return (
    <Show when={subagents().length > 0}>
      <div class="rounded-xl border border-[var(--border-subtle)] bg-[var(--bg-inset)]/85 px-3 py-2">
        <div class="flex items-center justify-between gap-2 text-[12px]">
          <button
            type="button"
            onClick={() => setExpanded((v) => !v)}
            class="flex items-center gap-2 text-left min-w-0 flex-1 cursor-pointer"
          >
            <Show
              when={runningCount() > 0}
              fallback={<Bot size={13} class="text-[var(--accent-primary)] shrink-0" />}
            >
              <span class="text-[var(--accent-primary)] shrink-0 inline-flex items-center">
                <Spinner class="w-3.5 h-3.5" />
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

            <Show when={attentionCount() > 0}>
              <span class="inline-flex items-center gap-1 rounded-full bg-[var(--warn-surface)] px-2 py-0.5 text-[10.5px] font-medium text-[var(--warn-text)]">
                <ShieldAlert size={11} />
                {t("timeline.subagentsWaiting", { count: attentionCount() })}
              </span>
            </Show>
          </button>

          <div class="flex items-center gap-1.5 shrink-0">
            <Show when={interruptedList().length > 0 && runningCount() === 0}>
              <button
                type="button"
                disabled={!!resumingID()}
                onClick={() => void handleResumeAll()}
                class="inline-flex items-center gap-1 rounded-md border border-[var(--accent-border)] bg-[var(--accent-surface)] px-2 py-0.5 text-[10.5px] font-medium text-[var(--accent-primary)] hover:opacity-90 transition-opacity cursor-pointer"
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

            <Show when={isViewingSubagent() && rootID()}>
              {(mainID) => (
                <button
                  type="button"
                  onClick={() => void selectSession(mainID())}
                  class="inline-flex items-center gap-1 rounded-md border border-[var(--accent-border)] bg-[var(--accent-surface)] px-2 py-0.5 text-[10.5px] font-medium text-[var(--accent-primary)] hover:opacity-90 transition-opacity cursor-pointer"
                >
                  <ArrowLeft size={10.5} />
                  <span>{t("timeline.backToMainSession")}</span>
                </button>
              )}
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
              onClick={() => setExpanded((v) => !v)}
              class="text-[var(--text-muted)] hover:text-[var(--text-primary)] cursor-pointer"
            >
              {expanded() ? <ChevronDown size={13} /> : <ChevronUp size={13} />}
            </button>
          </div>
        </div>

        <Show when={expanded()}>
          <div class="mt-2 space-y-1.5 border-t border-[var(--border-subtle)] pt-2 max-h-40 overflow-y-auto">
            <For each={subagents()}>
              {(sub) => {
                const isCurrent = () => workspace.activeSessionID === sub.sessionID;
                return (
                  <div
                    onClick={() => {
                      if (!isCurrent()) void selectSession(sub.sessionID);
                    }}
                    class={`flex items-center justify-between gap-2 rounded-lg px-2.5 py-1.5 text-[11.5px] transition-colors ${
                      isCurrent()
                        ? "border border-[var(--accent-border)] bg-[var(--accent-surface)]/30"
                        : "border border-transparent bg-[var(--bg-panel)]/70 hover:bg-[var(--bg-hover)]/60 cursor-pointer"
                    }`}
                  >
                    <div class="flex items-center gap-2 min-w-0">
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
                      <span class="truncate text-[var(--text-primary)]">
                        {sub.description}
                      </span>
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
                          <div class="flex items-center gap-1">
                            <span class="rounded bg-[var(--accent-surface)] px-1.5 py-0.5 text-[10px] font-medium text-[var(--accent-primary)]">
                              {t("timeline.viewingSubagentBadge")}
                            </span>
                            <Show when={rootID()}>
                              {(mainID) => (
                                <button
                                  type="button"
                                  onClick={() => void selectSession(mainID())}
                                  class="inline-flex items-center gap-0.5 rounded px-1.5 py-0.5 text-[10.5px] font-medium text-[var(--text-secondary)] hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)] cursor-pointer"
                                >
                                  <ArrowLeft size={10} />
                                  <span>{t("timeline.backToMainSessionShort")}</span>
                                </button>
                              )}
                            </Show>
                          </div>
                        }
                      >
                        <button
                          type="button"
                          onClick={() => void selectSession(sub.sessionID)}
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
        </Show>
      </div>
    </Show>
  );
}
