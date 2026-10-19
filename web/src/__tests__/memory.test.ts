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
  });

  it("freezes system prompt memory per session across turns and skips redundant HTTP fetches when ledger has evidence", async () => {
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
      const sessionsDir = path.join(tmpDir, ".opencode", "memory", "sessions");
      await fs.mkdir(sessionsDir, { recursive: true });
      await fs.writeFile(
        path.join(sessionsDir, "ses_resumed.md"),
        "# Session Memory (ses_resumed)\nUpdated: 2026-10-18T01:00:00.000Z\n\n## 1. Goals\n- Keep KV prefix stable\n",
        "utf8"
      );

      const plugin = await HoxiMemoryPlugin({ client: mockClient, directory: tmpDir });
      const turn1Out = { system: [] as string[] };
      await plugin["experimental.chat.system.transform"]({ sessionID: "ses_resumed" }, turn1Out);
      assertTrue(turn1Out.system.some((b) => b.includes("Keep KV prefix stable")));
      assertTrue(!turn1Out.system.some((b) => b.includes("Updated:")));

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
    } finally {
      await fs.rm(tmpDir, { recursive: true, force: true });
    }
  });
});
