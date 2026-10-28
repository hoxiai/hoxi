// web/src/components/timeline/ComposerShelf.tsx
import { createEffect, createMemo, createSignal, For, onCleanup, Show } from "solid-js";
import {
  ChevronDown,
  ChevronUp,
  ExternalLink,
  GitMerge,
  LoaderCircle,
  MessageSquareShare,
  MessageSquareText,
  Workflow,
  X,
} from "lucide-solid";
import {
  buildConvoyCompletionSummaryPrompt,
  clearReviewComments,
  dismissSessionSpawnedConvoyPlan,
  fetchConvoyPlan,
  getSessionSpawnedConvoyPlans,
  isConvoyPlanTerminal,
  isSessionBusy,
  mergeConvoyPlan,
  openFileInInspector,
  recordSessionConvoyPlan,
  removeReviewComment,
  resolveRootSessionID,
  selectSession,
  setWorkspace,
  submitSessionPrompt,
  workspace,
} from "../../store/workspace";
import ApprovalGateCard from "../approval/ApprovalGateCard";
import QuestionDock from "../approval/QuestionDock";
import { FileIcon } from "../icons";
import ChangedFilesSummary from "./ChangedFilesSummary";
import PlanFollowupBar from "./PlanFollowupBar";
import RevertBanner from "./RevertBanner";
import TaskProgressCard from "./TaskProgressCard";
import { collectSessionChangedFiles } from "./tool-card-state";
import { filterVisibleTurnsByRevert } from "./turn-window";
import { t } from "../../i18n";

export default function ComposerShelf() {
  const [reviewsExpanded, setReviewsExpanded] = createSignal(false);
  const [subtasksExpanded, setSubtasksExpanded] = createSignal(false);
  const [mergingPlanID, setMergingPlanID] = createSignal<string | null>(null);
  let reviewContainerRef: HTMLDivElement | undefined;
  let subtasksContainerRef: HTMLDivElement | undefined;

  const sessionID = () => workspace.activeSessionID;

  const spawnedPlans = createMemo(() => getSessionSpawnedConvoyPlans(sessionID()));
  const activeSpawnedPlans = createMemo(() =>
    spawnedPlans().filter((p) => !isConvoyPlanTerminal(p.status))
  );

  // Poll active spawned convoy plans for the current session
  createEffect(() => {
    const sid = sessionID();
    const active = activeSpawnedPlans();
    if (!sid || active.length === 0) return;
    let disposed = false;
    const timer = setInterval(() => {
      for (const p of active) {
        fetchConvoyPlan(p.id)
          .then((next) => {
            if (!disposed) recordSessionConvoyPlan(sid, next);
          })
          .catch(() => {});
      }
    }, 1500);
    onCleanup(() => {
      disposed = true;
      clearInterval(timer);
    });
  });

  const activeSession = createMemo(() => {
    const id = sessionID();
    return id ? workspace.sessions.find((s) => s.id === id) : undefined;
  });

  const visibleTurns = createMemo(() => {
    const id = sessionID();
    const turns = id ? workspace.messagesBySession[id] ?? [] : [];
    return filterVisibleTurnsByRevert(turns, activeSession()?.revert);
  });

  // Transient chip: it tracks the live transcript only while the agent is
  // working and clears once the turn settles. The durable commit affordance
  // lives on the latest per-turn "view changes" card instead.
  const changedFiles = createMemo(() => {
    const id = sessionID();
    if (!id || !isSessionBusy(id)) return [];
    return collectSessionChangedFiles(visibleTurns(), id);
  });

  const pendingPermissions = createMemo(() => {
    const id = sessionID();
    if (!id) return [];
    return workspace.pendingPermissions.filter(
      (p) => p.sessionID === id || resolveRootSessionID(p.sessionID) === id
    );
  });

  const pendingQuestions = createMemo(() => {
    const id = sessionID();
    if (!id) return [];
    return workspace.pendingQuestions.filter(
      (q) => q.sessionID === id || resolveRootSessionID(q.sessionID) === id
    );
  });

  const reviewComments = createMemo(() => {
    const id = sessionID();
    return id ? (workspace.reviewCommentsBySession[id] ?? []) : [];
  });

  const reviewFileCount = createMemo(
    () => new Set(reviewComments().map((c) => c.file)).size
  );

  createEffect(() => {
    if (!reviewsExpanded()) return;
    const onPointerDown = (e: PointerEvent) => {
      if (reviewContainerRef && !reviewContainerRef.contains(e.target as Node)) {
        setReviewsExpanded(false);
      }
    };
    const onKeyDown = (e: KeyboardEvent) => {
      if (e.key === "Escape") {
        e.preventDefault();
        e.stopPropagation();
        setReviewsExpanded(false);
      }
    };
    document.addEventListener("pointerdown", onPointerDown, true);
    document.addEventListener("keydown", onKeyDown, { capture: true });
    onCleanup(() => {
      document.removeEventListener("pointerdown", onPointerDown, true);
      document.removeEventListener("keydown", onKeyDown, { capture: true });
    });
  });

  createEffect(() => {
    if (!subtasksExpanded()) return;
    const onPointerDown = (e: PointerEvent) => {
      if (subtasksContainerRef && !subtasksContainerRef.contains(e.target as Node)) {
        setSubtasksExpanded(false);
      }
    };
    document.addEventListener("pointerdown", onPointerDown, true);
    onCleanup(() => document.removeEventListener("pointerdown", onPointerDown, true));
  });

  return (
    <>
      <Show when={workspace.sessionSetupError}>
        {(message) => (
          <div role="alert" class="mb-2 flex items-start gap-2 rounded-xl border border-[var(--danger-border)] bg-[var(--danger-surface)] px-3 py-2 text-xs text-[var(--danger-text)]">
            <span class="flex-1 whitespace-pre-wrap break-words">{message()}</span>
            <button type="button" aria-label={t("common.close")}
              class="shrink-0 cursor-pointer" onClick={() => setWorkspace("sessionSetupError", null)}>
              <X size={14} />
            </button>
          </div>
        )}
      </Show>
      {/* Kilo QuestionDock: Docked Interactive Questions (question.asked) */}
      <Show when={pendingQuestions().length > 0}>
        <div class="mb-2.5 space-y-2">
          <For each={pendingQuestions()}>
            {(request) => <QuestionDock request={request} />}
          </For>
        </div>
      </Show>

      {/* Kilo SessionDock: Docked Permission Approvals (Never scrolls out of view) */}
      <Show when={pendingPermissions().length > 0}>
        <div class="mb-2.5 space-y-2">
          <For each={pendingPermissions()}>
            {(request) => <ApprovalGateCard request={request} />}
          </For>
        </div>
      </Show>

      {/* Context chips share one shelf with the compact task entry on the right. */}
      <div class="mb-2 flex items-center gap-2">
        <div class="flex min-w-0 flex-1 items-center justify-center gap-2 flex-wrap empty:hidden">
          <RevertBanner />
          <PlanFollowupBar />
          <ChangedFilesSummary files={changedFiles()} variant="pill" />

          <Show when={spawnedPlans().length > 0}>
            <div ref={subtasksContainerRef} class="relative inline-flex items-center">
              <div class="inline-flex items-center gap-1.5 rounded-full border border-[var(--accent-border)] bg-[var(--accent-surface)]/45 px-2.5 py-1 text-[12px]">
                <button
                  type="button"
                  onClick={() => setSubtasksExpanded((v) => !v)}
                  class="inline-flex items-center gap-1.5 text-left cursor-pointer"
                >
                  <Show
                    when={activeSpawnedPlans().length > 0}
                    fallback={<Workflow size={12.5} class="text-[var(--ok-text)] shrink-0" />}
                  >
                    <LoaderCircle size={12.5} class="animate-spin text-[var(--accent-primary)] shrink-0" />
                  </Show>
                  <span class="font-medium text-[var(--text-primary)]">
                    {activeSpawnedPlans().length > 0
                      ? t("timeline.spawnedSubtasksShelfPill", {
                          running: activeSpawnedPlans().reduce(
                            (acc, p) => acc + p.tasks.filter((t) => t.status === "running").length,
                            0
                          ) || activeSpawnedPlans().length,
                          total: spawnedPlans().reduce((acc, p) => acc + p.tasks.length, 0),
                        })
                      : t("timeline.spawnedSubtasksShelfDone", {
                          succeeded: spawnedPlans().reduce(
                            (acc, p) => acc + p.tasks.filter((t) => t.status === "succeeded").length,
                            0
                          ),
                          total: spawnedPlans().reduce((acc, p) => acc + p.tasks.length, 0),
                        })}
                  </span>
                  <span class="text-[var(--text-muted)] inline-flex items-center">
                    {subtasksExpanded() ? <ChevronDown size={12} /> : <ChevronUp size={12} />}
                  </span>
                </button>
              </div>

              <Show when={subtasksExpanded()}>
                <div class="absolute left-1/2 -translate-x-1/2 bottom-[calc(100%+6px)] z-40 w-[min(460px,calc(100vw-32px))] rounded-xl border border-[var(--accent-border)] bg-[var(--bg-elevated)]/98 backdrop-blur-xl p-2.5 shadow-2xl space-y-2 max-h-64 overflow-y-auto">
                  <For each={spawnedPlans()}>
                    {(plan) => (
                      <div class="rounded-lg border border-[var(--border-subtle)] bg-[var(--bg-panel)]/85 p-2 space-y-1.5 text-[12px]">
                        <div class="flex items-center justify-between gap-2">
                          <span class="font-semibold text-[var(--text-primary)] truncate">
                            {plan.title}
                          </span>
                          <div class="flex items-center gap-1 shrink-0">
                            <Show
                              when={
                                plan.status === "succeeded" &&
                                plan.isolated &&
                                !plan.mergedAt &&
                                sessionID()
                              }
                            >
                              <button
                                type="button"
                                disabled={mergingPlanID() === plan.id}
                                onClick={() => {
                                  const sid = sessionID();
                                  if (!sid) return;
                                  setMergingPlanID(plan.id);
                                  mergeConvoyPlan(plan.id, sid)
                                    .then((updated) => recordSessionConvoyPlan(sid, updated))
                                    .catch(() => {})
                                    .finally(() => setMergingPlanID(null));
                                }}
                                class="inline-flex items-center gap-1 rounded bg-[var(--accent-surface)] px-1.5 py-0.5 text-[11px] font-medium text-[var(--accent-primary)] hover:opacity-90 cursor-pointer"
                              >
                                <GitMerge size={10} />
                                <span>{t("timeline.inlineSubtasksMergeBtn")}</span>
                              </button>
                            </Show>
                            <Show when={isConvoyPlanTerminal(plan.status) && sessionID()}>
                              <button
                                type="button"
                                onClick={() => {
                                  const sid = sessionID();
                                  if (!sid) return;
                                  setSubtasksExpanded(false);
                                  void submitSessionPrompt(buildConvoyCompletionSummaryPrompt(plan), sid);
                                }}
                                class="inline-flex items-center gap-1 rounded bg-[var(--accent-primary)] px-1.5 py-0.5 text-[11px] font-medium text-[var(--accent-foreground)] hover:opacity-90 cursor-pointer"
                              >
                                <MessageSquareShare size={10} />
                                <span>{t("timeline.inlineSubtasksSummarizeBtn")}</span>
                              </button>
                              <button
                                type="button"
                                onClick={() => dismissSessionSpawnedConvoyPlan(sessionID(), plan.id)}
                                class="p-0.5 text-[var(--text-muted)] hover:text-[var(--text-primary)] cursor-pointer"
                                title={t("common.close")}
                              >
                                <X size={12} />
                              </button>
                            </Show>
                          </div>
                        </div>
                        <For each={plan.tasks}>
                          {(task) => (
                            <div class="flex items-center justify-between gap-2 text-[11px] text-[var(--text-secondary)]">
                              <span class="truncate font-mono">
                                {task.id}: {task.summary || task.prompt}
                              </span>
                              <Show when={task.sessionID}>
                                <button
                                  type="button"
                                  onClick={() => {
                                    setSubtasksExpanded(false);
                                    void selectSession(task.sessionID!);
                                  }}
                                  class="shrink-0 inline-flex items-center gap-1 text-[var(--accent-primary)] hover:underline cursor-pointer"
                                >
                                  <ExternalLink size={10} />
                                  <span>{t("timeline.convoyOpenSession")}</span>
                                </button>
                              </Show>
                            </div>
                          )}
                        </For>
                      </div>
                    )}
                  </For>
                </div>
              </Show>
            </div>
          </Show>

          {/* Queued Inline Diff ReviewComments Pill + Upward Popover */}
          <Show when={reviewComments().length > 0}>
            <div ref={reviewContainerRef} class="relative inline-flex items-center">
              <div class="inline-flex items-center gap-1.5 rounded-full border border-[var(--accent-border)] bg-[var(--accent-surface)]/45 px-2.5 py-1 text-[12px]">
                <button
                  type="button"
                  class="inline-flex items-center gap-1.5 text-left cursor-pointer"
                  onClick={() => setReviewsExpanded((v) => !v)}
                >
                  <MessageSquareText size={12.5} class="text-[var(--accent-primary)] shrink-0" />
                  <span class="font-semibold text-[var(--text-primary)]">
                    {reviewComments().length === 1
                      ? t("timeline.reviewCommentSingular", { count: reviewComments().length })
                      : t("timeline.reviewCommentPlural", { count: reviewComments().length })}
                  </span>
                  <Show when={reviewFileCount() > 1}>
                    <span class="text-[11px] text-[var(--text-muted)]">
                      {t("timeline.filesCount", { count: reviewFileCount() })}
                    </span>
                  </Show>
                  <span class="text-[var(--text-muted)] inline-flex items-center">
                    {reviewsExpanded() ? <ChevronDown size={12} /> : <ChevronUp size={12} />}
                  </span>
                </button>

                <button
                  type="button"
                  class="text-[11px] text-[var(--text-muted)] hover:text-[var(--text-primary)] cursor-pointer ml-1"
                  onClick={() => {
                    const id = sessionID();
                    if (id) clearReviewComments(id);
                  }}
                >
                  {t("timeline.clearAll")}
                </button>
              </div>

              <Show when={reviewsExpanded()}>
                <div class="absolute left-1/2 -translate-x-1/2 bottom-[calc(100%+6px)] z-40 w-[min(480px,calc(100vw-32px))] rounded-xl border border-[var(--accent-border)] bg-[var(--bg-elevated)]/98 backdrop-blur-xl p-2.5 shadow-2xl animate-in fade-in zoom-in-95 duration-150">
                  <div class="space-y-1 max-h-48 overflow-y-auto">
                    <For each={reviewComments()}>
                      {(item) => (
                        <div class="flex items-center justify-between gap-2 rounded-lg bg-[var(--bg-panel)]/80 px-2.5 py-1.5 text-[12px]">
                          <div class="flex items-center gap-2 min-w-0 flex-1">
                            <button
                              type="button"
                              onClick={() => openFileInInspector(item.file)}
                              class="inline-flex items-center gap-1.5 font-mono text-[11px] font-medium text-[var(--accent-text)] hover:underline shrink-0 cursor-pointer"
                              title={t("timeline.openInInspectorTooltip")}
                            >
                              <FileIcon
                                node={{ path: item.file, type: "file" }}
                                class="w-3.5 h-3.5 shrink-0"
                              />
                              <span>
                                {item.file}
                                {item.line ? `:${item.line}` : ""}
                              </span>
                              <ExternalLink size={10} />
                            </button>
                            <span class="text-[var(--text-secondary)] truncate">{item.body}</span>
                          </div>
                          <button
                            type="button"
                            onClick={() => {
                              const id = sessionID();
                              if (id) removeReviewComment(id, item.id);
                            }}
                            class="text-[var(--text-muted)] hover:text-[var(--danger-text)] p-0.5 cursor-pointer"
                            title={t("timeline.removeCommentTooltip")}
                          >
                            <X size={12} />
                          </button>
                        </div>
                      )}
                    </For>
                  </div>
                </div>
              </Show>
            </div>
          </Show>
        </div>
        <TaskProgressCard />
      </div>
    </>
  );
}
