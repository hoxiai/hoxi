import { createEffect, createMemo, createSignal, For, lazy, Show, Suspense } from "solid-js";
import { Maximize2, Minimize2 } from "lucide-solid";
import MemoryBankView from "./MemoryBankView";
import FileTreeView from "./FileTreeView";
import SkillsInspectorView from "../skills/SkillsInspectorView";
import TaskOverviewView from "../tasks/TaskOverviewView";
import { isSessionBusy, setWorkspace, workspace } from "../../store/workspace";
import { collectCycleChangedFiles } from "../timeline/tool-card-state";
import { t } from "../../i18n";

const PierreDiffViewer = lazy(() => import("./PierreDiffViewer"));
const WebGLTerminal = lazy(() => import("../terminal/WebGLTerminal"));
const BrowserInspector = lazy(() => import("./BrowserInspector"));

type TabID = "diffs" | "memory" | "files" | "skills" | "terminal" | "browser" | "tasks";

const PRIMARY_TABS: TabID[] = ["diffs", "files", "terminal", "tasks"];
const SECONDARY_TABS: TabID[] = ["browser", "memory", "skills"];

export default function InspectorPane() {
  const [visitedTabs, setVisitedTabs] = createSignal<Record<TabID, boolean>>({
    diffs: workspace.inspectorTab === "diffs",
    memory: workspace.inspectorTab === "memory",
    files: workspace.inspectorTab === "files",
    skills: workspace.inspectorTab === "skills",
    terminal: workspace.inspectorTab === "terminal",
    browser: workspace.inspectorTab === "browser",
    tasks: workspace.inspectorTab === "tasks",
  });

  createEffect(() => {
    const active = workspace.inspectorTab;
    setVisitedTabs((prev) => (prev[active] ? prev : { ...prev, [active]: true }));
  });

  // Secondary panes (memory / skills / browser) stay reachable through the
  // command palette; they join the tab strip once opened, so switching to a
  // primary tab never strands them.
  const tabs = createMemo<Array<{ id: TabID }>>(() => {
    const active = workspace.inspectorTab;
    const visited = visitedTabs();
    const secondary = SECONDARY_TABS.filter((id) => id === active || visited[id]);
    return [...PRIMARY_TABS, ...secondary].map((id) => ({ id }));
  });

  const diffCount = createMemo(() => {
    if (!workspace.inspectorOpen) return 0;
    const id = workspace.activeSessionID;
    if (workspace.diffScope === "worktree") {
      return (workspace.worktreeDiffsBySession[id || "__root__"] ?? []).length;
    }
    if (id) {
      const sDiffs = workspace.diffsBySession[id];
      if (sDiffs && sDiffs.length > 0) return sDiffs.length;
      if (workspace.worktreeBySession[id]?.dirty === false && !isSessionBusy(id)) {
        return 0;
      }
      const turns = workspace.messagesBySession[id] ?? [];
      if (turns.length > 0) return collectCycleChangedFiles(turns, id).length;
    }
    const key = id || "__root__";
    return (workspace.diffsBySession[key] ?? []).length;
  });

  const tabLabel = (id: TabID) => {
    switch (id) {
      case "diffs":
        return `${t("inspector.tabs.diffs")} (${diffCount()})`;
      case "memory":
        return t("inspector.tabs.memory");
      case "files":
        return t("inspector.tabs.files");
      case "skills":
        return t("inspector.tabs.skills");
      case "terminal":
        return t("inspector.tabs.terminal");
      case "browser":
        return t("inspector.tabs.browser");
      case "tasks":
        return t("inspector.tabs.tasks");
    }
  };

  return (
    <aside class="h-full w-full flex flex-col bg-transparent">
      <div class="shrink-0 flex items-center gap-1 px-2 py-1.5 border-b border-[var(--border-subtle)] overflow-x-auto no-scrollbar">
        <For each={tabs()}>
          {(tab) => (
            <button
              type="button"
              class={`hoxi-press relative shrink-0 whitespace-nowrap rounded-lg px-2.5 py-1 text-xs font-medium cursor-pointer ${
                workspace.inspectorTab === tab.id
                  ? "bg-[var(--accent-surface)] text-[var(--accent-primary)]"
                  : "text-[var(--text-muted)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-hover)]"
              }`}
              onClick={() => setWorkspace("inspectorTab", tab.id)}
            >
              {tabLabel(tab.id)}
            </button>
          )}
        </For>

        <button
          type="button"
          class={`hoxi-press ml-auto shrink-0 inline-flex items-center justify-center w-6 h-6 rounded-lg cursor-pointer ${
            workspace.inspectorMaximized
              ? "bg-[var(--accent-surface)] text-[var(--accent-primary)]"
              : "text-[var(--text-muted)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-hover)]"
          }`}
          onClick={() => setWorkspace("inspectorMaximized", !workspace.inspectorMaximized)}
          title={
            workspace.inspectorMaximized
              ? t("inspector.tabs.restore")
              : t("inspector.tabs.maximize")
          }
        >
          <Show
            when={workspace.inspectorMaximized}
            fallback={<Maximize2 size={13} />}
          >
            <Minimize2 size={13} />
          </Show>
        </button>
      </div>

      <div class="hoxi-tab-stack flex-1 min-h-0">
        <Suspense>
          <Show when={visitedTabs().diffs}>
            <div class="hoxi-tab-panel" data-active={workspace.inspectorTab === "diffs" ? "true" : "false"} aria-hidden={workspace.inspectorTab !== "diffs"}>
              <PierreDiffViewer />
            </div>
          </Show>
          <Show when={visitedTabs().memory}>
            <div class="hoxi-tab-panel" data-active={workspace.inspectorTab === "memory" ? "true" : "false"} aria-hidden={workspace.inspectorTab !== "memory"}>
              <MemoryBankView />
            </div>
          </Show>
          <Show when={visitedTabs().files}>
            <div class="hoxi-tab-panel" data-active={workspace.inspectorTab === "files" ? "true" : "false"} aria-hidden={workspace.inspectorTab !== "files"}>
              <FileTreeView />
            </div>
          </Show>
          <Show when={visitedTabs().skills}>
            <div class="hoxi-tab-panel" data-active={workspace.inspectorTab === "skills" ? "true" : "false"} aria-hidden={workspace.inspectorTab !== "skills"}>
              <SkillsInspectorView />
            </div>
          </Show>
          <Show when={visitedTabs().terminal}>
            <div class="hoxi-tab-panel" data-active={workspace.inspectorTab === "terminal" ? "true" : "false"} aria-hidden={workspace.inspectorTab !== "terminal"}>
              <WebGLTerminal />
            </div>
          </Show>
          <Show when={visitedTabs().browser}>
            <div class="hoxi-tab-panel" data-active={workspace.inspectorTab === "browser" ? "true" : "false"} aria-hidden={workspace.inspectorTab !== "browser"}>
              <BrowserInspector />
            </div>
          </Show>
          <Show when={visitedTabs().tasks}>
            <div class="hoxi-tab-panel" data-active={workspace.inspectorTab === "tasks" ? "true" : "false"} aria-hidden={workspace.inspectorTab !== "tasks"}>
              <TaskOverviewView />
            </div>
          </Show>
        </Suspense>
      </div>
    </aside>
  );
}
