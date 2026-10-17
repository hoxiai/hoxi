import { createMemo, For, Show } from "solid-js";
import PierreDiffViewer from "./PierreDiffViewer";
import MemoryBankView from "./MemoryBankView";
import FileTreeView from "./FileTreeView";
import WebGLTerminal from "../terminal/WebGLTerminal";
import { setWorkspace, workspace } from "../../store/workspace";
import { t } from "../../i18n";

type TabID = "diffs" | "memory" | "files" | "terminal";

const TABS: Array<{ id: TabID }> = [
  { id: "diffs" },
  { id: "memory" },
  { id: "files" },
  { id: "terminal" },
];

export default function InspectorPane() {
  const diffCount = createMemo(() => {
    const id = workspace.activeSessionID;
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
    }
  };

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
              {tabLabel(tab.id)}
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
