import { reconcile, unwrap } from "solid-js/store";
import { hoxiAPI } from "../services/api";
import { appSettings, setAppSettings } from "../store/settings";
import { newIsolatedSession, switchActiveSessionSandboxMode } from "../store/session-sandbox";
import { cancelScheduledWorktreeStatus, exitSessionWorktree, replaceSessionFromEvent, sessionDirectory } from "../store/workspace-sessions";
import { setWorkspace, workspace } from "../store/workspace";
import { assertEqual, assertTrue, describe, it } from "./test-harness";

async function withFixture(run: () => Promise<void>) {
  const saved = structuredClone(unwrap(workspace));
  const settings = structuredClone(unwrap(appSettings));
  const api = { ...hoxiAPI };
  const documentDescriptor = Object.getOwnPropertyDescriptor(globalThis, "document");
  Object.defineProperty(globalThis, "document", { configurable: true, value: {
    documentElement: { classList: { toggle() {} }, dataset: {}, style: { setProperty() {} } },
  } });
  try {
    setWorkspace({ rootDir: "/runner-project", activeSessionID: null, sessions: [],
      worktreeBySession: {}, statusBySession: {}, hoxiProviders: {} });
    hoxiAPI.getSessionDiff = async () => [];
    hoxiAPI.getWorktreeDiff = async () => [];
    hoxiAPI.getWorktreeStatus = async (sid) => {
      const meta = workspace.worktreeBySession[sid];
      return { sessionID: sid, directory: meta?.directory || "/runner-project", isolated: meta?.isolated || false,
        branch: meta?.branch, dirty: false, changedFiles: 0, uncommittedLines: 0, additions: 0, deletions: 0,
        aheadCount: 0, hasUpstream: false };
    };
    await run();
    await new Promise((resolve) => setTimeout(resolve, 0));
  } finally {
    for (const session of workspace.sessions) cancelScheduledWorktreeStatus(session.id);
    Object.assign(hoxiAPI, api);
    setAppSettings(reconcile(settings));
    setWorkspace(reconcile(saved));
    if (documentDescriptor) Object.defineProperty(globalThis, "document", documentDescriptor);
    else Reflect.deleteProperty(globalThis, "document");
  }
}

describe("local CLI sandbox settings", () => {
  it("forwards explicit discard and branch retention choices to the runner", async () => {
    await withFixture(async () => {
      const id = "ses_cld_discard";
      const choices: boolean[] = [];
      hoxiAPI.setSandboxMode = async (_sid, mode, options) => {
        assertEqual(mode, "local");
        assertEqual(options?.force, true);
        choices.push(options!.keepBranch!);
        return { id, title: "test", runtime: "claude-code", sandboxMode: "local",
          directory: "/runner-project", time: { created: 1, updated: 1 } };
      };
      await exitSessionWorktree(id, false);
      await exitSessionWorktree(id, true);
      assertEqual(choices.join(","), "false,true");
    });
  });

  for (const runtime of ["claude-code", "antigravity"] as const) {
    for (const mode of ["local", "worktree"] as const) {
      it(`${runtime} follows the ${mode} default without extra worktree creation`, async () => {
        await withFixture(async () => {
          setAppSettings("defaultSandboxMode", mode);
          setWorkspace("defaultAI", { model: { providerID: runtime, modelID: "model" }, agent: "plan", effort: "" });
          const id = runtime === "claude-code" ? "ses_cld_mode" : "ses_agy_mode";
          let selected: string | undefined;
          hoxiAPI.createSession = async (_title, directory, options) => {
            assertEqual(directory, "/runner-project");
            assertEqual(options?.runtime, runtime);
            selected = options?.sandboxMode;
            return { id, title: "test", runtime, sandboxMode: mode,
              directory: mode === "worktree" ? `/runner-project/.hoxi/worktrees/${id}` : "/runner-project",
              branch: mode === "worktree" ? `hoxi/${id}` : undefined, time: { created: 1, updated: 1 } };
          };
          hoxiAPI.createWorktree = async () => { throw new Error("runner already provisions its selected directory"); };
          assertEqual(await newIsolatedSession(), id);
          assertEqual(selected, mode);
          assertEqual(workspace.worktreeBySession[id].isolated, mode === "worktree");
          assertEqual(sessionDirectory(id), mode === "worktree" ? `/runner-project/.hoxi/worktrees/${id}` : "/runner-project");
        });
      });
    }

    it(`${runtime} allows a draft to select local mode and an idle session to switch directories`, async () => {
      await withFixture(async () => {
        setWorkspace("defaultAI", { model: { providerID: runtime, modelID: "model" }, agent: "plan", effort: "" });
        setAppSettings("defaultSandboxMode", "worktree");
        await switchActiveSessionSandboxMode("local");
        assertEqual(appSettings.defaultSandboxMode, "local");
        const id = runtime === "claude-code" ? "ses_cld_switch" : "ses_agy_switch";
        setWorkspace({ activeSessionID: id, sessions: [{ id, runtime, sandboxMode: "local", title: "test",
          directory: "/runner-project", time: { created: 1, updated: 1 } }] });
        const calls: string[] = [];
        hoxiAPI.setSandboxMode = async (sid, mode) => {
          assertEqual(sid, id);
          calls.push(mode);
          return { id, runtime, sandboxMode: mode, title: "test", time: { created: 1, updated: 1 },
            directory: mode === "local" ? "/runner-project" : `/runner-project/.hoxi/worktrees/${id}` };
        };
        await switchActiveSessionSandboxMode("worktree");
        assertTrue(workspace.worktreeBySession[id].isolated);
        await switchActiveSessionSandboxMode("local");
        assertEqual(workspace.worktreeBySession[id].isolated, false);
        assertEqual(sessionDirectory(id), "/runner-project");
        assertEqual(calls.join(","), "worktree,local");
        setWorkspace("statusBySession", id, { type: "busy" });
        let error: unknown;
        try { await switchActiveSessionSandboxMode("worktree"); } catch (caught) { error = caught; }
        assertTrue(error instanceof Error);
        assertEqual(calls.length, 2);
      });
    });
  }

  it("keeps local session metadata local when an SSE session snapshot is restored", async () => {
    await withFixture(async () => {
      replaceSessionFromEvent({ id: "ses_cld_restored", runtime: "claude-code", sandboxMode: "local",
        title: "test", directory: "/runner-project", time: { created: 1, updated: 1 } });
      assertEqual(workspace.worktreeBySession.ses_cld_restored.isolated, false);
    });
  });

  it("lets an explicit local selection override the worktree default", async () => {
    await withFixture(async () => {
      setAppSettings("defaultSandboxMode", "worktree");
      setWorkspace("defaultAI", { model: { providerID: "claude-code", modelID: "model" }, agent: "plan", effort: "" });
      hoxiAPI.createSession = async (_title, _directory, options) => {
        assertEqual(options?.sandboxMode, "local");
        return { id: "ses_cld_override", runtime: "claude-code", sandboxMode: "local", title: "test",
          directory: "/runner-project", time: { created: 1, updated: 1 } };
      };
      const id = await newIsolatedSession(undefined, { isolated: false });
      assertEqual(workspace.worktreeBySession[id].isolated, false);
    });
  });
});
