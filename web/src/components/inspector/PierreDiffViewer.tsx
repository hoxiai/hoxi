import { createEffect, createMemo, createSignal, For, onCleanup, Show } from "solid-js";
import {
  ChevronDown,
  ChevronRight,
  ExternalLink,
  MessageSquarePlus,
  RefreshCw,
} from "lucide-solid";
import { DIFFS_TAG_NAME, FileDiff, getSingularPatch } from "@pierre/diffs";
import {
  activeDirectory,
  addReviewComment,
  loadSessionMessages,
  openFileInInspector,
  workspace,
} from "../../store/workspace";
import type { SnapshotFileDiff } from "../../services/sse-types";
import { DiffChanges, FileIcon } from "../icons";

/**
 * Renders a single unified/split code diff using the @pierre/diffs vanilla
 * renderer mounted into a `<diffs-container>` shadow host.
 */
export function FileDiffBlock(props: {
  patch?: string;
  diff?: SnapshotFileDiff;
  diffStyle?: "unified" | "split";
  maxHeight?: number;
  defaultExpanded?: boolean;
  compact?: boolean;
}) {
  let host!: HTMLDivElement;

  createEffect(() => {
    const patch = props.patch ?? props.diff?.patch ?? "";
    const diffStyle = props.diffStyle ?? "unified";
    const themeType = workspace.theme === "dark" ? "dark" : "light";
    host.innerHTML = "";
    if (!patch) {
      host.textContent = "(diff preview available in Diff Inspector)";
      return;
    }

    const el = document.createElement(DIFFS_TAG_NAME) as HTMLElement;
    el.style.display = "block";
    host.appendChild(el);

    let instance: FileDiff<undefined, undefined> | undefined;
    try {
      const meta = getSingularPatch(patch);
      instance = new FileDiff({
        theme: { dark: "pierre-dark", light: "pierre-light" },
        themeType,
        diffStyle,
        disableFileHeader: true,
        overflow: "scroll",
        hunkSeparators: "simple",
      });
      instance.hydrate({ fileDiff: meta, fileContainer: el });
    } catch (err) {
      console.error("PierreDiff render failed:", err);
      el.textContent = patch;
    }

    onCleanup(() => {
      try {
        instance?.cleanUp();
      } catch {
        /* noop */
      }
    });
  });

  return (
    <div
      ref={host}
      class="rounded-lg border border-[var(--border-subtle)] bg-[var(--bg-code)] overflow-auto text-[11px]"
      style={{ "max-height": `${props.maxHeight ?? (props.compact ? 240 : 360)}px` } as any}
    />
  );
}

function DiffCard(props: { diff: SnapshotFileDiff; diffStyle: "unified" | "split" }) {
  const [open, setOpen] = createSignal(true);
  const [commenting, setCommenting] = createSignal(false);
  const [lineNum, setLineNum] = createSignal("");
  const [commentBody, setCommentBody] = createSignal("");

  const onSaveComment = () => {
    const sessionID = workspace.activeSessionID;
    const body = commentBody().trim();
    if (!sessionID || !body) return;
    const parsedLine = parseInt(lineNum().trim(), 10);
    addReviewComment(sessionID, {
      file: props.diff.file,
      line: Number.isFinite(parsedLine) && parsedLine > 0 ? parsedLine : undefined,
      body,
    });
    setCommentBody("");
    setLineNum("");
    setCommenting(false);
  };

  return (
    <div class="rounded-xl border border-[var(--border-subtle)] bg-[var(--bg-inset)] overflow-hidden">
      <div class="flex items-center gap-2 px-3 py-2 hover:bg-[var(--bg-hover)] transition-colors">
        <button
          type="button"
          class="flex items-center gap-2 flex-1 min-w-0 text-left cursor-pointer"
          onClick={() => setOpen((v) => !v)}
        >
          {open() ? (
            <ChevronDown size={13} class="text-[var(--text-muted)] shrink-0" />
          ) : (
            <ChevronRight size={13} class="text-[var(--text-muted)] shrink-0" />
          )}
          <FileIcon node={{ path: props.diff.file, type: "file" }} class="w-4 h-4 shrink-0" />
          <span
            class="flex-1 truncate font-mono text-[11.5px] font-medium text-[var(--text-primary)]"
            title={props.diff.file}
          >
            {props.diff.file}
          </span>
        </button>

        <DiffChanges
          changes={{ additions: props.diff.additions, deletions: props.diff.deletions }}
        />
        <DiffChanges
          changes={{ additions: props.diff.additions, deletions: props.diff.deletions }}
          variant="bars"
        />

        <button
          type="button"
          class="shrink-0 inline-flex items-center gap-1 rounded-md px-1.5 py-0.5 text-[10.5px] text-[var(--text-muted)] hover:text-[var(--accent-primary)] hover:bg-[var(--bg-panel)] transition-colors cursor-pointer"
          onClick={() => setCommenting((v) => !v)}
          title="Add inline review comment for the agent"
        >
          <MessageSquarePlus size={12} />
          Comment
        </button>

        <button
          type="button"
          class="shrink-0 text-[var(--text-muted)] hover:text-[var(--text-primary)] p-0.5 cursor-pointer"
          onClick={() => openFileInInspector(props.diff.file)}
          title="Open file in Inspector"
        >
          <ExternalLink size={12} />
        </button>
      </div>

      {/* Inline Review Comment Composer (ported from kilocode diff-comment-forms) */}
      <Show when={commenting()}>
        <div class="border-t border-[var(--border-subtle)] bg-[var(--bg-panel)] px-3 py-2.5 space-y-2">
          <div class="flex items-center gap-2">
            <span class="text-[11px] font-medium text-[var(--text-secondary)]">
              Review note on <code class="font-mono text-[var(--accent-text)]">{props.diff.file}</code>
            </span>
            <input
              type="number"
              min="1"
              value={lineNum()}
              onInput={(ev) => setLineNum(ev.currentTarget.value)}
              placeholder="Line # (opt)"
              class="ml-auto w-24 rounded-md border border-[var(--border-subtle)] bg-[var(--bg-inset)] px-2 py-0.5 font-mono text-[11px] text-[var(--text-primary)] outline-none focus:border-[var(--accent-border)]"
            />
          </div>
          <textarea
            rows={2}
            value={commentBody()}
            onInput={(ev) => setCommentBody(ev.currentTarget.value)}
            onKeyDown={(ev) => {
              if (ev.key === "Enter" && !ev.shiftKey && !ev.isComposing) {
                ev.preventDefault();
                onSaveComment();
              } else if (ev.key === "Escape") {
                setCommenting(false);
              }
            }}
            placeholder="Describe what to change on this file/line (Enter to queue)…"
            class="w-full rounded-lg border border-[var(--border-strong)] bg-[var(--bg-inset)] px-2.5 py-1.5 text-[12px] text-[var(--text-primary)] placeholder:text-[var(--text-faint)] outline-none focus:border-[var(--accent-border)]"
          />
          <div class="flex items-center justify-end gap-1.5">
            <button
              type="button"
              onClick={() => setCommenting(false)}
              class="rounded-md px-2.5 py-1 text-[11px] text-[var(--text-muted)] hover:bg-[var(--bg-hover)] cursor-pointer"
            >
              Cancel
            </button>
            <button
              type="button"
              disabled={!commentBody().trim()}
              onClick={onSaveComment}
              class="rounded-md bg-[var(--accent-primary)] px-2.5 py-1 text-[11px] font-medium text-white disabled:opacity-40 cursor-pointer"
            >
              Add to Review
            </button>
          </div>
        </div>
      </Show>

      <Show when={open()}>
        <div class="px-2 pb-2 pt-1">
          <Show
            when={props.diff.patch}
            fallback={
              <div class="px-2 py-2 text-[11px] text-[var(--text-muted)] font-mono">
                (no patch payload for this file)
              </div>
            }
          >
            <FileDiffBlock patch={props.diff.patch!} diffStyle={props.diffStyle} maxHeight={320} />
          </Show>
        </div>
      </Show>
    </div>
  );
}

export default function PierreDiffViewer() {
  const [diffStyle, setDiffStyle] = createSignal<"unified" | "split">("unified");
  const [refreshing, setRefreshing] = createSignal(false);

  const diffs = createMemo(() => {
    const id = workspace.activeSessionID;
    return id ? workspace.diffsBySession[id] ?? [] : [];
  });

  const totals = createMemo(() => {
    let add = 0;
    let del = 0;
    for (const d of diffs()) {
      add += d.additions || 0;
      del += d.deletions || 0;
    }
    return { add, del };
  });

  const onRefresh = async () => {
    const id = workspace.activeSessionID;
    if (!id || refreshing()) return;
    setRefreshing(true);
    try {
      await loadSessionMessages(id);
    } catch (err) {
      console.error(err);
    } finally {
      setRefreshing(false);
    }
  };

  return (
    <div class="flex flex-col h-full">
      <div class="shrink-0 flex items-center justify-between px-3 py-2 border-b border-[var(--border-subtle)]">
        <div class="flex items-center gap-2 text-[11.5px] text-[var(--text-secondary)]">
          <span class="font-mono">
            <span class="text-[var(--ok-text)]">+{totals().add}</span>{" "}
            <span class="text-[var(--danger-text)]">-{totals().del}</span>
          </span>
          <span class="text-[var(--text-faint)]">·</span>
          <span>{diffs().length} files</span>
        </div>
        <div class="flex items-center gap-1.5">
          <div class="flex rounded-lg border border-[var(--border-subtle)] bg-[var(--bg-inset)] p-0.5">
            <button
              type="button"
              class={`rounded-md px-2 py-0.5 text-[10.5px] font-medium transition-colors cursor-pointer ${
                diffStyle() === "unified"
                  ? "bg-[var(--bg-panel)] text-[var(--accent-primary)] shadow-2xs"
                  : "text-[var(--text-muted)] hover:text-[var(--text-primary)]"
              }`}
              onClick={() => setDiffStyle("unified")}
            >
              Unified
            </button>
            <button
              type="button"
              class={`rounded-md px-2 py-0.5 text-[10.5px] font-medium transition-colors cursor-pointer ${
                diffStyle() === "split"
                  ? "bg-[var(--bg-panel)] text-[var(--accent-primary)] shadow-2xs"
                  : "text-[var(--text-muted)] hover:text-[var(--text-primary)]"
              }`}
              onClick={() => setDiffStyle("split")}
            >
              Split
            </button>
          </div>
          <button
            type="button"
            class="p-1 text-[var(--text-muted)] hover:text-[var(--text-primary)] transition-colors cursor-pointer"
            onClick={onRefresh}
            title="Refresh session diff"
          >
            <RefreshCw size={13} class={refreshing() ? "animate-spin" : ""} />
          </button>
        </div>
      </div>

      <div class="flex-1 min-h-0 overflow-y-auto px-2.5 py-2.5 space-y-2.5">
        <Show
          when={diffs().length > 0}
          fallback={
            <div class="px-3 py-10 text-center text-[12px] text-[var(--text-muted)]">
              No code changes recorded for this session yet.
              <div class="mt-1 text-[10.5px] text-[var(--text-faint)] font-mono truncate">
                {activeDirectory() || "—"}
              </div>
            </div>
          }
        >
          <For each={diffs()}>
            {(diff) => <DiffCard diff={diff} diffStyle={diffStyle()} />}
          </For>
        </Show>
      </div>
    </div>
  );
}
