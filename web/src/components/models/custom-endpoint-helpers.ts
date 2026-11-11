import {
  inferDedicatedMediaModality,
  inferProviderNpmFromEndpoint,
  isCustomOrConfiguredProvider,
  isHoxiCustomProvider,
  isImportedOpenCodeProvider,
  isNonChatUtilityModelID,
  isNonThinkingGeminiModelID,
  MASKED_SECRET,
  supportsMultimodalImageEditModelID,
  workspace,
  type CustomModelCategory,
  type CustomProviderModelDraft,
} from "../../store/workspace";

export { inferProviderNpmFromEndpoint };

export interface ModelProbeResult {
  status: "testing" | "ok" | "error";
  latencyMs?: number;
  error?: string;
}

function inferModelCategoryFromID(cleanID: string): CustomModelCategory {
  if (inferDedicatedMediaModality(cleanID) === "image") return "image";
  if (/(whisper|sensevoice|paraformer|transcribe|tts|speech)/i.test(cleanID)) return "audio";
  if (/(embedding|embed|bge-|jina-embeddings)/i.test(cleanID)) return "embedding";
  return "chat";
}

/** Default capability guess for a model fetched from a provider's model list. */
export function guessFetchedModelDraft(rm: { id: string; name: string }): CustomProviderModelDraft {
  const cleanID = rm.id.trim().replace(/^models\/(?=gemini|imagen|gemma|veo|embedding|text-embedding)/i, "");
  const category = inferModelCategoryFromID(cleanID);
  if (category === "image") {
    return {
      id: cleanID,
      name: rm.name || cleanID,
      category: "image",
      reasoning: false,
      image: supportsMultimodalImageEditModelID(cleanID),
      toolCall: false,
      pdf: false,
      media: false,
    };
  }
  if (category !== "chat" || isNonChatUtilityModelID(cleanID)) {
    return {
      id: cleanID,
      name: rm.name || cleanID,
      category,
      reasoning: false,
      image: false,
      toolCall: false,
      pdf: false,
      media: false,
    };
  }
  const lower = cleanID.toLowerCase();
  const isGemini = lower.includes("gemini");
  const isClaudePdf =
    lower.includes("claude-3-5") ||
    lower.includes("claude-3-7") ||
    lower.includes("claude-sonnet-4") ||
    lower.includes("claude-opus-4") ||
    lower.includes("claude-haiku-4") ||
    lower.includes("claude-4");
  const isOpenAIPdf = /^(gpt-4o|chatgpt-4o|gpt-4\.1|gpt-5|o3|o4)/i.test(lower);
  const supportsMedia =
    isGemini || lower.includes("omni") || lower.includes("qwen2.5-vl") || lower.includes("qwen3-vl");

  return {
    id: cleanID,
    name: rm.name || cleanID,
    category: "chat",
    reasoning: !isNonThinkingGeminiModelID(cleanID),
    image: true,
    toolCall: true,
    pdf: isGemini || isClaudePdf || isOpenAIPdf,
    media: supportsMedia,
  };
}

export const PROVIDER_VARIANTS = [
  { value: "@ai-sdk/openai-compatible", label: "OpenAI Compatible (/v1/chat/completions)" },
  { value: "@ai-sdk/openai", label: "OpenAI Responses (/v1/responses)" },
  { value: "@ai-sdk/anthropic", label: "Anthropic Messages (/v1/messages)" },
  { value: "@ai-sdk/google", label: "Google Gemini (/v1beta/models)" },
] as const;

export function defaultProviderNpm(providerID?: string | null): string {
  const pid = (providerID || "").trim().toLowerCase();
  if (pid === "google" || pid === "gemini" || pid.startsWith("gemini-") || pid.startsWith("google-")) {
    return "@ai-sdk/google";
  }
  if (pid === "anthropic" || pid.startsWith("anthropic-")) return "@ai-sdk/anthropic";
  return "@ai-sdk/openai-compatible";
}

export interface CustomProviderFormState {
  providerID: string;
  displayName: string;
  npmPkg: string;
  baseURL: string;
  apiKey: string;
  preserveExistingKey: boolean;
  models: CustomProviderModelDraft[];
  headers: Array<{ key: string; value: string }>;
}

interface RawConfigModelEntry {
  name?: string;
  category?: CustomModelCategory;
  tool_call?: boolean;
  attachment?: boolean;
  reasoning?: boolean;
  modalities?: { input?: string[]; output?: string[] };
  capabilities?: {
    toolcall?: boolean;
    attachment?: boolean;
    reasoning?: boolean;
    input?: { image?: boolean; pdf?: boolean; audio?: boolean; video?: boolean };
  };
}

interface RawConfigProviderEntry {
  name?: string;
  npm?: string;
  options?: {
    baseURL?: string;
    apiKey?: string;
    headers?: Record<string, unknown>;
  };
  models?: Record<string, RawConfigModelEntry>;
}

export function listExistingCustomProviders(): Array<{
  id: string;
  name: string;
  imported: boolean;
}> {
  const out: Array<{ id: string; name: string; imported: boolean }> = [];
  const seen = new Set<string>();

  const hoxiProviders = (workspace.hoxiProviders ?? {}) as Record<string, RawConfigProviderEntry>;
  for (const [id, cfg] of Object.entries(hoxiProviders)) {
    if (cfg && typeof cfg === "object") {
      seen.add(id);
      out.push({ id, name: cfg.name || id, imported: false });
    }
  }

  const cfgProviders = (workspace.globalConfig?.provider ?? {}) as Record<
    string,
    RawConfigProviderEntry
  >;
  for (const [id, cfg] of Object.entries(cfgProviders)) {
    if (seen.has(id)) continue;
    if (cfg && typeof cfg === "object" && isCustomOrConfiguredProvider(id)) {
      out.push({ id, name: cfg.name || id, imported: isImportedOpenCodeProvider(id) });
    }
  }
  return out;
}

export function extractCustomProviderFormState(editID: string | null): CustomProviderFormState {
  if (!editID) {
    return {
      providerID: "",
      displayName: "",
      npmPkg: "@ai-sdk/openai-compatible",
      baseURL: "",
      apiKey: "",
      preserveExistingKey: false,
      models: [],
      headers: [],
    };
  }

  const isHoxi = isHoxiCustomProvider(editID);
  const cfgProv = (
    isHoxi
      ? workspace.hoxiProviders?.[editID]
      : workspace.globalConfig?.provider?.[editID] ?? {}
  ) as RawConfigProviderEntry;
  const runtimeProv = workspace.providers.find((p) => p.id === editID);
  const hasConnectedAuth =
    workspace.connectedProviders.includes(editID) ||
    Boolean(runtimeProv?.key) ||
    Boolean(cfgProv?.options?.apiKey);

  let apiKey = "";
  let preserveExistingKey = false;
  if (typeof cfgProv?.options?.apiKey === "string" && cfgProv.options.apiKey.startsWith("{env:")) {
    apiKey = cfgProv.options.apiKey;
  } else if (hasConnectedAuth) {
    apiKey = MASKED_SECRET;
    preserveExistingKey = true;
  }

  const rawModels = (cfgProv.models || runtimeProv?.models || {}) as Record<
    string,
    RawConfigModelEntry
  >;
  const loadedModels: CustomProviderModelDraft[] = [];
  for (const [mID, mVal] of Object.entries(rawModels)) {
    if (!mVal || typeof mVal !== "object") continue;
    const rtModel = runtimeProv?.models?.[mID] as RawConfigModelEntry | undefined;
    const guessed = guessFetchedModelDraft({ id: mID, name: mVal.name || rtModel?.name || mID });
    const category: CustomModelCategory =
      mVal.category ||
      (Array.isArray(mVal.modalities?.output) && mVal.modalities.output.includes("image")
        ? "image"
        : guessed.category || "chat");

    const hasExplicitReasoning =
      mVal.reasoning !== undefined ||
      mVal.capabilities?.reasoning !== undefined ||
      rtModel?.capabilities?.reasoning !== undefined ||
      rtModel?.reasoning !== undefined;
    const reasoning = hasExplicitReasoning
      ? Boolean(
          mVal.reasoning ??
            mVal.capabilities?.reasoning ??
            rtModel?.capabilities?.reasoning ??
            rtModel?.reasoning
        )
      : guessed.reasoning;

    const hasExplicitImage =
      mVal.attachment !== undefined ||
      mVal.capabilities?.attachment !== undefined ||
      Array.isArray(mVal.modalities?.input) ||
      mVal.capabilities?.input?.image !== undefined ||
      rtModel?.capabilities?.input?.image !== undefined ||
      rtModel?.attachment !== undefined;
    const image = hasExplicitImage
      ? Boolean(
          mVal.capabilities?.input?.image ??
            (Array.isArray(mVal.modalities?.input) ? mVal.modalities.input.includes("image") : undefined) ??
            mVal.attachment ??
            mVal.capabilities?.attachment ??
            rtModel?.capabilities?.input?.image ??
            rtModel?.attachment
        )
      : guessed.image;

    const hasExplicitTools =
      mVal.tool_call !== undefined ||
      mVal.capabilities?.toolcall !== undefined ||
      rtModel?.capabilities?.toolcall !== undefined ||
      rtModel?.tool_call !== undefined;
    const toolCall = hasExplicitTools
      ? Boolean(
          mVal.tool_call ??
            mVal.capabilities?.toolcall ??
            rtModel?.capabilities?.toolcall ??
            rtModel?.tool_call
        )
      : guessed.toolCall;

    const pdf =
      mVal.capabilities?.input?.pdf !== undefined
        ? Boolean(mVal.capabilities.input.pdf)
        : Array.isArray(mVal.modalities?.input)
        ? mVal.modalities.input.includes("pdf")
        : Boolean(guessed.pdf);

    const media =
      mVal.capabilities?.input?.audio !== undefined || mVal.capabilities?.input?.video !== undefined
        ? Boolean(mVal.capabilities?.input?.audio || mVal.capabilities?.input?.video)
        : Array.isArray(mVal.modalities?.input)
        ? mVal.modalities.input.includes("audio") || mVal.modalities.input.includes("video")
        : Boolean(guessed.media);

    loadedModels.push({
      id: mID,
      name: mVal.name || rtModel?.name || mID,
      category,
      reasoning,
      image,
      toolCall,
      pdf,
      media,
    });
  }

  const rawHeaders = (cfgProv?.options?.headers || runtimeProv?.options?.headers || {}) as Record<
    string,
    unknown
  >;
  const loadedHeaders = Object.entries(rawHeaders)
    .filter(([, v]) => typeof v === "string")
    .map(([k, v]) => ({ key: k, value: String(v) }));

  return {
    providerID: editID,
    displayName: cfgProv.name || runtimeProv?.name || editID,
    npmPkg: cfgProv.npm || runtimeProv?.npm || defaultProviderNpm(editID),
    baseURL: cfgProv?.options?.baseURL || runtimeProv?.options?.baseURL || "",
    apiKey,
    preserveExistingKey,
    models: loadedModels,
    headers: loadedHeaders,
  };
}
