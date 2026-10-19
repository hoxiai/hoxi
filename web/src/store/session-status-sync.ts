import { produce } from "solid-js/store";
import { hoxiAPI } from "../services/api";
import type { SessionStatus } from "../services/sse-types";
import { setWorkspace, workspace } from "./workspace";
import { activeDirectory, isManagedSession, loadSessionMessagesOnly, sessionDirectory } from "./workspace-sessions";
import { drainNextQueuedPrompt, lastPromptDispatchedAt } from "./workspace-queue-subagents";

// Hoxi-managed conversations report busy status globally from the runner process table.
// Only unadopted OpenCode sessions need per-directory status queries, and only for the
// active directory or sessions currently marked busy/retrying (never every historical worktree).
export function collectStatusDirectories(extra?: string): Array<string | undefined> {
  const dirs = new Set<string>();
  const add = (d?: string | null) => {
    const trimmed = d?.trim();
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

export async function syncSessionStatuses(directory?: string): Promise<void> {
  try {
    const dirs = collectStatusDirectories(directory);
    const results = await Promise.allSettled(
      dirs.map((dir) => hoxiAPI.getSessionStatuses(dir))
    );
    const remoteStatuses: Record<string, SessionStatus> = {};
    const queriedDirs = new Set<string | undefined>();
    results.forEach((res, i) => {
      if (res.status !== "fulfilled") return;
      queriedDirs.add(dirs[i]);
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
          if ((prev === "busy" || prev === "retry") && status?.type === "idle") {
            newlyIdleSessions.push(sid);
          }
        }
        for (const [sid, current] of Object.entries(state.statusBySession)) {
          if (
            (current?.type === "busy" || current?.type === "retry") &&
            !remoteStatuses[sid] &&
            (isManagedSession(sid) || queriedDirs.has(sessionDirectory(sid)))
          ) {
            const dispatchedAt = lastPromptDispatchedAt(sid);
            if (now - dispatchedAt > 3500) {
              state.statusBySession[sid] = { type: "idle" };
              newlyIdleSessions.push(sid);
            }
          }
        }
      })
    );

    for (const sid of newlyIdleSessions) {
      if (sid === workspace.activeSessionID || workspace.messagesBySession[sid] !== undefined) {
        void loadSessionMessagesOnly(sid).catch(() => {});
      }
      void drainNextQueuedPrompt(sid);
    }
  } catch (err) {
    console.warn("syncSessionStatuses warning:", err);
  }
}
