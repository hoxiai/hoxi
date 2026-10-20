import { createEffect, createMemo, createSignal, For, onCleanup, Show, type Component } from "solid-js";
import { Presence } from "../motion/Presence";
import { Dynamic } from "solid-js/web";
import {
  ArrowRightLeft,
  Bot,
  Brain,
  CornerDownLeft,
  Cpu,
  Download,
  FileText,
  GitCompare,
  Globe,
  Link2,
  MessageSquare,
  Moon,
  Plus,
  Search,
  Settings,
  ShieldCheck,
  Sparkles,
  Square,
  Stethoscope,
  Sun,
  Terminal,
} from "lucide-solid";
import { hoxiAPI } from "../../services/api";
import {
  isSessionArchived,
  isSessionBusy,
  openFileInInspector,
  openHandoffModal,
  openSettingsModal,
  selectSession,
  sessionDirectory,
  setWorkspace,
  startNewThreadDraft,
  stopSessionTask,
  toggleTheme,
  workspace,
} from "../../store/workspace";
import { t } from "../../i18n";
import { copyMessageLink, downloadSessionExport } from "../timeline/session-export";

export type CommandPaletteMode = "actions" | "files";

type PaletteIcon = Component<{ size?: number; class?: string }>;
type PaletteGroup = "actions" | "sessions" | "files";

interface PaletteItem {
  id: string;
  group: PaletteGroup;
  label: string;
  detail?: string;
  shortcut?: string;
  icon: PaletteIcon;
  run: () => void | Promise<void>;
}

interface CommandPaletteProps {
  mode: CommandPaletteMode | null;
  onClose: () => void;
}

function scoreMatch(query: string, text: string): number {
  const q = query.trim().toLowerCase();
  if (!q) return 0;
  const value = text.toLowerCase();
  if (value === q) return 0;
  if (value.startsWith(q)) return 1;
  if (value.includes(q)) return 2;
  let pos = 0;
  for (const char of value) {
    if (char === q[pos]) pos++;
    if (pos === q.length) return 3;
  }
  return -1;
}

function pathDetail(path: string): string {
  const index = path.lastIndexOf("/");
  return index > 0 ? path.slice(0, index) : "";
}

export default function CommandPalette(props: CommandPaletteProps) {
  const [query, setQuery] = createSignal("");
  const [selectedIndex, setSelectedIndex] = createSignal(0);
  const [fileResults, setFileResults] = createSignal<Array<{ path: string; name: string }>>([]);
  const [searching, setSearching] = createSignal(false);
  let inputRef: HTMLInputElement | undefined;
  let requestGeneration = 0;

  const activeSessionID = () => workspace.activeSessionID;
  const isMac = typeof navigator !== "undefined" && /Mac|iPhone|iPad/i.test(navigator.platform || "");

  const actionItems = createMemo<PaletteItem[]>(() => {
    const items: PaletteItem[] = [
      {
        id: "new-thread",
        group: "actions",
        label: t("commandPalette.actionNewThread"),
        shortcut: isMac ? "⌘N" : "Ctrl+N",
        icon: Plus,
        run: () => startNewThreadDraft(),
      },
      {
        id: "settings",
        group: "actions",
        label: t("commandPalette.actionOpenSettings"),
        shortcut: isMac ? "⌘," : "Ctrl+,",
        icon: Settings,
        run: () => openSettingsModal("general"),
      },
      {
        id: "toggle-theme",
        group: "actions",
        label: t("commandPalette.actionToggleTheme"),
        icon: workspace.theme === "dark" ? Sun : Moon,
        run: () => toggleTheme(),
      },
      {
        id: "open-files",
        group: "actions",
        label: t("commandPalette.actionOpenFiles"),
        icon: FileText,
        run: () => setWorkspace({ inspectorOpen: true, inspectorTab: "files" }),
      },
      {
        id: "open-diffs",
        group: "actions",
        label: t("commandPalette.actionOpenDiffs"),
        icon: GitCompare,
        run: () => setWorkspace({ inspectorOpen: true, inspectorTab: "diffs" }),
      },
      {
        id: "open-memory",
        group: "actions",
        label: t("commandPalette.actionOpenMemory"),
        icon: Brain,
        run: () => setWorkspace({ inspectorOpen: true, inspectorTab: "memory" }),
      },
      {
        id: "open-skills",
        group: "actions",
        label: t("commandPalette.actionOpenSkills"),
        icon: Sparkles,
        run: () => setWorkspace({ inspectorOpen: true, inspectorTab: "skills" }),
      },
      {
        id: "open-terminal",
        group: "actions",
        label: t("commandPalette.actionOpenTerminal"),
        icon: Terminal,
        run: () => setWorkspace({ inspectorOpen: true, inspectorTab: "terminal" }),
      },
      {
        id: "open-browser",
        group: "actions",
        label: t("commandPalette.actionOpenBrowser"),
        icon: Globe,
        run: () => setWorkspace({ inspectorOpen: true, inspectorTab: "browser" }),
      },
      {
        id: "handoff",
        group: "actions",
        label: t("commandPalette.actionHandoff"),
        icon: ArrowRightLeft,
        run: () => openHandoffModal(),
      },
      {
        id: "providers",
        group: "actions",
        label: t("commandPalette.actionProviders"),
        icon: Cpu,
        run: () => openSettingsModal("providers"),
      },
      {
        id: "agents",
        group: "actions",
        label: t("commandPalette.actionAgents"),
        icon: Bot,
        run: () => openSettingsModal("agents"),
      },
      {
        id: "permissions",
        group: "actions",
        label: t("commandPalette.actionPermissions"),
        icon: ShieldCheck,
        run: () => openSettingsModal("policy"),
      },
      {
        id: "diagnostics",
        group: "actions",
        label: t("commandPalette.actionDiagnostics"),
        icon: Stethoscope,
        run: () => openSettingsModal("diagnostics"),
      },
    ];

    const sessionID = activeSessionID();
    if (sessionID) {
      items.push(
        {
          id: "export-session",
          group: "actions",
          label: t("commandPalette.actionExportSession"),
          icon: Download,
          run: () => downloadSessionExport(sessionID),
        },
        {
          id: "copy-session-link",
          group: "actions",
          label: t("commandPalette.actionCopySessionLink"),
          icon: Link2,
          run: () => { void copyMessageLink(sessionID); },
        },
      );
      if (isSessionBusy(sessionID)) {
        items.push({
          id: "stop-session",
          group: "actions",
          label: t("commandPalette.actionStop"),
          icon: Square,
          run: () => stopSessionTask(sessionID),
        });
      }
    }
    return items;
  });

  const sessionItems = createMemo<PaletteItem[]>(() =>
    workspace.sessions
      .filter((session) => !isSessionArchived(session))
      .slice(0, 40)
      .map((session) => ({
        id: `session:${session.id}`,
        group: "sessions" as const,
        label: session.title || session.id,
        detail: session.id,
        icon: MessageSquare,
        run: () => selectSession(session.id),
      }))
  );

  const visibleItems = createMemo<PaletteItem[]>(() => {
    if (props.mode === "files") {
      return fileResults().map((file) => ({
        id: `file:${file.path}`,
        group: "files" as const,
        label: file.path,
        detail: pathDetail(file.path),
        icon: FileText,
        run: () => openFileInInspector(file.path),
      }));
    }
    const q = query().trim();
    const actions = actionItems()
      .map((item) => ({ item, score: scoreMatch(q, `${item.label} ${item.detail || ""}`) }))
      .filter((entry) => entry.score >= 0)
      .sort((a, b) => a.score - b.score)
      .map((entry) => entry.item);
    const sessions = sessionItems()
      .map((item) => ({ item, score: scoreMatch(q, `${item.label} ${item.detail || ""}`) }))
      .filter((entry) => entry.score >= 0)
      .slice(0, q ? 12 : 6)
      .map((entry) => entry.item);
    return [...actions, ...sessions];
  });

  createEffect(() => {
    const mode = props.mode;
    if (!mode) {
      requestGeneration++;
      setQuery("");
      setSelectedIndex(0);
      setFileResults([]);
      setSearching(false);
      return;
    }
    requestGeneration++;
    setQuery("");
    setSelectedIndex(0);
    setFileResults([]);
    setSearching(false);
    queueMicrotask(() => inputRef?.focus());
  });

  createEffect(() => {
    if (props.mode !== "files") return;
    const q = query().trim();
    const generation = ++requestGeneration;
    if (!q) {
      setFileResults([]);
      setSearching(false);
      return;
    }
    setSearching(true);
    const timer = setTimeout(() => {
      const directory = sessionDirectory(activeSessionID()) || workspace.rootDir;
      void hoxiAPI
        .searchFiles(q, directory, activeSessionID() || undefined, 60)
        .then((results) => {
          if (generation === requestGeneration) {
            setFileResults(results.map((item) => ({ path: item.path, name: item.name })));
          }
        })
        .catch(() => {
          if (generation === requestGeneration) setFileResults([]);
        })
        .finally(() => {
          if (generation === requestGeneration) setSearching(false);
        });
    }, 160);
    onCleanup(() => clearTimeout(timer));
  });

  createEffect(() => {
    const list = visibleItems();
    setSelectedIndex((index) => (list.length === 0 ? 0 : Math.min(index, list.length - 1)));
  });

  const runItem = (item: PaletteItem | undefined) => {
    if (!item) return;
    props.onClose();
    void item.run();
  };

  const onKeyDown = (event: KeyboardEvent) => {
    const list = visibleItems();
    if (event.key === "Escape") {
      event.preventDefault();
      props.onClose();
      return;
    }
    if (event.key === "ArrowDown") {
      event.preventDefault();
      setSelectedIndex((index) => (list.length === 0 ? 0 : (index + 1) % list.length));
      return;
    }
    if (event.key === "ArrowUp") {
      event.preventDefault();
      setSelectedIndex((index) => (list.length === 0 ? 0 : (index - 1 + list.length) % list.length));
      return;
    }
    if (event.key === "Enter" && !event.shiftKey) {
      event.preventDefault();
      runItem(list[selectedIndex()]);
    }
  };

  const groupLabel = (group: PaletteGroup) =>
    group === "actions"
      ? t("commandPalette.actions")
      : group === "sessions"
        ? t("commandPalette.sessions")
        : t("commandPalette.files");

  return (
    <Presence when={Boolean(props.mode)}>
      {(state) => (
      <div
        data-state={state()}
        class="hoxi-modal-backdrop fixed inset-0 z-[95] flex items-start justify-center px-4 pt-[12vh]"
        role="presentation"
        onMouseDown={(event) => {
          if (event.target === event.currentTarget) props.onClose();
        }}
      >
        <div
          role="dialog"
          aria-modal="true"
          aria-label={props.mode === "files" ? t("commandPalette.files") : t("commandPalette.actions")}
          class="hoxi-modal-panel hoxi-elevation-4 w-full max-w-2xl overflow-hidden rounded-2xl border border-[var(--border-strong)] bg-[var(--bg-elevated)]"
          onKeyDown={onKeyDown}
        >
          <div class="flex items-center gap-2 border-b border-[var(--border-subtle)] px-4 py-3">
            <Search size={16} class="shrink-0 text-[var(--text-muted)]" />
            <input
              ref={inputRef}
              value={query()}
              onInput={(event) => {
                setQuery(event.currentTarget.value);
                setSelectedIndex(0);
              }}
              placeholder={
                props.mode === "files"
                  ? t("commandPalette.placeholderFiles")
                  : t("commandPalette.placeholderActions")
              }
              class="min-w-0 flex-1 bg-transparent text-[15px] text-[var(--text-primary)] outline-none placeholder:text-[var(--text-faint)]"
              autocomplete="off"
              spellcheck={false}
            />
            <Show when={searching()}>
              <span class="text-[11px] text-[var(--text-muted)]">{t("common.loading")}</span>
            </Show>
            <kbd class="shrink-0 rounded-md border border-[var(--border-subtle)] bg-[var(--bg-inset)] px-1.5 py-0.5 font-mono text-[11px] text-[var(--text-faint)]">
              Esc
            </kbd>
          </div>

          <div class="max-h-[min(58vh,520px)] overflow-y-auto p-1.5">
            <Show
              when={visibleItems().length > 0}
              fallback={
                <div class="px-3 py-10 text-center text-[12px] text-[var(--text-muted)]">
                  {props.mode === "files" && !query().trim()
                    ? t("commandPalette.placeholderFiles")
                    : t("commandPalette.noResults")}
                </div>
              }
            >
              <For each={visibleItems()}>
                {(item, index) => (
                  <>
                    <Show when={index() === 0 || visibleItems()[index() - 1]?.group !== item.group}>
                      <div class="px-2.5 pb-1 pt-2 text-[11px] font-semibold uppercase tracking-[0.12em] text-[var(--text-faint)]">
                        {groupLabel(item.group)}
                      </div>
                    </Show>
                    <button
                      type="button"
                      role="option"
                      aria-selected={selectedIndex() === index()}
                      onMouseEnter={() => setSelectedIndex(index())}
                      onClick={() => runItem(item)}
                      class={`flex w-full items-center gap-2.5 rounded-xl px-2.5 py-2 text-left transition-colors cursor-pointer ${
                        selectedIndex() === index()
                          ? "bg-[var(--accent-surface)] text-[var(--text-primary)]"
                          : "text-[var(--text-secondary)] hover:bg-[var(--bg-hover)]"
                      }`}
                    >
                      <Dynamic
                        component={item.icon}
                        size={15}
                        class={selectedIndex() === index() ? "shrink-0 text-[var(--accent-primary)]" : "shrink-0 text-[var(--text-muted)]"}
                      />
                      <span class="min-w-0 flex-1">
                        <span class="block truncate text-[13px] font-medium">{item.label}</span>
                        <Show when={item.detail}>
                          <span class="block truncate font-mono text-[11px] text-[var(--text-faint)]">
                            {item.detail}
                          </span>
                        </Show>
                      </span>
                      <Show when={item.shortcut}>
                        <kbd class="shrink-0 rounded-md border border-[var(--border-subtle)] bg-[var(--bg-inset)] px-1.5 py-0.5 font-mono text-[11px] text-[var(--text-faint)]">
                          {item.shortcut}
                        </kbd>
                      </Show>
                      <Show when={selectedIndex() === index()}>
                        <CornerDownLeft size={13} class="shrink-0 text-[var(--accent-primary)]" />
                      </Show>
                    </button>
                  </>
                )}
              </For>
            </Show>
          </div>

          <div class="flex items-center gap-3 border-t border-[var(--border-subtle)] px-3 py-2 text-[11px] text-[var(--text-faint)]">
            <span>{t("commandPalette.hintNavigate")}</span>
            <span>{t("commandPalette.hintSelect")}</span>
            <span>{t("commandPalette.hintClose")}</span>
            <span class="ml-auto font-mono">
              {props.mode === "files" ? t("commandPalette.shortcutFiles") : t("commandPalette.shortcutActions")}
            </span>
          </div>
        </div>
      </div>
      )}
    </Presence>
  );
}
