// web/src/__tests__/gemini-image-session.test.ts
import { hoxiAPI } from "../services/api";
import {
  AUTO_PROVIDER_ID,
  candidateNeverReportsCache,
  computeSmartDefaultCandidates,
  detectRequiredAttachmentModalities,
  dispatchSessionPrompt,
  evaluatePromptModalityIntent,
  getMediaRoleModelGroups,
  hasConfiguredSpeechModel,
  hasConfiguredVideoGenerationModel,
  inferDedicatedMediaModality,
  isImageGenerationModel,
  isSpeechSynthesisModel,
  isVideoGenerationModel,
  mediaRolePrefs,
  modelSupportsImageInput,
  modelSupportsModalityInput,
  recommendEconomySubagentModel,
  resolveAutoModelTarget,
  saveAutoPool,
  setSelectedImageAspectRatio,
  setSelectedImageSize,
  setWorkspace,
  updateMediaRolePrefs,
  type MessageTurn,
} from "../store/workspace";
import {
  extractCustomProviderFormState,
  guessFetchedModelDraft,
  inferProviderNpmFromEndpoint,
} from "../components/models/custom-endpoint-helpers";
import { classifyAttachmentKind } from "../components/timeline/composer-hooks";
import {
  groupAssistantParts,
  hasLiveTailRenderedOutput,
  resolveMergedTurnParts,
} from "../components/timeline/context-activity-turns";
import { assertDeepEqual, assertEqual, assertTrue, describe, it } from "./test-harness";

describe("Gemini custom endpoint & session image generation support", () => {
  it("auto-infers Google Gemini protocol from URL/providerID while keeping /openai compatible", () => {
    const localBase = ["http:", "", "127.0.0.1:8787"].join("/");
    assertEqual(inferProviderNpmFromEndpoint("gemini-custom", ""), "@ai-sdk/google");
    assertEqual(inferProviderNpmFromEndpoint("relay", `${localBase}/v1beta`), "@ai-sdk/google");
    assertEqual(
      inferProviderNpmFromEndpoint("google", `${localBase}/v1beta/openai`),
      "@ai-sdk/openai-compatible"
    );
  });

  it("classifies Gemini image models without enabling reasoning variants and preserves image edit input", () => {
    assertEqual(inferDedicatedMediaModality("gemini-2.5-flash-image"), "image");
    assertEqual(inferDedicatedMediaModality("gemini-3-pro-image-preview"), "image");
    assertEqual(inferDedicatedMediaModality("gemini-2.0-flash-preview-image-generation"), "image");
    assertEqual(inferDedicatedMediaModality("gemini-2.5-pro"), null);

    const flashImgDraft = guessFetchedModelDraft({
      id: "gemini-2.5-flash-image",
      name: "Gemini 2.5 Flash Image",
    });
    assertEqual(flashImgDraft.reasoning, false);
    assertEqual(flashImgDraft.image, true);

    const imagenDraft = guessFetchedModelDraft({
      id: "imagen-3.0-generate-002",
      name: "Imagen 3",
    });
    assertEqual(imagenDraft.reasoning, false);
    assertEqual(imagenDraft.image, false);

    const flash20Draft = guessFetchedModelDraft({
      id: "models/gemini-2.0-flash",
      name: "Gemini 2.0 Flash",
    });
    assertEqual(flash20Draft.id, "gemini-2.0-flash");
    assertEqual(flash20Draft.reasoning, false);
    assertEqual(flash20Draft.image, true);

    const pro25Draft = guessFetchedModelDraft({
      id: "models/gemini-2.5-pro",
      name: "Gemini 2.5 Pro",
    });
    assertEqual(pro25Draft.id, "gemini-2.5-pro");
    assertEqual(pro25Draft.reasoning, true);
    assertEqual(pro25Draft.image, true);

    setWorkspace("providers", [
      {
        id: "custom-gemini",
        name: "Custom Gemini",
        models: {
          "gemini-2.5-flash-image": {
            id: "gemini-2.5-flash-image",
            name: "Gemini 2.5 Flash Image",
          },
          "gemini-2.5-pro": {
            id: "gemini-2.5-pro",
            name: "Gemini 2.5 Pro",
            capabilities: { toolcall: true, reasoning: true },
          },
          "imagen-3.0-generate-002": {
            id: "imagen-3.0-generate-002",
            name: "Imagen 3",
          },
        },
      },
    ]);
    setWorkspace("connectedProviders", ["custom-gemini"]);

    assertTrue(isImageGenerationModel("custom-gemini", "gemini-2.5-flash-image"));
    assertTrue(modelSupportsImageInput("custom-gemini", "gemini-2.5-flash-image"));
    assertEqual(modelSupportsImageInput("custom-gemini", "imagen-3.0-generate-002"), false);

    // Dedicated image models must not leak into smart chat/coding auto pools.
    const dailyCands = computeSmartDefaultCandidates("daily");
    assertEqual(
      dailyCands.some((c) => c.modelID === "gemini-2.5-flash-image"),
      false
    );
  });

  it("dispatches explicitly selected Gemini image model directly with aspectRatio and imageSize options", async () => {
    const origSend = hoxiAPI.sendPromptAsync;
    const origSavePool = hoxiAPI.saveAutoPoolConfig;
    let capturedModel: { providerID: string; modelID: string } | undefined;
    let capturedSystem: string | undefined;
    let capturedImageOptions: { aspectRatio?: string; imageSize?: string; count?: number } | undefined;

    (hoxiAPI as any).saveAutoPoolConfig = async () => ({ ok: true, autoPools: {} });
    (hoxiAPI as any).sendPromptAsync = async (_sid: string, _text: string, opts?: any) => {
      capturedModel = opts?.model;
      capturedSystem = opts?.system;
      capturedImageOptions = opts?.imageOptions;
    };

    try {
      setSelectedImageAspectRatio("16:9");
      setSelectedImageSize("2K");
      setWorkspace("providers", [
        {
          id: "custom-gemini",
          name: "Custom Gemini",
          models: {
            "gemini-2.5-flash-image": {
              id: "gemini-2.5-flash-image",
              name: "Gemini 2.5 Flash Image",
            },
            "gemini-2.5-pro": {
              id: "gemini-2.5-pro",
              name: "Gemini 2.5 Pro",
              capabilities: { toolcall: true, reasoning: true },
            },
          },
        },
      ]);
      setWorkspace("connectedProviders", ["custom-gemini"]);
      await saveAutoPool("daily", {
        id: "daily",
        preset: true,
        strategy: "priority",
        customized: true,
        candidates: [{ providerID: "custom-gemini", modelID: "gemini-2.5-pro" }],
      });

      const sid = "ses_img_direct_test";
      setWorkspace("sessions", [
        {
          id: sid,
          title: "Image Test",
          directory: "/tmp",
          runtime: "opencode",
          time: { created: 1, updated: 1 },
        } as any,
      ]);
      setWorkspace("messagesBySession", sid, []);
      setWorkspace("statusBySession", sid, { type: "idle" });
      setWorkspace("aiBySession", sid, {
        agent: "build",
        model: { providerID: "custom-gemini", modelID: "gemini-2.5-flash-image" },
      } as any);

      await dispatchSessionPrompt("generate image product photo of a cyberpunk cat", sid, {
        followTimeline: false,
      });

      assertDeepEqual(capturedModel, {
        providerID: "custom-gemini",
        modelID: "gemini-2.5-flash-image",
      });
      assertDeepEqual(capturedImageOptions, {
        aspectRatio: "16:9",
        imageSize: "2K",
        count: 1,
      });
      assertEqual(Boolean(capturedSystem?.includes("[Modality Router:")), false);
      assertEqual(capturedModel?.providerID === AUTO_PROVIDER_ID, false);
    } finally {
      setSelectedImageAspectRatio("1:1");
      setSelectedImageSize("1K");
      (hoxiAPI as any).sendPromptAsync = origSend;
      (hoxiAPI as any).saveAutoPoolConfig = origSavePool;
    }
  });

  it("classifies audio/video attachments, recommends economy subagent models, and gates no-cache detection by cacheEligibleTurns", () => {
    assertEqual(classifyAttachmentKind("voice_memo.mp3", "audio/mpeg"), "audio");
    assertEqual(classifyAttachmentKind("recording.m4a", ""), "audio");
    assertEqual(classifyAttachmentKind("walkthrough.mp4", "video/mp4"), "video");
    assertEqual(classifyAttachmentKind("clip.mov", ""), "video");

    setWorkspace("providers", [
      {
        id: "custom-gemini",
        name: "Custom Gemini",
        models: {
          "gemini-2.5-pro": {
            id: "gemini-2.5-pro",
            name: "Gemini 2.5 Pro",
            capabilities: { toolcall: true },
          },
          "gemini-2.5-flash": {
            id: "gemini-2.5-flash",
            name: "Gemini 2.5 Flash",
            family: "gemini-flash",
            capabilities: { toolcall: true },
          },
          "gemini-2.5-flash-image": {
            id: "gemini-2.5-flash-image",
            name: "Gemini 2.5 Flash Image",
            modalities: { input: ["text", "image"], output: ["text", "image"] },
          },
        },
      },
    ]);
    setWorkspace("connectedProviders", ["custom-gemini"]);
    assertEqual(
      recommendEconomySubagentModel("custom-gemini"),
      "custom-gemini/gemini-2.5-flash"
    );

    // 30 short turns (< 2048 input tokens -> cacheEligibleTurns = 3) must NOT be penalized as no-cache
    setWorkspace("providerTelemetry", {
      "custom-gemini": {
        providerID: "custom-gemini",
        totalTurns: 30,
        successTurns: 30,
        errorTurns: 0,
        timeoutTurns: 0,
        retryCount: 0,
        toolCallsTotal: 0,
        toolCallsFailed: 0,
        hallucinationFlags: 0,
        revertCount: 0,
        cacheHitTurns: 0,
        cacheEligibleTurns: 3,
        updatedAt: Date.now(),
        models: {
          "gemini-2.5-flash": {
            modelID: "gemini-2.5-flash",
            totalTurns: 30,
            successTurns: 30,
            errorTurns: 0,
            timeoutTurns: 0,
            retryCount: 0,
            toolCallsTotal: 0,
            toolCallsFailed: 0,
            hallucinationFlags: 0,
            revertCount: 0,
            cacheHitTurns: 0,
            cacheEligibleTurns: 3,
            updatedAt: Date.now(),
          },
        },
      },
    });
    assertEqual(candidateNeverReportsCache("custom-gemini", "gemini-2.5-flash"), false);

    // 22 cache-eligible (>= 2048 input token) turns with 0 cache hits ARE flagged as no-cache
    setWorkspace(
      "providerTelemetry",
      "custom-gemini",
      "models",
      "gemini-2.5-flash",
      "cacheEligibleTurns",
      22
    );
    assertEqual(candidateNeverReportsCache("custom-gemini", "gemini-2.5-flash"), true);
  });

  it("renders assistant image-only and text+image turns in timeline grouping", () => {
    const turns: MessageTurn[] = [
      {
        info: { id: "u1", sessionID: "s1", role: "user", time: { created: 100 } },
        parts: [{ id: "pu1", sessionID: "s1", messageID: "u1", type: "text", text: "draw an orange cat" }],
      },
      {
        info: {
          id: "a1",
          sessionID: "s1",
          role: "assistant",
          time: { created: 101, completed: 120 },
        },
        parts: [
          {
            id: "pa1_img",
            sessionID: "s1",
            messageID: "a1",
            type: "file",
            mime: "image/png",
            filename: "generated_1.png",
            url: "/api/hoxi/attachments/att_1/content",
          },
        ],
      },
    ];

    const merged = resolveMergedTurnParts(turns, 1);
    assertEqual(merged.isVisuallyEmpty, false);
    assertEqual(merged.effectiveParts.length, 1);

    const grouped = groupAssistantParts(merged.effectiveParts);
    assertEqual(grouped.length, 1);
    assertEqual(grouped[0]?.kind, "single");
    assertTrue(hasLiveTailRenderedOutput([turns[1]!]));
  });

  it("routes audio/video/pdf attachments to multimodal candidates in Auto pools and round-trips granular custom model capabilities", async () => {
    const origSavePool = hoxiAPI.saveAutoPoolConfig;
    (hoxiAPI as any).saveAutoPoolConfig = async () => ({ ok: true, autoPools: {} });
    try {
      const reqMods = detectRequiredAttachmentModalities([
        { mime: "application/pdf", filename: "arch.pdf" },
        { mime: "video/mp4", filename: "demo.mp4" },
      ]);
      assertDeepEqual(reqMods, ["pdf", "video"]);

      setWorkspace("providers", [
        {
          id: "deepseek",
          name: "DeepSeek",
          models: {
            "deepseek-chat": {
              id: "deepseek-chat",
              name: "DeepSeek V3",
              capabilities: { toolcall: true, input: { text: true, image: false, pdf: false, audio: false, video: false } },
            },
          },
        },
        {
          id: "custom-gemini",
          name: "Custom Gemini",
          models: {
            "gemini-2.5-pro": {
              id: "gemini-2.5-pro",
              name: "Gemini 2.5 Pro",
              capabilities: {
                toolcall: true,
                reasoning: true,
                input: { text: true, image: true, pdf: true, audio: true, video: true },
              },
            },
          },
        },
      ]);
      setWorkspace("connectedProviders", ["deepseek", "custom-gemini"]);

      await saveAutoPool("daily", {
        id: "daily",
        preset: true,
        strategy: "priority",
        customized: true,
        candidates: [
          { providerID: "deepseek", modelID: "deepseek-chat" },
          { providerID: "custom-gemini", modelID: "gemini-2.5-pro" },
        ],
      });

      assertEqual(modelSupportsModalityInput("deepseek", "deepseek-chat", "video"), false);
      assertEqual(modelSupportsModalityInput("custom-gemini", "gemini-2.5-pro", "video"), true);

      // Plain text picks top priority deepseek-chat; video attachment automatically routes to gemini-2.5-pro.
      const textPick = resolveAutoModelTarget("daily");
      assertDeepEqual(textPick, { providerID: "deepseek", modelID: "deepseek-chat" });

      const videoPick = resolveAutoModelTarget("daily", { requiredModalities: reqMods });
      assertDeepEqual(videoPick, { providerID: "custom-gemini", modelID: "gemini-2.5-pro" });

      // Granular custom provider form extraction preserves category, toolCall, pdf, and media flags.
      setWorkspace("hoxiProviders", {
        "my-gw": {
          name: "My Gateway",
          npm: "@ai-sdk/google",
          models: {
            "gemini-2.5-pro": {
              name: "Gemini 2.5 Pro",
              capabilities: {
                reasoning: true,
                toolcall: true,
                input: { image: true, pdf: true, audio: true, video: true },
              },
            },
            "whisper-large-v3": {
              name: "Whisper v3",
              modalities: { input: ["audio"], output: ["text"] },
            },
          },
        },
      } as any);
      const extracted = extractCustomProviderFormState("my-gw");
      const gemModel = extracted.models.find((m) => m.id === "gemini-2.5-pro");
      const asrModel = extracted.models.find((m) => m.id === "whisper-large-v3");
      assertEqual(gemModel?.category, "chat");
      assertEqual(gemModel?.toolCall, true);
      assertEqual(gemModel?.pdf, true);
      assertEqual(gemModel?.media, true);
      assertEqual(asrModel?.category, "audio");

      assertEqual(modelSupportsModalityInput("openai", "gpt-6-astra", "pdf"), true);
      assertEqual(modelSupportsModalityInput("anthropic", "claude-haiku-5-5", "pdf"), true);
      assertEqual(modelSupportsModalityInput("alibaba", "qwen3.5-plus", "video"), true);
      const specialeDraft = guessFetchedModelDraft({
        id: "deepseek-v3.2-speciale",
        name: "DeepSeek V3.2 Speciale",
      });
      assertEqual(specialeDraft.toolCall, false);
      const gpt6Draft = guessFetchedModelDraft({
        id: "gpt-6-astra",
        name: "GPT-6 Astra",
      });
      assertEqual(gpt6Draft.pdf, true);
    } finally {
      (hoxiAPI as any).saveAutoPoolConfig = origSavePool;
    }
  });

  it("self-checks native/configured video capability before routing to hoxi_video vs hoxi_tools and preserves explicit video model in chat", async () => {
    const origSend = hoxiAPI.sendPromptAsync;
    let capturedModel: { providerID: string; modelID: string } | undefined;
    (hoxiAPI as any).sendPromptAsync = async (
      _sid: string,
      _text: string,
      opts?: { model?: { providerID: string; modelID: string } }
    ) => {
      capturedModel = opts?.model;
      return { ok: true, messageID: "msg_vid_1" };
    };

    try {
      setWorkspace("providers", [
        {
          id: "openai",
          name: "OpenAI",
          models: {
            "gpt-5": { id: "gpt-5", name: "GPT-5", capabilities: { toolcall: true } },
          },
        },
      ]);
      setWorkspace("hoxiProviders", {} as any);
      assertEqual(hasConfiguredVideoGenerationModel(), false);
      const noLocalVid = evaluatePromptModalityIntent("generate video of ocean waves");
      assertEqual(noLocalVid.kind, "video_gen");
      assertEqual(Boolean(noLocalVid.mediaRoutingDirective?.includes("hoxi_tools")), true);

      setWorkspace("providers", [
        {
          id: "google",
          name: "Google Gemini",
          models: {
            "gemini-2.5-pro": { id: "gemini-2.5-pro", name: "Gemini 2.5 Pro", capabilities: { toolcall: true } },
            "veo-3.0-generate-001": { id: "veo-3.0-generate-001", name: "Veo 3" },
          },
        },
      ]);
      assertEqual(isVideoGenerationModel("google", "veo-3.0-generate-001"), true);
      assertEqual(hasConfiguredVideoGenerationModel(), true);
      const withLocalVid = evaluatePromptModalityIntent("generate video of ocean waves");
      assertEqual(withLocalVid.mediaRoutingDirective?.startsWith("<hoxi_turn_directive>\n"), true);
      assertEqual(withLocalVid.mediaRoutingDirective?.endsWith("\n</hoxi_turn_directive>"), true);
      assertEqual(evaluatePromptModalityIntent("refactor the parser").mediaRoutingDirective, undefined);
      assertEqual(Boolean(withLocalVid.mediaRoutingDirective?.includes("Invoke `hoxi_video`")), true);

      // Modality intent precision: pasted store reports, code reviews, and English substrings must NOT misfire.
      const auditReportPrompt =
        "\u5168\u5e97\u5206\u6790\u7ed3\u679c\u6709\u6ca1\u6709\u4ee3\u7801\u95ee\u9898\uff1f\n\n\u767d\u5e95\u56fe\u5360\u6bd4\u504f\u4f4e\uff0c\u5efa\u8bae\u8865\u5145\u573a\u666f\u56fe\u548c\u6d77\u62a5\u56fe";
      assertEqual(evaluatePromptModalityIntent(auditReportPrompt).kind, "chat");
      assertEqual(evaluatePromptModalityIntent(auditReportPrompt).mediaRoutingDirective, undefined);
      assertEqual(
        evaluatePromptModalityIntent("\u6392\u67e5\u751f\u56fe\u63a5\u53e3\u8fd4\u56de 500 bug").mediaRoutingDirective,
        undefined
      );
      assertEqual(
        evaluatePromptModalityIntent("\u68c0\u67e5 hero-card.svg \u6e32\u67d3\u4ee3\u7801").mediaRoutingDirective,
        undefined
      );
      for (const prompt of [
        "\u4e0d\u8981\u751f\u6210\u56fe\u7247\uff0c\u53ea\u5206\u6790\u65e5\u5fd7", "\u8bf7\u4fee\u590d\u751f\u6210\u56fe\u7247\u63a5\u53e3\u7684\u8d85\u65f6\u95ee\u9898", "fix render image cache invalidation",
        "\u7ed9\u201c\u751f\u6210\u56fe\u7247\u201d\u6309\u94ae\u6dfb\u52a0\u5355\u5143\u6d4b\u8bd5", "Explain the phrase \"generate a video\"",
        "do not generate a video", "\u4e0d\u8981\u5e2e\u6211\u5408\u6210\u914d\u97f3", "explain the `generate an image` command",
      ]) {
        assertEqual(evaluatePromptModalityIntent(prompt).mediaRoutingDirective, undefined, prompt);
      }
      assertEqual(evaluatePromptModalityIntent("\u4e0d\u8981\u751f\u6210\u89c6\u9891\uff0c\u5e2e\u6211\u751f\u6210\u4e00\u5f20\u5c01\u9762").kind, "image_gen");
      assertEqual(evaluatePromptModalityIntent("\u5e2e\u6211\u751f\u6210\u4e00\u5f20\u7279\u522b\u7684\u6d77\u62a5").kind, "image_gen");
      assertEqual(evaluatePromptModalityIntent("\u5e2e\u6211\u5236\u4f5c\u4e00\u5f20\u6545\u969c\u8bca\u65ad\u6d77\u62a5").kind, "image_gen");
      assertEqual(evaluatePromptModalityIntent("generate an image without text").kind, "image_gen");
      assertEqual(evaluatePromptModalityIntent("\u4e0d\u8981\u751f\u6210\u56fe\u7247", { chipId: "hero-card.svg" }).kind, "chat");
      assertEqual(evaluatePromptModalityIntent("We are tackling this performance regression").mediaRoutingDirective, undefined);
      assertEqual(evaluatePromptModalityIntent("Inspect the watts calculation in power.ts").mediaRoutingDirective, undefined);
      assertEqual(
        evaluatePromptModalityIntent("\u6392\u67e5 tts \u914d\u97f3\u63a5\u53e3 bug").mediaRoutingDirective,
        undefined
      );
      assertEqual(
        evaluatePromptModalityIntent("\u68c0\u67e5 sora / veo \u9996\u5c3e\u5e27\u89c6\u9891\u914d\u7f6e\u4ee3\u7801").mediaRoutingDirective,
        undefined
      );
      assertEqual(
        evaluatePromptModalityIntent("\u4fee\u590d\u5c01\u9762\u8bca\u65ad\u6a21\u5757\u6e32\u67d3\u95ee\u9898").mediaRoutingDirective,
        undefined
      );

      // Genuine creation/review requests and explicit chip/skill metadata still route accurately.
      assertEqual(
        evaluatePromptModalityIntent("\u5e2e\u6211\u751f\u6210\u4e00\u5f20\u5546\u54c1\u767d\u5e95\u56fe").kind,
        "image_gen"
      );
      assertEqual(evaluatePromptModalityIntent("\u753b\u4e00\u5f20 3:4 \u7ad6\u7248\u5c01\u9762").kind, "image_gen");
      assertEqual(evaluatePromptModalityIntent("\u505a\u4e00\u5f20\u6d77\u62a5\u56fe").kind, "image_gen");
      assertEqual(evaluatePromptModalityIntent("generate an illustration of a mountain").kind, "image_gen");
      assertEqual(evaluatePromptModalityIntent("\u5e2e\u6211\u751f\u56fe").kind, "image_gen");
      assertEqual(evaluatePromptModalityIntent("continue", { chipId: "hero-card.svg" }).kind, "image_gen");
      assertEqual(evaluatePromptModalityIntent("\u5e2e\u6211\u751f\u6210\u4e00\u6bb5\u53e3\u64ad\u914d\u97f3").kind, "audio_tts");
      assertEqual(evaluatePromptModalityIntent("\u5e2e\u6211\u505a\u4e00\u4e2a\u89c6\u89c9\u590d\u76d8").kind, "visual_review");
      assertEqual(evaluatePromptModalityIntent("continue", { skillName: "sora-storyboard" }).kind, "video_gen");

      // Explicitly selected video model in chat must NOT be hijacked to fallbackBrain
      const sid = "ses_vid_direct_test";
      setWorkspace("sessions", [
        { id: sid, title: "Video Test", directory: "/tmp", runtime: "native", time: { created: 1, updated: 1 } } as any,
      ]);
      setWorkspace("messagesBySession", sid, []);
      setWorkspace("statusBySession", sid, { type: "idle" });
      setWorkspace("aiBySession", sid, {
        agent: "build",
        model: { providerID: "google", modelID: "veo-3.0-generate-001" },
      } as any);

      await dispatchSessionPrompt("generate video of ocean waves", sid, { followTimeline: false });
      assertDeepEqual(capturedModel, { providerID: "google", modelID: "veo-3.0-generate-001" });

      // Speech synthesis detection & media role groups / prefs round-trip
      setWorkspace("providers", [
        {
          id: "openai",
          name: "OpenAI",
          models: {
            "gpt-4o-mini-tts": { id: "gpt-4o-mini-tts", name: "GPT-4o Mini TTS" },
            "whisper-1": { id: "whisper-1", name: "Whisper 1" },
          },
        },
      ]);
      assertEqual(isSpeechSynthesisModel("openai", "gpt-4o-mini-tts"), true);
      assertEqual(hasConfiguredSpeechModel(), true);
      const speechIntent = evaluatePromptModalityIntent("generate voiceover tts audio");
      assertEqual(Boolean(speechIntent.mediaRoutingDirective?.includes("invoke `hoxi_speech`")), true);
      assertEqual(
        Boolean(speechIntent.mediaRoutingDirective?.includes("configured local TTS models")),
        true
      );
      assertEqual(getMediaRoleModelGroups("speech").length, 1);
      assertEqual(getMediaRoleModelGroups("transcribe").length, 1);

      const origUpdateRoles = hoxiAPI.updateMediaRoles;
      (hoxiAPI as any).updateMediaRoles = async (patch: Record<string, string | null>) => ({
        ok: true,
        roles: { speechModel: patch.speechModel || "" },
      });
      try {
        await updateMediaRolePrefs({ speechModel: "openai/gpt-4o-mini-tts" });
        assertEqual(mediaRolePrefs().speechModel, "openai/gpt-4o-mini-tts");
      } finally {
        (hoxiAPI as any).updateMediaRoles = origUpdateRoles;
      }
    } finally {
      (hoxiAPI as any).sendPromptAsync = origSend;
    }
  });
});
