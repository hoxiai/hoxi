import { createEffect, createMemo, createSignal, For, onCleanup, Show } from "solid-js";
import type { Accessor, JSX } from "solid-js";
import type { SessionInfo } from "../../services/sse-types";
import { appSettings } from "../../store/settings";
import {
  groupSubagentsByRoot,
  normalizeFolderPath,
  sessionActivityTime,
  workspace,
} from "../../store/workspace";
import { t } from "../../i18n";
import { Icon } from "../icons";
import { useNowTicker } from "./session-time-helpers";
import {
  COLLAPSED_SECTION_PAGE_SIZE,
  compareSessionRunning,
  DEFAULT_PROJECT_MAX_VISIBLE,
  partitionSessionHistory,
  SEARCH_SECTION_MAX_VISIBLE,
  slicePaginatedSessions,
} from "./session-history";
import { dayLabel } from "./left-nav-helpers";

const MAX_TRACKED_PATHS = 128;

// Keep manual expansion and paging bounded by project path across switches.
const [expandedPaths, setExpandedPaths] = createSignal<Record<string, true>>({});
const [olderLimitsByPath, setOlderLimitsByPath] = createSignal<Record<string, number>>({});
const [archivedLimitsByPath, setArchivedLimitsByPath] = createSignal<Record<string, number>>({});

function putBoundedEntry<T>(
  record: Record<string, T>,
  key: string,
  value: T,
): Record<string, T> {
  const next: Record<string, T> = { ...record };
  delete next[key];
  const keys = Object.keys(next);
  if (keys.length >= MAX_TRACKED_PATHS) {
    delete next[keys[0]!];
  }
  next[key] = value;
  return next;
}

function removeEntry<T>(record: Record<string, T>, key: string): Record<string, T> {
  if (!(key in record)) return record;
  const next = { ...record };
  delete next[key];
  return next;
}

export function resetExpandedSessionHistory(path: string): void {
  const key = normalizeFolderPath(path) || path;
  if (!key) return;
  setExpandedPaths((prev) => removeEntry(prev, key));
  setOlderLimitsByPath((prev) => removeEntry(prev, key));
}

/**
 * Mounts drawer children immediately on open and unmounts them after the CSS
 * collapse transition finishes, preventing hidden collapsed lists from keeping
 * hundreds of SessionNavItem nodes and effects alive.
 */
export function useCollapsibleDrawerMount(
  open: Accessor<boolean>,
  closeDelayMs = 200,
): Accessor<boolean> {
  const [mounted, setMounted] = createSignal(open());
  createEffect(() => {
    if (open()) {
      setMounted(true);
      return;
    }
    const timer = setTimeout(() => setMounted(false), closeDelayMs);
    onCleanup(() => clearTimeout(timer));
  });
  return mounted;
}

export const sameSessionArray = (a: SessionInfo[], b: SessionInfo[]) =>
  a.length === b.length && a.every((s, i) => s === b[i]);

const EMPTY_SUBAGENT_MAP: ReadonlyMap<string, SessionInfo[]> = new Map();

export function useSessionHistory(
  sessions: Accessor<SessionInfo[]>,
  path: Accessor<string>,
  pending: Accessor<Set<string>>,
  query: Accessor<string>,
  subagentsByRoot?: Accessor<ReadonlyMap<string, SessionInfo[]>>,
) {
  const now = useNowTicker();
  const pathKey = createMemo(() => normalizeFolderPath(path()) || path());
  const isSearching = createMemo(() => Boolean(query().trim()));
  const expanded = () => Boolean(expandedPaths()[pathKey()]);
  const olderLimit = () => olderLimitsByPath()[pathKey()] || COLLAPSED_SECTION_PAGE_SIZE;
  const isActiveProject = createMemo(
    () => normalizeFolderPath(path()) === normalizeFolderPath(workspace.rootDir),
  );

  const partition = createMemo(() => {
    const raw = sessions();
    if (raw.length === 0) {
      return { visible: [] as SessionInfo[], older: [] as SessionInfo[], total: 0 };
    }
    const sorted = [...raw].sort((a, b) => compareSessionRunning(a, b, workspace.statusBySession));
    const searching = isSearching();
    const protectActive = !expanded() || searching;
    const pendingSet = pending();
    const activeProj = isActiveProject();
    const activeID = activeProj ? workspace.activeSessionID : null;
    const childrenByParent = subagentsByRoot
      ? subagentsByRoot()
      : activeProj
        ? groupSubagentsByRoot(workspace.sessions)
        : EMPTY_SUBAGENT_MAP;

    const split = partitionSessionHistory(sorted, {
      enabled: appSettings.sidebarAutoCollapse && !searching,
      minimum: appSettings.sidebarMinSessions,
      maximum: searching
        ? SEARCH_SECTION_MAX_VISIBLE
        : appSettings.sidebarAutoCollapse
          ? Math.max(appSettings.sidebarMinSessions, DEFAULT_PROJECT_MAX_VISIBLE)
          : SEARCH_SECTION_MAX_VISIBLE,
      recentMinutes: appSettings.sidebarRecentMinutes,
      now: now(),
      activityTime: sessionActivityTime,
      protectedSession: (session) => {
        const isProtectedRow = (s: SessionInfo) =>
          (protectActive && s.id === activeID) ||
          pendingSet.has(s.id) ||
          workspace.statusBySession[s.id]?.type === "busy" ||
          workspace.statusBySession[s.id]?.type === "retry";
        if (isProtectedRow(session)) return true;
        const children = childrenByParent.get(session.id);
        return Boolean(children && children.some(isProtectedRow));
      },
    });
    return { ...split, total: sorted.length };
  });

  const visibleSessions = createMemo(() => partition().visible, [], { equals: sameSessionArray });
  const pagedOlder = createMemo(() => {
    if (isSearching() || !expanded()) {
      return { items: [] as SessionInfo[], remainingCount: 0 };
    }
    return slicePaginatedSessions(partition().older, olderLimit());
  });
  const olderSessions = createMemo(() => pagedOlder().items, [], { equals: sameSessionArray });

  return {
    sessions: visibleSessions,
    olderSessions,
    olderCount: () => (isSearching() ? 0 : partition().older.length),
    remainingOlderCount: () => pagedOlder().remainingCount,
    searchTruncatedTotal: () =>
      isSearching() && partition().older.length > 0 ? partition().total : 0,
    expanded,
    loadMoreOlder: () => {
      const key = pathKey();
      setOlderLimitsByPath((prev) =>
        putBoundedEntry(
          prev,
          key,
          (prev[key] || COLLAPSED_SECTION_PAGE_SIZE) + COLLAPSED_SECTION_PAGE_SIZE,
        ),
      );
    },
    toggle: () => {
      const key = pathKey();
      if (expandedPaths()[key]) {
        setExpandedPaths((prev) => removeEntry(prev, key));
        setOlderLimitsByPath((prev) => removeEntry(prev, key));
      } else {
        setExpandedPaths((prev) => putBoundedEntry(prev, key, true));
      }
    },
  };
}

export function SessionHistoryToggle(props: { history: ReturnType<typeof useSessionHistory> }) {
  return (
    <Show when={props.history.olderCount() > 0}>
      <button
        type="button"
        aria-expanded={props.history.expanded()}
        title={props.history.expanded() ? t("leftNav.collapseSessions") : undefined}
        onClick={() => props.history.toggle()}
        class="w-full flex items-center justify-between gap-1.5 rounded-lg px-2.5 py-1 text-[12px] text-[var(--text-faint)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-hover)]/50 transition-colors cursor-pointer"
      >
        <span class="truncate">
          {t("leftNav.olderSessions", { count: props.history.olderCount() })}
        </span>
        <span
          class="hoxi-chevron shrink-0"
          data-open={props.history.expanded() ? "true" : "false"}
        >
          <Icon name="chevron-right" size="small" class="w-3 h-3" />
        </span>
      </button>
    </Show>
  );
}

export function SessionHistoryList(props: {
  history: ReturnType<typeof useSessionHistory>;
  grouped?: boolean;
  children: (session: SessionInfo) => JSX.Element;
}) {
  const renderSessions = (sessions: Accessor<SessionInfo[]>) => {
    // Key rows by session identity, not freshly allocated date-group objects.
    return <For each={sessions()}>{(session, index) => {
      const label = createMemo(() => {
        if (!props.grouped) return "";
        const current = dayLabel(sessionActivityTime(session));
        const previous = sessions()[index() - 1];
        return previous && dayLabel(sessionActivityTime(previous)) === current ? "" : current;
      });
      return (
        <div class="space-y-0.5 min-w-0">
          <Show when={label()}>
            <div class="px-2.5 py-0.5 text-[11px] font-medium text-[var(--text-faint)]">{label()}</div>
          </Show>
          {props.children(session)}
        </div>
      );
    }}</For>;
  };

  return (
    <>
      {renderSessions(props.history.sessions)}
      <Show when={props.history.searchTruncatedTotal() > 0}>
        <div class="px-2 py-1 text-[11px] text-[var(--text-faint)]">
          {t("leftNav.searchTruncatedHint", {
            shown: props.history.sessions().length,
            total: props.history.searchTruncatedTotal(),
          })}
        </div>
      </Show>
      <SessionHistoryToggle history={props.history} />
      {renderSessions(props.history.olderSessions)}
      <Show when={props.history.expanded() && props.history.remainingOlderCount() > 0}>
        <button
          type="button"
          onClick={() => props.history.loadMoreOlder()}
          class="w-full flex items-center justify-center rounded-lg px-2 py-1 text-[11.5px] font-medium text-[var(--accent-primary)] hover:bg-[var(--bg-hover)]/60 transition-colors cursor-pointer"
        >
          {t("leftNav.loadMoreSessions", { count: props.history.remainingOlderCount() })}
        </button>
      </Show>
    </>
  );
}

export function ArchivedSessionsDrawer(props: {
  projectKey: string;
  sessions: SessionInfo[];
  open: boolean;
  filterQuery?: string;
  bordered?: boolean;
  onToggle: (ev: MouseEvent) => void;
  children: (session: SessionInfo) => JSX.Element;
}) {
  const key = createMemo(() => normalizeFolderPath(props.projectKey) || props.projectKey);
  const isSearching = createMemo(() => Boolean(props.filterQuery?.trim()));
  const mounted = useCollapsibleDrawerMount(() => props.open);

  createEffect(() => {
    if (!props.open) {
      setArchivedLimitsByPath((prev) => removeEntry(prev, key()));
    }
  });

  const limit = () =>
    isSearching()
      ? SEARCH_SECTION_MAX_VISIBLE
      : archivedLimitsByPath()[key()] || COLLAPSED_SECTION_PAGE_SIZE;

  const page = createMemo(() =>
    mounted()
      ? slicePaginatedSessions(props.sessions, limit())
      : { items: [] as SessionInfo[], remainingCount: 0 },
  );
  const visibleItems = createMemo(() => page().items, [], { equals: sameSessionArray });

  return (
    <Show when={props.sessions.length > 0}>
      <div class={props.bordered ? "pt-1 border-t border-[var(--border-subtle)]/40" : "pt-0.5"}>
        <button
          type="button"
          aria-expanded={props.open}
          onClick={(ev) => props.onToggle(ev)}
          class="w-full flex items-center justify-between gap-1.5 rounded-lg px-2.5 py-1 text-[12px] text-[var(--text-faint)] hover:text-[var(--text-secondary)] hover:bg-[var(--bg-hover)]/45 transition-colors cursor-pointer"
        >
          <div class="flex items-center gap-1.5 min-w-0">
            <Icon name="archive" size="small" class="w-3 h-3 shrink-0 text-[var(--text-faint)]" />
            <span class="truncate">{t("leftNav.archivedSessions")}</span>
          </div>
          <div class="flex items-center gap-1 shrink-0">
            <span class="rounded-full bg-[var(--bg-inset)] px-1.5 py-0.2 text-[11px] font-mono text-[var(--text-faint)]">
              {props.sessions.length}
            </span>
            <span class="hoxi-chevron" data-open={props.open ? "true" : "false"}>
              <Icon name="chevron-right" size="small" class="w-3 h-3" />
            </span>
          </div>
        </button>

        <Show when={mounted()}>
          <div
            class="hoxi-drawer-grid"
            data-open={props.open ? "true" : "false"}
            aria-hidden={!props.open}
          >
            <div class="min-h-0 overflow-hidden">
              <div class="mt-1 space-y-0.5">
                <For each={visibleItems()}>{(session) => props.children(session)}</For>
                <Show when={!isSearching() && page().remainingCount > 0}>
                  <button
                    type="button"
                    onClick={(ev) => {
                      ev.stopPropagation();
                      const k = key();
                      setArchivedLimitsByPath((prev) =>
                        putBoundedEntry(
                          prev,
                          k,
                          (prev[k] || COLLAPSED_SECTION_PAGE_SIZE) + COLLAPSED_SECTION_PAGE_SIZE,
                        ),
                      );
                    }}
                    class="w-full flex items-center justify-center rounded-lg px-2 py-1 text-[11.5px] font-medium text-[var(--accent-primary)] hover:bg-[var(--bg-hover)]/60 transition-colors cursor-pointer"
                  >
                    {t("leftNav.loadMoreSessions", { count: page().remainingCount })}
                  </button>
                </Show>
                <Show when={isSearching() && page().remainingCount > 0}>
                  <div class="px-2 py-1 text-[11px] text-[var(--text-faint)]">
                    {t("leftNav.searchTruncatedHint", {
                      shown: visibleItems().length,
                      total: props.sessions.length,
                    })}
                  </div>
                </Show>
              </div>
            </div>
          </div>
        </Show>
      </div>
    </Show>
  );
}
