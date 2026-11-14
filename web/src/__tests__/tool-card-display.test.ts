import {
  extractCommand,
  extractDescription,
  extractShellOutput,
  extractRollingShellLines,
  formatToolDisplayName,
  hasDistinctShellDescription,
  isShellAwaitingFirstOutput,
  processCarriageReturns,
  repairWindowsMojibake,
} from "../components/timeline/tool-card-helpers";
import {
  analyzeShellCommand,
  groupVisibleActivitySteps,
} from "../components/timeline/shell-command-summary";
import {
  groupAssistantParts,
  isShortTransitionalText,
  resolveMergedTurnParts,
} from "../components/timeline/context-activity-turns";
import { summarizeStepTarget } from "../components/timeline/context-activity-summary";
import { resolveReadLineRange, resolveToolArgPills } from "../components/timeline/tool-card-state";
import type { MessagePart, ToolPart, ToolState } from "../services/sse-types";
import { dictionaries, setLocale } from "../i18n";
import { type MessageTurn, setWorkspace, workspace } from "../store/workspace-state";
import { assertDeepEqual, assertEqual, assertTrue, describe, it } from "./test-harness";

const marker = "# hoxi-stop:ses_display:call_1";
const command = "git status\nprintf 'done\\n'";
const taggedCommand = `${marker}\ntrap : EXIT\n${command}`;

function shellPart(state: ToolState): ToolPart {
  return { id: "prt_display", sessionID: "ses_display", messageID: "msg_display", callID: "call_1", type: "tool", tool: "bash", state };
}

describe("shell tool display", () => {
  it("shows a first-output hint for silent live commands, clearing it on output or completion", () => {
    const part = shellPart({ status: "running", input: { command: "node scripts/check-workspace.mjs --mode tenant 2>&1 | grep -Ev '^\\s+(git|gofmt|php|node)' | tail -120" }, time: { start: 1 } });
    assertEqual(isShellAwaitingFirstOutput(part, true, 14.9), false);
    assertEqual(isShellAwaitingFirstOutput(part, true, 15), true);
    assertEqual(isShellAwaitingFirstOutput(part, false, 60), false);
    for (const key of ["output", "stdout"]) {
      part.state.metadata = { [key]: "[gate] running checks" };
      assertEqual(isShellAwaitingFirstOutput(part, true, 60), false);
    }
    part.state = { status: "pending", input: {}, raw: "" };
    assertEqual(isShellAwaitingFirstOutput(part, true, 60), false);
    part.state = { status: "completed", input: {}, output: "", title: "", metadata: {}, time: { start: 1, end: 2 } };
    assertEqual(isShellAwaitingFirstOutput(part, true, 60), false);
    part.state = { status: "running", input: {}, time: { start: 1 } };
    part.tool = "read";
    assertEqual(isShellAwaitingFirstOutput(part, true, 60), false);
  });

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

  it("unwraps single and nested sandbox-exec wrappers and full USR1 stop traps", () => {
    const usr1Trap = String.raw`trap 'printf "\n[Command stopped by user]\n"; exit 130' USR1; trap : EXIT`;
    const wrappedOnce = `${marker}\n${usr1Trap}\n'/usr/bin/sandbox-exec' -f '/tmp/p.sb' -- /bin/sh -c 'git status'`;
    const wrappedNested = `${marker}\n${usr1Trap}\n'/usr/bin/sandbox-exec' -f '/tmp/p.sb' -- /bin/sh -c '${wrappedOnce.replace(/'/g, "'\\''")}'`;
    const part = shellPart({
      status: "completed",
      input: { command: wrappedNested },
      title: wrappedNested,
      output: "clean",
      metadata: {},
      time: { start: 1, end: 2 },
    });
    assertEqual(extractCommand(part), "git status");
    assertEqual(extractDescription(part), "git status");
  });

  it("hides duplicate secondary shell command preview when description is the command itself", () => {
    const withoutDesc = shellPart({
      status: "running",
      input: { command: "cd /repo && grep -rn foo .\necho done" },
      title: "cd /repo && grep -rn foo .",
      time: { start: 1 },
    });
    assertEqual(
      hasDistinctShellDescription(extractDescription(withoutDesc), extractCommand(withoutDesc)),
      false,
    );
    const withDesc = shellPart({
      status: "running",
      input: { command: "cd /repo && grep -rn foo .", description: "Search for foo references" },
      time: { start: 1 },
    });
    assertEqual(
      hasDistinctShellDescription(extractDescription(withDesc), extractCommand(withDesc)),
      true,
    );
    const withToolSummary = shellPart({
      status: "running",
      input: { command: "git log -n 20 --oneline", toolSummary: "Check recent git commits" },
      title: "git log -n 20 --oneline",
      time: { start: 1 },
    });
    assertEqual(extractDescription(withToolSummary), "Check recent git commits");
    assertEqual(
      hasDistinctShellDescription(extractDescription(withToolSummary), extractCommand(withToolSummary)),
      true,
    );
  });
});

describe("localized tool labels for CLI runtimes", () => {
  function part(tool: string, overrides: Partial<ToolState> = {}): ToolPart {
    return {
      id: "prt_label",
      sessionID: "ses_label",
      messageID: "msg_label",
      callID: "call_label",
      type: "tool",
      tool,
      state: { status: "completed", input: {}, ...overrides } as ToolState,
    };
  }

  it("maps Antigravity tool identifiers to localized labels instead of raw names", () => {
    const zh = dictionaries["zh-CN"].toolCard;
    const en = dictionaries["en-US"].toolCard;
    setLocale("zh-CN");
    const expected: Array<[string, string]> = [
      ["run_command", zh.actionShell],
      ["cmd_command", zh.actionShell],
      ["view_file", zh.actionRead],
      ["read_file", zh.actionRead],
      ["list_dir", zh.actionList],
      ["find_by_name", zh.actionFind],
      ["grep_search", zh.actionSearch],
      ["write_to_file", zh.actionWrite],
      ["multi_replace_file_content", zh.actionEdit],
      ["apply_patch", zh.actionPatch],
      ["manage_subagents", zh.actionCheckSubagents],
      ["ask_question", zh.actionQuestion],
      ["search_web", zh.actionWebSearch],
      ["read_url_content", zh.actionWebFetch],
      ["taskcreate", zh.actionTodo],
      ["taskupdate", zh.actionTodo],
      ["manage_task", zh.actionBackgroundTask],
      ["hoxi_browser", zh.actionBrowser],
      ["hoxi_code_index", zh.actionCodeIndex],
    ];
    for (const [tool, label] of expected) {
      assertEqual(formatToolDisplayName(tool), label, tool);
    }
    setLocale("en-US");
    assertEqual(formatToolDisplayName("run_command"), en.actionShell);
    setLocale("zh-CN");
  });

  it("drops a raw tool identifier used as the persisted title", () => {
    setLocale("zh-CN");
    const shell = part("bash", { title: "run_command", input: { command: "ls -la" } });
    assertEqual(extractDescription(shell), "ls -la");
    const read = part("read", { title: "read_file", input: { filePath: "a.ts" } });
    assertEqual(extractDescription(read), "");
    const real = part("read", { title: "Reading a.ts", input: { filePath: "a.ts" } });
    assertEqual(extractDescription(real), "Reading a.ts");
  });

  it("formats line ranges for native read tools and excludes raw offset/limit pills", () => {
    const agyRead = part("read_file", {
      input: {
        Path: "/Users/hugh/code/hoxi/hoxi/web/src/components/timeline/ToolCard.tsx",
        StartLine: "315",
        EndLine: "415",
      },
    });
    assertEqual(resolveReadLineRange(agyRead), "315-415");
    assertEqual(summarizeStepTarget(agyRead), "ToolCard.tsx:315-415");

    const openCodeRead = part("read", {
      input: {
        filePath: "web/src/components/timeline/ToolCard.tsx",
        offset: 100,
        limit: 50,
      },
    });
    assertEqual(resolveReadLineRange(openCodeRead), "100-149");
    assertEqual(summarizeStepTarget(openCodeRead), "ToolCard.tsx:100-149");
    assertDeepEqual(resolveToolArgPills(openCodeRead), []);

    const fullRead = part("read_file", {
      input: {
        Path: "/Users/hugh/code/hoxi/hoxi/web/src/components/timeline/ToolCard.tsx",
      },
    });
    assertEqual(resolveReadLineRange(fullRead), undefined);
    assertEqual(summarizeStepTarget(fullRead), "ToolCard.tsx");
  });

  it("summarizes noisy shell exploration and ssh commands into concise semantic intents", () => {
    const prevRoot = workspace.rootDir;
    setWorkspace("rootDir", "/Users/hugh/code/hoxi/shopark");
    try {

    const sedRead = analyzeShellCommand(
      "cd /Users/hugh/code/hoxi/shopark && sed -n '340,420p' go-fast/app/service/theme_decor_bundle.go"
    );
    assertEqual(sedRead.kind, "read");
    assertEqual(sedRead.targetFile, "go-fast/app/service/theme_decor_bundle.go");
    assertEqual(sedRead.lineRange, "340-420");
    assertEqual(sedRead.summary, "theme_decor_bundle.go:340-420");
    assertEqual(sedRead.isReadOnlyExploration, true);

    const rgSearch = analyzeShellCommand(
      `cd /Users/hugh/code/hoxi/shopark && rg -n "published decor missing" --glob '*.go' go-fast | head -5`
    );
    assertEqual(rgSearch.kind, "search");
    assertEqual(rgSearch.searchPattern, "published decor missing");
    assertEqual(rgSearch.searchPath, "go-fast");
    assertEqual(rgSearch.summary, `"published decor missing" (go-fast)`);
    assertEqual(rgSearch.isReadOnlyExploration, true);

    const sshCmd = analyzeShellCommand(
      "ssh -o BatchMode=yes -o ConnectTimeout=10 root@8.135.58.57 'cd /opt/gopanel && sudo -u gopanel env'"
    );
    assertEqual(sshCmd.kind, "ssh");
    assertEqual(sshCmd.sshHost, "root@8.135.58.57");
    assertEqual(sshCmd.summary, "root@8.135.58.57 · sudo -u gopanel env");
    assertEqual(sshCmd.isReadOnlyExploration, false);

    // Semantic read/search shell commands prioritize target file/search summary over generic toolSummary,
    // while general shell commands use toolSummary when present.
    const agyRead = part("run_command", {
      input: {
        CommandLine: "sed -n '340,420p' go-fast/app/service/theme_decor_bundle.go",
        toolSummary: "Inspect theme decor bundle",
      },
    });
    assertEqual(summarizeStepTarget(agyRead), "theme_decor_bundle.go:340-420");

    const agyBuild = part("run_command", {
      input: {
        CommandLine: "npm --prefix web run lint && npm --prefix web test",
        toolSummary: "Verify web lint and test",
      },
    });
    assertEqual(summarizeStepTarget(agyBuild), "Verify web lint and test");

    const rawChainNoDesc = part("bash", {
      input: {
        command: "/usr/local/go/bin/go vet ./... && /usr/local/go/bin/go build ./...",
      },
    });
    assertEqual(summarizeStepTarget(rawChainNoDesc), "go vet · go build");

    const npmChainNoDesc = part("bash", {
      input: {
        command: "npm --prefix web run lint && npm --prefix web test && npm --prefix web run build",
      },
    });
    assertEqual(summarizeStepTarget(npmChainNoDesc), "npm run lint · npm test · npm run build");

    // Consecutive completed exploration steps fold into a single group while ssh/running steps stay top-level
    const completedShell = (id: string, command: string): ToolPart =>
      shellPart({
        status: "completed",
        input: { command },
        output: "ok",
        title: command,
        metadata: {},
        time: { start: 1, end: 2 },
      }) && {
        id,
        sessionID: "ses_display",
        messageID: "msg_display",
        callID: id,
        type: "tool",
        tool: "bash",
        state: {
          status: "completed",
          input: { command },
          output: "ok",
          title: command,
          metadata: {},
          time: { start: 1, end: 2 },
        },
      };

    const grouped = groupVisibleActivitySteps(
      [
        completedShell("s1", "ssh -o BatchMode=yes root@8.135.58.57 'cd /opt/gopanel && ls'"),
        completedShell("s2", "cd /Users/hugh/code/hoxi/shopark && sed -n '1,120p' go-fast/app/service/theme_decor.go"),
        completedShell("s3", `cd /Users/hugh/code/hoxi/shopark && rg -n "ErrDecorPublishedMissing" --glob '*.go' go-fast | head -10`),
        completedShell("s4", "cd /Users/hugh/code/hoxi/shopark && sed -n '340,420p' go-fast/app/service/theme_decor_bundle.go"),
      ],
      extractCommand
    );
    assertEqual(grouped.length, 2);
    assertEqual(grouped[0].kind, "part");
    assertEqual(grouped[1].kind, "exploration-fold");
      if (grouped[1].kind === "exploration-fold") {
        assertEqual(grouped[1].parts.length, 3);
      }
    } finally {
      setWorkspace("rootDir", prevRoot);
    }
  });

  it("keeps assistant text explanations on the main timeline while folding contiguous work steps into activity groups", () => {
    assertTrue(isShortTransitionalText("Let me inspect the relevant files first:"));
    assertTrue(isShortTransitionalText("Let me check the configuration first."));
    assertEqual(isShortTransitionalText("### Root Cause\nHere is why it failed:"), false);
    assertEqual(isShortTransitionalText("```ts\nconst x = 1;\n```"), false);

    const sid = "ses_transitional";
    const turns: MessageTurn[] = [
      {
        info: { id: "msg_u", sessionID: sid, role: "user", time: { created: 1 } },
        parts: [{ id: "u1", sessionID: sid, messageID: "msg_u", type: "text", text: "Check bug" }],
      },
      {
        info: { id: "msg_a1", sessionID: sid, role: "assistant", time: { created: 2 } },
        parts: [
          { id: "r1", sessionID: sid, messageID: "msg_a1", type: "reasoning", text: "Thinking 1", time: { start: 1, end: 2 } },
          { id: "p1", sessionID: sid, messageID: "msg_a1", type: "text", text: "Let me read the first file:" },
          {
            id: "t1",
            sessionID: sid,
            messageID: "msg_a1",
            type: "tool",
            callID: "t1",
            tool: "read",
            state: { status: "completed", input: { filePath: "a.go" }, output: "ok", title: "a.go", metadata: {}, time: { start: 2, end: 3 } },
          },
        ],
      },
      {
        info: { id: "msg_a2", sessionID: sid, role: "assistant", time: { created: 3 } },
        parts: [
          { id: "r2", sessionID: sid, messageID: "msg_a2", type: "reasoning", text: "Thinking 2", time: { start: 3, end: 4 } },
          { id: "p2", sessionID: sid, messageID: "msg_a2", type: "text", text: "Now let me check the second file:" },
          {
            id: "t2",
            sessionID: sid,
            messageID: "msg_a2",
            type: "tool",
            callID: "t2",
            tool: "read",
            state: { status: "completed", input: { filePath: "b.go" }, output: "ok", title: "b.go", metadata: {}, time: { start: 4, end: 5 } },
          },
        ],
      },
      {
        info: { id: "msg_a3", sessionID: sid, role: "assistant", time: { created: 4, completed: 5 } },
        parts: [
          { id: "r3", sessionID: sid, messageID: "msg_a3", type: "reasoning", text: "Final thought", time: { start: 5, end: 6 } },
          { id: "final", sessionID: sid, messageID: "msg_a3", type: "text", text: "Root cause identified and fixed." },
        ],
      },
    ];

    const m1 = resolveMergedTurnParts(turns, 1);
    const m2 = resolveMergedTurnParts(turns, 2);
    const m3 = resolveMergedTurnParts(turns, 3);

    assertTrue(!m1.isVisuallyEmpty);
    assertTrue(m2.isVisuallyEmpty, "intermediate turn with only transitional text + tool folds into anchor");
    assertTrue(!m3.isVisuallyEmpty);
    assertDeepEqual(m3.effectiveParts.map((p: MessagePart) => p.id), ["final"]);

    const grouped1 = groupAssistantParts(m1.effectiveParts);
    assertEqual(grouped1.length, 1, "all steps and transitional preambles stay in one unified activity group");
    assertEqual(grouped1[0].kind, "activity-group");
  });

  it("strips Windows ConPTY/PowerShell OSC sequences, UTF-8 BOM, and resolves carriage returns", () => {
    const cjkSuffix = String.fromCodePoint(0x914d, 0x7f6e);
    const raw =
      `\uFEFF\u001b]0;Administrator: Windows PowerShell\u0007\u001b]9;4;1;50\u001b\\\u001b[38:2:0:255:128mDownloading 10%\rDownloading 100%\u001b[0m\r\nWindows IP ${cjkSuffix}\r\n`;
    assertEqual(processCarriageReturns(raw), `Downloading 100%\nWindows IP ${cjkSuffix}\n`);
  });

  it("repairs Windows CP936/GBK and Latin-1 Mojibake in historical and live terminal outputs and commands", () => {
    const expectedComment = "// \u4e2d\u6587\u6ce8\u91ca: \u6d4b\u8bd5\u6210\u529f\nconst ok = true;";
    const mojibakeComment = new TextDecoder("gb18030").decode(new TextEncoder().encode(expectedComment));
    const mixedComment = "// \u6d93\ue15f\u6783\u6ce8\u91ca: \u5a34\u5b2d\u762f\u6210\u529f\nconst ok = true;";
    assertEqual(repairWindowsMojibake(mojibakeComment), expectedComment);
    assertEqual(repairWindowsMojibake(mixedComment), expectedComment);
    assertEqual(processCarriageReturns(mojibakeComment), expectedComment);

    // Odd-byte CJK + CP936 PUA overrides ("\u4e00" -> "\u6d93\u20ac", "\u5f00" -> "\u5bee\u20ac") and box-drawing prefix
    assertEqual(repairWindowsMojibake("\u251c\u2500\u2500\u6d93\ue15f\u6783"), "\u251c\u2500\u2500\u4e2d\u6587");
    assertEqual(repairWindowsMojibake("\u6d93\u20ac\u4e2a\u5bee\u20ac\u59cb"), "\u4e00\u4e2a\u5f00\u59cb");

    // UTF-8 decoded as Latin-1/CP1252 ("\u4e2d\u6587" -> "\u00e4\u00b8\u00ad\u00e6\u2013\u2021") is repaired while valid Western Unicode stays untouched
    assertEqual(repairWindowsMojibake("output: \u00e4\u00b8\u00ad\u00e6\u2013\u2021"), "output: \u4e2d\u6587");
    assertEqual(repairWindowsMojibake("r\u00e9\u00e9crire"), "r\u00e9\u00e9crire");
    assertEqual(repairWindowsMojibake("cr\u00e9\u00e9e"), "cr\u00e9\u00e9e");
    assertEqual(repairWindowsMojibake("\u00e4\u00e4"), "\u00e4\u00e4");
    assertEqual(processCarriageReturns("r\u00e9\u00e9crire cr\u00e9\u00e9e \u00e4\u00e4"), "r\u00e9\u00e9crire cr\u00e9\u00e9e \u00e4\u00e4");

    // Normal Chinese text and code must remain untouched
    const normalChinese = "// \u6b63\u5e38\u4e2d\u6587\u8f93\u51fa\u4e0e\u9519\u8bef\u63d0\u793a: \u6587\u4ef6\u4e0d\u5b58\u5728";
    assertEqual(repairWindowsMojibake(normalChinese), normalChinese);
    const repeatedChinese = "\u4e2d".repeat(3000);
    assertEqual(repairWindowsMojibake(repeatedChinese), repeatedChinese);

    // Persisted tool card state with empty metadata.output falls back to state.output and repairs command + output
    const part = shellPart({
      status: "completed",
      input: { command: "echo \u6d93\ue15f\u6783" },
      output: mojibakeComment,
      title: "\u6d93\ue15f\u6783",
      metadata: { output: "" },
      time: { start: 1, end: 2 },
    });
    assertEqual(extractCommand(part), "echo \u4e2d\u6587");
    assertEqual(extractDescription(part), "\u4e2d\u6587");
    assertEqual(extractShellOutput(part.state), expectedComment);
  });
});
