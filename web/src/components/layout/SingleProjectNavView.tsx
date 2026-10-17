// web/src/components/layout/SingleProjectNavView.tsx
import { createEffect, createMemo, createSignal, For, onCleanup, Show } from "solid-js";
import {
  Check,
  ChevronDown,
  Folder,
  LoaderCircle,
  MessageSquarePlus,
  Search,
} from "lucide-solid";
import {
  newIsolatedSession,
  selectSession,
  switchWorkspace,
  workspace,
} from "../../store/workspace";
import type { SessionInfo } from "../../services/sse-types";
import {
  basename,
  dayLabel,
  SessionNavItem,
  shortenPath,
  type ProjectNavEntry,
} from "./left-nav-helpers";

export default function SingleProjectNavView(props: {
  allProjects: ProjectNavEntry[];
  filterQuery: string;
  pendingSessions: Set<string>;
  onDiscard: (ev: MouseEvent, sessionID: string) => void;
}) {
  const [creatingSingle, setCreatingSingle] = createSignal(false);
  const [projectMenuOpen, setProjectMenuOpen] = createSignal(false);
  const [dropdownFilter, setDropdownFilter] = createSignal("");
  let menuRef: HTMLDivElement | undefined;
  let buttonRef: HTMLButtonElement | undefined;

  createEffect(() => {
    if (!projectMenuOpen()) return;
    const handleClick = (e: MouseEvent) => {
      const target = e.target as Node;
      if (
        menuRef &&
        !menuRef.contains(target) &&
        buttonRef &&
        !buttonRef.contains(target)
      ) {
        setProjectMenuOpen(false);
      }
    };
    window.addEventListener("pointerdown", handleClick);
    onCleanup(() => window.removeEventListener("pointerdown", handleClick));
  });

  const currentProjectName = createMemo(() => basename(workspace.rootDir));

  const dropdownProjectList = createMemo(() => {
    const q = dropdownFilter().trim().toLowerCase();
    if (!q) return props.allProjects;
    return props.allProjects.filter(
      (item) => item.name.toLowerCase().includes(q) || item.path.toLowerCase().includes(q)
    );
  });

  const groupedSingleSessions = createMemo(() => {
    const q = props.filterQuery.trim().toLowerCase();
    const raw = [...workspace.sessions].sort((a, b) => b.time.created - a.time.created);
    const list = q
      ? raw.filter(
          (s) =>
            (s.title || "").toLowerCase().includes(q) ||
            (s.slug || "").toLowerCase().includes(q)
        )
      : raw;
    const groups: Array<{ label: string; items: SessionInfo[] }> = [];
    for (const item of list) {
      const label = dayLabel(item.time.created);
      const last = groups[groups.length - 1];
      if (last && last.label === label) last.items.push(item);
      else groups.push({ label, items: [item] });
    }
    return groups;
  });

  const onSelectProjectSingle = async (path: string) => {
    setProjectMenuOpen(false);
    if (path !== workspace.rootDir) {
      await switchWorkspace(path);
    }
  };

  const onCreateThreadSingle = async () => {
    if (creatingSingle()) return;
    setCreatingSingle(true);
    try {
      await newIsolatedSession();
    } catch (err) {
      console.error("new session failed:", err);
    } finally {
      setCreatingSingle(false);
    }
  };

  return (
    <>
      {/* Clean Project Switcher Row + New Thread */}
      <div class="px-1.5 pb-1.5 flex items-center gap-1">
        <button
          ref={buttonRef}
          type="button"
          onClick={() => setProjectMenuOpen((v) => !v)}
          class="flex-1 min-w-0 flex items-center justify-between gap-1.5 rounded-lg px-2.5 py-1.5 hover:bg-[var(--bg-hover)] transition-colors cursor-pointer text-left"
          title={shortenPath(workspace.rootDir || "—")}
        >
          <div class="flex items-center gap-2 min-w-0 flex-1">
            <Folder size={13} class="shrink-0 text-[var(--text-muted)]" />
            <span class="truncate text-[12.5px] font-semibold text-[var(--text-primary)]">
              {currentProjectName()}
            </span>
          </div>
          <ChevronDown
            size={13}
            class={`shrink-0 text-[var(--text-muted)] transition-transform duration-150 ${
              projectMenuOpen() ? "rotate-180" : ""
            }`}
          />
        </button>

        <button
          type="button"
          onClick={onCreateThreadSingle}
          disabled={creatingSingle()}
          class="shrink-0 p-1.5 rounded-lg text-[var(--text-muted)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-hover)] transition-colors cursor-pointer"
          title="New Session"
        >
          <Show
            when={!creatingSingle()}
            fallback={<LoaderCircle size={13} class="animate-spin" />}
          >
            <MessageSquarePlus size={14} />
          </Show>
        </button>
      </div>

      {/* Floating Project Switcher Popover */}
      <Show when={projectMenuOpen()}>
        <div
          ref={menuRef}
          class="absolute left-3 top-18 w-[240px] rounded-xl border border-[var(--border-strong)] bg-[var(--bg-elevated)] p-1.5 shadow-xl z-50 animate-in fade-in zoom-in-95 duration-100"
        >
          <div class="mb-1 px-1">
            <div class="flex items-center gap-1.5 rounded-lg bg-[var(--bg-inset)] px-2 py-1 text-[11.5px]">
              <Search size={11} class="text-[var(--text-muted)] shrink-0" />
              <input
                type="text"
                placeholder="Switch project…"
                value={dropdownFilter()}
                onInput={(e) => setDropdownFilter(e.currentTarget.value)}
                class="w-full bg-transparent text-[11.5px] text-[var(--text-primary)] placeholder:text-[var(--text-faint)] outline-none"
              />
            </div>
          </div>

          <div class="max-h-56 overflow-y-auto space-y-0.5">
            <For each={dropdownProjectList()}>
              {(proj) => {
                const isActive = () => proj.path === workspace.rootDir;
                return (
                  <button
                    type="button"
                    onClick={() => void onSelectProjectSingle(proj.path)}
                    class={`w-full flex items-center justify-between gap-2 rounded-lg px-2 py-1.5 text-left transition-colors cursor-pointer ${
                      isActive()
                        ? "bg-[var(--bg-hover)] text-[var(--text-primary)] font-medium"
                        : "text-[var(--text-secondary)] hover:bg-[var(--bg-hover)]/60 hover:text-[var(--text-primary)]"
                    }`}
                    title={shortenPath(proj.path)}
                  >
                    <span class="truncate text-[12px]">{proj.name}</span>
                    <Show when={isActive()}>
                      <Check size={12} class="shrink-0 text-[var(--accent-primary)]" />
                    </Show>
                  </button>
                );
              }}
            </For>
          </div>
        </div>
      </Show>

      {/* Chronological Session Stream */}
      <div class="flex-1 min-h-0 overflow-y-auto px-1 pb-4 space-y-3">
        <Show
          when={groupedSingleSessions().length > 0}
          fallback={
            <div class="px-3 py-6 text-center text-[11.5px] text-[var(--text-faint)]">
              No sessions yet.
            </div>
          }
        >
          <For each={groupedSingleSessions()}>
            {(group) => (
              <div class="space-y-0.5">
                <div class="px-2 py-0.5 text-[10px] font-medium text-[var(--text-faint)]">
                  {group.label}
                </div>
                <For each={group.items}>
                  {(session) => (
                    <SessionNavItem
                      session={session}
                      active={workspace.activeSessionID === session.id}
                      canDelete={true}
                      showBranchIcon={true}
                      pendingSessions={props.pendingSessions}
                      onSelect={() => void selectSession(session.id)}
                      onDiscard={props.onDiscard}
                    />
                  )}
                </For>
              </div>
            )}
          </For>
        </Show>
      </div>
    </>
  );
}
