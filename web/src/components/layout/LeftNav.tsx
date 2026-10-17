// web/src/components/layout/LeftNav.tsx
import { createEffect, createMemo, createSignal, For, Show } from "solid-js";
import { User } from "lucide-solid";
import {
  archiveSession,
  discardSession,
  isSessionArchived,
  newIsolatedSession,
  openWorkspaceFolder,
  selectSession,
  setSidebarProjectMode,
  setWorkspace,
  switchWorkspace,
  unarchiveSession,
  workspace,
} from "../../store/workspace";
import { hoxiAPI } from "../../services/api";
import type { SessionInfo } from "../../services/sse-types";
import { Icon, Spinner } from "../icons";
import {
  buildAllProjects,
  SessionNavItem,
  shortenPath,
} from "./left-nav-helpers";
import SingleProjectNavView from "./SingleProjectNavView";
import { t } from "../../i18n";

export default function LeftNav() {
  const [filterQuery, setFilterQuery] = createSignal("");
  const [showSearch, setShowSearch] = createSignal(false);
  const [creatingForPath, setCreatingForPath] = createSignal<string | null>(null);

  // Expanded projects set (by path) for Multi-Project mode
  const [expandedProjects, setExpandedProjects] = createSignal<Set<string>>(
    new Set([workspace.rootDir])
  );

  // Expanded archived sections per project (by path) for Multi-Project mode
  const [expandedArchivedByProject, setExpandedArchivedByProject] = createSignal<
    Record<string, boolean>
  >({});

  // Cached sessions for inactive projects: Record<path, SessionInfo[]>
  const [projectSessions, setProjectSessions] = createSignal<Record<string, SessionInfo[]>>({});
  const [loadingProjects, setLoadingProjects] = createSignal<Record<string, boolean>>({});

  createEffect(() => {
    if (workspace.rootDir) {
      setExpandedProjects((prev) => new Set([...prev, workspace.rootDir]));
    }
  });

  const allProjects = createMemo(() => buildAllProjects());

  const projectList = createMemo(() => {
    const all = allProjects();
    const q = filterQuery().trim().toLowerCase();
    if (!q) return all;
    return all.filter(
      (item) => item.name.toLowerCase().includes(q) || item.path.toLowerCase().includes(q)
    );
  });

  const pendingSessions = createMemo(
    () => new Set(workspace.pendingPermissions.map((p) => p.sessionID))
  );

  const isExpanded = (path: string) => expandedProjects().has(path);

  const loadSessionsForProject = async (path: string) => {
    setLoadingProjects((prev) => ({ ...prev, [path]: true }));
    try {
      const list = await hoxiAPI.listSessions(path);
      setProjectSessions((prev) => ({ ...prev, [path]: list }));
    } catch (err) {
      console.error(`Failed to load sessions for ${path}:`, err);
    } finally {
      setLoadingProjects((prev) => ({ ...prev, [path]: false }));
    }
  };

  const toggleProject = async (path: string) => {
    const next = new Set(expandedProjects());
    if (next.has(path)) {
      next.delete(path);
      setExpandedProjects(next);
    } else {
      next.add(path);
      setExpandedProjects(next);
      if (path !== workspace.rootDir && !projectSessions()[path]) {
        await loadSessionsForProject(path);
      }
    }
  };

  const toggleArchivedForProject = (path: string) => {
    setExpandedArchivedByProject((prev) => ({
      ...prev,
      [path]: !prev[path],
    }));
  };

  const onCreateThreadInProject = async (e: MouseEvent, path: string) => {
    e.stopPropagation();
    if (creatingForPath()) return;
    setCreatingForPath(path);
    try {
      if (path !== workspace.rootDir) {
        await switchWorkspace(path);
      }
      setExpandedProjects((prev) => new Set([...prev, path]));
      await newIsolatedSession();
    } catch (err) {
      console.error("Failed to create thread in project:", err);
    } finally {
      setCreatingForPath(null);
    }
  };

  const onSelectSessionInProject = async (path: string, sessionID: string) => {
    if (path !== workspace.rootDir) {
      await switchWorkspace(path);
    }
    await selectSession(sessionID);
  };

  const onArchiveInProject = async (
    projPath: string,
    ev: MouseEvent,
    sessionID: string
  ) => {
    ev.stopPropagation();
    const now = Date.now();
    if (projPath !== workspace.rootDir) {
      setProjectSessions((prev) => ({
        ...prev,
        [projPath]: (prev[projPath] || []).map((s) =>
          s.id === sessionID ? { ...s, time: { ...s.time, archived: now } } : s
        ),
      }));
    }
    await archiveSession(sessionID, projPath);
  };

  const onUnarchiveInProject = async (
    projPath: string,
    ev: MouseEvent,
    sessionID: string
  ) => {
    ev.stopPropagation();
    if (projPath !== workspace.rootDir) {
      setProjectSessions((prev) => ({
        ...prev,
        [projPath]: (prev[projPath] || []).map((s) => {
          if (s.id !== sessionID) return s;
          const nextTime = { ...s.time };
          delete nextTime.archived;
          return { ...s, time: nextTime };
        }),
      }));
    }
    await unarchiveSession(sessionID, projPath);
  };

  const onDiscardInProject = async (
    projPath: string,
    ev: MouseEvent,
    sessionID: string
  ) => {
    ev.stopPropagation();
    if (projPath !== workspace.rootDir) {
      setProjectSessions((prev) => ({
        ...prev,
        [projPath]: (prev[projPath] || []).filter((s) => s.id !== sessionID),
      }));
    }
    await discardSession(sessionID, true, projPath);
  };

  const onOpenFolderClick = async () => {
    await openWorkspaceFolder();
  };

  const currentUsername = createMemo(() => {
    const parts = (workspace.rootDir || "").split("/").filter(Boolean);
    if (parts[0] === "Users" && parts[1]) return parts[1];
    return "User";
  });

  return (
    <aside class="relative h-full w-full flex flex-col bg-transparent pl-2 pr-1 select-none">
      {/* Minimal Header: Mode Toggle + Search + Open Folder */}
      <div class="px-2 pt-1 pb-1.5 flex items-center justify-between gap-1">
        <div
          class="inline-flex items-center rounded-lg bg-[var(--bg-inset)] p-0.5"
          title={t("leftNav.switchSidebarMode")}
        >
          <button
            type="button"
            onClick={() => setSidebarProjectMode("multi")}
            class={`inline-flex items-center gap-1 rounded-md px-2 py-0.5 text-[11px] font-medium transition-colors cursor-pointer ${
              workspace.sidebarProjectMode === "multi"
                ? "bg-[var(--bg-panel)] text-[var(--text-primary)] shadow-2xs"
                : "text-[var(--text-muted)] hover:text-[var(--text-primary)]"
            }`}
          >
            <Icon name="layers" size="small" class="w-3 h-3" />
            <span>{t("leftNav.multiProject")}</span>
          </button>

          <button
            type="button"
            onClick={() => setSidebarProjectMode("single")}
            class={`inline-flex items-center gap-1 rounded-md px-2 py-0.5 text-[11px] font-medium transition-colors cursor-pointer ${
              workspace.sidebarProjectMode === "single"
                ? "bg-[var(--bg-panel)] text-[var(--text-primary)] shadow-2xs"
                : "text-[var(--text-muted)] hover:text-[var(--text-primary)]"
            }`}
          >
            <Icon name="folder" size="small" class="w-3 h-3" />
            <span>{t("leftNav.singleProject")}</span>
          </button>
        </div>

        <div class="flex items-center gap-0.5">
          <button
            type="button"
            onClick={() => setShowSearch((v) => !v)}
            class={`p-1 rounded-md text-[var(--text-muted)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-hover)] transition-colors cursor-pointer ${
              showSearch() ? "bg-[var(--bg-hover)] text-[var(--text-primary)]" : ""
            }`}
            title={t("leftNav.filterPlaceholder")}
          >
            <Icon name="magnifying-glass" size="small" />
          </button>
          <button
            type="button"
            onClick={() => void onOpenFolderClick()}
            class="p-1 rounded-md text-[var(--text-muted)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-hover)] transition-colors cursor-pointer"
            title={t("leftNav.addProjectFolder")}
          >
            <Icon name="folder-add-left" size="small" />
          </button>
        </div>
      </div>

      {/* Collapsible Search Filter */}
      <Show when={showSearch()}>
        <div class="px-2 pb-1.5">
          <div class="flex items-center gap-1.5 rounded-lg border border-[var(--border-subtle)] bg-[var(--bg-inset)] px-2 py-1 text-[11.5px]">
            <Icon name="magnifying-glass" size="small" class="text-[var(--text-muted)]" />
            <input
              type="text"
              placeholder={t("leftNav.filterPlaceholder")}
              value={filterQuery()}
              onInput={(e) => setFilterQuery(e.currentTarget.value)}
              class="w-full bg-transparent text-[11.5px] text-[var(--text-primary)] placeholder:text-[var(--text-faint)] outline-none"
            />
            <Show when={filterQuery()}>
              <button
                type="button"
                onClick={() => setFilterQuery("")}
                class="text-[var(--text-muted)] hover:text-[var(--text-primary)] cursor-pointer"
              >
                <Icon name="close-small" size="small" />
              </button>
            </Show>
          </div>
        </div>
      </Show>

      {/* MODE A: CLAUDE CODE-STYLE MINIMAL MULTI-PROJECT TREE */}
      <Show when={workspace.sidebarProjectMode === "multi"}>
        <div class="flex-1 min-h-0 overflow-y-auto px-1 py-1 space-y-0.5">
          <For each={projectList()}>
            {(proj) => {
              const isActive = () => proj.path === workspace.rootDir;
              const expanded = () => isExpanded(proj.path);
              const isCreating = () => creatingForPath() === proj.path;
              const isLoadingSessions = () => loadingProjects()[proj.path];
              const isArchivedOpen = () =>
                Boolean(expandedArchivedByProject()[proj.path]) ||
                Boolean(filterQuery().trim());

              const allFilteredSessions = createMemo(() => {
                const q = filterQuery().trim().toLowerCase();
                const list = isActive()
                  ? workspace.sessions
                  : projectSessions()[proj.path] || [];
                const sorted = [...list].sort((a, b) => b.time.created - a.time.created);
                if (!q) return sorted;
                return sorted.filter(
                  (s) =>
                    (s.title || "").toLowerCase().includes(q) ||
                    (s.slug || "").toLowerCase().includes(q)
                );
              });

              const activeSessions = createMemo(() =>
                allFilteredSessions().filter((s) => !isSessionArchived(s))
              );

              const archivedSessions = createMemo(() =>
                allFilteredSessions().filter((s) => isSessionArchived(s))
              );

              return (
                <div>
                  {/* Flat Minimal Project Row (No card, no border, single line) */}
                  <div
                    class="group/proj flex items-center justify-between gap-1.5 rounded-lg px-2 py-1.5 hover:bg-[var(--bg-hover)]/70 cursor-pointer transition-colors"
                    onClick={() => void toggleProject(proj.path)}
                    title={shortenPath(proj.path)}
                  >
                    <div class="flex items-center gap-1.5 min-w-0 flex-1">
                      <span class="shrink-0 text-[var(--text-faint)] group-hover/proj:text-[var(--text-muted)] transition-colors inline-flex items-center">
                        <Show
                          when={expanded()}
                          fallback={<Icon name="chevron-right" size="small" class="w-3.5 h-3.5" />}
                        >
                          <Icon name="chevron-down" size="small" class="w-3.5 h-3.5" />
                        </Show>
                      </span>

                      <span
                        class={`truncate text-[12.5px] ${
                          isActive()
                            ? "font-semibold text-[var(--text-primary)]"
                            : "font-medium text-[var(--text-secondary)] group-hover/proj:text-[var(--text-primary)]"
                        }`}
                      >
                        {proj.name}
                      </span>
                    </div>

                    <button
                      type="button"
                      onClick={(e) => void onCreateThreadInProject(e, proj.path)}
                      disabled={isCreating()}
                      class="shrink-0 p-0.5 rounded text-[var(--accent-primary)] hover:bg-[var(--accent-surface)] opacity-0 group-hover/proj:opacity-100 transition-opacity cursor-pointer"
                      title={`New session in ${proj.name}`}
                    >
                      <Show
                        when={!isCreating()}
                        fallback={<Spinner class="w-3.5 h-3.5 text-[var(--accent-primary)]" />}
                      >
                        <Icon name="plus-small" size="small" />
                      </Show>
                    </button>
                  </div>

                  {/* Indented Session Rows */}
                  <Show when={expanded()}>
                    <div class="ml-3.5 pl-2 border-l border-[var(--border-subtle)]/60 my-0.5 space-y-0.5">
                      <Show when={isLoadingSessions()}>
                        <div class="flex items-center gap-1.5 px-2 py-1 text-[11px] text-[var(--text-faint)]">
                          <Spinner class="w-3 h-3" />
                          <span>{t("common.loading")}</span>
                        </div>
                      </Show>

                      <Show when={!isLoadingSessions()}>
                        <button
                          type="button"
                          onClick={(e) => void onCreateThreadInProject(e, proj.path)}
                          disabled={isCreating()}
                          class="w-full flex items-center gap-1 text-left px-2 py-1 rounded-lg text-[11.5px] font-medium text-[var(--accent-primary)] hover:bg-[var(--accent-surface)] transition-colors cursor-pointer"
                        >
                          <Show
                            when={!isCreating()}
                            fallback={<Spinner class="w-3 h-3 text-[var(--accent-primary)]" />}
                          >
                            <Icon name="plus-small" size="small" class="w-3.5 h-3.5" />
                          </Show>
                          <span>{t("leftNav.newThread")}</span>
                        </button>
                      </Show>

                      <Show when={!isLoadingSessions() && activeSessions().length > 0}>
                        <For each={activeSessions()}>
                          {(session) => (
                            <SessionNavItem
                              session={session}
                              active={isActive() && workspace.activeSessionID === session.id}
                              canDelete={true}
                              isArchived={false}
                              pendingSessions={pendingSessions()}
                              onSelect={() => void onSelectSessionInProject(proj.path, session.id)}
                              onArchive={(ev, id) => void onArchiveInProject(proj.path, ev, id)}
                              onDiscard={(ev, id) => onDiscardInProject(proj.path, ev, id)}
                            />
                          )}
                        </For>
                      </Show>

                      {/* Collapsible Archived Section for this project */}
                      <Show when={!isLoadingSessions() && archivedSessions().length > 0}>
                        <div class="pt-0.5">
                          <button
                            type="button"
                            onClick={(ev) => {
                              ev.stopPropagation();
                              toggleArchivedForProject(proj.path);
                            }}
                            class="w-full flex items-center justify-between gap-1.5 rounded-lg px-2 py-0.5 text-[11px] text-[var(--text-faint)] hover:text-[var(--text-secondary)] hover:bg-[var(--bg-hover)]/40 transition-colors cursor-pointer"
                          >
                            <div class="flex items-center gap-1">
                              <Show
                                when={isArchivedOpen()}
                                fallback={
                                  <Icon name="chevron-right" size="small" class="w-3 h-3" />
                                }
                              >
                                <Icon name="chevron-down" size="small" class="w-3 h-3" />
                              </Show>
                              <Icon name="archive" size="small" class="w-3 h-3" />
                              <span>{t("leftNav.archivedSessions")}</span>
                            </div>
                            <span class="text-[10px] font-mono">
                              {archivedSessions().length}
                            </span>
                          </button>

                          <Show when={isArchivedOpen()}>
                            <div class="mt-0.5 space-y-0.5">
                              <For each={archivedSessions()}>
                                {(session) => (
                                  <SessionNavItem
                                    session={session}
                                    active={
                                      isActive() && workspace.activeSessionID === session.id
                                    }
                                    canDelete={true}
                                    isArchived={true}
                                    pendingSessions={pendingSessions()}
                                    onSelect={() =>
                                      void onSelectSessionInProject(proj.path, session.id)
                                    }
                                    onUnarchive={(ev, id) =>
                                      void onUnarchiveInProject(proj.path, ev, id)
                                    }
                                    onDiscard={(ev, id) =>
                                      onDiscardInProject(proj.path, ev, id)
                                    }
                                  />
                                )}
                              </For>
                            </div>
                          </Show>
                        </div>
                      </Show>
                    </div>
                  </Show>
                </div>
              );
            }}
          </For>
        </div>
      </Show>

      {/* MODE B: SINGLE-PROJECT FOCUSED VIEW */}
      <Show when={workspace.sidebarProjectMode === "single"}>
        <SingleProjectNavView
          allProjects={allProjects()}
          filterQuery={filterQuery()}
          pendingSessions={pendingSessions()}
          onArchive={(ev, id) => void onArchiveInProject(workspace.rootDir, ev, id)}
          onUnarchive={(ev, id) => void onUnarchiveInProject(workspace.rootDir, ev, id)}
          onDiscard={(ev, id) => onDiscardInProject(workspace.rootDir, ev, id)}
        />
      </Show>

      {/* Kilo-style Bottom Bar: Left User Profile, Right Settings Icon */}
      <div class="mt-auto border-t border-[var(--border-subtle)]/50 pt-2 pb-2.5 px-2 flex items-center justify-between">
        <button
          type="button"
          onClick={() => setWorkspace("providerModalOpen", true)}
          class="flex items-center gap-2 rounded-lg px-1.5 py-1 text-left text-[12px] text-[var(--text-secondary)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-hover)]/60 transition-colors cursor-pointer min-w-0"
          title={t("leftNav.accountAndProviders")}
        >
          <div class="flex h-6 w-6 shrink-0 items-center justify-center rounded-full bg-[var(--bg-inset)] text-[var(--text-secondary)]">
            <User size={13} />
          </div>
          <span class="truncate font-medium">
            {currentUsername()}
          </span>
        </button>

        <button
          type="button"
          onClick={() => setWorkspace("providerModalOpen", true)}
          class="p-1.5 rounded-lg text-[var(--text-muted)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-hover)]/60 transition-colors cursor-pointer shrink-0"
          title={t("leftNav.settingsAndProviders")}
        >
          <Icon name="settings-gear" size="small" />
        </button>
      </div>
    </aside>
  );
}
