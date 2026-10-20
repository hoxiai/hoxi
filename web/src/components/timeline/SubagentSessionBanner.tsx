// web/src/components/timeline/SubagentSessionBanner.tsx
import { createEffect, createMemo, createSignal, Show } from "solid-js";
import {
  ArrowLeft,
  CheckCircle2,
  ChevronLeft,
  ChevronRight,
  LoaderCircle,
  RotateCcw,
  Square,
} from "lucide-solid";
import type { ToolPart } from "../../services/sse-types";
import { hoxiAPI } from "../../services/api";
import {
  ensureChildSubagentSessionLoaded,
  extractChildSubagentSessionId,
  isPlaceholderSessionTitle,
  isSessionBusy,
  isSubagentToolAlive,
  isSubagentToolInterrupted,
  loadSessionMessagesOnly,
  markSessionInterrupted,
  resolveParentSessionID,
  resolveRootSessionID,
  resolveSubagentTitle,
  resumeSubagentTask,
  selectSession,
  sessionDirectory,
  workspace,
} from "../../store/workspace";
import { DiffChanges, Spinner } from "../icons";
import { t } from "../../i18n";

export { resolveParentSessionID, resolveRootSessionID };
import {
  extractCommand,
  extractDescription,
  extractTargetFile,
  formatToolDisplayName,
  getFilename,
} from "./tool-card-helpers";
import { collectCycleChangedFiles } from "./tool-card-state";

export interface SubagentStackItem {
  sessionID: string;
  description: string;
  /** Display title; "Subtask N" when the description only repeats the parent's title. */
  title: string;
  /** Full original title, for tooltips. */
  titleTooltip: string;
  agentType: string;
  status: "running" | "interrupted" | "completed" | "error";
  needsAttention: boolean;
  stepCount: number;
  currentStepLabel?: string;
  changedFilesCount: number;
  additions: number;
  deletions: number;
}

function summarizeSubagentTelemetry(subId: string) {
  const childTurns = workspace.messagesBySession[subId] ?? [];
  const tools: ToolPart[] = [];
  for (const turn of childTurns) {
    for (const part of turn.parts ?? []) {
      if (part.type === "tool") tools.push(part as ToolPart);
    }
  }
  const activeTool =
    [...tools]
      .reverse()
      .find((tp) => tp.state.status === "running" || tp.state.status === "pending") ??
    tools[tools.length - 1];

  let currentStepLabel: string | undefined;
  if (activeTool) {
    const file = extractTargetFile(activeTool);
    const cmd = extractCommand(activeTool);
    const desc = extractDescription(activeTool);
    const detail = file ? getFilename(file) : cmd ? `$ ${cmd}` : desc || "";
    const toolLabel = formatToolDisplayName(activeTool.tool);
    currentStepLabel = detail ? `${toolLabel} ${detail}` : toolLabel;
  }

  const changed = collectCycleChangedFiles(childTurns, subId);
  let additions = 0;
  let deletions = 0;
  for (const d of changed) {
    additions += d.additions ?? 0;
    deletions += d.deletions ?? 0;
  }

  return {
    stepCount: tools.length,
    currentStepLabel,
    changedFilesCount: changed.length,
    additions,
    deletions,
  };
}

/**
 * Collects all child subagents spawned from `parentID`.
 * Liveness is derived strictly from `statusBySession` (Kilo `background-agents.ts` pattern)
 * so stale `running` parts after an interrupted parent task are surfaced as `"interrupted"` (resumable).
 */
export function collectSubagentsForSession(parentID: string | null): SubagentStackItem[] {
  if (!parentID) return [];
  const map = new Map<string, SubagentStackItem>();
  const isViewingChild =
    Boolean(workspace.activeSessionID) && workspace.activeSessionID !== parentID;

  // 1. Scan `task` tool parts in the parent session (current cycle unless viewing a child session)
  const allTurns = workspace.messagesBySession[parentID] ?? [];
  let startIdx = 0;
  if (!isViewingChild && allTurns.length > 0) {
    for (let i = allTurns.length - 1; i >= 0; i--) {
      if (allTurns[i].info.role === "user") {
        startIdx = i;
        break;
      }
    }
  }
  const turns = allTurns.slice(startIdx);

  for (const turn of turns) {
    for (const part of turn.parts ?? []) {
      if (part.type !== "tool") continue;
      const tp = part as ToolPart;
      if ((tp.tool || "").toLowerCase() !== "task") continue;
      const subId = extractChildSubagentSessionId(tp);
      if (!subId) continue;

      ensureChildSubagentSessionLoaded(subId, parentID);

      const alive = isSubagentToolAlive(tp, parentID);
      const interrupted = isSubagentToolInterrupted(tp, parentID);
      const rawStatus = tp.state.status;
      const status: SubagentStackItem["status"] = alive
        ? "running"
        : interrupted
          ? "interrupted"
          : rawStatus === "error"
            ? "error"
            : "completed";

      const needsAttention =
        workspace.pendingPermissions.some((p) => p.sessionID === subId) ||
        workspace.pendingQuestions.some((q) => q.sessionID === subId);

      const telemetry = summarizeSubagentTelemetry(subId);
      const description = String(tp.state.input?.description || tp.state.title || "Subagent Task");
      const title = resolveSubagentTitle(parentID, subId, description);
      map.set(subId, {
        sessionID: subId,
        description,
        title: title.label,
        titleTooltip: title.full,
        agentType: String(tp.state.input?.subagent_type || "general"),
        status,
        needsAttention,
        ...telemetry,
      });
    }
  }

  // 2. Also include active/attention/viewed child sessions whose `parentID === parentID`
  for (const s of workspace.sessions) {
    if (s.parentID !== parentID || map.has(s.id)) continue;
    const childBusy = isSessionBusy(s.id);
    const childInterrupted = Boolean(workspace.interruptedBySession[s.id]);
    const needsAttention =
      workspace.pendingPermissions.some((p) => p.sessionID === s.id) ||
      workspace.pendingQuestions.some((q) => q.sessionID === s.id);
    if (!isViewingChild && !childBusy && !childInterrupted && !needsAttention) continue;
    ensureChildSubagentSessionLoaded(s.id, parentID);
    const telemetry = summarizeSubagentTelemetry(s.id);
    const title = resolveSubagentTitle(parentID, s.id, s.title || s.id.slice(0, 10));
    map.set(s.id, {
      sessionID: s.id,
      description: title.full,
      title: title.label,
      titleTooltip: title.full,
      agentType: "subagent",
      status: childBusy ? "running" : childInterrupted ? "interrupted" : "completed",
      needsAttention,
      ...telemetry,
    });
  }

  const rank = (st: SubagentStackItem["status"]) =>
    st === "running" ? 0 : st === "interrupted" ? 1 : 2;
  return Array.from(map.values()).sort((a, b) => rank(a.status) - rank(b.status));
}

/**
 * Sticky top breadcrumb & return bar rendered when the user is viewing a child subagent session.
 * Provides 1-click return to the parent session, live status, and sibling subagent switching.
 */
export function SubagentSessionBanner() {
  const [stopping, setStopping] = createSignal(false);

  const activeID = () => workspace.activeSessionID;
  const parentID = createMemo(() => resolveParentSessionID(activeID()));
  const rootID = createMemo(() => resolveRootSessionID(activeID()));

  // Ensure parent session messages are loaded so sibling subagents & metadata are populated
  createEffect(() => {
    const pid = parentID();
    if (!pid) return;
    const existing = workspace.messagesBySession[pid];
    if (!existing || existing.length === 0) {
      void loadSessionMessagesOnly(pid);
    }
  });

  const subagents = createMemo(() => collectSubagentsForSession(parentID() || rootID()));
  const currentSub = createMemo(() =>
    subagents().find((s) => s.sessionID === activeID())
  );
  const currentIndex = createMemo(() =>
    subagents().findIndex((s) => s.sessionID === activeID())
  );

  // The header shows the main session's title, so the back button only names
  // the parent when it is an intermediate subagent rather than the main session.
  const backLabel = createMemo(() => {
    const pid = parentID();
    const info = pid && pid !== rootID() ? workspace.sessions.find((s) => s.id === pid) : undefined;
    return info && !isPlaceholderSessionTitle(info.title)
      ? resolveSubagentTitle(resolveParentSessionID(pid), pid).label
      : t("timeline.backToMainSession");
  });

  const currentTitle = createMemo(() => {
    const sub = currentSub();
    if (sub) return { label: sub.title, full: sub.titleTooltip };
    return resolveSubagentTitle(parentID(), activeID());
  });

  const isRunning = createMemo(
    () => currentSub()?.status === "running" || isSessionBusy(activeID())
  );

  const switchRelative = (delta: number) => {
    const list = subagents();
    const idx = currentIndex();
    if (list.length <= 1 || idx < 0) return;
    const nextIdx = (idx + delta + list.length) % list.length;
    void selectSession(list[nextIdx].sessionID);
  };

  const stopCurrentSubagent = async () => {
    const id = activeID();
    if (!id || stopping()) return;
    setStopping(true);
    markSessionInterrupted(id, "stopped");
    try {
      await hoxiAPI.abortSession(id, sessionDirectory(id));
    } catch (err) {
      console.error("abort subagent failed:", err);
    } finally {
      setStopping(false);
    }
  };

  const resumeCurrentSubagent = async () => {
    const pid = parentID();
    const id = activeID();
    const sub = currentSub();
    if (!pid || !id) return;
    await selectSession(pid);
    await resumeSubagentTask({
      parentSessionID: pid,
      childSessionID: id,
      agentType: sub?.agentType,
      description: sub?.description,
    });
  };

  return (
    <Show when={parentID()}>
      {(pid) => (
        <div class="flex items-center justify-between gap-3 border-b border-[var(--accent-border)]/60 bg-[var(--bg-elevated)]/95 px-5 py-2 backdrop-blur-md shadow-xs">
          {/* Left: Back to Main Session Button + Breadcrumb + Subagent Info */}
          <div class="flex items-center gap-2 min-w-0 flex-1 text-[12px]">
            <button
              type="button"
              onClick={() => void selectSession(pid())}
              class="inline-flex items-center gap-1.5 rounded-lg border border-[var(--accent-border)] bg-[var(--accent-surface)] px-2.5 py-1 text-[12px] font-medium text-[var(--accent-primary)] hover:opacity-90 transition-opacity shrink-0 cursor-pointer"
            >
              <ArrowLeft size={12.5} />
              <span class="truncate max-w-[160px]">{backLabel()}</span>
            </button>

            <span class="text-[var(--text-faint)] shrink-0">/</span>

            <div class="flex items-center gap-1.5 min-w-0">
              <Show
                when={isRunning()}
                fallback={
                  <CheckCircle2
                    size={13}
                    class={
                      currentSub()?.status === "error"
                        ? "text-[var(--danger-text)] shrink-0"
                        : currentSub()?.status === "interrupted"
                          ? "text-[var(--warn-text)] shrink-0"
                          : "text-[var(--ok-text)] shrink-0"
                    }
                  />
                }
              >
                <span class="text-[var(--accent-primary)] shrink-0 inline-flex items-center">
                  <Spinner class="w-3.5 h-3.5" />
                </span>
              </Show>

              <span class="rounded bg-[var(--bg-inset)] px-1.5 py-0.5 font-mono text-[11px] text-[var(--text-muted)] shrink-0">
                {currentSub()?.agentType || "subagent"}
              </span>

              <span
                class="font-medium text-[var(--text-primary)] truncate"
                title={currentTitle().full}
              >
                {currentTitle().label}
              </span>

              <Show
                when={
                  currentSub() &&
                  (currentSub()!.additions > 0 || currentSub()!.deletions > 0)
                }
              >
                <span class="shrink-0">
                  <DiffChanges
                    changes={{
                      additions: currentSub()!.additions,
                      deletions: currentSub()!.deletions,
                    }}
                  />
                </span>
              </Show>
            </div>
          </div>

          {/* Right: Sibling Subagent Quick Switcher & Resume / Stop Button */}
          <div class="flex items-center gap-1.5 shrink-0">
            <Show when={subagents().length > 1 && currentIndex() >= 0}>
              <div class="inline-flex items-center gap-0.5 rounded-lg border border-[var(--border-subtle)] bg-[var(--bg-inset)]/80 p-0.5 text-[11px]">
                <button
                  type="button"
                  onClick={() => switchRelative(-1)}
                  class="rounded px-1 py-0.5 text-[var(--text-muted)] hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)] transition-colors cursor-pointer"
                  title={t("timeline.prevSubagentTooltip")}
                >
                  <ChevronLeft size={12} />
                </button>
                <span class="px-1.5 font-mono text-[11px] text-[var(--text-secondary)] tabular-nums">
                  {currentIndex() + 1} / {subagents().length}
                </span>
                <button
                  type="button"
                  onClick={() => switchRelative(1)}
                  class="rounded px-1 py-0.5 text-[var(--text-muted)] hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)] transition-colors cursor-pointer"
                  title={t("timeline.nextSubagentTooltip")}
                >
                  <ChevronRight size={12} />
                </button>
              </div>
            </Show>

            <Show when={!isRunning() && currentSub()?.status === "interrupted"}>
              <button
                type="button"
                onClick={() => void resumeCurrentSubagent()}
                class="inline-flex items-center gap-1 rounded-lg border border-[var(--accent-border)] bg-[var(--accent-surface)] px-2.5 py-1 text-[11px] font-medium text-[var(--accent-primary)] hover:opacity-90 transition-opacity cursor-pointer"
                title={t("timeline.resumeSubagentTooltip")}
              >
                <RotateCcw size={11} />
                <span>{t("timeline.resumeSubagentBtn")}</span>
              </button>
            </Show>

            <Show when={isRunning()}>
              <button
                type="button"
                disabled={stopping()}
                onClick={() => void stopCurrentSubagent()}
                class="inline-flex items-center gap-1 rounded-lg bg-[var(--danger-surface)] px-2 py-1 text-[11px] font-medium text-[var(--danger-text)] hover:opacity-90 transition-opacity cursor-pointer"
                title={t("timeline.stopSubagentTooltip")}
              >
                <Show
                  when={!stopping()}
                  fallback={<LoaderCircle size={11} class="animate-spin" />}
                >
                  <Square size={9.5} class="fill-current" />
                </Show>
                <span>{t("timeline.stop")}</span>
              </button>
            </Show>
          </div>
        </div>
      )}
    </Show>
  );
}
