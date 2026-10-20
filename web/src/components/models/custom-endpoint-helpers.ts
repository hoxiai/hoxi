// web/src/components/models/custom-endpoint-helpers.ts
import {
  isCustomOrConfiguredProvider,
  isHoxiCustomProvider,
  isImportedOpenCodeProvider,
  MASKED_SECRET,
  workspace,
  type CustomProviderModelDraft,
} from "../../store/workspace";

export const PROVIDER_VARIANTS = [
  { value: "@ai-sdk/openai-compatible", label: "OpenAI Compatible (/v1/chat/completions)" },
  { value: "@ai-sdk/openai", label: "OpenAI Responses (/v1/responses)" },
  { value: "@ai-sdk/anthropic", label: "Anthropic Messages (/v1/messages)" },
] as const;

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
  reasoning?: boolean;
  modalities?: { input?: string[] };
  capabilities?: {
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
    const rtModel = runtimeProv?.models?.[mID];
    const reasoning = Boolean(
      mVal.reasoning ??
        mVal.capabilities?.reasoning ??
        rtModel?.capabilities?.reasoning ??
        rtModel?.reasoning
    );
    const image = Boolean(
      (Array.isArray(mVal.modalities?.input) && mVal.modalities.input.includes("image")) ||
        mVal.capabilities?.input?.image ||
        rtModel?.capabilities?.input?.image
    );
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
    npmPkg: cfgProv.npm || runtimeProv?.npm || "@ai-sdk/openai-compatible",
    baseURL: cfgProv?.options?.baseURL || runtimeProv?.options?.baseURL || "",
    apiKey,
    preserveExistingKey,
    models: loadedModels,
    headers: loadedHeaders,
  };
}
