// web/src/components/layout/CrossProjectNavView.tsx
// Cross-project session views (Recent / Attention / Running) for the left sidebar.
// Grouping and windowing live in ./cross-project-nav so they stay testable without a DOM.
import { createMemo, createSelector, createSignal, For, Show } from "solid-js";
import { Layers, Plus } from "lucide-solid";
import {
  normalizeFolderPath,
  openHandoffModal,
  sessionActivityTime,
  setSidebarProjectMode,
  switchSidebarNavTab,
  workspace,
} from "../../store/workspace";
import type { SessionInfo } from "../../services/sse-types";
import { t } from "../../i18n";
import { Spinner } from "../icons";
import { SessionNavItem } from "./SessionNavItem";
import { COLLAPSED_SECTION_PAGE_SIZE } from "./session-history";
import {
  dayLabel,
  FolderIcon,
  resolveActiveRootSessionID,
  shortenPath,
  type ProjectNavEntry,
} from "./left-nav-helpers";
import {
  collectCrossProjectSessions,
  groupRunningByProject,
  partitionAttentionSessions,
  selectRecentCrossProjectSessions,
  type AttentionSectionID,
  type CrossProjectSession,
} from "./cross-project-nav";
import { useNowTicker } from "./session-time-helpers";

export interface CrossProjectNavProps {
  allProjects: ProjectNavEntry[];
  projectSessions: Record<string, SessionInfo[]>;
  isLoading?: boolean;
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
  filterQuery: string;
  showProjectBadge?: boolean;
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
      projectName={props.showProjectBadge ? props.item.proj.name : undefined}
      projectTitle={props.showProjectBadge ? shortenPath(props.item.proj.path) : undefined}
      pendingSessions={props.pendingSessions}
      subagents={props.item.subagents}
      filterQuery={props.filterQuery}
      onSelect={() => props.onSelectSession(props.item.proj.path, props.item.session.id)}
      onSelectSubagent={(subID) => props.onSelectSession(props.item.proj.path, subID)}
      onArchive={(ev, id) => props.onArchiveSession(props.item.proj.path, ev, id)}
      onDiscard={(ev, id) => props.onDiscardSession(props.item.proj.path, ev, id)}
    />
  );
}

export function RecentTasksNavView(props: CrossProjectNavProps) {
  const now = useNowTicker();
  const activeRootID = createMemo(() => resolveActiveRootSessionID());
  const isSelectedSession = createSelector<string | null, string>(activeRootID);

  const recentSelection = createMemo(() =>
    selectRecentCrossProjectSessions(
      collectCrossProjectSessions({
        allProjects: props.allProjects,
        projectSessions: props.projectSessions,
        filterQuery: props.filterQuery,
      }),
      {
        now: now(),
        query: props.filterQuery,
        pendingSessions: props.pendingSessions,
        activeRootSessionID: activeRootID(),
      },
    ),
  );

  const recentItems = createMemo(
    () => recentSelection().items,
    [],
    {
      equals: (a, b) =>
        a.length === b.length &&
        a.every((item, i) => item.session === b[i]?.session && item.proj.path === b[i]?.proj.path),
    },
  );

  return (
    <div class="sidebar-scroll hoxi-view-enter flex-1 min-h-0 overflow-y-auto pl-1 pr-2 py-1 space-y-0.5">
      <Show
        when={recentItems().length > 0}
        fallback={
          <div class="px-3 py-6 flex items-center justify-center gap-1.5 text-center text-[12px] text-[var(--text-faint)]">
            <Show when={props.isLoading} fallback={<span>{t("leftNav.noThreads")}</span>}>
              <Spinner class="w-3.5 h-3.5" />
              <span>{t("common.loading")}</span>
            </Show>
          </div>
        }
      >
        <For each={recentItems()}>
          {(item, index) => {
            const label = createMemo(() => {
              const current = dayLabel(sessionActivityTime(item.session));
              const previous = recentItems()[index() - 1];
              const prevLabel = previous ? dayLabel(sessionActivityTime(previous.session)) : "";
              return prevLabel === current ? "" : current;
            });
            return (
              <div class={label() && index() > 0 ? "pt-1.5 space-y-0.5" : "space-y-0.5"}>
                <Show when={label()}>
                  <div class="px-2.5 py-0.5 text-[11px] font-medium text-[var(--text-faint)]">
                    {label()}
                  </div>
                </Show>
                <SessionRow
                  item={item}
                  filterQuery={props.filterQuery}
                  showProjectBadge={true}
                  isSelected={isSelectedSession}
                  pendingSessions={props.pendingSessions}
                  onSelectSession={props.onSelectSession}
                  onArchiveSession={props.onArchiveSession}
                  onDiscardSession={props.onDiscardSession}
                />
              </div>
            );
          }}
        </For>
      </Show>

      <Show when={recentSelection().hiddenCount > 0}>
        <Show
          when={recentSelection().isSearchTruncated}
          fallback={
            <button
              type="button"
              onClick={() => switchSidebarNavTab("projects")}
              class="w-full flex items-center justify-center gap-1 rounded-lg px-2 py-1.5 text-[11.5px] font-medium text-[var(--text-muted)] hover:text-[var(--accent-primary)] hover:bg-[var(--bg-hover)]/60 transition-colors cursor-pointer"
            >
              <span>
                {t("leftNav.viewOlderInProjects", { count: recentSelection().hiddenCount })}
              </span>
              <span>→</span>
            </button>
          }
        >
          <div class="px-2.5 py-1 text-[11px] text-[var(--text-faint)]">
            {t("leftNav.searchTruncatedHint", {
              shown: recentItems().length,
              total: recentSelection().totalMatches,
            })}
          </div>
        </Show>
      </Show>

      <ImportSessionFooter />
    </div>
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
                    item={{
                      session,
                      proj: group.proj,
                      subagents: group.subagentsByRoot?.get(session.id),
                    }}
                    filterQuery={props.filterQuery}
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
  const [sectionLimits, setSectionLimits] = createSignal<Partial<Record<AttentionSectionID, number>>>({});
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
          {(section) => {
            const visibleItems = () => {
              if (section.id === "approvals") return section.items;
              const limit = sectionLimits()[section.id] || COLLAPSED_SECTION_PAGE_SIZE;
              return section.items.slice(0, limit);
            };
            const remainingCount = () => Math.max(0, section.items.length - visibleItems().length);
            return (
              <div class="space-y-0.5">
                <SectionHeader label={ATTENTION_LABELS[section.id]()} count={section.items.length} />
                <For each={visibleItems()}>
                  {(item) => (
                    <SessionRow
                      item={item}
                      filterQuery={props.filterQuery}
                      showProjectBadge={true}
                      isSelected={isSelectedSession}
                      pendingSessions={props.pendingSessions}
                      onSelectSession={props.onSelectSession}
                      onArchiveSession={props.onArchiveSession}
                      onDiscardSession={props.onDiscardSession}
                    />
                  )}
                </For>
                <Show when={remainingCount() > 0}>
                  <button
                    type="button"
                    onClick={() =>
                      setSectionLimits((prev) => ({
                        ...prev,
                        [section.id]:
                          (prev[section.id] || COLLAPSED_SECTION_PAGE_SIZE) +
                          COLLAPSED_SECTION_PAGE_SIZE,
                      }))
                    }
                    class="w-full flex items-center justify-center rounded-lg px-2 py-1 text-[11.5px] font-medium text-[var(--accent-primary)] hover:bg-[var(--bg-hover)]/60 transition-colors cursor-pointer"
                  >
                    {t("leftNav.loadMoreSessions", { count: remainingCount() })}
                  </button>
                </Show>
              </div>
            );
          }}
        </For>
      </Show>
      <ImportSessionFooter />
    </div>
  );
}
