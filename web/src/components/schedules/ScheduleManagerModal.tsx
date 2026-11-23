// web/src/components/schedules/ScheduleManagerModal.tsx
import { createEffect, createMemo, createSignal, For, onCleanup, Show, untrack } from "solid-js";
import {
  CalendarClock,
  Check,
  ExternalLink,
  History,
  Play,
  Plus,
  SlidersHorizontal,
  Square,
  X,
} from "lucide-solid";
import { Presence } from "../motion/Presence";
import { t } from "../../i18n";
import {
  acknowledgeScheduleAlert,
  closeScheduleModal,
  formatScheduleError,
  loadScheduleRuns,
  loadSchedules,
  openScheduleRunSession,
  runScheduleNow,
  scheduleModalOpen,
  scheduleModalTarget,
  scheduleRunsByID,
  schedulesForWorkspace,
  schedulesList,
  stopScheduleRun,
  workspace,
} from "../../store/workspace";
import ScheduleEditorForm from "./ScheduleEditorForm";
import ScheduleRunHistory from "./ScheduleRunHistory";
import {
  formatScheduleTrigger,
  scheduleStatusLabel,
  scheduleStatusTone,
} from "./schedule-helpers";

export default function ScheduleManagerModal() {
  const [scope, setScope] = createSignal<"current" | "all">("current");
  const [selectedID, setSelectedID] = createSignal<string | null>(null);
  const [activeTab, setActiveTab] = createSignal<"config" | "history">("config");
  const [actionBusy, setActionBusy] = createSignal(false);
  const [actionError, setActionError] = createSignal<string | null>(null);

  const targetDir = createMemo(() => scheduleModalTarget().directory || workspace.rootDir || "");
  const targetProjectID = createMemo(() => scheduleModalTarget().projectID || workspace.activeProjectID || "");

  const visibleSchedules = createMemo(() => {
    if (scope() === "all") return schedulesList();
    return schedulesForWorkspace(targetDir(), targetProjectID());
  });

  const selectedSchedule = createMemo(() => {
    const id = selectedID();
    if (!id) return null;
    return schedulesList().find((item) => item.id === id) ?? null;
  });

  createEffect(() => {
    if (!scheduleModalOpen()) return;
    const tgt = scheduleModalTarget();
    setActionError(null);
    void loadSchedules().catch(() => {});
    if (tgt.createNew) {
      setSelectedID(null);
      setActiveTab("config");
    } else if (tgt.scheduleID) {
      setSelectedID(tgt.scheduleID);
      setActiveTab("config");
    } else {
      // Read initial schedule list untracked so 10s background polls never
      // reset the user's selected schedule, active tab, or unsaved form edits.
      const first = untrack(
        () => schedulesForWorkspace(tgt.directory || workspace.rootDir, tgt.projectID)[0]
      );
      setSelectedID(first?.id ?? null);
      setActiveTab("config");
    }
    const onKeyDown = (ev: KeyboardEvent) => {
      if (ev.key === "Escape") {
        ev.preventDefault();
        closeScheduleModal();
      }
    };
    window.addEventListener("keydown", onKeyDown);
    onCleanup(() => window.removeEventListener("keydown", onKeyDown));
  });

  createEffect(() => {
    if (!scheduleModalOpen()) return;
    const id = selectedID();
    setActionError(null);
    if (id && activeTab() === "history") {
      void loadScheduleRuns(id).catch(() => {});
    }
  });

  const runsForSelected = createMemo(() => {
    const id = selectedID();
    if (!id) return [];
    return scheduleRunsByID()[id] ?? [];
  });

  const onRunOrStop = async () => {
    const sch = selectedSchedule();
    if (!sch || actionBusy()) return;
    setActionBusy(true);
    setActionError(null);
    try {
      if (sch.running) {
        await stopScheduleRun(sch.id);
      } else {
        await runScheduleNow(sch.id);
        setActiveTab("history");
      }
    } catch (err) {
      setActionError(formatScheduleError(err, "Schedule action failed"));
    } finally {
      setActionBusy(false);
    }
  };

  return (
    <Presence when={scheduleModalOpen()}>
      {(state) => (
        <div
          data-state={state()}
          class="hoxi-modal-backdrop fixed inset-0 z-[80] flex items-center justify-center p-4"
          onClick={(ev) => {
            if (ev.target === ev.currentTarget) closeScheduleModal();
          }}
        >
          <div class="hoxi-modal-panel flex h-[min(760px,88vh)] w-[min(920px,94vw)] flex-col overflow-hidden rounded-2xl border border-[var(--border-strong)] bg-[var(--bg-elevated)] shadow-2xl">
            {/* Header */}
            <div class="flex shrink-0 items-center justify-between gap-3 border-b border-[var(--border-subtle)] px-5 py-3.5">
              <div class="flex items-center gap-2.5 min-w-0">
                <div class="flex h-8 w-8 shrink-0 items-center justify-center rounded-xl bg-[var(--accent-surface)] text-[var(--accent-primary)]">
                  <CalendarClock size={16} />
                </div>
                <div class="min-w-0">
                  <h2 class="text-[14px] font-semibold text-[var(--text-primary)] truncate">
                    {t("schedules.modalTitle")}
                  </h2>
                  <p class="text-[11px] text-[var(--text-muted)] truncate">
                    {t("schedules.modalSubtitle")}
                  </p>
                </div>
              </div>
              <button
                type="button"
                onClick={closeScheduleModal}
                class="inline-flex h-7 w-7 items-center justify-center rounded-lg text-[var(--text-muted)] hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)] cursor-pointer"
                aria-label="Close"
              >
                <X size={15} />
              </button>
            </div>

            {/* Body: Left List + Right Editor/History */}
            <div class="flex min-h-0 flex-1 overflow-hidden">
              <aside class="flex w-[260px] shrink-0 flex-col border-r border-[var(--border-subtle)] bg-[var(--bg-panel)]/60">
                <div class="flex items-center gap-1 border-b border-[var(--border-subtle)]/70 p-2">
                  <button
                    type="button"
                    onClick={() => setScope("current")}
                    class={`flex-1 rounded-lg px-2 py-1 text-[11px] font-medium cursor-pointer ${
                      scope() === "current"
                        ? "bg-[var(--accent-surface)] text-[var(--accent-primary)]"
                        : "text-[var(--text-muted)] hover:text-[var(--text-primary)]"
                    }`}
                  >
                    {t("schedules.scopeCurrent")}
                  </button>
                  <button
                    type="button"
                    onClick={() => setScope("all")}
                    class={`flex-1 rounded-lg px-2 py-1 text-[11px] font-medium cursor-pointer ${
                      scope() === "all"
                        ? "bg-[var(--accent-surface)] text-[var(--accent-primary)]"
                        : "text-[var(--text-muted)] hover:text-[var(--text-primary)]"
                    }`}
                  >
                    {t("schedules.scopeAll")}
                  </button>
                </div>

                <div class="p-2">
                  <button
                    type="button"
                    onClick={() => {
                      setSelectedID(null);
                      setActiveTab("config");
                    }}
                    class="flex w-full items-center justify-center gap-1.5 rounded-lg border border-dashed border-[var(--accent-primary)]/50 bg-[var(--accent-surface)]/40 px-2.5 py-1.5 text-[12px] font-medium text-[var(--accent-primary)] hover:bg-[var(--accent-surface)] cursor-pointer"
                  >
                    <Plus size={13} />
                    <span>{t("schedules.createNew")}</span>
                  </button>
                </div>

                <div class="min-h-0 flex-1 overflow-y-auto px-2 pb-2 space-y-1">
                  <For each={visibleSchedules()}>
                    {(item) => {
                      const active = () => selectedID() === item.id;
                      return (
                        <button
                          type="button"
                          onClick={() => {
                            setSelectedID(item.id);
                            if (activeTab() === "history") {
                              void loadScheduleRuns(item.id).catch(() => {});
                            }
                          }}
                          class={`w-full rounded-xl border px-2.5 py-2 text-left transition-colors cursor-pointer ${
                            active()
                              ? "border-[var(--accent-primary)]/60 bg-[var(--accent-surface)]/70"
                              : "border-transparent hover:bg-[var(--bg-hover)]/70"
                          }`}
                        >
                          <div class="flex items-center justify-between gap-1.5">
                            <span class="truncate text-[12px] font-medium text-[var(--text-primary)]">
                              {item.name}
                            </span>
                            <Show when={item.alertUnseen}>
                              <span class="h-1.5 w-1.5 shrink-0 rounded-full bg-[var(--status-warning)]" />
                            </Show>
                          </div>
                          <div class="mt-0.5 flex items-center justify-between gap-1 text-[10.5px] text-[var(--text-muted)]">
                            <span class="font-mono truncate">{formatScheduleTrigger(item.trigger)}</span>
                            <span class={scheduleStatusTone(item.lastOutcome, item.running)}>
                              {item.enabled
                                ? scheduleStatusLabel(item.lastOutcome, item.running)
                                : t("schedules.pausedLabel")}
                            </span>
                          </div>
                        </button>
                      );
                    }}
                  </For>
                </div>
              </aside>

              {/* Right Detail Pane */}
              <div class="flex min-w-0 flex-1 flex-col overflow-hidden">
                <Show when={selectedSchedule()}>
                  {(sch) => (
                    <div class="flex shrink-0 flex-wrap items-center justify-between gap-2 border-b border-[var(--border-subtle)] px-5 py-2.5">
                      <div class="flex items-center gap-1">
                        <button
                          type="button"
                          onClick={() => setActiveTab("config")}
                          class={`inline-flex items-center gap-1.5 rounded-lg px-2.5 py-1 text-[12px] font-medium cursor-pointer ${
                            activeTab() === "config"
                              ? "bg-[var(--accent-surface)] text-[var(--accent-primary)]"
                              : "text-[var(--text-muted)] hover:text-[var(--text-primary)]"
                          }`}
                        >
                          <SlidersHorizontal size={12} />
                          <span>{t("schedules.tabConfig")}</span>
                        </button>
                        <button
                          type="button"
                          onClick={() => {
                            setActiveTab("history");
                            void loadScheduleRuns(sch().id).catch(() => {});
                          }}
                          class={`inline-flex items-center gap-1.5 rounded-lg px-2.5 py-1 text-[12px] font-medium cursor-pointer ${
                            activeTab() === "history"
                              ? "bg-[var(--accent-surface)] text-[var(--accent-primary)]"
                              : "text-[var(--text-muted)] hover:text-[var(--text-primary)]"
                          }`}
                        >
                          <History size={12} />
                          <span>{t("schedules.tabHistory")}</span>
                        </button>
                      </div>

                      <div class="flex items-center gap-1.5">
                        <Show when={sch().alertUnseen}>
                          <button
                            type="button"
                            onClick={() => void acknowledgeScheduleAlert(sch().id).catch(() => {})}
                            class="inline-flex items-center gap-1 rounded-lg border border-[var(--status-warning)]/40 bg-[var(--status-warning)]/10 px-2.5 py-1 text-[11px] font-medium text-[var(--status-warning)] hover:bg-[var(--status-warning)]/20 cursor-pointer"
                          >
                            <Check size={11} />
                            <span>{t("schedules.ackAlertBtn")}</span>
                          </button>
                        </Show>
                        <Show when={sch().lastSessionID || sch().sessionID}>
                          <button
                            type="button"
                            onClick={() => void openScheduleRunSession(sch())}
                            class="inline-flex items-center gap-1 rounded-lg border border-[var(--border-subtle)] bg-[var(--bg-inset)] px-2.5 py-1 text-[11px] text-[var(--text-secondary)] hover:text-[var(--text-primary)] cursor-pointer"
                          >
                            <ExternalLink size={11} />
                            <span>{t("schedules.openSessionBtn")}</span>
                          </button>
                        </Show>
                        <button
                          type="button"
                          disabled={actionBusy()}
                          onClick={() => void onRunOrStop()}
                          class="inline-flex items-center gap-1 rounded-lg border border-[var(--border-subtle)] bg-[var(--bg-inset)] px-2.5 py-1 text-[11px] font-medium text-[var(--text-primary)] hover:border-[var(--accent-primary)]/60 cursor-pointer disabled:opacity-40"
                        >
                          <Show when={sch().running} fallback={<Play size={11} class="text-[var(--accent-primary)]" />}>
                            <Square size={10} class="text-[var(--danger-text)]" />
                          </Show>
                          <span>{sch().running ? t("schedules.stopBtn") : t("schedules.runNowBtn")}</span>
                        </button>
                      </div>
                    </div>
                  )}
                </Show>

                <div class="min-h-0 flex-1 overflow-y-auto px-5 py-4">
                  <Show when={actionError()}>
                    <div class="mb-3 rounded-lg border border-[var(--danger-border)]/40 bg-[var(--danger-surface)] px-3 py-2 text-[11.5px] text-[var(--danger-text)]">
                      {actionError()}
                    </div>
                  </Show>
                  <Show
                    when={activeTab() === "history" && selectedSchedule()}
                    fallback={
                      <ScheduleEditorForm
                        schedule={selectedSchedule()}
                        defaultDirectory={targetDir()}
                        defaultProjectID={targetProjectID()}
                        defaultSessionID={scheduleModalTarget().sessionID}
                        onSaved={(saved) => setSelectedID(saved.id)}
                        onDeleted={() => {
                          setSelectedID(null);
                          setActiveTab("config");
                        }}
                      />
                    }
                  >
                    {(sch) => <ScheduleRunHistory schedule={sch()} runs={runsForSelected()} />}
                  </Show>
                </div>
              </div>
            </div>
          </div>
        </div>
      )}
    </Presence>
  );
}
