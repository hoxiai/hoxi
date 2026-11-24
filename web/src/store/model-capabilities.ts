// web/src/store/model-capabilities.ts
// Unified standard model capability vocabulary, business slots (ModelSlot),
// backward-compatible modality normalization, and strict slot filtering.

export const MODEL_CAPABILITIES = [
  "chat",
  "vision",
  "text_translate",
  "image_generate",
  "image_edit",
  "image_translate",
  "video",
  "tts",
  "asr",
  "embedding",
  "rerank",
  "moderation",
] as const;

export type ModelCapability = (typeof MODEL_CAPABILITIES)[number];

export const IMAGE_TRANSLATE_OUTPUT_MODES = ["raster", "editable_scene"] as const;
export type ImageTranslateOutputMode = (typeof IMAGE_TRANSLATE_OUTPUT_MODES)[number];

export interface ImageTranslateCapability {
  output_modes?: ImageTranslateOutputMode[];
  outputModes?: ImageTranslateOutputMode[];
}

export const MODEL_SLOTS = [
  "chat",
  "vision",
  "text_translate",
  "image_generate",
  "image_edit",
  "image_translate_raster",
  "image_translate_editor",
  "video",
  "tts",
  "asr",
  "embedding",
  "rerank",
] as const;

export type ModelSlot = (typeof MODEL_SLOTS)[number];

export interface ModelCapabilityInput {
  modelID: string;
  modality?: string | null;
  category?: string | null;
  /** Standard gateway /v1/models string array, or undefined when omitted. */
  capabilities?: readonly string[] | null;
  imageTranslate?: {
    output_modes?: readonly string[];
    outputModes?: readonly string[];
  } | null;
  imageTranslateOutputModes?: readonly string[] | null;
  inputModalities?: readonly string[] | null;
  outputModalities?: readonly string[] | null;
  attachment?: boolean | null;
  supportsImageInput?: boolean | null;
}

export interface NormalizedModelCapabilities {
  capabilities: ModelCapability[];
  imageTranslate?: { outputModes: ImageTranslateOutputMode[] };
}

const CAPABILITY_SET = new Set<string>(MODEL_CAPABILITIES);
const TRANSLATE_MODE_SET = new Set<string>(IMAGE_TRANSLATE_OUTPUT_MODES);

const GEMINI_IMAGE_RE =
  /nano-banana|gemini[\w./-]*(?:-image|_image|\/image|image-generation|image-preview)/i;
const IMAGEN_GEN_ONLY_RE = /imagen[\w./-]*-generate/i;
const IMAGE_EDIT_MODEL_RE =
  /nano-banana|gemini[\w./-]*(?:-image|_image|\/image|image-generation|image-preview)|gpt-image|dall-e-2|imagen|qwen-image|kontext|inpaint|flux-fill|seedream|-edit|_edit|gpt-4o-image|chatgpt-image/i;
const IMAGE_GEN_MODEL_RE =
  /dall-e|gpt-image|chatgpt-image|gpt-4o-image|imagen|flux-|flux\.|flux\/|midjourney|stable-diffusion|sdxl|ideogram|recraft|cogview|seedream|kolors|hunyuan-image|qwen-image|wanx-v1|grok-imagine/i;
const VIDEO_MODEL_RE =
  /seedance|kling|veo-|veo2|veo3|sora|wanx|wan2|cogvideo|hailuo|luma-|runway|vidu/i;
const TTS_MODEL_RE = /tts|cosyvoice|speech|elevenlabs|fishaudio|suno|udio/i;
const ASR_MODEL_RE = /asr|whisper|paraformer|fun-asr|sensevoice|transcribe/i;
const EMBEDDING_MODEL_RE = /embedding|embed|bge-/i;
const PURE_TEXT_MODEL_RE =
  /deepseek-chat|deepseek-reasoner|deepseek-r1|deepseek-v2|deepseek-v3|qwen-mt-|qwen-coder|qwen2\.5-coder|qwen3-coder|deepseek-coder|codestral|o1-mini|gpt-3\.5|text-davinci/i;
const VISION_CHAT_MODEL_RE =
  /vision|-vl|_vl|\/vl|vl-|omni|gpt-4o|chatgpt-4o|gpt-4\.1|gpt-4-turbo|gpt-5|gpt-6|claude-3|claude-4|claude-sonnet|claude-opus|claude-haiku|gemini|qwen3[.-][568]|qwen4|pixtral|llava|internvl|glm-4(?:\.[56])?v|grok-[34]|(?:^|\/)o[34](?:\b|[-_])/i;

export function isKnownPureTextModelID(modelID?: string | null): boolean {
  return PURE_TEXT_MODEL_RE.test((modelID || "").trim());
}

function matchesImageEditHeuristic(id: string): boolean {
  if (IMAGEN_GEN_ONLY_RE.test(id) && !id.includes("edit") && !id.includes("inpaint")) {
    return false;
  }
  return IMAGE_EDIT_MODEL_RE.test(id);
}

export function isExcludedUtilityModel(modelID?: string | null, modality?: string | null): boolean {
  const mod = (modality || "").trim().toLowerCase();
  if (mod === "crawl" || mod === "crawler" || mod === "utility" || mod === "none") {
    return true;
  }
  let id = (modelID || "").trim().toLowerCase();
  if (!id) return false;
  const slash = id.lastIndexOf("/");
  if (slash >= 0) id = id.slice(slash + 1);
  return (
    id.startsWith("collector-") ||
    id === "collector" ||
    id.includes("codex-auto-review") ||
    id.startsWith("crawl-") ||
    id === "crawl"
  );
}

function isEditableImageTranslateID(lowerID: string): boolean {
  return lowerID.includes("alimt-image-translate") || lowerID.includes("image-translate-editor");
}

function isRasterImageTranslateID(lowerID: string): boolean {
  return (
    lowerID.includes("mt-image") ||
    lowerID.includes("image-translate") ||
    lowerID.includes("image_translate")
  );
}

function hasLowerItem(list: readonly string[] | null | undefined, target: string): boolean {
  if (!Array.isArray(list)) return false;
  const want = target.toLowerCase();
  return list.some((s) => String(s || "").trim().toLowerCase() === want);
}

function canonicalizeCaps(set: Set<ModelCapability>): ModelCapability[] {
  return MODEL_CAPABILITIES.filter((c) => set.has(c));
}

function normalizeOutputModes(
  explicit: readonly string[] | null | undefined,
  lowerID: string,
  mod: string
): ImageTranslateOutputMode[] {
  const seen = new Set<ImageTranslateOutputMode>();
  if (Array.isArray(explicit)) {
    for (const raw of explicit) {
      const m = String(raw || "").trim().toLowerCase();
      if (TRANSLATE_MODE_SET.has(m)) {
        seen.add(m as ImageTranslateOutputMode);
      }
    }
  }
  if (seen.size === 0) {
    if (mod === "translate_image" || isEditableImageTranslateID(lowerID)) {
      seen.add("raster");
      seen.add("editable_scene");
    } else {
      seen.add("raster");
    }
  }
  return IMAGE_TRANSLATE_OUTPUT_MODES.filter((m) => seen.has(m));
}

/**
 * Normalizes a model's capabilities from explicit gateway `capabilities`
 * or backward-compatible `modality` / `category` / model ID heuristics.
 */
export function normalizeModelCapabilities(
  input: ModelCapabilityInput
): NormalizedModelCapabilities {
  const id = (input.modelID || "").trim().toLowerCase();
  const mod = (input.modality || "").trim().toLowerCase();
  const cat = (input.category || "").trim().toLowerCase();
  const rawExplicitModes =
    input.imageTranslateOutputModes ??
    input.imageTranslate?.output_modes ??
    input.imageTranslate?.outputModes ??
    null;

  if (Array.isArray(input.capabilities)) {
    const set = new Set<ModelCapability>();
    for (const raw of input.capabilities) {
      const c = String(raw || "").trim().toLowerCase();
      if (CAPABILITY_SET.has(c)) {
        set.add(c as ModelCapability);
      }
    }
    const capabilities = canonicalizeCaps(set);
    return {
      capabilities,
      ...(set.has("image_translate")
        ? { imageTranslate: { outputModes: normalizeOutputModes(rawExplicitModes, id, mod) } }
        : {}),
    };
  }

  if (isExcludedUtilityModel(id, mod) || cat === "utility" || cat === "crawl") {
    return { capabilities: [] };
  }

  // Backward-compatible modality rules
  switch (mod) {
    case "translate_image":
      return {
        capabilities: ["image_translate"],
        imageTranslate: { outputModes: normalizeOutputModes(rawExplicitModes, id, mod) },
      };
    case "translate_image_task":
      return {
        capabilities: ["image_translate"],
        imageTranslate: {
          outputModes: normalizeOutputModes(
            rawExplicitModes && rawExplicitModes.length > 0 ? rawExplicitModes : ["raster"],
            id,
            mod
          ),
        },
      };
    case "image": {
      const set = new Set<ModelCapability>(["image_generate"]);
      if (
        matchesImageEditHeuristic(id) ||
        Boolean(input.supportsImageInput) ||
        hasLowerItem(input.inputModalities, "image")
      ) {
        set.add("image_edit");
      }
      return { capabilities: canonicalizeCaps(set) };
    }
    case "video":
      return { capabilities: ["video"] };
    case "tts":
    case "audio":
      return { capabilities: [ASR_MODEL_RE.test(id) ? "asr" : "tts"] };
    case "asr":
      return { capabilities: ["asr"] };
    case "embedding":
      return { capabilities: ["embedding"] };
    case "rerank":
      return { capabilities: ["rerank"] };
    case "moderation":
      return { capabilities: ["moderation"] };
    case "vision":
      return { capabilities: ["chat", "vision", "text_translate"] };
    case "text":
      return { capabilities: ["chat", "text_translate"] };
  }

  if (cat === "image_translate" || isEditableImageTranslateID(id) || isRasterImageTranslateID(id)) {
    return {
      capabilities: ["image_translate"],
      imageTranslate: { outputModes: normalizeOutputModes(rawExplicitModes, id, mod) },
    };
  }
  if (cat === "rerank" || id.includes("rerank")) {
    return { capabilities: ["rerank"] };
  }
  if (cat === "moderation" || id.includes("moderation")) {
    return { capabilities: ["moderation"] };
  }
  if (cat === "embedding" || EMBEDDING_MODEL_RE.test(id)) {
    return { capabilities: ["embedding"] };
  }
  if (cat === "asr" || ASR_MODEL_RE.test(id)) {
    return { capabilities: ["asr"] };
  }
  if (cat === "tts" || cat === "audio" || TTS_MODEL_RE.test(id) || hasLowerItem(input.outputModalities, "audio")) {
    return { capabilities: ["tts"] };
  }
  if (cat === "video" || VIDEO_MODEL_RE.test(id) || hasLowerItem(input.outputModalities, "video")) {
    return { capabilities: ["video"] };
  }
  if (
    cat === "image" ||
    GEMINI_IMAGE_RE.test(id) ||
    IMAGE_GEN_MODEL_RE.test(id) ||
    hasLowerItem(input.outputModalities, "image")
  ) {
    const set = new Set<ModelCapability>(["image_generate"]);
    if (
      matchesImageEditHeuristic(id) ||
      Boolean(input.supportsImageInput) ||
      hasLowerItem(input.inputModalities, "image")
    ) {
      set.add("image_edit");
    }
    return { capabilities: canonicalizeCaps(set) };
  }

  // Conversational models: strictly exclude pure-text models from vision.
  const set = new Set<ModelCapability>(["chat", "text_translate"]);
  if (input.attachment === false) {
    return { capabilities: canonicalizeCaps(set) };
  }
  if (Array.isArray(input.inputModalities) && input.inputModalities.length > 0) {
    if (hasLowerItem(input.inputModalities, "image") && !PURE_TEXT_MODEL_RE.test(id)) {
      set.add("vision");
    }
    return { capabilities: canonicalizeCaps(set) };
  }
  if (input.supportsImageInput && !PURE_TEXT_MODEL_RE.test(id)) {
    set.add("vision");
    return { capabilities: canonicalizeCaps(set) };
  }
  if (!PURE_TEXT_MODEL_RE.test(id) && VISION_CHAT_MODEL_RE.test(id)) {
    set.add("vision");
  }
  return { capabilities: canonicalizeCaps(set) };
}

export function matchesModelSlot(
  caps: NormalizedModelCapabilities,
  slot: ModelSlot
): boolean {
  const has = (c: ModelCapability) => caps.capabilities.includes(c);
  switch (slot) {
    case "chat":
      return has("chat");
    case "vision":
      return has("vision");
    case "text_translate":
      return has("text_translate");
    case "image_generate":
      return has("image_generate");
    case "image_edit":
      return has("image_edit");
    case "image_translate_raster":
      return (
        has("image_translate") &&
        Boolean(caps.imageTranslate?.outputModes?.includes("raster"))
      );
    case "image_translate_editor":
      return (
        has("image_translate") &&
        Boolean(caps.imageTranslate?.outputModes?.includes("editable_scene"))
      );
    case "video":
      return has("video");
    case "tts":
      return has("tts");
    case "asr":
      return has("asr");
    case "embedding":
      return has("embedding");
    case "rerank":
      return has("rerank");
    default:
      return false;
  }
}

export function getSlotsForCapabilities(
  caps: NormalizedModelCapabilities
): ModelSlot[] {
  return MODEL_SLOTS.filter((slot) => matchesModelSlot(caps, slot));
}

export function normalizeStoredModelCapabilities(
  modelID: string,
  mObj?: Record<string, any> | null
): NormalizedModelCapabilities {
  if (!mObj || typeof mObj !== "object") {
    return normalizeModelCapabilities({ modelID });
  }
  const explicitCapTags = Array.isArray(mObj.capabilityTags)
    ? mObj.capabilityTags
    : Array.isArray(mObj.capabilities)
    ? mObj.capabilities
    : null;
  const imgTransObj = mObj.imageTranslate || mObj.image_translate || null;
  const explicitImgTransModes =
    imgTransObj?.outputModes ||
    imgTransObj?.output_modes ||
    mObj.imageTranslateOutputModes ||
    mObj.image_translate_output_modes ||
    null;

  let attachment: boolean | null = null;
  if (typeof mObj.attachment === "boolean") {
    attachment = mObj.attachment;
  } else if (mObj.capabilities && !Array.isArray(mObj.capabilities)) {
    if (typeof mObj.capabilities.input?.image === "boolean") {
      attachment = mObj.capabilities.input.image;
    } else if (typeof mObj.capabilities.attachment === "boolean") {
      attachment = mObj.capabilities.attachment;
    }
  }

  return normalizeModelCapabilities({
    modelID,
    modality: typeof mObj.modality === "string" ? mObj.modality : undefined,
    category: typeof mObj.category === "string" ? mObj.category : undefined,
    capabilities: explicitCapTags,
    imageTranslateOutputModes: Array.isArray(explicitImgTransModes)
      ? explicitImgTransModes
      : undefined,
    inputModalities: Array.isArray(mObj.modalities?.input) ? mObj.modalities.input : undefined,
    outputModalities: Array.isArray(mObj.modalities?.output) ? mObj.modalities.output : undefined,
    attachment,
    supportsImageInput: Boolean(
      mObj.supportsImageInput ||
        (!Array.isArray(mObj.capabilities) && mObj.capabilities?.input?.image === true)
    ),
  });
}

export function filterModelsForSlot<T extends { modelID: string; capabilities: NormalizedModelCapabilities }>(
  models: readonly T[],
  slot: ModelSlot
): T[] {
  return models.filter((item) => matchesModelSlot(item.capabilities, slot));
}
