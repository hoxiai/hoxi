import { createSignal, For, onCleanup, Show } from "solid-js";
import { hoxiAPI } from "../../services/api";
import { FileIcon, Icon, Spinner } from "../icons";

export type FileTreeNode = { path: string; name?: string; type?: string };

export default function FileTreeNodes(props: {
  nodes: FileTreeNode[];
  directory?: string;
  selected: string | null;
  onOpen: (path: string) => void;
  depth?: number;
}) {
  return <For each={props.nodes}>{(node) => {
    const [expanded, setExpanded] = createSignal(false);
    const [children, setChildren] = createSignal<FileTreeNode[]>();
    const [loading, setLoading] = createSignal(false);
    let disposed = false;
    onCleanup(() => { disposed = true; });
    const isDirectory = () => node.type === "directory" || node.path.endsWith("/");
    const toggle = async () => {
      if (!isDirectory()) { props.onOpen(node.path); return; }
      setExpanded((value) => !value);
      if (!expanded() || children() || loading()) return;
      setLoading(true);
      try {
        const result = await hoxiAPI.listFiles(node.path, props.directory);
        if (!disposed) setChildren(result ?? []);
      } catch {
        if (!disposed) setExpanded(false);
      } finally {
        if (!disposed) setLoading(false);
      }
    };
    return <>
      <button
        type="button"
        aria-expanded={isDirectory() ? expanded() : undefined}
        aria-current={!isDirectory() && props.selected === node.path ? "true" : undefined}
        onClick={() => void toggle()}
        style={{ "padding-left": `${10 + (props.depth ?? 0) * 14}px` }}
        class={`w-full flex items-center gap-2 rounded-lg pr-2.5 py-1.5 text-left transition-colors cursor-pointer ${
          props.selected === node.path
            ? "bg-[var(--accent-surface)] text-[var(--accent-primary)]"
            : "text-[var(--text-primary)] hover:bg-[var(--bg-hover)]"
        }`}
      >
        <span class="w-3.5 shrink-0">
          <Show when={isDirectory()}>
            <Show when={loading()} fallback={<Icon name={expanded() ? "chevron-down" : "chevron-right"} size="small" />}>
              <Spinner class="w-3.5 h-3.5" />
            </Show>
          </Show>
        </span>
        <FileIcon node={{ path: node.path, type: isDirectory() ? "directory" : "file" }} expanded={expanded()} class="w-4 h-4 shrink-0" />
        <span class="truncate font-mono text-[11.5px]" title={node.path}>
          {node.name || node.path.replace(/\/$/, "").split("/").pop()}
        </span>
      </button>
      <Show when={expanded() && children()}>{(nodes) =>
        <FileTreeNodes nodes={nodes()} directory={props.directory} selected={props.selected} onOpen={props.onOpen} depth={(props.depth ?? 0) + 1} />
      }</Show>
    </>;
  }}</For>;
}
