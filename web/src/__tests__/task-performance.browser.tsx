import { createSignal } from "solid-js";
import { render } from "solid-js/web";
import { produce } from "solid-js/store";
import ContextActivityGroupView from "../components/timeline/ContextActivityGroupView";
import ContextProgressPill from "../components/timeline/ContextProgressPill";
import MarkdownView from "../components/timeline/MarkdownView";
import { parseInline } from "../components/timeline/markdown/inline";
import { advanceStreamingSlice } from "../components/timeline/markdown/stream";
import { hoxiAPI } from "../services/api";
import type { MessagePart } from "../services/sse-types";
import { upsertTurnPart } from "../store/message-lookup";
import { trimSessionTranscripts } from "../store/session-transcripts";
import { setAppSettings } from "../store/settings";
import { ensureChildSubagentSessionLoaded } from "../store/workspace-sessions";
import { setWorkspace, workspace } from "../store/workspace-state";
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

function testUnclosedLinkLabels(): string {
  const text = "[label ".repeat(Math.ceil(32000 / 7)).slice(0, 32000);
  let t = performance.now();
  parseInline(text);
  const parseMs = performance.now() - t;
  t = performance.now();
  advanceStreamingSlice(text, 0, 10);
  const revealMs = performance.now() - t;
  assertTrue(parseMs < 25, `32K unclosed link labels parseInline took ${parseMs.toFixed(1)}ms`);
  assertTrue(revealMs < 25, `32K unclosed link labels advanceStreamingSlice took ${revealMs.toFixed(1)}ms`);
  return `32K unclosed link labels parse in ${parseMs.toFixed(1)}ms and slice in ${revealMs.toFixed(1)}ms`;
}

function testPartAppendAndTrimBudget(): string {
  const sid = "ses_deeper_audit";
  const parts: MessagePart[] = Array.from({ length: 3000 }, (_, i) => ({
    type: "tool",
    id: `prt_probe_${i}`,
    sessionID: sid,
    messageID: "msg_probe",
    tool: "write",
    callID: `call_${i}`,
    state: {
      status: "completed",
      input: { filePath: `src/file${i}.ts`, content: "x".repeat(4096) },
      output: "ok",
      title: "Write",
      time: { start: 1, end: 2 },
      metadata: {
        filediff: { before: "a".repeat(4096), after: "b".repeat(4096), additions: 1, deletions: 1 },
        diagnostics: {},
      },
    },
  }));
  setWorkspace({
    activeSessionID: sid,
    rootDir: "/probe",
    sessions: [{ id: sid, title: "Audit", directory: "/probe", time: { created: 1, updated: 1 } }],
  });
  setWorkspace("statusBySession", sid, { type: "busy" });
  setWorkspace("messagesBySession", sid, [
    { info: { id: "msg_probe", sessionID: sid, role: "assistant", time: { created: 1 } }, parts },
  ]);
  let t = performance.now();
  trimSessionTranscripts();
  const trimColdMs = performance.now() - t;
  t = performance.now();
  trimSessionTranscripts();
  const trimWarmMs = performance.now() - t;
  const appendBatches: number[] = [];
  for (let i = 0; i < 3; i++) {
    t = performance.now();
    setWorkspace(
      produce((s) => {
        for (let j = 0; j < 50; j++) {
          upsertTurnPart(s.messagesBySession[sid], {
            type: "text",
            id: `prt_new_${i}_${j}`,
            sessionID: sid,
            messageID: "msg_probe",
            text: "hello",
          });
        }
      })
    );
    appendBatches.push(performance.now() - t);
  }
  assertTrue(trimColdMs < 15 && trimWarmMs < 10, `trimSessionTranscripts took ${trimColdMs.toFixed(1)}ms / ${trimWarmMs.toFixed(1)}ms`);
  assertTrue(Math.max(...appendBatches) < 15, `appending 50 parts at 3000 parts took ${appendBatches.map((x) => x.toFixed(1)).join("/")}ms`);
  return `3000-part turn housekeeping: trim ${trimColdMs.toFixed(1)}ms/${trimWarmMs.toFixed(1)}ms, +50 parts [${appendBatches.map((x) => x.toFixed(1)).join(", ")}]ms`;
}

function testCollapsedActivityGroupAppend(): string {
  const sid = "ses_collapsed_steps_perf";
  setAppSettings("reasoningDisplay", "collapsed");
  const parts: MessagePart[] = Array.from({ length: 3000 }, (_, i) => ({
    type: "tool",
    id: `prt_read_3000_${i}`,
    sessionID: sid,
    messageID: "msg_probe",
    tool: "read",
    callID: `call_${i}`,
    state: {
      status: "completed",
      input: { filePath: `src/module/file${i}.ts` },
      output: "ok",
      title: "Read",
      time: { start: 1, end: 2 },
      metadata: {},
    },
  }));
  setWorkspace("messagesBySession", sid, [
    { info: { id: "msg_probe", sessionID: sid, role: "assistant", time: { created: 1 } }, parts },
  ]);
  const host = document.createElement("div");
  document.body.append(host);
  const dispose = render(
    () => (
      <ContextActivityGroupView
        parts={workspace.messagesBySession[sid][0].parts}
        renderFallbackPart={() => null}
        isLiveTail
      />
    ),
    host
  );
  const times: number[] = [];
  try {
    for (let i = 0; i < 5; i++) {
      const start = performance.now();
      setWorkspace(
        produce((s) => {
          const base = s.messagesBySession[sid][0].parts[0];
          if (base.type === "tool") {
            s.messagesBySession[sid][0].parts.push({
              ...base,
              id: `prt_added_${i}`,
              state: { ...base.state, input: { filePath: `src/other/file${i}.ts` } },
            });
          }
        })
      );
      times.push(performance.now() - start);
    }
    assertTrue(Math.max(...times) < 10, `collapsed activity group step append took ${times.map((x) => x.toFixed(1)).join("/")}ms`);
    return `3000-step collapsed activity group appends steps in [${times.map((x) => x.toFixed(1)).join(", ")}]ms`;
  } finally {
    dispose();
    host.remove();
  }
}

async function testChildSessionLookupCoalescing(): Promise<string> {
  const origGetSession = hoxiAPI.getSession;
  const origGetMessages = hoxiAPI.getMessages;
  let calls = 0;
  let resolveLookup: (() => void) | undefined;
  hoxiAPI.getSession = async () => {
    calls++;
    await new Promise<void>((r) => {
      resolveLookup = r;
    });
    return { id: "ses_missing_child", title: "Child", directory: "/probe", time: { created: 1, updated: 1 } };
  };
  hoxiAPI.getMessages = async () => [];
  try {
    for (let i = 0; i < 20; i++) {
      ensureChildSubagentSessionLoaded("ses_missing_child", "ses_context_perf");
    }
    assertEqual(calls, 1, "concurrent ensureChildSubagentSessionLoaded calls must coalesce in-flight getSession");
    resolveLookup?.();
    await wait(10);
    return "coalesces 20 concurrent unknown child session lookups into 1 request";
  } finally {
    resolveLookup?.();
    hoxiAPI.getSession = origGetSession;
    hoxiAPI.getMessages = origGetMessages;
  }
}

export async function runBrowserTest(): Promise<string[]> {
  await testClosedContextStats();
  return [
    "closed context stats skip all historical input serialization, including after reopening",
    await testLongCodeStream(),
    testUnclosedLinkLabels(),
    testPartAppendAndTrimBudget(),
    testCollapsedActivityGroupAppend(),
    await testChildSessionLookupCoalescing(),
  ];
}
