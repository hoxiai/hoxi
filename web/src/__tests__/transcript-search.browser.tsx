import { createMemo, createRoot, createSignal } from "solid-js";
import { render } from "solid-js/web";
import { produce } from "solid-js/store";
import { hoxiAPI } from "../services/api";
import { createTranscriptSearch, createTranscriptSearchClient, noteSessionHistoryTail, forgetSessionHistory, setWorkspace, workspace, type MessageTurn } from "../store/workspace";
import { searchPattern, matchSearchRanges, matchSearchTurns } from "../store/transcript-search-engine";
import TranscriptSearchBar from "../components/timeline/TranscriptSearchBar";
import { highlightTranscriptSearch } from "../components/timeline/transcript-search-highlights";
import { assertDeepEqual, assertEqual, assertTrue } from "./test-harness";

const wait = (ms: number) => new Promise<void>((resolve) => setTimeout(resolve, ms));
async function until(check: () => boolean, message: string) {
  for (let i = 0; i < 300; i++) { if (check()) return; await wait(10); }
  throw new Error(message);
}
function turns(sid: string, count: number): MessageTurn[] {
  return Array.from({ length: count }, (_, index) => ({
    info: { id: `msg_${String(index).padStart(4, "0")}`, sessionID: sid, role: index % 2 ? "assistant" : "user", time: { created: index+1 } },
    parts: [{ id: `prt_${index}`, sessionID: sid, messageID: `msg_${String(index).padStart(4, "0")}`, type: "text", text: `needle ${index}` }],
  }));
}

async function testPagedSearch() {
  const sid = "ses_search_pages";
  const all = turns(sid, 620);
  const original = hoxiAPI.getMessages;
  const calls: Array<{ limit: number | "all"; before?: string; directory?: string }> = [];
  setWorkspace("rootDir", "/search-probe");
  setWorkspace("activeSessionID", sid);
  setWorkspace("messagesBySession", sid, all.slice(-20));
  noteSessionHistoryTail(sid, 20, 20, 0);
  hoxiAPI.getMessages = async (_id, directory, limit = 100, before) => {
    calls.push({ limit, before, directory });
    const end = before ? all.findIndex((turn) => turn.info.id === before) : all.length;
    return structuredClone(all.slice(Math.max(0, end-Number(limit)), end));
  };
  let dispose!: () => void;
  try {
    const f = createRoot((cleanup) => {
      dispose = cleanup;
      const [enabled, setEnabled] = createSignal(true);
      const [query, setQuery] = createSignal("");
      const options = createMemo(() => ({ query: query(), matchCase: false, wholeWord: false, useRegex: false }));
      const search = createTranscriptSearch({ enabled, sessionID: () => sid, turns: () => workspace.messagesBySession[sid], options });
      return { search, setQuery, setEnabled };
    });
    await wait(200);
    assertEqual(calls.length, 0, "opening an empty search fetched history");
    f.setQuery("needle");
    await until(() => f.search.state().status === "ready", "paged search did not complete");
    assertEqual(f.search.state().matches.length, 620);
    assertEqual(workspace.messagesBySession[sid].length, 20, "background search hydrated the reactive transcript");
    assertTrue(calls.every((call) => call.limit === 200 && Boolean(call.before) && call.directory === "/search-probe"), "history reads escaped page/directory bounds");
    assertTrue(await f.search.reveal(f.search.state().matches[205]), "cold match was not loaded for navigation");
    assertTrue(workspace.messagesBySession[sid].some((turn) => turn.info.id === all[205].info.id));
    f.setEnabled(false);
    const readCount = calls.length;
    await wait(200);
    assertEqual(calls.length, readCount, "closing search left history reads running");
    return "620-turn search uses scoped 200-turn pages, leaves only the resident tail in Store, and loads history only when navigating";
  } finally { dispose?.(); hoxiAPI.getMessages = original; forgetSessionHistory(sid); }
}

async function testStaleHistory(mode: "close" | "switch" | "directory" | "evict" = "close") {
  const sid = "ses_search_stale";
  const all = turns(sid, 25);
  const original = hoxiAPI.getMessages;
  setWorkspace("messagesBySession", sid, all.slice(-5));
  noteSessionHistoryTail(sid, 5, 5, 0);
  let finish!: (page: MessageTurn[]) => void;
  let signal: AbortSignal | undefined;
  hoxiAPI.getMessages = (_sid, _directory, _limit, _before, abort) => {
    signal = abort;
    return new Promise((resolve) => { finish = resolve; });
  };
  let dispose!: () => void;
  try {
    const f = createRoot((cleanup) => {
      dispose = cleanup;
      const [enabled, setEnabled] = createSignal(true);
      const [active, setActive] = createSignal(sid);
      const search = createTranscriptSearch({ enabled, sessionID: active, turns: () => workspace.messagesBySession[active()] ?? [],
        options: () => ({ query: "needle", matchCase: false, wholeWord: false, useRegex: false }) });
      return { search, setEnabled, setActive };
    });
    await until(() => Boolean(finish), "history read did not start");
    if (mode === "close") f.setEnabled(false);
    if (mode === "switch") { setWorkspace("messagesBySession", "ses_search_new", []); f.setActive("ses_search_new"); }
    if (mode === "directory") { setWorkspace("messagesBySession", sid, []); setWorkspace("rootDir", "/changed-search-directory"); }
    if (mode === "evict") setWorkspace("messagesBySession", sid, []);
    assertTrue(Boolean(signal?.aborted), `${mode} did not cancel its fetch`);
    finish(all.slice(0, 20));
    await wait(250);
    assertEqual(f.search.state().status, mode === "close" ? "idle" : "ready");
    assertEqual(f.search.state().matches.length, 0, "late history restored stale results");
    assertEqual(workspace.messagesBySession[sid].length, mode === "evict" || mode === "directory" ? 0 : 5);
    return `${mode}: aborts page requests and rejects late history without repopulating results or messages`;
  } finally { dispose?.(); hoxiAPI.getMessages = original; forgetSessionHistory(sid); }
}

async function testPathologicalRegex() {
  const client = createTranscriptSearchClient(150);
  let beats = 0;
  const timer = setInterval(() => beats++, 10);
  const start = performance.now();
  try {
    let error: unknown;
    try { await client.search([{ id: "slow", parts: [{ id: "part", text: "a".repeat(35)+"!" }] }], { source: "^(a+)+$", flags: "g" }); }
    catch (reason) { error = reason; }
    assertTrue(error instanceof Error && error.message === "timeout", "expensive regex was not terminated");
    assertTrue(beats >= 5, "regex blocked the UI heartbeat");
    const matches = await client.search([{ id: "fresh", parts: [{ id: "part", text: "recovered" }] }], { source: "recovered", flags: "g" });
    assertEqual(matches[0]?.messageID, "fresh");
    return `pathological regex terminated with the UI responsive (${beats} heartbeats; ${(performance.now()-start).toFixed(0)}ms including recovery)`;
  } finally { clearInterval(timer); client.dispose(); }
}

async function testSearchUI() {
  const host = document.createElement("div"); document.body.append(host);
  const sid = "ses_search_ui";
  let selected: string | null = null;
  const dispose = render(() => <TranscriptSearchBar open={true} turns={turns(sid, 3)} onClose={() => {}}
    onActiveTurnChange={(id) => { selected = id; }} />, host);
  try {
    const input = host.querySelector("input")!;
    input.value = "needle 1"; input.dispatchEvent(new Event("input", { bubbles: true }));
    await until(() => selected === "msg_0001", "worker result did not navigate");
    input.value = "needle 2"; input.dispatchEvent(new Event("input", { bubbles: true }));
    await until(() => selected === "msg_0002", "latest query did not replace the previous result");
    input.value = "["; input.dispatchEvent(new Event("input", { bubbles: true }));
    host.querySelectorAll("details button")[2].dispatchEvent(new MouseEvent("click", { bubbles: true }));
    await until(() => selected === null, "invalid query kept a stale match");
    return "search UI preserves asynchronous match navigation, latest-query replacement and invalid-regex handling";
  } finally { dispose(); host.remove(); }
}

async function testLiveCorrections() {
  const sid = "ses_search_live";
  const live = turns(sid, 1);
  live[0].parts = [
    { id: "tool", messageID: live[0].info.id, sessionID: sid, type: "tool", tool: "bash", callID: "call",
      state: { status: "pending", raw: "", input: { nested: { command: "needle" } } } },
    { id: "stream", messageID: live[0].info.id, sessionID: sid, type: "text", text: "live" },
  ];
  setWorkspace("messagesBySession", sid, live);
  let dispose!: () => void;
  const search = createRoot((cleanup) => {
    dispose = cleanup;
    return createTranscriptSearch({ enabled: () => true, sessionID: () => sid, turns: () => workspace.messagesBySession[sid],
      options: () => ({ query: "needle", matchCase: false, wholeWord: false, useRegex: false }) });
  });
  const timer = setInterval(() => setWorkspace(produce((state) => {
    const part = state.messagesBySession[sid][0].parts[1];
    if (part.type === "text") part.text += " token";
  })), 10);
  try {
    await until(() => search.state().status === "ready", "continuous tokens starved the search");
    assertEqual(search.state().matches[0]?.partID, "tool");
    setWorkspace(produce((state) => {
      const part = state.messagesBySession[sid][0].parts[0];
      if (part.type === "tool") part.state.input.nested.command = "changed";
    }));
    await until(() => search.state().matches.length === 0, "nested tool-input correction did not refresh matches");
    return "continuous token streaming does not starve searches; nested tool-input corrections invalidate their matches";
  } finally { clearInterval(timer); dispose(); }
}

async function testBoundedHighlights() {
  const root = document.createElement("div"); root.className = "timeline-result-scroll";
  for (let i = 0; i < 2000; i++) { const span = document.createElement("span"); span.textContent = "earlier"; root.append(span); }
  const active = document.createElement("span"); active.dataset.partId = "active_part"; active.textContent = "needle ".repeat(500);
  root.append(active); document.body.append(root);
  const highlights = (CSS as typeof CSS & { highlights: Map<string, { size: number }> }).highlights;
  let cleanup = highlightTranscriptSearch({ source: "needle", flags: "g" }, "turn", "active_part");
  try {
    await until(() => (highlights.get("transcript-search-active")?.size ?? 0) > 0, "active match outside DOM budget was not highlighted");
    assertEqual(highlights.get("transcript-search-active")?.size, 300);
    cleanup();
    cleanup = highlightTranscriptSearch({ source: "needle", flags: "g" }, "turn", "active_part");
    cleanup();
    await wait(50);
    assertEqual(highlights.has("transcript-search-active"), false, "cancelled highlights reappeared");
    return "DOM highlights prioritize the active target, cap at 300 ranges and cannot reappear after cancellation";
  } finally { cleanup(); root.remove(); }
}

function testMatchingSemantics() {
  const pattern = (query: string, overrides = {}) => searchPattern({ query, matchCase: false, wholeWord: false, useRegex: false, ...overrides });
  const list = [{ id: "tool", parts: [{ id: "args", tool: "bash", input: { command: "git status" }, output: "a.b foo foobar FOO" }] }];
  assertEqual(matchSearchTurns(list, pattern("git status"))[0]?.partID, "args");
  assertEqual(matchSearchTurns(list, pattern("a.b")).length, 1);
  assertEqual(matchSearchTurns(list, pattern("GIT", { matchCase: true })).length, 0);
  assertDeepEqual(matchSearchRanges(["foo foobar FOO"], pattern("foo", { wholeWord: true }), 300), [[0, 0, 3], [0, 11, 14]]);
  assertEqual(matchSearchRanges(["a".repeat(1000)], pattern("a"), 300).length, 300);
  return "literal, case, whole-word, tool-input and 300-range limits preserve search semantics";
}

export async function runBrowserTest() {
  return [testMatchingSemantics(), await testPathologicalRegex(), await testPagedSearch(),
    await testStaleHistory(), await testStaleHistory("switch"), await testStaleHistory("directory"), await testStaleHistory("evict"),
    await testSearchUI(), await testLiveCorrections(), await testBoundedHighlights()];
}
