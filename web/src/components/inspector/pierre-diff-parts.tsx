// web/src/components/inspector/pierre-diff-parts.tsx
import { createEffect, createSignal, onCleanup, Show } from "solid-js";
import {
  ChevronDown,
  ChevronRight,
  ExternalLink,
  MessageSquarePlus,
  RotateCcw,
} from "lucide-solid";
import { DIFFS_TAG_NAME, FileDiff, getSingularPatch, parseDiffFromFile } from "@pierre/diffs";
import { hoxiAPI } from "../../services/api";
import {
  addReviewComment,
  loadSessionMessages,
  openFileInInspector,
  refreshWorktreeStatus,
  workspace,
} from "../../store/workspace";
import type { MessagePart, SnapshotFileDiff } from "../../services/sse-types";
import { DiffChanges, FileIcon } from "../icons";
import { t } from "../../i18n";

export function synthesizeAICommitMessage(
  sessionID: string | null | undefined,
  diffs: SnapshotFileDiff[]
): string {
  const files = diffs.map((d) => d.file);
  const topDirs = Array.from(
    new Set(
      files.map((f) => {
        const parts = f.split("/").filter(Boolean);
        return parts.length > 1
          ? parts[parts.length - 2]
          : parts[0]?.replace(/\.[^.]+$/, "") || "core";
      })
    )
  ).slice(0, 2);
  const scope = topDirs.join(",") || "workspace";

  const session = workspace.sessions.find((s) => s.id === sessionID);
  const turns = sessionID ? workspace.messagesBySession[sessionID] ?? [] : [];
  let rawIntent = session?.title || "";
  if (!rawIntent || rawIntent === "New Session" || rawIntent.startsWith("Session")) {
    for (let i = turns.length - 1; i >= 0; i--) {
      if (turns[i].info.role === "user") {
        const textPart = turns[i].parts.find(
          (p): p is Extract<MessagePart, { type: "text" }> => p.type === "text"
        );
        if (textPart?.text) {
          rawIntent = textPart.text.split("\n")[0].trim();
          break;
        }
      }
    }
  }

  const lower = rawIntent.toLowerCase();
  let prefix = "feat";
  if (/\b(fix|bug|error|issue|crash|repair)\b/.test(lower)) prefix = "fix";
  else if (/\b(refactor|clean|split|modular)\b/.test(lower)) prefix = "refactor";
  else if (/\b(doc|readme|agents\.md)\b/.test(lower)) prefix = "docs";
  else if (/\b(test|spec)\b/.test(lower)) prefix = "test";

  const cleanSummary = rawIntent
    ? rawIntent
        .replace(/^(please\s+|can you\s+|help me\s+)/i, "")
        .replace(/[.!?]+$/, "")
        .slice(0, 64)
        .trim()
    : `update ${files
        .slice(0, 2)
        .map((f) => f.split("/").pop())
        .join(", ")}${files.length > 2 ? ` and ${files.length - 2} more` : ""}`;

  return `${prefix}(${scope}): ${cleanSummary}`;
}

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
    const diffObj = props.diff;
    const hasBeforeAfter =
      Boolean(diffObj) &&
      (typeof diffObj?.before === "string" || typeof diffObj?.after === "string");
    const diffStyle = props.diffStyle ?? "unified";
    const themeType = workspace.theme === "dark" ? "dark" : "light";
    host.innerHTML = "";
    if (!patch && !hasBeforeAfter) {
      host.textContent = t("inspector.diffs.noPatch");
      return;
    }

    const el = document.createElement(DIFFS_TAG_NAME) as HTMLElement;
    el.style.display = "block";
    host.appendChild(el);

    let instance: FileDiff<undefined, undefined> | undefined;
    try {
      const meta = patch
        ? getSingularPatch(patch)
        : parseDiffFromFile(
            { name: diffObj?.file || "file", contents: diffObj?.before ?? "" },
            { name: diffObj?.file || "file", contents: diffObj?.after ?? "" }
          );
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
      el.textContent = patch || diffObj?.after || "";
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
      style={{ "max-height": `${props.maxHeight ?? (props.compact ? 240 : 360)}px` }}
    />
  );
}

export function DiffCard(props: { diff: SnapshotFileDiff; diffStyle: "unified" | "split" }) {
  const [open, setOpen] = createSignal(true);
  const [commenting, setCommenting] = createSignal(false);
  const [reverting, setReverting] = createSignal(false);
  const [lineNum, setLineNum] = createSignal("");
  const [commentBody, setCommentBody] = createSignal("");

  const onRevertFile = async () => {
    const sessionID = workspace.activeSessionID || "__root__";
    if (reverting()) return;
    setReverting(true);
    try {
      await hoxiAPI.revertWorktreeFile(sessionID, props.diff.file);
      if (workspace.activeSessionID) {
        await loadSessionMessages(workspace.activeSessionID);
      } else {
        await refreshWorktreeStatus("__root__");
      }
    } catch (err) {
      console.error("revertWorktreeFile failed:", err);
    } finally {
      setReverting(false);
    }
  };

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
          class="shrink-0 inline-flex items-center gap-1 rounded-md px-1.5 py-0.5 text-[10.5px] text-[var(--text-muted)] hover:text-[var(--warn-text)] hover:bg-[var(--warn-surface)] transition-colors cursor-pointer disabled:opacity-50"
          disabled={reverting()}
          onClick={() => void onRevertFile()}
          title={t("inspector.diffs.revertFileTooltip")}
        >
          <RotateCcw size={11} />
          <span>{t("inspector.diffs.revertFileBtn")}</span>
        </button>

        <button
          type="button"
          class="shrink-0 inline-flex items-center gap-1 rounded-md px-1.5 py-0.5 text-[10.5px] text-[var(--text-muted)] hover:text-[var(--accent-primary)] hover:bg-[var(--bg-panel)] transition-colors cursor-pointer"
          onClick={() => setCommenting((v) => !v)}
          title={t("inspector.diffs.comment")}
        >
          <MessageSquarePlus size={12} />
          {t("inspector.diffs.comment")}
        </button>

        <button
          type="button"
          class="shrink-0 text-[var(--text-muted)] hover:text-[var(--text-primary)] p-0.5 cursor-pointer"
          onClick={() => openFileInInspector(props.diff.file)}
          title={t("inspector.diffs.openFile")}
        >
          <ExternalLink size={12} />
        </button>
      </div>

      <Show when={commenting()}>
        <div class="border-t border-[var(--border-subtle)] bg-[var(--bg-panel)] px-3 py-2.5 space-y-2">
          <div class="flex items-center gap-2">
            <span class="text-[11px] font-medium text-[var(--text-secondary)]">
              {t("inspector.diffs.reviewNoteOn")}{" "}
              <code class="font-mono text-[var(--accent-text)]">{props.diff.file}</code>
            </span>
            <input
              type="number"
              min="1"
              value={lineNum()}
              onInput={(ev) => setLineNum(ev.currentTarget.value)}
              placeholder={t("inspector.diffs.lineNumPlaceholder")}
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
            placeholder={t("inspector.diffs.commentPlaceholder")}
            class="w-full rounded-lg border border-[var(--border-strong)] bg-[var(--bg-inset)] px-2.5 py-1.5 text-[12px] text-[var(--text-primary)] placeholder:text-[var(--text-faint)] outline-none focus:border-[var(--accent-border)]"
          />
          <div class="flex items-center justify-end gap-1.5">
            <button
              type="button"
              onClick={() => setCommenting(false)}
              class="rounded-md px-2.5 py-1 text-[11px] text-[var(--text-muted)] hover:bg-[var(--bg-hover)] cursor-pointer"
            >
              {t("common.cancel")}
            </button>
            <button
              type="button"
              disabled={!commentBody().trim()}
              onClick={onSaveComment}
              class="rounded-md bg-[var(--accent-primary)] px-2.5 py-1 text-[11px] font-medium text-white disabled:opacity-40 cursor-pointer"
            >
              {t("inspector.diffs.addToReview")}
            </button>
          </div>
        </div>
      </Show>

      <Show when={open()}>
        <div class="px-2 pb-2 pt-1">
          <Show
            when={
              props.diff.patch ||
              typeof props.diff.before === "string" ||
              typeof props.diff.after === "string"
            }
            fallback={
              <div class="px-2 py-2 text-[11px] text-[var(--text-muted)] font-mono">
                {t("inspector.diffs.noPatch")}
              </div>
            }
          >
            <FileDiffBlock
              diff={props.diff}
              patch={props.diff.patch}
              diffStyle={props.diffStyle}
              maxHeight={320}
            />
          </Show>
        </div>
      </Show>
    </div>
  );
}
