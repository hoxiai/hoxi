import { t } from "../i18n";
import {
  hoxiAPI,
  isRunnerSessionID,
  resolveRunnerRuntimeForProvider,
  type WorktreeMeta,
} from "../services/api";
import type { SessionInfo } from "../services/sse-types";
import { appSettings, updateAppSetting } from "./settings";
import { setWorkspace, workspace } from "./workspace";
import { initializeSessionAI } from "./session-ai";
import { sessionRuntimeForID } from "./workspace-providers";
import {
  isSessionBusy,
  refreshWorktreeStatus,
  removeSessionFromStore,
  replaceSession,
} from "./workspace-sessions";

const pendingSandboxChanges = new Map<string, Promise<void>>();

export function startNewThreadDraft(): void {
  setWorkspace({ activeSessionID: null, sessionSetupError: null,
    newThreadRequest: workspace.newThreadRequest + 1 });
}

export async function waitForSessionSandbox(sessionID: string): Promise<void> {
  await pendingSandboxChanges.get(sessionID);
}

async function changeSandbox(sessionID: string, change: () => Promise<void>): Promise<void> {
  if (pendingSandboxChanges.has(sessionID)) throw new Error(t("timeline.worktreeBusy"));
  const pending = Promise.resolve().then(change);
  pendingSandboxChanges.set(sessionID, pending);
  try {
    await pending;
  } finally {
    pendingSandboxChanges.delete(sessionID);
  }
}

async function prepareWorktree(sessionID: string, directory: string): Promise<void> {
  await changeSandbox(sessionID, async () => {
    const meta = await hoxiAPI.createWorktree(sessionID, directory);
    if (!meta.isolated || !meta.directory || meta.directory === directory) {
      throw new Error(t("timeline.worktreeIsolationUnavailable"));
    }
    setWorkspace("worktreeBySession", sessionID, meta);
  });
}

function localMeta(sessionID: string, directory: string): WorktreeMeta {
  return {
    sessionID,
    isolated: false,
    directory,
    branch: workspace.worktreeBySession["__root__"]?.branch,
    createdAt: new Date().toISOString(),
  };
}

function reportSetupError(error: unknown): void {
  setWorkspace("sessionSetupError", t("timeline.sessionSetupFailed", {
    error: error instanceof Error ? error.message : String(error),
  }));
}

export async function newIsolatedSession(
  title?: string,
  options?: { isolated?: boolean; onSessionCreated?: (sessionID: string) => void }
): Promise<string> {
  const directory = workspace.rootDir;
  const initialAI = { ...workspace.defaultAI };
  const previousSessionID = workspace.activeSessionID;
  const runtime = resolveRunnerRuntimeForProvider(
    initialAI.model?.providerID,
    Boolean(workspace.hoxiProviders?.[initialAI.model?.providerID || ""])
  );
  const isRunner = runtime === "antigravity" || runtime === "claude-code";
  const shouldIsolate = options?.isolated ?? (appSettings.defaultSandboxMode === "worktree");
  let session: SessionInfo | undefined;
  setWorkspace("sessionSetupError", null);
  try {
    if (!directory) throw new Error(t("timeline.selectFolderHint"));
    session = await hoxiAPI.createSession(title, directory || undefined, {
      runtime,
      modelID: initialAI.model?.modelID,
      providerID: initialAI.model?.providerID,
      mode: initialAI.agent,
      sandboxMode: shouldIsolate ? "worktree" : "local",
    });
    if (isRunner) {
      const wtDir = session.directory || directory;
      if (!wtDir || (shouldIsolate && wtDir === directory)) {
        throw new Error(t("timeline.worktreeIsolationUnavailable"));
      }
      setWorkspace("worktreeBySession", session.id, {
        sessionID: session.id,
        isolated: shouldIsolate,
        directory: wtDir,
        branch: session.branch || (shouldIsolate ? undefined : workspace.worktreeBySession["__root__"]?.branch),
        createdAt: new Date().toISOString(),
      });
      void refreshWorktreeStatus(session.id);
    } else if (shouldIsolate) {
      await prepareWorktree(session.id, directory);
    } else {
      setWorkspace("worktreeBySession", session.id, localMeta(session.id, directory));
    }
  } catch (error) {
    reportSetupError(error);
    if (session) {
      removeSessionFromStore(session.id);
      if (workspace.activeSessionID === session.id) setWorkspace("activeSessionID", previousSessionID);
      try {
        await hoxiAPI.deleteSession(session.id, directory || undefined);
      } catch (cleanupError) {
        console.warn("cleanup incomplete session failed:", cleanupError);
      }
    }
    throw error;
  }
  setWorkspace("messagesBySession", session.id, []);
  setWorkspace("diffsBySession", session.id, []);
  setWorkspace("loadingMessagesBySession", session.id, false);
  setWorkspace("unreadCompletedBySession", session.id, false);
  replaceSession(session);
  initializeSessionAI(session.id, initialAI);
  if (workspace.rootDir === directory) setWorkspace("activeSessionID", session.id);
  options?.onSessionCreated?.(session.id);
  return session.id;
}

export async function switchActiveSessionSandboxMode(mode: "local" | "worktree"): Promise<void> {
  const id = workspace.activeSessionID;
  if (!id) {
    // Draft sandbox selection must not provision a session before the first send.
    updateAppSetting("defaultSandboxMode", mode);
    return;
  }
  const directory = workspace.rootDir;
  setWorkspace("sessionSetupError", null);
  try {
    if (isSessionBusy(id)) throw new Error(t("timeline.worktreeBusy"));
    if (isRunnerSessionID(id) || sessionRuntimeForID(id) !== "opencode") {
      await changeSandbox(id, async () => {
        const session = await hoxiAPI.setSandboxMode(id, mode, undefined, directory || undefined);
        replaceSession(session);
        setWorkspace("worktreeBySession", id, {
          sessionID: id, isolated: session.sandboxMode === "worktree", directory: session.directory!,
          branch: session.branch, createdAt: new Date(session.time.created).toISOString(),
        });
      });
      await refreshWorktreeStatus(id);
      return;
    }
    const isolated = Boolean(workspace.worktreeBySession[id]?.isolated);
    if (mode === "worktree" && !isolated) {
      await prepareWorktree(id, directory);
      await refreshWorktreeStatus(id);
    } else if (mode === "local" && isolated) {
      await changeSandbox(id, async () => {
        await hoxiAPI.removeWorktree(id, true);
        setWorkspace("worktreeBySession", id, localMeta(id, directory));
      });
      await refreshWorktreeStatus(id);
    }
  } catch (error) {
    reportSetupError(error);
    throw error;
  }
}
