import {
  extractCommand,
  extractDescription,
  extractShellOutput,
  extractRollingShellLines,
} from "../components/timeline/tool-card-helpers";
import type { ToolPart, ToolState } from "../services/sse-types";
import { assertDeepEqual, assertEqual, describe, it } from "./test-harness";

const marker = "# hoxi-stop:ses_display:call_1";
const command = "git status\nprintf 'done\\n'";
const taggedCommand = `${marker}\ntrap : EXIT\n${command}`;

function shellPart(state: ToolState): ToolPart {
  return { id: "prt_display", sessionID: "ses_display", messageID: "msg_display", callID: "call_1", type: "tool", tool: "bash", state };
}

describe("shell tool display", () => {
  it("cleans historical and live tool cards in every lifecycle state without changing stop identities or input", () => {
    const states: ToolState[] = [
      { status: "pending", input: { command: taggedCommand }, raw: "", title: marker.slice(2) },
      { status: "running", input: { command: taggedCommand }, title: marker, time: { start: 1 } },
      { status: "completed", input: { command: taggedCommand }, title: marker.slice(2), output: "done", metadata: {}, time: { start: 1, end: 2 } },
      { status: "error", input: { command: taggedCommand }, title: marker, error: "failed", time: { start: 1, end: 2 } },
    ];
    for (const state of states) {
      const part = shellPart(state);
      const original = JSON.stringify(part);
      assertEqual(extractCommand(part), command);
      assertEqual(extractDescription(part), "git status");
      assertEqual(JSON.stringify(part), original);
    }
  });

  it("handles pending partial JSON without flattening the marker and real command together", () => {
    for (const key of ["command", "cmd"]) {
      const complete = JSON.stringify({ [key]: taggedCommand });
      for (const raw of [complete, complete.slice(0, -2), complete.slice(0, -10)]) {
        const part = shellPart({ status: "pending", input: {}, raw, title: marker.slice(2) });
        assertEqual(extractCommand(part).startsWith("git status\nprintf"), true);
        assertEqual(extractDescription(part), "git status");
      }
    }
  });

  it("keeps meaningful descriptions and supports old tags and CRLF", () => {
    for (const prefix of [`${marker}\r\ntrap : EXIT\r\n`, `trap : EXIT ${marker}\n`, `${marker}\ntrap : EXIT\n${marker}\ntrap : EXIT\n`]) {
      const part = shellPart({ status: "running", input: { command: prefix + command }, title: prefix + "Inspect working tree", time: { start: 1 } });
      assertEqual(extractCommand(part), command);
      assertEqual(extractDescription(part), "Inspect working tree");
      part.state.input.description = "Read repository status";
      assertEqual(extractDescription(part), "Read repository status");
    }
  });

  it("preserves actual output, literal marker arguments and unrelated tools", () => {
    const literalCommand = `printf '%s' '${marker}'`;
    const part = shellPart({ status: "completed", input: { command: literalCommand }, title: "Print marker example", output: marker, metadata: {}, time: { start: 1, end: 2 } });
    assertEqual(extractCommand(part), literalCommand);
    assertEqual(extractShellOutput(part.state), marker);
    assertDeepEqual(extractRollingShellLines(part.state), [marker]);
    part.tool = "custom";
    part.state.input.command = taggedCommand;
    part.state.title = marker;
    assertEqual(extractCommand(part), taggedCommand);
    assertEqual(extractDescription(part), marker);
  });
});
