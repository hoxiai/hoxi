import { createMemo, createSignal, For, Show } from "solid-js";
import { t } from "../../i18n";
import type { WorkspaceFolderInput, WorkspaceProfile } from "../../services/api";
import {
  closeWorkspaceModal,
  DEFAULT_WORKSPACE_IDENTITY_COLOR,
  isDismissedProjectPath,
  normalizeFolderPath,
  normalizeWorkspaceIdentityColor,
  openCreateWorkspaceModal,
  openDeleteWorkspaceModal,
  openEditWorkspaceModal,
  removeWorkspaceProfile,
  switchWorkspace,
  workspace,
  workspaceDisplayName,
  workspacePrimaryFolder,
} from "../../store/workspace";
import { FolderIcon } from "./left-nav-helpers";

export function folderBaseName(p: string): string {
  const parts = normalizeFolderPath(p).split(/[\\/]/).filter(Boolean);
  return parts[parts.length - 1] || p;
}

export function DeleteWorkspaceModalView(props: {
  targetProfile?: WorkspaceProfile;
}) {
  const [saving, setSaving] = createSignal(false);
  const [errorMsg, setErrorMsg] = createSignal<string | null>(null);

  const handleConfirmDelete = async () => {
    if (saving()) return;
    setSaving(true);
    setErrorMsg(null);
    try {
      const idOrDir =
        props.targetProfile?.id ||
        workspace.editingWorkspaceDir ||
        workspace.rootDir;
      if (idOrDir) {
        await removeWorkspaceProfile(idOrDir);
      }
      closeWorkspaceModal();
    } catch (err) {
      setErrorMsg(err instanceof Error ? err.message : String(err));
    } finally {
      setSaving(false);
    }
  };

  return (
    <div class="w-full max-w-[400px] rounded-xl border border-zinc-200 dark:border-zinc-800 bg-white dark:bg-[#16161A] hoxi-modal-panel hoxi-elevation-4 overflow-hidden">
      <div class="px-5 pt-4 pb-3">
        <h3 class="text-[15px] font-semibold text-zinc-900 dark:text-zinc-100">
          {t("leftNav.deleteWorkspaceTitle")}
        </h3>
        <p class="mt-1.5 text-[12.5px] text-zinc-600 dark:text-zinc-400 leading-relaxed">
          {t("leftNav.deleteWorkspaceDesc", {
            name:
              props.targetProfile?.name ||
              workspaceDisplayName(
                workspace.editingWorkspaceDir || workspace.rootDir
              ),
          })}
        </p>
        <Show when={errorMsg()}>
          <div class="mt-2 px-3 py-2 rounded-lg bg-rose-500/10 border border-rose-500/30 text-[12px] text-rose-500">
            {errorMsg()}
          </div>
        </Show>
      </div>
      <div class="flex items-center justify-end gap-2 px-5 py-3 border-t border-zinc-100 dark:border-zinc-800/80 bg-zinc-50/50 dark:bg-zinc-900/40">
        <button
          type="button"
          onClick={closeWorkspaceModal}
          disabled={saving()}
          class="px-3.5 py-1.5 rounded-lg text-[12.5px] font-medium text-zinc-600 dark:text-zinc-400 hover:bg-zinc-200/60 dark:hover:bg-zinc-800 transition-colors"
        >
          {t("common.cancel")}
        </button>
        <button
          type="button"
          onClick={() => void handleConfirmDelete()}
          disabled={saving()}
          class="px-4 py-1.5 rounded-lg text-[12.5px] font-medium bg-rose-600 hover:bg-rose-500 text-white disabled:opacity-40 transition-colors shadow-sm"
        >
          {t("leftNav.deleteWorkspace")}
        </button>
      </div>
    </div>
  );
}

export function ManageWorkspacesModalView() {
  const allManagedWorkspaces = createMemo(() => {
    const items: Array<{
      key: string;
      id?: string;
      name: string;
      color: string;
      primaryDir: string;
      folders: WorkspaceFolderInput[];
    }> = [];
    const coveredDirs = new Set<string>();

    for (const saved of workspace.savedWorkspaces) {
      if (isDismissedProjectPath(saved.id)) continue;
      const primary = workspacePrimaryFolder(saved);
      if (isDismissedProjectPath(primary)) continue;
      for (const f of saved.folders) {
        coveredDirs.add(normalizeFolderPath(f.path));
      }
      items.push({
        key: saved.id,
        id: saved.id,
        name: saved.name,
        color: saved.color || DEFAULT_WORKSPACE_IDENTITY_COLOR,
        primaryDir: primary,
        folders: saved.folders,
      });
    }

    const extraDirs = new Set<string>();
    if (workspace.rootDir) extraDirs.add(normalizeFolderPath(workspace.rootDir));
    for (const r of workspace.recentProjects) extraDirs.add(normalizeFolderPath(r));
    for (const p of workspace.projects) extraDirs.add(normalizeFolderPath(p.worktree));

    for (const dir of extraDirs) {
      if (!dir || isDismissedProjectPath(dir) || coveredDirs.has(dir)) continue;
      const proj = workspace.projects.find(
        (p) => normalizeFolderPath(p.worktree) === dir
      );
      items.push({
        key: dir,
        name: folderBaseName(dir),
        color: normalizeWorkspaceIdentityColor(proj?.icon?.color),
        primaryDir: dir,
        folders: [{ path: dir, isPrimary: true }],
      });
    }
    return items;
  });

  return (
    <div class="w-full max-w-[480px] rounded-xl border border-zinc-200 dark:border-zinc-800 bg-white dark:bg-[#16161A] hoxi-modal-panel hoxi-elevation-4 overflow-hidden">
      <div class="flex items-start justify-between px-5 pt-4 pb-3 border-b border-zinc-100 dark:border-zinc-800/80">
        <div>
          <h3 class="text-[15px] font-semibold text-zinc-900 dark:text-zinc-100">
            {t("leftNav.manageWorkspacesTitle")}
          </h3>
          <p class="mt-0.5 text-[12px] text-zinc-500 dark:text-zinc-400">
            {t("leftNav.manageWorkspacesDesc")}
          </p>
        </div>
        <button
          type="button"
          onClick={() => openCreateWorkspaceModal()}
          class="inline-flex items-center gap-1 px-2.5 py-1 rounded-lg text-[12px] font-medium bg-[var(--accent-primary)] hover:opacity-90 text-[var(--accent-foreground)] transition-opacity"
        >
          + {t("leftNav.newWorkspace")}
        </button>
      </div>

      <div class="px-4 py-3 space-y-1.5 max-h-[60vh] overflow-y-auto">
        <For each={allManagedWorkspaces()}>
          {(item) => {
            const isCurrent = () =>
              normalizeFolderPath(item.primaryDir) ===
              normalizeFolderPath(workspace.rootDir);
            return (
              <div class="flex items-center justify-between gap-2 px-3 py-2 rounded-lg border border-zinc-200/80 dark:border-zinc-800 bg-zinc-50/60 dark:bg-zinc-900/40">
                <button
                  type="button"
                  onClick={() => {
                    closeWorkspaceModal();
                    void switchWorkspace(item.primaryDir);
                  }}
                  class="flex items-center gap-2.5 min-w-0 flex-1 text-left"
                >
                  <FolderIcon open={isCurrent()} color={item.color} />
                  <div class="min-w-0 flex-1">
                    <div class="flex items-center gap-1.5">
                      <span class="text-[13px] font-medium text-zinc-800 dark:text-zinc-100 truncate">
                        {item.name}
                      </span>
                      <Show when={item.folders.length > 1}>
                        <span class="px-1.5 py-0.5 rounded text-[10px] font-medium bg-zinc-200/80 dark:bg-zinc-800 text-zinc-600 dark:text-zinc-400">
                          {t("leftNav.folderCountBadge", {
                            count: item.folders.length,
                          })}
                        </span>
                      </Show>
                    </div>
                    <div class="text-[11px] text-zinc-400 font-mono truncate">
                      {item.primaryDir}
                    </div>
                  </div>
                </button>

                <div class="flex items-center gap-1 shrink-0">
                  <button
                    type="button"
                    onClick={() =>
                      openEditWorkspaceModal(item.id || item.primaryDir)
                    }
                    class="px-2 py-1 rounded text-[11.5px] font-medium text-zinc-600 dark:text-zinc-300 hover:bg-zinc-200/70 dark:hover:bg-zinc-800 transition-colors"
                  >
                    {t("leftNav.editWorkspace")}
                  </button>
                  <button
                    type="button"
                    onClick={() =>
                      openDeleteWorkspaceModal(item.id || item.primaryDir)
                    }
                    class="px-2 py-1 rounded text-[11.5px] font-medium text-rose-500 hover:bg-rose-500/10 transition-colors"
                  >
                    {t("common.delete")}
                  </button>
                </div>
              </div>
            );
          }}
        </For>
      </div>

      <div class="flex items-center justify-end px-5 py-3 border-t border-zinc-100 dark:border-zinc-800/80 bg-zinc-50/50 dark:bg-zinc-900/40">
        <button
          type="button"
          onClick={closeWorkspaceModal}
          class="px-3.5 py-1.5 rounded-lg text-[12.5px] font-medium text-zinc-600 dark:text-zinc-400 hover:bg-zinc-200/60 dark:hover:bg-zinc-800 transition-colors"
        >
          {t("common.close")}
        </button>
      </div>
    </div>
  );
}
