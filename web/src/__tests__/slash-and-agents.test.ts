import { reconcile, unwrap } from "solid-js/store";
import { surfaceExtension as coreSurfaceExtensionStub } from "../components/layout/surface-extension";
import { hoxiAPI } from "../services/api";
import { DEFAULT_AGENTS, loadAgents, setWorkspace, submitSessionPrompt, workspace } from "../store/workspace";
import { assertDeepEqual, assertEqual, assertTrue, describe, it } from "./test-harness";

describe("custom agents and /compact slash command", () => {
  it("keeps DEFAULT_AGENTS free of extension-only modes for open-source builds", () => {
    assertEqual(
      DEFAULT_AGENTS.some((a) => a.extension === true),
      false,
      "open-source DEFAULT_AGENTS must not include extension-only modes"
    );
    if (!coreSurfaceExtensionStub.enabled) {
      assertEqual(coreSurfaceExtensionStub.extraPrimaryModes, undefined);
    }
  });
  it("includes mode='all' custom Markdown agents in workspace.agents while filtering subagents and hidden entries", async () => {
    const savedWorkspace = structuredClone(unwrap(workspace));
    const origListAgents = hoxiAPI.listAgents;
    try {
      hoxiAPI.listAgents = async () => [
        { name: "build", mode: "primary", builtIn: true },
        { name: "plan", mode: "primary", builtIn: true },
        { name: "architect", mode: "all", description: "Custom Markdown architect" },
        { name: "explore", mode: "subagent", builtIn: true },
        { name: "secret-bot", mode: "all", hidden: true },
      ];
      setWorkspace("rootDir", "/custom-agents-ws");
      await loadAgents("/custom-agents-ws");
      assertDeepEqual(
        workspace.agents.map((a) => a.name),
        ["build", "plan", "architect"]
      );
    } finally {
      hoxiAPI.listAgents = origListAgents;
      setWorkspace(reconcile(savedWorkspace));
    }
  });

  it("handles /compact in submitSessionPrompt via summarizeSession without dispatching a chat turn", async () => {
    const savedWorkspace = structuredClone(unwrap(workspace));
    const origSummarize = hoxiAPI.summarizeSession;
    const origGetMessages = hoxiAPI.getMessages;
    const origSendPrompt = hoxiAPI.sendPromptAsync;
    let compactedCall: { sid: string; prov: string; mod: string; dir?: string } | null = null;
    let reloadedMessages = false;
    let sentRawPrompt = false;

    try {
      setWorkspace({
        rootDir: "/compact-ws",
        activeSessionID: "ses_compact_1",
        sessions: [
          {
            id: "ses_compact_1",
            title: "Long conversation",
            directory: "/compact-ws",
            time: { created: 1, updated: 2 },
          },
        ],
        statusBySession: { ses_compact_1: { type: "idle" } },
        defaultAI: {
          agent: "build",
          model: { providerID: "anthropic", modelID: "claude-sonnet-4-5" },
          effort: "",
        },
        aiBySession: {},
      });

      hoxiAPI.summarizeSession = async (sid, prov, mod, dir) => {
        compactedCall = { sid, prov, mod, dir };
        return true;
      };
      hoxiAPI.getMessages = async () => {
        reloadedMessages = true;
        return [];
      };
      hoxiAPI.sendPromptAsync = async () => {
        sentRawPrompt = true;
      };

      const res = await submitSessionPrompt("  /compact  ", "ses_compact_1");
      assertEqual(res, "");
      assertTrue(compactedCall !== null, "summarizeSession must be called");
      assertEqual(compactedCall!.sid, "ses_compact_1");
      assertEqual(compactedCall!.prov, "anthropic");
      assertEqual(compactedCall!.mod, "claude-sonnet-4-5");
      assertTrue(reloadedMessages, "session messages must reload after compaction");
      assertEqual(sentRawPrompt, false, "must not send /compact as a normal prompt");
    } finally {
      hoxiAPI.summarizeSession = origSummarize;
      hoxiAPI.getMessages = origGetMessages;
      hoxiAPI.sendPromptAsync = origSendPrompt;
      setWorkspace(reconcile(savedWorkspace));
    }
  });
});
