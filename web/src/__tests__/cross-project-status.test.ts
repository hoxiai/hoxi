import { reconcile, unwrap } from "solid-js/store";
import { hoxiAPI } from "../services/api";
import { indexSession } from "../store/session-index";
import {
  applyManagedStatusSnapshot,
  syncSessionStatuses,
} from "../store/session-status-sync";
import { isManagedSession, sessionDirectory } from "../store/session-query";
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
});
