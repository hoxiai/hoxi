// web/src/__tests__/history-paging.test.ts
import { reconcile, unwrap } from "solid-js/store";
import { hoxiAPI } from "../services/api";
import type { MessageInfo, MessagePart } from "../services/sse-types";
import {
  ensureFullSessionHistory,
  hasOlderSessionHistory,
  loadOlderSessionHistory,
  loadSessionMessagesOnly,
  mergeServerTurnsInto,
  noteSessionHistoryTail,
  removeSessionFromStore,
  SESSION_HISTORY_PAGE_TURNS,
  setWorkspace,
  workspace,
  type MessageTurn,
} from "../store/workspace";
import { assertDeepEqual, assertEqual, assertTrue, describe, it } from "./test-harness";

type Remote = { info: MessageInfo; parts: MessagePart[] };

function transcript(sid: string, count: number): Remote[] {
  return Array.from({ length: count }, (_, index) => {
    const id = `msg_${String(index).padStart(4, "0")}`;
    return {
      info: { id, sessionID: sid, role: index % 4 === 0 ? "user" : "assistant", time: { created: 1_000 + index } } as MessageInfo,
      parts: [{ id: `prt_${id}`, sessionID: sid, messageID: id, type: "text", text: id } as MessagePart],
    };
  });
}

// Mirrors the runner's `limit` / `before` paging of GET .../message.
function pagedServer(all: Remote[], calls: string[], options: { ignoreBefore?: boolean } = {}) {
  return async (_sid: string, _dir?: string, limit: number | "all" = 100, before?: string) => {
    calls.push(`${limit}:${before ?? ""}`);
    let end = all.length;
    if (before && !options.ignoreBefore) end = all.findIndex((turn) => turn.info.id === before);
    if (end < 0) return [];
    const start = limit === "all" ? 0 : Math.max(0, end - limit);
    return structuredClone(all.slice(start, end));
  };
}

const ids = (sid: string) => (workspace.messagesBySession[sid] ?? []).map((turn) => turn.info.id);

async function withPagingFixture(run: (sid: string) => Promise<void>) {
  const saved = structuredClone(unwrap(workspace));
  const original = { ...hoxiAPI };
  const originalFetch = globalThis.fetch;
  const sid = `ses_hoxi_paging_${Math.random().toString(36).slice(2)}`;
  globalThis.fetch = async () => { throw new Error("Unexpected network request in history paging test"); };
  try {
    setWorkspace({ rootDir: "/paging", sessions: [{ id: sid, title: "long", directory: "/paging", time: { created: 1, updated: 1 } }] });
    await run(sid);
  } finally {
    removeSessionFromStore(sid);
    Object.assign(hoxiAPI, original);
    globalThis.fetch = originalFetch;
    setWorkspace(reconcile(saved));
  }
}

describe("Session history paging", () => {
  it("keeps already loaded older turns when a newest-page snapshot is merged", () => {
    const all = transcript("ses_merge", 8);
    const list = structuredClone(all.slice(0, 6)) as MessageTurn[];
    const kept = mergeServerTurnsInto(list, structuredClone(all.slice(4)), undefined, { keepOlder: true });
    assertEqual(kept, 4);
    assertDeepEqual(list.map((turn) => turn.info.id), all.map((turn) => turn.info.id));

    const replaced = structuredClone(all.slice(0, 6)) as MessageTurn[];
    assertEqual(mergeServerTurnsInto(replaced, structuredClone(all.slice(4))), 0);
    assertDeepEqual(replaced.map((turn) => turn.info.id), ["msg_0004", "msg_0005", "msg_0006", "msg_0007"],
      "a full snapshot still drops turns the server no longer has");
  });

  it("decides from a newest page whether older history remains", () => {
    noteSessionHistoryTail("ses_note", 3, 3, 0);
    assertTrue(hasOlderSessionHistory("ses_note"), "a full page may have older turns");
    noteSessionHistoryTail("ses_note", 3, 3, 5);
    assertTrue(hasOlderSessionHistory("ses_note"), "kept older pages keep the previous answer");
    noteSessionHistoryTail("ses_note", 2, 3, 0);
    assertTrue(!hasOlderSessionHistory("ses_note"), "a short page is the whole transcript");
    noteSessionHistoryTail("ses_note", 900, "all", 0);
    assertTrue(!hasOlderSessionHistory("ses_note"));
  });

  it("opens long sessions with the newest page and lazy-loads older pages", async () => {
    await withPagingFixture(async (sid) => {
      const total = SESSION_HISTORY_PAGE_TURNS * 2 + 50;
      const all = transcript(sid, total);
      const calls: string[] = [];
      hoxiAPI.getMessages = pagedServer(all, calls);

      await loadSessionMessagesOnly(sid);
      assertEqual(ids(sid).length, SESSION_HISTORY_PAGE_TURNS);
      assertEqual(ids(sid)[0], all[total - SESSION_HISTORY_PAGE_TURNS].info.id);
      assertTrue(hasOlderSessionHistory(sid));

      assertEqual(await loadOlderSessionHistory(sid), SESSION_HISTORY_PAGE_TURNS);
      assertEqual(ids(sid).length, SESSION_HISTORY_PAGE_TURNS * 2);

      // A status refresh only fetches the newest page but must not drop loaded pages.
      await loadSessionMessagesOnly(sid);
      assertEqual(ids(sid).length, SESSION_HISTORY_PAGE_TURNS * 2);
      assertTrue(hasOlderSessionHistory(sid));

      assertEqual(await loadOlderSessionHistory(sid), 50);
      assertDeepEqual(ids(sid), all.map((turn) => turn.info.id));
      assertTrue(!hasOlderSessionHistory(sid), "a short older page ends paging");
      assertEqual(await loadOlderSessionHistory(sid), 0);
      assertEqual(calls.length, 4, "no request once history is complete");
      assertTrue(calls.every((call) => !call.startsWith("all")), "paging never falls back to the full transcript");
    });
  });

  it("stops paging when the backend ignores the before cursor", async () => {
    await withPagingFixture(async (sid) => {
      const all = transcript(sid, SESSION_HISTORY_PAGE_TURNS + 10);
      hoxiAPI.getMessages = pagedServer(all, [], { ignoreBefore: true });
      await loadSessionMessagesOnly(sid);
      assertTrue(hasOlderSessionHistory(sid));
      assertEqual(await loadOlderSessionHistory(sid), 0);
      assertTrue(!hasOlderSessionHistory(sid), "re-sent turns must not loop or duplicate");
      assertEqual(ids(sid).length, SESSION_HISTORY_PAGE_TURNS);
    });
  });

  it("loads the full transcript once for search, export and commit scoping", async () => {
    await withPagingFixture(async (sid) => {
      const all = transcript(sid, SESSION_HISTORY_PAGE_TURNS + 30);
      const calls: string[] = [];
      hoxiAPI.getMessages = pagedServer(all, calls);
      await loadSessionMessagesOnly(sid);
      await Promise.all([ensureFullSessionHistory(sid), ensureFullSessionHistory(sid)]);
      assertDeepEqual(ids(sid), all.map((turn) => turn.info.id));
      assertTrue(!hasOlderSessionHistory(sid));
      assertEqual(calls.filter((call) => call.startsWith("all")).length, 1, "concurrent callers share one request");
      await ensureFullSessionHistory(sid);
      assertEqual(calls.length, 2);
    });
  });

  it("builds limit, before and full-history queries", async () => {
    const originalFetch = globalThis.fetch;
    const urls: string[] = [];
    globalThis.fetch = (async (url: string) => {
      urls.push(String(url));
      return new Response("[]", { status: 200 });
    }) as typeof fetch;
    try {
      await hoxiAPI.getMessages("ses_hoxi_q", "/repo dir", 200, "msg_9");
      await hoxiAPI.getMessages("ses_hoxi_q", undefined, "all");
      await hoxiAPI.getMessages("ses_hoxi_q");
    } finally {
      globalThis.fetch = originalFetch;
    }
    assertTrue(urls[0].endsWith("/ses_hoxi_q/message?limit=200&before=msg_9&directory=%2Frepo%20dir"), urls[0]);
    assertTrue(urls[1].endsWith("/ses_hoxi_q/message"), urls[1]);
    assertTrue(urls[2].endsWith("/ses_hoxi_q/message?limit=100"), urls[2]);
  });
});
