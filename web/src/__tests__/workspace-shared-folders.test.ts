import { reconcile, unwrap } from "solid-js/store";
import { hoxiAPI } from "../services/api";
import type { WorkspaceProfile } from "../services/api-types";
import {
  findWorkspaceProfileByDir,
  isDismissedProjectPath,
  parseFileReference,
  removeWorkspaceProfile,
  saveWorkspaceProfile,
  setWorkspace,
  stripRootPrefix,
  workspace,
} from "../store/workspace";
import { buildAllProjects } from "../components/layout/left-nav-helpers";
import { getDirectory } from "../components/timeline/tool-card-helpers";
import { normalizeWorkspaceRelPath } from "../components/timeline/tool-card-state";
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

  it("rebuilds the folder lookup after in-place edits that keep the workspace count", () =>
    withSharedWorkspaces(() => {
      // Prime the cache so the lookups below must notice the edits themselves.
      assertEqual(findWorkspaceProfileByDir("/code/moved"), undefined);
      assertEqual(findWorkspaceProfileByDir("/code/first")?.id, "ws_first");

      setWorkspace("savedWorkspaces", 0, "folders", [
        { id: "ws_first_0", path: "/code/moved", isPrimary: true, position: 0 },
      ]);
      assertEqual(findWorkspaceProfileByDir("/code/moved")?.id, "ws_first");

      setWorkspace("savedWorkspaces", 0, "folders", 0, "path", "/code/renamed");
      assertEqual(findWorkspaceProfileByDir("/code/moved"), undefined);
      assertEqual(findWorkspaceProfileByDir("/code/renamed")?.id, "ws_first");
    }));

  it("strips Windows drive root prefixes with backslashes and mixed casing", () =>
    withSharedWorkspaces(() => {
      assertEqual(stripRootPrefix("C:\\Users\\Dev\\Repo\\src\\App.tsx", "c:/users/dev/repo/"), "src/App.tsx");
      assertEqual(stripRootPrefix("c:/users/dev/repo", "C:\\Users\\Dev\\Repo\\"), ".");
      assertEqual(stripRootPrefix("C:\\Users\\Dev\\RepoOther\\a.ts", "C:\\Users\\Dev\\Repo"), null);
      assertEqual(stripRootPrefix("D:\\Users\\Dev\\Repo\\a.ts", "C:\\Users\\Dev\\Repo"), null);

      setWorkspace({ rootDir: "C:\\Users\\Dev\\Repo", activeInspectorFolder: null });
      assertEqual(parseFileReference("C:\\Users\\Dev\\Repo\\src\\App.tsx:42").path, "src/App.tsx");
      assertEqual(parseFileReference("C:\\Users\\Dev\\Repo\\src\\App.tsx:42").line, 42);
      assertEqual(normalizeWorkspaceRelPath("c:\\users\\dev\\repo\\.hoxi\\worktrees\\wt1\\src\\a.ts"), "src/a.ts");
      assertEqual(getDirectory("C:\\Users\\Dev\\Repo\\src\\components\\Button.tsx"), "src/components");
    }));
});
