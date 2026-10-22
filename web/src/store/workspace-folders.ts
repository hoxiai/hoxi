// web/src/store/workspace-folders.ts
// Workspace profile / folder path queries and dismissed-project bookkeeping.
// Leaf module: depends only on workspace-state.
import type { WorkspaceFolder, WorkspaceProfile } from "../services/api";
import { DISMISSED_PROJECTS_KEY, setWorkspace, workspace } from "./workspace-state";

// Directly borrowed from Kilo Desktop: plugins/kilo-ui/src/contracts/workspaces/identity-color.ts
export const WORKSPACE_IDENTITY_COLORS = ["#5EA0F7", "#34D399", "#F87171", "#A78BFA", "#22D3EE", "#FBBF24"] as const;
export const DEFAULT_WORKSPACE_IDENTITY_COLOR = WORKSPACE_IDENTITY_COLORS[0];
const HEX_COLOR_RE = /^#[0-9A-Fa-f]{6}$/;

export function normalizeWorkspaceIdentityColor(value?: string | null): string {
  const trimmed = (value || "").trim().toUpperCase();
  if (HEX_COLOR_RE.test(trimmed)) return trimmed;
  return DEFAULT_WORKSPACE_IDENTITY_COLOR;
}

export function isDismissedProjectPath(pathOrID?: string | null): boolean {
  if (!pathOrID) return false;
  const clean = pathOrID.trim();
  const norm = normalizeFolderPath(clean);
  const list = workspace.dismissedProjects || [];
  return list.some((item) => {
    if (!item) return false;
    if (item === clean) return true;
    const itemNorm = normalizeFolderPath(item);
    return Boolean(itemNorm && norm && itemNorm === norm);
  });
}

export function recordDismissedProject(pathOrID?: string | null): void {
  if (!pathOrID) return;
  const clean = pathOrID.trim();
  const norm = normalizeFolderPath(clean);
  const next = [...(workspace.dismissedProjects || [])];
  if (clean && !next.includes(clean)) next.push(clean);
  if (norm && !next.includes(norm)) next.push(norm);
  setWorkspace("dismissedProjects", next);
  try { localStorage.setItem(DISMISSED_PROJECTS_KEY, JSON.stringify(next)); } catch { /* ignore */ }
}

export function unrecordDismissedProject(pathOrID?: string | null): void {
  if (!pathOrID) return;
  const clean = pathOrID.trim();
  const norm = normalizeFolderPath(clean);
  const next = (workspace.dismissedProjects || []).filter(
    (item) => item !== clean && normalizeFolderPath(item) !== norm
  );
  setWorkspace("dismissedProjects", next);
  try { localStorage.setItem(DISMISSED_PROJECTS_KEY, JSON.stringify(next)); } catch { /* ignore */ }
}

export function normalizeFolderPath(p?: string | null): string {
  const trimmed = (p || "").trim();
  if (!trimmed) return "";
  if (trimmed === "/") return "/";
  return trimmed.replace(/[\\/]+$/, "");
}

export function stripWorktreePath(p?: string | null): string {
  const norm = normalizeFolderPath(p);
  if (!norm) return "";
  return norm.replace(/[\\/]\.hoxi[\\/]worktrees[\\/][^\\/]+$/, "");
}

export function shortenPath(p: string): string {
  if (!p) return "";
  const winMatch = p.match(/^[A-Za-z]:[\\/]Users[\\/]([^\\/]+)([\\/].*)?$/);
  if (winMatch) {
    return "~" + (winMatch[2] || "");
  }
  const unixMatch = p.match(/^\/(?:Users|home)\/([^/]+)(\/.*)?$/);
  if (unixMatch) {
    return "~" + (unixMatch[2] || "");
  }
  return p;
}

export function isInvalidProjectPath(p?: string | null): boolean {
  const norm = normalizeFolderPath(p);
  if (!norm || norm === "." || norm === "/" || norm === "\\") return true;
  if (/^[A-Za-z]:$/.test(norm)) return true;
  if (norm.includes(".app/Contents")) return true;
  return false;
}

/** Saved workspaces the user can see: excludes auto-registered hidden projects and dismissed ones. */
export function visibleSavedWorkspaces(): WorkspaceProfile[] {
  return workspace.savedWorkspaces.filter((saved) => !saved.hidden && !isDismissedProjectPath(saved.id));
}

export function resolveDefaultProjectDir(preferredRoot?: string | null): string {
  const pref = normalizeFolderPath(preferredRoot);
  if (pref && !isInvalidProjectPath(pref) && !isDismissedProjectPath(pref)) {
    return pref;
  }
  for (const saved of visibleSavedWorkspaces()) {
    const primary = normalizeFolderPath(workspacePrimaryFolder(saved));
    if (primary && !isInvalidProjectPath(primary) && !isDismissedProjectPath(primary)) {
      return primary;
    }
  }
  for (const recent of workspace.recentProjects) {
    const clean = normalizeFolderPath(recent);
    if (clean && !isInvalidProjectPath(clean) && !isDismissedProjectPath(clean)) {
      return clean;
    }
  }
  for (const proj of workspace.projects) {
    const wt = normalizeFolderPath(proj.worktree);
    if (wt && !isInvalidProjectPath(wt) && !isDismissedProjectPath(wt)) {
      return wt;
    }
  }
  return "";
}

export function findWorkspaceProfileById(id?: string | null): WorkspaceProfile | undefined {
  if (!id) return undefined;
  return workspace.savedWorkspaces.find((w) => w.id === id);
}

export function findWorkspaceProfileByDir(dir?: string | null): WorkspaceProfile | undefined {
  const norm = stripWorktreePath(dir);
  if (!norm) return undefined;
  const primaryMatch = workspace.savedWorkspaces.find((w) =>
    w.folders.some((f) => f.isPrimary && normalizeFolderPath(f.path) === norm)
  );
  if (primaryMatch) return primaryMatch;
  return workspace.savedWorkspaces.find((w) =>
    w.folders.some((f) => normalizeFolderPath(f.path) === norm)
  );
}

export function activeWorkspaceProfile(): WorkspaceProfile | undefined {
  return findWorkspaceProfileByDir(workspace.rootDir);
}

export function workspacePrimaryFolder(profile?: WorkspaceProfile | null): string {
  if (!profile || profile.folders.length === 0) return "";
  const primary = profile.folders.find((f) => f.isPrimary) ?? profile.folders[0];
  return primary?.path || "";
}

export function activeWorkspaceFolders(): WorkspaceFolder[] {
  const profile = activeWorkspaceProfile();
  if (profile && profile.folders.length > 0) {
    return profile.folders;
  }
  if (!workspace.rootDir || isInvalidProjectPath(workspace.rootDir)) return [];
  return [{ id: "primary", path: workspace.rootDir, isPrimary: true, position: 0 }];
}

export function workspaceFoldersForDir(dir: string): WorkspaceFolder[] {
  const cleaned = normalizeFolderPath(dir);
  const profile = findWorkspaceProfileByDir(cleaned);
  if (profile && profile.folders.length > 0) {
    if (cleaned && stripWorktreePath(cleaned) !== cleaned) {
      return profile.folders.map((f) =>
        f.isPrimary ? { ...f, path: cleaned } : f
      );
    }
    return profile.folders;
  }
  if (!cleaned || isInvalidProjectPath(cleaned)) return [];
  return [{ id: "primary", path: cleaned, isPrimary: true, position: 0 }];
}

export function workspaceDisplayName(dir: string, fallbackName?: string): string {
  const profile = findWorkspaceProfileByDir(dir);
  if (profile?.name) return profile.name;
  if (fallbackName) return fallbackName;
  const parts = normalizeFolderPath(dir).split(/[\\/]/).filter(Boolean);
  return parts[parts.length - 1] || dir;
}

export function workspaceIdentityColor(dir: string, fallbackColor?: string): string | undefined {
  const profile = findWorkspaceProfileByDir(dir);
  if (profile?.color) return profile.color;
  return fallbackColor;
}

// Directly borrowed from Kilo Desktop: multi-folder workspace system prompt builder (lwe)
export function buildWorkspaceFoldersSystemPrompt(
  folders: Array<{ path: string; isPrimary: boolean }> = activeWorkspaceFolders()
): string | undefined {
  const primary = folders.find((f) => f.isPrimary) ?? folders[0];
  if (!primary?.path || isInvalidProjectPath(primary.path)) return undefined;
  const additional = folders.filter(
    (f) => normalizeFolderPath(f.path) !== normalizeFolderPath(primary.path)
  );
  if (additional.length === 0) {
    return [
      "Workspace context for this session:",
      `- Primary folder (default working directory): ${JSON.stringify(primary.path)}`,
    ].join("\n");
  }
  return [
    "Workspace context for this session:",
    `- Primary folder (default working directory): ${JSON.stringify(primary.path)}`,
    ...additional.map((f) => `- Additional workspace folder: ${JSON.stringify(f.path)}`),
    "All listed workspace folders are in scope and may be read from and written to.",
    "Use the primary folder as the default working directory, but consider every listed folder when locating or changing relevant files.",
  ].join("\n");
}

export function secondaryWorkspaceDirs(primaryDir?: string): string[] {
  const baseNorm = normalizeFolderPath(primaryDir || workspace.rootDir || "");
  if (!baseNorm) return [];
  const folders = workspaceFoldersForDir(baseNorm);
  if (folders.length <= 1) return [];
  const out: string[] = [];
  const seen = new Set<string>([baseNorm]);
  for (const f of folders) {
    const norm = normalizeFolderPath(f.path);
    if (!norm || seen.has(norm)) continue;
    seen.add(norm);
    out.push(norm);
  }
  return out;
}
