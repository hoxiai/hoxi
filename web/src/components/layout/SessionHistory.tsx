import { createMemo, createSignal, For, Show } from "solid-js";
import type { Accessor, JSX } from "solid-js";
import type { SessionInfo } from "../../services/sse-types";
import { appSettings } from "../../store/settings";
import { sessionActivityTime, workspace } from "../../store/workspace";
import { t } from "../../i18n";
import { Icon } from "../icons";
import { useNowTicker } from "./session-time-helpers";
import { compareSessionRunning, partitionSessionHistory } from "./session-history";
import { dayLabel } from "./left-nav-helpers";

// Keep manual expansion stable across workspace switches and background updates.
const [expandedPaths, setExpandedPaths] = createSignal(new Set<string>());

const sameSessionArray = (a: SessionInfo[], b: SessionInfo[]) =>
  a.length === b.length && a.every((s, i) => s === b[i]);

export function useSessionHistory(
  sessions: Accessor<SessionInfo[]>,
  path: Accessor<string>,
  pending: Accessor<Set<string>>,
  query: Accessor<string>,
) {
  const now = useNowTicker();
  const expanded = () => expandedPaths().has(path());
  const partition = createMemo(() => partitionSessionHistory(
    [...sessions()].sort((a, b) => compareSessionRunning(a, b, workspace.statusBySession)), {
    enabled: appSettings.sidebarAutoCollapse && !query().trim(),
    minimum: appSettings.sidebarMinSessions,
    recentMinutes: appSettings.sidebarRecentMinutes,
    now: now(),
    activityTime: sessionActivityTime,
    protectedSession: (session) => {
      // Protect root rows when a selected or running child belongs to them.
      // When older sessions are manually expanded, keep items in place instead of jumping across the toggle bar.
      const protectActive = !expanded();
      const family = [session, ...workspace.sessions.filter((s) => s.parentID === session.id)];
      return family.some((s) => (protectActive && s.id === workspace.activeSessionID) || pending().has(s.id) ||
        workspace.statusBySession[s.id]?.type === "busy" ||
        workspace.statusBySession[s.id]?.type === "retry");
    },
  }));
  const visibleSessions = createMemo(() => partition().visible, [], { equals: sameSessionArray });
  const olderSessions = createMemo(
    () => (expanded() ? partition().older : []),
    [],
    { equals: sameSessionArray },
  );
  return {
    sessions: visibleSessions,
    olderSessions,
    olderCount: () => partition().older.length,
    expanded,
    toggle: () => setExpandedPaths((previous) => {
      const next = new Set(previous);
      if (next.has(path())) next.delete(path());
      else next.add(path());
      return next;
    }),
  };
}

export function SessionHistoryToggle(props: { history: ReturnType<typeof useSessionHistory> }) {
  return (
    <Show when={props.history.olderCount() > 0}>
      <button
        type="button"
        aria-expanded={props.history.expanded()}
        onClick={() => props.history.toggle()}
        class="w-full flex items-center gap-1.5 rounded-lg px-2 py-1 text-[12px] text-[var(--text-faint)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-hover)]/50 cursor-pointer"
      >
        <Icon name={props.history.expanded() ? "chevron-down" : "chevron-right"} size="small" />
        {t("leftNav.olderSessions", { count: props.history.olderCount() })}
        <Show when={props.history.expanded()}>
          <span class="ml-auto text-[var(--text-secondary)]">{t("leftNav.collapseSessions")}</span>
        </Show>
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
        <div class="space-y-0.5">
          <Show when={label()}>
            <div class="px-2 py-0.5 text-[11px] font-medium text-[var(--text-faint)]">{label()}</div>
          </Show>
          {props.children(session)}
        </div>
      );
    }}</For>;
  };
  return <>
    {renderSessions(props.history.sessions)}
    <SessionHistoryToggle history={props.history} />
    {renderSessions(props.history.olderSessions)}
  </>;
}
