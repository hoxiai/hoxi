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
import { submitSessionPrompt } from "../store/session-goal";
import { upsertTurnInfo, upsertTurnPart } from "../store/message-lookup";
import { setWorkspace, workspace } from "../store/workspace-state";
import { assertEqual, assertTrue } from "./test-harness";
import "../styles.css";

const wait = (ms: number) => new Promise<void>((resolve) => setTimeout(resolve, ms));
const frame = () => new Promise<void>((resolve) => requestAnimationFrame(() => resolve()));
const waitFor = async (predicate: () => boolean, timeoutMs = 2000, message = "condition timed out") => {
  const deadline = performance.now() + timeoutMs;
  while (performance.now() < deadline) {
    if (predicate()) return;
    await wait(25);
  }
  assertTrue(predicate(), message);
};

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
  const f = fixture("collapsed");
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
    assertTrue(Boolean(body && reasoning && before > 40), "fixture must begin with level-1 expanded and level-2 compact");
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
  const f = fixture("collapsed");
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
    await wait(60);
    assertTrue(f.body() === body, "drawer must stay mounted while the 200ms exit transition is in flight");
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
  const f = fixture("collapsed");
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

function readStep(sid: string, index: number, file: string | undefined, status: "pending" | "running" | "completed"): ToolPart {
  return {
    type: "tool", id: `prt_read_${sid}_${index}`, sessionID: sid, messageID: `msg_${sid}`, callID: `read_${index}`, tool: "read",
    state: { status, input: file ? { filePath: file } : {}, time: status === "completed" ? { start: 2, end: 3 } : { start: 2 }, metadata: {} },
  } as ToolPart;
}

async function testPendingStepKeepsTarget() {
  const f = fixture("collapsed");
  const thought = { ...f.reasoning, time: { start: 1, end: 2 } } as MessagePart;
  const detail = () => f.header().querySelector("[data-component='text-reveal']")?.getAttribute("aria-label") ?? "";
  try {
    f.setParts([thought, readStep(f.sid, 0, "/probe/alpha.ts", "running")]);
    await wait(1100); // the bar swaps at most once a second, counting the mount
    assertEqual(detail(), "alpha.ts");
    // The next read starts before its arguments arrive.
    f.setParts([thought, readStep(f.sid, 0, "/probe/alpha.ts", "completed"), readStep(f.sid, 1, undefined, "pending")]);
    await frame();
    assertEqual(detail(), "alpha.ts", "a step without its target yet must not blank the collapsed bar");
    f.setParts([thought, readStep(f.sid, 0, "/probe/alpha.ts", "completed"), readStep(f.sid, 1, "/probe/beta.ts", "running")]);
    await wait(1300);
    assertEqual(detail(), "beta.ts", "the bar moves on once the new target is known");
  } finally { f.cleanup(); }
}

async function testParallelStepsNameAKnownTarget() {
  const f = fixture("collapsed");
  const thought = { ...f.reasoning, time: { start: 1, end: 2 } } as MessagePart;
  try {
    f.setParts([thought, readStep(f.sid, 0, "/probe/alpha.ts", "running"), readStep(f.sid, 1, undefined, "pending")]);
    await wait(1100);
    const detail = f.header().querySelector("[data-component='text-reveal']")?.getAttribute("aria-label");
    assertEqual(detail, "alpha.ts (+1)", "parallel steps should name a step whose target is known, never a lone count");
  } finally { f.cleanup(); }
}

async function testRapidStepsSwapCalmly() {
  const f = fixture("collapsed");
  const thought = { ...f.reasoning, time: { start: 1, end: 2 } } as MessagePart;
  const detail = () => f.header().querySelector<HTMLElement>("[data-component='text-reveal']");
  try {
    f.setParts([thought, readStep(f.sid, 0, "/probe/a0.ts", "running")]);
    await wait(1200);
    let swaps = 0;
    const observer = new MutationObserver((records) => {
      for (const record of records) {
        if ((record.target as HTMLElement).getAttribute("data-swapping") === "true") swaps++;
      }
    });
    observer.observe(f.group(), { subtree: true, attributes: true, attributeFilter: ["data-swapping"] });
    const done: ToolPart[] = [];
    for (let i = 1; i <= 4; i++) {
      done.push(readStep(f.sid, i - 1, `/probe/a${i - 1}.ts`, "completed"));
      f.setParts([thought, ...done, readStep(f.sid, i, `/probe/a${i}.ts`, "running")]);
      await wait(60);
    }
    observer.disconnect();
    assertTrue(swaps <= 1, `four steps in 240ms should swap the bar at most once, swapped ${swaps} times`);
    await wait(1300);
    assertEqual(detail()?.getAttribute("aria-label"), "a4.ts", "the bar settles on the latest step");
  } finally { f.cleanup(); }
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
    assertTrue(Boolean(body && before > 40), "level-1 stays expanded while level-2 reasoning stays compact by default");
    batch(() => { f.setDefaultOpen(false); f.setParts([f.reasoning, f.tool()]); });
    await frame();
    assertTrue(f.body() === body, "a late first tool must not retract the activity group");
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
  const host = document.createElement("div");
  host.style.cssText = "width:900px;height:650px;display:flex;flex-direction:column";
  document.body.append(host);
  const dispose = render(() => <MessageStream />, host);
  try {
    await wait(80);
    await submitSessionPrompt("Continue task", f.sid);
    await frame(); await frame();
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
    assertTrue(Boolean(group.querySelector("[data-activity-group-body]")), "live tool execution in auto-collapse mode expands the step drawer");
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

async function testCompletedStepMetadataDiffStatsUpdate() {
  const sid = `ses_activity_diff_${++nextFixture}`;
  setAppSettings("reasoningDisplay", "collapsed");
  setWorkspace({
    rootDir: "/probe",
    activeSessionID: sid,
    sessions: [{ id: sid, title: "Diff update fixture", directory: "/probe", time: { created: 1, updated: 1 } }],
  });
  setWorkspace("statusBySession", sid, { type: "busy" });
  setWorkspace("messagesBySession", sid, [
    {
      info: { id: `msg_${sid}`, sessionID: sid, role: "assistant", time: { created: 1 } },
      parts: [
        {
          type: "tool",
          id: "prt_edit_1",
          sessionID: sid,
          messageID: `msg_${sid}`,
          callID: "call_edit_1",
          tool: "edit",
          state: {
            status: "completed",
            input: { filePath: "/probe/src/index.ts", oldString: "a", newString: "b" },
            output: "ok",
            title: "Edit src/index.ts",
            metadata: { filediff: { file: "/probe/src/index.ts", additions: 1, deletions: 0 } },
            time: { start: 1, end: 2 },
          },
        },
        {
          type: "tool",
          id: "prt_read_2",
          sessionID: sid,
          messageID: `msg_${sid}`,
          callID: "call_read_2",
          tool: "read",
          state: {
            status: "completed",
            input: { filePath: "/probe/src/index.ts" },
            output: "ok",
            title: "Read src/index.ts",
            metadata: {},
            time: { start: 2, end: 3 },
          },
        },
      ],
    },
  ]);

  const host = document.createElement("div");
  host.style.cssText = "width:900px";
  document.body.append(host);
  const dispose = render(
    () => (
      <ContextActivityGroupView
        parts={[...workspace.messagesBySession[sid][0].parts]}
        isLiveTail={false}
      />
    ),
    host
  );
  try {
    await frame();
    const header = host.querySelector<HTMLButtonElement>("[data-activity-group] > button")!;
    assertTrue(header.textContent?.includes("+1"), `initial collapsed bar should show +1, got ${header.textContent}`);

    // Late metadata corrects the completed edit step's additions from +1 to +9.
    setWorkspace(
      produce((state) => {
        const editPart = state.messagesBySession[sid][0].parts[0];
        if (editPart.type === "tool") {
          editPart.state.metadata = {
            ...editPart.state.metadata,
            filediff: { file: "/probe/src/index.ts", additions: 9, deletions: 0 },
          };
        }
      })
    );
    await frame();
    assertTrue(header.textContent?.includes("+9"), `collapsed bar must immediately update to +9, got ${header.textContent}`);

    // Appending a subsequent step must preserve the corrected +9 stat.
    setWorkspace(
      produce((state) => {
        state.messagesBySession[sid][0].parts.push({
          type: "tool",
          id: "prt_bash_3",
          sessionID: sid,
          messageID: `msg_${sid}`,
          callID: "call_bash_3",
          tool: "bash",
          state: {
            status: "completed",
            input: { command: "npm test" },
            output: "ok",
            title: "npm test",
            metadata: {},
            time: { start: 3, end: 4 },
          },
        });
      })
    );
    await frame();
    assertTrue(header.textContent?.includes("+9"), `collapsed bar must retain +9 after appending next step, got ${header.textContent}`);
  } finally {
    dispose();
    host.remove();
  }
}

async function testAutoCollapseOnlyAfterScrollingOffscreen() {
  const sid = `ses_activity_offscreen_${++nextFixture}`;
  setAppSettings("reasoningDisplay", "auto-collapse");
  setWorkspace({
    rootDir: "/Users/hugh/code/hoxi/shopark",
    activeSessionID: sid,
    sessions: [{ id: sid, title: "Offscreen auto-collapse", directory: "/Users/hugh/code/hoxi/shopark", time: { created: 1, updated: 1 } }],
  });
  setWorkspace("statusBySession", sid, { type: "busy" });

  const baseNow = Date.now();
  const makeShell = (
    index: number,
    command: string,
    status: "pending" | "running" | "completed" = "completed"
  ): ToolPart => ({
    type: "tool",
    id: `prt_exp_${sid}_${index}`,
    sessionID: sid,
    messageID: `msg_${sid}`,
    callID: `call_exp_${index}`,
    tool: "bash",
    state:
      status === "pending"
        ? { status: "pending", input: command ? { command } : {}, raw: "" }
        : status === "running"
          ? { status: "running", input: { command }, metadata: {}, time: { start: baseNow + index * 10 } }
          : {
              status: "completed",
              input: { command },
              output: "ok",
              title: command,
              metadata: {},
              time: { start: baseNow + index * 10, end: baseNow + index * 10 + 50 },
            },
  });

  const [parts, setParts] = createSignal<MessagePart[]>([
    {
      type: "reasoning",
      id: `prt_reason_noise_${sid}_1`,
      sessionID: sid,
      messageID: `msg_${sid}`,
      text: "Internal monologue before ssh",
      time: { start: baseNow, end: baseNow + 5 },
    },
    {
      type: "tool",
      id: `prt_todo_noise_${sid}_1`,
      sessionID: sid,
      messageID: `msg_${sid}`,
      callID: `call_todo_1`,
      tool: "todowrite",
      state: {
        status: "completed",
        input: { todos: [{ id: "1", content: "Check decor", status: "in_progress" }] },
        output: "ok",
        title: "Update todos",
        metadata: {},
        time: { start: baseNow + 5, end: baseNow + 8 },
      },
    },
    makeShell(
      1,
      "ssh -o BatchMode=yes -o ConnectTimeout=10 root@8.135.58.57 'cd /opt/gopanel && sudo -u gopanel env'"
    ),
    {
      type: "reasoning",
      id: `prt_reason_noise_${sid}_2`,
      sessionID: sid,
      messageID: `msg_${sid}`,
      text: "Internal monologue between tools",
      time: { start: baseNow + 15, end: baseNow + 18 },
    },
    {
      type: "text",
      id: `prt_transitional_${sid}_1`,
      sessionID: sid,
      messageID: `msg_${sid}`,
      text: "Let me inspect the theme decor service next:",
    },
    makeShell(
      2,
      "cd /Users/hugh/code/hoxi/shopark && sed -n '1,120p' go-fast/app/service/theme_decor.go"
    ),
    makeShell(
      3,
      `cd /Users/hugh/code/hoxi/shopark && rg -n "ErrDecorPublishedMissing" --glob '*.go' go-fast | head -10`
    ),
    makeShell(
      4,
      "cd /Users/hugh/code/hoxi/shopark && sed -n '340,420p' go-fast/app/service/theme_decor_bundle.go",
      "running"
    ),
  ]);
  const [live, setLive] = createSignal(true);
  const [activeGroup, setActiveGroup] = createSignal(true);

  const scroller = document.createElement("div");
  scroller.className = "timeline-result-scroll";
  scroller.style.cssText = "width:900px;height:220px;overflow-y:auto;position:relative";
  const content = document.createElement("div");
  const tailSpacer = document.createElement("div");
  tailSpacer.style.cssText = "height:600px";
  scroller.append(content, tailSpacer);
  document.body.append(scroller);

  const dispose = render(
    () => <ContextActivityGroupView parts={parts()} isLiveTail={live()} isActiveGroup={activeGroup()} />,
    content
  );

  try {
    await waitFor(
      () =>
        Boolean(scroller.textContent?.includes("root@8.135.58.57 · sudo -u gopanel env")) &&
        Boolean(scroller.textContent?.includes("Let me inspect the theme decor service next:")),
      2000,
      "transitional text explanation and de-noised tools should finish streaming reveal"
    );
    const group = scroller.querySelector<HTMLElement>("[data-activity-group]")!;
    const header = group.querySelector<HTMLButtonElement>(":scope > button")!;
    assertEqual(header.getAttribute("aria-expanded"), "true", "live tool steps expand automatically in auto-collapse mode");
    assertEqual(
      getComputedStyle(header).position,
      "sticky",
      "expanded first-level header stays sticky while scrolling a long activity group"
    );
    assertTrue(
      !header.querySelector("[data-component='text-reveal']"),
      "expanded header must show group summary instead of duplicating the last running step"
    );
    assertTrue(
      Boolean(scroller.textContent?.includes("root@8.135.58.57 · sudo -u gopanel env")) &&
        Boolean(scroller.textContent?.includes("Let me inspect the theme decor service next:")) &&
        Boolean(scroller.textContent?.includes("Internal monologue before ssh")),
      "transitional text explanation, compact reasoning thought rows, and de-noised tools are visible"
    );
    assertEqual(
      scroller.querySelectorAll("[data-part-id]").length,
      7,
      "all 4 tool steps, 2 compact reasoning headers, and 1 transitional text explanation render while hiding todowrite"
    );

    // Finish execution while still visible on screen: must NOT collapse or jump.
    const heightWhileRunning = group.getBoundingClientRect().height;
    batch(() => {
      setParts([
        ...parts().slice(0, -1),
        makeShell(
          4,
          "cd /Users/hugh/code/hoxi/shopark && sed -n '340,420p' go-fast/app/service/theme_decor_bundle.go",
          "completed"
        ),
      ]);
      setLive(false);
      setWorkspace("statusBySession", sid, { type: "idle" });
    });
    await wait(120);
    assertEqual(
      header.getAttribute("aria-expanded"),
      "true",
      "completed group must remain expanded while still in the viewport"
    );
    const heightAfterFinish = group.getBoundingClientRect().height;
    assertTrue(
      Math.abs(heightAfterFinish - heightWhileRunning) < 4,
      `completing execution on screen must cause zero height collapse (${heightWhileRunning} -> ${heightAfterFinish})`
    );

    // Scroll down so the completed group leaves the top of the viewport:
    // level-1 remains stably expanded while it is still the latest group.
    scroller.scrollTop = 450;
    await wait(60);
    assertEqual(
      header.getAttribute("aria-expanded"),
      "true",
      "completed group stays expanded until a newer activity group starts"
    );

    // Once a newer activity group starts, the offscreen completed group folds
    // in 0ms and compensates scrollTop so the visible viewport does not jump.
    const spacerTopBefore = tailSpacer.getBoundingClientRect().top;
    setActiveGroup(false);
    await frame();
    assertEqual(
      header.getAttribute("aria-expanded"),
      "false",
      "completed group folds automatically when a newer activity group starts"
    );
    const spacerTopAfter = tailSpacer.getBoundingClientRect().top;
    assertTrue(
      Math.abs(spacerTopAfter - spacerTopBefore) < 2,
      `offscreen auto-collapse must preserve viewport position (${spacerTopBefore} -> ${spacerTopAfter})`
    );
  } finally {
    dispose();
    scroller.remove();
  }
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
    ["a step waiting for its arguments keeps the previous target", testPendingStepKeepsTarget],
    ["parallel steps name a known target instead of a bare count", testParallelStepsNameAKnownTarget],
    ["rapid steps swap the collapsed bar at most once per interval", testRapidStepsSwapCalmly],
    ["search reveals closed groups and all earlier steps", testSearchReveal],
    ["reasoning-only final output stays readable when a tool arrives late", testFinalReasoning],
    ["timeline commit keeps the activity container through reasoning, tools, and its result", testCommitExecutionStream],
    ["completed step metadata corrections update collapsed diff stats immediately and across appends", testCompletedStepMetadataDiffStatsUpdate],
    ["auto-collapse expands live steps without header duplication and collapses only after scrolling offscreen", testAutoCollapseOnlyAfterScrollingOffscreen],
  ] as const) { await run(); results.push(name); }
  return results;
}
