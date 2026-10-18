// web/src/components/inspector/pierre-diff-parts.tsx
import { createEffect, createSignal, onCleanup, Show } from "solid-js";
import {
  ArrowLeft,
  ChevronLeft,
  ChevronRight,
  ExternalLink,
  GitBranch,
  MessageSquarePlus,
  RotateCcw,
} from "lucide-solid";
import { DIFFS_TAG_NAME, FileDiff, getSingularPatch, parseDiffFromFile } from "@pierre/diffs";
import { hoxiAPI } from "../../services/api";
import {
  activeDirectory,
  addReviewComment,
  loadSessionMessages,
  openFileInInspector,
  refreshWorktreeStatus,
  synthesizeLocalCommitMessage,
  workspace,
} from "../../store/workspace";
import type { SnapshotFileDiff } from "../../services/sse-types";
import { DiffChanges, FileIcon } from "../icons";
import { t } from "../../i18n";
import { DiffStatusBadge } from "./diff-file-tree";

export function synthesizeAICommitMessage(
  sessionID: string | null | undefined,
  diffs: SnapshotFileDiff[]
): string {
  return synthesizeLocalCommitMessage(sessionID, diffs);
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
  fillHeight?: boolean;
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
      return;
    }

    const el = document.createElement(DIFFS_TAG_NAME) as HTMLElement;
    el.style.display = "block";
    host.appendChild(el);

    let instance: FileDiff<undefined, undefined> | undefined;
    try {
      let meta = patch ? getSingularPatch(patch) : undefined;
      if ((!meta || !meta.hunks?.length) && hasBeforeAfter) {
        meta = parseDiffFromFile(
          { name: diffObj?.file || "file", contents: diffObj?.before ?? "" },
          { name: diffObj?.file || "file", contents: diffObj?.after ?? "" }
        );
      }
      if (!meta) {
        meta = getSingularPatch(patch);
      }
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
      hidden={
        !(props.patch ?? props.diff?.patch) &&
        typeof props.diff?.before !== "string" &&
        typeof props.diff?.after !== "string"
      }
      class="rounded-lg border border-[var(--border-subtle)] bg-[var(--bg-code)] overflow-auto text-[11px]"
      style={
        props.fillHeight
          ? undefined
          : { "max-height": `${props.maxHeight ?? (props.compact ? 240 : 360)}px` }
      }
    />
  );
}

/**
 * Single-File Diff Detail View:
 * Mounts only 1 @pierre/diffs instance for the selected file, with back-to-tree
 * navigation, prev/next file stepper, inline review comment drawer, and file revert.
 */
export function SelectedDiffDetail(props: {
  diff: SnapshotFileDiff;
  diffStyle: "unified" | "split";
  currentIndex: number;
  totalFiles: number;
  onBack: () => void;
  onPrev: () => void;
  onNext: () => void;
}) {
  const [commenting, setCommenting] = createSignal(false);
  const [reverting, setReverting] = createSignal(false);
  const [lineNum, setLineNum] = createSignal("");
  const [commentBody, setCommentBody] = createSignal("");

  const dirPart = () => {
    const idx = props.diff.file.lastIndexOf("/");
    return idx >= 0 ? props.diff.file.slice(0, idx + 1) : "";
  };

  const baseName = () => {
    const idx = props.diff.file.lastIndexOf("/");
    return idx >= 0 ? props.diff.file.slice(idx + 1) : props.diff.file;
  };

  const onRevertFile = async () => {
    const sessionID = workspace.activeSessionID || "__root__";
    if (reverting()) return;
    setReverting(true);
    try {
      await hoxiAPI.revertWorktreeFile(sessionID, props.diff.file, activeDirectory());
      await refreshWorktreeStatus(workspace.activeSessionID);
      if (workspace.activeSessionID) {
        await loadSessionMessages(workspace.activeSessionID);
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
      {/* Row 1: Back to Tree & Prev/Next Stepper + File Actions */}
      <div class="flex items-center justify-between gap-2 px-3 py-2 border-b border-[var(--border-subtle)] bg-[var(--bg-panel)]/70">
        <button
          type="button"
          onClick={() => props.onBack()}
          class="inline-flex items-center gap-1.5 rounded-lg border border-[var(--border-subtle)] bg-[var(--bg-inset)] px-2 py-1 text-[11px] font-medium text-[var(--text-secondary)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-hover)] transition-colors cursor-pointer"
        >
          <ArrowLeft size={12} />
          <span>{t("inspector.diffs.backToTree")}</span>
        </button>

        <div class="flex items-center gap-1.5">
          <Show when={props.totalFiles > 1}>
            <div class="inline-flex items-center rounded-lg border border-[var(--border-subtle)] bg-[var(--bg-inset)] p-0.5">
              <button
                type="button"
                disabled={props.currentIndex <= 0}
                onClick={() => props.onPrev()}
                class="rounded p-1 text-[var(--text-muted)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-hover)] disabled:opacity-35 transition-colors cursor-pointer"
                title={t("inspector.diffs.prevFileTooltip")}
              >
                <ChevronLeft size={12} />
              </button>
              <span class="px-1.5 font-mono text-[10.5px] text-[var(--text-secondary)]">
                {props.currentIndex + 1} / {props.totalFiles}
              </span>
              <button
                type="button"
                disabled={props.currentIndex >= props.totalFiles - 1}
                onClick={() => props.onNext()}
                class="rounded p-1 text-[var(--text-muted)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-hover)] disabled:opacity-35 transition-colors cursor-pointer"
                title={t("inspector.diffs.nextFileTooltip")}
              >
                <ChevronRight size={12} />
              </button>
            </div>
          </Show>

          <button
            type="button"
            class="shrink-0 inline-flex items-center gap-1 rounded-md px-1.5 py-1 text-[12px] text-[var(--text-muted)] hover:text-[var(--warn-text)] hover:bg-[var(--warn-surface)] transition-colors cursor-pointer disabled:opacity-50"
            disabled={reverting()}
            onClick={() => void onRevertFile()}
            title={t("inspector.diffs.revertFileTooltip")}
          >
            <RotateCcw size={11} />
            <span>{t("inspector.diffs.revertFileBtn")}</span>
          </button>

          <button
            type="button"
            class="shrink-0 inline-flex items-center gap-1 rounded-md px-1.5 py-1 text-[12px] text-[var(--text-muted)] hover:text-[var(--accent-primary)] hover:bg-[var(--bg-hover)] transition-colors cursor-pointer"
            onClick={() => setCommenting((v) => !v)}
            title={t("inspector.diffs.comment")}
          >
            <MessageSquarePlus size={12} />
            <span>{t("inspector.diffs.comment")}</span>
          </button>

          <button
            type="button"
            class="shrink-0 text-[var(--text-muted)] hover:text-[var(--text-primary)] p-1 cursor-pointer"
            onClick={() => openFileInInspector(props.diff.file)}
            title={t("inspector.diffs.openFile")}
          >
            <ExternalLink size={12} />
          </button>
        </div>
      </div>

      {/* Row 2: Active File Path & Line Changes Summary */}
      <div class="flex items-center justify-between gap-2 px-3 py-2 border-b border-[var(--border-subtle)] bg-[var(--bg-inset)]">
        <div class="flex items-center gap-2 min-w-0 flex-1">
          <FileIcon node={{ path: props.diff.file, type: "file" }} class="w-4 h-4 shrink-0" />
          <div class="truncate font-mono text-[11.5px]" title={props.diff.file}>
            <Show when={dirPart()}>
              <span class="text-[var(--text-muted)]">{dirPart()}</span>
            </Show>
            <span class="font-semibold text-[var(--text-primary)]">{baseName()}</span>
          </div>
          <DiffStatusBadge status={props.diff.status} />
          <Show when={props.diff.repo}>
            <span
              class="shrink-0 inline-flex items-center gap-1 rounded bg-[var(--accent-surface)] px-1.5 py-0 text-[10px] font-mono text-[var(--accent-primary)] border border-[var(--accent-border)]"
              title={
                props.diff.branch
                  ? `${props.diff.repo} (${props.diff.branch})`
                  : props.diff.repo
              }
            >
              <GitBranch size={10} />
              <span>
                {props.diff.repo}
                {props.diff.branch ? `:${props.diff.branch}` : ""}
              </span>
            </span>
          </Show>
        </div>

        <div class="flex items-center gap-2 shrink-0">
          <DiffChanges
            changes={{ additions: props.diff.additions, deletions: props.diff.deletions }}
          />
          <DiffChanges
            changes={{ additions: props.diff.additions, deletions: props.diff.deletions }}
            variant="bars"
          />
        </div>
      </div>

      {/* Optional Inline Review Comment Form */}
      <Show when={commenting()}>
        <div class="border-b border-[var(--border-subtle)] bg-[var(--bg-panel)] px-3 py-2.5 space-y-2">
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
              class="rounded-md bg-[var(--accent-primary)] px-2.5 py-1 text-[11px] font-medium text-[var(--accent-foreground)] disabled:opacity-40 cursor-pointer"
            >
              {t("inspector.diffs.addToReview")}
            </button>
          </div>
        </div>
      </Show>

      {/* Single File Diff Body */}
      <Show
        when={
          props.diff.patch ||
          typeof props.diff.before === "string" ||
          typeof props.diff.after === "string"
        }
      >
        <div class="p-2">
          <FileDiffBlock
            diff={props.diff}
            patch={props.diff.patch}
            diffStyle={props.diffStyle}
            fillHeight={true}
          />
        </div>
      </Show>
    </div>
  );
}
