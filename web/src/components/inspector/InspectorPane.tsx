import { createMemo, For, lazy, Show, Suspense } from "solid-js";
import { Maximize2, Minimize2 } from "lucide-solid";
import MemoryBankView from "./MemoryBankView";
import FileTreeView from "./FileTreeView";
import { setWorkspace, workspace } from "../../store/workspace";
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
  const diffCount = createMemo(() => {
    const id = workspace.activeSessionID;
    if (workspace.diffScope === "worktree") {
      return (workspace.worktreeDiffsBySession[id || "__root__"] ?? []).length;
    }
    if (id) {
      const sDiffs = workspace.diffsBySession[id];
      if (sDiffs && sDiffs.length > 0) return sDiffs.length;
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
      <div class="shrink-0 flex flex-wrap items-center gap-1 px-3 py-2 border-b border-[var(--border-subtle)]">
        <For each={TABS}>
          {(tab) => (
            <button
              type="button"
              class={`relative rounded-lg px-3 py-1.5 text-[12.5px] font-medium transition-all cursor-pointer ${
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
          class={`ml-auto inline-flex items-center justify-center w-6.5 h-6.5 rounded-lg transition-colors cursor-pointer ${
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
        <Show when={workspace.inspectorTab === "diffs"}>
          <PierreDiffViewer />
        </Show>
        <Show when={workspace.inspectorTab === "memory"}>
          <MemoryBankView />
        </Show>
        <Show when={workspace.inspectorTab === "files"}>
          <FileTreeView />
        </Show>
        <Show when={workspace.inspectorTab === "terminal"}>
          <WebGLTerminal />
        </Show>
        <Show when={workspace.inspectorTab === "browser"}>
          <BrowserInspector />
        </Show>
        </Suspense>
      </div>
    </aside>
  );
}
