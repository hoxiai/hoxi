import { batch } from "solid-js";
import { render } from "solid-js/web";
import { produce, unwrap } from "solid-js/store";
import MessageStream from "../components/timeline/MessageStream";
import { hoxiAPI } from "../services/api";
import type { MessageTurn } from "../store/workspace-state";
import { setCommitModelSpec } from "../store/git-commit";
import { submitSessionPrompt } from "../store/session-goal";
import { upsertTurnInfo } from "../store/message-lookup";
import { setWorkspace, workspace } from "../store/workspace-state";
import "../styles.css";

function check(condition: unknown, message: string): asserts condition {
  if (!condition) throw new Error(message);
}

const frame = () => new Promise<void>((resolve) => requestAnimationFrame(() => resolve()));
const settle = () => new Promise<void>((resolve) => setTimeout(resolve, 240));

/** Real DOM regression; run with `npm run test:commit-transition` (Chrome required). */
export async function runBrowserTest(): Promise<string[]> {
  // This dedicated test page must never contact a model, backend, or Git repository.
  window.fetch = async () => new Response("{}", { headers: { "Content-Type": "application/json" } });
  let directGitCalls = 0;
  hoxiAPI.commitWorktree = async () => { directGitCalls++; throw new Error("Unexpected real commit"); };
  hoxiAPI.pushWorktree = async () => { directGitCalls++; throw new Error("Unexpected real push"); };
  setCommitModelSpec("none");
  const results: string[] = [];

  for (const scenario of ["commit", "prompt", "failure", "switch", "local", "reading"] as const) {
    const sid = `ses_transition_${scenario}`;
    const directory = `/probe/.hoxi/worktrees/${sid}`;
    const model = { providerID: "probe", modelID: "probe-model" };
    const diff = { file: "src/task.ts", before: "old", after: "new", additions: 1, deletions: 1 };
    const meta = {
      sessionID: sid, directory, isolated: scenario !== "local", branch: "hoxi/probe",
      baseBranch: "main", dirty: true, changedFiles: 1, aheadCount: 0, behindCount: 0,
      uncommittedLines: 2, additions: 1, deletions: 1,
    };
    const turns: MessageTurn[] = [{
      info: { id: "msg_user", sessionID: sid, role: "user", time: { created: 1 } },
      parts: [{ id: "prt_user", messageID: "msg_user", sessionID: sid, type: "text", text: "Update task" }],
    }, {
      info: { id: "msg_done", sessionID: sid, role: "assistant", time: { created: 2, completed: 3 } },
      parts: [{ id: "prt_done", messageID: "msg_done", sessionID: sid, type: "text", text: "Completed the change\n\n".repeat(30) }, {
        id: "prt_edit", messageID: "msg_done", sessionID: sid, type: "tool", tool: "edit", callID: "edit",
        state: {
          status: "completed", input: { filePath: `${directory}/src/task.ts`, oldString: "old", newString: "new" },
          output: "Updated", title: "Edit task", metadata: { filediff: diff }, time: { start: 2, end: 3 },
        },
      }],
    }];
    let sends = 0;
    hoxiAPI.sendPromptAsync = async (id, _prompt, options) => {
      check(id === sid && options?.directory === directory, "dispatch must retain session and directory");
      sends++;
      if (scenario === "failure") throw new Error("Probe dispatch rejected");
    };
    hoxiAPI.getWorktreeStatus = async () => meta;
    hoxiAPI.getWorktreeDiff = async () => [diff];
    hoxiAPI.getSessionDiff = async () => [diff];
    hoxiAPI.getMessages = async (id) => structuredClone(unwrap(workspace.messagesBySession[id] ?? []));
    batch(() => {
      setWorkspace({ rootDir: "/probe", activeSessionID: sid, connected: true, bootstrappingSessions: false,
        sessions: [{ id: sid, title: "Transition fixture", directory, time: { created: 1, updated: 1 } }] });
      setWorkspace("aiBySession", sid, { model, agent: "build", effort: "" });
      setWorkspace("worktreeBySession", sid, meta);
      setWorkspace("diffsBySession", sid, [diff]);
      setWorkspace("messagesBySession", sid, turns);
      setWorkspace("statusBySession", sid, { type: "idle" });
    });
    const host = document.createElement("div");
    host.style.cssText = "height:650px;width:900px;display:flex;flex-direction:column";
    document.body.append(host);
    const dispose = render(() => <MessageStream />, host);
    const card = () => host.querySelector<HTMLElement>("[data-sandbox-merge-prompt]");
    const reply = () => host.querySelector<HTMLElement>('[data-turn-id="msg_done"]');
    try {
      await settle();
      const originalReply = reply();
      check(originalReply, `${scenario}: missing completed reply`);
      const originalHeight = originalReply.getBoundingClientRect().height;
      const originalCard = card();
      if (scenario === "local") check(!originalCard, "local sessions must not show sandbox merge actions");
      else check(originalCard, `${scenario}: missing sandbox merge actions`);

      await submitSessionPrompt("Continue working", sid).catch(() => {
        if (scenario !== "failure") throw new Error("unexpected dispatch failure");
      });
      // Allow async dispatch staging and Solid effects to finish, before the exit timer.
      for (let i = 0; i < 20 && sends === 0; i++) await Promise.resolve();
      await frame();
      check(sends === 1, `${scenario}: expected exactly one dispatched task`);
      check(reply() === originalReply, `${scenario}: completed reply must not remount`);
      if (scenario === "failure") {
        await settle();
        check(card() === originalCard, "failed dispatch must restore actions without a stale exit");
        check(workspace.statusBySession[sid].type === "idle", "failed dispatch must return to idle");
        results.push("failed dispatch restores actions and reports the error");
        continue;
      }

      check(workspace.statusBySession[sid].type === "busy", `${scenario}: submitted task must remain busy`);
      const firstFrameHeight = originalReply.getBoundingClientRect().height;
      if (originalCard) {
        check(card() === originalCard, `${scenario}: card must survive the first frame instead of disappearing`);
        const transition = originalCard.parentElement!.parentElement!;
        check(transition.inert && transition.getAttribute("aria-hidden") === "true", "outgoing actions must be inaccessible");
        check(getComputedStyle(transition).transitionProperty.includes("grid-template-rows"), "exit must animate layout height");
      }
      if (scenario === "switch") {
        setWorkspace({ activeSessionID: "ses_transition_other", sessions: [...workspace.sessions, {
          id: "ses_transition_other", title: "Other session", directory: "/other", time: { created: 4, updated: 4 },
        }] });
        setWorkspace("messagesBySession", "ses_transition_other", []);
        await settle();
        check(!reply() && !card(), "switching sessions during exit must remove the old card and reply");
        results.push("session switch cancels the outgoing view");
        continue;
      }
      await settle();
      check(!card(), `${scenario}: outgoing merge actions must be removed after the transition`);
      if (originalCard) {
        const finalHeight = originalReply.getBoundingClientRect().height;
        check(originalHeight > finalHeight + 30, "fixture must exercise a meaningful layout collapse");
        check(firstFrameHeight > finalHeight + 1, "the whole card height must not disappear in the first frame");
      }
      const scroller = host.querySelector<HTMLElement>(".overflow-y-auto");
      check(scroller && scroller.scrollHeight - scroller.clientHeight - scroller.scrollTop < 2, "live tail must stay followed");
      let readingTop: number | undefined;
      if (scenario === "reading") {
        scroller.dispatchEvent(new WheelEvent("wheel", { deltaY: -200, bubbles: true }));
        scroller.scrollTop -= 200;
        scroller.dispatchEvent(new Event("scroll"));
        readingTop = scroller.scrollTop;
      }

      // Promote the optimistic user and finish a new reply through the same store plane as SSE.
      const now = Date.now();
      batch(() => {
        setWorkspace(produce((state) => {
          upsertTurnInfo(state.messagesBySession[sid], {
            id: "msg_commit_user", sessionID: sid, role: "user", time: { created: now },
          });
          state.messagesBySession[sid].push({
            info: { id: "msg_commit_done", sessionID: sid, role: "assistant", time: { created: now + 1, completed: now + 2 } },
            parts: [{ id: "prt_commit_done", messageID: "msg_commit_done", sessionID: sid, type: "text", text: "Committed successfully" }],
          });
        }));
        setWorkspace("statusBySession", sid, { type: "idle" });
      });
      await settle();
      check(reply() === originalReply, "finishing a new task must retain the historical reply");
      check(!originalReply.querySelector("[data-sandbox-merge-prompt]"), "historical merge actions must stay hidden after idle");
      if (scenario !== "local") check(card(), "latest completed exchange must offer merge actions again");
      if (readingTop !== undefined) check(Math.abs(scroller.scrollTop - readingTop) < 2, "new output must preserve historical reading position");
      results.push(`${scenario}: smooth exit, single dispatch, live following, and correct historical actions`);
    } finally {
      dispose();
      host.remove();
    }
  }
  check(directGitCalls === 0, "UI transition must never execute Git directly");
  return results;
}
