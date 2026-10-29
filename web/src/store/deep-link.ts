// web/src/store/deep-link.ts
// Session deep links (hoxi:// URLs, CLI focus requests, location hash).
// Owns the pending-link lifecycle: immediate focus when the session is loaded,
// a workspace switch when the intent names another project, and expiry so an
// unresolved link can never fire later after an unrelated project change.
import { createEffect, createRoot, createSignal, untrack } from "solid-js";
import { hoxiAPI, type DesktopIntent } from "../services/api";
import { onDesktopIntent } from "../services/desktop-events";
import { saveLayoutPrefs, setLeftNavOpen } from "./layout";
import { isSessionArchived, isSessionBusy, sessionDirectory } from "./session-query";
import { startNewThreadDraft } from "./session-sandbox";
import {
  closeHandoffModal,
  closeSettingsModal,
  fetchSystemRuntimeInfo,
  openHandoffModal,
  openSettingsModal,
} from "./settings";
import { resolveRootSessionID } from "./subagent-sessions";
import { setWorkspace, workspace } from "./workspace-state";
import { findWorkspaceProfileByDir, normalizeFolderPath, workspacePrimaryFolder } from "./workspace-folders";
import {
  closeWorkspaceModal,
  selectInitialProjectSession,
  setSidebarProjectMode,
  switchWorkspace,
} from "./workspace-projects";
import { selectSession } from "./workspace-sessions";

export interface SessionDeepLink {
  sessionID: string;
  messageID?: string;
  directory?: string;
}

interface PendingDeepLink {
  link: SessionDeepLink;
  /** Project root the link is bound to; empty until the first root is known. */
  rootDir: string;
  /** Project root targeted by the link's directory, if any. */
  targetRoot: string;
  switching: boolean;
  expiresAt: number;
}

export const DEEP_LINK_TTL_MS = 15_000;
const FOCUS_MESSAGE_DELAY_MS = 350;

const [pending, setPending] = createSignal<PendingDeepLink | null>(null);
let expiryTimer: ReturnType<typeof setTimeout> | undefined;
let focusTimer: ReturnType<typeof setTimeout> | undefined;

/** The link still waiting for its session; null once focused, dropped or expired. */
export function pendingDeepLink(): SessionDeepLink | null {
  return pending()?.link ?? null;
}

function clearExpiryTimer() {
  if (expiryTimer !== undefined) clearTimeout(expiryTimer);
  expiryTimer = undefined;
}

function clearFocusTimer() {
  if (focusTimer !== undefined) clearTimeout(focusTimer);
  focusTimer = undefined;
}

export function clearPendingDeepLink(): void {
  clearExpiryTimer();
  setPending(null);
}

// Worktree sessions and secondary folders resolve to their owning project root.
function projectRootForDirectory(directory?: string): string {
  const cleaned = normalizeFolderPath(directory);
  if (!cleaned) return "";
  const worktreeIdx = cleaned.indexOf("/.hoxi/worktrees/");
  const base = worktreeIdx > 0 ? cleaned.slice(0, worktreeIdx) : cleaned;
  const profile = findWorkspaceProfileByDir(base);
  return normalizeFolderPath(profile ? workspacePrimaryFolder(profile) : base) || base;
}

function focusSessionLink(link: SessionDeepLink) {
  clearFocusTimer();
  void selectSession(link.sessionID).then(() => {
    if (!link.messageID || typeof window === "undefined") return;
    clearFocusTimer();
    focusTimer = setTimeout(() => {
      focusTimer = undefined;
      window.dispatchEvent(new CustomEvent("hoxi:focus-message", { detail: { messageID: link.messageID } }));
    }, FOCUS_MESSAGE_DELAY_MS);
  });
}

/** Parses `#session=<id>&message=<id>` location hashes. */
export function parseSessionHashLink(hash: string): SessionDeepLink | null {
  const raw = hash.replace(/^#/, "");
  if (!raw) return null;
  const params = new URLSearchParams(raw);
  const sessionID = params.get("session");
  if (!sessionID) return null;
  return { sessionID, messageID: params.get("message") ?? undefined };
}

/**
 * Navigates to a session link. A loaded session is focused immediately; an
 * unknown one waits for bootstrap, switches to the intent's project when a
 * directory is known, and is dropped on project change or after `ttlMs`.
 */
export function openSessionDeepLink(
  link: SessionDeepLink | null | undefined,
  options: { ttlMs?: number } = {}
): void {
  const sessionID = link?.sessionID?.trim();
  if (!link || !sessionID) return;
  clearPendingDeepLink();
  const ttlMs = options.ttlMs ?? DEEP_LINK_TTL_MS;
  const entry: PendingDeepLink = {
    link: { ...link, sessionID },
    rootDir: normalizeFolderPath(workspace.rootDir),
    targetRoot: projectRootForDirectory(link.directory),
    switching: false,
    expiresAt: Date.now() + ttlMs,
  };
  setPending(entry);
  expiryTimer = setTimeout(() => {
    expiryTimer = undefined;
    if (pending() === entry) setPending(null);
  }, ttlMs);
  resolvePendingDeepLink();
}

/** Re-evaluates the pending link against the current store; reactive-safe. */
export function resolvePendingDeepLink(): void {
  const entry = pending();
  if (!entry) return;
  if (Date.now() > entry.expiresAt) {
    clearPendingDeepLink();
    return;
  }
  const bootstrapping = workspace.bootstrappingSessions;
  const root = normalizeFolderPath(workspace.rootDir);
  const loaded = workspace.sessions.some((session) => session.id === entry.link.sessionID);
  if (bootstrapping) return;
  if (loaded) {
    clearPendingDeepLink();
    focusSessionLink(entry.link);
    return;
  }
  // The startup restore picks the first root; the link adopts it once.
  if (!entry.rootDir) entry.rootDir = root;
  if (root !== entry.rootDir) {
    if (!(entry.switching && root === entry.targetRoot)) {
      clearPendingDeepLink();
      return;
    }
    entry.rootDir = root;
  }
  if (entry.switching || !entry.targetRoot || entry.targetRoot === root) return;
  entry.switching = true;
  entry.rootDir = entry.targetRoot;
  const targetRoot = entry.targetRoot;
  void switchWorkspace(targetRoot, { targetSessionID: entry.link.sessionID }).finally(() => {
    if (pending() !== entry) return;
    entry.switching = false;
    if (workspace.sessions.some((session) => session.id === entry.link.sessionID)) {
      resolvePendingDeepLink();
      return;
    }
    // The target project does not know this session: drop the link and fall
    // back to the normal initial selection instead of a dangling active ID.
    clearPendingDeepLink();
    if (
      normalizeFolderPath(workspace.rootDir) === targetRoot &&
      workspace.activeSessionID === entry.link.sessionID
    ) {
      setWorkspace("activeSessionID", null);
      void selectInitialProjectSession(workspace.sessions, targetRoot);
    }
  });
}

// Pulls the pending navigation queued by a hoxi:// deep link, the CLI, or the
// macOS status bar menu. The gateway clears it on read, so polling is safe and idempotent.
export async function consumeDesktopIntent(): Promise<DesktopIntent | null> {
  try {
    const intent = await hoxiAPI.consumeDesktopIntent();
    if (!intent?.action && !intent?.sessionID) return null;
    return intent;
  } catch {
    return null;
  }
}

/**
 * Ensures the sidebar is open without changing the current sidebar view mode,
 * and focuses the next session awaiting approval, interactive question input,
 * or user attention.
 */
export function focusFirstAttentionSession(targetSessionID?: string, directory?: string): void {
  setLeftNavOpen(true);
  saveLayoutPrefs();

  const explicitID = targetSessionID?.trim();
  if (explicitID) {
    const rootID = resolveRootSessionID(explicitID) || explicitID;
    openSessionDeepLink({
      sessionID: rootID,
      directory: directory || sessionDirectory(rootID) || sessionDirectory(explicitID),
    });
    return;
  }

  const targets: SessionDeepLink[] = [];
  const seen = new Set<string>();
  const pushTarget = (rawSessionID?: string) => {
    const sid = rawSessionID?.trim();
    if (!sid) return;
    const rootID = resolveRootSessionID(sid) || sid;
    if (seen.has(rootID)) return;
    seen.add(rootID);
    targets.push({
      sessionID: rootID,
      directory: sessionDirectory(rootID) || sessionDirectory(sid),
    });
  };

  for (const perm of workspace.pendingPermissions) {
    pushTarget(perm?.sessionID);
  }
  for (const question of workspace.pendingQuestions) {
    pushTarget(question?.sessionID);
  }

  if (targets.length > 0) {
    const activeID = workspace.activeSessionID?.trim();
    const activeRootID = activeID ? resolveRootSessionID(activeID) || activeID : "";
    const currentIdx = activeRootID
      ? targets.findIndex((t) => t.sessionID === activeRootID)
      : -1;
    const nextTarget =
      currentIdx >= 0 ? targets[(currentIdx + 1) % targets.length] : targets[0];
    openSessionDeepLink(nextTarget);
    return;
  }

  const candidate = workspace.sessions.find(
    (s) =>
      !s.parentID &&
      !isSessionArchived(s) &&
      Boolean(workspace.unreadCompletedBySession[s.id] || workspace.interruptedBySession[s.id])
  );
  if (candidate) {
    openSessionDeepLink({
      sessionID: candidate.id,
      directory: sessionDirectory(candidate.id),
    });
  }
}

/**
 * Opens the sidebar in "Running" mode and focuses the target or first active running session.
 */
export function focusFirstRunningSession(targetSessionID?: string, directory?: string): void {
  setLeftNavOpen(true);
  saveLayoutPrefs();
  setSidebarProjectMode("running");

  const explicitID = targetSessionID?.trim();
  if (explicitID) {
    const rootID = resolveRootSessionID(explicitID) || explicitID;
    openSessionDeepLink({
      sessionID: rootID,
      directory: directory || sessionDirectory(rootID) || sessionDirectory(explicitID),
    });
    return;
  }

  const loadedRunning = workspace.sessions.find(
    (s) => !s.parentID && !isSessionArchived(s) && isSessionBusy(s.id)
  );
  if (loadedRunning) {
    openSessionDeepLink({
      sessionID: loadedRunning.id,
      directory: sessionDirectory(loadedRunning.id),
    });
    return;
  }

  for (const [id, st] of Object.entries(workspace.statusBySession)) {
    if (st?.type === "busy" || st?.type === "retry") {
      const rootID = resolveRootSessionID(id) || id;
      openSessionDeepLink({
        sessionID: rootID,
        directory: sessionDirectory(rootID) || sessionDirectory(id),
      });
      return;
    }
  }
}

export function applyDesktopIntent(intent: DesktopIntent | null | undefined): void {
  if (!intent) return;
  switch (intent.action) {
    case "sync-keep-awake":
      void fetchSystemRuntimeInfo().catch(() => {});
      return;
    case "policy":
      closeHandoffModal();
      closeWorkspaceModal();
      openSettingsModal("policy");
      return;
    case "settings":
      closeHandoffModal();
      closeWorkspaceModal();
      openSettingsModal("general");
      return;
    case "handoff":
      closeSettingsModal();
      closeWorkspaceModal();
      openHandoffModal();
      return;
    case "new-thread":
      closeSettingsModal();
      closeHandoffModal();
      closeWorkspaceModal();
      startNewThreadDraft();
      return;
    case "attention":
      closeSettingsModal();
      closeHandoffModal();
      closeWorkspaceModal();
      focusFirstAttentionSession(intent.sessionID, intent.directory);
      return;
    case "running":
      closeSettingsModal();
      closeHandoffModal();
      closeWorkspaceModal();
      focusFirstRunningSession(intent.sessionID, intent.directory);
      return;
    default:
      if (intent.sessionID) {
        openSessionDeepLink({
          sessionID: intent.sessionID,
          messageID: intent.messageID,
          directory: intent.directory,
        });
      }
  }
}

async function consumeAndOpenDesktopIntent(): Promise<void> {
  const intent = await consumeDesktopIntent();
  if (intent) applyDesktopIntent(intent);
}

/**
 * Starts deep-link navigation for the desktop shell: subscribes to desktop
 * intent wake-ups, drains any intent queued before the UI mounted, and
 * re-resolves the pending link as sessions load. Returns a disposer.
 */
export function startDeepLinkNavigation(): () => void {
  const disposeRoot = createRoot((dispose) => {
    // Track only the inputs the link depends on; navigation side effects
    // (selectSession / switchWorkspace) run untracked.
    createEffect(() => {
      if (!pending()) return;
      void workspace.bootstrappingSessions;
      void workspace.rootDir;
      for (const session of workspace.sessions) void session.id;
      untrack(resolvePendingDeepLink);
    });
    return dispose;
  });
  const unsubscribe = onDesktopIntent(() => void consumeAndOpenDesktopIntent());
  void consumeAndOpenDesktopIntent();
  return () => {
    unsubscribe();
    disposeRoot();
    clearFocusTimer();
    clearPendingDeepLink();
  };
}
