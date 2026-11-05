import { batch } from "solid-js";
import { render } from "solid-js/web";
import { unwrap } from "solid-js/store";
import ThreePaneShell from "../components/layout/ThreePaneShell";
import { hoxiAPI } from "../services/api";
import { setLocale, t } from "../i18n";
import { setAppSettings } from "../store/settings";
import { setLeftNavOpen } from "../store/layout";
import { drainNextQueuedPrompt, upsertMessageInfo, upsertMessagePart } from "../store/workspace";
import { setWorkspace, workspace, type MessageTurn } from "../store/workspace-state";
import { assertTrue } from "./test-harness";
import "../styles.css";

const wait = (ms: number) => new Promise<void>((resolve) => setTimeout(resolve, ms));
const frame = () => new Promise<void>((resolve) => requestAnimationFrame(() => resolve()));

type Mode = "send" | "send-scrolled" | "queue";

// A finished multi-step task whose expanded step group starts above the
// viewport, so starting the next task auto-collapses it off-screen.
function fixture(sid: string, steps: number, busy: boolean): MessageTurn[] {
  const turns: MessageTurn[] = [{
    info: { id: "msg_a_user", sessionID: sid, role: "user", time: { created: 1 } },
    parts: [{ type: "text", id: "prt_a_user", sessionID: sid, messageID: "msg_a_user", text: "First task" }],
  }, {
    info: { id: "msg_a_reply", sessionID: sid, role: "assistant", time: { created: 2, completed: 3 } },
    parts: [{ type: "text", id: "prt_a_reply", sessionID: sid, messageID: "msg_a_reply", text: "First answer paragraph.\n\n".repeat(8) }],
  }, {
    info: { id: "msg_b_user", sessionID: sid, role: "user", time: { created: 4 } },
    parts: [{ type: "text", id: "prt_b_user", sessionID: sid, messageID: "msg_b_user", text: "Second task" }],
  }];
  for (let index = 0; index < steps; index++) {
    const id = `msg_b_step_${String(index).padStart(3, "0")}`;
    turns.push({
      info: { id, sessionID: sid, role: "assistant", time: { created: 5 + index, completed: 5 + index } },
      parts: [{
        type: "tool", tool: "read", callID: `call_${index}`, id: `prt_${id}`, sessionID: sid, messageID: id,
        state: { status: "completed", input: { filePath: `/probe/file_${index}.ts` }, title: "Read", output: "ok", metadata: {}, time: { start: 5, end: 6 } },
      }],
    });
  }
  turns.push({
    info: { id: "msg_b_zfinal", sessionID: sid, role: "assistant", time: { created: 500, ...(busy ? {} : { completed: 501 }) } },
    parts: [{ type: "text", id: "prt_b_final", sessionID: sid, messageID: "msg_b_zfinal", text: "Done. Short final summary." }],
  });
  return turns;
}

async function check(mode: Mode, steps: number) {
  const sid = `ses_scroll_anchor_${mode}_${steps}`;
  const model = { providerID: "probe", modelID: "mini" };
  const meta = { sessionID: sid, directory: "/probe", isolated: false, dirty: false, changedFiles: 0, uncommittedLines: 0, additions: 0, deletions: 0 };
  hoxiAPI.getWorktreeStatus = async () => meta;
  const busyAtStart = mode === "queue";
  batch(() => {
    setWorkspace({ rootDir: "/probe", activeSessionID: sid, connected: true, bootstrappingSessions: false,
      inspectorOpen: false, sessions: [{ id: sid, title: "Scroll fixture", directory: "/probe", time: { created: 1, updated: 3 } }],
      providers: [{ id: "probe", name: "Probe", models: { mini: { id: "mini", name: "Mini" } } }] });
    setWorkspace("aiBySession", sid, { model, agent: "build", effort: "" });
    setWorkspace("worktreeBySession", sid, meta);
    setWorkspace("messagesBySession", sid, fixture(sid, steps, busyAtStart));
    setWorkspace("statusBySession", sid, { type: busyAtStart ? "busy" : "idle" });
  });
  setLeftNavOpen(false);
  setAppSettings("reasoningDisplay", "auto-collapse");
  setAppSettings("approvalMode", "trusted");
  setAppSettings("autoScrollStream", true);
  hoxiAPI.sendPromptAsync = async () => {};
  hoxiAPI.getMessages = async () => structuredClone(unwrap(workspace.messagesBySession[sid] ?? []));
  hoxiAPI.getSessionDiff = async () => [];
  hoxiAPI.getWorktreeDiff = async () => [];
  const root = document.createElement("div");
  document.body.append(root);
  const dispose = render(() => <ThreePaneShell />, root);
  try {
    await wait(300);
    const scroller = root.querySelector<HTMLElement>(".timeline-result-scroll")!;
    scroller.scrollTop = scroller.scrollHeight;
    await wait(200);
    if (mode === "send-scrolled") {
      // The reader scrolled up a little to re-read the answer before sending.
      scroller.dispatchEvent(new WheelEvent("wheel", { deltaY: -120, bubbles: true }));
      scroller.scrollTop -= 120;
      await wait(200);
    }
    const distance =() => Math.round(scroller.scrollHeight - scroller.clientHeight - scroller.scrollTop);
    // Chrome repins within the same frame, so also catch any script write that
    // pulls the pinned reader upward (WebKit paints that frame).
    const writes: string[] = [];
    const desc = Object.getOwnPropertyDescriptor(Element.prototype, "scrollTop")!;
    Object.defineProperty(scroller, "scrollTop", { configurable: true, get() { return desc.get!.call(this); }, set(value: number) {
      const before = desc.get!.call(this) as number;
      desc.set!.call(this, value);
      const after = desc.get!.call(this) as number;
      if (after < before - 50) writes.push(`${Math.round(before)}->${Math.round(after)} ${(new Error().stack || "").split("\n").slice(1, 3).join(" < ")}`);
    } });
    const header = scroller.querySelector<HTMLElement>("[data-turn-id] .sticky");
    const headerPinned = header ? Math.round(header.getBoundingClientRect().top - scroller.getBoundingClientRect().top) : null;
    const composer = root.querySelector<HTMLElement>(".chat-composer-glass")!;
    const input = composer.querySelector("textarea")!;
    input.value = "Next task please";
    input.dispatchEvent(new Event("input", { bubbles: true }));
    await frame();
    const button = Array.from(composer.querySelectorAll("button"))
      .find((item) => busyAtStart ? item.title === t("timeline.queueSend") : item.title.startsWith(`${t("timeline.send")} (`))!;
    button.click();
    await wait(150);
    if (mode === "queue") {
      // The running task finishes; the queued prompt drains automatically.
      batch(() => {
        upsertMessageInfo({ id: "msg_b_zfinal", sessionID: sid, role: "assistant", time: { created: 500, completed: 501 } });
        setWorkspace("statusBySession", sid, { type: "idle" });
      });
      await frame();
      void drainNextQueuedPrompt(sid);
    }
    const samples: number[] = [];
    for (let i = 0; i < 40; i++) {
      if (i === 8) {
        const now = Date.now();
        batch(() => {
          upsertMessageInfo({ id: "msg_c_ack", sessionID: sid, role: "user", time: { created: now } });
          upsertMessagePart({ type: "text", id: "prt_c_ack", messageID: "msg_c_ack", sessionID: sid, text: "Next task please" });
          upsertMessageInfo({ id: "msg_c_next", sessionID: sid, role: "assistant", time: { created: now + 1 } });
          upsertMessagePart({ type: "text", id: "prt_c_next", messageID: "msg_c_next", sessionID: sid, text: "Starting the next task" });
        });
      }
      await frame();
      // Sample after the frame's resize observers and paint, i.e. what the reader saw.
      await wait(0);
      samples.push(distance());
    }
    await wait(600);
    const settled = distance();
    return { mode, steps, headerPinned, maxDistance: Math.max(...samples), settled, samples: samples.join(","), writes };
  } finally {
    delete (root.querySelector(".timeline-result-scroll") as { scrollTop?: number } | null)?.scrollTop;
    dispose();
    root.remove();
  }
}

export async function runBrowserTest(): Promise<string[]> {
  window.fetch = async () => new Response("{}", { headers: { "Content-Type": "application/json" } });
  setLocale("zh-CN");
  const results: string[] = [];
  const failures: string[] = [];
  const labels: Record<Mode, string> = {
    send: "sending",
    "send-scrolled": "sending while scrolled up",
    queue: "a queued prompt draining",
  };
  for (const mode of ["send", "send-scrolled", "queue"] as const) for (const steps of [6, 40]) {
    const result = await check(mode, steps);
    // A reader who scrolled up may be anchored before the send pulls them down.
    const strayWrites = mode === "send-scrolled" ? 0 : result.writes.length;
    if (result.maxDistance >= 2 || result.settled >= 2 || strayWrites > 0) {
      failures.push(JSON.stringify(result));
    }
    results.push(`${labels[mode]} after a finished ${steps}-step task keeps the live tail pinned`);
  }
  assertTrue(failures.length === 0, `starting the next task must keep the live tail pinned:\n${failures.join("\n")}`);
  return results;
}
