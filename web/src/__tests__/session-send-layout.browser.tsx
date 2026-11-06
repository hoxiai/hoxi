import { batch } from "solid-js";
import { render } from "solid-js/web";
import { unwrap } from "solid-js/store";
import ThreePaneShell from "../components/layout/ThreePaneShell";
import { hoxiAPI } from "../services/api";
import { setLocale, t } from "../i18n";
import { setAppSettings } from "../store/settings";
import { setLeftNavOpen } from "../store/layout";
import { upsertMessageInfo, upsertMessagePart } from "../store/workspace";
import { setWorkspace, workspace, type MessageTurn } from "../store/workspace-state";
import { assertEqual, assertTrue } from "./test-harness";
import "../styles.css";

const wait = (ms: number) => new Promise<void>((resolve) => setTimeout(resolve, ms));
const frame = () => new Promise<void>((resolve) => requestAnimationFrame(() => resolve()));

async function checkSend(busy: boolean, width: number, completedTools = 1, emptyTail = false) {
  const sid = `ses_send_layout_${busy}_${width}_${completedTools}_${emptyTail}`;
  const model = { providerID: "probe", modelID: "mini" };
  const turns: MessageTurn[] = [{
    info: { id: "msg_user", sessionID: sid, role: "user", time: { created: 1 } },
    parts: [{ type: "text", id: "prt_user", sessionID: sid, messageID: "msg_user", text: "Previous task" }],
  }, {
    info: { id: "msg_work", sessionID: sid, role: "assistant", time: { created: 2, ...(busy ? {} : { completed: 3 }) } },
    parts: [...Array.from({ length: completedTools }, (_, index): MessageTurn["parts"][number] => ({
      type: "tool", tool: "read", callID: `call_read_${index}`, id: `prt_read_${sid}_${index}`, sessionID: sid, messageID: "msg_work",
      state: { status: "completed", input: { filePath: `/probe/task_${index}.ts` }, title: "Read task", output: "task contents", metadata: {}, time: { start: 2, end: 3 } },
    })), { type: "text", id: "prt_reply", sessionID: sid, messageID: "msg_work", text: "Existing reply paragraph.\n\n".repeat(completedTools > 1 ? 1 : 30) }],
  }];
  if (emptyTail) turns.push({
    info: { id: "msg_empty", sessionID: sid, role: "assistant", time: { created: 4, completed: 5 } }, parts: [],
  });
  const meta = { sessionID: sid, directory: "/probe", isolated: emptyTail, dirty: emptyTail, changedFiles: emptyTail ? 1 : 0,
    uncommittedLines: emptyTail ? 2 : 0, additions: emptyTail ? 1 : 0, deletions: emptyTail ? 1 : 0 };
  hoxiAPI.getWorktreeStatus = async () => meta;
  batch(() => {
    setWorkspace({ rootDir: "/probe", activeSessionID: sid, connected: true, bootstrappingSessions: false,
      inspectorOpen: false, sessions: [{ id: sid, title: "Layout fixture", directory: "/probe", time: { created: 1, updated: 3 } }],
      providers: [{ id: "probe", name: "Probe", models: { mini: { id: "mini", name: "Claude Sonnet 4.5 (Extended Thinking)" } } }] });
    setWorkspace("aiBySession", sid, { model, agent: "build", effort: "" });
    setWorkspace("worktreeBySession", sid, meta);
    setWorkspace("messagesBySession", sid, turns);
    setWorkspace("statusBySession", sid, { type: busy ? "busy" : "idle" });
  });
  setLeftNavOpen(false);
  setAppSettings("reasoningDisplay", "auto-collapse");
  setAppSettings("approvalMode", "trusted");
  let sends = 0;
  hoxiAPI.sendPromptAsync = async () => { sends++; };
  hoxiAPI.getMessages = async () => structuredClone(unwrap(workspace.messagesBySession[sid] ?? []));
  hoxiAPI.getSessionDiff = async () => [];
  hoxiAPI.getWorktreeDiff = async () => [];
  const root = document.createElement("div");
  document.body.append(root);
  const dispose = render(() => <ThreePaneShell />, root);
  try {
    root.querySelector<HTMLElement>("[data-surface]")!.style.width = `${width}px`;
    await wait(250);
    const island = root.querySelector<HTMLElement>("section.pane-island")!;
    const scroller = root.querySelector<HTMLElement>(".timeline-result-scroll")!;
    const composer = root.querySelector<HTMLElement>(".chat-composer-glass")!;
    const toolbar = composer.querySelector<HTMLElement>(".composer-toolbar")!;
    assertEqual(getComputedStyle(toolbar).flexDirection, width === 1100 ? "row" : "column",
      "toolbar rows should depend on pane width instead of draft contents");
    const input = composer.querySelector("textarea")!;
    input.value = "One short follow-up";
    input.dispatchEvent(new Event("input", { bubbles: true }));
    await frame();
    const original = root.querySelector('[data-turn-id="msg_work"]')!;
    const paragraph = original.querySelector("p")!;
    const footer = emptyTail ? root.querySelector('[data-turn-id="msg_empty"]') : undefined;
    if (emptyTail) assertTrue(Boolean(footer?.querySelector("[data-sandbox-merge-prompt]")), "empty completed tail should own the task summary and merge actions");
    const before = { top: composer.getBoundingClientRect().top, height: scroller.clientHeight, paragraph: paragraph.textContent };
    const button = Array.from(composer.querySelectorAll("button"))
      .find((item) => busy ? item.title === t("timeline.queueSend") : item.title.startsWith(`${t("timeline.send")} (`))!;
    button.focus(); button.click();
    if (!busy && completedTools > 1) {
      assertTrue(
        !original.querySelector("[data-activity-group-body]"),
        "completed activity group must fold synchronously in the same render pass as send"
      );
      assertTrue(
        scroller.scrollHeight - scroller.clientHeight - scroller.scrollTop < 2,
        "scrollTop must be pinned to the post-collapse height synchronously on send"
      );
    }
    const samples: Array<{ top: number; height: number; distance: number }> = [];
    for (let i = 0; i < 22; i++) {
      if (!busy && i === 5) {
        const now = Date.now();
        batch(() => {
          upsertMessageInfo({ id: "msg_ack", sessionID: sid, role: "user", time: { created: now } });
          upsertMessagePart({ type: "text", id: "prt_ack", messageID: "msg_ack", sessionID: sid, text: "One short follow-up" });
          upsertMessageInfo({ id: "msg_next", sessionID: sid, role: "assistant", time: { created: now + 1 } });
          upsertMessagePart({ type: "text", id: "prt_next", messageID: "msg_next", sessionID: sid, text: "Starting the next task" });
        });
      }
      await frame();
      if (!busy && completedTools > 1 && i === 0) {
        assertTrue(
          !original.querySelector("[data-activity-group-body]"),
          "completed activity group must fold in the same frame when following the live tail"
        );
      }
      assertTrue(root.querySelector("section.pane-island") === island, "sending must retain the page island");
      assertTrue(root.querySelector('[data-turn-id="msg_work"]') === original, "sending must retain the historical reply");
      if (emptyTail) assertTrue(root.querySelector('[data-turn-id="msg_empty"]') === footer,
        "resending must retain the completed empty tail's task summary and allow its merge actions to animate out");
      assertTrue(original.querySelector("p") === paragraph && paragraph.textContent === before.paragraph,
        "sending must retain historical Markdown DOM and text");
      assertTrue(scroller.clientHeight > 0 && scroller.getClientRects().length > 0, "sending must never hide the transcript");
      assertTrue(toolbar.scrollWidth <= toolbar.clientWidth + 1, "toolbar controls must fit without horizontal clipping");
      samples.push({ top: composer.getBoundingClientRect().top, height: scroller.clientHeight,
        distance: scroller.scrollHeight - scroller.clientHeight - scroller.scrollTop });
    }
    assertEqual(sends, busy ? 0 : 1);
    const maxShift = Math.max(...samples.map((sample) => Math.max(Math.abs(sample.top - before.top), Math.abs(sample.height - before.height))));
    assertTrue(maxShift < 2, `short send must not resize the entire transcript: ${JSON.stringify({ busy, width, maxShift })}`);
    const maxDistance = Math.max(...samples.map((sample) => sample.distance));
    assertTrue(maxDistance < 2, `completed-task resend must retain the live tail on every frame: ${JSON.stringify({ busy, width, completedTools, maxDistance, samples })}`);
  } finally { dispose(); root.remove(); }
}

export async function runBrowserTest(): Promise<string[]> {
  window.fetch = async () => new Response("{}", { headers: { "Content-Type": "application/json" } });
  const results: string[] = [];
  for (const language of ["en-US", "zh-CN"] as const) {
    setLocale(language);
    for (const busy of [false, true]) for (const width of [560, 780, 1100]) {
      await checkSend(busy, width);
      results.push(`${language}: ${busy ? "busy" : "idle"} short send retains the full page at ${width}px`);
    }
  }
  await checkSend(false, 780, 25);
  results.push("resending after a completed multi-step task retains the live tail on every frame");
  await checkSend(false, 780, 1, true);
  results.push("resending retains the footer of a completed task with an empty final assistant step");
  return results;
}
