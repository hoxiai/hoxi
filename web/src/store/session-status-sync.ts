import { produce } from "solid-js/store";
import { hoxiAPI } from "../services/api";
import type { SessionStatus } from "../services/sse-types";
import { setWorkspace, workspace } from "./workspace-state";
import { loadSessionMessagesOnly } from "./workspace-sessions";
import { advanceSessionGoal } from "./session-goal";
import { activeDirectory, isManagedSession, sessionDirectory } from "./session-query";
import { resolveParentSessionID } from "./subagent-sessions";
import { drainNextQueuedPrompt } from "./workspace-session-messages";
import { lastPromptDispatchedAt } from "./prompt-queue";
import { normalizeFolderPath } from "./workspace-folders";

// Hoxi-managed conversations report busy status globally from the runner process
// table, which the unified stream pushes as `hoxi.status.snapshot`. That snapshot
// covers every project, so a project switch never turns a running session idle.
// Only unadopted OpenCode sessions need per-directory status queries, and only for
// the active directory or sessions currently marked busy/retrying.
let managedSnapshotSeen = false;

export function collectStatusDirectories(extra?: string): Array<string | undefined> {
  const dirs = new Set<string>();
  const add = (d?: string | null) => {
    const trimmed = normalizeFolderPath(d);
    if (trimmed) dirs.add(trimmed);
  };
  add(extra);
  add(workspace.rootDir);
  if (workspace.activeSessionID && !isManagedSession(workspace.activeSessionID)) {
    add(activeDirectory());
  }
  for (const [sid, st] of Object.entries(workspace.statusBySession)) {
    if ((st?.type === "busy" || st?.type === "retry") && !isManagedSession(sid)) {
      add(sessionDirectory(sid));
    }
  }
  return dirs.size > 0 ? Array.from(dirs) : [undefined];
}

function finishIdleSessions(sessionIDs: string[]): void {
  for (const sid of sessionIDs) {
    if (sid === workspace.activeSessionID || workspace.messagesBySession[sid] !== undefined) {
      void loadSessionMessagesOnly(sid).catch(() => {});
    }
    if (sid !== workspace.activeSessionID &&
        !workspace.interruptedBySession[sid] &&
        !resolveParentSessionID(sid)) {
      setWorkspace("unreadCompletedBySession", sid, true);
    }
    void drainNextQueuedPrompt(sid);
    void advanceSessionGoal(sid);
  }
}

/**
 * Applies the authoritative busy set from the unified stream. Sessions the
 * snapshot omits are idle; this is safe across projects because the runner
 * tracks every Hoxi-managed session in one process table.
 */
export function applyManagedStatusSnapshot(
  busySessionIDs: string[],
  stalledBySession: Record<string, number> = {}
): void {
  managedSnapshotSeen = true;
  const busy = new Set(busySessionIDs.filter((id) => typeof id === "string" && id));
  const now = Date.now();
  const newlyIdle: string[] = [];

  setWorkspace(
    produce((state) => {
      for (const sid of busy) {
        const prevIdx = state.sessionIndex[sid];
        if (!prevIdx) {
          state.sessionIndex[sid] = { managed: true, updated: 0 };
        } else if (!prevIdx.managed) {
          prevIdx.managed = true;
        }
        if (state.statusBySession[sid]?.type === "busy" || state.statusBySession[sid]?.type === "retry") {
          continue;
        }
        state.statusBySession[sid] = { type: "busy" };
        state.unreadCompletedBySession[sid] = false;
        state.stalledSinceBySession[sid] = 0;
      }
      for (const [sid, current] of Object.entries(state.statusBySession)) {
        if (current?.type !== "busy" && current?.type !== "retry") continue;
        if (busy.has(sid)) {
          const since = stalledBySession[sid];
          if (since) state.stalledSinceBySession[sid] = since;
          continue;
        }
        if (!isManagedSession(sid)) continue;
        // A prompt dispatched moments ago may not have reached the process
        // table yet; do not race the snapshot against it.
        if (now - lastPromptDispatchedAt(sid) <= 3500) continue;
        state.statusBySession[sid] = { type: "idle" };
        state.stalledSinceBySession[sid] = 0;
        state.sessionFinishedAt[sid] = now;
        newlyIdle.push(sid);
      }
    })
  );

  if (newlyIdle.length > 0) finishIdleSessions(newlyIdle);
}

export async function syncSessionStatuses(directory?: string): Promise<void> {
  try {
    const dirs = collectStatusDirectories(directory);
    const results = await Promise.allSettled(
      dirs.map((dir) => hoxiAPI.getSessionStatuses(dir))
    );
    const remoteStatuses: Record<string, SessionStatus> = {};
    const queriedDirs = new Set<string>();
    results.forEach((res, i) => {
      if (res.status !== "fulfilled") return;
      const dir = normalizeFolderPath(dirs[i]);
      if (dir) queriedDirs.add(dir);
      for (const [sid, status] of Object.entries(res.value || {})) {
        if (!remoteStatuses[sid] || remoteStatuses[sid].type === "idle") {
          remoteStatuses[sid] = status;
        }
      }
    });
    if (queriedDirs.size === 0) return;
    const now = Date.now();
    const newlyIdleSessions: string[] = [];

    setWorkspace(
      produce((state) => {
        for (const [sid, status] of Object.entries(remoteStatuses)) {
          if (
            status?.type === "idle" &&
            now - lastPromptDispatchedAt(sid) <= 3500
          ) {
            continue;
          }
          const prevStatus = state.statusBySession[sid];
          const prev = prevStatus?.type;
          const prevMsg = prevStatus?.type === "retry" ? prevStatus.message : undefined;
          const nextMsg = status?.type === "retry" ? status.message : undefined;
          if (prev !== status?.type || prevMsg !== nextMsg) {
            state.statusBySession[sid] = status;
          }
          // Live `session.stall` events clear the flag; a snapshot only
          // restores it (e.g. after reconnecting) or drops it once idle.
          if (status?.type !== "busy") state.stalledSinceBySession[sid] = 0;
          else if (status.stalledSince) state.stalledSinceBySession[sid] = status.stalledSince;
          if ((prev === "busy" || prev === "retry") && status?.type === "idle") {
            newlyIdleSessions.push(sid);
          }
        }
        for (const [sid, current] of Object.entries(state.statusBySession)) {
          if (current?.type !== "busy" && current?.type !== "retry") continue;
          if (remoteStatuses[sid]) continue;
          // Managed sessions are authoritative from the global stream snapshot;
          // a per-directory query can never prove one of them is idle.
          if (isManagedSession(sid)) {
            if (managedSnapshotSeen) continue;
            const dispatchedAt = lastPromptDispatchedAt(sid);
            if (now - dispatchedAt > 3500) {
              state.statusBySession[sid] = { type: "idle" };
              newlyIdleSessions.push(sid);
            }
            continue;
          }
          // Unadopted sessions are only proven idle when their own directory
          // was actually queried in this round.
          const dir = normalizeFolderPath(sessionDirectory(sid));
          if (!dir || !queriedDirs.has(dir)) continue;
          const dispatchedAt = lastPromptDispatchedAt(sid);
          if (now - dispatchedAt > 3500) {
            state.statusBySession[sid] = { type: "idle" };
            newlyIdleSessions.push(sid);
          }
        }
      })
    );

    for (const sid of newlyIdleSessions) {
      if (sid === workspace.activeSessionID || workspace.messagesBySession[sid] !== undefined) {
        void loadSessionMessagesOnly(sid).catch(() => {});
      }
      void drainNextQueuedPrompt(sid);
      void advanceSessionGoal(sid);
    }
  } catch (err) {
    console.warn("syncSessionStatuses warning:", err);
  }
}
