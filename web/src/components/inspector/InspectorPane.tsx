import { createEffect, createMemo, createSignal, For, lazy, Show, Suspense } from "solid-js";
import { Maximize2, Minimize2 } from "lucide-solid";
import MemoryBankView from "./MemoryBankView";
import FileTreeView from "./FileTreeView";
import { setWorkspace, workspace } from "../../store/workspace";
import { collectCycleChangedFiles } from "../timeline/tool-card-state";
import { t } from "../../i18n";

const PierreDiffViewer = lazy(() => import("./PierreDiffViewer"));
const WebGLTerminal = lazy(() => import("../terminal/WebGLTerminal"));
const BrowserInspector = lazy(() => import("./BrowserInspector"));

type TabID = "diffs" | "memory" | "files" | "terminal" | "browser";

const TABS: Array<{ id: TabID }> = [
  { id: "diffs" },
  { id: "memory" },
  { id: "files" },
  { id: "terminal" },
  { id: "browser" },
];

export default function InspectorPane() {
  const [visitedTabs, setVisitedTabs] = createSignal<Record<TabID, boolean>>({
    diffs: workspace.inspectorTab === "diffs",
    memory: workspace.inspectorTab === "memory",
    files: workspace.inspectorTab === "files",
    terminal: workspace.inspectorTab === "terminal",
    browser: workspace.inspectorTab === "browser",
  });

  createEffect(() => {
    const active = workspace.inspectorTab;
    setVisitedTabs((prev) => (prev[active] ? prev : { ...prev, [active]: true }));
  });

  const diffCount = createMemo(() => {
    const id = workspace.activeSessionID;
    if (workspace.diffScope === "worktree") {
      return (workspace.worktreeDiffsBySession[id || "__root__"] ?? []).length;
    }
    if (id) {
      const sDiffs = workspace.diffsBySession[id];
      if (sDiffs && sDiffs.length > 0) return sDiffs.length;
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
      case "terminal":
        return t("inspector.tabs.terminal");
      case "browser":
        return t("inspector.tabs.browser");
    }
  };

  return (
    <aside class="h-full w-full flex flex-col bg-transparent">
      <div class="shrink-0 flex items-center gap-1 px-2 py-1.5 border-b border-[var(--border-subtle)] overflow-x-auto no-scrollbar">
        <For each={TABS}>
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

      <div class="flex-1 min-h-0">
        <Suspense>
          <Show when={visitedTabs().diffs}>
            <div class={workspace.inspectorTab === "diffs" ? "h-full w-full hoxi-view-enter" : "hidden"}>
              <PierreDiffViewer />
            </div>
          </Show>
          <Show when={visitedTabs().memory}>
            <div class={workspace.inspectorTab === "memory" ? "h-full w-full hoxi-view-enter" : "hidden"}>
              <MemoryBankView />
            </div>
          </Show>
          <Show when={visitedTabs().files}>
            <div class={workspace.inspectorTab === "files" ? "h-full w-full hoxi-view-enter" : "hidden"}>
              <FileTreeView />
            </div>
          </Show>
          <Show when={visitedTabs().terminal}>
            <div class={workspace.inspectorTab === "terminal" ? "h-full w-full hoxi-fade-enter" : "hidden"}>
              <WebGLTerminal />
            </div>
          </Show>
          <Show when={workspace.inspectorTab === "browser"}>
            <div class="h-full w-full hoxi-view-enter">
              <BrowserInspector />
            </div>
          </Show>
        </Suspense>
      </div>
    </aside>
  );
}
