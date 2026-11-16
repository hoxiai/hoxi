import { render } from "solid-js/web";
import { createRoot, createSignal } from "solid-js";
import { produce } from "solid-js/store";
import { getSingletonHighlighter } from "shiki";
import MarkdownView from "../components/timeline/MarkdownView";
import TaskTimelineBar from "../components/timeline/TaskTimelineBar";
import ContextProgressPill from "../components/timeline/ContextProgressPill";
import { highlightCode } from "../components/timeline/syntax-highlight";
import CodeBlockCard from "../components/timeline/CodeBlockCard";
import { expandInlineThinkingParts } from "../components/timeline/context-activity-parts";
import { createContextContributors } from "../store/context-contributors";
import { createTimelineActivity } from "../store/timeline-activity";
import { setWorkspace, workspace, type MessageTurn } from "../store/workspace-state";
import type { MessagePart } from "../services/sse-types";
import { assertEqual, assertTrue } from "./test-harness";

const wait = (ms: number) => new Promise<void>((resolve) => setTimeout(resolve, ms));

async function realTypeScriptStream(): Promise<string> {
  setWorkspace("theme", "dark");
  await highlightCode("export const warm = 1;", "typescript", "dark");
  const highlighter = await getSingletonHighlighter({ langs: ["typescript"], themes: ["github-dark"] });
  const original = highlighter.codeToTokens;
  let mainCalls = 0;
  highlighter.codeToTokens = function (...args) {
    mainCalls++;
    return original.apply(highlighter, args);
  } as typeof highlighter.codeToTokens;
  const host = document.createElement("div");
  document.body.append(host);
  const [content, setContent] = createSignal("```typescript\n");
  const [streaming, setStreaming] = createSignal(true);
  const dispose = render(() => <MarkdownView content={content()} streaming={streaming()} />, host);
  const longTasks: PerformanceEntry[] = [];
  const observer = new PerformanceObserver((list) => longTasks.push(...list.getEntries()));
  observer.observe({ entryTypes: ["longtask"] });
  const started = performance.now();
  let lastBeat = started;
  let maxBeatDelay = 0;
  const beat = setInterval(() => {
    const now = performance.now();
    maxBeatDelay = Math.max(maxBeatDelay, now-lastBeat-20);
    lastBeat = now;
  }, 20);
  try {
    const lines = Array.from({ length: 450 }, (_, i) => `export const entry${i} = { id: ${i}, label: "value_${i}", enabled: true };`);
    for (let end = 5; end <= lines.length; end += 5) {
      setContent(`\`\`\`typescript\n${lines.slice(0, end).join("\n")}`);
      await wait(8);
    }
    const body = lines.join("\n");
    setContent(`\`\`\`typescript\n${body}\n\`\`\``);
    setStreaming(false);
    for (let i = 0; i < 300; i++) {
      if (host.querySelector("code")?.textContent === body && host.querySelector("code span[style]")) break;
      await wait(10);
    }
    assertEqual(host.querySelector("code")?.textContent, body, "continuous code stream lost text");
    assertTrue(Boolean(host.querySelector("code span[style]")), "real TypeScript must eventually be highlighted");
    assertEqual(mainCalls, 0, "chat streaming must not tokenize code on the UI thread");
    longTasks.push(...observer.takeRecords());
    const longest = Math.max(0, ...longTasks.filter((entry) => entry.startTime >= started).map((entry) => entry.duration));
    return `real ${body.length}-character TS stream: ${mainCalls} main-thread tokenizations; longest task ${longest.toFixed(1)}ms; heartbeat delay ${maxBeatDelay.toFixed(1)}ms`;
  } finally {
    clearInterval(beat); observer.disconnect(); dispose(); host.remove();
    highlighter.codeToTokens = original;
  }
}

function timelineHistoryWork(): string {
  const sid = "ses_timeline_incremental_perf";
  let toolReads = 0;
  const turns: MessageTurn[] = [];
  for (let i = 0; i < 200; i++) {
    turns.push({ info: { id: `user_${i}`, sessionID: sid, role: "user", time: { created: i*10+1 } }, parts: [
      { type: "text", id: `prompt_${i}`, messageID: `user_${i}`, sessionID: sid, text: `Prompt ${i}` },
    ] });
    turns.push({ info: { id: `assistant_${i}`, sessionID: sid, role: "assistant", time: { created: i*10+2, completed: i*10+3 } }, parts: [{
      type: "tool", id: `tool_${i}`, messageID: `assistant_${i}`, sessionID: sid, callID: `call_${i}`,
      get tool() { toolReads++; return "read"; },
      state: { status: "completed", input: {}, output: "ok", title: "Read", metadata: {}, time: { start: i*10+2, end: i*10+3 } },
    }] });
  }
  turns.push({ info: { id: "live", sessionID: sid, role: "assistant", time: { created: 2001 } }, parts: [
    { type: "text", id: "live_text", messageID: "live", sessionID: sid, text: "initial" },
  ] });
  setWorkspace({ activeSessionID: sid, rootDir: "/probe" });
  setWorkspace("messagesBySession", sid, turns);
  const host = document.createElement("div");
  document.body.append(host);
  const mountedAt = performance.now();
  const dispose = render(() => <TaskTimelineBar sessionID={sid} turns={workspace.messagesBySession[sid]}
    searchOpen={false} controlsVisible={false} onJumpToTurn={() => {}} onToggleSearch={() => {}}
    onCloseSearch={() => {}} onSearchMatch={() => {}} />, host);
  const mountMs = performance.now()-mountedAt;
  try {
    toolReads = 0;
    const start = performance.now();
    for (let i = 0; i < 20; i++) setWorkspace(produce((state) => {
      const part = state.messagesBySession[sid][400].parts[0];
      if (part.type === "text") part.text += " token";
    }));
    assertEqual(toolReads, 0, "text growth must not reclassify historical tools");
    return `200 historical tool turns / 20 text updates: ${toolReads} historical tool reads, ${(performance.now()-start).toFixed(1)}ms (mount ${mountMs.toFixed(1)}ms)`;
  } finally { dispose(); host.remove(); }
}

function openContextStatsWork(): string {
  const sid = "ses_context_incremental_perf";
  let serializations = 0;
  const parts: MessagePart[] = Array.from({ length: 300 }, (_, i) => ({
    id: `tool_${i}`, sessionID: sid, messageID: "context", type: "tool", tool: "write", callID: `call_${i}`,
    state: { status: "completed", output: "ok", title: "Write", metadata: {}, time: { start: 1, end: 2 },
      input: { toJSON() { serializations++; return { content: "x".repeat(4096) }; } } },
  }));
  parts.push({ type: "text", id: "live", messageID: "context", sessionID: sid, text: "initial" });
  setWorkspace({ activeSessionID: sid, rootDir: "/probe" });
  setWorkspace("messagesBySession", sid, [{ info: { id: "context", sessionID: sid, role: "assistant", time: { created: 1 } }, parts }]);
  const host = document.createElement("div");
  document.body.append(host);
  const dispose = render(() => <ContextProgressPill />, host);
  try {
    host.querySelector<HTMLButtonElement>('button[aria-haspopup="dialog"]')!.click();
    const initial = serializations;
    const start = performance.now();
    for (let i = 0; i < 20; i++) setWorkspace(produce((state) => {
      const part = state.messagesBySession[sid][0].parts[300];
      if (part.type === "text") part.text += " token";
    }));
    assertEqual(serializations, initial, "text growth must not reserialize historical tool inputs");
    return `open context stats / 20 text updates: ${serializations-initial} historical input serializations, ${(performance.now()-start).toFixed(1)}ms`;
  } finally { dispose(); host.remove(); }
}

function reactiveMetricCorrections(): string {
  const sid = "ses_metric_corrections";
  let serializations = 0;
  setWorkspace("messagesBySession", sid, [
    { info: { id: "optimistic_user", sessionID: sid, role: "user", time: { created: 1 } }, parts: [] },
    { info: { id: "assistant", sessionID: sid, role: "assistant", time: { created: 10 } }, parts: [
      { id: "edit", messageID: "assistant", sessionID: sid, type: "tool", tool: "write", callID: "edit",
        state: { status: "pending", raw: "", input: { content: "short", toJSON() { serializations++; return { content: this.content }; } } } },
      { id: "text", messageID: "assistant", sessionID: sid, type: "text", text: "" },
    ] },
  ]);
  createRoot((dispose) => {
    const [enabled, setEnabled] = createSignal(true);
    const [active, setActive] = createSignal(sid);
    const turns = () => workspace.messagesBySession[active()] ?? [];
    const activity = createTimelineActivity(turns, (part) => expandInlineThinkingParts([part]));
    const contributors = createContextContributors(turns, enabled);
    try {
      assertEqual(activity()[0].kind, "edit");
      assertEqual(activity()[0].running, true);
      assertEqual(activity()[0].stepCount, 2);
      assertEqual(contributors()[0].key, "edit");
      const initial = serializations;
      setWorkspace(produce((state) => {
        const turn = state.messagesBySession[sid][1];
        const part = turn.parts[0];
        if (part.type === "tool") part.state = { status: "completed", input: part.state.input, output: "done", title: "Write", metadata: {}, time: { start: 100, end: 160 } };
        turn.info.time.completed = 200;
      }));
      assertEqual(activity()[0].running, false);
      assertEqual(activity()[0].stepCount, 1);
      assertEqual(activity()[0].duration, 199);
      assertEqual(serializations, initial, "status changes reserialized an unchanged input");
      setWorkspace(produce((state) => {
        const part = state.messagesBySession[sid][1].parts[1];
        if (part.type === "text") part.text = "x".repeat(20_000);
      }));
      assertEqual(contributors()[0].key, "text");
      assertEqual(contributors()[0].tokens, 5000);
      assertEqual(activity()[0].stepCount, 2);
      setWorkspace(produce((state) => {
        const part = state.messagesBySession[sid][1].parts[0];
        if (part.type === "tool") part.state.input.content = "y".repeat(40_000);
        state.messagesBySession[sid][0].info.id = "msg_user_real";
      }));
      assertEqual(contributors()[0].key, "edit");
      assertEqual(serializations, initial+1, "nested input edits did not invalidate just their own estimate");
      assertEqual(activity()[0].id, "msg_user_real");
      const retained = activity()[0];
      setWorkspace(produce((state) => state.messagesBySession[sid].unshift({
        info: { id: "older", sessionID: sid, role: "user", time: { created: 0 } }, parts: [],
      })));
      assertEqual(activity()[1], retained, "history prepend rebuilt the unchanged task segment");
      setEnabled(false);
      assertEqual(contributors().length, 0);
      const closedCount = serializations;
      setWorkspace(produce((state) => {
        const part = state.messagesBySession[sid][2].parts[0];
        if (part.type === "tool") part.state.input.content = "closed";
      }));
      assertEqual(serializations, closedCount, "closed stats retained input subscriptions");
      setEnabled(true);
      assertEqual(contributors()[0].key, "text");
      setWorkspace("messagesBySession", "ses_other_metrics", []);
      setActive("ses_other_metrics");
      assertEqual(activity().length, 0);
      assertEqual(contributors().length, 0);
      const switched = serializations;
      setWorkspace(produce((state) => {
        const part = state.messagesBySession[sid][2].parts[0];
        if (part.type === "tool") part.state.input.content = "background";
      }));
      assertEqual(serializations, switched, "session switch retained the previous transcript's input computations");
    } finally { dispose(); }
  });
  return "incremental metrics retain corrections, nested input edits, optimistic promotion, history prepend and cleanup across close/session switches";
}

async function highlightScopeSwitch(): Promise<string> {
  const host = document.createElement("div");
  document.body.append(host);
  const [code, setCode] = createSignal("export const old_scope = 1;");
  const [lang, setLang] = createSignal("typescript");
  const dispose = render(() => <CodeBlockCard code={code()} lang={lang()} />, host);
  try {
    await wait(100);
    setCode("const intermediate_scope = 2;"); setLang("javascript");
    setWorkspace("theme", "light");
    const final = "export const final_scope_unique = 3;";
    setCode(final); setLang("typescript");
    for (let i = 0; i < 300; i++) {
      if (host.querySelector("code")?.textContent === final && host.querySelector("code span[style]")) break;
      await wait(10);
    }
    assertEqual(host.querySelector("code")?.textContent, final);
    const expected = await highlightCode(final, "typescript", "light");
    const expectedColor = expected?.tokens.flat().find((token) => token.content.trim())?.color;
    const actual = Array.from(host.querySelectorAll<HTMLElement>("code span[style]")).find((span) => span.textContent?.trim());
    const normalized = document.createElement("span"); normalized.style.color = expectedColor ?? "";
    assertEqual(actual?.style.color, normalized.style.color, "a stale language/theme reply replaced the current highlight");
    return "worker highlights survive rapid code/language/theme replacement without stale text or colors";
  } finally { dispose(); host.remove(); setWorkspace("theme", "dark"); }
}

export async function runBrowserTest(): Promise<string[]> {
  return [await realTypeScriptStream(), timelineHistoryWork(), openContextStatsWork(), reactiveMetricCorrections(), await highlightScopeSwitch()];
}
