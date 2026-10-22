// web/src/store/session-revert.ts
// Checkpoint revert / undo-revert and fork-from-message for a session.
import { createSignal } from "solid-js";
import { hoxiAPI } from "../services/api";
import type { MessagePart } from "../services/sse-types";
import { recordUserRevertAction } from "./provider-telemetry";
import { initializeSessionAI, sessionAIConfig } from "./session-ai";
import { isSessionBusy, sessionDirectory } from "./session-query";
import { loadSessionMessages, replaceSession, selectSession } from "./workspace-sessions";
import { setWorkspace, workspace } from "./workspace-state";
import { appSettings } from "./settings";
import { scheduleRefreshWorktreeStatus } from "./session-diffs";
import { extractUserTurnSkillSelection } from "./skills";
import { abortChildSubagentSessions } from "./workspace-session-messages";

export interface PendingRevertRequest {
  sessionID: string;
  messageID: string;
  partID?: string;
  affectedFiles: string[];
}

export const [pendingRevertConfirm, setPendingRevertConfirm] =
  createSignal<PendingRevertRequest | null>(null);

const MUTATING_TOOLS = new Set([
  "edit",
  "write",
  "patch",
  "multiedit",
  "apply_patch",
  "notebookedit",
  "notebook_edit",
  "write_to_file",
  "write_file",
  "create_file",
  "edit_file",
  "replace_file_content",
  "multi_replace_file_content",
  "sed_file",
  "insert_edit_into_file",
  "str_replace_editor",
  "str_replace_based_edit_tool",
]);

function collectAffectedRevertFiles(sessionID: string, messageID: string, partID?: string): string[] {
  const turns = workspace.messagesBySession[sessionID] ?? [];
  const bIdx = turns.findIndex((m) => m.info.id === messageID);
  const slice = bIdx >= 0 ? turns.slice(bIdx) : turns.filter((m) => m.info.id >= messageID);
  const files = new Set<string>();
  slice.forEach((turn, idx) => {
    const start = idx === 0 && partID ? Math.max(0, turn.parts.findIndex((p) => p.id === partID)) : 0;
    for (const p of turn.parts.slice(start)) {
      if (p.type === "patch" && Array.isArray(p.files)) {
        for (const f of p.files) if (f) files.add(f);
      } else if (p.type === "tool" && p.state.status === "completed" && MUTATING_TOOLS.has(p.tool.toLowerCase())) {
        const inp = (p.state.input || {}) as Record<string, any>;
        const fp =
          inp.filePath ||
          inp.filepath ||
          inp.file_path ||
          inp.Path ||
          inp.AbsolutePath ||
          inp.TargetFile ||
          inp.target_file ||
          inp.path ||
          inp.file ||
          inp.File ||
          inp.NotebookPath ||
          inp.notebook_path ||
          (p.state.metadata as any)?.filediff?.file;
        if (typeof fp === "string" && fp.trim()) files.add(fp.trim());
      }
    }
  });
  return Array.from(files);
}

export async function revertToHere(
  sessionID: string,
  messageID: string,
  partID?: string,
  options?: { skipConfirm?: boolean }
): Promise<void> {
  const existingSession = workspace.sessions.find((s) => s.id === sessionID);
  const affectedFiles = collectAffectedRevertFiles(sessionID, messageID, partID);
  if (
    !options?.skipConfirm &&
    appSettings.confirmBeforeRevert &&
    !existingSession?.revert &&
    (affectedFiles.length > 0 || Boolean(partID))
  ) {
    setPendingRevertConfirm({ sessionID, messageID, partID, affectedFiles });
    return;
  }
  setPendingRevertConfirm(null);
  recordUserRevertAction(sessionID, messageID);

  if (!partID) {
    const targetTurn = (workspace.messagesBySession[sessionID] ?? []).find((m) => m.info.id === messageID);
    if (targetTurn && targetTurn.info.role === "user") {
      const text = targetTurn.parts
        .filter((p): p is Extract<MessagePart, { type: "text" }> => p.type === "text")
        .map((p) => p.text)
        .join("\n")
        .trim();
      const attachments = targetTurn.parts
        .filter((p): p is Extract<MessagePart, { type: "file" }> => p.type === "file")
        .map((p) => ({ type: "file" as const, mime: p.mime, filename: p.filename, url: p.url }));
      const skillSelection = extractUserTurnSkillSelection(targetTurn.parts) ?? undefined;
      if (text || attachments.length > 0 || skillSelection) {
        setWorkspace("composerDraftEditBySession", sessionID, { id: `revert_${messageID}`, text, attachments, skillSelection });
      }
    }
  }

  if (existingSession) replaceSession({ ...existingSession, revert: { messageID, partID } });
  if (isSessionBusy(sessionID)) {
    setWorkspace("statusBySession", sessionID, { type: "idle" });
    try { await hoxiAPI.abortSession(sessionID, sessionDirectory(sessionID)); } catch { /* ignore */ }
    void abortChildSubagentSessions(sessionID);
  }
  const info = await hoxiAPI.revertSession(sessionID, messageID, partID, sessionDirectory(sessionID));
  replaceSession(info);
  void loadSessionMessages(sessionID);
  scheduleRefreshWorktreeStatus(sessionID, 120);
}

export async function undoRevert(sessionID: string): Promise<void> {
  setPendingRevertConfirm(null);
  const existingSession = workspace.sessions.find((s) => s.id === sessionID);
  if (existingSession) replaceSession({ ...existingSession, revert: undefined });
  const info = await hoxiAPI.unrevertSession(sessionID, sessionDirectory(sessionID));
  replaceSession(info);
  void loadSessionMessages(sessionID);
  scheduleRefreshWorktreeStatus(sessionID, 120);
}

export async function forkFromMessage(sessionID: string, messageID?: string): Promise<string> {
  setPendingRevertConfirm(null);
  const forked = await hoxiAPI.forkSession(sessionID, messageID, sessionDirectory(sessionID));
  initializeSessionAI(forked.id, sessionAIConfig(sessionID));
  replaceSession(forked);
  await selectSession(forked.id);
  return forked.id;
}
