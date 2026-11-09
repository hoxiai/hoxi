import { reconcile, unwrap } from "solid-js/store";
import { hoxiAPI } from "../services/api";
import type { SnapshotFileDiff } from "../services/sse-types";
import {
  COMMIT_MODEL_STORAGE_KEY,
  commitModelSpec,
  generateSmartCommitMessage,
  resolveEffectiveCommitModel,
  setCommitModelSpec,
  uncommittedSessionFiles,
} from "../store/git-commit";
import { readStorageItem, setWorkspace, workspace, writeStorageItem } from "../store/workspace";
import { assertDeepEqual, assertEqual, describe, it } from "./test-harness";

let nextID = 0;

describe("git commit helpers", () => {
  it("passes sessionDirectory(id) when generateSmartCommitMessage fetches uncached worktree diffs", async () => {
    const saved = structuredClone(unwrap(workspace));
    const originalAPI = { ...hoxiAPI };
    const originalSpec = commitModelSpec();
    const id = `ses_git_commit_${++nextID}`;
    const directory = `/commit-test/.hoxi/worktrees/${id}`;
    const diffs: SnapshotFileDiff[] = [{
      file: "src/task.ts", before: "old\n", after: "new\n", status: "modified",
      additions: 1, deletions: 1,
    }];
    const worktreeDiffCalls: Parameters<typeof hoxiAPI.getWorktreeDiff>[] = [];
    try {
      setCommitModelSpec("none");
      setWorkspace({
        rootDir: "/unrelated-root",
        activeSessionID: "ses_other",
        sessions: [{
          id, parentID: "fixture-parent", title: "Commit fixture", directory,
          time: { created: 1, updated: 1 },
        }],
      });
      setWorkspace("diffsBySession", reconcile({}));
      hoxiAPI.getWorktreeDiff = async (...args) => {
        worktreeDiffCalls.push(args);
        return diffs;
      };
      const msg = await generateSmartCommitMessage(id);
      assertEqual(msg, "feat(src): update task");
      assertDeepEqual(worktreeDiffCalls, [[id, directory]]);
    } finally {
      Object.assign(hoxiAPI, originalAPI);
      setWorkspace(reconcile(saved));
      setCommitModelSpec(originalSpec);
    }
  });

  it("preserves stored none opt-out and never sends diffs to LLM providers", async () => {
    const saved = structuredClone(unwrap(workspace));
    const originalAPI = { ...hoxiAPI };
    const originalSpec = commitModelSpec();
    const storageDescriptor = Object.getOwnPropertyDescriptor(globalThis, "localStorage");
    const values = new Map<string, string>();
    const storage: Storage = {
      get length() { return values.size; },
      clear: () => values.clear(),
      getItem: (key) => values.get(key) ?? null,
      key: (index) => [...values.keys()][index] ?? null,
      removeItem: (key) => { values.delete(key); },
      setItem: (key, value) => { values.set(key, String(value)); },
    };
    Object.defineProperty(globalThis, "localStorage", { configurable: true, value: storage });
    const id = `ses_git_optout_${++nextID}`;
    const directory = `/commit-test/.hoxi/worktrees/${id}`;
    const diffs: SnapshotFileDiff[] = [{
      file: "src/secret.ts", before: "old\n", after: "new\n", status: "modified",
      additions: 1, deletions: 1,
    }];
    let llmCalled = false;
    try {
      setCommitModelSpec("");
      writeStorageItem(COMMIT_MODEL_STORAGE_KEY, "none");
      setWorkspace({
        rootDir: "/commit-test",
        activeSessionID: id,
        globalConfig: {
          model: "audit-provider/audit-mini",
          small_model: "audit-provider/audit-mini",
        },
        defaultAI: {
          model: { providerID: "audit-provider", modelID: "audit-mini" },
          agent: "build",
          effort: "",
        },
        aiBySession: {
          [id]: {
            model: { providerID: "audit-provider", modelID: "audit-mini" },
            agent: "build",
            effort: "",
          },
        },
        connectedProviders: ["audit-provider"],
        providers: [{
          id: "audit-provider",
          name: "Audit Provider",
          models: { "audit-mini": { id: "audit-mini", name: "Audit Mini" } },
        }],
        sessions: [{
          id, title: "Opt-out fixture", directory,
          time: { created: 1, updated: 1 },
        }],
      });
      setWorkspace("diffsBySession", reconcile({ [id]: diffs }));
      hoxiAPI.generateCommitMessage = async () => {
        llmCalled = true;
        return { ok: true, message: "feat: leaked diff" };
      };
      assertEqual(readStorageItem(COMMIT_MODEL_STORAGE_KEY), "none");
      assertEqual(resolveEffectiveCommitModel(), null);
      const msg = await generateSmartCommitMessage(id, diffs);
      assertEqual(llmCalled, false);
      assertEqual(msg, "feat(src): update secret");
    } finally {
      Object.assign(hoxiAPI, originalAPI);
      setWorkspace(reconcile(saved));
      setCommitModelSpec(originalSpec);
      if (storageDescriptor) {
        Object.defineProperty(globalThis, "localStorage", storageDescriptor);
      } else {
        Reflect.deleteProperty(globalThis, "localStorage");
      }
    }
  });

  it("tracks real worktree diffs via uncommittedSessionFiles", () => {
    const saved = structuredClone(unwrap(workspace));
    try {
      const id = `ses_gate_${++nextID}`;
      const diffs: SnapshotFileDiff[] = [{
        file: "src/a.ts", before: "", after: "x\n", status: "added",
        additions: 1, deletions: 0,
      }];
      setWorkspace("diffsBySession", reconcile({ [id]: diffs }));
      assertDeepEqual(uncommittedSessionFiles(id), diffs, "reads real worktree diffs");
      assertDeepEqual(uncommittedSessionFiles(""), []);
      assertDeepEqual(uncommittedSessionFiles("ses_absent"), []);
    } finally {
      setWorkspace(reconcile(saved));
    }
  });
});
