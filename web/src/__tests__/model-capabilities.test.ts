// web/src/__tests__/model-capabilities.test.ts
import {
  assertDeepEqual,
  assertEqual,
  assertTrue,
  describe,
  it,
} from "./test-harness";
import {
  getSlotsForCapabilities,
  isExcludedUtilityModel,
  matchesModelSlot,
  normalizeModelCapabilities,
  normalizeStoredModelCapabilities,
} from "../store/model-capabilities";
import {
  getMediaRoleModelGroups,
  getSlotModelGroups,
  resolveLegacyImageEditFallbackSpec,
} from "../store/media-options";
import { workspace, setWorkspace } from "../store/workspace-state";
import {
  extractCustomProviderFormState,
  guessFetchedModelDraft,
} from "../components/models/custom-endpoint-helpers";
import { hoxiAPI } from "../services/api";
import { saveCustomProvider } from "../store/provider-connections";
import {
  getInternalLLMProviderModelGroups,
  isInternalLLMModel,
} from "../store/provider-catalog";
import {
  hasConfiguredImageGenerationModel,
  hasConfiguredSpeechModel,
  hasConfiguredVideoGenerationModel,
} from "../store/auto-pool-defaults";

describe("Unified Model Capabilities & Business Slots", () => {
  it("normalizes explicit /v1/models capabilities and image_translate.output_modes", () => {
    const editorTrans = normalizeModelCapabilities({
      modelID: "alimt-image-translate",
      capabilities: ["image_translate"],
      imageTranslate: { output_modes: ["raster", "editable_scene"] },
    });
    assertDeepEqual(editorTrans.capabilities, ["image_translate"]);
    assertDeepEqual(editorTrans.imageTranslate?.outputModes, ["raster", "editable_scene"]);
    assertTrue(matchesModelSlot(editorTrans, "image_translate_raster"));
    assertTrue(matchesModelSlot(editorTrans, "image_translate_editor"));
    assertTrue(!matchesModelSlot(editorTrans, "image_generate"));
    assertTrue(!matchesModelSlot(editorTrans, "image_edit"));
    assertTrue(!matchesModelSlot(editorTrans, "vision"));

    const rasterTrans = normalizeModelCapabilities({
      modelID: "qwen-mt-image",
      capabilities: ["image_translate"],
      imageTranslate: { output_modes: ["raster"] },
    });
    assertTrue(matchesModelSlot(rasterTrans, "image_translate_raster"));
    assertTrue(!matchesModelSlot(rasterTrans, "image_translate_editor"));

    const imgEdit = normalizeModelCapabilities({
      modelID: "gpt-image-1",
      capabilities: ["image_generate", "image_edit"],
    });
    assertTrue(matchesModelSlot(imgEdit, "image_generate"));
    assertTrue(matchesModelSlot(imgEdit, "image_edit"));
    assertTrue(!matchesModelSlot(imgEdit, "image_translate_editor"));

    const imgGenOnly = normalizeModelCapabilities({
      modelID: "flux-schnell",
      capabilities: ["image_generate"],
    });
    assertTrue(matchesModelSlot(imgGenOnly, "image_generate"));
    assertTrue(!matchesModelSlot(imgGenOnly, "image_edit"));
  });

  it("derives backward-compatible capabilities from legacy modality when capabilities is omitted", () => {
    const legacyEditor = normalizeModelCapabilities({
      modelID: "custom-mt",
      modality: "translate_image",
    });
    assertDeepEqual(legacyEditor.capabilities, ["image_translate"]);
    assertDeepEqual(legacyEditor.imageTranslate?.outputModes, ["raster", "editable_scene"]);
    assertTrue(matchesModelSlot(legacyEditor, "image_translate_editor"));
    assertTrue(matchesModelSlot(legacyEditor, "image_translate_raster"));

    const legacyRaster = normalizeModelCapabilities({
      modelID: "qwen-mt-image",
      modality: "translate_image_task",
    });
    assertDeepEqual(legacyRaster.capabilities, ["image_translate"]);
    assertDeepEqual(legacyRaster.imageTranslate?.outputModes, ["raster"]);
    assertTrue(!matchesModelSlot(legacyRaster, "image_translate_editor"));

    const legacyEditImg = normalizeModelCapabilities({
      modelID: "qwen-image-edit-v1",
      modality: "image",
    });
    assertDeepEqual(legacyEditImg.capabilities, ["image_generate", "image_edit"]);

    const legacyGenOnly = normalizeModelCapabilities({
      modelID: "midjourney-v7",
      modality: "image",
    });
    assertDeepEqual(legacyGenOnly.capabilities, ["image_generate"]);

    const legacyVision = normalizeModelCapabilities({
      modelID: "custom-vl",
      modality: "vision",
    });
    assertDeepEqual(legacyVision.capabilities, ["chat", "vision", "text_translate"]);

    const legacyText = normalizeModelCapabilities({
      modelID: "gpt-4o-text-proxy",
      modality: "text",
    });
    assertDeepEqual(legacyText.capabilities, ["chat", "text_translate"]);
    assertTrue(!matchesModelSlot(legacyText, "vision"), "modality=text must not enter vision slot");
  });

  it("excludes utility/crawl/collector/codex-auto-review models from all slots", () => {
    for (const [id, mod] of [
      ["collector-1688", undefined],
      ["codex-auto-review", undefined],
      ["crawl-product", undefined],
      ["any-model", "crawl"],
    ] as const) {
      assertTrue(isExcludedUtilityModel(id, mod), `expected ${id}/${mod} to be excluded`);
      const norm = normalizeModelCapabilities({ modelID: id, modality: mod });
      assertDeepEqual(norm.capabilities, []);
      assertDeepEqual(getSlotsForCapabilities(norm), []);
    }
  });

  it("strictly excludes pure-text models from vision slot", () => {
    for (const pureTextID of ["deepseek-chat", "deepseek-r1", "qwen-coder-plus", "qwen-mt-plus"]) {
      const norm = normalizeModelCapabilities({ modelID: pureTextID });
      assertTrue(matchesModelSlot(norm, "chat"));
      assertTrue(matchesModelSlot(norm, "text_translate"));
      assertTrue(!matchesModelSlot(norm, "vision"), `${pureTextID} must not match vision`);
    }
  });

  it("maps fetched gateway models into typed drafts preserving capabilityTags & outputModes", () => {
    const alimt = guessFetchedModelDraft({
      id: "alimt-image-translate",
      name: "AliMT Image Translate",
      capabilities: ["image_translate"],
      image_translate: { output_modes: ["raster", "editable_scene"] },
    });
    assertEqual(alimt.category, "image_translate");
    assertDeepEqual(alimt.capabilityTags, ["image_translate"]);
    assertDeepEqual(alimt.imageTranslateOutputModes, ["raster", "editable_scene"]);

    const editModel = guessFetchedModelDraft({
      id: "seedream-4-edit",
      name: "Seedream Edit",
      modality: "image",
    });
    assertEqual(editModel.category, "image");
    assertEqual(editModel.image, true);
    assertDeepEqual(editModel.capabilityTags, ["image_generate", "image_edit"]);

    const excluded = guessFetchedModelDraft({
      id: "collector-1688",
      name: "Collector",
      modality: "crawl",
    });
    assertDeepEqual(excluded.capabilityTags, []);
  });

  it("filters slot candidate groups strictly without mixing incompatible models", () => {
    const prevProviders = workspace.providers;
    const prevHoxiProviders = workspace.hoxiProviders;
    try {
      setWorkspace("providers", [
        {
          id: "ainode",
          name: "AINode",
          models: {
            "deepseek-chat": {
              id: "deepseek-chat",
              name: "DeepSeek Chat",
              modality: "text",
            } as any,
            "gpt-4o": {
              id: "gpt-4o",
              name: "GPT-4o",
              modality: "vision",
            } as any,
            "flux-schnell": {
              id: "flux-schnell",
              name: "Flux Schnell",
              capabilityTags: ["image_generate"],
            } as any,
            "gpt-image-1": {
              id: "gpt-image-1",
              name: "GPT Image 1",
              capabilityTags: ["image_generate", "image_edit"],
            } as any,
            "qwen-mt-image": {
              id: "qwen-mt-image",
              name: "Qwen MT Image",
              capabilityTags: ["image_translate"],
              imageTranslate: { outputModes: ["raster"] },
            } as any,
            "alimt-image-translate": {
              id: "alimt-image-translate",
              name: "AliMT Image Translate",
              capabilityTags: ["image_translate"],
              imageTranslate: { outputModes: ["raster", "editable_scene"] },
            } as any,
            "collector-1688": {
              id: "collector-1688",
              name: "Collector 1688",
              modality: "crawl",
            } as any,
          },
        } as any,
      ]);
      setWorkspace("hoxiProviders", {});

      const flatIDs = (groups: ReturnType<typeof getSlotModelGroups>) =>
        groups.flatMap((g) => g.models.map((m) => m.fullKey)).sort();

      assertDeepEqual(flatIDs(getSlotModelGroups("vision")), ["ainode/gpt-4o"]);
      assertDeepEqual(flatIDs(getSlotModelGroups("image_generate")), [
        "ainode/flux-schnell",
        "ainode/gpt-image-1",
      ]);
      assertDeepEqual(flatIDs(getSlotModelGroups("image_edit")), ["ainode/gpt-image-1"]);
      assertDeepEqual(flatIDs(getSlotModelGroups("image_translate_editor")), [
        "ainode/alimt-image-translate",
      ]);
      assertDeepEqual(flatIDs(getSlotModelGroups("image_translate_raster")), [
        "ainode/alimt-image-translate",
        "ainode/qwen-mt-image",
      ]);
      // No rerank configured -> empty group, never stuffed with incompatible models.
      assertDeepEqual(flatIDs(getMediaRoleModelGroups("rerank")), []);

      // Stored capability normalization reads OpenCode capability objects + explicit tags cleanly.
      const stored = normalizeStoredModelCapabilities("alimt-image-translate", {
        capabilities: ["image_translate"],
        image_translate: { output_modes: ["raster", "editable_scene"] },
      });
      assertTrue(matchesModelSlot(stored, "image_translate_editor"));
      assertEqual(resolveLegacyImageEditFallbackSpec(), "");
    } finally {
      setWorkspace("providers", prevProviders);
      setWorkspace("hoxiProviders", prevHoxiProviders);
    }
  });

  it("preserves mediaModels across custom provider form edit/save and keeps non-chat models out of chat selection & internal LLM roles", async () => {
    const prevProviders = workspace.providers;
    const prevHoxiProviders = workspace.hoxiProviders;
    const prevConnected = workspace.connectedProviders;
    const prevActiveSid = workspace.activeSessionID;
    const prevDefaultAI = { ...workspace.defaultAI };
    const origSaveCfg = hoxiAPI.saveHoxiProviderConfig;
    const origSetAuth = hoxiAPI.setProviderAuth;
    const origListProv = hoxiAPI.listProviders;
    const origGetModelsCfg = hoxiAPI.getHoxiModelsConfig;
    const origGetCLI = hoxiAPI.getLocalCLIStatus;
    const origGetMediaCaps = hoxiAPI.getMediaCapabilities;

    let capturedSaveCfg = null as Record<string, any> | null;
    try {
      setWorkspace("hoxiProviders", {
        "ainode-gw": {
          name: "AINode",
          npm: "@ai-sdk/openai-compatible",
          options: { baseURL: "http://127.0.0.1:8080/v1", apiKey: "sk-test" },
          models: {
            "gpt-5.4": {
              name: "GPT-5.4",
              category: "chat",
              capabilityTags: ["chat", "vision", "text_translate"],
            },
          },
          mediaModels: {
            "alimt-image-translate": {
              name: "AliMT Image Translate",
              category: "image_translate",
              capabilityTags: ["image_translate"],
              imageTranslate: { outputModes: ["raster", "editable_scene"] },
            },
            "gpt-image-1": {
              name: "GPT Image 1",
              category: "image",
              capabilityTags: ["image_generate", "image_edit"],
            },
            "veo-3": {
              name: "Veo 3",
              category: "video",
              capabilityTags: ["video"],
            },
            "cosyvoice-v3-flash": {
              name: "CosyVoice",
              category: "tts",
              capabilityTags: ["tts"],
            },
          },
        },
      } as any);
      setWorkspace("providers", [
        {
          id: "ainode-gw",
          name: "AINode",
          models: {
            "gpt-5.4": { id: "gpt-5.4", name: "GPT-5.4" } as any,
            "gpt-image-1": {
              id: "gpt-image-1",
              name: "GPT Image 1",
              category: "image",
              capabilityTags: ["image_generate", "image_edit"],
            } as any,
            "alimt-image-translate": {
              id: "alimt-image-translate",
              name: "AliMT",
              category: "image_translate",
              capabilityTags: ["image_translate"],
            } as any,
          },
        } as any,
      ]);
      setWorkspace("connectedProviders", ["ainode-gw"]);
      setWorkspace("activeSessionID", null);
      setWorkspace("defaultAI", { ...workspace.defaultAI, model: null });

      // 1. Modality router helpers recognize mediaModels entries
      assertTrue(hasConfiguredImageGenerationModel());
      assertTrue(hasConfiguredVideoGenerationModel());
      assertTrue(hasConfiguredSpeechModel());

      // 2. Internal LLM roles strictly exclude non-chat models even if present in prov.models
      assertTrue(isInternalLLMModel({ providerID: "ainode-gw", modelID: "gpt-5.4" }));
      assertTrue(!isInternalLLMModel({ providerID: "ainode-gw", modelID: "gpt-image-1" }));
      assertTrue(!isInternalLLMModel({ providerID: "ainode-gw", modelID: "alimt-image-translate" }));
      const internalIDs = getInternalLLMProviderModelGroups().flatMap((g) =>
        g.models.map((m) => m.fullKey)
      );
      assertDeepEqual(internalIDs, ["ainode-gw/gpt-5.4"]);

      // 3. Extracting form state merges both models and mediaModels without dropping any
      const form = extractCustomProviderFormState("ainode-gw");
      assertDeepEqual(
        form.models.map((m) => m.id).sort(),
        ["alimt-image-translate", "cosyvoice-v3-flash", "gpt-5.4", "gpt-image-1", "veo-3"]
      );

      // 4. Saving custom provider keeps media/translate models in mediaModels and selects chat model (not alphabetically first alimt-image-translate)
      hoxiAPI.setProviderAuth = async () => {};
      hoxiAPI.saveHoxiProviderConfig = async (_pid, cfg) => {
        capturedSaveCfg = cfg;
        return { ok: true, hoxiProviders: { "ainode-gw": cfg }, configPath: "test" };
      };
      hoxiAPI.listProviders = async () => ({
        all: [
          {
            id: "ainode-gw",
            name: "AINode",
            models: { "gpt-5.4": { id: "gpt-5.4", name: "GPT-5.4" } as any },
          },
        ],
        connected: ["ainode-gw"],
        default: { "ainode-gw": "gpt-5.4" },
      });
      hoxiAPI.getHoxiModelsConfig = async () => ({
        hoxiProviders: capturedSaveCfg ? { "ainode-gw": capturedSaveCfg } : {},
        openCodeProviders: [],
        configPath: "test",
      });
      hoxiAPI.getLocalCLIStatus = async () => ({ ok: true, tools: [] }) as any;
      hoxiAPI.getMediaCapabilities = async () => ({ ok: true } as any);

      // Put alimt-image-translate FIRST in input.models to prove it won't hijack active chat model
      const orderedDrafts = [
        ...form.models.filter((m) => m.id === "alimt-image-translate"),
        ...form.models.filter((m) => m.id !== "alimt-image-translate"),
      ];
      await saveCustomProvider({
        providerID: "ainode-gw",
        name: "AINode",
        npm: "@ai-sdk/openai-compatible",
        baseURL: "http://127.0.0.1:8080/v1",
        apiKey: "********",
        preserveExistingKey: true,
        models: orderedDrafts,
      });

      assertDeepEqual(Object.keys(capturedSaveCfg?.models || {}), ["gpt-5.4"]);
      assertDeepEqual(Object.keys(capturedSaveCfg?.mediaModels || {}).sort(), [
        "alimt-image-translate",
        "cosyvoice-v3-flash",
        "gpt-image-1",
        "veo-3",
      ]);
      assertDeepEqual(workspace.selectedModel, {
        providerID: "ainode-gw",
        modelID: "gpt-5.4",
      });
    } finally {
      hoxiAPI.saveHoxiProviderConfig = origSaveCfg;
      hoxiAPI.setProviderAuth = origSetAuth;
      hoxiAPI.listProviders = origListProv;
      hoxiAPI.getHoxiModelsConfig = origGetModelsCfg;
      hoxiAPI.getLocalCLIStatus = origGetCLI;
      hoxiAPI.getMediaCapabilities = origGetMediaCaps;
      setWorkspace("providers", prevProviders);
      setWorkspace("hoxiProviders", prevHoxiProviders);
      setWorkspace("connectedProviders", prevConnected);
      setWorkspace("activeSessionID", prevActiveSid);
      setWorkspace("defaultAI", prevDefaultAI);
    }
  });
});
