// web/src/components/layout/LeftNav.tsx
import { createEffect, createMemo, createSignal, For, onCleanup, Show } from "solid-js";
import { ArrowRightLeft, Check, Clock, Folder, Layers, Plus } from "lucide-solid";
import {
  archiveSession,
  bootstrapProjectSessions,
  discardSession,
  isInvalidProjectPath,
  newIsolatedSession,
  normalizeFolderPath,
  openCreateWorkspaceModal,
  openHandoffModal,
  reorderProjects,
  selectSession,
  setProjectOrder,
  setSidebarProjectMode,
  switchWorkspace,
  unarchiveSession,
  workspace,
} from "../../store/workspace";
import { hoxiAPI } from "../../services/api";
import type { SessionInfo } from "../../services/sse-types";
import { Icon, Spinner } from "../icons";
import { buildAllProjects, collectPendingSessionIDs, findProjectDropTarget } from "./left-nav-helpers";
import MultiProjectTreeItem from "./MultiProjectItem";
import SingleProjectNavView, { RecentTasksNavView } from "./SingleProjectNavView";
import UserAccountMenu from "./UserAccountMenu";
import { t } from "../../i18n";

export default function LeftNav() {
  const [filterQuery, setFilterQuery] = createSignal("");
  const [showSearch, setShowSearch] = createSignal(false);
  const [modeMenuOpen, setModeMenuOpen] = createSignal(false);
  const [creatingForPath, setCreatingForPath] = createSignal<string | null>(null);
  const [creatingGlobal, setCreatingGlobal] = createSignal(false);
  let modeBtnRef: HTMLButtonElement | undefined;
  let modeMenuRef: HTMLDivElement | undefined;

  // Global shortcut: Cmd+N (or Ctrl+N) creates a new thread
  createEffect(() => {
    const handleGlobalKeyDown = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === "n" && !e.shiftKey && !e.altKey) {
        const activeTag = (document.activeElement as HTMLElement)?.tagName?.toLowerCase();
        if (activeTag !== "input" && activeTag !== "textarea") {
          e.preventDefault();
          void onCreateGlobalNewThread();
        }
      }
    };
    window.addEventListener("keydown", handleGlobalKeyDown);
    onCleanup(() => window.removeEventListener("keydown", handleGlobalKeyDown));
  });

  const onCreateGlobalNewThread = async () => {
    if (creatingGlobal()) return;
    setCreatingGlobal(true);
    try {
      await newIsolatedSession();
    } catch (err) {
      console.error("Failed to create new thread:", err);
    } finally {
      setCreatingGlobal(false);
    }
  };

  // Drag-and-drop state for reordering projects
  const [draggingProjectPath, setDraggingProjectPath] = createSignal<string | null>(null);
  const [dropTarget, setDropTarget] = createSignal<{ path: string; position: "before" | "after" } | null>(null);
  let projectListContainerRef: HTMLDivElement | undefined;
  let suppressNextProjectClick = false;

  // Expanded projects set (by path) for Multi-Project mode
  const [expandedProjects, setExpandedProjects] = createSignal<Set<string>>(
    new Set(workspace.rootDir && !isInvalidProjectPath(workspace.rootDir) ? [normalizeFolderPath(workspace.rootDir)] : [])
  );
  const [expandedArchivedByProject, setExpandedArchivedByProject] = createSignal<Record<string, boolean>>({});
  const [projectSessions, setProjectSessions] = createSignal<Record<string, SessionInfo[]>>({});
  const [loadingProjects, setLoadingProjects] = createSignal<Record<string, boolean>>({});

  createEffect(() => {
    const root = normalizeFolderPath(workspace.rootDir);
    if (root && !isInvalidProjectPath(root)) {
      setExpandedProjects((prev) => (prev.has(root) ? prev : new Set([...prev, root])));
      if (workspace.sessions.length > 0) setProjectSessions((prev) => ({ ...prev, [root]: workspace.sessions }));
    }
  });

  const allProjects = createMemo(() => buildAllProjects());

  // Ensure newly discovered projects are recorded into projectOrder so switching projects never scrambles order
  createEffect(() => {
    if (workspace.bootstrappingSessions) return;
    const current = allProjects().map((p) => p.path);
    if (current.length === 0) return;
    const missing = current.filter((p) => !workspace.projectOrder.includes(p));
    if (missing.length > 0) setProjectOrder([...workspace.projectOrder, ...missing]);
  });

  const projectList = createMemo(() => {
    const all = allProjects();
    const q = filterQuery().trim().toLowerCase();
    if (!q) return all;
    return all.filter(
      (item) =>
        item.name.toLowerCase().includes(q) ||
        item.path.toLowerCase().includes(q) ||
        item.folders.some((f) => f.path.toLowerCase().includes(q))
    );
  });

  const pendingSessions = createMemo(() => collectPendingSessionIDs());
  const isExpanded = (path: string) => expandedProjects().has(normalizeFolderPath(path));

  const loadSessionsForProject = async (path: string) => {
    const clean = normalizeFolderPath(path);
    if (!clean || isInvalidProjectPath(clean)) return;
    setLoadingProjects((prev) => ({ ...prev, [clean]: true }));
    try {
      if (clean === normalizeFolderPath(workspace.rootDir)) {
        await bootstrapProjectSessions(clean);
        setProjectSessions((prev) => ({ ...prev, [clean]: workspace.sessions }));
      } else {
        const list = await hoxiAPI.listSessions(clean);
        setProjectSessions((prev) => ({ ...prev, [clean]: list }));
      }
    } catch (err) {
      console.error(`Failed to load sessions for ${clean}:`, err);
    } finally {
      setLoadingProjects((prev) => ({ ...prev, [clean]: false }));
    }
  };

  createEffect(() => {
    const activeRoot = normalizeFolderPath(workspace.rootDir);
    for (const rawPath of expandedProjects()) {
      const path = normalizeFolderPath(rawPath);
      if (!path || isInvalidProjectPath(path) || loadingProjects()[path] || projectSessions()[path]) continue;
      if (path === activeRoot && (workspace.bootstrappingSessions || workspace.sessions.length > 0)) continue;
      void loadSessionsForProject(path);
    }
  });

  const toggleProject = async (path: string) => {
    if (suppressNextProjectClick) {
      suppressNextProjectClick = false;
      return;
    }
    const clean = normalizeFolderPath(path);
    const next = new Set(expandedProjects());
    if (next.has(clean)) {
      next.delete(clean);
      setExpandedProjects(next);
    } else {
      next.add(clean);
      setExpandedProjects(next);
      const isRoot = clean === normalizeFolderPath(workspace.rootDir);
      if ((!isRoot && !projectSessions()[clean]) || (isRoot && workspace.sessions.length === 0 && !workspace.bootstrappingSessions)) {
        await loadSessionsForProject(clean);
      }
    }
  };

  const onProjectPointerDown = (ev: PointerEvent, fromPath: string) => {
    if (ev.button !== 0 || (ev.target as HTMLElement | null)?.closest("button")) return;
    const startY = ev.clientY;
    let isDragging = false;

    const handlePointerMove = (moveEv: PointerEvent) => {
      if (!isDragging) {
        if (Math.abs(moveEv.clientY - startY) < 5) return;
        isDragging = true;
        setDraggingProjectPath(fromPath);
      }
      if (projectListContainerRef) {
        setDropTarget(findProjectDropTarget(projectListContainerRef, moveEv.clientY, fromPath));
      }
    };

    const handlePointerUp = () => {
      window.removeEventListener("pointermove", handlePointerMove);
      window.removeEventListener("pointerup", handlePointerUp);
      window.removeEventListener("pointercancel", handlePointerUp);
      if (!isDragging) return;
      suppressNextProjectClick = true;
      setTimeout(() => { suppressNextProjectClick = false; }, 80);
      const target = dropTarget();
      if (target && target.path !== fromPath) {
        reorderProjects(allProjects().map((p) => p.path), fromPath, target.path, target.position);
      }
      setDraggingProjectPath(null);
      setDropTarget(null);
    };

    window.addEventListener("pointermove", handlePointerMove);
    window.addEventListener("pointerup", handlePointerUp);
    window.addEventListener("pointercancel", handlePointerUp);
  };

  const toggleArchivedForProject = (path: string) =>
    setExpandedArchivedByProject((prev) => ({ ...prev, [path]: !prev[path] }));

  const onCreateThreadInProject = async (e: MouseEvent, path: string) => {
    e.stopPropagation();
    if (creatingForPath()) return;
    setCreatingForPath(path);
    try {
      if (path !== workspace.rootDir) await switchWorkspace(path);
      setExpandedProjects((prev) => new Set([...prev, path]));
      await newIsolatedSession();
    } catch (err) {
      console.error("Failed to create thread in project:", err);
    } finally {
      setCreatingForPath(null);
    }
  };

  const onSelectSessionInProject = async (path: string, sessionID: string) => {
    if (path !== workspace.rootDir) await switchWorkspace(path);
    await selectSession(sessionID);
  };

  const onArchiveInProject = async (projPath: string, ev: MouseEvent, sessionID: string) => {
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

  const onUnarchiveInProject = async (projPath: string, ev: MouseEvent, sessionID: string) => {
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

  const onDiscardInProject = async (projPath: string, ev: MouseEvent, sessionID: string) => {
    ev.stopPropagation();
    if (projPath !== workspace.rootDir) {
      setProjectSessions((prev) => ({
        ...prev,
        [projPath]: (prev[projPath] || []).filter((s) => s.id !== sessionID),
      }));
    }
    await discardSession(sessionID, true, projPath);
  };

  createEffect(() => {
    if (!modeMenuOpen()) return;
    const handlePointerDown = (e: MouseEvent) => {
      const target = e.target as Node;
      if (modeMenuRef && !modeMenuRef.contains(target) && modeBtnRef && !modeBtnRef.contains(target)) {
        setModeMenuOpen(false);
      }
    };
    window.addEventListener("pointerdown", handlePointerDown);
    onCleanup(() => window.removeEventListener("pointerdown", handlePointerDown));
  });

  createEffect(() => {
    if (workspace.sidebarProjectMode !== "recent") return;
    for (const p of allProjects()) {
      if (p.path !== workspace.rootDir && !projectSessions()[p.path] && !loadingProjects()[p.path]) {
        void loadSessionsForProject(p.path);
      }
    }
  });

  const modeOptions: Array<{
    id: "single" | "multi" | "recent";
    label: () => string;
    icon: typeof Folder;
  }> = [
    { id: "single", label: () => t("leftNav.singleProject"), icon: Folder },
    { id: "multi", label: () => t("leftNav.multiProject"), icon: Layers },
    { id: "recent", label: () => t("leftNav.recentTasks"), icon: Clock },
  ];

  return (
    <aside class="relative h-full w-full flex flex-col bg-transparent pl-2 pr-1 select-none">
      {/* Unified Minimal Header: New Thread + Mode Switcher + Search + New Workspace */}
      <div class="px-1.5 pt-1 pb-1.5 flex items-center justify-between gap-1 shrink-0">
        <button
          type="button"
          onClick={onCreateGlobalNewThread}
          disabled={creatingGlobal()}
          class="inline-flex items-center gap-2 rounded-lg px-2 py-1.5 text-[var(--text-primary)] hover:bg-[var(--bg-hover)] transition-colors cursor-pointer group min-w-0"
          title={`${t("leftNav.newThread")} (⌘N)`}
        >
          <Show
            when={!creatingGlobal()}
            fallback={<Spinner class="w-3.5 h-3.5 text-[var(--accent-primary)] shrink-0" />}
          >
            <Icon name="new-session" size="small" class="text-[var(--accent-primary)] shrink-0" />
          </Show>
          <span class="text-[13px] font-medium truncate">{t("leftNav.newThread")}</span>
          <span class="text-[11px] font-mono text-[var(--text-faint)] group-hover:text-[var(--text-muted)]">
            ⌘N
          </span>
        </button>

        <div class="flex items-center gap-0.5 shrink-0">
          {/* Sidebar Mode Switcher (Single / Multi / Recent Tasks) */}
          <div class="relative">
            <button
              ref={modeBtnRef}
              type="button"
              onClick={() => setModeMenuOpen((v) => !v)}
              class={`p-1.5 rounded-lg transition-colors cursor-pointer ${
                modeMenuOpen()
                  ? "bg-[var(--bg-hover)] text-[var(--text-primary)]"
                  : "text-[var(--text-muted)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-hover)]"
              }`}
              title={t("leftNav.switchSidebarMode")}
            >
              <ArrowRightLeft size={13.5} />
            </button>

            <Show when={modeMenuOpen()}>
              <div
                ref={modeMenuRef}
                class="absolute right-0 top-full mt-1 w-36 rounded-xl border border-[var(--border-strong)] bg-[var(--bg-elevated)] p-1 shadow-xl z-50 animate-in fade-in zoom-in-95 duration-100 space-y-0.5"
              >
                <For each={modeOptions}>
                  {(opt) => {
                    const OptIcon = opt.icon;
                    return (
                      <button
                        type="button"
                        onClick={() => {
                          setSidebarProjectMode(opt.id);
                          setModeMenuOpen(false);
                        }}
                        class={`w-full flex items-center justify-between gap-2 rounded-lg px-2.5 py-1.5 text-[12px] transition-colors cursor-pointer ${
                          workspace.sidebarProjectMode === opt.id
                            ? "bg-[var(--accent-surface)] text-[var(--text-primary)] font-medium"
                            : "text-[var(--text-secondary)] hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)]"
                        }`}
                      >
                        <span class="inline-flex items-center gap-2">
                          <OptIcon size={13} class="text-[var(--text-muted)]" />
                          <span>{opt.label()}</span>
                        </span>
                        <Show when={workspace.sidebarProjectMode === opt.id}>
                          <Check size={13} class="text-[var(--accent-primary)]" />
                        </Show>
                      </button>
                    );
                  }}
                </For>
              </div>
            </Show>
          </div>

          <button
            type="button"
            onClick={() => setShowSearch((v) => !v)}
            class={`p-1.5 rounded-lg text-[var(--text-muted)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-hover)] transition-colors cursor-pointer ${
              showSearch() ? "bg-[var(--bg-hover)] text-[var(--text-primary)]" : ""
            }`}
            title={t("leftNav.filterPlaceholder")}
          >
            <Icon name="magnifying-glass" size="small" />
          </button>
          <button
            type="button"
            onClick={() => openCreateWorkspaceModal()}
            class="p-1.5 rounded-lg text-[var(--text-muted)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-hover)] transition-colors cursor-pointer"
            title={t("leftNav.newWorkspace")}
          >
            <Icon name="folder-add-left" size="small" />
          </button>
        </div>
      </div>

      {/* Collapsible Search Filter */}
      <Show when={showSearch()}>
        <div class="px-2 pb-2 shrink-0">
          <div class="flex items-center gap-1.5 rounded-lg border border-[var(--border-subtle)] bg-[var(--bg-inset)] px-2.5 py-1.5 text-[12px]">
            <Icon name="magnifying-glass" size="small" class="text-[var(--text-muted)]" />
            <input
              type="text"
              placeholder={t("leftNav.filterPlaceholder")}
              value={filterQuery()}
              onInput={(e) => setFilterQuery(e.currentTarget.value)}
              class="w-full bg-transparent text-[12px] text-[var(--text-primary)] placeholder:text-[var(--text-faint)] outline-none"
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

      {/* MODE A: MULTI-PROJECT TREE */}
      <Show when={workspace.sidebarProjectMode === "multi"}>
        <div
          ref={projectListContainerRef}
          class="flex-1 min-h-0 overflow-y-auto px-1 py-1 space-y-1"
        >
          <For each={projectList()}>
            {(proj) => {
              const normPath = normalizeFolderPath(proj.path);
              const isRootProj = normPath === normalizeFolderPath(workspace.rootDir);
              return (
                <MultiProjectTreeItem
                  proj={proj}
                  expanded={isExpanded(proj.path)}
                  isCreating={creatingForPath() === proj.path}
                  isLoadingSessions={Boolean(loadingProjects()[normPath]) || (isRootProj && workspace.bootstrappingSessions && workspace.sessions.length === 0)}
                  isDraggingThis={draggingProjectPath() === proj.path}
                  isDropBefore={dropTarget()?.path === proj.path && dropTarget()?.position === "before"}
                  isDropAfter={dropTarget()?.path === proj.path && dropTarget()?.position === "after"}
                  isArchivedOpen={Boolean(expandedArchivedByProject()[proj.path]) || Boolean(filterQuery().trim())}
                  filterQuery={filterQuery()}
                  cachedSessions={projectSessions()[normPath] || projectSessions()[proj.path] || []}
                  pendingSessions={pendingSessions()}
                  onPointerDown={onProjectPointerDown}
                  onToggleProject={(p) => void toggleProject(p)}
                  onToggleArchived={toggleArchivedForProject}
                  onCreateThread={(e, p) => void onCreateThreadInProject(e, p)}
                  onSelectSession={(p, id) => void onSelectSessionInProject(p, id)}
                  onArchiveSession={(p, ev, id) => void onArchiveInProject(p, ev, id)}
                  onUnarchiveSession={(p, ev, id) => void onUnarchiveInProject(p, ev, id)}
                  onDiscardSession={(p, ev, id) => onDiscardInProject(p, ev, id)}
                />
              );
            }}
          </For>

          {/* Bottom + Import External Task Button */}
          <div class="pt-1">
            <button
              type="button"
              onClick={() => openHandoffModal()}
              class="w-full flex items-center gap-2 rounded-lg px-2.5 py-1.5 text-[12px] text-[var(--text-faint)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-hover)]/60 transition-colors cursor-pointer"
              title={t("leftNav.handoffTooltip")}
            >
              <Plus size={13.5} class="shrink-0 text-[var(--accent-primary)]" />
              <span>{t("leftNav.handoffBtn")}</span>
            </button>
          </div>
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

      {/* MODE C: RECENT TASKS FLAT VIEW */}
      <Show when={workspace.sidebarProjectMode === "recent"}>
        <RecentTasksNavView
          allProjects={allProjects()}
          projectSessions={projectSessions()}
          filterQuery={filterQuery()}
          pendingSessions={pendingSessions()}
          onSelectSession={(p, id) => void onSelectSessionInProject(p, id)}
          onArchiveSession={(p, ev, id) => void onArchiveInProject(p, ev, id)}
          onDiscardSession={(p, ev, id) => onDiscardInProject(p, ev, id)}
        />
      </Show>

      {/* Bottom Bar: Account Popover + Settings Gear */}
      <UserAccountMenu />
    </aside>
  );
}
