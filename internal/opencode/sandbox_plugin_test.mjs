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
  t.after(() => {
    if (previous === undefined) delete process.env.HOXI_SANDBOX_ROOT;
    else process.env.HOXI_SANDBOX_ROOT = previous;
  });

  await t.test("wraps bash in the generated profile", async () => {
    const { workspace, profilePath } = fixture("deny");
    const hooks = await HoxiSandboxPlugin();
    const output = { args: { command: "echo 'hi' > out.txt" } };
    await hooks["tool.execute.before"]({ tool: "bash", sessionID: "ses_sandbox", callID: "c1" }, output);
    assert.ok(output.args.command.startsWith("'/usr/bin/sandbox-exec' -f "));
    assert.ok(output.args.command.includes(profilePath));
    assert.ok(output.args.command.includes("/bin/sh -c 'echo '\\''hi'\\'' > out.txt'"));
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
  });

  await t.test("allows network tools when network mode is allow", async () => {
    fixture("allow");
    const hooks = await HoxiSandboxPlugin();
    await hooks["tool.execute.before"](
      { tool: "webfetch", sessionID: "ses_sandbox", callID: "c7" },
      { args: { url: "https://hoxi.ai" } }
    );
  });

  await t.test("leaves sessions without a snapshot untouched", async () => {
    fixture("deny");
    const hooks = await HoxiSandboxPlugin();
    const output = { args: { command: "echo hi" } };
    await hooks["tool.execute.before"]({ tool: "bash", sessionID: "ses_other", callID: "c8" }, output);
    assert.equal(output.args.command, "echo hi");
  });
});
