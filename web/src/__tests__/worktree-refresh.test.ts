import { reconcile, unwrap } from "solid-js/store";
import { hoxiAPI, type WorktreeStatus } from "../services/api";
import type { ToolPart } from "../services/sse-types";
import { resolveSessionDiffForFile } from "../components/timeline/file-diff-lines";
import {
  buildConvoyConflictResolutionPrompt,
  buildDefaultPullRequestBody,
  buildSandboxConflictResolutionPrompt,
  buildWorktreeVerifyFailurePrompt,
  clearMemoryBankDraft,
  createBranchPullRequest,
  getMemoryBankDraft,
  isSandboxMergeConflictError,
  openChangedFileInInspector,
  resetMemoryBankDrafts,
  resolveMemoryBankTarget,
  resolveProjectMemoryDirectory,
  setMemoryBankDraft,
  setWorkspace,
  workspace,
  type ConvoyPlan,
} from "../store/workspace";
import { assertEqual, assertTrue, describe, it } from "./test-harness";
import {
  cancelScheduledSessionDiff,
  cancelScheduledWorktreeStatus,
  refreshWorktreeStatus,
  scheduleRefreshSessionDiff,
  scheduleRefreshWorktreeStatus,
} from "../store/session-diffs";
import { reconcileSessionsInPlace } from "../store/workspace-sessions";
import { applyStreamFrameBatch } from "../store/session-stream";

describe("worktree refresh lifecycle", () => {
  it("does not fire scheduled worktree or diff refresh after a mounted session is unloaded without deleteConversation", async () => {
    const saved = structuredClone(unwrap(workspace));
    const origStatus = hoxiAPI.getWorktreeStatus;
    const origDiff = hoxiAPI.getSessionDiff;
    const sid = "ses_unloaded_without_delete";
    let statusCalls = 0;
    let diffCalls = 0;
    hoxiAPI.getWorktreeStatus = async () => { statusCalls++; throw new Error("stale worktree refresh fired"); };
    hoxiAPI.getSessionDiff = async () => { diffCalls++; throw new Error("stale diff refresh fired"); };
    try {
      setWorkspace({
        rootDir: "/repo",
        activeSessionID: sid,
        sessions: [{ id: sid, title: "Mounted", time: { created: 1, updated: 1 } }],
      });
      scheduleRefreshWorktreeStatus(sid, 10);
      scheduleRefreshSessionDiff(sid, 10);
      // Session list resets / switches out without markSessionDeleted; rootDir remains "/repo".
      setWorkspace("activeSessionID", null);
      reconcileSessionsInPlace([]);
      await new Promise((resolve) => setTimeout(resolve, 25));
      assertEqual(statusCalls, 0);
      assertEqual(diffCalls, 0);
    } finally {
      cancelScheduledWorktreeStatus(sid);
      cancelScheduledSessionDiff(sid);
      hoxiAPI.getWorktreeStatus = origStatus;
      hoxiAPI.getSessionDiff = origDiff;
      setWorkspace(reconcile(saved));
    }
  });
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

  it("coalesces automatic refreshes while the previous worktree read is pending", async () => {
    const saved = structuredClone(unwrap(workspace));
    const original = hoxiAPI.getWorktreeStatus;
    const sid = "ses_coalesced_worktree_refresh";
    const status: WorktreeStatus = {
      sessionID: sid, directory: "/repo", isolated: false, dirty: false,
      changedFiles: 0, uncommittedLines: 0, additions: 0, deletions: 0,
    };
    let release!: (status: WorktreeStatus) => void;
    const firstResponse = new Promise<WorktreeStatus>((resolve) => { release = resolve; });
    let calls = 0;
    hoxiAPI.getWorktreeStatus = () => ++calls === 1 ? firstResponse : Promise.resolve(status);
    const tick = () => new Promise<void>((resolve) => setTimeout(resolve, 0));
    try {
      setWorkspace({ rootDir: "/repo", activeSessionID: null });
      const first = refreshWorktreeStatus(sid);
      scheduleRefreshWorktreeStatus(sid, 0);
      await tick();
      scheduleRefreshWorktreeStatus(sid, 0);
      await tick();
      assertEqual(calls, 1);
      release(status);
      await first;
      await tick();
      assertEqual(calls, 2);
    } finally {
      cancelScheduledWorktreeStatus(sid);
      release(status);
      hoxiAPI.getWorktreeStatus = original;
      setWorkspace(reconcile(saved));
    }
  });

  it("refreshes Git once when an editing tool settles, even if it fails after making changes", async () => {
    const saved = structuredClone(unwrap(workspace));
    const originalStatus = hoxiAPI.getWorktreeStatus;
    const originalDiff = hoxiAPI.getSessionDiff;
    const sid = "ses_tool_worktree_refresh";
    const part = { id: "prt_edit", sessionID: sid, messageID: "msg_edit", type: "tool", callID: "edit", tool: "bash" } as const;
    const apply = (state: ToolPart["state"]) => applyStreamFrameBatch({ infos: [], parts: [{ ...part, state }], deltas: [] });
    const waitForRefresh = () => new Promise<void>((resolve) => setTimeout(resolve, 275));
    let calls = 0;
    hoxiAPI.getWorktreeStatus = async () => {
      calls++;
      return { sessionID: sid, directory: "/repo", isolated: false, dirty: false,
        changedFiles: 0, uncommittedLines: 0, additions: 0, deletions: 0 };
    };
    hoxiAPI.getSessionDiff = async () => [];
    try {
      setWorkspace({ rootDir: "/repo", activeSessionID: sid });
      apply({ status: "pending", input: {}, raw: "" });
      await waitForRefresh();
      apply({ status: "running", input: {}, time: { start: 1 } });
      await waitForRefresh();
      assertEqual(calls, 0);
      const failed = { status: "error", input: {}, error: "failed", time: { start: 1, end: 2 } } as const;
      apply(failed);
      await waitForRefresh();
      apply({ ...failed, error: "additional details" });
      await waitForRefresh();
      assertEqual(calls, 1);
    } finally {
      cancelScheduledWorktreeStatus(sid);
      hoxiAPI.getWorktreeStatus = originalStatus;
      hoxiAPI.getSessionDiff = originalDiff;
      setWorkspace(reconcile(saved));
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

  it("never includes private chat messages in default PR creation body", async () => {
    const saved = structuredClone(unwrap(workspace));
    const originalFetch = globalThis.fetch;
    let capturedBody = null as Record<string, unknown> | null;
    try {
      const sid = "ses_pr_privacy";
      setWorkspace({
        rootDir: "/audit-project",
        activeSessionID: sid,
        worktreeBySession: {
          [sid]: {
            sessionID: sid,
            isolated: true,
            directory: "/audit-project/.hoxi/worktrees/" + sid,
            branch: "hoxi/" + sid,
            baseBranch: "main",
            headSubject: "feat(auth): support token rotation",
            dirty: false,
            changedFiles: 1,
            createdAt: "2026-11-07T00:00:00Z",
          },
        },
        diffsBySession: {
          [sid]: [
            {
              file: "internal/auth/rotate.go",
              patch: "@@ -1 +1,2 @@\n+func Rotate() {}\n",
              additions: 1,
              deletions: 0,
              status: "modified",
            },
          ],
        },
        messagesBySession: {
          [sid]: [
            {
              info: { id: "msg_priv", sessionID: sid, role: "user", time: { created: 1 } },
              parts: [
                {
                  id: "prt_priv",
                  messageID: "msg_priv",
                  sessionID: sid,
                  type: "text",
                  text: "PRIVATE_AUDIT_CHAT_MARKER_123 do not publish this secret note",
                },
              ],
            },
          ],
        },
      });

      const defaultBody = buildDefaultPullRequestBody({ sessionID: sid });
      assertEqual(defaultBody.includes("PRIVATE_AUDIT_CHAT_MARKER_123"), false);
      assertTrue(defaultBody.includes("feat(auth): support token rotation"));
      assertTrue(defaultBody.includes("internal/auth/rotate.go"));

      globalThis.fetch = (async (_input: RequestInfo | URL, init?: RequestInit) => {
        if (init?.method === "POST" && init.body) {
          capturedBody = JSON.parse(String(init.body));
        }
        return new Response(
          JSON.stringify({ ok: true, available: true, url: "https://example.invalid/pr/1" }),
          { status: 200, headers: { "Content-Type": "application/json" } }
        );
      }) as typeof fetch;

      await createBranchPullRequest({
        directory: "/audit-project",
        head: "hoxi/" + sid,
        title: "Change title",
      });
      assertTrue(capturedBody !== null);
      const sentBody = String(capturedBody?.body ?? "");
      assertEqual(sentBody.includes("PRIVATE_AUDIT_CHAT_MARKER_123"), false);
    } finally {
      globalThis.fetch = originalFetch;
      setWorkspace(reconcile(saved));
    }
  });

  it("isolates memory bank drafts per scope, project root, and sessionID", () => {
    resetMemoryBankDrafts();
    try {
      const targetA = resolveMemoryBankTarget(
        "session",
        "/Users/dev/project/.hoxi/worktrees/ses_a",
        "ses_a"
      );
      const targetB = resolveMemoryBankTarget(
        "session",
        "/Users/dev/project/.hoxi/worktrees/ses_b",
        "ses_b"
      );
      const targetProj = resolveMemoryBankTarget("project", "/Users/dev/project", "ses_a");

      assertEqual(targetA.directory, "/Users/dev/project");
      assertEqual(targetB.directory, "/Users/dev/project");
      assertEqual(targetA.key !== targetB.key, true);
      assertEqual(targetProj.sessionID, "");

      setMemoryBankDraft(targetA.key, "# Draft A");
      assertEqual(getMemoryBankDraft(targetA.key), "# Draft A");
      assertEqual(getMemoryBankDraft(targetB.key), undefined);
      assertEqual(getMemoryBankDraft(targetProj.key), undefined);

      clearMemoryBankDraft(targetA.key);
      assertEqual(getMemoryBankDraft(targetA.key), undefined);
    } finally {
      resetMemoryBankDrafts();
    }
  });
});

