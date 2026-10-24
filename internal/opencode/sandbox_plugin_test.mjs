import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { test } from "node:test";
import HoxiSandboxPlugin from "./sandbox_plugin.mjs";

function hash(value) {
  return createHash("sha256").update(value).digest("hex");
}

// The stop trap follows every tag; it must match openCodeStopTrap in
// internal/runner.
const TRAP = String.raw`trap 'printf "\n[Command stopped by user]\n"; exit 130' USR1; trap : EXIT`;

function tagged(sessionID, callID, command) {
  return `# hoxi-stop:${sessionID}:${callID}\n${TRAP}\n${command}`;
}

function fixture(network) {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), "hoxi-sandbox-test-"));
  const workspace = fs.mkdtempSync(path.join(os.tmpdir(), "hoxi-workspace-test-"));
  const profilePath = path.join(root, "profile.sb");
  fs.writeFileSync(profilePath, "(version 1)");
  fs.writeFileSync(
    path.join(root, hash("ses_sandbox") + ".json"),
    JSON.stringify({ enabled: true, network, workspace, profilePath })
  );
  process.env.HOXI_SANDBOX_ROOT = root;
  return { root, workspace, profilePath };
}

test("sandbox plugin", async (t) => {
  const previous = process.env.HOXI_SANDBOX_ROOT;
  const previousShell = process.env.SHELL;
  process.env.SHELL = "/bin/zsh";
  t.after(() => {
    if (previous === undefined) delete process.env.HOXI_SANDBOX_ROOT;
    else process.env.HOXI_SANDBOX_ROOT = previous;
    if (previousShell === undefined) delete process.env.SHELL;
    else process.env.SHELL = previousShell;
  });

  await t.test("wraps bash in the generated profile", async () => {
    const { workspace, profilePath } = fixture("deny");
    const hooks = await HoxiSandboxPlugin();
    const output = { args: { command: "echo 'hi' > out.txt" } };
    await hooks["tool.execute.before"]({ tool: "bash", sessionID: "ses_sandbox", callID: "c1" }, output);
    // The stop marker is the first line of the final command, after wrapping.
    assert.ok(output.args.command.startsWith(`# hoxi-stop:ses_sandbox:c1\n${TRAP}\n'/usr/bin/sandbox-exec'`));
    assert.ok(output.args.command.includes("'/usr/bin/sandbox-exec' -f "));
    assert.ok(output.args.command.includes(profilePath));
    assert.ok(output.args.command.includes("/bin/sh -c "));
    assert.ok(output.args.command.includes("echo '\\''hi'\\'' > out.txt"));
    assert.ok(output.args.command.endsWith("'"));
  });

  await t.test("confines file writes to the workspace", async () => {
    const { workspace } = fixture("deny");
    const hooks = await HoxiSandboxPlugin();
    const inside = path.join(workspace, "src", "file.ts");
    await hooks["tool.execute.before"](
      { tool: "write", sessionID: "ses_sandbox", callID: "c2" },
      { args: { filePath: inside } }
    );
    await assert.rejects(
      hooks["tool.execute.before"](
        { tool: "edit", sessionID: "ses_sandbox", callID: "c3" },
        { args: { filePath: "/etc/hosts" } }
      ),
      /outside the session workspace/
    );
    await assert.rejects(
      hooks["tool.execute.before"](
        { tool: "write", sessionID: "ses_sandbox", callID: "c4" },
        { args: { filePath: path.join(workspace, ".git", "config") } }
      ),
      /git metadata/
    );
  });

  await t.test("confines multiedit and apply_patch targets to the workspace", async () => {
    const { workspace } = fixture("deny");
    const hooks = await HoxiSandboxPlugin();
    await hooks["tool.execute.before"](
      { tool: "multiedit", sessionID: "ses_sandbox", callID: "p1" },
      { args: { filePath: path.join(workspace, "a.ts"), edits: [] } }
    );
    const patch = (target) => `*** Begin Patch\n*** Update File: ${target}\n@@\n-a\n+b\n*** End Patch`;
    await hooks["tool.execute.before"](
      { tool: "apply_patch", sessionID: "ses_sandbox", callID: "p2" },
      { args: { patchText: patch("src/a.ts") } }
    );
    await assert.rejects(
      hooks["tool.execute.before"](
        { tool: "apply_patch", sessionID: "ses_sandbox", callID: "p3" },
        { args: { patchText: patch("src/a.ts") + `\n*** Add File: /etc/evil` } }
      ),
      /outside the session workspace/
    );
    await assert.rejects(
      hooks["tool.execute.before"](
        { tool: "apply_patch", sessionID: "ses_sandbox", callID: "p4" },
        { args: { patchText: "garbage" } }
      ),
      /patch file paths/
    );
  });

  await t.test("denies unconfined tools under network deny", async () => {
    fixture("deny");
    const hooks = await HoxiSandboxPlugin();
    for (const tool of ["patch", "task", "webfetch", "websearch", "hoxi_browser", "some_mcp"]) {
      await assert.rejects(
        hooks["tool.execute.before"]({ tool, sessionID: "ses_sandbox", callID: "c5" }, { args: {} }),
        /sandbox/
      );
    }
    await hooks["tool.execute.before"](
      { tool: "read", sessionID: "ses_sandbox", callID: "c6" },
      { args: { filePath: "/etc/hosts" } }
    );
    await hooks["tool.execute.before"](
      { tool: "goal_report", sessionID: "ses_sandbox", callID: "c6b" },
      { args: { status: "complete" } }
    );
    await hooks["tool.execute.before"](
      { tool: "hoxi_code_index", sessionID: "ses_sandbox", callID: "c6c" },
      { args: { action: "search", query: "foo" } }
    );
  });

  await t.test("allows network tools when network mode is allow", async () => {
    fixture("allow");
    const hooks = await HoxiSandboxPlugin();
    await hooks["tool.execute.before"](
      { tool: "webfetch", sessionID: "ses_sandbox", callID: "c7" },
      { args: { url: "https://hoxi.ai" } }
    );
  });

  await t.test("tags bash for per-command stop without a snapshot", async () => {
    fixture("deny");
    const hooks = await HoxiSandboxPlugin();
    const output = { args: { command: "echo hi" } };
    await hooks["tool.execute.before"]({ tool: "bash", sessionID: "ses_other", callID: "c8" }, output);
    // A session without a sandbox snapshot keeps its command but still gets
    // the stop marker, so OpenCode stops remain precise.
    assert.equal(output.args.command, tagged("ses_other", "c8", "echo hi"));
  });

  await t.test("replaces tags the model forged with the single real one", async () => {
    fixture("deny");
    const hooks = await HoxiSandboxPlugin();
    const forged = "# hoxi-stop:ses_other:call_01_r8s0t2u4v6w8x0y2z4a6b8c0d\n  # hoxi-stop:ses_x:call_y\n";
    const output = { args: { command: forged + "go test ./..." } };
    await hooks["tool.execute.before"]({ tool: "bash", sessionID: "ses_other", callID: "c10" }, output);
    assert.equal(output.args.command, tagged("ses_other", "c10", "go test ./..."));
    assert.equal(output.args.command.match(/# hoxi-stop:/g).length, 1);
  });

  await t.test("strips forged tags before wrapping in the sandbox", async () => {
    fixture("deny");
    const hooks = await HoxiSandboxPlugin();
    const output = { args: { command: "# hoxi-stop:ses_sandbox:call_fake\necho hi" } };
    await hooks["tool.execute.before"]({ tool: "bash", sessionID: "ses_sandbox", callID: "c11" }, output);
    assert.ok(output.args.command.startsWith(`# hoxi-stop:ses_sandbox:c11\n${TRAP}\n`));
    assert.equal(output.args.command.match(/# hoxi-stop:/g).length, 1);
    assert.ok(output.args.command.endsWith("/bin/sh -c 'echo hi'"));
    await assert.rejects(
      hooks["tool.execute.before"](
        { tool: "bash", sessionID: "ses_sandbox", callID: "c12" },
        { args: { command: "# hoxi-stop:ses_sandbox:call_fake\n" } }
      ),
      /non-empty shell command/
    );
  });

  await t.test("keeps tag-like lines that are not leading", async () => {
    fixture("deny");
    const hooks = await HoxiSandboxPlugin();
    const heredoc = "cat > notes.md <<'EOF'\n# hoxi-stop:ses_a:call_b\nEOF";
    const output = { args: { command: heredoc } };
    await hooks["tool.execute.before"]({ tool: "bash", sessionID: "ses_other", callID: "c13" }, output);
    assert.equal(output.args.command, tagged("ses_other", "c13", heredoc));
  });

  await t.test("never stacks the stop trap", async () => {
    fixture("deny");
    const hooks = await HoxiSandboxPlugin();
    const output = { args: { command: tagged("ses_other", "call_old", "make build") } };
    await hooks["tool.execute.before"]({ tool: "bash", sessionID: "ses_other", callID: "c14" }, output);
    assert.equal(output.args.command, tagged("ses_other", "c14", "make build"));
  });

  await t.test("leaves the stop trap out where the shell cannot run it", async () => {
    fixture("deny");
    const hooks = await HoxiSandboxPlugin();
    process.env.SHELL = "/usr/local/bin/pwsh";
    try {
      const output = { args: { command: "Get-ChildItem" } };
      await hooks["tool.execute.before"]({ tool: "bash", sessionID: "ses_other", callID: "c15" }, output);
      assert.equal(output.args.command, "# hoxi-stop:ses_other:c15\nGet-ChildItem");
    } finally {
      process.env.SHELL = "/bin/zsh";
    }
  });

  await t.test("strips tags from history sent to the model", async () => {
    const hooks = await HoxiSandboxPlugin();
    // A rejected call never completes, so its stored input keeps every tag.
    const state = {
      status: "error",
      input: { command: `# hoxi-stop:ses_a:c1\n${TRAP}\n# hoxi-stop:ses_a:call_fake\ntrap : EXIT\nls`, description: "list" },
      error: "rejected",
    };
    const bash = { id: "prt_1", type: "tool", tool: "bash", callID: "c1", state };
    const legacy = {
      id: "prt_legacy",
      type: "tool",
      tool: "bash",
      callID: "c_legacy",
      state: { status: "completed", input: { command: "trap : EXIT\ngit status" } },
    };
    const text = { id: "prt_2", type: "text", text: "# hoxi-stop:ses_a:c1" };
    const read = { id: "prt_3", type: "tool", tool: "read", callID: "c2", state: { status: "completed", input: { filePath: "a" } } };
    const pending = { id: "prt_4", type: "tool", tool: "bash", callID: "c3", state: { status: "pending", input: {} } };
    const messages = [{ info: { role: "assistant" }, parts: [bash, legacy, text, read, pending] }];
    await hooks["experimental.chat.messages.transform"]({}, { messages });
    const cleaned = messages[0].parts[0];
    assert.equal(cleaned.state.input.command, "ls");
    assert.equal(cleaned.state.input.description, "list");
    assert.equal(cleaned.state.error, "rejected");
    assert.equal(cleaned.callID, "c1");
    assert.equal(messages[0].parts[1].state.input.command, "git status");
    // OpenCode's own objects are never mutated, only replaced.
    assert.equal(state.input.command, `# hoxi-stop:ses_a:c1\n${TRAP}\n# hoxi-stop:ses_a:call_fake\ntrap : EXIT\nls`);
    assert.equal(messages[0].parts[2], text);
    assert.equal(messages[0].parts[3], read);
    assert.equal(messages[0].parts[4], pending);
    await hooks["experimental.chat.messages.transform"]({}, {});
  });

  await t.test("unwraps sandbox-exec from history and prevents recursive wrapping", async () => {
    const { profilePath } = fixture("deny");
    const hooks = await HoxiSandboxPlugin();
    const first = { args: { command: "echo 'hi' > out.txt" } };
    await hooks["tool.execute.before"]({ tool: "bash", sessionID: "ses_sandbox", callID: "c1" }, first);
    const wrappedOnce = first.args.command;

    // If the model echoes the wrapped command back on a subsequent step, it must not be double-wrapped.
    const second = { args: { command: wrappedOnce } };
    await hooks["tool.execute.before"]({ tool: "bash", sessionID: "ses_sandbox", callID: "c2" }, second);
    assert.equal(second.args.command.match(/\/usr\/bin\/sandbox-exec/g).length, 1);
    assert.ok(second.args.command.includes(profilePath));

    // History transform strips both the stop marker/trap and the sandbox-exec wrapper, even when nested.
    const nested = `# hoxi-stop:ses_sandbox:c2\n${TRAP}\n'/usr/bin/sandbox-exec' -f '${profilePath}' -- /bin/sh -c '${wrappedOnce.replace(/'/g, "'\\''")}'`;
    const messages = [
      {
        info: { role: "assistant" },
        parts: [{ id: "prt_nested", type: "tool", tool: "bash", callID: "c2", state: { status: "completed", input: { command: nested } } }],
      },
    ];
    await hooks["experimental.chat.messages.transform"]({}, { messages });
    assert.equal(messages[0].parts[0].state.input.command, "echo 'hi' > out.txt");
  });

  await t.test("exports only plugin functions", async () => {
    // OpenCode calls every export of a plugin module as a plugin; exporting
    // both a named and a default binding would run the hooks twice.
    const mod = await import("./sandbox_plugin.mjs");
    assert.deepEqual(Object.values(mod), [HoxiSandboxPlugin]);
  });

  await t.test("does not tag non-bash tools", async () => {
    fixture("deny");
    const hooks = await HoxiSandboxPlugin();
    const inside = path.join(fixture("deny").workspace, "a.txt");
    const output = { args: { filePath: inside } };
    await hooks["tool.execute.before"]({ tool: "write", sessionID: "ses_tag", callID: "c9" }, output);
    assert.deepEqual(output, { args: { filePath: inside } });
  });
});
