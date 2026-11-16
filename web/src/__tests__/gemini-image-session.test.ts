// web/src/__tests__/gemini-image-session.test.ts
import { hoxiAPI } from "../services/api";
import {
  AUTO_PROVIDER_ID,
  candidateNeverReportsCache,
  computeSmartDefaultCandidates,
  detectRequiredAttachmentModalities,
  dispatchSessionPrompt,
  inferDedicatedMediaModality,
  isImageGenerationModel,
  modelSupportsImageInput,
  modelSupportsModalityInput,
  recommendEconomySubagentModel,
  resolveAutoModelTarget,
  saveAutoPool,
  setSelectedImageAspectRatio,
  setSelectedImageSize,
  setWorkspace,
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
});
