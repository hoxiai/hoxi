// web/src/components/layout/LeftNav.tsx
import { createEffect, createMemo, createSignal, For, onCleanup, Show } from "solid-js";
import { Presence } from "../motion/Presence";
import { Activity, ArrowRightLeft, Bell, Check, Clock, Folder, FolderPlus, Layers, Plus, RefreshCw, Search } from "lucide-solid";
import {
  archiveSession,
  createProjectSessionLists,
  discardSession,
  focusFirstAttentionSession,
  getInitialCodeProjectDir,
  healTransientSidebarProjectMode,
  isDefaultWorkspacePath,
  isInvalidProjectPath,
  isWorkSurfacePath,
  startNewThreadDraft,
  normalizeFolderPath,
  openCreateWorkspaceModal,
  openHandoffModal,
  reorderProjects,
  resolveDefaultProjectDir,
  resolveRootSessionID,
  selectSession,
  setProjectOrder,
  setSidebarProjectMode,
  switchWorkspace,
  type SidebarProjectMode,
  unarchiveSession,
  workspace,
} from "../../store/workspace";
import { Icon, Spinner } from "../icons";
import { buildAllProjects, collectPendingSessionIDs, findProjectDropTarget, resolveActiveRootSessionID, sameStringSet, type ProjectNavEntry } from "./left-nav-helpers";
import MultiProjectTreeItem from "./MultiProjectItem";
import SingleProjectNavView, { RecentTasksNavView } from "./SingleProjectNavView";
import { AttentionNavView, RunningNavView } from "./CrossProjectNavView";
import UserAccountMenu from "./UserAccountMenu";
import { t } from "../../i18n";
import { matchesProjectSessionSearch } from "./session-search";
import { isWorkSurface, surfaceExtension } from "./surface-extension";

const SYNC_MIN_SPIN_MS = 600;

export default function LeftNav() {
  const [filterQuery, setFilterQuery] = createSignal("");
  const [showSearch, setShowSearch] = createSignal(false);
  const [modeMenuOpen, setModeMenuOpen] = createSignal(false);
  const [creatingForPath, setCreatingForPath] = createSignal<string | null>(null);
  const [creatingGlobal, setCreatingGlobal] = createSignal(false);
  const [syncingLists, setSyncingLists] = createSignal(false);
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
      startNewThreadDraft();
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
    new Set(
      workspace.rootDir && !isInvalidProjectPath(workspace.rootDir) && !isWorkSurfacePath(workspace.rootDir)
        ? [normalizeFolderPath(workspace.rootDir)]
        : []
    )
  );
  const [expandedArchivedByProject, setExpandedArchivedByProject] = createSignal<Record<string, boolean>>({});
  const sessionLists = createProjectSessionLists();
  const projectSessions = sessionLists.sessions;
  const loadingProjects = sessionLists.loading;
  onCleanup(sessionLists.dispose);

  healTransientSidebarProjectMode();

  createEffect(() => {
    if (isWorkSurface()) return;
    const root = normalizeFolderPath(workspace.rootDir);
    if (!root || isWorkSurfacePath(root)) {
      const fallbackCodeDir = normalizeFolderPath(resolveDefaultProjectDir(""));
      if (fallbackCodeDir && !isWorkSurfacePath(fallbackCodeDir)) {
        void switchWorkspace(fallbackCodeDir, { selectInitial: true });
      }
    }
  });

  createEffect(() => {
    if (isWorkSurface()) return;
    const root = normalizeFolderPath(workspace.rootDir);
    void workspace.sessions.length;
    void workspace.bootstrappingSessions;
    void workspace.activeProjectID;
    if (root && !isInvalidProjectPath(root) && !isWorkSurfacePath(root)) {
      setExpandedProjects((prev) => (prev.has(root) ? prev : new Set([...prev, root])));
      sessionLists.syncActive(root);
    } else {
      const codeDir = getInitialCodeProjectDir();
      if (codeDir && !isInvalidProjectPath(codeDir) && !isWorkSurfacePath(codeDir)) {
        setExpandedProjects((prev) => (prev.has(codeDir) ? prev : new Set([...prev, codeDir])));
      }
    }
  });

  const allProjects = createMemo<ProjectNavEntry[]>((prev) =>
    isWorkSurface() ? prev ?? [] : buildAllProjects()
  );

  // Ensure newly discovered projects are recorded into projectOrder so switching projects never scrambles order
  createEffect(() => {
    if (isWorkSurface() || workspace.bootstrappingSessions) return;
    const current = allProjects().map((p) => p.path);
    if (current.length === 0) return;
    const missing = current.filter((p) => !workspace.projectOrder.includes(p));
    if (missing.length > 0) setProjectOrder([...workspace.projectOrder, ...missing]);
  });

  const projectList = createMemo<ProjectNavEntry[]>((prev) => {
    if (isWorkSurface()) return prev ?? [];
    const all = allProjects();
    const q = filterQuery().trim().toLowerCase();
    if (!q) return all;
    return all.filter((item) => {
      const path = normalizeFolderPath(item.path);
      const sessions = path === normalizeFolderPath(workspace.rootDir) && !workspace.bootstrappingSessions
        ? workspace.sessions : projectSessions()[path] || [];
      return matchesProjectSessionSearch(item, sessions, q) || Boolean(loadingProjects()[path]);
    });
  });

  const projectByPath = createMemo(() => new Map(projectList().map((p) => [p.path, p])));
  const projectPaths = createMemo(
    () => projectList().map((p) => p.path),
    [],
    { equals: (a, b) => a.length === b.length && a.every((v, i) => v === b[i]) }
  );

  const pendingSessions = createMemo(() => collectPendingSessionIDs(), new Set<string>(), {
    equals: sameStringSet,
  });
  const pendingApprovalRoots = createMemo(() => {
    const roots = new Set<string>();
    for (const p of workspace.pendingPermissions) {
      roots.add(resolveRootSessionID(p.sessionID) || p.sessionID);
    }
    for (const q of workspace.pendingQuestions) {
      roots.add(resolveRootSessionID(q.sessionID) || q.sessionID);
    }
    return roots;
  });
  const pendingApprovalCount = createMemo(() => pendingApprovalRoots().size);
  const hasUnfocusedPendingApproval = createMemo(() => {
    const roots = pendingApprovalRoots();
    if (roots.size === 0) return false;
    const activeRoot = resolveActiveRootSessionID();
    if (!activeRoot) return true;
    for (const id of roots) {
      if (id !== activeRoot) return true;
    }
    return false;
  });
  const isExpanded = (path: string) => expandedProjects().has(normalizeFolderPath(path));

  const loadSessionsForProject = sessionLists.load;

  createEffect(() => {
    if (isWorkSurface()) return;
    const activeRoot = normalizeFolderPath(workspace.rootDir);
    for (const rawPath of expandedProjects()) {
      const path = normalizeFolderPath(rawPath);
      if (!path || isInvalidProjectPath(path) || path === activeRoot) continue;
      if (loadingProjects()[path] || projectSessions()[path] !== undefined) continue;
      void loadSessionsForProject(path);
    }
  });

  const refreshSessionLists = async () => {
    if (syncingLists()) return;
    setSyncingLists(true);
    const startedAt = Date.now();
    try {
      const activeRoot = normalizeFolderPath(workspace.rootDir);
      const paths = [activeRoot];
      const crossProjectMode =
        workspace.sidebarProjectMode === "recent" ||
        workspace.sidebarProjectMode === "attention" ||
        workspace.sidebarProjectMode === "running";
      for (const proj of allProjects()) {
        const clean = normalizeFolderPath(proj.path);
        if (!clean || clean === activeRoot || isInvalidProjectPath(clean)) continue;
        if (projectSessions()[clean] !== undefined || expandedProjects().has(clean) || crossProjectMode) {
          paths.push(clean);
        }
      }
      await sessionLists.refresh(paths);
    } catch (err) {
      console.error("Failed to refresh session lists:", err);
    } finally {
      const remaining = SYNC_MIN_SPIN_MS - (Date.now() - startedAt);
      if (remaining > 0) await new Promise((r) => setTimeout(r, remaining));
      setSyncingLists(false);
    }
  };

  const toggleProject = async (path: string, ev?: MouseEvent) => {
    if (suppressNextProjectClick) {
      suppressNextProjectClick = false;
      return;
    }
    const clean = normalizeFolderPath(path);
    if (!clean) return;
    const isRoot = clean === normalizeFolderPath(workspace.rootDir);
    const clickedChevron = Boolean(
      (ev?.target as HTMLElement | null)?.closest("[data-project-chevron]")
    );
    const next = new Set(expandedProjects());
    if (!isRoot && !(clickedChevron && next.has(clean))) {
      next.add(clean);
      setExpandedProjects(next);
      const cached = projectSessions()[clean] || projectSessions()[path];
      startNewThreadDraft();
      await switchWorkspace(clean, { selectInitial: false, cachedSessions: cached });
      return;
    }
    if (next.has(clean)) {
      next.delete(clean);
      setExpandedProjects(next);
    } else {
      next.add(clean);
      setExpandedProjects(next);
      if (!isRoot && projectSessions()[clean] === undefined && !loadingProjects()[clean]) {
        await loadSessionsForProject(clean);
      }
    }
  };

  const onProjectPointerDown = (ev: PointerEvent, fromPath: string) => {
    if (ev.button !== 0 || (ev.target as HTMLElement | null)?.closest("button")) return;
    const startY = ev.clientY;
    let isDragging = false;
    let rafId: number | null = null;
    let latestClientY = startY;

    const updateDropTarget = () => {
      rafId = null;
      if (!isDragging || !projectListContainerRef) return;
      const next = findProjectDropTarget(projectListContainerRef, latestClientY, fromPath);
      setDropTarget((prev) => {
        if (!prev && !next) return prev;
        if (prev && next && prev.path === next.path && prev.position === next.position) return prev;
        return next;
      });
    };

    const handlePointerMove = (moveEv: PointerEvent) => {
      latestClientY = moveEv.clientY;
      if (!isDragging) {
        if (Math.abs(moveEv.clientY - startY) < 5) return;
        isDragging = true;
        setDraggingProjectPath(fromPath);
      }
      if (rafId === null) {
        rafId = requestAnimationFrame(updateDropTarget);
      }
    };

    const handlePointerUp = () => {
      if (rafId !== null) {
        cancelAnimationFrame(rafId);
        rafId = null;
      }
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
    const normPath = normalizeFolderPath(path);
    setCreatingForPath(path);
    try {
      setExpandedProjects((prev) => (prev.has(normPath) ? prev : new Set([...prev, normPath])));
      startNewThreadDraft();
      if (normPath && normPath !== normalizeFolderPath(workspace.rootDir)) {
        const cached = projectSessions()[normPath] || projectSessions()[path];
        await switchWorkspace(normPath, { selectInitial: false, cachedSessions: cached });
      }
    } catch (err) {
      console.error("Failed to create thread in project:", err);
    } finally {
      setCreatingForPath(null);
    }
  };

  const onSelectSessionInProject = async (path: string, sessionID: string) => {
    const normPath = normalizeFolderPath(path);
    if (normPath && normPath !== normalizeFolderPath(workspace.rootDir)) {
      const cached = projectSessions()[normPath] || projectSessions()[path];
      await switchWorkspace(normPath, {
        selectInitial: false,
        targetSessionID: sessionID,
        cachedSessions: cached,
      });
      return;
    }
    await selectSession(sessionID);
  };

  const onArchiveInProject = async (projPath: string, ev: MouseEvent, sessionID: string) => {
    ev.stopPropagation();
    const normPath = normalizeFolderPath(projPath);
    const now = Date.now();
    sessionLists.updateSession(normPath, sessionID, (s) => ({ ...s, time: { ...s.time, archived: now } }));
    await archiveSession(sessionID, normPath);
  };

  const onUnarchiveInProject = async (projPath: string, ev: MouseEvent, sessionID: string) => {
    ev.stopPropagation();
    const normPath = normalizeFolderPath(projPath);
    sessionLists.updateSession(normPath, sessionID, (s) => {
      const nextTime = { ...s.time };
      delete nextTime.archived;
      return { ...s, time: nextTime };
    });
    await unarchiveSession(sessionID, normPath);
  };

  const onDiscardInProject = async (projPath: string, ev: MouseEvent, sessionID: string) => {
    ev.stopPropagation();
    const normPath = normalizeFolderPath(projPath);
    sessionLists.updateSession(normPath, sessionID, () => null);
    await discardSession(sessionID, true, normPath);
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
    if (isWorkSurface()) return;
    const mode = workspace.sidebarProjectMode;
    if (mode !== "recent" && mode !== "attention" && mode !== "running" &&
      !(mode === "multi" && filterQuery().trim())) return;
    const activeRoot = normalizeFolderPath(workspace.rootDir);
    for (const p of allProjects()) {
      const clean = normalizeFolderPath(p.path);
      if (clean && clean !== activeRoot && projectSessions()[clean] === undefined && !loadingProjects()[clean]) {
        void loadSessionsForProject(clean);
      }
    }
  });

  const modeOptions: Array<{
    id: SidebarProjectMode;
    label: () => string;
    icon: typeof Folder;
  }> = [
    { id: "multi", label: () => t("leftNav.multiProject"), icon: Layers },
    { id: "attention", label: () => t("leftNav.attentionTasks"), icon: Bell },
    { id: "running", label: () => t("leftNav.runningTasks"), icon: Activity },
    { id: "single", label: () => t("leftNav.singleProject"), icon: Folder },
    { id: "recent", label: () => t("leftNav.recentTasks"), icon: Clock },
  ];

  return (
    <aside class="relative h-full w-full flex flex-col bg-transparent pl-2 select-none">
      <Show when={surfaceExtension.enabled && surfaceExtension.renderLeftNav}>
        <div class={isWorkSurface() ? "flex-1 min-h-0 flex flex-col" : "hidden"}>
          {surfaceExtension.renderLeftNav!()}
        </div>
      </Show>
      <div class={isWorkSurface() && surfaceExtension.renderLeftNav ? "hidden" : "flex-1 min-h-0 flex flex-col"}>
      {/* Unified Minimal Header: New Thread + Mode Switcher + Search + New Workspace */}
      <div class="pl-1.5 pr-2.5 pt-1 pb-1.5 flex items-center justify-between gap-1 shrink-0">
        <div class="group/newthread relative flex min-w-0 items-center">
          <button
            type="button"
            onClick={onCreateGlobalNewThread}
            disabled={creatingGlobal()}
            class="inline-flex items-center gap-2 rounded-lg px-2 py-1.5 text-[var(--text-primary)] hover:bg-[var(--bg-hover)] transition-colors cursor-pointer min-w-0"
            title={`${t("leftNav.newThread")}`}
          >
            <Show
              when={!creatingGlobal()}
              fallback={<Spinner class="w-3.5 h-3.5 text-[var(--accent-primary)] shrink-0" />}
            >
              <Icon name="new-session" size="small" class="text-[var(--accent-primary)] shrink-0" />
            </Show>
            <span class="text-[13px] font-medium truncate">{t("leftNav.newThread")}</span>
          </button>
          <button
            type="button"
            onClick={() => void refreshSessionLists()}
            disabled={syncingLists()}
            title={t("leftNav.syncSessions")}
            aria-label={t("leftNav.syncSessions")}
            class="inline-flex h-[22px] w-[22px] shrink-0 items-center justify-center rounded-md text-[var(--text-muted)] opacity-0 transition-opacity hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)] group-hover/newthread:opacity-100 group-focus-within/newthread:opacity-100 [@media(hover:none)]:opacity-100 cursor-pointer"
          >
            <RefreshCw size={12.5} class={syncingLists() ? "animate-spin" : ""} />
          </button>
        </div>

        <div class="flex items-center gap-0.5 shrink-0">
          {/* Sidebar Mode Switcher (Single / Multi / Recent Tasks) */}
          <div class="relative flex items-center">
            <button
              ref={modeBtnRef}
              type="button"
              onClick={() => setModeMenuOpen((v) => !v)}
              class={`inline-flex h-[26px] w-[26px] shrink-0 items-center justify-center rounded-lg transition-colors cursor-pointer ${
                modeMenuOpen()
                  ? "bg-[var(--bg-hover)] text-[var(--text-primary)]"
                  : "text-[var(--text-muted)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-hover)]"
              }`}
              title={t("leftNav.switchSidebarMode")}
            >
              <ArrowRightLeft size={14} class="block shrink-0" />
            </button>

            <Presence when={modeMenuOpen()}>
              {(state) => (
              <div
                ref={modeMenuRef}
                data-state={state()}
                style={{ "--hoxi-origin": "top right" }}
                class="hoxi-popover absolute right-0 top-full mt-1 w-44 rounded-xl border border-[var(--border-strong)] bg-[var(--bg-elevated)] p-1 z-50 space-y-0.5"
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
                        <span class="inline-flex items-center gap-1.5">
                          <Show when={opt.id === "attention" && pendingApprovalCount() > 0}>
                            <span class="rounded-full bg-[var(--status-warning)]/15 px-1.5 py-0.2 font-mono text-[10.5px] font-semibold text-[var(--status-warning)]">
                              {pendingApprovalCount()}
                            </span>
                          </Show>
                          <Show when={workspace.sidebarProjectMode === opt.id}>
                            <Check size={13} class="text-[var(--accent-primary)]" />
                          </Show>
                        </span>
                      </button>
                    );
                  }}
                </For>
              </div>
              )}
            </Presence>
          </div>

          <button
            type="button"
            onClick={() => {
              if (showSearch()) setFilterQuery("");
              setShowSearch((v) => !v);
            }}
            class={`inline-flex h-[26px] w-[26px] shrink-0 items-center justify-center rounded-lg text-[var(--text-muted)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-hover)] transition-colors cursor-pointer ${
              showSearch() ? "bg-[var(--bg-hover)] text-[var(--text-primary)]" : ""
            }`}
            title={t("leftNav.filterPlaceholder")}
          >
            <Search size={14} class="block shrink-0" />
          </button>
          <button
            type="button"
            onClick={() => openCreateWorkspaceModal()}
            class="inline-flex h-[26px] w-[26px] shrink-0 items-center justify-center rounded-lg text-[var(--text-muted)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-hover)] transition-colors cursor-pointer"
            title={t("leftNav.newWorkspace")}
          >
            <FolderPlus size={14} class="block shrink-0" />
          </button>
        </div>
      </div>

      {/* Pending Approvals Quick Entry */}
      <Show when={hasUnfocusedPendingApproval() && workspace.sidebarProjectMode !== "attention"}>
        <div class="pl-1.5 pr-2.5 pb-1.5 shrink-0">
          <button
            type="button"
            onClick={() => {
              focusFirstAttentionSession();
              const root = normalizeFolderPath(workspace.rootDir);
              if (root && !isInvalidProjectPath(root) && !isDefaultWorkspacePath(root)) {
                setExpandedProjects((prev) => (prev.has(root) ? prev : new Set([...prev, root])));
              }
            }}
            class="w-full flex items-center justify-between gap-2 rounded-lg border border-[var(--status-warning)]/30 bg-[var(--status-warning)]/10 hover:bg-[var(--status-warning)]/15 px-2.5 py-1.5 text-[12px] text-[var(--text-primary)] transition-colors cursor-pointer"
          >
            <span class="inline-flex items-center gap-2 min-w-0">
              <span class="h-2 w-2 rounded-full bg-[var(--status-warning)] animate-pulse shrink-0" />
              <span class="font-medium truncate">
                {t("leftNav.pendingApprovalsBanner", { count: pendingApprovalCount() })}
              </span>
            </span>
            <span class="shrink-0 text-[11px] font-medium text-[var(--status-warning)]">
              {t("leftNav.viewPendingApprovals")} →
            </span>
          </button>
        </div>
      </Show>

      {/* Collapsible Search Filter */}
      <div
        class="hoxi-drawer-grid shrink-0"
        data-open={showSearch() ? "true" : "false"}
        aria-hidden={!showSearch()}
      >
        <div class="min-h-0 overflow-hidden">
          <div class="pl-2 pr-3 pb-2">
            <div class="flex items-center gap-1.5 rounded-lg border border-[var(--border-subtle)] bg-[var(--bg-inset)] px-2.5 py-1.5 text-[12px]">
              <Icon name="magnifying-glass" size="small" class="text-[var(--text-muted)]" />
              <input
                type="text"
                placeholder={t("leftNav.filterPlaceholder")}
                value={filterQuery()}
                tabIndex={showSearch() ? 0 : -1}
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
        </div>
      </div>

      {/* MODE A: MULTI-PROJECT TREE */}
      <Show when={workspace.sidebarProjectMode === "multi"}>
        <div
          ref={projectListContainerRef}
          class="sidebar-scroll hoxi-view-enter flex-1 min-h-0 overflow-y-auto pl-1 pr-2 py-1 space-y-1"
        >
          <For each={projectPaths()}>
            {(projPath) => {
              const proj = () => projectByPath().get(projPath)!;
              const normPath = normalizeFolderPath(projPath);
              const isRootProj = () => normPath === normalizeFolderPath(workspace.rootDir);
              const cached = () => projectSessions()[normPath] || projectSessions()[projPath] || [];
              return (
                <MultiProjectTreeItem
                  proj={proj()}
                  expanded={isExpanded(projPath) || Boolean(filterQuery().trim())}
                  isCreating={creatingForPath() === projPath}
                  isLoadingSessions={
                    Boolean(loadingProjects()[normPath]) ||
                    (isRootProj() &&
                      workspace.bootstrappingSessions &&
                      workspace.sessions.length === 0 &&
                      cached().length === 0)
                  }
                  isDraggingThis={draggingProjectPath() === projPath}
                  isDropBefore={dropTarget()?.path === projPath && dropTarget()?.position === "before"}
                  isDropAfter={dropTarget()?.path === projPath && dropTarget()?.position === "after"}
                  isArchivedOpen={Boolean(expandedArchivedByProject()[projPath]) || Boolean(filterQuery().trim())}
                  filterQuery={filterQuery()}
                  cachedSessions={cached()}
                  pendingSessions={pendingSessions()}
                  onPointerDown={onProjectPointerDown}
                  onToggleProject={(p, ev) => void toggleProject(p, ev)}
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

          {/* Bottom + New Workspace (when empty) & Import External Task Button */}
          <div class="pt-1 space-y-0.5">
            <Show when={allProjects().length === 0}>
              <button
                type="button"
                onClick={() => openCreateWorkspaceModal()}
                class="w-full flex items-center gap-2 rounded-lg px-2.5 py-1.5 text-[12px] text-[var(--text-secondary)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-hover)]/60 transition-colors cursor-pointer"
                title={t("leftNav.newWorkspace")}
              >
                <FolderPlus size={13.5} class="shrink-0 text-[var(--accent-primary)]" />
                <span>{t("leftNav.newWorkspace")}</span>
              </button>
            </Show>
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

      {/* MODE B: ATTENTION INBOX (approvals / completed / paused) */}
      <Show when={workspace.sidebarProjectMode === "attention"}>
        <AttentionNavView
          allProjects={allProjects()}
          projectSessions={projectSessions()}
          filterQuery={filterQuery()}
          pendingSessions={pendingSessions()}
          onSelectSession={(p, id) => void onSelectSessionInProject(p, id)}
          onArchiveSession={(p, ev, id) => void onArchiveInProject(p, ev, id)}
          onDiscardSession={(p, ev, id) => onDiscardInProject(p, ev, id)}
        />
      </Show>

      {/* MODE C: RUNNING TASKS BY PROJECT */}
      <Show when={workspace.sidebarProjectMode === "running"}>
        <RunningNavView
          allProjects={allProjects()}
          projectSessions={projectSessions()}
          filterQuery={filterQuery()}
          pendingSessions={pendingSessions()}
          onSelectSession={(p, id) => void onSelectSessionInProject(p, id)}
          onArchiveSession={(p, ev, id) => void onArchiveInProject(p, ev, id)}
          onDiscardSession={(p, ev, id) => onDiscardInProject(p, ev, id)}
        />
      </Show>

      {/* MODE D: SINGLE-PROJECT FOCUSED VIEW */}
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

      {/* MODE E: RECENT TASKS FLAT VIEW */}
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
      </div>

      {/* Bottom Bar: Account Popover + Settings Gear */}
      <UserAccountMenu />
    </aside>
  );
}
