import type { SessionInfo } from "../../services/sse-types";

export const DEFAULT_PROJECT_MAX_VISIBLE = 15;
export const COLLAPSED_SECTION_PAGE_SIZE = 20;
export const SEARCH_SECTION_MAX_VISIBLE = 30;

export function compareSessionRunning(
  a: SessionInfo,
  b: SessionInfo,
  statuses: Record<string, { type: string } | undefined>,
): number {
  const running = (id: string) => {
    const status = statuses[id]?.type;
    return status === "busy" || status === "retry";
  };
  return Number(running(b.id)) - Number(running(a.id));
}

export function partitionSessionHistory(
  sessions: SessionInfo[],
  options: {
    enabled: boolean;
    minimum: number;
    maximum?: number;
    recentMinutes: number;
    now: number;
    activityTime: (session: SessionInfo) => number;
    protectedSession: (session: SessionInfo) => boolean;
  },
) {
  const visible: SessionInfo[] = [];
  const older: SessionInfo[] = [];
  const cutoff = options.now - options.recentMinutes * 60_000;
  const maxUnprotected =
    options.maximum !== undefined && options.maximum > 0
      ? options.maximum
      : Number.POSITIVE_INFINITY;
  let unprotectedKept = 0;

  sessions.forEach((session, index) => {
    if (options.protectedSession(session)) {
      visible.push(session);
      return;
    }
    const withinWindow =
      !options.enabled ||
      index < options.minimum ||
      (options.recentMinutes > 0 && options.activityTime(session) >= cutoff);
    if (withinWindow && unprotectedKept < maxUnprotected) {
      visible.push(session);
      unprotectedKept++;
    } else {
      older.push(session);
    }
  });
  return { visible, older };
}

export function slicePaginatedSessions(
  sessions: readonly SessionInfo[],
  limit: number,
): { items: SessionInfo[]; remainingCount: number } {
  const cap = Math.max(1, limit);
  if (sessions.length <= cap) {
    return { items: sessions as SessionInfo[], remainingCount: 0 };
  }
  return {
    items: sessions.slice(0, cap),
    remainingCount: sessions.length - cap,
  };
}
