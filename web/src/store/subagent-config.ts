// web/src/store/subagent-config.ts
// Subagent roster and delegation policy on top of OpenCode's `agent` config.
// OpenCode has no `subagent_model` key: a subagent's model is `agent.<name>.model`,
// and which subagents a primary agent may invoke is `agent.<primary>.permission.task`.
import { createSignal } from "solid-js";
import { hoxiAPI } from "../services/api";
import { patchGlobalConfig } from "./global-config";
import { workspace } from "./workspace-state";

export type TaskPermissionMode = "allow" | "ask" | "deny";

/** How one primary agent may delegate to subagents. */
export type DelegationPolicy =
  | { kind: "inherit" }
  | { kind: "all"; action: TaskPermissionMode }
  | { kind: "allowlist"; allow: string[] };

export interface SubagentEntry {
  id: string;
  builtin: boolean;
  description: string;
  mode: "subagent" | "all";
  model: string;
  /** Agentic iteration cap; 0 means unlimited. */
  steps: number;
  hidden: boolean;
  disabled: boolean;
  /** Denies file edits: the agent can only read and report. */
  readOnly: boolean;
  prompt: string;
}

export const BUILTIN_SUBAGENT_IDS = ["general", "explore", "scout"] as const;

// Hidden system agents and the primary modes Hoxi ships or upstream defines.
// They are never part of the subagent roster even when `mode` is omitted.
const NON_SUBAGENT_IDS = new Set([
  "build",
  "plan",
  "ask",
  "debug",
  "compaction",
  "title",
  "summary",
]);

type ConfigDoc = Record<string, any> | undefined;

function agentMap(config: ConfigDoc): Record<string, any> {
  const agents = config?.agent;
  return agents && typeof agents === "object" ? agents : {};
}

function agentEntry(config: ConfigDoc, id: string): Record<string, any> {
  const entry = agentMap(config)[id];
  return entry && typeof entry === "object" ? entry : {};
}

// Engine-side agent metadata (descriptions and modes) as OpenCode reports it.
// `/global/config` only carries Hoxi overrides, so builtin subagent descriptions
// have to come from the engine's own agent list.
const [engineSubagents, setEngineSubagents] = createSignal<SubagentEntry[]>([]);
export { engineSubagents };

/** Reloads the engine's agent list so the roster can show builtin descriptions. */
export async function refreshSubagentCatalog(): Promise<void> {
  try {
    const list = await hoxiAPI.listAgents(workspace.rootDir || undefined);
    if (!Array.isArray(list)) return;
    setEngineSubagents(
      list
        .filter((agent) => agent?.name && agent.mode !== "primary" && !NON_SUBAGENT_IDS.has(agent.name))
        .map((agent) => ({
          id: agent.name,
          builtin: (BUILTIN_SUBAGENT_IDS as readonly string[]).includes(agent.name),
          description: agent.description || "",
          mode: agent.mode === "subagent" ? "subagent" : "all",
          model: "",
          steps: 0,
          hidden: agent.hidden === true,
          disabled: false,
          readOnly: false,
          prompt: "",
        }))
    );
  } catch {
    /* the roster still renders from config overrides */
  }
}

/** Reads every agent OpenCode can invoke as a subagent. */
export function readSubagents(config: ConfigDoc = workspace.globalConfig): SubagentEntry[] {
  const agents = agentMap(config);
  const ids = new Set<string>(BUILTIN_SUBAGENT_IDS);
  for (const [id, value] of Object.entries(agents)) {
    if (!value || typeof value !== "object") continue;
    if (NON_SUBAGENT_IDS.has(id)) continue;
    const mode = (value as Record<string, any>).mode;
    if (mode === "primary") continue;
    ids.add(id);
  }
  const engineByID = new Map(engineSubagents().map((entry) => [entry.id, entry]));
  return [...ids]
    .filter((id) => !NON_SUBAGENT_IDS.has(id))
    .map((id) => mergeEngineEntry(toEntry(id, agentEntry(config, id)), engineByID.get(id)))
    .sort((a, b) => {
      const builtinDelta = Number(b.builtin) - Number(a.builtin);
      if (builtinDelta !== 0) return builtinDelta;
      const ai = BUILTIN_SUBAGENT_IDS.indexOf(a.id as (typeof BUILTIN_SUBAGENT_IDS)[number]);
      const bi = BUILTIN_SUBAGENT_IDS.indexOf(b.id as (typeof BUILTIN_SUBAGENT_IDS)[number]);
      if (ai !== -1 || bi !== -1) return (ai === -1 ? 99 : ai) - (bi === -1 ? 99 : bi);
      return a.id.localeCompare(b.id);
    });
}

/** The engine's description fills the gap for builtins the user never configured. */
function mergeEngineEntry(entry: SubagentEntry, engine?: SubagentEntry): SubagentEntry {
  if (!engine) return entry;
  return { ...entry, description: entry.description || engine.description };
}

function toEntry(id: string, entry: Record<string, any>): SubagentEntry {
  const permission = entry.permission && typeof entry.permission === "object" ? entry.permission : {};
  const steps = typeof entry.steps === "number" ? entry.steps : typeof entry.maxSteps === "number" ? entry.maxSteps : 0;
  return {
    id,
    builtin: (BUILTIN_SUBAGENT_IDS as readonly string[]).includes(id),
    description: typeof entry.description === "string" ? entry.description : "",
    mode: entry.mode === "subagent" ? "subagent" : "all",
    model: typeof entry.model === "string" ? entry.model : "",
    steps: Number.isFinite(steps) && steps > 0 ? Math.floor(steps) : 0,
    hidden: entry.hidden === true,
    disabled: entry.disable === true,
    readOnly: permission.edit === "deny",
    prompt: typeof entry.prompt === "string" ? entry.prompt : "",
  };
}

/** Global `permission.task`; `inherit` means the engine default (allow). */
export function readGlobalTaskPolicy(
  config: ConfigDoc = workspace.globalConfig
): TaskPermissionMode | "inherit" {
  const task = config?.permission?.task;
  if (task === "allow" || task === "ask" || task === "deny") return task;
  return "inherit";
}

/** Reads one primary agent's delegation policy. */
export function readDelegationPolicy(
  primaryID: string,
  config: ConfigDoc = workspace.globalConfig
): DelegationPolicy {
  const task = agentEntry(config, primaryID).permission?.task;
  if (task === "allow" || task === "ask" || task === "deny") {
    return { kind: "all", action: task };
  }
  if (!task || typeof task !== "object") return { kind: "inherit" };
  const allow: string[] = [];
  let fallback: TaskPermissionMode | null = null;
  for (const [pattern, action] of Object.entries(task as Record<string, unknown>)) {
    if (pattern === "*") {
      if (action === "allow" || action === "ask" || action === "deny") fallback = action;
      continue;
    }
    if (action === "allow") allow.push(pattern);
  }
  if (allow.length > 0) return { kind: "allowlist", allow: allow.sort() };
  if (fallback) return { kind: "all", action: fallback };
  return { kind: "inherit" };
}

/**
 * Builds the `permission.task` value for one primary agent. Existing allow
 * patterns that are no longer selected are sent as `null` so the gateway's
 * deep merge drops them instead of leaving a stale grant behind.
 */
export function buildDelegationTaskValue(
  current: unknown,
  policy: DelegationPolicy
): Record<string, TaskPermissionMode | null> | TaskPermissionMode | null {
  if (policy.kind === "inherit") return null;
  if (policy.kind === "all") return policy.action;
  const value: Record<string, TaskPermissionMode | null> = { "*": "deny" };
  for (const id of policy.allow) value[id] = "allow";
  if (current && typeof current === "object") {
    for (const key of Object.keys(current as Record<string, unknown>)) {
      if (key === "*" || key in value) continue;
      value[key] = null;
    }
  }
  return value;
}

/** Ids a primary agent may invoke right now, or null when unrestricted. */
export function effectiveAllowedSubagents(
  primaryID: string,
  subagentIDs: string[],
  config: ConfigDoc = workspace.globalConfig
): string[] | null {
  const policy = readDelegationPolicy(primaryID, config);
  if (policy.kind === "allowlist") {
    return subagentIDs.filter((id) => policy.allow.includes(id));
  }
  if (policy.kind === "all" && policy.action === "deny") return [];
  const globalPolicy = readGlobalTaskPolicy(config);
  if (policy.kind === "inherit" && globalPolicy === "deny") return [];
  return null;
}

/** Legacy `subagent_model`, written by older Hoxi versions but ignored by OpenCode. */
export function legacySubagentModel(config: ConfigDoc = workspace.globalConfig): string {
  return typeof config?.subagent_model === "string" ? config.subagent_model : "";
}

async function patchAgent(id: string, patch: Record<string, unknown>): Promise<void> {
  await patchGlobalConfig({ agent: { [id]: patch } });
}

export async function updateSubagent(
  id: string,
  patch: {
    model?: string | null;
    description?: string | null;
    prompt?: string | null;
    steps?: number | null;
    hidden?: boolean | null;
    disable?: boolean | null;
    readOnly?: boolean;
  }
): Promise<void> {
  const entry: Record<string, unknown> = {};
  if (patch.model !== undefined) entry.model = patch.model || null;
  if (patch.description !== undefined) entry.description = patch.description || null;
  if (patch.prompt !== undefined) entry.prompt = patch.prompt || null;
  if (patch.steps !== undefined) entry.steps = patch.steps && patch.steps > 0 ? patch.steps : null;
  if (patch.hidden !== undefined) entry.hidden = patch.hidden ? true : null;
  if (patch.disable !== undefined) entry.disable = patch.disable ? true : null;
  if (patch.readOnly !== undefined) {
    // Only a read-only agent needs an override; sending `allow` would quietly
    // weaken a stricter global edit policy.
    entry.permission = { edit: patch.readOnly ? "deny" : null };
  }
  await patchAgent(id, entry);
}

export async function createSubagent(input: {
  id: string;
  description: string;
  prompt: string;
  model?: string;
  readOnly?: boolean;
}): Promise<void> {
  const entry: Record<string, unknown> = {
    mode: "subagent",
    description: input.description,
    prompt: input.prompt,
  };
  if (input.readOnly) entry.permission = { edit: "deny" };
  if (input.model) entry.model = input.model;
  await patchAgent(input.id, entry);
}

export async function deleteSubagent(id: string): Promise<void> {
  await patchGlobalConfig({ agent: { [id]: null } });
}

export async function setGlobalTaskPolicy(mode: TaskPermissionMode | "inherit"): Promise<void> {
  await patchGlobalConfig({ permission: { task: mode === "inherit" ? null : mode } });
}

export async function setDelegationPolicy(
  primaryID: string,
  policy: DelegationPolicy
): Promise<void> {
  const current = agentEntry(workspace.globalConfig, primaryID).permission?.task;
  const value = buildDelegationTaskValue(current, policy);
  await patchAgent(primaryID, { permission: { task: value } });
}

/** Moves the ignored legacy key onto `general`, the closest equivalent. */
export async function migrateLegacySubagentModel(): Promise<void> {
  const legacy = legacySubagentModel();
  if (!legacy) return;
  await patchGlobalConfig({ subagent_model: null, agent: { general: { model: legacy } } });
}
