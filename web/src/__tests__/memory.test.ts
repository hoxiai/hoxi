import {
  EIGHT_DIM_COMPACTION_PROMPT,
  createFrozenProjectMemoryResolver,
  projectMemoryRoot,
  readProjectMemoryBlock,
  redactMemorySecrets,
  stripVolatileMemoryHeader,
} from "../../../internal/opencode/memory_compaction.js";
import { assertDeepEqual, assertEqual, assertTrue, describe, it } from "./test-harness";

describe("8-dim session memory compaction & project memory resolution", () => {
  it("defines the 8-dimension compaction headings and redacts API keys, tokens, and PEM blocks", () => {
    for (const heading of [
      "## Goals",
      "## User constraints",
      "## Key facts",
      "## Changed files and reasons",
      "## Errors and fixes",
      "## Todo",
      "## Current progress",
      "## Next steps",
    ]) {
      assertTrue(EIGHT_DIM_COMPACTION_PROMPT.includes(heading), `missing heading ${heading}`);
    }

    const scrubbed = redactMemorySecrets(
      "Keys: sk-1234567890abcdefghijklmno, sk-ant-1234567890abcdefghijklmno, ghp_1234567890abcdefghijklmno, Bearer abcdefghijklmnopqrstuvwxyz1234",
    );
    assertEqual(scrubbed.includes("sk-1234567890"), false);
    assertEqual(scrubbed.includes("ghp_1234567890"), false);
    assertEqual(scrubbed.includes("abcdefghijklmnopqrstuvwxyz1234"), false);
    assertTrue(scrubbed.includes("[REDACTED_SECRET]"));
    assertEqual(
      stripVolatileMemoryHeader("Updated: 2026-10-18T00:00:00Z\n# Project\n- Rule 1\n"),
      "# Project\n- Rule 1",
    );
  });

  it("maps isolated .hoxi/worktrees/<id> directories back to the project root", () => {
    assertEqual(projectMemoryRoot("/repo/.hoxi/worktrees/ses_123"), "/repo");
    assertEqual(projectMemoryRoot("/repo/.hoxi/worktrees/ses_123/"), "/repo");
    assertEqual(projectMemoryRoot("/repo/.hoxi/worktrees/ses_123/subdir"), "/repo/.hoxi/worktrees/ses_123/subdir");
    assertEqual(projectMemoryRoot("/repo"), "/repo");
  });

  it("prefers .hoxi/memory/project.md, falls back to legacy .opencode/memory/project.md, and freezes per session", async () => {
    const fs = await (Function('return import("node:fs/promises")')() as Promise<any>);
    const os = await (Function('return import("node:os")')() as Promise<any>);
    const path = await (Function('return import("node:path")')() as Promise<any>);
    const tmpDir = await fs.mkdtemp(path.join(os.tmpdir(), "hoxi-mem-cache-"));
    try {
      const legacyMemoryDir = path.join(tmpDir, ".opencode", "memory");
      await fs.mkdir(legacyMemoryDir, { recursive: true });
      await fs.writeFile(
        path.join(legacyMemoryDir, "project.md"),
        "# Project\nUpdated: 2026-10-18T00:00:00.000Z\n- Keep KV prefix stable\n",
        "utf8",
      );

      const resolveFrozen = createFrozenProjectMemoryResolver(tmpDir);
      const worktreeDir = path.join(tmpDir, ".hoxi", "worktrees", "ses_resumed");
      const turn1 = await resolveFrozen("ses_resumed", worktreeDir);
      assertTrue(turn1.includes("<project_memory>") && turn1.includes("Keep KV prefix stable"));
      assertEqual(turn1.includes("Updated:"), false);

      // Creating .hoxi/memory/project.md mid-session overrides new sessions while keeping ses_resumed frozen.
      const hoxiMemoryDir = path.join(tmpDir, ".hoxi", "memory");
      await fs.mkdir(hoxiMemoryDir, { recursive: true });
      await fs.writeFile(
        path.join(hoxiMemoryDir, "project.md"),
        "Updated: 2026-11-17T00:00:00.000Z\n# Project\n- Native .hoxi rule\n",
        "utf8",
      );

      const turn2 = await resolveFrozen("ses_resumed", worktreeDir);
      assertDeepEqual(turn2, turn1);

      const freshSession = await resolveFrozen("ses_fresh", worktreeDir);
      assertTrue(freshSession.includes("Native .hoxi rule"));
      assertEqual(freshSession.includes("Keep KV prefix stable"), false);
      assertEqual(await readProjectMemoryBlock(""), "");
    } finally {
      await fs.rm(tmpDir, { recursive: true, force: true });
    }
  });
});
