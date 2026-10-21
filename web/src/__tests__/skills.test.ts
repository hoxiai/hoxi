// web/src/__tests__/skills.test.ts
import { assertEqual, assertTrue, describe, it } from "./test-harness";
import {
  extractUserTurnSkillSelection,
  getComposerSkill,
  installSkillFromMarketplace,
  loadSkillMarketplace,
  resolveTurnSkillStatus,
  selectComposerSkill,
  skillsStore,
  transferDraftComposerSkill,
} from "../store/skills";
import { buildSlashCommandSuggestions } from "../components/timeline/composer-autocomplete";
import { extractDescription } from "../components/timeline/tool-card-helpers";
import type { MessagePart } from "../services/sse-types";
import type { SkillDescriptor } from "../services/api";

describe("skills capability", () => {
  it("extracts user turn skill selection and resolves authentic skill tool cycle status", () => {
    const userTurn = {
      info: { id: "msg_u1", sessionID: "ses_1", role: "user" as const, time: { created: 100 } },
      parts: [
        {
          id: "prt_u1",
          sessionID: "ses_1",
          messageID: "msg_u1",
          type: "text" as const,
          text: "Please review this module",
          metadata: {
            hoxiSkill: { key: "skl_abc123", name: "code-review" },
          },
        },
      ],
    };

    const extracted = extractUserTurnSkillSelection(userTurn.parts);
    assertEqual(extracted?.key, "skl_abc123");
    assertEqual(extracted?.name, "code-review");

    // 1. Requested (no skill tool call yet, session still busy)
    const requested = resolveTurnSkillStatus([userTurn], 0, true);
    assertEqual(requested?.status, "requested");

    // 2. Loading (skill tool call running)
    const runningTurn = {
      info: { id: "msg_a1", sessionID: "ses_1", role: "assistant" as const, time: { created: 101 } },
      parts: [
        {
          id: "prt_t1",
          sessionID: "ses_1",
          messageID: "msg_a1",
          type: "tool" as const,
          callID: "call_1",
          tool: "skill",
          state: {
            status: "running" as const,
            input: { name: "code-review" },
            time: { start: 101 },
          },
        },
      ],
    };
    const loading = resolveTurnSkillStatus([userTurn, runningTurn], 0, true);
    assertEqual(loading?.status, "loading");

    // 3. Loaded (skill tool call completed)
    const completedTurn = {
      info: {
        id: "msg_a1",
        sessionID: "ses_1",
        role: "assistant" as const,
        time: { created: 101, completed: 105 },
      },
      parts: [
        {
          id: "prt_t1",
          sessionID: "ses_1",
          messageID: "msg_a1",
          type: "tool" as const,
          callID: "call_1",
          tool: "skill",
          state: {
            status: "completed" as const,
            input: { name: "code-review" },
            output: "<skill_content name=\"code-review\">...</skill_content>",
            title: "Loaded skill: code-review",
            metadata: { name: "code-review" },
            time: { start: 101, end: 103 },
          },
        },
      ],
    };
    const loaded = resolveTurnSkillStatus([userTurn, completedTurn], 0, false);
    assertEqual(loaded?.status, "loaded");

    // 4. Unconfirmed (turn finished without calling skill tool)
    const noToolTurn = {
      info: {
        id: "msg_a2",
        sessionID: "ses_1",
        role: "assistant" as const,
        time: { created: 101, completed: 105 },
      },
      parts: [
        {
          id: "prt_txt",
          sessionID: "ses_1",
          messageID: "msg_a2",
          type: "text" as const,
          text: "Done without loading skill",
        },
      ],
    };
    const unconfirmed = resolveTurnSkillStatus([userTurn, noToolTurn], 0, false);
    assertEqual(unconfirmed?.status, "unconfirmed");
  });

  it("buildSlashCommandSuggestions attaches skillSelection for every runtime and filters hidden skills", () => {
    const skills: SkillDescriptor[] = [
      {
        key: "skl_visible",
        name: "frontend-design",
        description: "Design polished UI",
        runtime: "opencode",
        source: "project",
        sourceLabel: ".opencode/skills/frontend-design/SKILL.md",
        canOpenLocation: true,
      },
      {
        key: "skl_hidden",
        name: "legacy-skill",
        description: "Hidden skill",
        runtime: "opencode",
        source: "user",
        sourceLabel: "~/.config/opencode/skills/legacy-skill/SKILL.md",
        canOpenLocation: true,
      },
    ];
    const commands = [
      { name: "init", description: "Initialize project", source: "command" as const },
      { name: "frontend-design", description: "Duplicate from command list", source: "skill" as const },
      { name: "github-pr", description: "MCP PR helper", source: "mcp" as const },
    ];

    const opencodeItems = buildSlashCommandSuggestions({
      commands,
      skills,
      query: "",
      runtime: "opencode",
      isHidden: (key) => key === "skl_hidden",
    });
    assertEqual(opencodeItems.length, 3);
    assertEqual(opencodeItems[0].label, "/frontend-design");
    assertEqual(opencodeItems[0].source, "skill");
    assertEqual(opencodeItems[0].skillSelection?.key, "skl_visible");
    assertEqual(opencodeItems[1].label, "/init");
    assertEqual(opencodeItems[1].source, "command");
    assertEqual(opencodeItems[2].label, "/github-pr");
    assertEqual(opencodeItems[2].source, "mcp");

    // CLI runtimes share the same catalog and keep the selection key
    const claudeItems = buildSlashCommandSuggestions({
      commands,
      skills,
      query: "",
      runtime: "claude-code",
    });
    assertEqual(claudeItems.length, 4);
    assertEqual(claudeItems[0].label, "/frontend-design");
    assertEqual(claudeItems[0].skillSelection?.key, "skl_visible");
    assertTrue(
      claudeItems.some((item) => item.label === "/init" && item.source === "command"),
      "commands must remain visible for non-opencode runtimes"
    );
  });

  it("keeps the full skill catalog visible in slash suggestions", () => {
    const skills: SkillDescriptor[] = Array.from({ length: 20 }, (_, i) => ({
      key: `skl_${i}`,
      name: `skill-${i}`,
      description: `Skill ${i}`,
      runtime: "opencode",
      source: "user",
      sourceLabel: `~/.agents/skills/skill-${i}/SKILL.md`,
      canOpenLocation: true,
    }));
    const items = buildSlashCommandSuggestions({
      commands: [],
      skills,
      query: "",
      runtime: "opencode",
    });
    assertEqual(items.length, 20);
    assertEqual(items[19].label, "/skill-19");
  });

  it("marks inline skill delivery as loaded without a native skill tool", () => {
    const userTurn = {
      info: { id: "msg_user_inline", sessionID: "ses_inline", role: "user" as const, time: { created: 200 } },
      parts: [
        {
          id: "prt_text_inline",
          sessionID: "ses_inline",
          messageID: "msg_user_inline",
          type: "text" as const,
          text: "ship it",
          metadata: { hoxiSkill: { key: "skl_inline", name: "release", mode: "inline" } },
        },
      ],
    };
    const status = resolveTurnSkillStatus([userTurn], 0, false);
    assertEqual(status?.status, "loaded");
  });

  it("transfers draft composer skill cleanly to newly created session", () => {
    selectComposerSkill(null, { key: "skl_draft", name: "test-skill" });
    assertEqual(getComposerSkill(null)?.key, "skl_draft");

    transferDraftComposerSkill("ses_new_123");
    assertEqual(getComposerSkill(null), null);
    assertEqual(getComposerSkill("ses_new_123")?.key, "skl_draft");
    selectComposerSkill("ses_new_123", null);
  });

  it("loads the marketplace catalog and records installed state after an install", async () => {
    const original = globalThis.fetch;
    const calls: string[] = [];
    globalThis.fetch = (async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      calls.push(`${init?.method || "GET"} ${url}`);
      if (url.startsWith("/api/hoxi/skills/marketplace/install")) {
        return new Response(
          JSON.stringify({
            ok: true,
            reload: true,
            skill: {
              slug: "demo-skill",
              name: "demo-skill",
              contentUrl: "https://hoxi.ai/skills/demo-skill/SKILL.md",
              contentSha256: "a".repeat(64),
              installed: true,
              installedDigest: "a".repeat(64),
            },
          }),
          { status: 200, headers: { "Content-Type": "application/json" } }
        );
      }
      return new Response(
        JSON.stringify({
          source: "https://hoxi.ai",
          cacheHint: "metadata only",
          skills: [
            {
              slug: "demo-skill",
              name: "demo-skill",
              description: "Demo",
              contentUrl: "https://hoxi.ai/skills/demo-skill/SKILL.md",
              contentSha256: "a".repeat(64),
              installed: false,
            },
          ],
        }),
        { status: 200, headers: { "Content-Type": "application/json" } }
      );
    }) as typeof fetch;

    try {
      const list = await loadSkillMarketplace({ forceRefresh: true });
      assertEqual(list?.length, 1);
      assertEqual(skillsStore.marketplace[0].installed, false);
      assertEqual(skillsStore.marketplaceSource, "https://hoxi.ai");

      const ok = await installSkillFromMarketplace("demo-skill", { overwrite: false });
      assertTrue(ok);
      assertEqual(skillsStore.marketplace[0].installed, true);
      assertEqual(skillsStore.marketplace[0].updateAvailable, false);
      assertTrue(calls.some((entry) => entry === "POST /api/hoxi/skills/marketplace/install"));
    } finally {
      globalThis.fetch = original;
    }
  });

  it("extracts skill name in ToolCard extractDescription", () => {
    const part: Extract<MessagePart, { type: "tool" }> = {
      id: "prt_sk1",
      sessionID: "ses_1",
      messageID: "msg_1",
      type: "tool",
      callID: "call_sk1",
      tool: "skill",
      state: {
        status: "running",
        input: { name: "git-release" },
        time: { start: 100 },
      },
    };
    assertEqual(extractDescription(part), "git-release");
  });
});
