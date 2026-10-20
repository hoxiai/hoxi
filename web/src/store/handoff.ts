// web/src/store/handoff.ts
// Cross-CLI session handoff: imports an external CLI session (Claude / Codex /
// Gemini / OpenCode / …) into Hoxi and makes it the active session.
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
    selectModel({ providerID: resolvedProviderID, modelID: resolvedModelID });
  }
  return null;
}
