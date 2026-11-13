import { render } from "solid-js/web";
import { produce } from "solid-js/store";
import ComposerShelf from "../components/timeline/ComposerShelf";
import { hoxiAPI } from "../services/api";
import { t } from "../i18n";
import {
  collectCurrentTaskTurns, noteSessionHistoryTail, removeSessionFromStore,
  setWorkspace, workspace, type MessageTurn,
} from "../store/workspace";
import { assertEqual, assertTrue } from "./test-harness";

const tick = () => new Promise<void>((resolve) => setTimeout(resolve, 0));

function message(sid: string, index: number, role: "user" | "assistant" = "assistant", continueTask = false): MessageTurn {
  return { info: { id: `msg_${String(index).padStart(5, "0")}`, sessionID: sid, role,
    time: { created: index + 1 }, ...(continueTask ? { continueTask: true } : {}) }, parts: [] };
}

function earlyHistory(sid: string): MessageTurn[] {
  const edit = message(sid, 1);
  edit.parts.push({ type: "tool", id: "prt_early", sessionID: sid, messageID: edit.info.id,
    tool: "edit", callID: "edit", state: { status: "completed",
      input: { filePath: "src/early.ts", oldString: "before", newString: "after" },
      output: "", title: "", metadata: {}, time: { start: 1, end: 2 } } });
  return [message(sid, 0, "user"), edit];
}

function seed(sid: string): void {
  setWorkspace({ rootDir: "/repo", activeSessionID: sid,
    sessions: [{ id: sid, directory: "/repo", title: "Long task", time: { created: 1, updated: 1 } }],
    pendingPermissions: [], pendingQuestions: [],
  });
  setWorkspace("messagesBySession", sid, Array.from({ length: 200 }, (_, i) => message(sid, i + 200)));
  setWorkspace("statusBySession", sid, { type: "busy" });
  noteSessionHistoryTail(sid, 200, 200, 0);
}

export async function runBrowserTest(): Promise<string[]> {
  const originalMessages = hoxiAPI.getMessages;
  const sid = "ses_hoxi_composer_changes";
  const otherID = `${sid}_other`;
  const host = document.createElement("div");
  document.body.append(host);
  let dispose: (() => void) | undefined;
  const pill = () => [...host.querySelectorAll<HTMLButtonElement>("button")]
    .find((button) => button.textContent?.includes(t("timeline.filesChangedCount", { count: 1 })));
  try {
    seed(sid);
    let calls = 0;
    hoxiAPI.getMessages = async (id, directory, limit, before) => {
      calls++;
      assertEqual(id, sid);
      assertEqual(directory, "/repo");
      assertEqual(limit, 200);
      assertEqual(before, "msg_00200");
      return earlyHistory(sid);
    };
    dispose = render(() => <ComposerShelf />, host);
    await tick();
    assertTrue(Boolean(pill()), "the composer must show edits before the resident history page");
    assertEqual(workspace.messagesBySession[sid].length, 200);
    pill()!.click();
    assertTrue(host.querySelector('[role="dialog"]')?.textContent?.includes("early.ts") ?? false);
    setWorkspace(produce((state) => { state.messagesBySession[sid].push(message(sid, 400)); }));
    await tick();
    assertEqual(calls, 1, "assistant streaming must not restart the missing-history read");
    assertTrue(Boolean(pill()));
    setWorkspace(produce((state) => { state.messagesBySession[sid].push(message(sid, 401, "user", true)); }));
    await tick();
    assertTrue(Boolean(pill()), "an explicit continuation keeps the task's earlier edits");
    assertEqual(calls, 1, "steering must retain the already loaded task prefix");
    setWorkspace("statusBySession", sid, { type: "idle" });
    await tick();
    assertTrue(!pill(), "the transient chip hides after the task finishes");
    assertEqual(collectCurrentTaskTurns(sid).some((turn) => turn.info.id === "msg_00001"), false,
      "idle releases the independently loaded task context");
    setWorkspace(produce((state) => { state.messagesBySession[sid].push(message(sid, 402, "user")); }));
    setWorkspace("statusBySession", sid, { type: "busy" });
    await tick();
    assertTrue(!pill(), "a new task must not inherit the previous task's edits");
    dispose();
    dispose = undefined;

    seed(sid);
    let finish!: (turns: MessageTurn[]) => void;
    hoxiAPI.getMessages = () => new Promise((resolve) => { finish = resolve; });
    dispose = render(() => <ComposerShelf />, host);
    await tick();
    setWorkspace("sessions", (sessions) => [...sessions, { id: otherID, directory: "/repo", title: "Other task",
      time: { created: 1, updated: 1 } }]);
    setWorkspace("messagesBySession", otherID, [message(otherID, 500, "user")]);
    setWorkspace("statusBySession", otherID, { type: "busy" });
    setWorkspace("activeSessionID", otherID);
    finish(earlyHistory(sid));
    await tick();
    assertTrue(!pill(), "late history from the previous session must not populate the new composer's chip");
    assertEqual(collectCurrentTaskTurns(sid).some((turn) => turn.info.id === "msg_00001"), false);
    return [
      "paged early edits appear in the composer popover while the chat remains at 200 messages",
      "streaming reuses history; continue retains edits; idle and new tasks release them",
      "switching sessions discards late task-history responses",
    ];
  } finally {
    dispose?.();
    host.remove();
    removeSessionFromStore(sid);
    removeSessionFromStore(otherID);
    hoxiAPI.getMessages = originalMessages;
  }
}
