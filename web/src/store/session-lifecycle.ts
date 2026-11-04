// web/src/store/session-lifecycle.ts
import { produce } from "solid-js/store";
import type { SessionInfo } from "../services/sse-types";
import { hoxiAPI } from "../services/api";
import { persistSessionAI } from "./session-ai";
import { forgetSessionHistory } from "./session-history";
import { clearSessionPendingApprovals } from "./approval-mode";
import { ARCHIVED_SESSIONS_KEY, setWorkspace, workspace, writeStorageJSON } from "./workspace-state";
import {
  forgetSessionTitles,
  hasManualSessionTitle,
  recordCustomSessionTitle,
  recordManualSessionTitle,
} from "./session-title";
import {
  evictCachedSessionTurns,
  hydratedSessionIDs,
} from "./session-transcripts";
import {
  isManagedSession,
  isSessionBusy,
  markSessionDeleted,
  sessionDirectory,
} from "./session-query";
import {
  cancelScheduledWorktreeStatus,
  refreshWorktreeStatus,
} from "./session-diffs";
import {
  collectChildSubagentSessionIDs,
  deleteChildSubagentSessionsBackend,
} from "./subagent-sessions";
import {
  loadSessionMessages,
  nextActiveSessionCandidate,
  purgeSessionsFromStoreState,
  replaceSession,
  selectSession,
} from "./workspace-sessions";

function persistArchivedSessionIDs(ids: string[]) {
  writeStorageJSON(ARCHIVED_SESSIONS_KEY, ids);
}

export async function exitSessionWorktree(sessionID: string, keepBranch = false): Promise<void> {
  await leaveSessionWorktree(sessionID, keepBranch);
  await refreshWorktreeStatus(sessionID);
  await refreshWorktreeStatus("__root__");
}

async function leaveSessionWorktree(sessionID: string, keepBranch: boolean): Promise<void> {
  if (isManagedSession(sessionID)) {
    const targetSelector = sessionDirectory(sessionID) || workspace.rootDir || undefined;
    replaceSession(
      await hoxiAPI.setSandboxMode(
        sessionID,
        "local",
        { force: true, keepBranch },
        targetSelector
      )
    );
  } else {
    await hoxiAPI.removeWorktree(sessionID, true, keepBranch);
    if (workspace.rootDir) {
      setWorkspace("worktreeBySession", sessionID, {
        sessionID,
        isolated: false,
        directory: workspace.rootDir,
        branch: workspace.worktreeBySession["__root__"]?.branch,
        createdAt: new Date().toISOString(),
      });
    }
  }
}

export async function renameSessionTitle(
  sessionID: string,
  title: string,
  source: "manual" | "automatic" = "manual"
): Promise<void> {
  const cleaned = title.trim();
  if (!cleaned || (source === "automatic" && hasManualSessionTitle(sessionID))) return;
  if (source === "manual") recordManualSessionTitle(sessionID, cleaned);
  recordCustomSessionTitle(sessionID, cleaned);
  setWorkspace("sessions", (s) => s.id === sessionID, "title", cleaned);
  try {
    const updated = await hoxiAPI.renameSession(sessionID, cleaned, sessionDirectory(sessionID));
    if (updated?.id) {
      replaceSession({ ...updated, title: cleaned });
    }
  } catch (err) {
    console.error("rename session failed:", err);
  }
}

export async function archiveSession(sessionID: string, directory?: string): Promise<void> {
  const existing = workspace.sessions.find((s) => s.id === sessionID);
  const targetDir =
    directory || existing?.directory || sessionDirectory(sessionID) || workspace.rootDir || undefined;

  if (!workspace.archivedSessionIDs.includes(sessionID)) {
    const nextArchived = [...workspace.archivedSessionIDs, sessionID];
    setWorkspace("archivedSessionIDs", nextArchived);
    persistArchivedSessionIDs(nextArchived);
  }

  const now = Date.now();
  setWorkspace(
    produce((state) => {
      const target = state.sessions.find((s) => s.id === sessionID);
      if (target) {
        if (!target.time) target.time = { created: now, updated: now, archived: now };
        else target.time.archived = now;
      }
    })
  );
  clearSessionPendingApprovals(sessionID);

  if (workspace.activeSessionID === sessionID) {
    const next = nextActiveSessionCandidate(sessionID);
    if (next) {
      await selectSession(next.id);
    } else {
      setWorkspace("activeSessionID", null);
    }
  }

  try {
    await hoxiAPI.archiveSession(sessionID, true, targetDir);
  } catch (err) {
    console.warn("archive session backend sync warning:", err);
  }
}

export async function unarchiveSession(sessionID: string, directory?: string): Promise<void> {
  const existing = workspace.sessions.find((s) => s.id === sessionID);
  const targetDir =
    directory || existing?.directory || sessionDirectory(sessionID) || workspace.rootDir || undefined;

  if (workspace.archivedSessionIDs.includes(sessionID)) {
    const nextArchived = workspace.archivedSessionIDs.filter((id) => id !== sessionID);
    setWorkspace("archivedSessionIDs", nextArchived);
    persistArchivedSessionIDs(nextArchived);
  }

  setWorkspace(
    produce((state) => {
      const target = state.sessions.find((s) => s.id === sessionID);
      if (target?.time) {
        delete target.time.archived;
      }
    })
  );

  try {
    await hoxiAPI.archiveSession(sessionID, false, targetDir);
  } catch (err) {
    console.warn("unarchive session backend sync warning:", err);
  }
}

export function removeSessionFromStore(sessionID: string): SessionInfo | undefined {
  const childIDs = collectChildSubagentSessionIDs(sessionID);
  const allIDs = [sessionID, ...childIDs];
  for (const id of allIDs) {
    cancelScheduledWorktreeStatus(id);
    hydratedSessionIDs.delete(id);
    forgetSessionHistory(id);
    markSessionDeleted(id);
  }
  forgetSessionTitles(allIDs);
  evictCachedSessionTurns(allIDs);
  const nextCandidate = nextActiveSessionCandidate(sessionID);
  if (workspace.archivedSessionIDs.some((id) => allIDs.includes(id))) {
    const nextArchived = workspace.archivedSessionIDs.filter((id) => !allIDs.includes(id));
    setWorkspace("archivedSessionIDs", nextArchived);
    persistArchivedSessionIDs(nextArchived);
  }
  setWorkspace(produce((state) => purgeSessionsFromStoreState(state, allIDs)));
  persistSessionAI();
  return nextCandidate;
}

export async function discardSession(
  sessionID: string,
  force = true,
  directory?: string
): Promise<void> {
  const existing = workspace.sessions.find((s) => s.id === sessionID);
  const targetDir =
    directory || existing?.directory || sessionDirectory(sessionID) || workspace.rootDir || undefined;
  const childIDs = collectChildSubagentSessionIDs(sessionID);
  const wasActive =
    workspace.activeSessionID === sessionID ||
    (Boolean(workspace.activeSessionID) && childIDs.includes(workspace.activeSessionID!));
  const wasBusy = isSessionBusy(sessionID);

  const nextCandidate = removeSessionFromStore(sessionID);
  if (wasActive) {
    if (nextCandidate) {
      void selectSession(nextCandidate.id);
    } else {
      setWorkspace("activeSessionID", null);
    }
  }

  if (childIDs.length > 0) {
    void deleteChildSubagentSessionsBackend(childIDs, targetDir);
  }

  if (wasBusy) {
    try {
      await hoxiAPI.abortSession(sessionID, targetDir);
    } catch {
      /* ignore abort error */
    }
  }

  try {
    await hoxiAPI.removeWorktree(sessionID, force);
  } catch (err) {
    console.warn("remove worktree warning:", err);
  }

  try {
    await hoxiAPI.deleteSession(sessionID, targetDir);
  } catch (err) {
    if (workspace.rootDir && targetDir !== workspace.rootDir) {
      try {
        await hoxiAPI.deleteSession(sessionID, workspace.rootDir);
        return;
      } catch {
        /* ignore fallback error */
      }
    }
    console.error("delete session failed:", err);
  }
}

/** Asks the runtime to compact (summarize) a session's context, then reloads its transcript. */
export async function compactSessionContext(
  sessionID: string,
  model: { providerID: string; modelID: string }
): Promise<boolean> {
  const compacted = await hoxiAPI.summarizeSession(
    sessionID,
    model.providerID,
    model.modelID,
    sessionDirectory(sessionID)
  );
  if (compacted) await loadSessionMessages(sessionID);
  return Boolean(compacted);
}
