import { reconcile, unwrap } from "solid-js/store";
import { hoxiAPI } from "../services/api";
import type { SessionInfo, SessionRuntime } from "../services/sse-types";
import { selectModel } from "../store/workspace-providers";
import { setWorkspace, workspace } from "../store/workspace";
import { assertDeepEqual, assertEqual, assertTrue, describe, it } from "./test-harness";
import { sessionNeedsModelSelection, sessionRuntimeForID } from "../store/session-query";

const modelFor = (runtime: SessionRuntime) => ({ providerID: runtime === "opencode" ? "ollama" : runtime, modelID: "model" });

describe("runtime switching in an existing conversation", () => {
  it("keeps the current conversation, history, directory and agent across model selections", () => {
    const saved = structuredClone(unwrap(workspace));
    try {
      const id = "ses_cld_keep_conversation";
      setWorkspace({ activeSessionID: id, hoxiProviders: {}, sessions: [{ id, runtime: "claude-code", directory: "/project", title: "Existing", time: { created: 1, updated: 1 } }],
        messagesBySession: { [id]: [{ info: { id: "msg_old", sessionID: id, role: "user", time: { created: 1 } }, parts: [] }] },
        aiBySession: { [id]: { model: modelFor("claude-code"), agent: "plan", effort: "high" } } });
      const requestCount = workspace.newThreadRequest;
      for (const runtime of ["antigravity", "opencode", "claude-code"] as const) {
        selectModel(modelFor(runtime));
        assertEqual(workspace.activeSessionID, id);
        assertEqual(workspace.newThreadRequest, requestCount);
        assertEqual(workspace.messagesBySession[id][0].info.id, "msg_old");
        assertEqual(workspace.sessions[0].directory, "/project");
        assertDeepEqual(workspace.aiBySession[id], { model: modelFor(runtime), agent: "plan", effort: "high" });
      }
      setWorkspace("sessions", 0, "runtime", "opencode");
      assertEqual(sessionRuntimeForID(id), "opencode");
    } finally { setWorkspace(reconcile(saved)); }
  });

  it("asks for a model only when OpenCode would run without one", () => {
    const saved = structuredClone(unwrap(workspace));
    try {
      const cld = "ses_cld_no_model";
      setWorkspace({ activeSessionID: null, hoxiProviders: {}, globalConfig: {},
        defaultAI: { model: null, agent: "build", effort: "high" },
        sessions: [{ id: cld, runtime: "claude-code", directory: "/project", title: "Existing", time: { created: 1, updated: 1 } }],
        aiBySession: { [cld]: { model: null, agent: "build", effort: "high" } } });
      assertTrue(sessionNeedsModelSelection(null));
      assertEqual(sessionNeedsModelSelection(cld), false);
      setWorkspace("globalConfig", { agent: { build: { model: "ollama/model" } } });
      assertEqual(sessionNeedsModelSelection(null), false);
      setWorkspace("globalConfig", {});
      setWorkspace("defaultAI", "model", modelFor("opencode"));
      assertEqual(sessionNeedsModelSelection(null), false);
    } finally { setWorkspace(reconcile(saved)); }
  });

  it("hands off before dispatch and routes both directions using the same session ID", async () => {
    const savedFetch = globalThis.fetch;
    const id = "ses_runtime_transport";
    const calls: Array<{ path: string; body: any }> = [];
    globalThis.fetch = (async (url: string, init?: RequestInit) => {
      const body = JSON.parse(String(init?.body || "{}"));
      calls.push({ path: url, body });
      const info: SessionInfo = { id, runtime: body.runtime || "opencode", managed: true, directory: "/project/.hoxi/worktrees/existing", title: "Existing", time: { created: 1, updated: 1 } };
      return { ok: true, status: 200, json: async () => url.includes("/runtime") ? info : { ok: true } } as Response;
    }) as typeof fetch;
    try {
      for (const runtime of ["claude-code", "antigravity", "opencode"] as const) {
        await hoxiAPI.sendPromptAsync(id, "Continue", { runtime, model: modelFor(runtime), directory: "/project/.hoxi/worktrees/existing", system: "Project rules" });
      }
      assertEqual(calls.length, 6);
      for (let i = 0; i < calls.length; i += 2) {
        assertTrue(calls[i].path.startsWith(`/api/hoxi/sessions/${id}/runtime?directory=`));
        assertTrue(calls[i + 1].path.startsWith(`/api/hoxi/sessions/${id}/prompt_async?directory=`));
        assertEqual(calls[i + 1].body.system, "Project rules");
        assertDeepEqual(calls[i + 1].body.parts, [{ type: "text", text: "Continue" }]);
      }
      // Ownership is resolved by the gateway, so every session uses the same endpoint.
      await hoxiAPI.getMessages("ses_restored_managed", "/project");
      assertEqual(calls[6].path, "/api/hoxi/sessions/ses_restored_managed/message?limit=100&directory=%2Fproject");
    } finally { globalThis.fetch = savedFetch; }
  });

  it("does not dispatch to the wrong runtime when handoff fails", async () => {
    const savedFetch = globalThis.fetch;
    const calls: string[] = [];
    globalThis.fetch = (async (url: string) => {
      calls.push(url);
      return { ok: false, status: 409, text: async () => "session is busy" } as Response;
    }) as typeof fetch;
    try {
      let failed = false;
      try { await hoxiAPI.sendPromptAsync("ses_failed_handoff", "Continue", { runtime: "claude-code", model: modelFor("claude-code"), directory: "/project" }); } catch { failed = true; }
      assertTrue(failed);
      assertEqual(calls.length, 1);
      assertTrue(calls[0].includes("/runtime?directory="));
    } finally { globalThis.fetch = savedFetch; }
  });
});
