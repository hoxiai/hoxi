import { t } from "../i18n";
import {
  hoxiAPI,
  resolveRunnerRuntimeForProvider,
  type WorktreeMeta,
  type WorktreeRepoStatus,
} from "../services/api";
import type { SessionInfo } from "../services/sse-types";
import { appSettings, updateAppSetting } from "./settings";
import { setWorkspace, workspace } from "./workspace";
import { initializeSessionAI } from "./session-ai";
import {
  getComposerSkill,
  revalidateSkillForTargetDirectory,
  selectComposerSkill,
  setComposerSkillNotice,
} from "./skills";
import { normalizeFolderPath, workspaceFoldersForDir } from "./workspace-projects";
import { sessionRuntimeForID } from "./workspace-providers";
import {
  exitSessionWorktree,
  isManagedSession,
  isSessionBusy,
  refreshWorktreeStatus,
  removeSessionFromStore,
  replaceSession,
} from "./workspace-sessions";

const pendingSandboxChanges = new Map<string, Promise<void>>();

export function secondaryWorkspaceDirs(primaryDir?: string): string[] {
  const baseNorm = normalizeFolderPath(primaryDir || workspace.rootDir || "");
  if (!baseNorm) return [];
  const folders = workspaceFoldersForDir(baseNorm);
  if (folders.length <= 1) return [];
  const out: string[] = [];
  const seen = new Set<string>([baseNorm]);
  for (const f of folders) {
    const norm = normalizeFolderPath(f.path);
    if (!norm || seen.has(norm)) continue;
    seen.add(norm);
    out.push(norm);
  }
  return out;
}

function areRepoStatusListsEqual(
  prev: WorktreeRepoStatus[] | undefined,
  next: WorktreeRepoStatus[] | undefined
): boolean {
  if (!prev && !next) return true;
  if (!prev || !next || prev.length !== next.length) return false;
  for (let i = 0; i < prev.length; i++) {
    const a = prev[i];
    const b = next[i];
    if (
      a.repo !== b.repo ||
      a.name !== b.name ||
      a.directory !== b.directory ||
      a.branch !== b.branch ||
      a.headCommit !== b.headCommit ||
      a.headSubject !== b.headSubject ||
      a.dirty !== b.dirty ||
      a.changedFiles !== b.changedFiles ||
      a.additions !== b.additions ||
      a.deletions !== b.deletions ||
      a.aheadCount !== b.aheadCount ||
      a.behindCount !== b.behindCount ||
      a.hasUpstream !== b.hasUpstream ||
      a.compareURL !== b.compareURL
    ) {
      return false;
    }
  }
  return true;
}

export function setWorktreeMetaIfChanged(key: string, next: WorktreeMeta): void {
  const prev = workspace.worktreeBySession[key];
  if (
    prev &&
    prev.sessionID === next.sessionID &&
    prev.isolated === next.isolated &&
    prev.directory === next.directory &&
    prev.branch === next.branch &&
    prev.baseBranch === next.baseBranch &&
    prev.baseCommit === next.baseCommit &&
    prev.headCommit === next.headCommit &&
    prev.headSubject === next.headSubject &&
    prev.dirty === next.dirty &&
    prev.changedFiles === next.changedFiles &&
    prev.uncommittedLines === next.uncommittedLines &&
    prev.additions === next.additions &&
    prev.deletions === next.deletions &&
    prev.aheadCount === next.aheadCount &&
    prev.behindCount === next.behindCount &&
    prev.hasUpstream === next.hasUpstream &&
    prev.compareURL === next.compareURL &&
    prev.health === next.health &&
    areRepoStatusListsEqual(prev.repos, next.repos)
  ) {
    return;
  }
  setWorkspace("worktreeBySession", key, next);
}

export function startNewThreadDraft(): void {
  selectComposerSkill(null, null);
  setComposerSkillNotice(null, null);
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
  const isRunner = runtime !== "opencode";
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
    if (isRunner || session.managed) {
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
    if (isManagedSession(id)) {
      await changeSandbox(id, async () => {
        const session = await hoxiAPI.setSandboxMode(id, mode, undefined, directory || undefined);
        replaceSession(session);
        setWorkspace("worktreeBySession", id, {
          sessionID: id, isolated: session.sandboxMode === "worktree", directory: session.directory!,
          branch: session.branch, createdAt: new Date(session.time.created).toISOString(),
        });
      });
      await refreshWorktreeStatus(id);
    } else {
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
    }
    const existingSkill = getComposerSkill(id);
    if (existingSkill) {
      const targetDir = workspace.worktreeBySession[id]?.directory || directory || "";
      const check = await revalidateSkillForTargetDirectory({
        sessionID: id,
        directory: targetDir,
        runtime: sessionRuntimeForID(id),
        selection: existingSkill,
      });
      if (check.ok && check.selection) {
        selectComposerSkill(id, check.selection);
      } else {
        selectComposerSkill(id, null);
        setComposerSkillNotice(id, t("skills.sandboxSkillMissing", { name: existingSkill.name }));
      }
    }
  } catch (error) {
    reportSetupError(error);
    throw error;
  }
}

export async function applySessionWorktree(
  sessionID: string,
  mode: "patch" | "merge" = "patch"
): Promise<void> {
  await hoxiAPI.applyWorktree(sessionID, mode);
  await refreshWorktreeStatus(sessionID);
  await refreshWorktreeStatus("__root__");
}

export async function applyAndExitSessionWorktree(
  sessionID: string,
  mode: "patch" | "merge" = "patch",
  keepBranch = false
): Promise<void> {
  await hoxiAPI.applyWorktree(sessionID, mode);
  await exitSessionWorktree(sessionID, keepBranch);
}

export async function renameSessionBranch(sessionID: string, newBranch: string): Promise<string> {
  const res = await hoxiAPI.renameBranch(sessionID, newBranch, workspace.rootDir || undefined);
  await refreshWorktreeStatus(sessionID);
  await refreshWorktreeStatus("__root__");
  return res.branch;
}

export async function checkoutLocalBranch(branch: string, create = false): Promise<void> {
  await hoxiAPI.checkoutBranch(branch, create, workspace.rootDir || undefined);
  await refreshWorktreeStatus("__root__");
  if (workspace.activeSessionID) {
    await refreshWorktreeStatus(workspace.activeSessionID);
  }
}

export async function syncSessionWorktree(sessionID: string): Promise<void> {
  await hoxiAPI.syncWorktree(sessionID, workspace.rootDir || undefined);
  await refreshWorktreeStatus(sessionID);
  await refreshWorktreeStatus("__root__");
}

export async function restoreSessionWorktree(sessionID: string): Promise<void> {
  await hoxiAPI.createWorktree(sessionID, workspace.rootDir || undefined);
  await refreshWorktreeStatus(sessionID);
  await refreshWorktreeStatus("__root__");
}
