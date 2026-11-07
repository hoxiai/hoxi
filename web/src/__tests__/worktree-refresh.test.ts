import { reconcile, unwrap } from "solid-js/store";
import { hoxiAPI } from "../services/api";
import { resolveSessionDiffForFile } from "../components/timeline/file-diff-lines";
import {
  buildConvoyConflictResolutionPrompt,
  buildSandboxConflictResolutionPrompt,
  buildWorktreeVerifyFailurePrompt,
  isSandboxMergeConflictError,
  openChangedFileInInspector,
  resolveProjectMemoryDirectory,
  setWorkspace,
  workspace,
  type ConvoyPlan,
} from "../store/workspace";
import { assertEqual, assertTrue, describe, it } from "./test-harness";
import {
  cancelScheduledWorktreeStatus,
  scheduleRefreshWorktreeStatus,
} from "../store/session-diffs";

describe("worktree refresh lifecycle", () => {
  it("does not request status after the scheduled refresh is cancelled", async () => {
    const original = hoxiAPI.getWorktreeStatus;
    let calls = 0;
    hoxiAPI.getWorktreeStatus = async () => { calls++; throw new Error("cancelled refresh reached transport"); };
    try {
      scheduleRefreshWorktreeStatus("ses_cancelled_refresh", 1);
      cancelScheduledWorktreeStatus("ses_cancelled_refresh");
      await new Promise((resolve) => setTimeout(resolve, 15));
      assertEqual(calls, 0);
    } finally {
      cancelScheduledWorktreeStatus("ses_cancelled_refresh");
      hoxiAPI.getWorktreeStatus = original;
    }
  });

  it("resolves nested sub-repository diffs from worktreeDiffsBySession without colliding same-basename files", () => {
    const saved = structuredClone(unwrap(workspace));
    try {
      const sid = "ses_nested_repos";
      setWorkspace({
        rootDir: "/multi-root",
        activeSessionID: sid,
        diffsBySession: { [sid]: [] },
        worktreeDiffsBySession: {
          [sid]: [
            {
              file: "service-a/main.go",
              repo: "service-a",
              branch: "main",
              patch: "@@ -1,1 +1,2 @@\n package main\n+func a() {}\n",
              additions: 1,
              deletions: 0,
              status: "modified",
            },
            {
              file: "packages/service-b/main.go",
              repo: "packages/service-b",
              branch: "feat/sub",
              patch: "@@ -1,1 +1,3 @@\n package main\n+func b1() {}\n+func b2() {}\n",
              additions: 2,
              deletions: 0,
              status: "modified",
            },
          ],
        },
      });

      const resolvedB = resolveSessionDiffForFile("packages/service-b/main.go", sid);
      assertEqual(resolvedB?.file, "packages/service-b/main.go");
      assertEqual(resolvedB?.repo, "packages/service-b");
      assertEqual(resolvedB?.branch, "feat/sub");
      assertEqual(resolvedB?.additions, 2);

      openChangedFileInInspector("packages/service-b/main.go");
      assertEqual(workspace.inspectedFilePath, "packages/service-b/main.go");
      assertEqual(workspace.inspectedFileDiff?.repo, "packages/service-b");
      assertEqual(workspace.inspectedFileLine, 3);
    } finally {
      setWorkspace(reconcile(saved));
    }
  });

  it("detects sandbox merge/rebase conflicts and builds structured resolution & verify self-heal prompts", () => {
    assertEqual(isSandboxMergeConflictError("worktree merge conflict: git apply --3way failed"), true);
    assertEqual(isSandboxMergeConflictError("CONFLICT (content): Merge conflict in pkg/a.go"), true);
    assertEqual(isSandboxMergeConflictError("network timeout"), false);

    const conflictPrompt = buildSandboxConflictResolutionPrompt({
      sessionID: "ses_1",
      sandboxBranch: "hoxi/ses_1",
      baseBranch: "main",
      errorDetail: "worktree merge conflict: CONFLICT (content): Merge conflict in pkg/a.go",
    });
    assertTrue(conflictPrompt.includes("hoxi/ses_1"));
    assertTrue(conflictPrompt.includes("main"));
    assertTrue(conflictPrompt.includes("<merge_conflict>"));

    const verifyPrompt = buildWorktreeVerifyFailurePrompt({
      kind: "test",
      command: "go test ./...",
      output: "FAIL github.com/hoxiai/hoxi/pkg",
    });
    assertTrue(verifyPrompt.includes("go test ./..."));
    assertTrue(verifyPrompt.includes("<verify_output>"));

    const plan: ConvoyPlan = {
      id: "cvy_1",
      title: "Parallel Subtasks",
      directory: "/repo",
      isolated: true,
      concurrency: 2,
      status: "succeeded",
      createdAt: 1,
      updatedAt: 2,
      tasks: [
        { id: "task-a", prompt: "Update a", status: "succeeded", sessionID: "ses_sub_a" },
        { id: "task-b", prompt: "Update b", status: "succeeded", sessionID: "ses_sub_b" },
      ],
    };
    const convoyPrompt = buildConvoyConflictResolutionPrompt(
      plan,
      "worktree merge conflict: CONFLICT in shared.go"
    );
    assertTrue(convoyPrompt.includes("task-a"));
    assertTrue(convoyPrompt.includes("hoxi/ses_sub_a"));
    assertTrue(convoyPrompt.includes("<convoy_merge_conflict>"));
  });

  it("resolves isolated worktree paths back to the project root for .hoxi/memory lookup", () => {
    assertEqual(
      resolveProjectMemoryDirectory("/Users/dev/project/.hoxi/worktrees/ses_123"),
      "/Users/dev/project"
    );
    assertEqual(
      resolveProjectMemoryDirectory("C:\\code\\project\\.hoxi\\worktrees\\ses_123\\sub"),
      "C:/code/project"
    );
    assertEqual(resolveProjectMemoryDirectory("/Users/dev/project"), "/Users/dev/project");
  });
});

