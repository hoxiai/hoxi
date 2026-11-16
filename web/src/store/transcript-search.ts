import { createEffect, createMemo, createSignal, mapArray, onCleanup, untrack, type Accessor } from "solid-js";
import type { MessageTurn } from "./workspace-state";
import { workspace } from "./workspace-state";
import { hoxiAPI } from "../services/api";
import { hasOlderSessionHistory, loadOlderSessionHistory } from "./session-history";
import { isSessionDeleted, sessionDirectory } from "./session-query";
import { createTranscriptSearchClient } from "./transcript-search-client";
import { searchPart, searchPattern, type SearchTurn, type TranscriptMatch, type TranscriptSearchOptions } from "./transcript-search-engine";

// Copy JSON-like input while tracking nested Solid updates, without serializing
// large inputs on the UI thread. Memoized per part, not per keystroke/token.
function copyInput(value: unknown): unknown {
  if (!value || typeof value !== "object") return typeof value === "function" ? undefined : value;
  if (Array.isArray(value)) return value.map(copyInput);
  return Object.fromEntries(Object.entries(value).map(([key, item]) => [key, copyInput(item)]));
}

export function createTranscriptSearch(params: {
  enabled: Accessor<boolean>; sessionID: Accessor<string | null | undefined>;
  turns: Accessor<MessageTurn[]>; options: Accessor<TranscriptSearchOptions>;
}) {
  const [state, setState] = createSignal<{ matches: TranscriptMatch[]; status: "idle" | "searching" | "ready" | "invalid" | "timeout" | "error" }>({ matches: [], status: "idle" });
  const sources = mapArray(() => params.enabled() && params.options().query.trim() ? params.turns() : [], (turn) => {
    const parts = mapArray(() => turn.parts, (part) => {
      const inputSource = createMemo(() => part.type === "tool" ? part.state.input : undefined);
      const input = createMemo(() => copyInput(inputSource()));
      return createMemo(() => ({ ...searchPart(part), input: input() }));
    });
    return createMemo<SearchTurn>(() => ({ id: turn.info.id, parts: parts().map((part) => part()) }));
  });
  const snapshots = createMemo(() => sources().map((source) => source()));
  const pattern = createMemo(() => {
    if (!params.options().query.trim()) return null;
    try { return searchPattern(params.options()); } catch { return null; }
  });
  let generation = 0;
  let previousScope = "";
  let previousSource: MessageTurn[] | undefined;
  let latest: SearchTurn[] = [];
  let timer: ReturnType<typeof setTimeout> | undefined;
  let running = false;
  let disposed = false;
  let blockedGeneration = -1;
  let cancelJob: (() => void) | undefined;
  const cancel = () => {
    generation++;
    clearTimeout(timer); timer = undefined;
    cancelJob?.(); cancelJob = undefined; running = false;
  };
  onCleanup(() => { disposed = true; cancel(); });

  function schedule() {
    if (disposed || timer !== undefined || running || blockedGeneration === generation) return;
    timer = setTimeout(() => { timer = undefined; void run(); }, 150);
  }
  async function run() {
    const activePattern = untrack(pattern);
    if (!activePattern) return;
    const turns = latest;
    const sessionID = params.sessionID();
    const directory = sessionID ? sessionDirectory(sessionID) : undefined;
    const source = sessionID ? workspace.messagesBySession[sessionID] : undefined;
    const version = generation;
    const client = createTranscriptSearchClient();
    const abort = new AbortController();
    cancelJob = () => { abort.abort(); client.dispose(); };
    running = true;
    const current = () => version === generation && !abort.signal.aborted && (!sessionID ||
      !isSessionDeleted(sessionID) && sessionDirectory(sessionID) === directory && workspace.messagesBySession[sessionID] === source);
    try {
      let matches: TranscriptMatch[] = [];
      for (let start = 0; start < turns.length && current(); start += 200) {
        matches.push(...await client.search(turns.slice(start, start+200), activePattern));
      }
      let cursor = turns[0]?.id;
      const seen = new Set(turns.map((turn) => turn.id));
      // Search cold pages without merging them into the reactive transcript.
      while (current() && sessionID && cursor && untrack(() => hasOlderSessionHistory(sessionID))) {
        const page = await hoxiAPI.getMessages(sessionID, directory, 200, cursor, abort.signal);
        if (!current()) return;
        const fresh = page.filter((turn) => !seen.has(turn.info.id));
        if (!fresh.length) break;
        for (const turn of fresh) seen.add(turn.info.id);
        matches = [...await client.search(fresh.map((turn) => ({ id: turn.info.id, parts: turn.parts.map(searchPart) })), activePattern), ...matches];
        cursor = fresh[0].info.id;
        if (page.length < 200) break;
      }
      if (current()) setState({ matches, status: "ready" });
    } catch (error) {
      if (current()) {
        blockedGeneration = version;
        setState({ matches: [], status: error instanceof Error && error.message === "timeout" ? "timeout" : "error" });
      }
    } finally {
      client.dispose();
      if (version === generation) {
        running = false; cancelJob = undefined;
        if (latest !== turns) schedule();
      }
    }
  }
  createEffect(() => {
    const enabled = params.enabled();
    const options = params.options();
    const sessionID = params.sessionID();
    const directory = sessionID ? sessionDirectory(sessionID) : undefined;
    const source = sessionID ? workspace.messagesBySession[sessionID] : undefined;
    latest = snapshots();
    const scope = JSON.stringify([enabled, sessionID, directory, options]);
    if (scope !== previousScope || source !== previousSource) {
      cancel(); previousScope = scope; previousSource = source;
      setState({ matches: [], status: "idle" });
    }
    if (!enabled || !options.query.trim()) return;
    if (!pattern()) { setState({ matches: [], status: "invalid" }); return; }
    if (blockedGeneration === generation) return;
    if (untrack(state).status !== "ready") setState({ matches: untrack(state).matches, status: "searching" });
    // Throttle transcript refreshes rather than debounce: continuous streaming
    // must not starve search. Query/scope changes still cancel old work at once.
    schedule();
  });
  return {
    state, pattern,
    async reveal(match: TranscriptMatch): Promise<boolean> {
      const id = params.sessionID();
      if (!id) return true;
      const directory = sessionDirectory(id);
      const options = JSON.stringify(params.options());
      while (!disposed && params.enabled() && params.sessionID() === id && !isSessionDeleted(id) && sessionDirectory(id) === directory && JSON.stringify(params.options()) === options) {
        if (workspace.messagesBySession[id]?.some((turn) => turn.info.id === match.messageID)) return true;
        if (!hasOlderSessionHistory(id) || !await loadOlderSessionHistory(id)) break;
      }
      return false;
    },
  };
}
