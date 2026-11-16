// web/src/store/global-config.ts
// Global config (`/global/config`) reads and key-scoped writes. The gateway
// deep-merges every PATCH into .hoxi/models.json and treats `null` as "delete
// this key", so actions here send only the keys they change. Never round-trip
// the whole GET response: it carries OpenCode's provider list and would be
// persisted into .hoxi/models.json on top of the user's custom providers.
import { reconcile } from "solid-js/store";
import { hoxiAPI } from "../services/api";
import { setWorkspace, workspace } from "./workspace-state";

export type PermissionMode = "ask" | "allow" | "deny";

export function isPermissionMode(value: unknown): value is PermissionMode {
  return value === "ask" || value === "allow" || value === "deny";
}

/**
 * Stores a fresh /global/config snapshot. OpenCode's config does not know Hoxi's
 * own providers, so the ones loadProviders merged in are kept until the next reload.
 */
function applyGlobalConfigSnapshot(cfg: Record<string, any> | undefined): void {
  const hoxiProviders = workspace.hoxiProviders ?? {};
  const keptHoxi = Object.fromEntries(
    Object.entries(workspace.globalConfig?.provider ?? {}).filter(([id]) => id in hoxiProviders)
  );
  setWorkspace(
    "globalConfig",
    reconcile({
      ...(cfg || {}),
      provider: { ...(cfg?.provider ?? {}), ...keptHoxi },
    })
  );
}

/** Reads the current global config and refreshes `workspace.globalConfig`. */
export async function fetchGlobalConfig(): Promise<Record<string, any>> {
  const cfg = (await hoxiAPI.getGlobalConfig()) || {};
  applyGlobalConfigSnapshot(cfg);
  return cfg;
}

/** PATCHes only `patch` (deep-merged server-side, `null` deletes) and syncs the store. */
export async function patchGlobalConfig(patch: Record<string, any>): Promise<Record<string, any>> {
  const updated = (await hoxiAPI.updateGlobalConfig(patch)) || {};
  applyGlobalConfigSnapshot(updated);
  return updated;
}

/** Merges permission entries (e.g. `{ bash: "ask" }` or `{ bash: { "git *": "allow" } }`). */
export function updateGlobalPermissions(patch: Record<string, unknown>): Promise<Record<string, any>> {
  return patchGlobalConfig({ permission: patch });
}

export function saveMcpServer(id: string, entry: Record<string, unknown>): Promise<Record<string, any>> {
  return patchGlobalConfig({ mcp: { [id]: entry } });
}

export function deleteMcpServer(id: string): Promise<Record<string, any>> {
  return patchGlobalConfig({ mcp: { [id]: null } });
}

/** Restarts the OpenCode instance so config / auth changes take effect. */
export async function disposeOpenCodeInstance(): Promise<void> {
  await hoxiAPI.disposeGlobal();
}
