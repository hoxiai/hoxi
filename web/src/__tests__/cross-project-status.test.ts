import { reconcile, unwrap } from "solid-js/store";
import { hoxiAPI } from "../services/api";
import { indexSession } from "../store/session-index";
import {
  applyManagedStatusSnapshot,
  syncSessionStatuses,
} from "../store/session-status-sync";
import { isManagedSession, sessionDirectory } from "../store/session-query";
import { syncPendingPermissions } from "../store/approval-mode";
import { setWorkspace, workspace } from "../store/workspace";
import { assertEqual, describe, it } from "./test-harness";

describe("cross-project execution status", () => {
  it("keeps a running session busy after switching to another project", async () => {
    const saved = structuredClone(unwrap(workspace));
    const originalAPI = { ...hoxiAPI };
    try {
      setWorkspace({
        rootDir: "/repo-b",
        activeProjectID: "prj_b",
        sessions: [
          { id: "ses_hoxi_b", title: "B", directory: "/repo-b", projectID: "prj_b",
            managed: true, time: { created: 1, updated: 1 } },
        ],
        statusBySession: { ses_hoxi_a: { type: "busy" } },
        sessionIndex: {},
      });
      indexSession({ id: "ses_hoxi_a", title: "A", directory: "/repo-a", projectID: "prj_a",
        managed: true, time: { created: 1, updated: 1 } });

      // The project A session is still in the runner's process table.
      applyManagedStatusSnapshot(["ses_hoxi_a"]);
      assertEqual(workspace.statusBySession["ses_hoxi_a"]?.type, "busy");
      assertEqual(sessionDirectory("ses_hoxi_a"), "/repo-a");
      assertEqual(isManagedSession("ses_hoxi_a"), true);

      // A per-directory query for the active project must not clear it.
      hoxiAPI.getSessionStatuses = async (dir) => {
        assertEqual(dir, "/repo-b");
        return { ses_hoxi_b: { type: "idle" } };
      };
      await syncSessionStatuses();
      assertEqual(workspace.statusBySession["ses_hoxi_a"]?.type, "busy");
    } finally {
      Object.assign(hoxiAPI, originalAPI);
      setWorkspace(reconcile(saved));
    }
  });

  it("treats an authoritative snapshot as the idle signal for managed sessions", () => {
    const saved = structuredClone(unwrap(workspace));
    try {
      setWorkspace({
        rootDir: "/repo-b",
        activeProjectID: "prj_b",
        sessions: [],
        statusBySession: { ses_hoxi_a: { type: "busy" } },
        sessionIndex: {},
      });
      indexSession({ id: "ses_hoxi_a", title: "A", directory: "/repo-a", projectID: "prj_a",
        managed: true, time: { created: 1, updated: 1 } });
      applyManagedStatusSnapshot(["ses_hoxi_a"]);
      applyManagedStatusSnapshot([]);
      assertEqual(workspace.statusBySession["ses_hoxi_a"]?.type, "idle");
    } finally {
      setWorkspace(reconcile(saved));
    }
  });

  it("restores stall flags for running sessions from the snapshot", () => {
    const saved = structuredClone(unwrap(workspace));
    try {
      setWorkspace({
        rootDir: "/repo-b",
        activeProjectID: "prj_b",
        sessions: [],
        statusBySession: { ses_hoxi_stalled: { type: "busy" } },
        sessionIndex: {},
      });
      indexSession({ id: "ses_hoxi_stalled", title: "S", directory: "/repo-a", projectID: "prj_a",
        managed: true, time: { created: 1, updated: 1 } });
      applyManagedStatusSnapshot(["ses_hoxi_stalled"], { ses_hoxi_stalled: 1234 });
      assertEqual(workspace.stalledSinceBySession["ses_hoxi_stalled"], 1234);
      applyManagedStatusSnapshot(["ses_hoxi_stalled"]);
      assertEqual(workspace.stalledSinceBySession["ses_hoxi_stalled"], 1234);
    } finally {
      setWorkspace(reconcile(saved));
    }
  });

  it("keeps approvals from other projects while refreshing the active one", async () => {
    const saved = structuredClone(unwrap(workspace));
    const originalAPI = { ...hoxiAPI };
    try {
      setWorkspace({
        rootDir: "/repo-b",
        activeProjectID: "prj_b",
        sessions: [],
        sessionIndex: {},
        statusBySession: {},
        pendingPermissions: [
          { id: "per_a", sessionID: "ses_hoxi_a", permission: "bash", patterns: ["rm -rf build"],
            metadata: {}, always: [] },
        ],
      });
      indexSession({ id: "ses_hoxi_a", title: "A", directory: "/repo-a", projectID: "prj_a",
        managed: true, time: { created: 1, updated: 1 } });
      hoxiAPI.listPermissions = async (dir) => {
        assertEqual(dir, "/repo-b");
        return [];
      };
      await syncPendingPermissions();
      assertEqual(workspace.pendingPermissions.length, 1);
      assertEqual(workspace.pendingPermissions[0].id, "per_a");
    } finally {
      Object.assign(hoxiAPI, originalAPI);
      setWorkspace(reconcile(saved));
    }
  });

  it("never retargets a foreign session at the active project root", () => {
    const saved = structuredClone(unwrap(workspace));
    try {
      setWorkspace({
        rootDir: "/repo-b",
        activeProjectID: "prj_b",
        sessions: [],
        sessionIndex: {},
      });
      indexSession({ id: "ses_hoxi_foreign", title: "F", projectID: "prj_a",
        managed: true, time: { created: 1, updated: 1 } });
      assertEqual(sessionDirectory("ses_hoxi_foreign"), undefined);
      // An unknown id may still fall back to the active root (fresh drafts).
      assertEqual(sessionDirectory("ses_unknown_draft"), "/repo-b");
    } finally {
      setWorkspace(reconcile(saved));
    }
  });

  it("prunes stale approvals for worktree sessions in the refreshed project", async () => {
    const saved = structuredClone(unwrap(workspace));
    const originalAPI = { ...hoxiAPI };
    try {
      setWorkspace({
        rootDir: "/repo-b",
        activeProjectID: "prj_b",
        sessions: [],
        sessionIndex: {},
        statusBySession: {},
        pendingPermissions: [
          {
            id: "per_wt_b",
            sessionID: "ses_hoxi_wt_b",
            permission: "bash",
            patterns: ["python3 run.py"],
            metadata: {},
            always: [],
          },
        ],
      });
      indexSession({
        id: "ses_hoxi_wt_b",
        title: "WT B",
        directory: "/repo-b/.hoxi/worktrees/ses_hoxi_wt_b",
        projectID: "prj_b",
        managed: true,
        time: { created: 1, updated: 1 },
      });
      hoxiAPI.listPermissions = async (dir) => {
        assertEqual(dir, "/repo-b");
        return [];
      };
      await syncPendingPermissions();
      assertEqual(workspace.pendingPermissions.length, 0);
    } finally {
      Object.assign(hoxiAPI, originalAPI);
      setWorkspace(reconcile(saved));
    }
  });
});
