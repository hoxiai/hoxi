// web/src/components/inspector/diff-file-tree.tsx
import { createMemo, createSignal, For, Show } from "solid-js";
import {
  ChevronDown,
  ChevronRight,
  ChevronsDownUp,
  ChevronsUpDown,
  ExternalLink,
  Folder,
  FolderOpen,
  FolderTree,
  GitBranch,
  MessageSquareText,
  RotateCcw,
} from "lucide-solid";
import { hoxiAPI } from "../../services/api";
import type { SnapshotFileDiff } from "../../services/sse-types";
import {
  activeDirectory,
  loadSessionMessages,
  openFileInInspector,
  refreshWorktreeStatus,
  workspace,
} from "../../store/workspace";
import { DiffChanges, FileIcon } from "../icons";
import { t } from "../../i18n";

interface RawFolderNode {
  name: string;
  path: string;
  folders: Map<string, RawFolderNode>;
  files: SnapshotFileDiff[];
  fileCount: number;
  additions: number;
  deletions: number;
}

export interface FlatFolderRow {
  kind: "folder";
  id: string;
  label: string;
  depth: number;
  fileCount: number;
  additions: number;
  deletions: number;
}

export interface FlatFileRow {
  kind: "file";
  id: string;
  name: string;
  depth: number;
  diff: SnapshotFileDiff;
}

export type FlatDiffTreeRow = FlatFolderRow | FlatFileRow;

function createRawFolder(name: string, path: string): RawFolderNode {
  return {
    name,
    path,
    folders: new Map(),
    files: [],
    fileCount: 0,
    additions: 0,
    deletions: 0,
  };
}

function computeStatsAndCompress(node: RawFolderNode): void {
  for (const child of node.folders.values()) {
    computeStatsAndCompress(child);
  }

  // Compress single-child folder chains (e.g., "web" -> "src" -> "components")
  while (node.path !== "" && node.files.length === 0 && node.folders.size === 1) {
    const onlyChild = Array.from(node.folders.values())[0];
    node.name = `${node.name}/${onlyChild.name}`;
    node.path = onlyChild.path;
    node.folders = onlyChild.folders;
    node.files = onlyChild.files;
  }

  let count = node.files.length;
  let add = 0;
  let del = 0;
  for (const f of node.files) {
    add += f.additions || 0;
    del += f.deletions || 0;
  }
  for (const child of node.folders.values()) {
    count += child.fileCount;
    add += child.additions;
    del += child.deletions;
  }
  node.fileCount = count;
  node.additions = add;
  node.deletions = del;
}

export function stripRepoPrefixFromFile(file: string, repo?: string): string {
  const clean = file.replace(/^\/+/, "");
  const prefix = (repo || "").replace(/^\/+|\/+$/g, "");
  if (prefix && clean.startsWith(`${prefix}/`)) {
    return clean.slice(prefix.length + 1);
  }
  return clean;
}

function buildDiffTreeRoot(diffs: SnapshotFileDiff[], stripRepoPrefix?: string): RawFolderNode {
  const root = createRawFolder("", "");
  for (const diff of diffs) {
    const cleanPath = stripRepoPrefixFromFile(diff.file, stripRepoPrefix);
    const parts = cleanPath.split("/").filter(Boolean);
    if (parts.length <= 1) {
      root.files.push(diff);
      continue;
    }
    let current = root;
    let currentPath = "";
    for (let i = 0; i < parts.length - 1; i++) {
      const seg = parts[i];
      currentPath = currentPath ? `${currentPath}/${seg}` : seg;
      let next = current.folders.get(seg);
      if (!next) {
        next = createRawFolder(seg, currentPath);
        current.folders.set(seg, next);
      }
      current = next;
    }
    current.files.push(diff);
  }
  computeStatsAndCompress(root);
  return root;
}

function collectAllFolderIds(node: RawFolderNode, out: string[] = []): string[] {
  if (node.path) out.push(node.path);
  for (const child of node.folders.values()) {
    collectAllFolderIds(child, out);
  }
  return out;
}

function flattenDiffTree(
  node: RawFolderNode,
  collapsed: Record<string, boolean>,
  depth: number,
  rows: FlatDiffTreeRow[]
): void {
  const sortedFolders = Array.from(node.folders.values()).sort((a, b) =>
    a.name.localeCompare(b.name)
  );
  for (const folder of sortedFolders) {
    rows.push({
      kind: "folder",
      id: folder.path,
      label: folder.name,
      depth,
      fileCount: folder.fileCount,
      additions: folder.additions,
      deletions: folder.deletions,
    });
    if (!collapsed[folder.path]) {
      flattenDiffTree(folder, collapsed, depth + 1, rows);
    }
  }

  const sortedFiles = [...node.files].sort((a, b) => {
    const aName = a.file.split("/").pop() || a.file;
    const bName = b.file.split("/").pop() || b.file;
    return aName.localeCompare(bName);
  });
  for (const diff of sortedFiles) {
    const name = diff.file.split("/").pop() || diff.file;
    rows.push({
      kind: "file",
      id: diff.file,
      name,
      depth,
      diff,
    });
  }
}

export function DiffStatusBadge(props: { status?: SnapshotFileDiff["status"] }) {
  const code = () => {
    if (props.status === "added") return "A";
    if (props.status === "deleted") return "D";
    return "M";
  };
  const label = () => {
    if (props.status === "added") return t("inspector.diffs.statusAdded");
    if (props.status === "deleted") return t("inspector.diffs.statusDeleted");
    return t("inspector.diffs.statusModified");
  };
  const cls = () => {
    if (props.status === "added") {
      return "bg-[var(--ok-surface)] text-[var(--ok-text)] border-[var(--ok-border)]";
    }
    if (props.status === "deleted") {
      return "bg-[var(--danger-surface)] text-[var(--danger-text)] border-[var(--danger-border)]";
    }
    return "bg-[var(--accent-surface)] text-[var(--accent-primary)] border-[var(--accent-border)]";
  };

  return (
    <span
      class={`shrink-0 inline-flex items-center justify-center rounded border px-1 py-0 text-[9.5px] font-mono font-semibold leading-4 ${cls()}`}
      title={label()}
    >
      {code()}
    </span>
  );
}

export function GeneratedFileBadge(props: { generated?: boolean }) {
  return (
    <Show when={props.generated}>
      <span
        class="shrink-0 inline-flex items-center justify-center rounded border border-[var(--border-subtle)] bg-[var(--bg-inset)] px-1 py-0 text-[9px] font-mono font-medium leading-4 text-[var(--text-muted)]"
        title={t("inspector.diffs.generatedTooltip")}
      >
        {t("inspector.diffs.generatedBadge")}
      </span>
    </Show>
  );
}

export default function DiffFileTree(props: {
  diffs: SnapshotFileDiff[];
  stripRepoPrefix?: string;
  hideHeader?: boolean;
  onSelectFile: (file: string) => void;
}) {
  const [collapsed, setCollapsed] = createSignal<Record<string, boolean>>({});
  const [revertingFile, setRevertingFile] = createSignal<string | null>(null);

  const treeRoot = createMemo(() => buildDiffTreeRoot(props.diffs, props.stripRepoPrefix));
  const allFolderIds = createMemo(() => collectAllFolderIds(treeRoot()));

  const rows = createMemo(() => {
    const list: FlatDiffTreeRow[] = [];
    flattenDiffTree(treeRoot(), collapsed(), 0, list);
    return list;
  });

  const anyCollapsed = createMemo(() => {
    const map = collapsed();
    return allFolderIds().some((id) => Boolean(map[id]));
  });

  const toggleFolder = (folderId: string) => {
    setCollapsed((prev) => ({
      ...prev,
      [folderId]: !prev[folderId],
    }));
  };

  const toggleAllFolders = () => {
    if (anyCollapsed()) {
      setCollapsed({});
    } else {
      const next: Record<string, boolean> = {};
      for (const id of allFolderIds()) {
        next[id] = true;
      }
      setCollapsed(next);
    }
  };

  const commentCountsByFile = createMemo(() => {
    const sid = workspace.activeSessionID;
    const list = sid ? workspace.reviewCommentsBySession[sid] ?? [] : [];
    const counts: Record<string, number> = {};
    for (const c of list) {
      counts[c.file] = (counts[c.file] || 0) + 1;
    }
    return counts;
  });

  const repoBranchByFolder = createMemo(() => {
    const map = new Map<string, string>();
    if (props.stripRepoPrefix) return map;
    for (const d of props.diffs) {
      if (d.repo && !map.has(d.repo)) {
        map.set(d.repo, d.branch || "HEAD");
      }
    }
    return map;
  });

  const folderRepoBranch = (folderRow: FlatFolderRow): string | undefined => {
    const map = repoBranchByFolder();
    const direct = map.get(folderRow.id);
    if (direct) return direct;
    if (folderRow.depth === 0) {
      for (const [repo, br] of map.entries()) {
        if (folderRow.id.startsWith(`${repo}/`)) return br;
      }
    }
    return undefined;
  };

  const onRevertFile = async (e: MouseEvent, diff: SnapshotFileDiff) => {
    e.stopPropagation();
    if (revertingFile()) return;
    setRevertingFile(diff.file);
    try {
      const targetDir = diff.repoDir || activeDirectory() || workspace.rootDir || undefined;
      const targetFile = diff.repoDir ? stripRepoPrefixFromFile(diff.file, diff.repo) : diff.file;
      const sessionID = diff.repoDir ? "__root__" : (workspace.activeSessionID || "__root__");
      await hoxiAPI.revertWorktreeFile(sessionID, targetFile, targetDir);
      await refreshWorktreeStatus(workspace.activeSessionID);
      if (workspace.activeSessionID) {
        await loadSessionMessages(workspace.activeSessionID);
      }
    } catch (err) {
      console.error("revertWorktreeFile failed:", err);
    } finally {
      setRevertingFile(null);
    }
  };

  return (
    <div
      class={
        props.hideHeader
          ? "overflow-hidden"
          : "rounded-xl border border-[var(--border-subtle)] bg-[var(--bg-inset)]/60 overflow-hidden"
      }
    >
      {/* Tree Header Bar */}
      <Show when={!props.hideHeader}>
        <div class="flex items-center justify-between gap-2 px-2.5 py-1 border-b border-[var(--border-subtle)] bg-[var(--bg-inset)]">
          <div class="flex items-center gap-1.5 min-w-0">
            <FolderTree size={13} class="text-[var(--accent-primary)] shrink-0" />
            <span class="text-[11.5px] font-semibold text-[var(--text-primary)] truncate">
              {t("inspector.diffs.treeTitle")}
            </span>
          </div>

          <Show when={allFolderIds().length > 0}>
            <button
              type="button"
              onClick={toggleAllFolders}
              class="shrink-0 inline-flex items-center gap-1 rounded-md px-1.5 py-0.5 text-[10.5px] text-[var(--text-muted)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-hover)] transition-colors cursor-pointer"
              title={
                anyCollapsed()
                  ? t("inspector.diffs.expandAll")
                  : t("inspector.diffs.collapseAll")
              }
            >
              <Show
                when={anyCollapsed()}
                fallback={<ChevronsDownUp size={12} />}
              >
                <ChevronsUpDown size={12} />
              </Show>
              <span>
                {anyCollapsed()
                  ? t("inspector.diffs.expandAll")
                  : t("inspector.diffs.collapseAll")}
              </span>
            </button>
          </Show>
        </div>
      </Show>

      {/* Tree Rows */}
      <div class="divide-y divide-[var(--border-subtle)]/50">
        <For each={rows()}>
          {(row) => (
            <Show
              when={row.kind === "file"}
              fallback={(() => {
                const folderRow = row as FlatFolderRow;
                const repoBranch = () => folderRepoBranch(folderRow);
                return (
                  <button
                    type="button"
                    onClick={() => toggleFolder(folderRow.id)}
                    class="w-full flex items-center justify-between gap-2 py-1.5 pr-3 text-left bg-[var(--bg-inset)]/40 hover:bg-[var(--bg-hover)] transition-colors cursor-pointer"
                    style={{ "padding-left": `${folderRow.depth * 14 + 10}px` }}
                  >
                    <div class="flex items-center gap-1.5 min-w-0 flex-1">
                      <Show
                        when={!collapsed()[folderRow.id]}
                        fallback={
                          <ChevronRight size={12} class="text-[var(--text-muted)] shrink-0" />
                        }
                      >
                        <ChevronDown size={12} class="text-[var(--text-muted)] shrink-0" />
                      </Show>
                      <Show
                        when={!collapsed()[folderRow.id]}
                        fallback={
                          <Folder size={13} class="text-[var(--accent-primary)]/80 shrink-0" />
                        }
                      >
                        <FolderOpen size={13} class="text-[var(--accent-primary)] shrink-0" />
                      </Show>
                      <span
                        class="font-mono text-[11.5px] font-medium text-[var(--text-secondary)] truncate"
                        title={folderRow.id}
                      >
                        {folderRow.label}
                      </span>
                      <Show when={repoBranch()}>
                        {(br) => (
                          <span class="shrink-0 inline-flex items-center gap-0.5 rounded bg-[var(--accent-surface)] px-1.5 py-0 text-[10px] font-mono text-[var(--accent-primary)] border border-[var(--accent-border)]">
                            <GitBranch size={9} />
                            <span>{br()}</span>
                          </span>
                        )}
                      </Show>
                      <span class="rounded-full bg-[var(--bg-panel)] px-1.5 py-0 text-[10px] font-mono text-[var(--text-muted)] border border-[var(--border-subtle)]">
                        {folderRow.fileCount}
                      </span>
                    </div>

                    <div class="flex items-center gap-1.5 shrink-0 opacity-85">
                      <DiffChanges
                        changes={{
                          additions: folderRow.additions,
                          deletions: folderRow.deletions,
                        }}
                      />
                    </div>
                  </button>
                );
              })()}
            >
              {(() => {
                const fileRow = row as FlatFileRow;
                const diff = fileRow.diff;
                const noteCount = () => commentCountsByFile()[diff.file] || 0;
                return (
                  <div
                    role="button"
                    tabIndex={0}
                    onClick={() => props.onSelectFile(diff.file)}
                    onKeyDown={(e) => {
                      if (e.key === "Enter" || e.key === " ") {
                        e.preventDefault();
                        props.onSelectFile(diff.file);
                      }
                    }}
                    class="group w-full flex items-center justify-between gap-2 py-2 pr-2.5 text-left bg-[var(--bg-panel)]/50 hover:bg-[var(--bg-hover)] transition-colors cursor-pointer"
                    style={{ "padding-left": `${fileRow.depth * 14 + 12}px` }}
                    title={diff.file}
                  >
                    <div class="flex items-center gap-1.5 min-w-0 flex-1">
                      <FileIcon
                        node={{ path: diff.file, type: "file" }}
                        class="w-4 h-4 shrink-0"
                      />
                      <span class="font-mono text-[12px] font-medium text-[var(--text-primary)] group-hover:text-[var(--accent-primary)] truncate">
                        {fileRow.name}
                      </span>
                      <DiffStatusBadge status={diff.status} />
                      <GeneratedFileBadge generated={diff.generatedLike} />
                      <Show when={noteCount() > 0}>
                        <span class="inline-flex items-center gap-0.5 rounded bg-[var(--accent-surface)] px-1 py-0 text-[10px] font-mono text-[var(--accent-primary)]">
                          <MessageSquareText size={10} />
                          {noteCount()}
                        </span>
                      </Show>
                    </div>

                    <div class="flex items-center gap-2 shrink-0">
                      <DiffChanges
                        changes={{ additions: diff.additions, deletions: diff.deletions }}
                      />
                      <DiffChanges
                        changes={{ additions: diff.additions, deletions: diff.deletions }}
                        variant="bars"
                      />

                      <div class="hidden group-hover:flex items-center gap-1">
                        <button
                          type="button"
                          disabled={revertingFile() === diff.file}
                          onClick={(e) => void onRevertFile(e, diff)}
                          class="rounded p-1 text-[var(--text-muted)] hover:text-[var(--warn-text)] hover:bg-[var(--warn-surface)] transition-colors cursor-pointer disabled:opacity-40"
                          title={t("inspector.diffs.revertFileTooltip")}
                        >
                          <RotateCcw size={11} />
                        </button>
                        <button
                          type="button"
                          onClick={(e) => {
                            e.stopPropagation();
                            const targetPath = diff.repoDir
                              ? `${diff.repoDir.replace(/\/+$/, "")}/${stripRepoPrefixFromFile(diff.file, diff.repo)}`
                              : diff.file;
                            openFileInInspector(targetPath);
                          }}
                          class="rounded p-1 text-[var(--text-muted)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-panel)] transition-colors cursor-pointer"
                          title={t("inspector.diffs.openFile")}
                        >
                          <ExternalLink size={11} />
                        </button>
                      </div>

                      <ChevronRight
                        size={13}
                        class="text-[var(--text-faint)] group-hover:text-[var(--accent-primary)] group-hover:translate-x-0.5 transition-all shrink-0"
                      />
                    </div>
                  </div>
                );
              })()}
            </Show>
          )}
        </For>
      </div>
    </div>
  );
}
