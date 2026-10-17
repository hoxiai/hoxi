import { createMemo, For, Show } from "solid-js";
import PierreDiffViewer from "./PierreDiffViewer";
import MemoryBankView from "./MemoryBankView";
import FileTreeView from "./FileTreeView";
import WebGLTerminal from "../terminal/WebGLTerminal";
import { setWorkspace, workspace } from "../../store/workspace";

type TabID = "diffs" | "memory" | "files" | "terminal";

const TABS: Array<{ id: TabID; label: string }> = [
  { id: "diffs", label: "Diffs" },
  { id: "memory", label: "Memory" },
  { id: "files", label: "Files" },
  { id: "terminal", label: "Terminal" },
];

export default function InspectorPane() {
  const diffCount = createMemo(() => {
    const id = workspace.activeSessionID;
    return id ? (workspace.diffsBySession[id] ?? []).length : 0;
  });

  const tabLabel = (tab: { id: TabID; label: string }) =>
    tab.id === "diffs" ? `${tab.label} (${diffCount()})` : tab.label;

  return (
    <aside class="h-full w-full flex flex-col bg-transparent">
      <div class="shrink-0 flex items-center gap-1 px-3 py-2 border-b border-[var(--border-subtle)]">
        <For each={TABS}>
          {(tab) => (
            <button
              type="button"
              class={`relative rounded-lg px-2.5 py-1 text-[11.5px] font-medium transition-all cursor-pointer ${
                workspace.inspectorTab === tab.id
                  ? "bg-[var(--accent-surface)] text-[var(--accent-primary)]"
                  : "text-[var(--text-muted)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-hover)]"
              }`}
              onClick={() => setWorkspace("inspectorTab", tab.id)}
            >
              {tabLabel(tab)}
            </button>
          )}
        </For>
      </div>

      <div class="flex-1 min-h-0">
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
      </div>
    </aside>
  );
}
