import { reconcile, unwrap } from "solid-js/store";
import { hoxiAPI } from "../services/api";
import { resolveSessionDiffForFile } from "../components/timeline/file-diff-lines";
import { openChangedFileInInspector, setWorkspace, workspace } from "../store/workspace";
import { assertEqual, describe, it } from "./test-harness";
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
});

