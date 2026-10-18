import type { SessionInfo } from "../../services/sse-types";

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
    recentMinutes: number;
    now: number;
    activityTime: (session: SessionInfo) => number;
    protectedSession: (session: SessionInfo) => boolean;
  },
) {
  const visible: SessionInfo[] = [];
  const older: SessionInfo[] = [];
  const cutoff = options.now - options.recentMinutes * 60_000;
  sessions.forEach((session, index) => {
    const keep = !options.enabled || index < options.minimum ||
      (options.recentMinutes > 0 && options.activityTime(session) >= cutoff) ||
      options.protectedSession(session);
    (keep ? visible : older).push(session);
  });
  return { visible, older };
}
