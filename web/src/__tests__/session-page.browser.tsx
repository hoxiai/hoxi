import { batch } from "solid-js";
import { render } from "solid-js/web";
import { produce, unwrap } from "solid-js/store";
import Composer from "../components/timeline/Composer";
import MessageStream from "../components/timeline/MessageStream";
import MarkdownView from "../components/timeline/MarkdownView";
import { createSignal } from "solid-js";
import SandboxMergePromptCard from "../components/timeline/SandboxMergePromptCard";
import { downloadSessionExport } from "../components/timeline/session-export";
import { t } from "../i18n";
import { hoxiAPI } from "../services/api";
import { clearPendingDeepLink, openSessionDeepLink, startDeepLinkNavigation } from "../store/deep-link";
import { setCommitModelSpec } from "../store/git-commit";
import { noteSessionHistoryTail } from "../store/session-history";
import { hydratedSessionIDs } from "../store/session-transcripts";
import { setWorkspace, workspace, type MessageTurn } from "../store/workspace-state";
import { assertEqual, assertTrue } from "./test-harness";
import "../styles.css";

const wait = (ms: number) => new Promise<void>((resolve) => setTimeout(resolve, ms));
const frame = () => new Promise<void>((resolve) => requestAnimationFrame(() => requestAnimationFrame(() => resolve())));
function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (error: Error) => void;
  const promise = new Promise<T>((yes, no) => { resolve = yes; reject = no; });
  return { promise, resolve, reject };
}
function button(root: ParentNode, label: string): HTMLButtonElement {
  const found = Array.from(root.querySelectorAll("button")).find((element) =>
    element.textContent?.trim() === label || element.title === label || element.getAttribute("aria-label") === label);
  assertTrue(Boolean(found), `Missing button: ${label}`);
  return found!;
}
function input(element: HTMLInputElement | HTMLTextAreaElement, value: string) {
  element.value = value;
  element.dispatchEvent(new Event("input", { bubbles: true }));
}
function host() {
  const element = document.createElement("div");
  element.style.cssText = "height:650px;width:900px;display:flex;flex-direction:column";
  document.body.append(element);
  return element;
}
function turn(id: string, sessionID: string, text: string): MessageTurn {
  return { info: { id, sessionID, role: "assistant", time: { created: 2, completed: 3 } },
    parts: [{ type: "text", id: `prt_${id}`, sessionID, messageID: id, text }] };
}

let nextFixture = 0;
function fixture() {
  const sid = `ses_page_a_${++nextFixture}`, other = `ses_page_b_${nextFixture}`;
  const directory = `/probe/.hoxi/worktrees/${sid}`;
  const model = { providerID: "probe", modelID: "mini" };
  const diff = { file: "src/task.ts", before: "old", after: "new", additions: 1, deletions: 1 };
  const meta = { sessionID: sid, directory, isolated: true, branch: "hoxi/probe", baseBranch: "main",
    dirty: true, changedFiles: 1, uncommittedLines: 2, additions: 1, deletions: 1 };
  batch(() => {
    setWorkspace({ rootDir: "/probe", activeSessionID: sid, connected: true, bootstrappingSessions: false,
      sessions: [
        { id: sid, title: "Session A", directory, time: { created: 1, updated: 1 } },
        { id: other, title: "Session B", directory: "/other", time: { created: 1, updated: 1 } },
      ], providers: [{ id: "probe", name: "Probe", models: { mini: { id: "mini", name: "Mini" } } }],
      globalConfig: { model: "probe/mini", small_model: "probe/mini" } });
    for (const id of [sid, other]) {
      setWorkspace("aiBySession", id, { model, agent: "build", effort: "" });
      setWorkspace("statusBySession", id, { type: "idle" });
      setWorkspace("messagesBySession", id, [turn(`msg_${id}`, id, "Completed the change\n\n".repeat(40))]);
      hydratedSessionIDs.add(id);
    }
    setWorkspace("worktreeBySession", sid, meta);
    setWorkspace("diffsBySession", sid, [diff]);
  });
  hoxiAPI.getWorktreeStatus = async () => meta;
  hoxiAPI.getWorktreeDiff = async () => [diff];
  hoxiAPI.getSessionDiff = async () => [diff];
  hoxiAPI.listPermissions = async () => [];
  hoxiAPI.listQuestions = async () => [];
  hoxiAPI.getMessages = async (id) => structuredClone(unwrap(workspace.messagesBySession[id] ?? []));
  return { sid, other, directory, diff };
}

async function testCommitInput() {
  const { sid, diff } = fixture();
  setCommitModelSpec("probe/mini");
  const requests: ReturnType<typeof deferred<Awaited<ReturnType<typeof hoxiAPI.generateCommitMessage>>>>[] = [];
  hoxiAPI.generateCommitMessage = () => { const request = deferred<Awaited<ReturnType<typeof hoxiAPI.generateCommitMessage>>>(); requests.push(request); return request.promise; };
  const root = host();
  const dispose = render(() => <SandboxMergePromptCard sessionID={sid} messageID="msg_done" changedFiles={[diff]} />, root);
  try {
    await frame();
    button(root, t("timeline.sandboxMergeCommitExitBtn", { branch: "main" })).click();
    await frame();
    const field = root.querySelector("input")!;
    assertEqual(requests.length, 1);
    input(field, "fix: manually reviewed message");
    requests[0].resolve({ ok: true, message: "chore: late AI message" });
    await frame();
    assertEqual(field.value, "fix: manually reviewed message", "AI must not overwrite manual edits");
    const regenerate = () => button(root, t("timeline.gitCommitModelLabel")).click();
    regenerate(); regenerate();
    assertEqual(requests.length, 2, "regeneration must not dispatch twice while pending");
    requests[1].resolve({ ok: true, message: "fix: latest generated message" });
    await frame();
    assertEqual(field.value, "fix: latest generated message", "explicit regeneration still works");
    regenerate();
    const beforeEdit = field.value;
    input(field, "temporary edit");
    input(field, beforeEdit);
    requests[2].resolve({ ok: true, message: "late result after edits" });
    await frame();
    assertEqual(field.value, beforeEdit, "typing back to the previous value still counts as editing");
  } finally { dispose(); root.remove(); }
}

async function testEnhancement() {
  const { sid, other } = fixture();
  const requests: ReturnType<typeof deferred<Awaited<ReturnType<typeof hoxiAPI.enhancePrompt>>>>[] = [];
  hoxiAPI.enhancePrompt = () => { const request = deferred<Awaited<ReturnType<typeof hoxiAPI.enhancePrompt>>>(); requests.push(request); return request.promise; };
  const root = host();
  const dispose = render(() => <Composer />, root);
  const field = () => root.querySelector("textarea")!;
  const enhance = async () => {
    button(root, t("timeline.composerToolsTooltip")).click();
    await frame();
    button(root, t("timeline.enhancePrompt")).click();
    await frame();
  };
  const hasPreview = () => root.textContent?.includes(t("timeline.enhancedPromptTitle"));
  try {
    await frame();
    input(field(), "A original"); await enhance();
    setWorkspace("activeSessionID", other); await frame();
    input(field(), "B draft"); await enhance();
    assertEqual(requests.length, 2, "B can start while A's stale request is pending");
    requests[0].resolve({ ok: true, prompt: "A enhanced" }); await frame();
    assertTrue(!hasPreview(), "A's suggestion must not appear in B");
    assertEqual(field().value, "B draft");
    // A's finally block must not clear B's pending state.
    button(root, t("timeline.composerToolsTooltip")).click(); await frame();
    assertTrue(button(root, t("timeline.enhancingPrompt")).disabled, "B remains pending after A completes");
    button(root, t("timeline.composerToolsTooltip")).click();
    requests[1].resolve({ ok: true, prompt: "B enhanced" }); await frame();
    assertTrue(hasPreview(), "the current suggestion should appear");
    button(root, t("timeline.enhanceAccept")).click();
    assertEqual(field().value, "B enhanced");

    input(field(), "B second draft"); await enhance();
    input(field(), "B edited while waiting");
    requests[2].resolve({ ok: true, prompt: "stale B enhancement" }); await frame();
    assertTrue(!hasPreview(), "editing the draft must invalidate a pending enhancement");
    assertEqual(field().value, "B edited while waiting");
    await enhance();
    setWorkspace("activeSessionID", sid); await frame();
    setWorkspace("activeSessionID", other); await frame();
    requests[3].reject(new Error("Stale failure")); await frame();
    assertTrue(!root.textContent?.includes("Stale failure"), "stale failures must not leak after switching away and back");
    await enhance();
    requests[4].reject(new Error("Current failure")); await frame();
    assertTrue(root.textContent?.includes("Current failure"), "current failures must remain visible");
  } finally { dispose(); root.remove(); }
}

async function testExport() {
  const { sid } = fixture();
  noteSessionHistoryTail(sid, 200, 200, 0);
  const originalCreate = URL.createObjectURL;
  const originalClick = HTMLAnchorElement.prototype.click;
  const blobs: Blob[] = [];
  let downloads = 0;
  URL.createObjectURL = (blob) => { blobs.push(blob as Blob); return "blob:session-page-test"; };
  HTMLAnchorElement.prototype.click = function () { downloads++; };
  try {
    for (const format of ["markdown", "json"] as const) {
      hoxiAPI.getMessages = async () => { throw new Error("Full history unavailable"); };
      const failed = downloadSessionExport(sid, format);
      await frame();
      const dialog = document.querySelector('[role="dialog"][aria-modal="true"]')!;
      assertTrue(dialog?.textContent?.includes("Full history unavailable"), "export failure must be shown");
      assertEqual(dialog.querySelectorAll("button").length, 1, "error only needs a close button");
      if (format === "markdown") {
        let propagated = false;
        const keyListener = () => { propagated = true; };
        window.addEventListener("keydown", keyListener);
        try {
          button(dialog, t("common.close")).dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true, cancelable: true }));
          assertTrue(!propagated, "closing an error must not trigger the timeline's Escape-to-stop handler");
        } finally { window.removeEventListener("keydown", keyListener); }
      } else button(dialog, t("common.close")).click();
      assertEqual(await failed, false);
      assertEqual(downloads, 0, "a failed full read must not download a partial file");
    }
    const full = [turn("msg_old", sid, "Older content"), turn("msg_tail", sid, "Newest content")];
    hoxiAPI.getMessages = async (id, _directory, limit) => {
      assertEqual(id, sid); assertEqual(limit, "all"); return full;
    };
    assertEqual(await downloadSessionExport(sid, "json"), true);
    assertEqual(JSON.parse(await blobs[0].text()).turns.length, 2);
    assertEqual(await downloadSessionExport(sid, "markdown"), true);
    const markdown = await blobs[1].text();
    assertTrue(markdown.includes("Older content") && markdown.includes("Newest content"));
    assertEqual(downloads, 2);
  } finally { URL.createObjectURL = originalCreate; HTMLAnchorElement.prototype.click = originalClick; }
}

async function testNavigation() {
  const { sid, other } = fixture();
  setWorkspace("statusBySession", other, { type: "busy" });
  const originalScrollIntoView = HTMLElement.prototype.scrollIntoView;
  const focused: Array<string | undefined> = [];
  HTMLElement.prototype.scrollIntoView = function () { focused.push(this.dataset.turnId); };
  const root = host();
  let dispose = render(() => <MessageStream />, root);
  const focus = (sessionID: string, messageID: string) => window.dispatchEvent(new CustomEvent("hoxi:focus-message", { detail: { sessionID, messageID } }));
  const growAndCheckFollowing = async () => {
    setWorkspace(produce((state) => {
      const part = state.messagesBySession[other][0].parts[0];
      if (part.type === "text") part.text = "More live output\n\n".repeat(100);
    }));
    await frame();
    const scroller = root.querySelector<HTMLElement>(".timeline-result-scroll")!;
    assertTrue(scroller.scrollHeight - scroller.clientHeight - scroller.scrollTop < 2, "stale focus must not stop B's live following");
  };
  try {
    await wait(240);
    focus(sid, `msg_${sid}`);
    await frame();
    setWorkspace("activeSessionID", other);
    await wait(550);
    assertEqual(focused.length, 1, "A's delayed retry must be canceled on a session switch");
    focus(sid, `msg_${sid}`);
    focus(other, "msg_missing");
    await growAndCheckFollowing();
    focus(other, `msg_${other}`); await wait(550);
    assertEqual(focused.length, 3, "valid focus keeps its layout retry");
    focus(other, `msg_${other}`); await frame();
    const beforeUnmount = focused.length;
    dispose(); dispose = () => {};
    await wait(500);
    assertEqual(focused.length, beforeUnmount, "unmount must cancel the pending retry");

    // Also cover the store's earlier, 350ms focus dispatch after selectSession.
    let events = 0;
    const onFocus = () => { events++; };
    window.addEventListener("hoxi:focus-message", onFocus);
    hoxiAPI.consumeDesktopIntent = async () => null;
    const stopDeepLinks = startDeepLinkNavigation();
    try {
      openSessionDeepLink({ sessionID: sid, messageID: `msg_${sid}` });
      await frame();
      setWorkspace("activeSessionID", other);
      await wait(400);
      assertEqual(events, 0, "deep-link store must not dispatch focus into a different session");
      openSessionDeepLink({ sessionID: sid, messageID: `msg_${sid}` });
      await frame();
      setWorkspace("activeSessionID", other); await frame();
      setWorkspace("activeSessionID", sid); await wait(400);
      assertEqual(events, 0, "switching away and back must invalidate the old focus request");
      openSessionDeepLink({ sessionID: other, messageID: `msg_${other}` });
      await wait(400);
      assertEqual(events, 1, "a valid deep link must still dispatch focus");
    } finally { stopDeepLinks(); window.removeEventListener("hoxi:focus-message", onFocus); clearPendingDeepLink(); }
  } finally { dispose(); root.remove(); HTMLElement.prototype.scrollIntoView = originalScrollIntoView; }
}

async function testStreamedTableRowsAppearWhole() {
  const rows = ["| `ToolCard.tsx` | renders tool steps |", "| `ThoughtBox.tsx` | shows reasoning |", "| `tail-hold.ts` | keeps height |"];
  const doc = `Files:\n\n| File | Role |\n|---|---|\n${rows.join("\n")}\n\nDone.\n`;
  const cells = (line: string) => line.split("|").slice(1, -1).map((cell) => cell.trim().replace(/`/g, ""));
  const fullRows = new Set(rows.map((line) => cells(line).join("|")));
  const [content, setContent] = createSignal("");
  const [streaming, setStreaming] = createSignal(true);
  const element = host();
  const dispose = render(() => <MarkdownView content={content()} streaming={streaming()} />, element);
  const problems: string[] = [];
  let checking = true;
  const check = () => {
    if (!checking) return;
    for (const p of element.querySelectorAll("p")) {
      if (p.textContent?.trim().startsWith("|")) problems.push(`table text shown as a paragraph: ${p.textContent}`);
    }
    for (const tr of element.querySelectorAll("tbody tr")) {
      const text = Array.from(tr.querySelectorAll("td")).map((td) => td.textContent?.trim() ?? "").join("|");
      if (!fullRows.has(text)) problems.push(`partial row rendered: ${text}`);
    }
    requestAnimationFrame(check);
  };
  requestAnimationFrame(check);
  try {
    for (let i = 0; i < doc.length; i += 4) { setContent(doc.slice(0, i + 4)); await wait(40); }
    await wait(600);
    setStreaming(false);
    await wait(300);
    checking = false;
    assertEqual(element.querySelectorAll("tbody tr").length, rows.length, "every row is shown once the stream ends");
    assertEqual(problems.length, 0, `rows must appear whole while streaming: ${problems.slice(0, 3).join("; ")}`);
  } finally { checking = false; dispose(); element.remove(); }
}

async function testScrollUpRevealsEarlierTurnsWithoutBlankTop() {
  const { sid } = fixture();
  const turns: MessageTurn[] = [];
  for (let i = 0; i < 6; i++) {
    turns.push({
      info: { id: `u_${i}`, sessionID: sid, role: "user", time: { created: i * 100 + 1, completed: i * 100 + 1 } },
      parts: [{ type: "text", id: `p_u_${i}`, sessionID: sid, messageID: `u_${i}`, text: `Prompt ${i}\n\n` + "User line\n".repeat(4) }],
    });
    for (let s = 0; s < 15; s++) {
      turns.push({
        info: { id: `a_${i}_s_${s}`, sessionID: sid, role: "assistant", time: { created: i * 100 + s + 2, completed: i * 100 + s + 3 } },
        parts: [{
          type: "tool", id: `t_${i}_${s}`, callID: `c_${i}_${s}`, sessionID: sid, messageID: `a_${i}_s_${s}`, tool: "read",
          state: { status: "completed", input: { filePath: `src/file_${s}.ts` }, output: "ok", title: "read", metadata: {}, time: { start: 1, end: 2 } },
        }],
      });
    }
    turns.push(turn(`a_${i}_final`, sid, `Answer ${i}\n\n` + "Detailed paragraph content.\n\n".repeat(8)));
  }
  setWorkspace("messagesBySession", sid, turns);
  const root = host();
  const dispose = render(() => <MessageStream />, root);
  try {
    await wait(240);
    await frame();
    const scroller = root.querySelector<HTMLElement>(".timeline-result-scroll")!;
    assertTrue(scroller.scrollHeight > scroller.clientHeight, "initial window must have scrollable height");
    assertTrue(!root.querySelector('[data-turn-id="u_0"]'), "oldest prompt starts unmounted");
    // Simulate scrolling up into the top threshold zone: earlier turns must mount
    // immediately without getting stuck on the 15 folded 0-height tool turns per cycle.
    scroller.scrollTop = 280;
    scroller.dispatchEvent(new Event("scroll"));
    await frame();
    await frame();
    assertTrue(Boolean(root.querySelector('[data-turn-id="u_1"]')), "first top-scroll batch reveals 4 earlier user prompts across 60+ folded tool turns");
    assertTrue(scroller.scrollTop > 800, "scrollTop is compensated when earlier turns prepend above the viewport");
    scroller.scrollTop = 280;
    scroller.dispatchEvent(new Event("scroll"));
    await frame();
    await frame();
    assertTrue(Boolean(root.querySelector('[data-turn-id="u_0"]')), "scrolling up again reveals the oldest user prompt");
  } finally {
    dispose();
    root.remove();
  }
}

export async function runBrowserTest(): Promise<string[]> {
  window.fetch = async () => new Response("{}", { headers: { "Content-Type": "application/json" } });
  hoxiAPI.commitWorktree = async () => { throw new Error("Unexpected real commit"); };
  hoxiAPI.pushWorktree = async () => { throw new Error("Unexpected real push"); };
  const results: string[] = [];
  for (const [name, run] of [
    ["commit input survives late and superseded AI results", testCommitInput],
    ["enhancement stays bound to the session and unchanged draft", testEnhancement],
    ["export errors never save partial files; retry saves full JSON and Markdown", testExport],
    ["message focus cancels stale work and preserves valid navigation", testNavigation],
    ["a streamed table shows only whole rows", testStreamedTableRowsAppearWhole],
    ["scrolling up reveals earlier turns across folded tool runs and compensates scrollTop", testScrollUpRevealsEarlierTurnsWithoutBlankTop],
  ] as const) { await run(); results.push(name); }
  return results;
}
