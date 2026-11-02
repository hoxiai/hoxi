import { reconcile, unwrap } from "solid-js/store";
import { hoxiAPI } from "../services/api";
import type { SnapshotFileDiff } from "../services/sse-types";
import { inspectPromptContextMentions, resolveContextMentionsForSend } from "../store/terminal-context";
import { removeSessionFromStore } from "../store/workspace-sessions";
import { setWorkspace, workspace, type MessageTurn } from "../store/workspace-state";
import { assertEqual, assertTrue, describe, it } from "./test-harness";

function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (error: Error) => void;
  const promise = new Promise<T>((yes, no) => { resolve = yes; reject = no; });
  return { promise, resolve, reject };
}
const flush = async () => { for (let i = 0; i < 12; i++) await Promise.resolve(); };
let nextFixture = 0;
async function withFixture(run: (sid: string, ref: string) => Promise<void>) {
  const saved = structuredClone(unwrap(workspace));
  const originalAPI = { ...hoxiAPI };
  const originalFetch = globalThis.fetch;
  const originalTimeout = globalThis.setTimeout;
  const sid = `ses_mentions_${++nextFixture}`, ref = `${sid}_ref`;
  globalThis.setTimeout = (() => 0) as unknown as typeof setTimeout;
  globalThis.fetch = async () => { throw new Error("Unexpected mention-test network request"); };
  try {
    setWorkspace({ rootDir: "/mentions", activeSessionID: sid, sessions: [sid, ref].map((id) => ({
      id, title: id, directory: `/mentions/${id}`, time: { created: 1, updated: 1 },
    })) });
    for (const id of [sid, ref]) {
      setWorkspace("messagesBySession", id, undefined!);
      setWorkspace("diffsBySession", id, []);
      setWorkspace("worktreeDiffsBySession", id, []);
      setWorkspace("worktreeBySession", id, { sessionID: id, directory: `/mentions/${id}`, isolated: false });
    }
    hoxiAPI.getSessionDiff = async () => [];
    await run(sid, ref);
  } finally {
    removeSessionFromStore(sid); removeSessionFromStore(ref);
    Object.assign(hoxiAPI, originalAPI);
    setWorkspace(reconcile(saved));
    globalThis.fetch = originalFetch;
    globalThis.setTimeout = originalTimeout;
  }
}
const referenceTurns = (sid: string): MessageTurn[] => [{
  info: { id: `msg_${sid}`, sessionID: sid, role: "assistant", time: { created: 1, completed: 2 } },
  parts: [{ type: "text", id: `prt_${sid}`, sessionID: sid, messageID: `msg_${sid}`, text: "Complete referenced content" }],
}];

describe("Context mention loading on send", () => {
  it("awaits an in-flight session preview and shares the request across concurrent sends", async () => {
    await withFixture(async (sid, ref) => {
      const request = deferred<MessageTurn[]>();
      let calls = 0;
      hoxiAPI.getMessages = async () => { calls++; return request.promise; };
      inspectPromptContextMentions(`@session:${ref}`, sid);
      let finished = false;
      const first = resolveContextMentionsForSend(`Use @session:${ref}`, sid).then((text) => { finished = true; return text; });
      const second = resolveContextMentionsForSend(`Use @session:${ref}`, sid);
      await flush();
      assertEqual(calls, 1);
      assertEqual(finished, false, "send must wait for the preview read");
      request.resolve(referenceTurns(ref));
      assertTrue((await first).includes("Complete referenced content"));
      assertTrue((await second).includes("Complete referenced content"));
    });
  });

  for (const kind of ["changes", "worktree"] as const) {
    it(`awaits an in-flight @${kind} preview without fetching twice`, async () => {
      await withFixture(async (sid, ref) => {
        const request = deferred<SnapshotFileDiff[]>();
        let calls = 0;
        hoxiAPI.getWorktreeDiff = async () => { calls++; return request.promise; };
        const prompt = kind === "changes" ? "Inspect @changes" : `Inspect @worktree:${ref}`;
        inspectPromptContextMentions(prompt, sid);
        let finished = false;
        const sending = resolveContextMentionsForSend(prompt, sid).then((text) => { finished = true; return text; });
        await flush();
        assertEqual(calls, 1); assertEqual(finished, false);
        request.resolve([{ file: "feature.ts", before: "old", after: "new", additions: 1, deletions: 1 }]);
        assertTrue((await sending).includes("feature.ts"));
      });
    });
  }

  it("reports a failed preview to send and permits an immediate explicit retry", async () => {
    await withFixture(async (sid, ref) => {
      const request = deferred<MessageTurn[]>();
      let calls = 0;
      hoxiAPI.getMessages = async () => { calls++; return request.promise; };
      inspectPromptContextMentions(`@session:${ref}`, sid);
      const sending = resolveContextMentionsForSend(`Use @session:${ref}`, sid).then(() => "", (error: Error) => error.message);
      request.reject(new Error("Reference unavailable"));
      const error = await sending;
      assertTrue(error.includes(`@session:${ref}`) && error.includes("Reference unavailable"));
      hoxiAPI.getMessages = async () => { calls++; return referenceTurns(ref); };
      assertTrue((await resolveContextMentionsForSend(`Use @session:${ref}`, sid)).includes("Complete referenced content"));
      assertEqual(calls, 2, "send retries must bypass preview throttling after a failure");
    });
  });

  it("rejects stale diff data after a directory change and protects a newer request", async () => {
    await withFixture(async (sid) => {
      const old = deferred<SnapshotFileDiff[]>(), current = deferred<SnapshotFileDiff[]>();
      let calls = 0;
      hoxiAPI.getWorktreeDiff = async () => (++calls === 1 ? old.promise : current.promise);
      const before = resolveContextMentionsForSend("Use @changes", sid).then(() => false, () => true);
      setWorkspace("worktreeBySession", sid, "directory", "/moved");
      const after = resolveContextMentionsForSend("Use @changes", sid);
      old.resolve([{ file: "old-directory.ts", before: "old", after: "wrong", additions: 1, deletions: 1 }]);
      assertTrue(await before, "the original send must reject stale context");
      const concurrent = resolveContextMentionsForSend("Use @changes", sid);
      assertEqual(calls, 2, "finishing the old request must not clear the new singleflight");
      current.resolve([{ file: "current-directory.ts", before: "old", after: "correct", additions: 1, deletions: 1 }]);
      assertTrue((await after).includes("current-directory.ts"));
      assertTrue((await concurrent).includes("current-directory.ts"));
      assertTrue(!workspace.worktreeDiffsBySession[sid].some((diff) => diff.file === "old-directory.ts"));
    });
  });

  it("loads session-specific diffs and avoids leaking __root__ diffs into isolated worktrees", async () => {
    await withFixture(async (sid, ref) => {
      setWorkspace("worktreeDiffsBySession", "__root__", [
        { file: "root-dirty.ts", before: "a", after: "b", additions: 1, deletions: 1 },
      ]);
      setWorkspace("worktreeBySession", sid, { sessionID: sid, directory: `/mentions/${sid}`, isolated: true });
      let calls = 0;
      hoxiAPI.getWorktreeDiff = async (targetID) => {
        calls++;
        if (targetID === ref) {
          return [{ file: "ref-change.ts", before: "x", after: "y", additions: 1, deletions: 1 }];
        }
        return [];
      };
      const cleanSend = await resolveContextMentionsForSend("Review @changes", sid);
      assertEqual(calls, 1, "cached __root__ diffs must not skip fetching session worktree diffs");
      assertTrue(!cleanSend.includes("root-dirty.ts"), "isolated worktree must not inherit __root__ dirty files");
      const refSend = await resolveContextMentionsForSend(`Review @worktree:${ref}`, sid);
      assertEqual(calls, 2);
      assertTrue(refSend.includes("ref-change.ts") && !refSend.includes("root-dirty.ts"));
    });
  });
});
