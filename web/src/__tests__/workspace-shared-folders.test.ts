import { reconcile, unwrap } from "solid-js/store";
import { hoxiAPI } from "../services/api";
import type { WorkspaceProfile } from "../services/api-types";
import {
  isDismissedProjectPath,
  removeWorkspaceProfile,
  saveWorkspaceProfile,
  setWorkspace,
  workspace,
} from "../store/workspace";
import { buildAllProjects } from "../components/layout/left-nav-helpers";
import { assertEqual, describe, it } from "./test-harness";

function profile(id: string, folders: string[]): WorkspaceProfile {
  return {
    id,
    name: id,
    color: "#5EA0F7",
    folders: folders.map((path, position) => ({ id: `${id}_${position}`, path, isPrimary: position === 0, position })),
    createdAt: "",
    updatedAt: "",
  };
}

const first = profile("ws_first", ["/code/first", "/code/shared"]);
const second = profile("ws_second", ["/code/second", "/code/first", "/code/shared"]);

function withSharedWorkspaces(run: () => Promise<void> | void): Promise<void> {
  const saved = structuredClone(unwrap(workspace));
  const origDelete = hoxiAPI.deleteWorkspace;
  const origUpdate = hoxiAPI.updateWorkspace;
  setWorkspace({
    rootDir: "/code/first",
    activeProjectID: first.id,
    activeSessionID: null,
    savedWorkspaces: [first, second],
    projects: [],
    recentProjects: ["/code/first", "/code/second"],
    projectOrder: ["/code/first", "/code/second"],
    dismissedProjects: [],
  });
  return Promise.resolve(run()).finally(() => {
    hoxiAPI.deleteWorkspace = origDelete;
    hoxiAPI.updateWorkspace = origUpdate;
    setWorkspace(reconcile(saved));
  });
}

describe("workspaces sharing folders", () => {
  it("keeps a sidebar row for a workspace whose primary folder another workspace also lists", () =>
    withSharedWorkspaces(() => {
      const entries = buildAllProjects();
      assertEqual(entries.map((e) => e.workspaceId).join(","), "ws_first,ws_second");
      assertEqual(entries.find((e) => e.workspaceId === "ws_second")?.folders.length, 3);
    }));

  it("removing one workspace leaves the shared folders and the other workspace alone", () =>
    withSharedWorkspaces(async () => {
      let deletedID = "";
      hoxiAPI.deleteWorkspace = async (id: string) => {
        deletedID = id;
        return { ok: true, deleted: true };
      };
      await removeWorkspaceProfile(second.id);
      assertEqual(deletedID, "ws_second");
      assertEqual(workspace.savedWorkspaces.map((w) => w.id).join(","), "ws_first");
      assertEqual(isDismissedProjectPath("/code/first"), false);
      assertEqual(isDismissedProjectPath("/code/shared"), false);
      assertEqual(isDismissedProjectPath("/code/second"), true);
      assertEqual(workspace.rootDir, "/code/first");
      assertEqual(workspace.projectOrder.includes("/code/first"), true);
      assertEqual(workspace.recentProjects.includes("/code/first"), true);
    }));

  it("saving a workspace that lists the active workspace's primary folder does not switch away", () =>
    withSharedWorkspaces(async () => {
      const updated = profile("ws_second", ["/code/second", "/code/first"]);
      hoxiAPI.updateWorkspace = async () => updated;
      await saveWorkspaceProfile(
        { folders: [{ path: "/code/second", isPrimary: true }, { path: "/code/first", isPrimary: false }] },
        second.id
      );
      assertEqual(workspace.rootDir, "/code/first");
      assertEqual(workspace.activeProjectID, "ws_first");
      assertEqual(buildAllProjects().length, 2);
    }));
});
