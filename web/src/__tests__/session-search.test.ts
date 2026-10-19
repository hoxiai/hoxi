import type { SessionInfo } from "../services/sse-types";
import {
  matchesProjectSearch, matchesProjectSessionSearch, matchesSessionSearch,
} from "../components/layout/session-search";
import { assertEqual, describe, it } from "./test-harness";

describe("Sidebar task search", () => {
  const project = { name: "Workspace", path: "/code/app", folders: [{ path: "/code/backend" }] };
  const session: SessionInfo = {
    id: "ses_search", title: "Fix login regression", slug: "bright-otter",
    time: { created: 1, updated: 1 },
  };

  it("keeps a project when only its task title matches", () => {
    assertEqual(matchesProjectSearch(project, "login"), false);
    assertEqual(matchesProjectSessionSearch(project, [session], "  LOGIN  "), true);
    assertEqual(matchesSessionSearch(session, "  LOGIN  "), true);
  });

  it("matches project names and folder paths independently of task titles", () => {
    for (const query of ["workspace", "/code/app", "backend"]) {
      assertEqual(matchesProjectSessionSearch(project, [session], query), true);
      assertEqual(matchesProjectSearch(project, query), true);
    }
  });

  it("includes slug and archived matches but excludes child tasks", () => {
    assertEqual(matchesProjectSessionSearch(project, [session], "otter"), true);
    assertEqual(matchesProjectSessionSearch(project, [
      { ...session, time: { ...session.time, archived: 2 } },
    ], "login"), true);
    assertEqual(matchesProjectSessionSearch(project, [
      { ...session, parentID: "ses_parent" },
    ], "login"), false);
  });

  it("rejects unrelated projects and restores all projects for an empty query", () => {
    assertEqual(matchesProjectSessionSearch(project, [session], "missing"), false);
    assertEqual(matchesProjectSessionSearch(project, [], "  "), true);
  });
});
