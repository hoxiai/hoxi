import { createSignal } from "solid-js";
import { render } from "solid-js/web";
import { produce } from "solid-js/store";
import ContextProgressPill from "../components/timeline/ContextProgressPill";
import MarkdownView from "../components/timeline/MarkdownView";
import type { MessagePart } from "../services/sse-types";
import { setWorkspace } from "../store/workspace-state";
import { assertEqual, assertTrue } from "./test-harness";

const wait = (ms: number) => new Promise<void>((resolve) => setTimeout(resolve, ms));

async function testClosedContextStats() {
  const sid = "ses_context_perf";
  let serializations = 0;
  const payload = "x".repeat(16384);
  const parts: MessagePart[] = Array.from({ length: 200 }, (_, i) => ({
    id: `prt_${i}`, sessionID: sid, messageID: "msg_context_perf", type: "tool", tool: "write", callID: `call_${i}`,
    state: { status: "completed", output: "ok", title: "Write", metadata: {}, time: { start: 1, end: 2 },
      input: { toJSON() { serializations++; return { auditPayload: payload }; } } },
  }));
  parts.push({ id: "prt_tail", sessionID: sid, messageID: "msg_context_perf", type: "text", text: "initial" });
  setWorkspace({ activeSessionID: sid, rootDir: "/probe", sessions: [
    { id: sid, title: "Performance fixture", directory: "/probe", time: { created: 1, updated: 1 } },
  ] });
  setWorkspace("messagesBySession", sid, [{
    info: { id: "msg_context_perf", sessionID: sid, role: "assistant", time: { created: 1 } }, parts,
  }]);
  const host = document.createElement("div");
  document.body.append(host);
  const dispose = render(() => <ContextProgressPill />, host);
  const append = () => setWorkspace(produce((state) => {
    const tail = state.messagesBySession[sid][0].parts[200];
    if (tail.type === "text") tail.text += " delta";
  }));
  try {
    const trigger = host.querySelector<HTMLButtonElement>('button[aria-haspopup="dialog"]')!;
    assertEqual(trigger.getAttribute("aria-expanded"), "false");
    for (let i = 0; i < 10; i++) append();
    assertEqual(serializations, 0, "closed stats must not serialize historical inputs");
    trigger.click();
    assertEqual(serializations, 200, "opening stats must calculate the current contributors");
    append();
    assertEqual(serializations, 400, "open stats must still react to live text");
    trigger.click();
    for (let i = 0; i < 10; i++) append();
    assertEqual(serializations, 400, "closing must unsubscribe from transcript content");
    trigger.click();
    assertEqual(serializations, 600, "reopening must calculate the latest transcript");
  } finally { dispose(); host.remove(); }
}

async function testLongCodeStream(): Promise<string> {
  const host = document.createElement("div");
  document.body.append(host);
  const [content, setContent] = createSignal("```text\n");
  const dispose = render(() => <MarkdownView content={content()} streaming />, host);
  const tasks: PerformanceEntry[] = [];
  const observer = new PerformanceObserver((list) => tasks.push(...list.getEntries()));
  observer.observe({ entryTypes: ["longtask"] });
  try {
    await wait(60);
    const start = performance.now();
    const line = "ABCDEFGHIJKLMNOPQRSTUVWXYZ".repeat(1231);
    setContent(`\`\`\`text\n${line}\n\`\`\``);
    await wait(800);
    tasks.push(...observer.takeRecords());
    const longest = Math.max(0, ...tasks.filter((entry) => entry.startTime >= start).map((entry) => entry.duration));
    assertTrue(host.querySelector("code")?.textContent?.includes(line) ?? false, "stream must render the full 32K code line");
    assertTrue(longest < 150, `long code streaming blocked the main thread for ${longest.toFixed(1)}ms`);
    return `32K code stream renders in full; longest main-thread task ${longest.toFixed(1)}ms`;
  } finally { observer.disconnect(); dispose(); host.remove(); }
}

export async function runBrowserTest(): Promise<string[]> {
  await testClosedContextStats();
  return ["closed context stats skip all historical input serialization, including after reopening", await testLongCodeStream()];
}
