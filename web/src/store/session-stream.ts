// web/src/store/session-stream.ts
// Applies /api/hoxi/event message frames (info / part / delta batches) to the store.
import { produce } from "solid-js/store";
import type { StreamFrameBatch } from "../services/sse";
import type { MessageInfo, MessagePart } from "../services/sse-types";
import { setWorkspace, workspace } from "./workspace-state";
import { ensureChildSubagentSessionLoaded } from "./workspace-sessions";
import { extractChildSubagentSessionId } from "./subagent-sessions";
import {
  recordAssistantTurnUpdated,
  recordFirstToken,
  recordToolPartUpdate,
} from "./provider-telemetry";
import { scheduleRefreshWorktreeStatus } from "./session-diffs";
import { findMessageIndex, findPartIndex, upsertTurnInfo, upsertTurnPart } from "./message-lookup";
import { touchSessionTranscript } from "./session-transcripts";

function isMutatingTool(toolName?: string): boolean {
  const t = (toolName || "").toLowerCase();
  if (!t || t === "todowrite" || t === "todoread" || t === "read" || t.endsWith("_read")) {
    return false;
  }
  return (
    t === "edit" ||
    t === "write" ||
    t === "patch" ||
    t === "multiedit" ||
    t === "apply_patch" ||
    t === "bash" ||
    t === "shell" ||
    t === "create_file" ||
    t.includes("edit") ||
    t.includes("write") ||
    t.includes("patch") ||
    t.includes("replace") ||
    t.includes("bash") ||
    t.includes("shell")
  );
}

function isTaskLikeTool(toolName?: string): boolean {
  const t = (toolName || "").toLowerCase();
  return t === "task" || t === "agent" || t.includes("subagent");
}

function belongsToActiveSession(sessionID: string): boolean {
  const activeID = workspace.activeSessionID;
  if (!activeID || !sessionID) return false;
  if (sessionID === activeID) return true;
  let cur: string | undefined = sessionID;
  const seen = new Set<string>();
  while (cur && !seen.has(cur)) {
    seen.add(cur);
    const s = workspace.sessions.find((item) => item.id === cur);
    if (!s?.parentID) break;
    if (s.parentID === activeID) return true;
    cur = s.parentID;
  }
  for (const turn of workspace.messagesBySession[activeID] ?? []) {
    for (const p of turn.parts) {
      if (
        p.type === "tool" &&
        isTaskLikeTool(p.tool) &&
        extractChildSubagentSessionId(p) === sessionID
      ) {
        return true;
      }
    }
  }
  return false;
}

function mutateUpsertInfo(state: typeof workspace, info: MessageInfo) {
  touchSessionTranscript(info.sessionID);
  const list = (state.messagesBySession[info.sessionID] ||= []);
  upsertTurnInfo(list, info);
}

function mutateUpsertPart(state: typeof workspace, part: MessagePart) {
  touchSessionTranscript(part.sessionID);
  const list = (state.messagesBySession[part.sessionID] ||= []);
  upsertTurnPart(list, part);
}

export function upsertMessageInfo(info: MessageInfo) {
  recordAssistantTurnUpdated(info);
  setWorkspace(produce((state) => mutateUpsertInfo(state, info)));
}

export function upsertMessagePart(part: MessagePart) {
  recordFirstToken(part.sessionID);
  recordToolPartUpdate(part);
  if (part.type === "tool" && part.tool === "hoxi_browser" &&
      part.state.status === "running" && part.sessionID === workspace.activeSessionID) {
    setWorkspace({ inspectorOpen: true, inspectorTab: "browser" });
  }
  if (part.type === "tool" && part.tool === "hoxi_computer" &&
      part.state.status === "running" && part.sessionID === workspace.activeSessionID) {
    setWorkspace({ inspectorOpen: true, inspectorTab: "computer" });
  }
  if (part.type === "tool" && isTaskLikeTool(part.tool)) {
    ensureChildSubagentSessionLoaded(
      extractChildSubagentSessionId(part),
      part.sessionID,
      part.state.status === "completed" || part.state.status === "error"
    );
  }
  if (
    part.type === "tool" &&
    (part.state.status === "pending" ||
      part.state.status === "running" ||
      part.state.status === "completed") &&
    workspace.activeSessionID &&
    isMutatingTool(part.tool) &&
    belongsToActiveSession(part.sessionID)
  ) {
    scheduleRefreshWorktreeStatus(workspace.activeSessionID, 250);
  }
  setWorkspace(produce((state) => mutateUpsertPart(state, part)));
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
  for (const b of batches) {
    recordFirstToken(b.sessionID);
  }
  setWorkspace(
    produce((state) => {
      for (const b of batches) {
        const list = state.messagesBySession[b.sessionID];
        if (!list) continue;
        const turn = list[findMessageIndex(list, b.messageID)];
        if (!turn) continue;
        appendPartDelta(turn.parts[findPartIndex(turn.parts, b.partID)], b.field, b.delta);
      }
    })
  );
}

function appendPartDelta(part: MessagePart | undefined, field: string, delta: string) {
  if (!part) return;
  // Older Runner snapshots can omit empty text, including snapshots loaded by REST.
  if (field === "text" && (part.type === "text" || part.type === "reasoning") && part.text == null) {
    part.text = "";
  }
  const fields = part as unknown as Record<string, unknown>;
  if (typeof fields[field] === "string") fields[field] += delta;
}

export function applyStreamFrameBatch(batch: StreamFrameBatch) {
  for (const info of batch.infos) {
    recordAssistantTurnUpdated(info);
  }

  let shouldRefreshActiveWorktree = false;
  for (const part of batch.parts) {
    recordFirstToken(part.sessionID);
    recordToolPartUpdate(part);
    if (
      part.type === "tool" &&
      part.tool === "hoxi_browser" &&
      part.state.status === "running" &&
      part.sessionID === workspace.activeSessionID
    ) {
      setWorkspace({ inspectorOpen: true, inspectorTab: "browser" });
    }
    if (
      part.type === "tool" &&
      part.tool === "hoxi_computer" &&
      part.state.status === "running" &&
      part.sessionID === workspace.activeSessionID
    ) {
      setWorkspace({ inspectorOpen: true, inspectorTab: "computer" });
    }
    if (part.type === "tool" && isTaskLikeTool(part.tool)) {
      ensureChildSubagentSessionLoaded(
        extractChildSubagentSessionId(part),
        part.sessionID,
        part.state.status === "completed" || part.state.status === "error"
      );
    }
    if (
      part.type === "tool" &&
      (part.state.status === "pending" ||
        part.state.status === "running" ||
        part.state.status === "completed") &&
      isMutatingTool(part.tool) &&
      belongsToActiveSession(part.sessionID)
    ) {
      shouldRefreshActiveWorktree = true;
    }
  }
  if (shouldRefreshActiveWorktree && workspace.activeSessionID) {
    scheduleRefreshWorktreeStatus(workspace.activeSessionID, 250);
  }

  for (const d of batch.deltas) {
    recordFirstToken(d.sessionID);
  }

  setWorkspace(
    produce((state) => {
      for (const info of batch.infos) {
        mutateUpsertInfo(state, info);
      }
      for (const part of batch.parts) {
        mutateUpsertPart(state, part);
      }
      for (const b of batch.deltas) {
        const list = state.messagesBySession[b.sessionID];
        if (!list) continue;
        const turn = list[findMessageIndex(list, b.messageID)];
        if (!turn) continue;
        appendPartDelta(turn.parts[findPartIndex(turn.parts, b.partID)], b.field, b.delta);
      }
    })
  );
}
