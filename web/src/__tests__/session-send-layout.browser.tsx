import { batch } from "solid-js";
import { render } from "solid-js/web";
import { unwrap } from "solid-js/store";
import ThreePaneShell from "../components/layout/ThreePaneShell";
import { hoxiAPI } from "../services/api";
import { setLocale, t } from "../i18n";
import { applyFontScaleToDOM, setAppSettings } from "../store/settings";
import { setLeftNavOpen } from "../store/layout";
import { upsertMessageInfo, upsertMessagePart } from "../store/workspace";
import { setWorkspace, workspace, type MessageTurn } from "../store/workspace-state";
import { assertEqual, assertTrue } from "./test-harness";
import "../styles.css";

const wait = (ms: number) => new Promise<void>((resolve) => setTimeout(resolve, ms));
const frame = () => new Promise<void>((resolve) => requestAnimationFrame(() => resolve()));

function assertToolbarGeometry(toolbar: HTMLElement, context: string) {
  assertTrue(
    toolbar.scrollWidth <= toolbar.clientWidth + 1,
    `toolbar controls must fit without horizontal clipping (${context}): scrollWidth=${toolbar.scrollWidth} clientWidth=${toolbar.clientWidth}`
  );
  const buttons = Array.from(toolbar.querySelectorAll<HTMLButtonElement>("button")).filter((btn) => {
    const rect = btn.getBoundingClientRect();
    return rect.width > 0 && rect.height > 0;
  });
  assertTrue(buttons.length > 0, `toolbar should render visible buttons (${context})`);
  for (const btn of buttons) {
    const rect = btn.getBoundingClientRect();
    assertTrue(
      btn.scrollHeight <= btn.clientHeight + 1,
      `toolbar button must not overflow vertically (${context}, "${btn.textContent?.trim() || btn.title}"): scrollHeight=${btn.scrollHeight} clientHeight=${btn.clientHeight}`
    );
    assertTrue(
      rect.height <= 34,
      `toolbar button text must stay single-line (${context}, "${btn.textContent?.trim() || btn.title}"): height=${rect.height}`
    );
  }
  if (getComputedStyle(toolbar).flexDirection === "row") {
    const centers = buttons.map((btn) => {
      const rect = btn.getBoundingClientRect();
      return rect.top + rect.height / 2;
    });
    const spread = Math.max(...centers) - Math.min(...centers);
    assertTrue(
      spread <= 4,
      `row-mode toolbar buttons must stay on one aligned row (${context}): spread=${spread.toFixed(1)}px`
    );
  }
}

async function checkCrowdedBusyComposer(opts: {
  language: "en-US" | "zh-CN";
  approvalMode: "trusted" | "unattended";
  fontScale: "compact" | "default";
  width: number;
  isolated?: boolean;
}) {
  const sid = `ses_crowded_${opts.language}_${opts.approvalMode}_${opts.fontScale}_${opts.width}_${Boolean(opts.isolated)}`;
  const model = { providerID: "google", modelID: "gemini-4-neon" };
  const variants = { xhigh: { reasoningEffort: "xhigh" } };
  const turns: MessageTurn[] = [{
    info: { id: "msg_user", sessionID: sid, role: "user", time: { created: 1 } },
    parts: [{ type: "text", id: "prt_user", sessionID: sid, messageID: "msg_user", text: "Configure Google OAuth" }],
  }, {
    info: {
      id: "msg_work", sessionID: sid, role: "assistant", providerID: model.providerID, modelID: model.modelID,
      time: { created: 2 },
      tokens: { input: 62000, output: 2000, reasoning: 0, cache: { read: 0, write: 0 } },
    },
    parts: [{ type: "text", id: "prt_reply", sessionID: sid, messageID: "msg_work", text: "Working on configuration…" }],
  }];
  const isolated = Boolean(opts.isolated);
  const meta = {
    sessionID: sid, directory: "/probe", isolated, dirty: isolated,
    changedFiles: isolated ? 12 : 0, uncommittedLines: isolated ? 24 : 0,
    additions: isolated ? 18 : 0, deletions: isolated ? 6 : 0,
  };
  hoxiAPI.getWorktreeStatus = async () => meta;
  setLocale(opts.language);
  applyFontScaleToDOM(opts.fontScale);
  setAppSettings("approvalMode", opts.approvalMode);
  setLeftNavOpen(false);
  batch(() => {
    setWorkspace({
      rootDir: "/probe", activeSessionID: sid, connected: true, bootstrappingSessions: false,
      inspectorOpen: false,
      sessions: [{ id: sid, title: "Crowded toolbar", directory: "/probe", time: { created: 1, updated: 3 } }],
      providers: [{
        id: "google", name: "Google",
        models: {
          "gemini-4-neon": {
            id: "gemini-4-neon", name: "Gemini 4 Neon (XHigh)",
            reasoning: true, variants, limit: { context: 200000, output: 16384 },
          },
          "whisper-1": { id: "whisper-1", name: "Whisper" },
        },
      }],
    });
    setWorkspace("aiBySession", sid, { model, agent: "build", effort: "xhigh" });
    setWorkspace("worktreeBySession", sid, meta);
    setWorkspace("messagesBySession", sid, turns);
    setWorkspace("statusBySession", sid, { type: "busy" });
  });
  hoxiAPI.sendPromptAsync = async () => {};
  hoxiAPI.getMessages = async () => structuredClone(unwrap(workspace.messagesBySession[sid] ?? []));
  hoxiAPI.getSessionDiff = async () => [];
  hoxiAPI.getWorktreeDiff = async () => [];
  const root = document.createElement("div");
  document.body.append(root);
  const dispose = render(() => <ThreePaneShell />, root);
  try {
    root.querySelector<HTMLElement>("[data-surface]")!.style.width = `${opts.width}px`;
    await wait(200);
    const composer = root.querySelector<HTMLElement>(".chat-composer-glass")!;
    const toolbar = composer.querySelector<HTMLElement>(".composer-toolbar")!;
    const label = `${opts.language}/${opts.approvalMode}/${opts.fontScale}/${opts.width}px/${isolated ? "worktree" : "local"}`;
    if (opts.width === 1100 && opts.approvalMode === "trusted") {
      assertEqual(getComputedStyle(toolbar).flexDirection, "row", `wide trusted composer should stay single-row (${label})`);
    }
    assertToolbarGeometry(toolbar, `${label}/empty`);
    const beforeToolbarHeight = toolbar.getBoundingClientRect().height;
    const input = composer.querySelector("textarea")!;
    input.value = "example-client-id-00000000000000000000000000000000.apps.example.com EXAMPLE_SECRET_TOKEN_0123456789 Please configure these credentials and verify the callback URL";
    input.dispatchEvent(new Event("input", { bubbles: true }));
    await frame();
    await frame();
    assertTrue(
      Array.from(toolbar.querySelectorAll("button")).some((btn) => btn.textContent?.includes(t("timeline.spawnSubtaskAside"))),
      `busy draft should render spawn subtask pill (${label})`
    );
    assertToolbarGeometry(toolbar, `${label}/draft`);
    const afterToolbarHeight = toolbar.getBoundingClientRect().height;
    if (opts.width >= 560) {
      assertTrue(
        Math.abs(afterToolbarHeight - beforeToolbarHeight) < 2,
        `typing a busy draft must not change toolbar height (${label}): before=${beforeToolbarHeight} after=${afterToolbarHeight}`
      );
    }
  } finally {
    applyFontScaleToDOM("default");
    dispose();
    root.remove();
  }
}

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
    // WebKit lays out a size container's subtree before styling newly inserted
    // nodes, so a container above the scroller clamps the pinned scrollTop (macOS blink).
    for (let node = scroller.parentElement; node; node = node.parentElement) {
      assertEqual(getComputedStyle(node).containerType, "normal",
        `transcript scroller must not sit inside a size container: ${node.className}`);
    }
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
      assertToolbarGeometry(toolbar, `${busy ? "busy" : "idle"}/${width}px/frame-${i}`);
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
  for (const language of ["zh-CN", "en-US"] as const) {
    for (const approvalMode of ["trusted", "unattended"] as const) {
      for (const width of [420, 560, 780, 1100]) {
        await checkCrowdedBusyComposer({ language, approvalMode, fontScale: "default", width });
      }
      await checkCrowdedBusyComposer({ language, approvalMode, fontScale: "compact", width: 1100 });
      await checkCrowdedBusyComposer({ language, approvalMode, fontScale: "default", width: 1100, isolated: true });
      await checkCrowdedBusyComposer({ language, approvalMode, fontScale: "compact", width: 1100, isolated: true });
    }
    results.push(`${language}: crowded busy composer toolbar stays aligned and single-line across widths and modes`);
  }
  return results;
}
