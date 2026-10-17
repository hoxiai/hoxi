// web/src/store/workspace-sessions.ts
import { produce } from "solid-js/store";
import type { MessageInfo, MessagePart, SessionInfo } from "../services/sse-types";
import { hoxiAPI } from "../services/api";
import { RECENT_PROJECTS_KEY, setWorkspace, workspace } from "./workspace";
import { loadAgents, loadProviders } from "./workspace-providers";

export async function loadProjects(): Promise<void> {
  try {
    const remote = await hoxiAPI.listProjects();
    const list = Array.isArray(remote) ? remote : [];
    setWorkspace("projects", list);
  } catch (err) {
    console.error("Failed to load projects:", err);
  }
}

export async function switchWorkspace(directory: string): Promise<void> {
  const cleaned = directory.trim();
  if (!cleaned) return;

  // Persist to recent projects
  const recents = workspace.recentProjects.filter((p) => p !== cleaned);
  const updatedRecents = [cleaned, ...recents].slice(0, 15);
  setWorkspace("recentProjects", updatedRecents);
  try {
    localStorage.setItem(RECENT_PROJECTS_KEY, JSON.stringify(updatedRecents));
  } catch {
    /* ignore */
  }

  setWorkspace({
    rootDir: cleaned,
    activeSessionID: null,
    messagesBySession: {},
    diffsBySession: {},
    worktreeBySession: {},
    reviewCommentsBySession: {},
    pendingPermissions: [],
  });

  try {
    const sessions = await hoxiAPI.listSessions(cleaned);
    setWorkspace("sessions", sessions);

    // Restore isolated worktree sandboxes
    await Promise.allSettled(
      sessions.map(async (s) => {
        const st = await hoxiAPI.getWorktreeStatus(s.id);
        if (st.isolated && st.directory) {
          setWorkspace("worktreeBySession", (prev) => ({
            ...prev,
            [s.id]: {
              sessionID: s.id,
              isolated: true,
              directory: st.directory,
              branch: st.branch,
              baseCommit: st.baseCommit,
              createdAt: new Date().toISOString(),
            },
          }));
        }
      })
    );

    if (sessions.length > 0) {
      const mostRecent = [...sessions].sort((a, b) => b.time.created - a.time.created)[0];
      await selectSession(mostRecent.id);
    }
  } catch (err) {
    console.error("switch workspace error:", err);
  }

  void loadProviders();
  void loadProjects();
  void loadAgents(cleaned);
}

export async function openWorkspaceFolder(): Promise<void> {
  try {
    const res = await hoxiAPI.openDirectoryDialog();
    if (res?.path && res.path.trim()) {
      await switchWorkspace(res.path.trim());
    }
  } catch (err) {
    console.error("open directory dialog error:", err);
  }
}

export function activeSession(): SessionInfo | undefined {
  return workspace.sessions.find((s) => s.id === workspace.activeSessionID);
}

export function sessionDirectory(sessionID: string | null): string | undefined {
  if (!sessionID) return undefined;
  const meta = workspace.worktreeBySession[sessionID];
  return meta?.directory || workspace.rootDir || undefined;
}

export function activeDirectory(): string | undefined {
  return sessionDirectory(workspace.activeSessionID);
}

export function isSessionBusy(sessionID: string | null): boolean {
  if (!sessionID) return false;
  return workspace.statusBySession[sessionID]?.type === "busy";
}

export function sessionsWithPendingPermission(): Set<string> {
  const set = new Set<string>();
  for (const p of workspace.pendingPermissions) set.add(p.sessionID);
  for (const q of workspace.pendingQuestions) set.add(q.sessionID);
  return set;
}

function replaceSession(info: SessionInfo) {
  setWorkspace("sessions", (prev) => {
    const idx = prev.findIndex((s) => s.id === info.id);
    if (idx < 0) return [info, ...prev];
    const next = prev.slice();
    next[idx] = info;
    return next;
  });
}

export async function loadSessionMessages(sessionID: string): Promise<void> {
  const directory = sessionDirectory(sessionID);
  const turns = await hoxiAPI.getMessages(sessionID, directory);
  setWorkspace("messagesBySession", (prev) => ({
    ...prev,
    [sessionID]: turns.map((t) => ({ info: t.info, parts: t.parts })),
  }));
  const diffs = await hoxiAPI.getSessionDiff(sessionID, directory);
  setWorkspace("diffsBySession", (prev) => ({ ...prev, [sessionID]: diffs }));
}

export async function selectSession(sessionID: string): Promise<void> {
  setWorkspace("activeSessionID", sessionID);
  try {
    await loadSessionMessages(sessionID);
  } catch (err) {
    console.error("load session messages failed:", err);
  }
}

export async function newIsolatedSession(title = "New Session"): Promise<string> {
  const session = await hoxiAPI.createSession(title, workspace.rootDir || undefined);
  try {
    const meta = await hoxiAPI.createWorktree(session.id);
    setWorkspace("worktreeBySession", (prev) => ({ ...prev, [session.id]: meta }));
  } catch (err) {
    console.error("create worktree failed:", err);
  }
  replaceSession(session);
  await selectSession(session.id);
  return session.id;
}

export async function applySessionWorktree(
  sessionID: string,
  mode: "patch" | "merge" = "patch"
): Promise<void> {
  await hoxiAPI.applyWorktree(sessionID, mode);
}

export async function discardSession(sessionID: string, force = false): Promise<void> {
  try {
    await hoxiAPI.removeWorktree(sessionID, force);
  } catch (err) {
    console.error("remove worktree failed:", err);
  }
  try {
    await hoxiAPI.deleteSession(sessionID, workspace.rootDir || undefined);
  } catch (err) {
    console.error("delete session failed:", err);
  }
  setWorkspace("sessions", (prev) => prev.filter((s) => s.id !== sessionID));
  setWorkspace("worktreeBySession", (prev) => {
    const next = { ...prev };
    delete next[sessionID];
    return next;
  });
  if (workspace.activeSessionID === sessionID) {
    setWorkspace("activeSessionID", workspace.sessions[0]?.id ?? null);
  }
}

export async function revertToHere(
  sessionID: string,
  messageID: string,
  partID?: string
): Promise<void> {
  const info = await hoxiAPI.revertSession(
    sessionID,
    messageID,
    partID,
    sessionDirectory(sessionID)
  );
  replaceSession(info);
}

export async function undoRevert(sessionID: string): Promise<void> {
  const info = await hoxiAPI.unrevertSession(sessionID, sessionDirectory(sessionID));
  replaceSession(info);
}

export async function forkFromMessage(
  sessionID: string,
  messageID?: string
): Promise<string> {
  const forked = await hoxiAPI.forkSession(sessionID, messageID, sessionDirectory(sessionID));
  replaceSession(forked);
  await selectSession(forked.id);
  return forked.id;
}

export async function submitSessionPrompt(
  promptText: string,
  targetSessionID?: string | null,
  options?: {
    agent?: string;
    model?: { providerID: string; modelID: string };
    attachments?: Array<{ type: "file"; mime: string; filename?: string; url: string }>;
  }
): Promise<string> {
  let id = targetSessionID ?? workspace.activeSessionID;
  if (!id) {
    id = await newIsolatedSession(promptText.slice(0, 48) || "Image Prompt");
  }

  const now = Date.now();
  const optimisticMsgID = `optimistic_${now}`;
  const effectiveAgent = options?.agent || workspace.activeMode;
  const effectiveModel = options?.model || workspace.selectedModel || undefined;
  const attachments = options?.attachments ?? [];

  // Optimistically show user prompt & busy status immediately (0ms perceived latency)
  setWorkspace("statusBySession", id, { type: "busy" });
  setWorkspace(
    produce((state) => {
      const list = (state.messagesBySession[id!] ||= []);
      const optimisticParts: MessagePart[] = attachments.map((att, idx) => ({
        id: `prt_file_${now}_${idx}`,
        sessionID: id!,
        messageID: optimisticMsgID,
        type: "file",
        mime: att.mime,
        filename: att.filename,
        url: att.url,
      }));
      if (promptText || optimisticParts.length === 0) {
        optimisticParts.push({
          id: `prt_${now}`,
          sessionID: id!,
          messageID: optimisticMsgID,
          type: "text",
          text: promptText,
        });
      }
      list.push({
        info: {
          id: optimisticMsgID,
          sessionID: id!,
          role: "user",
          time: { created: now },
          agent: effectiveAgent,
          modelID: effectiveModel?.modelID,
          providerID: effectiveModel?.providerID,
        },
        parts: optimisticParts,
      });
    })
  );

  try {
    await hoxiAPI.sendPromptAsync(id, promptText, {
      agent: effectiveAgent,
      model: effectiveModel,
      variant: workspace.reasoningEffort || undefined,
      directory: sessionDirectory(id),
      attachments,
    });
    // Sync server messages immediately and follow up shortly after
    void loadSessionMessages(id);
    setTimeout(() => {
      void loadSessionMessages(id!);
    }, 900);
  } catch (err) {
    setWorkspace("statusBySession", id, { type: "idle" });
    throw err;
  }

  return id;
}

export async function retryLastUserPrompt(sessionID?: string | null): Promise<void> {
  const id = sessionID ?? workspace.activeSessionID;
  if (!id) return;
  const turns = workspace.messagesBySession[id] ?? [];
  for (let i = turns.length - 1; i >= 0; i--) {
    if (turns[i].info.role === "user") {
      const text = turns[i].parts
        .filter((p): p is Extract<MessagePart, { type: "text" }> => p.type === "text")
        .map((p) => p.text)
        .join("\n")
        .trim();
      if (text) {
        await submitSessionPrompt(text, id);
      }
      return;
    }
  }
}

export function upsertMessageInfo(info: MessageInfo) {
  setWorkspace(
    produce((state) => {
      const list = (state.messagesBySession[info.sessionID] ||= []);
      // Remove any temporary optimistic user message once real user message arrives
      if (info.role === "user" && !info.id.startsWith("optimistic_")) {
        const optIdx = list.findIndex((m) => m.info.id.startsWith("optimistic_"));
        if (optIdx >= 0) {
          list.splice(optIdx, 1);
        }
      }
      const idx = list.findIndex((m) => m.info.id === info.id);
      if (idx >= 0) {
        list[idx].info = info;
      } else {
        list.push({ info, parts: [] });
      }
    })
  );
}

export function upsertMessagePart(part: MessagePart) {
  setWorkspace(
    produce((state) => {
      const list = (state.messagesBySession[part.sessionID] ||= []);
      let turn = list.find((m) => m.info.id === part.messageID);
      if (!turn) {
        turn = {
          info: {
            id: part.messageID,
            sessionID: part.sessionID,
            role: "assistant",
            time: { created: Date.now() },
          },
          parts: [],
        };
        list.push(turn);
      }
      const pIdx = turn.parts.findIndex((p) => p.id === part.id);
      if (pIdx >= 0) {
        turn.parts[pIdx] = part;
      } else {
        turn.parts.push(part);
      }
    })
  );
}

export function applyPartDeltaBatch(
  batches: Array<{
    sessionID: string;
    messageID: string;
    partID: string;
    field: string;
    delta: string;
  }>
) {
  setWorkspace(
    produce((state) => {
      for (const b of batches) {
        const list = state.messagesBySession[b.sessionID];
        if (!list) continue;
        const turn = list.find((m) => m.info.id === b.messageID);
        if (!turn) continue;
        const part = turn.parts.find((p) => p.id === b.partID) as any;
        if (part && typeof part[b.field] === "string") {
          part[b.field] += b.delta;
        }
      }
    })
  );
}
