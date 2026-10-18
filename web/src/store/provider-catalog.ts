// web/src/store/provider-catalog.ts
import { t } from "../i18n";
import type { ProviderInfo } from "../services/api";
import { workspace } from "./workspace";
import {
  isCustomOrConfiguredProvider,
  isHoxiCustomProvider,
  isImportedOpenCodeProvider,
} from "./workspace-providers";

export interface FlattenedModel {
  providerID: string;
  providerName: string;
  modelID: string;
  modelName: string;
  connected: boolean;
  isCustom: boolean;
  source?: string;
  baseURL?: string;
  reasoning: boolean;
  image: boolean;
  toolcall: boolean;
  contextLimit?: number;
  outputLimit?: number;
  inputCost?: number;
  outputCost?: number;
  free: boolean;
}

export interface ModelSourceInfo {
  label: string;
  configHint?: string;
  isImported: boolean;
}

export interface ConnectedProviderGroup {
  providerID: string;
  providerName: string;
  models: Array<{ id: string; name: string; fullKey: string; free: boolean }>;
}

export function getModelSourceInfo(model: FlattenedModel): ModelSourceInfo {
  const pid = model.providerID;
  if (pid === "antigravity") {
    return {
      label: t("timeline.runtimeAntigravityLocal"),
      configHint: "agy --sandbox",
      isImported: false,
    };
  }
  if (pid === "claude-code" && !isHoxiCustomProvider(pid)) {
    return {
      label: t("timeline.runtimeClaudeCodeLocal"),
      configHint: "claude",
      isImported: false,
    };
  }
  if (isHoxiCustomProvider(pid)) {
    return {
      label: t("models.sourceHoxi"),
      configHint: ".hoxi/models.json",
      isImported: false,
    };
  }
  if (isImportedOpenCodeProvider(pid)) {
    return {
      label: t("models.sourceOpenCode"),
      configHint: "opencode",
      isImported: true,
    };
  }
  if (model.isCustom) {
    return {
      label: t("models.sourceCustom"),
      isImported: false,
    };
  }
  if (pid === "opencode" && model.free && !workspace.connectedProviders.includes("opencode")) {
    return {
      label: t("models.sourceBuiltInFree"),
      isImported: false,
    };
  }
  if (model.source === "env") {
    return { label: t("models.sourceEnv"), isImported: false };
  }
  if (model.source === "config") {
    return { label: t("models.sourceConfig"), isImported: false };
  }
  return { label: t("models.sourceApi"), isImported: false };
}

export function formatTokenLimit(n?: number): string | null {
  if (!n || n <= 0) return null;
  if (n >= 1_000_000) {
    const m = n / 1_000_000;
    return `${Number.isInteger(m) ? m : m.toFixed(1)}M`;
  }
  if (n >= 1_000) return `${Math.round(n / 1_000)}k`;
  return String(n);
}

export function formatPricePerMillion(cost?: number): string | null {
  if (cost === undefined || cost === null) return null;
  if (cost === 0) return "$0";
  const val = cost < 0.001 ? cost * 1_000_000 : cost;
  return `$${val < 1 ? val.toFixed(2) : val.toFixed(2).replace(/\.00$/, "")}/M`;
}

export function isModelFree(
  providerID: string,
  modelID: string,
  mInfo?: Record<string, any> | null
): boolean {
  if (providerID !== "opencode") return false;
  const inCost = mInfo?.cost?.input;
  return !mInfo?.cost || inCost === 0 || modelID.endsWith("-free");
}

export function extractFlattenedModels(
  providers: ProviderInfo[],
  connectedIds: Set<string>,
  globalConfig: Record<string, any>
): FlattenedModel[] {
  const disabledSet = new Set<string>(
    Array.isArray(globalConfig?.disabled_providers) ? globalConfig.disabled_providers : []
  );
  const cfgProviders = globalConfig?.provider ?? {};
  const out: FlattenedModel[] = [];

  for (const p of providers) {
    if (disabledSet.has(p.id)) continue;
    const isCustom = isCustomOrConfiguredProvider(p);
    const baseConn = connectedIds.has(p.id) || Boolean(cfgProviders[p.id]);
    const baseURL =
      workspace.hoxiProviders?.[p.id]?.options?.baseURL ||
      cfgProviders?.[p.id]?.options?.baseURL ||
      p.options?.baseURL;

    for (const [mID, mInfo] of Object.entries(p.models || {})) {
      if (!mInfo || typeof mInfo !== "object") continue;
      const cfgModel = cfgProviders?.[p.id]?.models?.[mID];
      const reasoning = Boolean(
        mInfo.capabilities?.reasoning ?? mInfo.reasoning ?? cfgModel?.reasoning
      );
      const image = Boolean(
        mInfo.capabilities?.input?.image ||
          (Array.isArray(mInfo.modalities?.input) && mInfo.modalities.input.includes("image")) ||
          (Array.isArray(cfgModel?.modalities?.input) &&
            cfgModel.modalities.input.includes("image"))
      );
      const toolcall = Boolean(mInfo.capabilities?.toolcall ?? mInfo.tool_call ?? true);
      const inCost = mInfo.cost?.input ?? cfgModel?.cost?.input;
      const outCost = mInfo.cost?.output ?? cfgModel?.cost?.output;
      const free = isModelFree(p.id, mID, mInfo);
      const isConn = baseConn || (p.id === "opencode" && free);

      out.push({
        providerID: p.id,
        providerName: p.name || p.id,
        modelID: mID,
        modelName: mInfo.name || cfgModel?.name || mID,
        connected: isConn,
        isCustom,
        source: p.source,
        baseURL,
        reasoning,
        image,
        toolcall,
        contextLimit: mInfo.limit?.context,
        outputLimit: mInfo.limit?.output,
        inputCost: inCost,
        outputCost: outCost,
        free,
      });
    }
  }
  return out;
}

export function rankModelSearch(models: FlattenedModel[], rawQuery: string): FlattenedModel[] {
  const q = rawQuery.trim().toLowerCase();
  if (!q) return models;
  const tokens = q.split(/\s+/).filter(Boolean);

  const scored: Array<{ item: FlattenedModel; score: number }> = [];
  for (const m of models) {
    const idLower = m.modelID.toLowerCase();
    const nameLower = m.modelName.toLowerCase();
    const provLower = `${m.providerName} ${m.providerID}`.toLowerCase();
    const freeTag = m.free ? `free ${t("models.freeModel").toLowerCase()}` : "";
    const haystack = `${idLower} ${nameLower} ${provLower} ${freeTag}`;

    if (!tokens.every((tok) => haystack.includes(tok))) continue;

    let score = 0;
    if (idLower === q || nameLower === q) score += 200;
    else if (idLower.startsWith(q) || nameLower.startsWith(q)) score += 120;
    else if (idLower.includes(q) || nameLower.includes(q)) score += 70;
    if (m.connected) score += 40;
    if (m.free) score += 25;
    if (m.isCustom) score += 20;

    scored.push({ item: m, score });
  }

  return scored
    .sort((a, b) => b.score - a.score || a.item.modelName.localeCompare(b.item.modelName))
    .map((s) => s.item);
}

export function getConnectedProviderModelGroups(): ConnectedProviderGroup[] {
  const connSet = new Set(workspace.connectedProviders);
  const disSet = new Set<string>(
    Array.isArray(workspace.globalConfig?.disabled_providers)
      ? workspace.globalConfig.disabled_providers
      : []
  );
  const cfgProviders = workspace.globalConfig?.provider ?? {};
  const groups: ConnectedProviderGroup[] = [];

  for (const p of workspace.providers) {
    if (disSet.has(p.id)) continue;
    const isConn = connSet.has(p.id) || Boolean(cfgProviders[p.id]) || p.id === "opencode";
    if (!isConn) continue;

    const models = Object.entries(p.models || {}).map(([mID, mInfo]) => ({
      id: mID,
      name: mInfo?.name || mID,
      fullKey: `${p.id}/${mID}`,
      free: isModelFree(p.id, mID, mInfo),
    }));

    if (models.length > 0) {
      groups.push({
        providerID: p.id,
        providerName: p.name || p.id,
        models,
      });
    }
  }

  return groups.sort((a, b) => {
    const aCustom = isCustomOrConfiguredProvider(a.providerID);
    const bCustom = isCustomOrConfiguredProvider(b.providerID);
    if (aCustom !== bCustom) return aCustom ? -1 : 1;
    return a.providerName.localeCompare(b.providerName);
  });
}
