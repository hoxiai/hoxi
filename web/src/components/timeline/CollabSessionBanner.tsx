// web/src/components/timeline/CollabSessionBanner.tsx
// Slim top context banner shown when viewing a remote PM / QA collaborative session on desktop.
// Surfaces collaborator origin, workspace isolation (Worktree vs Direct Local), restricted paths,
// and quick access to diffs / tasks without cluttering ordinary coding sessions.
import { createMemo, Show } from "solid-js";
import {
  AlertTriangle,
  Bug,
  GitBranch,
  ListTodo,
  Lock,
  ShieldCheck,
  Sparkles,
} from "lucide-solid";
import {
  resolveCollabSessionMeta,
  resolveParentSessionID,
  setWorkspace,
  workspace,
} from "../../store/workspace";
import { t } from "../../i18n";
import { isWorkSurface } from "../layout/surface-extension";

export function CollabSessionBanner() {
  const activeID = () => workspace.activeSessionID;
  const isChildSubagent = createMemo(() => Boolean(resolveParentSessionID(activeID())));

  const meta = createMemo(() => {
    const id = activeID();
    if (!id || isChildSubagent()) return null;
    const session = workspace.sessions.find((s) => s.id === id);
    const turns = workspace.messagesBySession[id] ?? [];
    const wt = workspace.worktreeBySession[id];
    return resolveCollabSessionMeta(session, turns, wt);
  });

  const openInspectorTab = (tab: "diffs" | "tasks") => {
    setWorkspace("inspectorOpen", true);
    setWorkspace("inspectorTab", tab);
  };

  return (
    <Show when={meta()}>
      {(info) => {
        const isPM = () => info().role === "pm";
        const isDirect = () => info().workspace === "local";
        return (
          <div
            data-collab-session-banner={info().role}
            class={`flex items-center justify-between gap-2.5 border-b px-5 py-1.5 backdrop-blur-md text-[12px] ${
              isPM()
                ? "border-[var(--violet-border)]/50 bg-[var(--bg-elevated)]/90"
                : "border-[var(--warn-border)]/50 bg-[var(--bg-elevated)]/90"
            }`}
          >
            {/* Left: Role badge, collaborator label, workspace isolation, restricted paths */}
            <div class="flex items-center gap-2 min-w-0 flex-1 overflow-hidden">
              <span
                class={`inline-flex shrink-0 items-center gap-1 rounded-md border px-2 py-0.5 text-[11px] font-semibold leading-none ${
                  isPM()
                    ? "border-[var(--violet-border)] bg-[var(--violet-surface)] text-[var(--violet-text)]"
                    : "border-[var(--warn-border)] bg-[var(--warn-surface)] text-[var(--warn-text)]"
                }`}
              >
                <Show when={isPM()} fallback={<Bug size={11} class="shrink-0" />}>
                  <Sparkles size={11} class="shrink-0" />
                </Show>
                <span>
                  {isPM() ? t("timeline.collabBannerPM") : t("timeline.collabBannerQA")}
                </span>
              </span>

              <Show when={info().author}>
                <span class="truncate max-w-[140px] rounded bg-[var(--bg-inset)] px-1.5 py-0.5 text-[11px] text-[var(--text-secondary)]">
                  {info().author}
                </span>
              </Show>

              <Show when={info().workspace}>
                <span
                  class={`inline-flex shrink-0 items-center gap-1 rounded-md border px-1.5 py-0.5 text-[11px] font-medium leading-none ${
                    isDirect()
                      ? "border-[var(--warn-border)] bg-[var(--warn-surface)]/80 text-[var(--warn-text)]"
                      : "border-[var(--border-subtle)] bg-[var(--bg-inset)] text-[var(--text-secondary)]"
                  }`}
                >
                  <Show when={isDirect()} fallback={<GitBranch size={10.5} class="shrink-0" />}>
                    <AlertTriangle size={10.5} class="shrink-0" />
                  </Show>
                  <span>
                    {isDirect()
                      ? t("timeline.collabWorkspaceDirect")
                      : t("timeline.collabWorkspaceWorktree")}
                  </span>
                  <Show when={!isDirect() && info().branch}>
                    <span class="max-w-[110px] truncate font-mono text-[10.5px] text-[var(--text-muted)]">
                      · {info().branch}
                    </span>
                  </Show>
                </span>
              </Show>

              <Show when={info().deniedPaths.length > 0}>
                <span
                  title={`${t("timeline.collabDeniedPathsLabel")}: ${info().deniedPaths.join(", ")}`}
                  class="hidden md:inline-flex shrink-0 items-center gap-1 rounded bg-[var(--bg-inset)] px-1.5 py-0.5 text-[10.5px] text-[var(--text-muted)]"
                >
                  <Lock size={10} class="shrink-0" />
                  <span>
                    {t("timeline.collabDeniedPathsLabel")} ({info().deniedPaths.length})
                  </span>
                </span>
              </Show>
            </div>

            {/* Right: Security isolation indicator & quick inspector actions */}
            <div class="flex shrink-0 items-center gap-1.5">
              <span
                title={t("timeline.collabBannerIsolationTooltip")}
                class="hidden sm:inline-flex items-center gap-1 text-[11px] text-[var(--text-muted)] select-none"
              >
                <ShieldCheck size={12} class="shrink-0 text-[var(--ok-text)]" />
                <span>{t("timeline.collabBannerIsolationNote")}</span>
              </span>

              <Show when={info().mode === "build" && !isWorkSurface()}>
                <button
                  type="button"
                  onClick={() => openInspectorTab("diffs")}
                  class="inline-flex items-center gap-1 rounded-md border border-[var(--border-subtle)] bg-[var(--bg-panel)] px-2 py-0.5 text-[11px] font-medium text-[var(--text-secondary)] hover:border-[var(--accent-border)] hover:text-[var(--text-primary)] transition-colors cursor-pointer"
                >
                  <span>{t("timeline.collabBannerOpenDiffs")}</span>
                </button>
              </Show>

              <button
                type="button"
                onClick={() => openInspectorTab("tasks")}
                class="inline-flex items-center gap-1 rounded-md border border-[var(--border-subtle)] bg-[var(--bg-panel)] px-2 py-0.5 text-[11px] font-medium text-[var(--text-secondary)] hover:border-[var(--accent-border)] hover:text-[var(--text-primary)] transition-colors cursor-pointer"
              >
                <ListTodo size={11} class="shrink-0" />
                <span>{t("timeline.collabBannerOpenTasks")}</span>
              </button>
            </div>
          </div>
        );
      }}
    </Show>
  );
}
