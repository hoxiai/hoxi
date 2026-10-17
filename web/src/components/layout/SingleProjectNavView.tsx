// web/src/components/layout/SingleProjectNavView.tsx
import { createEffect, createMemo, createSignal, For, onCleanup, Show } from "solid-js";
import {
  isSessionArchived,
  newIsolatedSession,
  selectSession,
  switchWorkspace,
  workspace,
} from "../../store/workspace";
import type { SessionInfo } from "../../services/sse-types";
import { Icon, Spinner } from "../icons";
import {
  basename,
  dayLabel,
  SessionNavItem,
  shortenPath,
  type ProjectNavEntry,
} from "./left-nav-helpers";
import { t } from "../../i18n";

export default function SingleProjectNavView(props: {
  allProjects: ProjectNavEntry[];
  filterQuery: string;
  pendingSessions: Set<string>;
  onArchive: (ev: MouseEvent, sessionID: string) => void;
  onUnarchive: (ev: MouseEvent, sessionID: string) => void;
  onDiscard: (ev: MouseEvent, sessionID: string) => Promise<void> | void;
}) {
  const [creatingSingle, setCreatingSingle] = createSignal(false);
  const [projectMenuOpen, setProjectMenuOpen] = createSignal(false);
  const [dropdownFilter, setDropdownFilter] = createSignal("");
  const [showArchived, setShowArchived] = createSignal(false);
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

  const filteredSessions = createMemo(() => {
    const q = props.filterQuery.trim().toLowerCase();
    const raw = [...workspace.sessions].sort((a, b) => b.time.created - a.time.created);
    if (!q) return raw;
    return raw.filter(
      (s) =>
        (s.title || "").toLowerCase().includes(q) ||
        (s.slug || "").toLowerCase().includes(q)
    );
  });

  const groupedSingleSessions = createMemo(() => {
    const list = filteredSessions().filter((s) => !isSessionArchived(s));
    const groups: Array<{ label: string; items: SessionInfo[] }> = [];
    for (const item of list) {
      const label = dayLabel(item.time.created);
      const last = groups[groups.length - 1];
      if (last && last.label === label) last.items.push(item);
      else groups.push({ label, items: [item] });
    }
    return groups;
  });

  const archivedSessions = createMemo(() =>
    filteredSessions().filter((s) => isSessionArchived(s))
  );

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
      {/* Clean Project Switcher Row + New Session */}
      <div class="px-1.5 pb-1.5 flex items-center gap-1">
        <button
          ref={buttonRef}
          type="button"
          onClick={() => setProjectMenuOpen((v) => !v)}
          class="flex-1 min-w-0 flex items-center justify-between gap-1.5 rounded-lg px-2 py-1.5 hover:bg-[var(--bg-hover)] transition-colors cursor-pointer text-left"
          title={shortenPath(workspace.rootDir || "—")}
        >
          <div class="flex items-center gap-1.5 min-w-0 flex-1">
            <Icon name="folder" size="small" class="text-[var(--text-muted)]" />
            <span class="truncate text-[12.5px] font-semibold text-[var(--text-primary)]">
              {currentProjectName()}
            </span>
          </div>
          <Icon
            name="chevron-down"
            size="small"
            class={`text-[var(--text-muted)] transition-transform duration-150 ${
              projectMenuOpen() ? "rotate-180" : ""
            }`}
          />
        </button>

        <button
          type="button"
          onClick={onCreateThreadSingle}
          disabled={creatingSingle()}
          class="shrink-0 p-1.5 rounded-lg text-[var(--accent-primary)] hover:bg-[var(--accent-surface)] transition-colors cursor-pointer"
          title={t("leftNav.newThread")}
        >
          <Show
            when={!creatingSingle()}
            fallback={<Spinner class="w-3.5 h-3.5 text-[var(--accent-primary)]" />}
          >
            <Icon name="new-session" size="small" />
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
              <Icon name="magnifying-glass" size="small" class="text-[var(--text-muted)]" />
              <input
                type="text"
                placeholder={t("leftNav.switchProject")}
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
                      <Icon name="check-small" size="small" class="text-[var(--accent-primary)]" />
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
              {t("leftNav.noThreads")}
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
                      isArchived={false}
                      showBranchIcon={true}
                      pendingSessions={props.pendingSessions}
                      onSelect={() => void selectSession(session.id)}
                      onArchive={props.onArchive}
                      onDiscard={props.onDiscard}
                    />
                  )}
                </For>
              </div>
            )}
          </For>
        </Show>

        {/* Collapsible Archived Sessions Section */}
        <Show when={archivedSessions().length > 0}>
          <div class="pt-1 border-t border-[var(--border-subtle)]/40">
            <button
              type="button"
              onClick={() => setShowArchived((v) => !v)}
              class="w-full flex items-center justify-between gap-1.5 rounded-lg px-2 py-1 text-[11px] font-medium text-[var(--text-muted)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-hover)]/50 transition-colors cursor-pointer"
            >
              <div class="flex items-center gap-1.5">
                <Show
                  when={showArchived() || Boolean(props.filterQuery.trim())}
                  fallback={<Icon name="chevron-right" size="small" class="w-3 h-3" />}
                >
                  <Icon name="chevron-down" size="small" class="w-3 h-3" />
                </Show>
                <Icon name="archive" size="small" class="w-3 h-3 text-[var(--text-faint)]" />
                <span>{t("leftNav.archivedSessions")}</span>
              </div>
              <span class="rounded-full bg-[var(--bg-inset)] px-1.5 py-0.2 text-[10px] font-mono text-[var(--text-faint)]">
                {archivedSessions().length}
              </span>
            </button>

            <Show when={showArchived() || Boolean(props.filterQuery.trim())}>
              <div class="mt-0.5 space-y-0.5">
                <For each={archivedSessions()}>
                  {(session) => (
                    <SessionNavItem
                      session={session}
                      active={workspace.activeSessionID === session.id}
                      canDelete={true}
                      isArchived={true}
                      showBranchIcon={true}
                      pendingSessions={props.pendingSessions}
                      onSelect={() => void selectSession(session.id)}
                      onUnarchive={props.onUnarchive}
                      onDiscard={props.onDiscard}
                    />
                  )}
                </For>
              </div>
            </Show>
          </div>
        </Show>
      </div>
    </>
  );
}
