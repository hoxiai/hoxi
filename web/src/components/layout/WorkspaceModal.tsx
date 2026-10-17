import { createEffect, createMemo, createSignal, For, Show } from "solid-js";
import { t } from "../../i18n";
import { hoxiAPI, type WorkspaceFolderInput } from "../../services/api";
import {
  closeWorkspaceModal,
  DEFAULT_WORKSPACE_IDENTITY_COLOR,
  findWorkspaceProfileByDir,
  findWorkspaceProfileById,
  normalizeFolderPath,
  normalizeWorkspaceIdentityColor,
  saveWorkspaceProfile,
  switchWorkspace,
  workspace,
  WORKSPACE_IDENTITY_COLORS,
  workspacePrimaryFolder,
} from "../../store/workspace";
import { FolderIcon } from "./left-nav-helpers";
import {
  DeleteWorkspaceModalView,
  folderBaseName,
  ManageWorkspacesModalView,
} from "./WorkspaceSubModals";

export default function WorkspaceModal() {
  const mode = () => workspace.workspaceModalMode;
  const targetProfile = createMemo(() => {
    if (workspace.editingWorkspaceID) {
      return findWorkspaceProfileById(workspace.editingWorkspaceID);
    }
    if (workspace.editingWorkspaceDir) {
      return findWorkspaceProfileByDir(workspace.editingWorkspaceDir);
    }
    return undefined;
  });

  const [name, setName] = createSignal("");
  const [color, setColor] = createSignal<string>(DEFAULT_WORKSPACE_IDENTITY_COLOR);
  const [folders, setFolders] = createSignal<WorkspaceFolderInput[]>([]);
  const [manualPath, setManualPath] = createSignal("");
  const [saving, setSaving] = createSignal(false);
  const [errorMsg, setErrorMsg] = createSignal<string | null>(null);
  let colorInputRef: HTMLInputElement | undefined;

  createEffect(() => {
    const m = mode();
    if (m === "create") {
      const initDir = normalizeFolderPath(workspace.editingWorkspaceDir);
      setName("");
      setColor(DEFAULT_WORKSPACE_IDENTITY_COLOR);
      setFolders(initDir ? [{ path: initDir, isPrimary: true }] : []);
      setManualPath("");
      setErrorMsg(null);
    } else if (m === "edit") {
      const prof = targetProfile();
      const fallbackDir = normalizeFolderPath(
        workspace.editingWorkspaceDir || workspace.rootDir
      );
      const existingProject = workspace.projects.find(
        (p) => normalizeFolderPath(p.worktree) === fallbackDir
      );
      setName(prof?.name || (fallbackDir ? folderBaseName(fallbackDir) : ""));
      setColor(
        normalizeWorkspaceIdentityColor(prof?.color || existingProject?.icon?.color)
      );
      if (prof && prof.folders.length > 0) {
        setFolders(
          prof.folders.map((f) => ({ path: f.path, isPrimary: f.isPrimary }))
        );
      } else if (fallbackDir) {
        setFolders([{ path: fallbackDir, isPrimary: true }]);
      } else {
        setFolders([]);
      }
      setManualPath("");
      setErrorMsg(null);
    }
  });

  const appendFolderPath = (rawPath: string) => {
    const cleaned = normalizeFolderPath(rawPath);
    if (!cleaned) return;
    setErrorMsg(null);
    setFolders((prev) => {
      if (prev.some((f) => normalizeFolderPath(f.path) === cleaned)) {
        return prev;
      }
      const hasPrimary = prev.some((f) => f.isPrimary);
      return [...prev, { path: cleaned, isPrimary: !hasPrimary }];
    });
  };

  const handleBrowseFolder = async () => {
    try {
      const res = await hoxiAPI.openDirectoryDialog();
      if (res?.path && res.path.trim()) {
        appendFolderPath(res.path);
      }
    } catch (err) {
      setErrorMsg(err instanceof Error ? err.message : String(err));
    }
  };

  const handleAddManualPath = () => {
    const val = manualPath().trim();
    if (!val) return;
    appendFolderPath(val);
    setManualPath("");
  };

  const handleSetPrimary = (path: string) => {
    const target = normalizeFolderPath(path);
    setFolders((prev) =>
      prev.map((f) => ({
        ...f,
        isPrimary: normalizeFolderPath(f.path) === target,
      }))
    );
  };

  const handleRemoveFolder = (path: string) => {
    const target = normalizeFolderPath(path);
    setFolders((prev) => {
      const next = prev.filter((f) => normalizeFolderPath(f.path) !== target);
      if (next.length > 0 && !next.some((f) => f.isPrimary)) {
        next[0] = { ...next[0], isPrimary: true };
      }
      return next;
    });
  };

  const handleSave = async (e: Event) => {
    e.preventDefault();
    if (folders().length === 0 || saving()) return;
    setSaving(true);
    setErrorMsg(null);
    try {
      const prof = targetProfile();
      const saved = await saveWorkspaceProfile(
        {
          name: name().trim() || undefined,
          color: normalizeWorkspaceIdentityColor(color()),
          folders: folders(),
        },
        mode() === "edit" ? prof?.id : null
      );
      const primary = workspacePrimaryFolder(saved);
      closeWorkspaceModal();
      if (
        primary &&
        (mode() === "create" ||
          normalizeFolderPath(workspace.rootDir) !== normalizeFolderPath(primary))
      ) {
        await switchWorkspace(primary);
      }
    } catch (err) {
      setErrorMsg(err instanceof Error ? err.message : String(err));
    } finally {
      setSaving(false);
    }
  };

  const isCustomColor = createMemo(
    () =>
      !WORKSPACE_IDENTITY_COLORS.includes(
        normalizeWorkspaceIdentityColor(
          color()
        ) as (typeof WORKSPACE_IDENTITY_COLORS)[number]
      )
  );

  const primaryFolderHint = createMemo(() => {
    const p = folders().find((f) => f.isPrimary) ?? folders()[0];
    return p ? folderBaseName(p.path) : t("leftNav.workspaceNamePlaceholder");
  });

  return (
    <Show when={mode() !== null}>
      <div
        class="fixed inset-0 z-50 flex items-center justify-center bg-black/55 backdrop-blur-[2px] p-4"
        onClick={(e) => {
          if (e.target === e.currentTarget && !saving()) closeWorkspaceModal();
        }}
      >
        {/* CREATE / EDIT WORKSPACE MODAL */}
        <Show when={mode() === "create" || mode() === "edit"}>
          <form
            onSubmit={handleSave}
            class="w-full max-w-[460px] rounded-xl border border-zinc-200 dark:border-zinc-800 bg-white dark:bg-[#16161A] shadow-2xl overflow-hidden animate-in fade-in zoom-in-95 duration-150"
          >
            <div class="flex items-start justify-between px-5 pt-4 pb-3 border-b border-zinc-100 dark:border-zinc-800/80">
              <div>
                <h3 class="text-[15px] font-semibold text-zinc-900 dark:text-zinc-100">
                  {mode() === "create"
                    ? t("leftNav.createWorkspaceTitle")
                    : t("leftNav.editWorkspaceTitle")}
                </h3>
                <p class="mt-0.5 text-[12px] text-zinc-500 dark:text-zinc-400">
                  {mode() === "create"
                    ? t("leftNav.createWorkspaceDesc")
                    : t("leftNav.editWorkspaceDesc")}
                </p>
              </div>
              <button
                type="button"
                onClick={closeWorkspaceModal}
                class="p-1 rounded-lg text-zinc-400 hover:text-zinc-700 dark:hover:text-zinc-200 hover:bg-zinc-100 dark:hover:bg-zinc-800 transition-colors"
                title={t("common.close")}
              >
                <svg class="w-4 h-4" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                  <path stroke-linecap="round" stroke-linejoin="round" d="M6 18L18 6M6 6l12 12" />
                </svg>
              </button>
            </div>

            <div class="px-5 py-4 space-y-4 max-h-[70vh] overflow-y-auto">
              {/* Workspace Name */}
              <div>
                <label class="block text-[12px] font-medium text-zinc-700 dark:text-zinc-300 mb-1.5">
                  {t("leftNav.workspaceNameLabel")}
                </label>
                <input
                  type="text"
                  value={name()}
                  onInput={(e) => setName(e.currentTarget.value)}
                  placeholder={primaryFolderHint()}
                  class="w-full px-3 py-1.5 rounded-lg text-[13px] bg-zinc-50 dark:bg-zinc-900/90 border border-zinc-200 dark:border-zinc-700/80 text-zinc-900 dark:text-zinc-100 placeholder-zinc-400 focus:outline-none focus:border-blue-500 transition-colors"
                />
              </div>

              {/* Workspace Identity Color (Kilo 6 presets + custom picker) */}
              <div>
                <label class="block text-[12px] font-medium text-zinc-700 dark:text-zinc-300 mb-1.5">
                  {t("leftNav.workspaceColorLabel")}
                </label>
                <div class="flex items-center gap-2">
                  <For each={WORKSPACE_IDENTITY_COLORS}>
                    {(preset) => {
                      const active = () =>
                        normalizeWorkspaceIdentityColor(color()) === preset;
                      return (
                        <button
                          type="button"
                          onClick={() => setColor(preset)}
                          class={`w-6 h-6 rounded-full flex items-center justify-center transition-transform ${
                            active()
                              ? "ring-2 ring-offset-2 ring-zinc-900 dark:ring-zinc-100 dark:ring-offset-[#16161A] scale-105"
                              : "hover:scale-105 opacity-85 hover:opacity-100"
                          }`}
                          style={{ "background-color": preset }}
                          title={preset}
                        >
                          <Show when={active()}>
                            <svg class="w-3.5 h-3.5 text-zinc-950" viewBox="0 0 20 20" fill="currentColor">
                              <path
                                fill-rule="evenodd"
                                d="M16.704 4.153a.75.75 0 01.143 1.052l-8 10.5a.75.75 0 01-1.127.075l-4.5-4.5a.75.75 0 011.06-1.06l3.894 3.893 7.48-9.817a.75.75 0 011.05-.143z"
                                clip-rule="evenodd"
                              />
                            </svg>
                          </Show>
                        </button>
                      );
                    }}
                  </For>

                  <button
                    type="button"
                    onClick={() => colorInputRef?.click()}
                    class={`w-6 h-6 rounded-full border border-dashed border-zinc-400 dark:border-zinc-600 flex items-center justify-center transition-transform ${
                      isCustomColor()
                        ? "ring-2 ring-offset-2 ring-zinc-900 dark:ring-zinc-100 dark:ring-offset-[#16161A] scale-105"
                        : "hover:scale-105"
                    }`}
                    style={
                      isCustomColor()
                        ? { "background-color": normalizeWorkspaceIdentityColor(color()) }
                        : undefined
                    }
                    title={t("leftNav.customColor")}
                  >
                    <span class="text-[11px] font-bold text-zinc-600 dark:text-zinc-300">+</span>
                  </button>
                  <input
                    ref={colorInputRef}
                    type="color"
                    value={normalizeWorkspaceIdentityColor(color())}
                    onInput={(e) => setColor(e.currentTarget.value)}
                    class="sr-only"
                  />
                </div>
              </div>

              {/* Workspace Multi-Folder List */}
              <div>
                <div class="flex items-center justify-between mb-1.5">
                  <label class="text-[12px] font-medium text-zinc-700 dark:text-zinc-300">
                    {t("leftNav.workspaceFoldersLabel")} ({folders().length})
                  </label>
                  <button
                    type="button"
                    onClick={() => void handleBrowseFolder()}
                    class="inline-flex items-center gap-1 px-2 py-1 rounded-md text-[11.5px] font-medium text-blue-600 dark:text-blue-400 hover:bg-blue-500/10 transition-colors"
                  >
                    <svg class="w-3.5 h-3.5" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                      <path stroke-linecap="round" stroke-linejoin="round" d="M12 4.5v15m7.5-7.5h-15" />
                    </svg>
                    {t("leftNav.addFolder")}
                  </button>
                </div>

                <div class="space-y-1.5">
                  <Show
                    when={folders().length > 0}
                    fallback={
                      <div class="px-3 py-4 rounded-lg border border-dashed border-zinc-200 dark:border-zinc-800 text-center text-[12px] text-zinc-400">
                        {t("leftNav.noFoldersSelected")}
                      </div>
                    }
                  >
                    <For each={folders()}>
                      {(folder) => (
                        <div class="flex items-center justify-between gap-2 px-2.5 py-2 rounded-lg border border-zinc-200/80 dark:border-zinc-800 bg-zinc-50/70 dark:bg-zinc-900/50">
                          <div class="flex items-center gap-2 min-w-0 flex-1">
                            <FolderIcon
                              open={folder.isPrimary}
                              color={normalizeWorkspaceIdentityColor(color())}
                            />
                            <div class="min-w-0 flex-1">
                              <div class="flex items-center gap-1.5">
                                <span class="text-[12.5px] font-medium text-zinc-800 dark:text-zinc-200 truncate">
                                  {folderBaseName(folder.path)}
                                </span>
                                <Show when={folder.isPrimary}>
                                  <span class="px-1.5 py-0.5 rounded text-[10px] font-semibold bg-blue-500/15 text-blue-600 dark:text-blue-400 shrink-0">
                                    {t("leftNav.primaryBadge")}
                                  </span>
                                </Show>
                              </div>
                              <div
                                class="text-[11px] text-zinc-400 dark:text-zinc-500 font-mono truncate"
                                title={folder.path}
                              >
                                {folder.path}
                              </div>
                            </div>
                          </div>

                          <div class="flex items-center gap-1 shrink-0">
                            <Show when={!folder.isPrimary}>
                              <button
                                type="button"
                                onClick={() => handleSetPrimary(folder.path)}
                                class="px-2 py-1 rounded text-[11px] font-medium text-zinc-600 dark:text-zinc-300 hover:bg-zinc-200/70 dark:hover:bg-zinc-800 transition-colors"
                              >
                                {t("leftNav.makePrimary")}
                              </button>
                            </Show>
                            <button
                              type="button"
                              onClick={() => handleRemoveFolder(folder.path)}
                              class="px-2 py-1 rounded text-[11px] font-medium text-rose-500 hover:bg-rose-500/10 transition-colors"
                            >
                              {t("leftNav.removeFolder")}
                            </button>
                          </div>
                        </div>
                      )}
                    </For>
                  </Show>
                </div>

                {/* Manual folder path input for quick multi-directory addition */}
                <div class="mt-2 flex items-center gap-1.5">
                  <input
                    type="text"
                    value={manualPath()}
                    onInput={(e) => setManualPath(e.currentTarget.value)}
                    onKeyDown={(e) => {
                      if (e.key === "Enter") {
                        e.preventDefault();
                        handleAddManualPath();
                      }
                    }}
                    placeholder={t("leftNav.addFolderByPathPlaceholder")}
                    class="flex-1 px-2.5 py-1.5 rounded-lg text-[12px] font-mono bg-zinc-50 dark:bg-zinc-900/90 border border-zinc-200 dark:border-zinc-800 text-zinc-800 dark:text-zinc-200 placeholder-zinc-400 focus:outline-none focus:border-blue-500"
                  />
                  <button
                    type="button"
                    onClick={handleAddManualPath}
                    disabled={!manualPath().trim()}
                    class="px-2.5 py-1.5 rounded-lg text-[12px] font-medium border border-zinc-200 dark:border-zinc-700 text-zinc-700 dark:text-zinc-300 hover:bg-zinc-100 dark:hover:bg-zinc-800 disabled:opacity-40 transition-colors"
                  >
                    {t("leftNav.addFolderBtn")}
                  </button>
                </div>
              </div>

              <Show when={errorMsg()}>
                <div class="px-3 py-2 rounded-lg bg-rose-500/10 border border-rose-500/30 text-[12px] text-rose-500">
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
                type="submit"
                disabled={folders().length === 0 || saving()}
                class="px-4 py-1.5 rounded-lg text-[12.5px] font-medium bg-blue-600 hover:bg-blue-500 text-white disabled:opacity-40 transition-colors shadow-sm"
              >
                {saving()
                  ? t("leftNav.savingWorkspace")
                  : mode() === "create"
                  ? t("leftNav.createWorkspaceSubmit")
                  : t("leftNav.saveWorkspaceChanges")}
              </button>
            </div>
          </form>
        </Show>

        <Show when={mode() === "delete"}>
          <DeleteWorkspaceModalView targetProfile={targetProfile()} />
        </Show>

        <Show when={mode() === "manage"}>
          <ManageWorkspacesModalView />
        </Show>
      </div>
    </Show>
  );
}
