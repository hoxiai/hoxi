// web/src/store/orchestrator-mode.ts
// Dual-model Orchestrator + Executor state, persistence, per-session overrides,
// and strict dispatch resolution (native HTTP API models only, never silent fallback).
import { createSignal } from "solid-js";
import { t } from "../i18n";
import {
  hoxiAPI,
  resolveRunnerRuntimeForProvider,
  type OrchestratorConfig,
  type PromptOrchestrator,
} from "../services/api";
import { AUTO_PROVIDER_ID } from "./auto-pool-defaults";
import {
  getInternalLLMProviderModelGroups,
  inferModelEffortCapabilities,
  resolveEffectiveModelEffort,
  type FlattenedModel,
} from "./provider-catalog";
import { parseModelString, sessionAIConfig, updateSessionAI } from "./session-ai";
import { lookupModelContextLimit } from "./model-limits";
import {
  ORCHESTRATOR_MODEL_ID,
  ORCHESTRATOR_PROVIDER_ID,
  readStorageJSON,
  VALID_REASONING_EFFORTS,
  workspace,
  writeStorageJSON,
  type OrchestratorOverride,
  type ReasoningEffort,
  type SelectedModel,
} from "./workspace-state";

export const ORCHESTRATOR_CONFIG_STORAGE_KEY = "hoxi.orchestrator-config:v1";

export interface EffectiveOrchestratorPair {
  orchestratorModel: string;
  orchestratorEffort: ReasoningEffort;
  executorModel: string;
  executorEffort: ReasoningEffort;
  hasOverride: boolean;
}

export interface ResolvedOrchestratorDispatch {
  model: SelectedModel;
  effort?: ReasoningEffort;
  orchestrator: PromptOrchestrator;
}

function normalizeEffortValue(raw?: string | null): ReasoningEffort {
  const clean = (raw || "").trim().toLowerCase();
  return VALID_REASONING_EFFORTS.includes(clean as ReasoningEffort)
    ? (clean as ReasoningEffort)
    : "";
}

function normalizeModelSpecValue(raw?: string | null): string {
  const clean = (raw || "").trim();
  if (!clean) return "";
  const parsed = parseModelString(clean);
  if (!parsed) return "";
  if (parsed.providerID === AUTO_PROVIDER_ID || parsed.providerID === ORCHESTRATOR_PROVIDER_ID) {
    return "";
  }
  return `${parsed.providerID}/${parsed.modelID}`;
}

function normalizeConfig(raw?: OrchestratorConfig | null): Required<OrchestratorConfig> {
  return {
    orchestratorModel: normalizeModelSpecValue(raw?.orchestratorModel),
    orchestratorEffort: normalizeEffortValue(raw?.orchestratorEffort),
    executorModel: normalizeModelSpecValue(raw?.executorModel),
    executorEffort: normalizeEffortValue(raw?.executorEffort),
  };
}

function loadCachedOrchestratorConfig(): Required<OrchestratorConfig> {
  const cached = readStorageJSON<OrchestratorConfig>(ORCHESTRATOR_CONFIG_STORAGE_KEY);
  return normalizeConfig(cached);
}

const [orchestratorConfigSignal, setOrchestratorConfigSignal] = createSignal<Required<OrchestratorConfig>>(
  loadCachedOrchestratorConfig()
);

export function orchestratorConfig(): Required<OrchestratorConfig> {
  return orchestratorConfigSignal();
}

export function isOrchestratorModel(model?: SelectedModel | null): boolean {
  return Boolean(model && model.providerID === ORCHESTRATOR_PROVIDER_ID);
}

export async function fetchOrchestratorConfig(): Promise<Required<OrchestratorConfig>> {
  try {
    const res = await hoxiAPI.getOrchestratorConfig();
    const norm = normalizeConfig(res?.config);
    setOrchestratorConfigSignal(norm);
    writeStorageJSON(ORCHESTRATOR_CONFIG_STORAGE_KEY, norm);
    return norm;
  } catch {
    return orchestratorConfigSignal();
  }
}

export async function updateOrchestratorConfig(
  patch: Partial<Record<keyof OrchestratorConfig, string | null>>
): Promise<Required<OrchestratorConfig>> {
  const res = await hoxiAPI.saveOrchestratorConfig(patch);
  const norm = normalizeConfig(res?.config);
  setOrchestratorConfigSignal(norm);
  writeStorageJSON(ORCHESTRATOR_CONFIG_STORAGE_KEY, norm);
  return norm;
}

export function getEffectiveOrchestratorPair(
  sessionID: string | null = workspace.activeSessionID
): EffectiveOrchestratorPair {
  const base = orchestratorConfigSignal();
  const ov = sessionAIConfig(sessionID).orchestratorOverride;
  const hasOverride = Boolean(
    ov &&
      (ov.orchestratorModel !== undefined ||
        ov.orchestratorEffort !== undefined ||
        ov.executorModel !== undefined ||
        ov.executorEffort !== undefined)
  );
  return {
    orchestratorModel: ov?.orchestratorModel ?? base.orchestratorModel,
    orchestratorEffort:
      ov?.orchestratorEffort !== undefined
        ? normalizeEffortValue(ov.orchestratorEffort)
        : normalizeEffortValue(base.orchestratorEffort),
    executorModel: ov?.executorModel ?? base.executorModel,
    executorEffort:
      ov?.executorEffort !== undefined
        ? normalizeEffortValue(ov.executorEffort)
        : normalizeEffortValue(base.executorEffort),
    hasOverride,
  };
}

export function updateSessionOrchestratorOverride(
  patch: Partial<OrchestratorOverride>,
  sessionID: string | null = workspace.activeSessionID
): void {
  const cur = sessionAIConfig(sessionID).orchestratorOverride || {};
  const next: OrchestratorOverride = { ...cur };
  if ("orchestratorModel" in patch) {
    const norm = normalizeModelSpecValue(patch.orchestratorModel);
    if (norm) next.orchestratorModel = norm;
    else delete next.orchestratorModel;
  }
  if ("orchestratorEffort" in patch) {
    if (patch.orchestratorEffort !== undefined) {
      next.orchestratorEffort = normalizeEffortValue(patch.orchestratorEffort);
    } else {
      delete next.orchestratorEffort;
    }
  }
  if ("executorModel" in patch) {
    const norm = normalizeModelSpecValue(patch.executorModel);
    if (norm) next.executorModel = norm;
    else delete next.executorModel;
  }
  if ("executorEffort" in patch) {
    if (patch.executorEffort !== undefined) {
      next.executorEffort = normalizeEffortValue(patch.executorEffort);
    } else {
      delete next.executorEffort;
    }
  }
  updateSessionAI(
    { orchestratorOverride: Object.keys(next).length > 0 ? next : undefined },
    sessionID
  );
}

export function clearSessionOrchestratorOverride(
  sessionID: string | null = workspace.activeSessionID
): void {
  updateSessionAI({ orchestratorOverride: undefined }, sessionID);
}

export function isOrchestratorConfigured(
  sessionID: string | null = workspace.activeSessionID
): boolean {
  const pair = getEffectiveOrchestratorPair(sessionID);
  return Boolean(pair.orchestratorModel && pair.executorModel);
}

export function isOrchestratorSamePair(
  sessionID: string | null = workspace.activeSessionID
): boolean {
  const pair = getEffectiveOrchestratorPair(sessionID);
  return Boolean(
    pair.orchestratorModel &&
      pair.executorModel &&
      pair.orchestratorModel === pair.executorModel &&
      pair.orchestratorEffort === pair.executorEffort
  );
}

export function formatOrchestratorModelLabel(fullKey?: string | null): string {
  const parsed = parseModelString(fullKey);
  if (!parsed) return "";
  const prov = workspace.providers.find((p) => p.id === parsed.providerID);
  const info = prov?.models?.[parsed.modelID];
  return info?.name || parsed.modelID;
}

export function peekOrchestratorConcreteModel(
  sessionID: string | null = workspace.activeSessionID,
  role: "orchestrator" | "executor" = "orchestrator"
): SelectedModel | null {
  const pair = getEffectiveOrchestratorPair(sessionID);
  const primaryKey = role === "executor" ? pair.executorModel : pair.orchestratorModel;
  const fallbackKey = role === "executor" ? pair.orchestratorModel : pair.executorModel;
  return parseModelString(primaryKey) || parseModelString(fallbackKey);
}

function isNativeAPIModelAvailable(model: SelectedModel): boolean {
  if (!model.providerID || !model.modelID) return false;
  if (
    model.providerID === AUTO_PROVIDER_ID ||
    model.providerID === ORCHESTRATOR_PROVIDER_ID ||
    resolveRunnerRuntimeForProvider(model.providerID, false, "native") !== "native"
  ) {
    return false;
  }
  const internalGroups = getInternalLLMProviderModelGroups();
  if (internalGroups.length > 0) {
    return internalGroups.some(
      (g) => g.providerID === model.providerID && g.models.some((m) => m.id === model.modelID)
    );
  }
  const prov = workspace.providers.find((p) => p.id === model.providerID);
  return Boolean(prov && prov.source !== "runner" && prov.models?.[model.modelID]);
}

export function resolveOrchestratorDispatchTarget(
  sessionID: string | null = workspace.activeSessionID,
  effortOverride?: ReasoningEffort
): ResolvedOrchestratorDispatch {
  const pair = getEffectiveOrchestratorPair(sessionID);
  const orch = parseModelString(pair.orchestratorModel);
  const exec = parseModelString(pair.executorModel);
  if (!orch || !exec) {
    throw new Error(t("models.orchestratorNotConfiguredError"));
  }
  if (!isNativeAPIModelAvailable(orch)) {
    throw new Error(
      t("models.orchestratorModelUnavailableError", {
        role: t("models.orchestratorModelTitle"),
        model: pair.orchestratorModel,
      })
    );
  }
  if (!isNativeAPIModelAvailable(exec)) {
    throw new Error(
      t("models.orchestratorModelUnavailableError", {
        role: t("models.executorModelTitle"),
        model: pair.executorModel,
      })
    );
  }
  const rawOrchEffort = effortOverride || pair.orchestratorEffort;
  const orchEffort = resolveEffectiveModelEffort(orch, rawOrchEffort);
  const execEffort = resolveEffectiveModelEffort(exec, pair.executorEffort);
  return {
    model: orch,
    effort: orchEffort,
    orchestrator: {
      executorProviderID: exec.providerID,
      executorModelID: exec.modelID,
      ...(execEffort ? { executorEffort: execEffort } : {}),
    },
  };
}

export function buildOrchestratorVirtualFlattenedModel(
  sessionID: string | null = workspace.activeSessionID
): FlattenedModel {
  const pair = getEffectiveOrchestratorPair(sessionID);
  const orch = parseModelString(pair.orchestratorModel);
  const orchProv = orch ? workspace.providers.find((p) => p.id === orch.providerID) : undefined;
  const orchInfo = orch ? orchProv?.models?.[orch.modelID] : undefined;
  const caps = orch
    ? inferModelEffortCapabilities(orch.providerID, orch.modelID, Boolean(orchInfo?.reasoning ?? true))
    : { efforts: ["low", "medium", "high", "xhigh"] as const, defaultEffort: "high" as const };
  const orchName = formatOrchestratorModelLabel(pair.orchestratorModel);
  return {
    providerID: ORCHESTRATOR_PROVIDER_ID,
    providerName: t("models.orchestratorTitle"),
    modelID: ORCHESTRATOR_MODEL_ID,
    modelName: orchName
      ? `${t("models.orchestratorShortLabel")} · ${orchName}`
      : t("models.orchestratorShortLabel"),
    connected: true,
    isCustom: false,
    reasoning: Boolean(orchInfo?.reasoning ?? true),
    efforts: [...caps.efforts],
    defaultEffort: caps.defaultEffort,
    image: Boolean(orchInfo?.modalities?.input?.includes("image")),
    toolcall: true,
    contextLimit: orch ? lookupModelContextLimit(orch.providerID, orch.modelID) : undefined,
    free: false,
  };
}
