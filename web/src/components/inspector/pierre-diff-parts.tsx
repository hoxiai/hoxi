// web/src/components/inspector/pierre-diff-parts.tsx
import { createEffect, createSignal, on, onCleanup, Show } from "solid-js";
import {
  ArrowLeft,
  ChevronLeft,
  ChevronRight,
  ExternalLink,
  GitBranch,
  MessageSquarePlus,
  RotateCcw,
} from "lucide-solid";
import {
  FileDiff,
  type FileDiffMetadata,
  type FileDiffOptions,
  getSingularPatch,
  parseDiffFromFile,
} from "@pierre/diffs";
import { WorkerPoolManager } from "@pierre/diffs/worker";
import { hoxiAPI } from "../../services/api";
import {
  activeDirectory,
  addReviewComment,
  loadSessionMessagesOnly,
  openFileInInspector,
  refreshWorktreeStatus,
  synthesizeLocalCommitMessage,
  workspace,
} from "../../store/workspace";
import type { SnapshotFileDiff } from "../../services/sse-types";
import { DiffChanges, FileIcon } from "../icons";
import { t } from "../../i18n";
import { DiffStatusBadge, GeneratedFileBadge } from "./diff-file-tree";

const MAX_META_CACHE_ENTRIES = 48;
const MAX_FULL_DIFF_CACHE_ENTRIES = 32;
const LARGE_DIFF_LINE_THRESHOLD = 450;
const LARGE_DIFF_BYTE_THRESHOLD = 64 * 1024;

const fileDiffMetaCache = new Map<string, FileDiffMetadata>();
const fullDiffDetailCache = new Map<string, SnapshotFileDiff>();

let unifiedWorkerPool: WorkerPoolManager | undefined;
let splitWorkerPool: WorkerPoolManager | undefined;

function createPierreWorkerPool(lineDiffType: "none" | "word-alt"): WorkerPoolManager | undefined {
  if (typeof window === "undefined" || typeof Worker === "undefined") return undefined;
  try {
    const pool = new WorkerPoolManager(
      {
        workerFactory: () =>
          new Worker(new URL("@pierre/diffs/worker/worker.js", import.meta.url), {
            type: "module",
          }),
        poolSize: 2,
        totalASTLRUCacheSize: 120,
        onWorkerError: (event) => {
          event.preventDefault?.();
        },
      },
      {
        theme: { dark: "pierre-dark", light: "pierre-light" },
        lineDiffType,
        preferredHighlighter: "shiki-wasm",
      }
    );
    void pool.initialize().catch(() => {});
    return pool;
  } catch {
    return undefined;
  }
}

function getPierreWorkerPool(style: "unified" | "split"): WorkerPoolManager | undefined {
  if (typeof window === "undefined" || typeof Worker === "undefined") return undefined;
  if (style === "split") {
    if (!splitWorkerPool) splitWorkerPool = createPierreWorkerPool("word-alt");
    return splitWorkerPool;
  }
  if (!unifiedWorkerPool) unifiedWorkerPool = createPierreWorkerPool("none");
  return unifiedWorkerPool;
}

function fastStringDigest(input: string): string {
  const len = input.length;
  if (len === 0) return "0";
  let h1 = 0x811c9dc5;
  const step = len > 4096 ? Math.floor(len / 512) : 1;
  for (let i = 0; i < len; i += step) {
    h1 ^= input.charCodeAt(i);
    h1 = Math.imul(h1, 0x01000193);
  }
  return `${len}:${(h1 >>> 0).toString(36)}`;
}

function resolveCachedFileDiffMeta(
  file: string,
  stamp: string | undefined,
  patch: string,
  before: string | undefined,
  after: string | undefined
): FileDiffMetadata | undefined {
  const hasBeforeAfter = typeof before === "string" || typeof after === "string";
  if (!patch && !hasBeforeAfter) return undefined;

  const key = patch
    ? `p:${file}\0${stamp || ""}\0${fastStringDigest(patch)}`
    : `ba:${file}\0${stamp || ""}\0${fastStringDigest(before ?? "")}\0${fastStringDigest(after ?? "")}`;

  const hit = fileDiffMetaCache.get(key);
  if (hit) {
    fileDiffMetaCache.delete(key);
    fileDiffMetaCache.set(key, hit);
    return hit;
  }

  let meta = patch ? getSingularPatch(patch) : undefined;
  if ((!meta || !meta.hunks?.length) && hasBeforeAfter) {
    meta = parseDiffFromFile(
      { name: file || "file", contents: before ?? "" },
      { name: file || "file", contents: after ?? "" }
    );
  }
  if (!meta && patch) {
    meta = getSingularPatch(patch);
  }
  if (!meta) return undefined;

  meta.cacheKey = key;
  fileDiffMetaCache.set(key, meta);
  while (fileDiffMetaCache.size > MAX_META_CACHE_ENTRIES) {
    const oldest = fileDiffMetaCache.keys().next().value;
    if (oldest === undefined) break;
    fileDiffMetaCache.delete(oldest);
  }
  return meta;
}

export function synthesizeAICommitMessage(
  sessionID: string | null | undefined,
  diffs: SnapshotFileDiff[]
): string {
  return synthesizeLocalCommitMessage(sessionID, diffs);
}

/**
 * Renders a single unified/split code diff using the @pierre/diffs vanilla
 * renderer backed by a shared Web Worker pool and in-place instance reuse.
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
  let instance: FileDiff<undefined, undefined> | undefined;
  let instancePool: WorkerPoolManager | undefined;

  onCleanup(() => {
    try {
      instance?.cleanUp();
    } catch {
      /* noop */
    }
    instance = undefined;
    instancePool = undefined;
  });

  createEffect(() => {
    const diffObj = props.diff;
    const patch = props.patch ?? diffObj?.patch ?? "";
    const file = diffObj?.file || "file";
    const stamp = diffObj?.stamp;
    const before = diffObj?.before;
    const after = diffObj?.after;
    const diffStyle = props.diffStyle ?? "unified";
    const themeType = workspace.theme === "dark" ? "dark" : "light";

    const meta = resolveCachedFileDiffMeta(file, stamp, patch, before, after);
    if (!meta) {
      if (instance) {
        try {
          instance.cleanUp();
        } catch {
          /* noop */
        }
        instance = undefined;
        instancePool = undefined;
      }
      host.innerHTML = "";
      return;
    }

    const isLarge =
      (meta.unifiedLineCount ?? 0) > LARGE_DIFF_LINE_THRESHOLD ||
      patch.length > LARGE_DIFF_BYTE_THRESHOLD ||
      (before?.length ?? 0) + (after?.length ?? 0) > LARGE_DIFF_BYTE_THRESHOLD;

    const pool = getPierreWorkerPool(isLarge ? "unified" : diffStyle);
    const opts: FileDiffOptions<undefined, undefined> = {
      theme: { dark: "pierre-dark", light: "pierre-light" },
      themeType,
      diffStyle,
      disableFileHeader: true,
      overflow: "scroll",
      hunkSeparators: "simple",
      ...(isLarge
        ? {
            lineDiffType: "none" as const,
            maxLineDiffLength: 0,
            tokenizeMaxLineLength: 1,
          }
        : {}),
    };

    try {
      if (instance && instancePool !== pool) {
        instance.cleanUp();
        instance = undefined;
        host.innerHTML = "";
      }
      if (!instance) {
        instance = new FileDiff(opts, pool);
        instancePool = pool;
      } else {
        instance.setOptions(opts);
      }
      instance.render({
        fileDiff: meta,
        containerWrapper: host,
      });
    } catch (err) {
      console.error("PierreDiff render failed:", err);
      host.textContent = patch || after || "";
    }
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
  const [loadedDiff, setLoadedDiff] = createSignal<SnapshotFileDiff | null>(null);
  const [loadingFull, setLoadingFull] = createSignal(false);

  const effectiveDiff = () => loadedDiff() || props.diff;

  const fetchFullFileDiff = async (targetFile: string, stamp?: string) => {
    const sessionID = workspace.activeSessionID || "__root__";
    const cacheKey = `${sessionID}\0${targetFile}\0${stamp || ""}`;
    const cached = fullDiffDetailCache.get(cacheKey);
    if (cached) {
      setLoadedDiff(cached);
      return;
    }
    setLoadingFull(true);
    try {
      const detail = await hoxiAPI.getWorktreeFileDiff(
        sessionID,
        targetFile,
        activeDirectory() || workspace.rootDir || undefined
      );
      if (detail) {
        fullDiffDetailCache.set(cacheKey, detail);
        while (fullDiffDetailCache.size > MAX_FULL_DIFF_CACHE_ENTRIES) {
          const oldest = fullDiffDetailCache.keys().next().value;
          if (oldest === undefined) break;
          fullDiffDetailCache.delete(oldest);
        }
        if (props.diff.file === targetFile) {
          setLoadedDiff(detail);
        }
      }
    } catch (err) {
      console.error("getWorktreeFileDiff failed:", err);
    } finally {
      setLoadingFull(false);
    }
  };

  createEffect(
    on(
      () => [props.diff.file, props.diff.stamp] as const,
      ([file, stamp]) => {
        const sessionID = workspace.activeSessionID || "__root__";
        const cached = fullDiffDetailCache.get(`${sessionID}\0${file}\0${stamp || ""}`);
        if (cached) {
          setLoadedDiff(cached);
          return;
        }
        setLoadedDiff(null);
        if (
          props.diff.large &&
          !props.diff.patch &&
          typeof props.diff.before !== "string" &&
          typeof props.diff.after !== "string"
        ) {
          void fetchFullFileDiff(file, stamp);
        }
      }
    )
  );

  createEffect(() => {
    const onKeyDown = (ev: KeyboardEvent) => {
      if (ev.defaultPrevented || ev.metaKey || ev.ctrlKey) return;
      const target = ev.target as HTMLElement | null;
      if (
        target &&
        (target.tagName === "INPUT" ||
          target.tagName === "TEXTAREA" ||
          target.tagName === "SELECT" ||
          target.isContentEditable)
      ) {
        return;
      }
      if (ev.key === "Escape" && !ev.altKey && !ev.shiftKey) {
        ev.preventDefault();
        props.onBack();
      } else if (
        (ev.key === "[" && !ev.altKey) ||
        (ev.altKey && (ev.key === "ArrowUp" || ev.key === "ArrowLeft"))
      ) {
        if (props.currentIndex > 0) {
          ev.preventDefault();
          props.onPrev();
        }
      } else if (
        (ev.key === "]" && !ev.altKey) ||
        (ev.altKey && (ev.key === "ArrowDown" || ev.key === "ArrowRight"))
      ) {
        if (props.currentIndex < props.totalFiles - 1) {
          ev.preventDefault();
          props.onNext();
        }
      }
    };
    window.addEventListener("keydown", onKeyDown);
    onCleanup(() => window.removeEventListener("keydown", onKeyDown));
  });

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
      await hoxiAPI.revertWorktreeFile(
        sessionID,
        props.diff.file,
        activeDirectory() || workspace.rootDir || undefined
      );
      await refreshWorktreeStatus(workspace.activeSessionID);
      if (workspace.activeSessionID) {
        await loadSessionMessagesOnly(workspace.activeSessionID);
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
          <DiffStatusBadge status={effectiveDiff().status} />
          <GeneratedFileBadge generated={effectiveDiff().generatedLike} />
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
            changes={{ additions: effectiveDiff().additions, deletions: effectiveDiff().deletions }}
          />
          <DiffChanges
            changes={{ additions: effectiveDiff().additions, deletions: effectiveDiff().deletions }}
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
          effectiveDiff().patch ||
          typeof effectiveDiff().before === "string" ||
          typeof effectiveDiff().after === "string"
        }
        fallback={
          <Show when={effectiveDiff().large}>
            <div class="flex items-center justify-between gap-2 px-3 py-3 text-[11.5px] text-[var(--text-muted)]">
              <span>{t("inspector.diffs.largeDiffHint")}</span>
              <button
                type="button"
                disabled={loadingFull()}
                onClick={() => void fetchFullFileDiff(props.diff.file)}
                class="shrink-0 rounded-md border border-[var(--border-subtle)] bg-[var(--bg-panel)] px-2.5 py-1 text-[11px] font-medium text-[var(--text-primary)] hover:bg-[var(--bg-hover)] disabled:opacity-40 cursor-pointer"
              >
                {t("inspector.diffs.loadFullDiffBtn")}
              </button>
            </div>
          </Show>
        }
      >
        <div class="p-2">
          <FileDiffBlock
            diff={effectiveDiff()}
            patch={effectiveDiff().patch}
            diffStyle={props.diffStyle}
            fillHeight={true}
          />
        </div>
      </Show>
    </div>
  );
}
