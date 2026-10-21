// web/src/components/layout/LeftNav.tsx
import { createEffect, createMemo, createSignal, For, onCleanup, Show } from "solid-js";
import { Presence } from "../motion/Presence";
import { ArrowRightLeft, Check, Clock, Folder, FolderPlus, Layers, Plus, RefreshCw, Search } from "lucide-solid";
import {
  archiveSession,
  bootstrapProjectSessions,
  discardSession,
  isInvalidProjectPath,
  loadProjects,
  startNewThreadDraft,
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
import { buildAllProjects, collectPendingSessionIDs, findProjectDropTarget, sameStringSet } from "./left-nav-helpers";
import MultiProjectTreeItem from "./MultiProjectItem";
import { sameSessionArray } from "./SessionHistory";
import SingleProjectNavView, { RecentTasksNavView } from "./SingleProjectNavView";
import UserAccountMenu from "./UserAccountMenu";
import { t } from "../../i18n";
import { matchesProjectSessionSearch } from "./session-search";

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
    new Set(workspace.rootDir && !isInvalidProjectPath(workspace.rootDir) ? [normalizeFolderPath(workspace.rootDir)] : [])
  );
  const [expandedArchivedByProject, setExpandedArchivedByProject] = createSignal<Record<string, boolean>>({});
  const [projectSessions, setProjectSessions] = createSignal<Record<string, SessionInfo[]>>({});
  const [loadingProjects, setLoadingProjects] = createSignal<Record<string, boolean>>({});

  createEffect(() => {
    const root = normalizeFolderPath(workspace.rootDir);
    if (root && !isInvalidProjectPath(root)) {
      setExpandedProjects((prev) => (prev.has(root) ? prev : new Set([...prev, root])));
      if (!workspace.bootstrappingSessions) {
        const current = workspace.sessions.slice();
        setProjectSessions((prev) =>
          prev[root] && sameSessionArray(prev[root], current)
            ? prev
            : { ...prev, [root]: current }
        );
      }
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
  const isExpanded = (path: string) => expandedProjects().has(normalizeFolderPath(path));

  const loadSessionsForProject = async (path: string) => {
    const clean = normalizeFolderPath(path);
    if (!clean || isInvalidProjectPath(clean)) return;
    if (loadingProjects()[clean]) return;
    setLoadingProjects((prev) => (prev[clean] ? prev : { ...prev, [clean]: true }));
    try {
      if (clean === normalizeFolderPath(workspace.rootDir)) {
        await bootstrapProjectSessions(clean);
        setProjectSessions((prev) => ({ ...prev, [clean]: workspace.sessions.slice() }));
      } else {
        const list = await hoxiAPI.listSessions(clean);
        setProjectSessions((prev) => ({ ...prev, [clean]: Array.isArray(list) ? list : [] }));
      }
    } catch (err) {
      console.error(`Failed to load sessions for ${clean}:`, err);
      setProjectSessions((prev) => (prev[clean] !== undefined ? prev : { ...prev, [clean]: [] }));
    } finally {
      setLoadingProjects((prev) => (prev[clean] ? { ...prev, [clean]: false } : prev));
    }
  };

  createEffect(() => {
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
      await loadProjects();
      const tasks: Array<Promise<unknown>> = [];
      if (activeRoot && !isInvalidProjectPath(activeRoot)) {
        tasks.push(bootstrapProjectSessions(activeRoot, false));
      }
      const otherPaths = new Set<string>();
      for (const proj of allProjects()) {
        const clean = normalizeFolderPath(proj.path);
        if (!clean || clean === activeRoot || isInvalidProjectPath(clean)) continue;
        if (projectSessions()[clean] !== undefined || expandedProjects().has(clean)) {
          otherPaths.add(clean);
        }
      }
      for (const clean of otherPaths) {
        tasks.push(
          hoxiAPI.listSessions(clean).then((list) => {
            setProjectSessions((prev) => ({ ...prev, [clean]: Array.isArray(list) ? list : [] }));
          }).catch((err) => {
            console.error(`Failed to refresh sessions for ${clean}:`, err);
          })
        );
      }
      await Promise.all(tasks);
    } catch (err) {
      console.error("Failed to refresh session lists:", err);
    } finally {
      const remaining = SYNC_MIN_SPIN_MS - (Date.now() - startedAt);
      if (remaining > 0) await new Promise((r) => setTimeout(r, remaining));
      setSyncingLists(false);
    }
  };

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
      if (normPath && normPath !== normalizeFolderPath(workspace.rootDir)) {
        const cached = projectSessions()[normPath] || projectSessions()[path];
        await switchWorkspace(normPath, { selectInitial: false, cachedSessions: cached });
      }
      setExpandedProjects((prev) => (prev.has(normPath) ? prev : new Set([...prev, normPath])));
      startNewThreadDraft();
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
    setProjectSessions((prev) => {
      const list = prev[normPath];
      if (!list) return prev;
      return {
        ...prev,
        [normPath]: list.map((s) =>
          s.id === sessionID ? { ...s, time: { ...s.time, archived: now } } : s
        ),
      };
    });
    await archiveSession(sessionID, normPath);
  };

  const onUnarchiveInProject = async (projPath: string, ev: MouseEvent, sessionID: string) => {
    ev.stopPropagation();
    const normPath = normalizeFolderPath(projPath);
    setProjectSessions((prev) => {
      const list = prev[normPath];
      if (!list) return prev;
      return {
        ...prev,
        [normPath]: list.map((s) => {
          if (s.id !== sessionID) return s;
          const nextTime = { ...s.time };
          delete nextTime.archived;
          return { ...s, time: nextTime };
        }),
      };
    });
    await unarchiveSession(sessionID, normPath);
  };

  const onDiscardInProject = async (projPath: string, ev: MouseEvent, sessionID: string) => {
    ev.stopPropagation();
    const normPath = normalizeFolderPath(projPath);
    setProjectSessions((prev) => {
      const list = prev[normPath];
      if (!list) return prev;
      return {
        ...prev,
        [normPath]: list.filter((s) => s.id !== sessionID),
      };
    });
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
    if (workspace.sidebarProjectMode !== "recent" &&
      !(workspace.sidebarProjectMode === "multi" && filterQuery().trim())) return;
    const activeRoot = normalizeFolderPath(workspace.rootDir);
    for (const p of allProjects()) {
      const clean = normalizeFolderPath(p.path);
      if (clean && clean !== activeRoot && projectSessions()[clean] === undefined && !loadingProjects()[clean]) {
        void loadSessionsForProject(clean);
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
    <aside class="relative h-full w-full flex flex-col bg-transparent pl-2 select-none">
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
                class="hoxi-popover absolute right-0 top-full mt-1 w-36 rounded-xl border border-[var(--border-strong)] bg-[var(--bg-elevated)] p-1 z-50 space-y-0.5"
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
