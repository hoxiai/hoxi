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
  type CustomProviderModelDraft,
} from "../../store/workspace";

export { inferProviderNpmFromEndpoint };

export interface ModelProbeResult {
  status: "testing" | "ok" | "error";
  latencyMs?: number;
  error?: string;
}

/** Default capability guess for a model fetched from a provider's model list. */
export function guessFetchedModelDraft(rm: { id: string; name: string }): CustomProviderModelDraft {
  const cleanID = rm.id.trim().replace(/^models\/(?=gemini|imagen|gemma|veo|embedding|text-embedding)/i, "");
  if (inferDedicatedMediaModality(cleanID) === "image") {
    return {
      id: cleanID,
      name: rm.name || cleanID,
      reasoning: false,
      image: supportsMultimodalImageEditModelID(cleanID),
    };
  }
  const isNonChat = isNonChatUtilityModelID(cleanID);
  return {
    id: cleanID,
    name: rm.name || cleanID,
    reasoning: !isNonChat && !isNonThinkingGeminiModelID(cleanID),
    image: !isNonChat,
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
  attachment?: boolean;
  reasoning?: boolean;
  modalities?: { input?: string[] };
  capabilities?: {
    attachment?: boolean;
    reasoning?: boolean;
    input?: { image?: boolean };
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

  // 1. Hoxi's own providers from .hoxi/models.json first (editable)
  const hoxiProviders = (workspace.hoxiProviders ?? {}) as Record<string, RawConfigProviderEntry>;
  for (const [id, cfg] of Object.entries(hoxiProviders)) {
    if (cfg && typeof cfg === "object") {
      seen.add(id);
      out.push({ id, name: cfg.name || id, imported: false });
    }
  }

  // 2. Imported OpenCode providers (read-only)
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
          mVal.attachment ||
            mVal.capabilities?.attachment ||
            (Array.isArray(mVal.modalities?.input) && mVal.modalities.input.includes("image")) ||
            mVal.capabilities?.input?.image ||
            rtModel?.capabilities?.input?.image ||
            rtModel?.attachment
        )
      : guessed.image;
    loadedModels.push({
      id: mID,
      name: mVal.name || rtModel?.name || mID,
      reasoning,
      image,
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
