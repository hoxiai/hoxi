// web/src/store/handoff.ts
// Cross-CLI session handoff: imports an external CLI session (Claude / Codex /
// Gemini / OpenCode / …) into Hoxi and makes it the active session.
import { createSignal } from "solid-js";
import {
  hoxiAPI,
  isRunnerAuthMode,
  type ExternalSessionSummary,
  type LocalCLIStatusItem,
} from "../services/api";
import { recordCustomSessionTitle } from "./session-title";
import { normalizeFolderPath } from "./workspace-folders";
import { switchWorkspace } from "./workspace-projects";
import { refreshProviders, selectModel } from "./workspace-providers";
import { replaceSession, selectSession } from "./workspace-sessions";
import { workspace } from "./workspace-state";

const LOCAL_CLI_STATUS_TTL_MS = 30_000;
const [localCLIStatusSignal, setLocalCLIStatusSignal] = createSignal<LocalCLIStatusItem[]>([]);
let lastLocalCLIStatusAt = 0;
let inFlightLocalCLIStatus: Promise<LocalCLIStatusItem[]> | null = null;

export function localCLIStatusItems(): LocalCLIStatusItem[] {
  return localCLIStatusSignal();
}

export function setLocalCLIStatusItems(items: LocalCLIStatusItem[]): void {
  setLocalCLIStatusSignal(Array.isArray(items) ? items : []);
  lastLocalCLIStatusAt = Date.now();
}

export function ensureLocalCLIStatusLoaded(force = false): Promise<LocalCLIStatusItem[]> {
  const now = Date.now();
  if (!force && lastLocalCLIStatusAt > 0 && now - lastLocalCLIStatusAt < LOCAL_CLI_STATUS_TTL_MS) {
    return Promise.resolve(localCLIStatusSignal());
  }
  if (!force && inFlightLocalCLIStatus) {
    return inFlightLocalCLIStatus;
  }
  const p = hoxiAPI
    .getLocalCLIStatus()
    .then((res) => {
      const list = Array.isArray(res?.tools) ? res.tools : [];
      setLocalCLIStatusItems(list);
      return list;
    })
    .catch(() => localCLIStatusSignal())
    .finally(() => {
      if (inFlightLocalCLIStatus === p) {
        inFlightLocalCLIStatus = null;
      }
    });
  inFlightLocalCLIStatus = p;
  return p;
}

export function getLocalCLIStatusForProvider(providerID: string): LocalCLIStatusItem | undefined {
  const pid = (providerID || "").trim().toLowerCase();
  if (!pid) return undefined;
  const list = localCLIStatusSignal();
  for (const item of list) {
    if (!item.installed && !item.authenticated && !item.quota) continue;
    const itemPid = (item.providerID || "").trim().toLowerCase();
    if (itemPid === pid || item.id === pid) {
      return item;
    }
    if ((pid === "openai" || pid === "codex") && item.id === "codex-cli") {
      return item;
    }
    if (
      (pid === "antigravity" || pid === "gemini-cli" || pid === "google") &&
      item.id === "gemini-antigravity"
    ) {
      return item;
    }
  }
  return undefined;
}

export function formatCompactTokenCount(tokens: number | undefined): string {
  const n = Number(tokens ?? 0);
  if (!Number.isFinite(n) || n <= 0) return "0";
  if (n >= 1_000_000) return `${(n / 1_000_000).toFixed(n >= 10_000_000 ? 0 : 1)}M`;
  if (n >= 1_000) return `${(n / 1_000).toFixed(n >= 100_000 ? 0 : 1)}K`;
  return String(Math.round(n));
}

export function formatQuotaResetTime(resetsAtMs: number | undefined, nowMs = Date.now()): string {
  const ts = Number(resetsAtMs ?? 0);
  if (!Number.isFinite(ts) || ts <= nowMs) return "";
  const diffMin = Math.max(1, Math.round((ts - nowMs) / 60_000));
  if (diffMin < 60) return `${diffMin}m`;
  const diffHours = Math.floor(diffMin / 60);
  if (diffHours < 24) {
    const remMin = diffMin % 60;
    return remMin > 0 ? `${diffHours}h ${remMin}m` : `${diffHours}h`;
  }
  const d = new Date(ts);
  const month = String(d.getMonth() + 1).padStart(2, "0");
  const day = String(d.getDate()).padStart(2, "0");
  const hh = String(d.getHours()).padStart(2, "0");
  const mm = String(d.getMinutes()).padStart(2, "0");
  return `${month}-${day} ${hh}:${mm}`;
}

export interface ExternalSessionImportRequest {
  item: ExternalSessionSummary;
  cli?: LocalCLIStatusItem;
  /** Keep the model the source CLI used instead of the current Hoxi selection. */
  useOriginalModel: boolean;
  originalModel: string;
}

function preferredModelFor(req: ExternalSessionImportRequest): { providerID?: string; modelID?: string } {
  const { item, cli, originalModel } = req;
  if (!req.useOriginalModel) {
    return { providerID: workspace.selectedModel?.providerID, modelID: workspace.selectedModel?.modelID };
  }
  const slashIdx = item.source === "opencode-cli" ? originalModel.indexOf("/") : -1;
  if (slashIdx > 0) {
    return { providerID: originalModel.slice(0, slashIdx), modelID: originalModel.slice(slashIdx + 1) };
  }
  const providerID = cli?.providerID ||
    (item.source === "codex-cli" ? "openai" : item.source === "gemini-antigravity" ? "antigravity" : item.source);
  return { providerID, modelID: originalModel };
}

/** Imports the session, switches to its workspace and selects it. Returns an error message on failure. */
export async function importExternalSession(req: ExternalSessionImportRequest): Promise<string | null> {
  const { item, cli } = req;
  if (req.useOriginalModel && cli && !isRunnerAuthMode(cli.authMode) && cli.authenticated && !cli.connected) {
    await hoxiAPI.importLocalCLI(item.source).catch(() => null);
  }
  const preferred = preferredModelFor(req);
  const res = await hoxiAPI.importExternalSession({
    source: item.source,
    sessionID: item.id,
    sourceCwd: item.cwd,
    targetDirectory: item.cwd || workspace.rootDir || undefined,
    preferredProviderID: preferred.providerID,
    preferredModelID: preferred.modelID,
  });
  if (!res.ok || !res.session?.id) return res.error || "";
  await refreshProviders().catch(() => null);
  const targetDir = res.targetDirectory || item.cwd || workspace.rootDir;
  if (targetDir && normalizeFolderPath(targetDir) !== normalizeFolderPath(workspace.rootDir)) {
    // The imported session is selected below. Do not bootstrap and select a
    // second session here, which would load two full transcripts at once.
    await switchWorkspace(targetDir, false);
  }
  if (res.session.title) recordCustomSessionTitle(res.session.id, res.session.title);
  replaceSession(res.session);
  await selectSession(res.session.id);
  const { resolvedProviderID, resolvedModelID } = res;
  if (resolvedProviderID && resolvedModelID &&
      workspace.providers.some((p) => p.id === resolvedProviderID && Boolean(p.models?.[resolvedModelID]))) {
    selectModel({ providerID: resolvedProviderID, modelID: resolvedModelID }, false);
  }
  return null;
}
