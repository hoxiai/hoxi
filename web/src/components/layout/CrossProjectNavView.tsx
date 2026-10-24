// web/src/components/layout/CrossProjectNavView.tsx
// Cross-project session views (Attention / Running) for the left sidebar.
// Grouping lives in ./cross-project-nav so it stays testable without a DOM.
import { createMemo, createSelector, For, Show } from "solid-js";
import { Layers, Plus } from "lucide-solid";
import {
  normalizeFolderPath,
  openHandoffModal,
  setSidebarProjectMode,
  workspace,
} from "../../store/workspace";
import type { SessionInfo } from "../../services/sse-types";
import { t } from "../../i18n";
import { SessionNavItem } from "./SessionNavItem";
import {
  FolderIcon,
  resolveActiveRootSessionID,
  shortenPath,
  type ProjectNavEntry,
} from "./left-nav-helpers";
import {
  collectCrossProjectSessions,
  groupRunningByProject,
  partitionAttentionSessions,
  type AttentionSectionID,
  type CrossProjectSession,
} from "./cross-project-nav";

export interface CrossProjectNavProps {
  allProjects: ProjectNavEntry[];
  projectSessions: Record<string, SessionInfo[]>;
  filterQuery: string;
  pendingSessions: Set<string>;
  onSelectSession: (path: string, sessionID: string) => void;
  onArchiveSession: (path: string, ev: MouseEvent, sessionID: string) => void;
  onDiscardSession: (path: string, ev: MouseEvent, sessionID: string) => Promise<void> | void;
}

function ImportSessionFooter() {
  return (
    <div class="pt-1">
      <button
        type="button"
        onClick={() => openHandoffModal()}
        class="w-full flex items-center gap-2 rounded-lg px-2 py-1.5 text-[12px] text-[var(--text-faint)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-hover)]/60 transition-colors cursor-pointer"
        title={t("leftNav.handoffTooltip")}
      >
        <Plus size={13.5} class="shrink-0 text-[var(--accent-primary)]" />
        <span>{t("leftNav.handoffBtn")}</span>
      </button>
    </div>
  );
}

function SectionHeader(props: { label: string; count: number; title?: string; color?: string }) {
  return (
    <div
      class="flex items-center gap-1.5 px-2.5 py-1 text-[11px] font-medium text-[var(--text-faint)]"
      title={props.title}
    >
      <Show when={props.color}>
        <FolderIcon open={true} color={props.color} class="w-3 h-3 shrink-0" />
      </Show>
      <span class="truncate">{props.label}</span>
      <span class="shrink-0 rounded bg-[var(--bg-inset)] px-1.5 py-0.2 font-mono">{props.count}</span>
    </div>
  );
}

function SessionRow(props: {
  item: CrossProjectSession;
  isSelected: (id: string) => boolean;
  pendingSessions: Set<string>;
  onSelectSession: (path: string, sessionID: string) => void;
  onArchiveSession: (path: string, ev: MouseEvent, sessionID: string) => void;
  onDiscardSession: (path: string, ev: MouseEvent, sessionID: string) => Promise<void> | void;
}) {
  const isActiveProject = () =>
    normalizeFolderPath(props.item.proj.path) === normalizeFolderPath(workspace.rootDir);
  return (
    <SessionNavItem
      session={props.item.session}
      active={isActiveProject() && props.isSelected(props.item.session.id)}
      canDelete={true}
      isArchived={false}
      showBranchIcon={true}
      pendingSessions={props.pendingSessions}
      onSelect={() => props.onSelectSession(props.item.proj.path, props.item.session.id)}
      onArchive={(ev, id) => props.onArchiveSession(props.item.proj.path, ev, id)}
      onDiscard={(ev, id) => props.onDiscardSession(props.item.proj.path, ev, id)}
    />
  );
}

export function RunningNavView(props: CrossProjectNavProps) {
  const isSelectedSession = createSelector<string | null, string>(() => resolveActiveRootSessionID());
  const groups = createMemo(() =>
    groupRunningByProject(
      collectCrossProjectSessions({
        allProjects: props.allProjects,
        projectSessions: props.projectSessions,
        filterQuery: props.filterQuery,
      }),
    ),
  );

  return (
    <div class="sidebar-scroll hoxi-view-enter flex-1 min-h-0 overflow-y-auto pl-1 pr-2 py-1 space-y-2">
      <div class="flex items-center justify-between gap-2 rounded-lg bg-[var(--bg-inset)]/70 px-2.5 py-1.5 text-[11.5px]">
        <span class="font-medium text-[var(--text-secondary)] truncate">{t("leftNav.runningTasks")}</span>
        <button
          type="button"
          onClick={() => setSidebarProjectMode("multi")}
          class="inline-flex items-center gap-1 rounded px-1.5 py-0.5 text-[11px] font-medium text-[var(--accent-primary)] hover:bg-[var(--bg-hover)] transition-colors cursor-pointer shrink-0"
        >
          <Layers size={11.5} />
          <span>{t("leftNav.multiProject")}</span>
        </button>
      </div>
      <Show
        when={groups().length > 0}
        fallback={
          <div class="px-3 py-6 text-center text-[12px] text-[var(--text-faint)] space-y-2">
            <div>{t("leftNav.noRunningTasks")}</div>
            <button
              type="button"
              onClick={() => setSidebarProjectMode("multi")}
              class="inline-flex items-center gap-1.5 rounded-lg border border-[var(--border-subtle)] bg-[var(--bg-elevated)] px-2.5 py-1 text-[11.5px] text-[var(--text-secondary)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-hover)] transition-colors cursor-pointer"
            >
              <Layers size={12} />
              <span>{t("leftNav.multiProject")}</span>
            </button>
          </div>
        }
      >
        <For each={groups()}>
          {(group) => (
            <div class="space-y-0.5">
              <SectionHeader
                label={group.proj.name}
                count={group.sessions.length}
                title={shortenPath(group.proj.path)}
                color={group.proj.color}
              />
              <For each={group.sessions}>
                {(session) => (
                  <SessionRow
                    item={{ session, proj: group.proj }}
                    isSelected={isSelectedSession}
                    pendingSessions={props.pendingSessions}
                    onSelectSession={props.onSelectSession}
                    onArchiveSession={props.onArchiveSession}
                    onDiscardSession={props.onDiscardSession}
                  />
                )}
              </For>
            </div>
          )}
        </For>
      </Show>
      <ImportSessionFooter />
    </div>
  );
}

const ATTENTION_LABELS: Record<AttentionSectionID, () => string> = {
  approvals: () => t("leftNav.attentionApprovals"),
  completed: () => t("leftNav.attentionCompleted"),
  paused: () => t("leftNav.attentionPaused"),
};

export function AttentionNavView(props: CrossProjectNavProps) {
  const isSelectedSession = createSelector<string | null, string>(() => resolveActiveRootSessionID());
  const sections = createMemo(() =>
    partitionAttentionSessions(
      collectCrossProjectSessions({
        allProjects: props.allProjects,
        projectSessions: props.projectSessions,
        filterQuery: props.filterQuery,
      }),
      props.pendingSessions,
    ),
  );

  return (
    <div class="sidebar-scroll hoxi-view-enter flex-1 min-h-0 overflow-y-auto pl-1 pr-2 py-1 space-y-2">
      <div class="flex items-center justify-between gap-2 rounded-lg bg-[var(--bg-inset)]/70 px-2.5 py-1.5 text-[11.5px]">
        <span class="font-medium text-[var(--text-secondary)] truncate">{t("leftNav.attentionTasks")}</span>
        <button
          type="button"
          onClick={() => setSidebarProjectMode("multi")}
          class="inline-flex items-center gap-1 rounded px-1.5 py-0.5 text-[11px] font-medium text-[var(--accent-primary)] hover:bg-[var(--bg-hover)] transition-colors cursor-pointer shrink-0"
        >
          <Layers size={11.5} />
          <span>{t("leftNav.multiProject")}</span>
        </button>
      </div>
      <Show
        when={sections().length > 0}
        fallback={
          <div class="px-3 py-6 text-center text-[12px] text-[var(--text-faint)] space-y-2">
            <div>{t("leftNav.noAttentionTasks")}</div>
            <button
              type="button"
              onClick={() => setSidebarProjectMode("multi")}
              class="inline-flex items-center gap-1.5 rounded-lg border border-[var(--border-subtle)] bg-[var(--bg-elevated)] px-2.5 py-1 text-[11.5px] text-[var(--text-secondary)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-hover)] transition-colors cursor-pointer"
            >
              <Layers size={12} />
              <span>{t("leftNav.multiProject")}</span>
            </button>
          </div>
        }
      >
        <For each={sections()}>
          {(section) => (
            <div class="space-y-0.5">
              <SectionHeader label={ATTENTION_LABELS[section.id]()} count={section.items.length} />
              <For each={section.items}>
                {(item) => (
                  <SessionRow
                    item={item}
                    isSelected={isSelectedSession}
                    pendingSessions={props.pendingSessions}
                    onSelectSession={props.onSelectSession}
                    onArchiveSession={props.onArchiveSession}
                    onDiscardSession={props.onDiscardSession}
                  />
                )}
              </For>
            </div>
          )}
        </For>
      </Show>
      <ImportSessionFooter />
    </div>
  );
}
