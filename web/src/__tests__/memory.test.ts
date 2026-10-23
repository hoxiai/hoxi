import * as memoryPluginModule from "../../../.opencode/plugins/hoxi-memory.js";
import HoxiMemoryPlugin from "../../../.opencode/plugins/hoxi-memory.js";
import { assertDeepEqual, assertEqual, assertTrue, describe, it } from "./test-harness";

const {
  createSessionLedger,
  ingestMessages,
  ledgerHasEvidence,
  noteDiffs,
  noteTodos,
  renderEightDimMemory,
} = HoxiMemoryPlugin as typeof HoxiMemoryPlugin & Record<string, any>;

describe("8-dim session memory", () => {
  it("exports only the single OpenCode plugin entrypoint", () => {
    const uniqueExports = new Set(Object.values(memoryPluginModule));
    assertEqual(uniqueExports.size, 1);
    assertEqual(uniqueExports.has(HoxiMemoryPlugin), true);
  });

  it("writes observed goals, edits, errors, and todos instead of empty headings", () => {
    const ledger = createSessionLedger();
    assertEqual(ledgerHasEvidence(ledger), false);
    ingestMessages(ledger, [
      {
        info: { id: "msg_user", role: "user" },
        parts: [{ type: "text", messageID: "msg_user", text: "Fix the apply path. Never swallow git errors." }],
      },
      {
        info: { id: "msg_assistant", role: "assistant" },
        parts: [
          {
            type: "tool",
            tool: "edit",
            state: { status: "completed", input: { filePath: "internal/worktree/manager.go" } },
          },
          {
            type: "tool",
            tool: "bash",
            state: { status: "error", input: { command: "go test" }, error: "snapshot commit failed" },
          },
          { type: "text", messageID: "msg_assistant", text: "Apply now stops when the snapshot commit fails." },
        ],
      },
    ]);
    noteDiffs(ledger, [{ file: "internal/worktree/manager.go", additions: 4, deletions: 1 }]);
    noteTodos(ledger, [{ content: "Cover locked worktree removal", status: "pending" }]);

    const markdown = renderEightDimMemory("ses_test", ledger, "2026-10-18T00:00:00.000Z");
    assertTrue(ledgerHasEvidence(ledger));
    assertTrue(markdown.includes("Fix the apply path"));
    assertTrue(markdown.includes("Never swallow git errors"));
    assertTrue(markdown.includes("internal/worktree/manager.go"));
    assertTrue(markdown.includes("snapshot commit failed"));
    assertTrue(markdown.includes("Cover locked worktree removal"));
    assertTrue(markdown.includes("Apply now stops when the snapshot commit fails."));
    assertEqual(markdown.includes("## 1. Goals\n## 2. Constraints"), false);
    const constraints = markdown.split("## 2. Constraints")[1].split("## 3.")[0];
    assertTrue(constraints.includes("- Never swallow git errors."));
    assertEqual(constraints.includes("Fix the apply path"), false);
  });

  it("does not duplicate a single-sentence user turn into Constraints", () => {
    const ledger = createSessionLedger();
    // "\u4e0d\u8981" = "do not" (zh) and "\u3002" = full-width period; escaped to satisfy check:i18n.
    ingestMessages(ledger, [
      {
        info: { id: "msg_user", role: "user" },
        parts: [{ type: "text", messageID: "msg_user", text: "\u4e0d\u8981 create the .opencode directory" }],
      },
      {
        info: { id: "msg_user2", role: "user" },
        parts: [{ type: "text", messageID: "msg_user2", text: "Update the plugin\u3002\u4e0d\u8981 touch AGENTS.md" }],
      },
    ]);
    assertDeepEqual(ledger.constraints, ["\u4e0d\u8981 touch AGENTS.md"]);
    assertEqual(ledger.goals.length, 2);
  });

  it("freezes project memory per session, never re-injects the session's own ledger, and skips redundant HTTP fetches", async () => {
    const fs = await (Function('return import("node:fs/promises")')() as Promise<any>);
    const os = await (Function('return import("node:os")')() as Promise<any>);
    const path = await (Function('return import("node:path")')() as Promise<any>);
    const tmpDir = await fs.mkdtemp(path.join(os.tmpdir(), "hoxi-mem-cache-"));
    let fetchCalls = 0;
    const mockClient = {
      session: {
        messages: async () => { fetchCalls++; return []; },
        todo: async () => { fetchCalls++; return []; },
        diff: async () => { fetchCalls++; return []; },
      },
    };
    try {
      // Pre-migration memory lives under .opencode/memory and must still be read.
      const legacyMemoryDir = path.join(tmpDir, ".opencode", "memory");
      const legacySessionsDir = path.join(legacyMemoryDir, "sessions");
      await fs.mkdir(legacySessionsDir, { recursive: true });
      await fs.writeFile(
        path.join(legacyMemoryDir, "project.md"),
        "# Project\nUpdated: 2026-10-18T00:00:00.000Z\n- Keep KV prefix stable\n",
        "utf8"
      );
      await fs.writeFile(
        path.join(legacySessionsDir, "ses_resumed.md"),
        "# Session Memory (ses_resumed)\nUpdated: 2026-10-18T01:00:00.000Z\n\n## 1. Goals\n- Old session ledger\n",
        "utf8"
      );

      const plugin = await HoxiMemoryPlugin({ client: mockClient, directory: tmpDir });
      const turn1Out = { system: [] as string[] };
      await plugin["experimental.chat.system.transform"]({ sessionID: "ses_resumed" }, turn1Out);
      assertTrue(turn1Out.system.some((b) => b.includes("<project_memory>") && b.includes("Keep KV prefix stable")));
      assertTrue(!turn1Out.system.some((b) => b.includes("Updated:")));
      assertTrue(!turn1Out.system.some((b) => b.includes("session_handoff_memory") || b.includes("Old session ledger")));

      await fs.writeFile(path.join(legacyMemoryDir, "project.md"), "# Project\n- Edited mid-session\n", "utf8");

      await plugin.event({
        event: {
          type: "message.updated",
          properties: { info: { id: "msg_u1", sessionID: "ses_resumed", role: "user" } },
        },
      });
      await plugin.event({
        event: {
          type: "message.part.updated",
          properties: {
            part: { id: "prt_1", sessionID: "ses_resumed", messageID: "msg_u1", type: "text", text: "New turn goal" },
          },
        },
      });
      await plugin.event({
        event: { type: "session.idle", properties: { sessionID: "ses_resumed" } },
      });
      assertEqual(fetchCalls, 0);

      const turn2Out = { system: [] as string[] };
      await plugin["experimental.chat.system.transform"]({ sessionID: "ses_resumed" }, turn2Out);
      assertDeepEqual(turn2Out.system, turn1Out.system);

      // New writes go to .hoxi/memory so OpenCode never sees a project .opencode
      // config root it would populate with a plugin bundle.
      const written = await fs.readFile(path.join(tmpDir, ".hoxi", "memory", "sessions", "ses_resumed.md"), "utf8");
      assertTrue(written.includes("New turn goal"));
    } finally {
      await fs.rm(tmpDir, { recursive: true, force: true });
    }
  });

  it("injects the commit attribution rule only while the desktop setting allows it", async () => {
    const fs = await (Function('return import("node:fs/promises")')() as Promise<any>);
    const os = await (Function('return import("node:os")')() as Promise<any>);
    const path = await (Function('return import("node:path")')() as Promise<any>);
    const env = (globalThis as any).process.env as Record<string, string | undefined>;
    const previousHome = env.HOXI_HOME;
    const hoxiHome = await fs.mkdtemp(path.join(os.tmpdir(), "hoxi-attr-home-"));
    const project = await fs.mkdtemp(path.join(os.tmpdir(), "hoxi-attr-proj-"));
    const systemFor = async (sessionID: string) => {
      const plugin = await HoxiMemoryPlugin({ directory: project });
      const out = { system: [] as string[] };
      await plugin["experimental.chat.system.transform"]({ sessionID }, out);
      return out.system.join("\n");
    };
    try {
      env.HOXI_HOME = hoxiHome;
      assertTrue((await systemFor("ses_default")).includes("Co-authored-by: Hoxi"), "missing setting keeps attribution on");
      await fs.writeFile(path.join(hoxiHome, "git-attribution.json"), '{"enabled":false}\n', "utf8");
      assertEqual((await systemFor("ses_off")).includes("hoxi_git_attribution"), false);
      await fs.writeFile(path.join(hoxiHome, "git-attribution.json"), '{"mode":"never"}\n', "utf8");
      assertEqual((await systemFor("ses_never")).includes("hoxi_git_attribution"), false);
      await fs.writeFile(path.join(hoxiHome, "git-attribution.json"), '{"mode":"auto"}\n', "utf8");
      assertTrue((await systemFor("ses_auto")).includes("Co-authored-by: Hoxi"));
      await fs.writeFile(path.join(hoxiHome, "git-attribution.json"), '{"mode":"always"}\n', "utf8");
      assertTrue((await systemFor("ses_always")).includes("Co-authored-by: Hoxi"));
    } finally {
      if (previousHome === undefined) delete env.HOXI_HOME;
      else env.HOXI_HOME = previousHome;
      await fs.rm(hoxiHome, { recursive: true, force: true });
      await fs.rm(project, { recursive: true, force: true });
    }
  });
});
