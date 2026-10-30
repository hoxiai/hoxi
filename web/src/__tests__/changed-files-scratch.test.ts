// web/src/__tests__/changed-files-scratch.test.ts
import {
  collectCycleChangedFiles,
  collectSessionChangedFiles,
  isSessionScratchFile,
} from "../components/timeline/tool-card-state";
import type { MessagePart } from "../services/sse-types";
import { type MessageTurn, setWorkspace, workspace } from "../store/workspace";
import { assertDeepEqual, assertEqual, describe, it } from "./test-harness";

const sid = "ses_scratch";

const write = (id: string, filePath: string): MessagePart =>
  ({
    id,
    sessionID: sid,
    messageID: "msg_a",
    type: "tool",
    callID: id,
    tool: "write",
    state: {
      status: "completed",
      input: { filePath, content: "line 1\nline 2" },
      output: "",
      title: "",
      metadata: {},
      time: { start: 1, end: 2 },
    },
  }) as MessagePart;

const cycle = (parts: MessagePart[]): MessageTurn[] => [
  {
    info: { id: "msg_u", sessionID: sid, role: "user", time: { created: 1 } },
    parts: [],
  },
  {
    info: {
      id: "msg_a",
      sessionID: sid,
      role: "assistant",
      time: { created: 2, completed: 3 },
    },
    parts,
  },
];

function withRoot(rootDir: string, fn: () => void): void {
  const prevRoot = workspace.rootDir;
  setWorkspace("rootDir", rootDir);
  setWorkspace("worktreeBySession", sid, {
    sessionID: sid,
    isolated: false,
    directory: rootDir,
    createdAt: "test",
  });
  try {
    fn();
  } finally {
    setWorkspace("rootDir", prevRoot);
    setWorkspace("worktreeBySession", sid, undefined!);
  }
}

describe("changed files exclude system temp scratch files", () => {
  it("drops /tmp, /private/tmp, macOS /var/folders and Windows Temp edits", () => {
    withRoot("/repo", () => {
      const turns = cycle([
        write("w1", "/repo/src/app.ts"),
        write("w2", "/tmp/probe.py"),
        write("w3", "/private/tmp/claude/out.json"),
        write("w4", "/var/folders/ab/xyz123/T/scratch.txt"),
        write("w5", "C:\\Users\\dev\\AppData\\Local\\Temp\\run.ps1"),
        write("w6", "/other/project/notes.md"),
      ]);
      assertDeepEqual(
        collectSessionChangedFiles(turns, sid).map((d) => d.file),
        ["src/app.ts", "/other/project/notes.md"],
      );
    });
  });

  it("keeps paths that only look like temp dirs", () => {
    withRoot("/repo", () => {
      assertEqual(isSessionScratchFile("/tmpfiles/a.ts", sid), false);
      assertEqual(isSessionScratchFile("tmp/a.ts", sid), false);
      assertEqual(
        isSessionScratchFile("/var/folders/ab/xyz/C/a.ts", sid),
        false,
      );
    });
  });

  it("still counts edits when the project itself lives in a temp dir", () => {
    withRoot("/tmp/demo-project", () => {
      const turns = cycle([
        write("w1", "/tmp/demo-project/index.ts"),
        write("w2", "/tmp/elsewhere.log"),
      ]);
      assertDeepEqual(
        collectCycleChangedFiles(turns, sid).map((d) => d.file),
        ["index.ts"],
      );
    });
  });

  it("can include scratch files for per-file diff lookups", () => {
    withRoot("/repo", () => {
      const turns = cycle([write("w1", "/tmp/probe.py")]);
      assertDeepEqual(
        collectCycleChangedFiles(turns, sid, { includeScratch: true }).map(
          (d) => d.file,
        ),
        ["/tmp/probe.py"],
      );
    });
  });

  it("matches a Windows temp project root regardless of separator or drive case", () => {
    withRoot("c:/Users/dev/AppData/Local/Temp/proj", () => {
      assertEqual(
        isSessionScratchFile("C:\\Users\\dev\\AppData\\Local\\Temp\\proj\\a.ts", sid),
        false,
      );
      assertEqual(
        isSessionScratchFile("C:\\Users\\dev\\AppData\\Local\\Temp\\other\\a.ts", sid),
        true,
      );
    });
  });
});
