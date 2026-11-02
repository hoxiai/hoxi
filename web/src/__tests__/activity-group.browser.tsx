import { batch, createSignal } from "solid-js";
import { render } from "solid-js/web";
import { produce } from "solid-js/store";
import { ContextActivityGroupView } from "../components/timeline/message-stream-parts";
import MessageStream from "../components/timeline/MessageStream";
import ThoughtBox from "../components/timeline/ThoughtBox";
import { t } from "../i18n";
import { hoxiAPI } from "../services/api";
import type { MessagePart, ToolPart } from "../services/sse-types";
import { setAppSettings } from "../store/settings";
import { setCommitModelSpec } from "../store/git-commit";
import { upsertTurnInfo, upsertTurnPart } from "../store/message-lookup";
import { setWorkspace, workspace } from "../store/workspace-state";
import { assertEqual, assertTrue } from "./test-harness";
import "../styles.css";

const wait = (ms: number) => new Promise<void>((resolve) => setTimeout(resolve, ms));
const frame = () => new Promise<void>((resolve) => requestAnimationFrame(() => resolve()));

let nextFixture = 0;
function fixture(mode: "auto-collapse" | "expanded" | "collapsed", finalReasoning = false) {
  const sid = `ses_activity_${++nextFixture}`;
  const reasoning: MessagePart = {
    type: "reasoning", id: `prt_reasoning_${sid}`, sessionID: sid, messageID: `msg_${sid}`,
    text: "Reasoning line with enough content to exercise layout\n\n".repeat(30), time: { start: 1 },
  };
  const tool = (index = 0): ToolPart => ({
    type: "tool", id: `prt_tool_${sid}_${index}`, sessionID: sid, messageID: `msg_${sid}`, callID: `call_${index}`, tool: "bash",
    state: { status: "running", input: { command: "git status --short" }, time: { start: 2 }, metadata: {} },
  });
  setAppSettings("reasoningDisplay", mode);
  setWorkspace({ rootDir: "/probe", activeSessionID: sid,
    sessions: [{ id: sid, title: "Activity fixture", directory: "/probe", time: { created: 1, updated: 1 } }] });
  setWorkspace("statusBySession", sid, { type: "busy" });
  const [parts, setParts] = createSignal<MessagePart[]>([reasoning]);
  const [live, setLive] = createSignal(true);
  const [revealKey, setRevealKey] = createSignal<string>();
  const [defaultOpen, setDefaultOpen] = createSignal(finalReasoning);
  const host = document.createElement("div");
  host.style.cssText = "width:900px";
  document.body.append(host);
  let dispose = () => {};
  const mount = () => { dispose = render(() => (
    <ContextActivityGroupView parts={parts()} isLiveTail={live()} revealKey={revealKey()} defaultOpenReasoning={defaultOpen()} />
  ), host); };
  mount();
  const group = () => host.querySelector<HTMLElement>("[data-activity-group]")!;
  const header = () => group().querySelector<HTMLButtonElement>(":scope > button")!;
  const body = () => host.querySelector<HTMLElement>("[data-activity-group-body]");
  return { sid, reasoning, tool, host, setParts, setLive, setRevealKey, setDefaultOpen, group, header, body,
    remount: () => { dispose(); mount(); }, cleanup: () => { dispose(); host.remove(); } };
}

async function testCompactTransition() {
  const f = fixture("auto-collapse");
  try {
    await wait(60);
    const group = f.group(), header = f.header();
    const before = group.getBoundingClientRect().height;
    assertEqual(header.getAttribute("aria-expanded"), "false");
    assertTrue(!f.body(), "a closed group should not mount heavy step cards");
    f.setParts([f.reasoning, f.tool()]);
    await frame();
    assertTrue(f.group() === group && f.header() === header, "the first tool must retain the reasoning group's DOM");
    assertTrue(!f.body(), "starting a tool must not briefly expand the group");
    assertTrue(Math.abs(group.getBoundingClientRect().height - before) < 2, "the compact header should keep its height");
    f.setParts([f.reasoning, { ...f.tool(), state: {
      status: "completed", input: { command: "git status --short" }, output: "clean", title: "Git status", metadata: {}, time: { start: 2, end: 3 },
    } }]);
    batch(() => { f.setLive(false); setWorkspace("statusBySession", f.sid, { type: "idle" }); });
    await frame();
    assertTrue(f.header() === header && !f.body(), "completion must not replace or expand the header");
  } finally { f.cleanup(); }
}

async function testExpandedTransition() {
  const f = fixture("expanded");
  try {
    await wait(80);
    const group = f.group(), body = f.body();
    const reasoning = f.host.querySelector(`[data-part-id="${f.reasoning.id}"]`);
    const before = group.getBoundingClientRect().height;
    assertTrue(Boolean(body && reasoning && before > 100), "fixture must begin with expanded reasoning");
    f.setParts([f.reasoning, f.tool()]);
    await frame();
    assertTrue(f.group() === group && f.body() === body, "the first tool must retain the expanded container");
    assertTrue(f.host.querySelector(`[data-part-id="${f.reasoning.id}"]`) === reasoning, "the reasoning card must not remount");
    assertEqual(f.header().getAttribute("aria-expanded"), "true");
    assertTrue(group.getBoundingClientRect().height >= before - 2, "the first tool must not retract expanded reasoning");
    batch(() => { f.setLive(false); setWorkspace("statusBySession", f.sid, { type: "idle" }); });
    await frame();
    assertTrue(f.body() === body, "finishing a task must retain the expanded body");
  } finally { f.cleanup(); }
}

async function testCollapseAndReopen() {
  const f = fixture("auto-collapse");
  try {
    f.setParts([f.reasoning, f.tool()]);
    f.header().click(); await wait(80);
    const body = f.body()!;
    const before = body.getBoundingClientRect().height;
    assertTrue(before > 30);
    f.header().click();
    await frame();
    assertTrue(f.body() === body, "closing must not synchronously unmount the drawer");
    assertTrue(body.inert && body.getAttribute("aria-hidden") === "true", "closing steps must not remain interactive");
    assertTrue(body.getBoundingClientRect().height > 0, "collapse must animate instead of instantly dropping to zero");
    f.header().click();
    await wait(250);
    assertTrue(f.body() === body && !body.inert, "reopening must cancel the exit and keep the existing cards");
    f.header().click();
    await wait(260);
    assertTrue(!f.body(), "closed step cards should be released after the animation");
    f.remount(); await frame();
    assertEqual(f.header().getAttribute("aria-expanded"), "false", "manual collapse survives remounts");
    setAppSettings("reasoningDisplay", "expanded"); await frame();
    assertEqual(f.header().getAttribute("aria-expanded"), "false", "global settings must respect a manual collapse");
  } finally { f.cleanup(); }
}

async function testExpandAnimates() {
  const f = fixture("auto-collapse");
  try {
    f.setParts([f.reasoning, f.tool()]);
    await frame();
    f.header().click();
    await frame();
    const body = f.body()!;
    const opening = body.getBoundingClientRect().height;
    await wait(320);
    const settled = body.getBoundingClientRect().height;
    assertTrue(settled > 30, "an opened group should show its steps");
    assertTrue(opening < settled * 0.6,
      `expanding must grow from closed, not jump: ${Math.round(opening)}px of ${Math.round(settled)}px on the first frame`);
  } finally { f.cleanup(); }
}

async function testThoughtBoxFirstExpandAnimates() {
  setAppSettings("reasoningDisplay", "auto-collapse");
  const host = document.createElement("div");
  host.style.cssText = "width:900px";
  document.body.append(host);
  const part: Extract<MessagePart, { type: "reasoning" }> = {
    type: "reasoning", id: "prt_thought_first_open", sessionID: "ses_thought_idle", messageID: "msg_thought",
    text: "A finished line of reasoning\n\n".repeat(20), time: { start: 1, end: 2 },
  };
  const dispose = render(() => <ThoughtBox part={part} />, host);
  try {
    await frame();
    host.querySelector<HTMLButtonElement>("button")!.click();
    await frame();
    const body = host.querySelector<HTMLElement>("div.grid")!;
    assertTrue(Boolean(body), "the first open should mount the reasoning body");
    const opening = body.getBoundingClientRect().height;
    await wait(320);
    const settled = body.getBoundingClientRect().height;
    assertTrue(settled > 30, "an opened thought should show its text");
    assertTrue(opening < settled * 0.6,
      `the first expand must grow from closed, not jump: ${Math.round(opening)}px of ${Math.round(settled)}px on the first frame`);
  } finally { dispose(); host.remove(); }
}

async function testSearchReveal() {
  const f = fixture("collapsed");
  try {
    f.setParts([f.reasoning, ...Array.from({ length: 60 }, (_, index) => f.tool(index))]);
    f.setRevealKey("search:1"); await wait(80);
    assertEqual(f.header().getAttribute("aria-expanded"), "true", "search must reveal a closed activity group");
    assertEqual(f.host.querySelectorAll("[data-part-id]").length, 61, "search must reveal earlier steps beyond the default page");
    f.header().click(); await wait(260);
    f.setRevealKey("search:2"); await wait(80);
    assertTrue(Boolean(f.body()), "navigating to another match must reopen a manually collapsed group");
  } finally { f.cleanup(); }
}

async function testFinalReasoning() {
  const f = fixture("auto-collapse", true);
  try {
    await wait(80);
    const body = f.body();
    const before = f.group().getBoundingClientRect().height;
    assertTrue(Boolean(body && before > 100), "a reasoning-only final answer must remain readable by default");
    batch(() => { f.setDefaultOpen(false); f.setParts([f.reasoning, f.tool()]); });
    await frame();
    assertTrue(f.body() === body, "a late first tool must not retract the final reasoning");
    assertTrue(f.group().getBoundingClientRect().height >= before - 2);
  } finally { f.cleanup(); }
}

async function testCommitExecutionStream() {
  const f = fixture("auto-collapse");
  f.cleanup();
  const model = { providerID: "probe", modelID: "mini" };
  const directory = "/probe";
  const diff = { file: "task.ts", before: "old", after: "new", additions: 1, deletions: 1 };
  setWorkspace({ connected: true, bootstrappingSessions: false,
    providers: [{ id: model.providerID, name: "Probe", models: { mini: { id: "mini" } } }] });
  setWorkspace("aiBySession", f.sid, { model, agent: "build", effort: "" });
  setWorkspace("statusBySession", f.sid, { type: "idle" });
  setWorkspace("worktreeBySession", f.sid, { sessionID: f.sid, directory, isolated: false });
  setWorkspace("diffsBySession", f.sid, [diff]);
  setWorkspace("messagesBySession", f.sid, [{
    info: { id: "msg_before_user", sessionID: f.sid, role: "user", time: { created: 1 } }, parts: [],
  }, {
    info: { id: "msg_before_reply", sessionID: f.sid, role: "assistant", time: { created: 2, completed: 3 } },
    parts: [{ type: "tool", tool: "edit", id: "prt_edit", sessionID: f.sid, messageID: "msg_before_reply", callID: "edit", state: {
      status: "completed", input: { filePath: "/probe/task.ts", oldString: "old", newString: "new" },
      output: "updated", title: "Edit task", metadata: { filediff: diff }, time: { start: 2, end: 3 },
    } }],
  }]);
  let dispatched = 0;
  hoxiAPI.sendPromptAsync = async () => { dispatched++; };
  hoxiAPI.getWorktreeStatus = async () => ({ sessionID: f.sid, directory, isolated: false, dirty: true, changedFiles: 1, additions: 1, deletions: 1, uncommittedLines: 2 });
  setCommitModelSpec("none");
  const host = document.createElement("div");
  host.style.cssText = "width:900px;height:650px;display:flex;flex-direction:column";
  document.body.append(host);
  const dispose = render(() => <MessageStream />, host);
  try {
    await wait(80);
    const commit = Array.from(host.querySelectorAll("button")).find((button) => button.textContent?.trim() === t("timeline.gitQuickCommit"));
    assertTrue(Boolean(commit), "fixture must offer the actual timeline commit action");
    commit!.click(); await frame(); await frame();
    assertEqual(dispatched, 1);
    const now = Date.now();
    setWorkspace(produce((state) => {
      const turns = state.messagesBySession[f.sid];
      upsertTurnInfo(turns, { id: "msg_commit_user", sessionID: f.sid, role: "user", time: { created: now } });
      upsertTurnInfo(turns, { id: `msg_${f.sid}`, sessionID: f.sid, role: "assistant", time: { created: now + 1 } });
      upsertTurnPart(turns, f.reasoning);
    }));
    await frame(); await frame();
    const group = host.querySelector(`[data-activity-group="${f.reasoning.id}"]`)!;
    assertTrue(Boolean(group), "streamed reasoning must mount the activity container");
    setWorkspace(produce((state) => upsertTurnPart(state.messagesBySession[f.sid], f.tool())));
    await frame();
    assertTrue(host.querySelector(`[data-activity-group="${f.reasoning.id}"]`) === group, "commit's first tool must retain the reasoning container");
    assertTrue(!group.querySelector("[data-activity-group-body]"), "commit execution must not auto-expand steps");
    batch(() => {
      setWorkspace(produce((state) => {
        const turns = state.messagesBySession[f.sid];
        upsertTurnPart(turns, { ...f.tool(), state: {
          status: "completed", input: { command: "git status --short" }, output: "clean", title: "Git status", metadata: {}, time: { start: 2, end: 3 },
        } });
        upsertTurnPart(turns, { type: "text", id: "prt_final", sessionID: f.sid, messageID: `msg_${f.sid}`, text: "Commit completed" });
        upsertTurnInfo(turns, { id: `msg_${f.sid}`, sessionID: f.sid, role: "assistant", time: { created: now + 1, completed: now + 2 } });
      }));
      setWorkspace("statusBySession", f.sid, { type: "idle" });
    });
    await wait(260);
    assertTrue(host.querySelector(`[data-activity-group="${f.reasoning.id}"]`) === group, "commit result must retain the activity header");
    const scroller = host.querySelector<HTMLElement>(".timeline-result-scroll")!;
    assertTrue(scroller.scrollHeight - scroller.clientHeight - scroller.scrollTop < 2, "streaming and completion must preserve live tail following");
    assertTrue(host.textContent?.includes("Commit completed"));
    assertEqual(workspace.statusBySession[f.sid].type, "idle");
  } finally { dispose(); host.remove(); }
}

export async function runBrowserTest(): Promise<string[]> {
  window.fetch = async () => new Response("{}", { headers: { "Content-Type": "application/json" } });
  hoxiAPI.getSessionDiff = async () => [];
  hoxiAPI.getWorktreeDiff = async () => [];
  const results: string[] = [];
  for (const [name, run] of [
    ["compact reasoning-to-tool transition keeps its header and height", testCompactTransition],
    ["expanded reasoning survives the first tool and task completion", testExpandedTransition],
    ["collapse animates, rapid reopen cancels exit, and manual choices persist", testCollapseAndReopen],
    ["expanding a closed group grows instead of jumping open", testExpandAnimates],
    ["a thought's first expand grows instead of jumping open", testThoughtBoxFirstExpandAnimates],
    ["search reveals closed groups and all earlier steps", testSearchReveal],
    ["reasoning-only final output stays readable when a tool arrives late", testFinalReasoning],
    ["timeline commit keeps the activity container through reasoning, tools, and its result", testCommitExecutionStream],
  ] as const) { await run(); results.push(name); }
  return results;
}
