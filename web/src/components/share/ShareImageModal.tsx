// web/src/components/share/ShareImageModal.tsx
// Local-only session share image preview & export modal.
import { createEffect, createMemo, createSignal, onCleanup, Show, untrack } from "solid-js";
import { Portal } from "solid-js/web";
import {
  Check,
  ChevronLeft,
  ChevronRight,
  Copy,
  Download,
  FolderOpen,
  ImageDown,
  LoaderCircle,
  X,
} from "lucide-solid";
import { Presence } from "../motion/Presence";
import { t, locale } from "../../i18n";
import {
  appSettings,
  closeShareImageModal,
  copyShareImageBlob,
  fetchSessionExportTurns,
  formatSavedDisplayPath,
  resolveEffectiveTheme,
  revealSavedShareImage,
  saveShareImagePages,
  sessionDirectory,
  shareImageModal,
  shareImagePrefs,
  updateShareImagePrefs,
  workspace,
  type ShareSaveResult,
} from "../../store/workspace";
import ShareImageControls from "./ShareImageControls";
import {
  buildShareDocument,
  groupTurnsIntoExchanges,
  resolveInitialExchangeSelection,
  type ShareExchange,
  type ShareTurnInput,
} from "./share-image-model";
import {
  renderShareDocumentToImages,
  type RenderedShareResult,
} from "./share-image-renderer";

function formatFileSize(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
  return `${(bytes / (1024 * 1024)).toFixed(2)} MB`;
}

function formatShareDate(ts?: number): string {
  const date = ts ? new Date(ts) : new Date();
  if (Number.isNaN(date.getTime())) return "";
  const y = date.getFullYear();
  const m = String(date.getMonth() + 1).padStart(2, "0");
  const d = String(date.getDate()).padStart(2, "0");
  return `${y}-${m}-${d}`;
}

function isMacOSClient(): boolean {
  if (typeof navigator === "undefined") return false;
  return /Mac/i.test(navigator.platform || navigator.userAgent || "");
}

export default function ShareImageModal() {
  const [turns, setTurns] = createSignal<ShareTurnInput[]>([]);
  const [loadingTurns, setLoadingTurns] = createSignal(false);
  const [loadError, setLoadError] = createSignal("");
  const [selectedExchangeIDs, setSelectedExchangeIDs] = createSignal<Set<string>>(new Set());
  const [disabledImageIDs, setDisabledImageIDs] = createSignal<Set<string>>(new Set());
  const [rendered, setRendered] = createSignal<RenderedShareResult | null>(null);
  const [rendering, setRendering] = createSignal(false);
  const [renderError, setRenderError] = createSignal("");
  const [activePage, setActivePage] = createSignal(0);
  const [copyState, setCopyState] = createSignal<"idle" | "copied" | "failed">("idle");
  const [saving, setSaving] = createSignal(false);
  const [saveOutcome, setSaveOutcome] = createSignal<ShareSaveResult | null>(null);
  const [revealFailed, setRevealFailed] = createSignal(false);

  let loadGeneration = 0;
  let renderGeneration = 0;
  let activeAbort: AbortController | null = null;
  let copyTimer: ReturnType<typeof setTimeout> | undefined;
  let revealTimer: ReturnType<typeof setTimeout> | undefined;

  const disposeRendered = (next: RenderedShareResult | null = null) => {
    const prev = untrack(rendered);
    if (prev && prev !== next) prev.dispose();
    setRendered(next);
  };

  onCleanup(() => {
    loadGeneration++;
    activeAbort?.abort();
    activeAbort = null;
    if (copyTimer !== undefined) clearTimeout(copyTimer);
    if (revealTimer !== undefined) clearTimeout(revealTimer);
    disposeRendered(null);
  });

  const onKeyDown = (ev: KeyboardEvent) => {
    if (!shareImageModal()) return;
    if (ev.key === "Escape") {
      ev.preventDefault();
      ev.stopImmediatePropagation();
      closeShareImageModal();
    }
  };
  window.addEventListener("keydown", onKeyDown, { capture: true });
  onCleanup(() => window.removeEventListener("keydown", onKeyDown, { capture: true }));

  const currentSession = createMemo(() => {
    const req = shareImageModal();
    if (!req) return undefined;
    return workspace.sessions.find((s) => s.id === req.sessionID) ?? req.session;
  });

  const currentWorkDir = createMemo(() => {
    const req = shareImageModal();
    if (!req) return workspace.rootDir || "";
    return req.session?.directory || sessionDirectory(req.sessionID) || currentSession()?.directory || workspace.rootDir || "";
  });

  createEffect(() => {
    const req = shareImageModal();
    loadGeneration++;
    const gen = loadGeneration;
    activeAbort?.abort();
    activeAbort = null;
    setCopyState("idle");
    setSaving(false);
    setSaveOutcome(null);
    setRevealFailed(false);
    setRenderError("");
    setLoadError("");
    setActivePage(0);

    setTurns([]);
    setSelectedExchangeIDs(new Set<string>());
    setDisabledImageIDs(new Set<string>());
    disposeRendered(null);
    setLoadingTurns(Boolean(req));
    if (!req) return;

    void fetchSessionExportTurns(req.sessionID)
      .then((fetched) => {
        if (gen !== loadGeneration || untrack(shareImageModal)?.sessionID !== req.sessionID) return;
        const normalized = fetched as ShareTurnInput[];
        const livePrefs = untrack(shareImagePrefs);
        const liveDir = untrack(currentWorkDir);
        setTurns(normalized);
        const grouped = groupTurnsIntoExchanges(normalized, {
          hideLocalPaths: livePrefs.hideLocalPaths,
          redactSecrets: livePrefs.redactSecrets,
          workspaceDir: liveDir,
        });
        setSelectedExchangeIDs(resolveInitialExchangeSelection(grouped.exchanges, req.focusMessageID));
        setDisabledImageIDs(new Set<string>());
      })
      .catch((err) => {
        if (gen !== loadGeneration) return;
        setLoadError(err instanceof Error ? err.message : String(err));
      })
      .finally(() => {
        if (gen === loadGeneration) setLoadingTurns(false);
      });
  });

  const allExchanges = createMemo<ShareExchange[]>(() => {
    const p = shareImagePrefs();
    return groupTurnsIntoExchanges(turns(), {
      hideLocalPaths: p.hideLocalPaths,
      redactSecrets: p.redactSecrets,
      workspaceDir: currentWorkDir(),
    }).exchanges;
  });

  const shareDoc = createMemo(() => {
    const req = shareImageModal();
    if (!req) return null;
    const p = shareImagePrefs();
    return buildShareDocument(currentSession(), req.sessionID, turns(), {
      selectedExchangeIDs: selectedExchangeIDs(),
      disabledImageIDs: disabledImageIDs(),
      includeTools: p.includeTools,
      includeThinking: p.includeThinking,
      includeImages: p.includeImages,
      hideLocalPaths: p.hideLocalPaths,
      redactSecrets: p.redactSecrets,
      workspaceDir: currentWorkDir(),
    });
  });

  const selectedImages = createMemo(() => {
    const sel = selectedExchangeIDs();
    return (shareDoc()?.allImages ?? []).filter((img) => sel.has(img.exchangeID));
  });

  const effectiveTheme = createMemo<"light" | "dark">(() => {
    const choice = shareImagePrefs().theme;
    if (choice === "light" || choice === "dark") return choice;
    return resolveEffectiveTheme(appSettings.themeMode);
  });

  createEffect(() => {
    const req = shareImageModal();
    const doc = shareDoc();
    const p = shareImagePrefs();
    const theme = effectiveTheme();
    const workDir = currentWorkDir();
    void locale();

    if (!req || !doc || doc.exchanges.length === 0) {
      activeAbort?.abort();
      activeAbort = null;
      setRendering(false);
      setRenderError("");
      setSaveOutcome(null);
      setRevealFailed(false);
      disposeRendered(null);
      return;
    }

    renderGeneration++;
    const gen = renderGeneration;
    activeAbort?.abort();
    const controller = new AbortController();
    activeAbort = controller;
    disposeRendered(null);
    setSaveOutcome(null);
    setRevealFailed(false);
    setRendering(true);
    setRenderError("");

    const timer = setTimeout(() => {
      if (gen !== renderGeneration || controller.signal.aborted) return;
      void renderShareDocumentToImages({
        doc,
        theme,
        widthOption: p.width,
        layoutMode: p.layout,
        showBrandFooter: p.showBrandFooter,
        workspaceDir: workDir,
        formattedDate: formatShareDate(doc.updatedAt || doc.createdAt),
        signal: controller.signal,
        labels: {
          you: t("share.roleYou"),
          assistant: t("share.roleAssistant"),
          thinking: t("share.thinkingLabel"),
          quoted: t("share.quotedLabel"),
          referencedItems: (count) => t("share.referencedItems", { count }),
          referencedSingle: t("share.referencedSingle"),
          moreItems: (count) => t("share.moreItems", { count }),
          omittedLines: (count) => t("share.omittedLines", { count }),
          collabRolePM: t("share.collabRolePM"),
          collabRoleQA: t("share.collabRoleQA"),
          collabRoleOP: t("share.collabRoleOP"),
          moreTools: (count) => t("share.moreTools", { count }),
          truncatedCodeLines: (count) => t("share.truncatedCodeLines", { count }),
          imageUnavailable: (label) => t("share.imageUnavailable", { label }),
          turnsCount: (count) => t("share.turnsCount", { count }),
        },
      })
        .then((res) => {
          if (gen !== renderGeneration || controller.signal.aborted) {
            res.dispose();
            return;
          }
          disposeRendered(res);
          setActivePage((curr) => Math.min(curr, Math.max(0, res.pages.length - 1)));
          setRendering(false);
        })
        .catch((err) => {
          if (gen !== renderGeneration || controller.signal.aborted) return;
          if (err instanceof DOMException && err.name === "AbortError") return;
          setRenderError(err instanceof Error ? err.message : String(err));
          setRendering(false);
        });
    }, 60);

    onCleanup(() => {
      clearTimeout(timer);
      controller.abort();
    });
  });

  const currentPageObj = createMemo(() => {
    const res = rendered();
    if (!res || res.pages.length === 0) return null;
    return res.pages[Math.min(activePage(), res.pages.length - 1)] ?? null;
  });

  const toggleExchange = (id: string) => {
    setSelectedExchangeIDs((prev) => {
      const next = new Set(prev);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });
  };

  const toggleImage = (id: string) => {
    setDisabledImageIDs((prev) => {
      const next = new Set(prev);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });
  };

  const isAllPreset = createMemo(() => {
    const list = allExchanges();
    return list.length > 0 && selectedExchangeIDs().size === list.length;
  });

  const isRecentPreset = createMemo(() => {
    const list = allExchanges();
    if (list.length <= 3) return false;
    const sel = selectedExchangeIDs();
    const recent = list.slice(-3);
    return sel.size === recent.length && recent.every((ex) => sel.has(ex.id));
  });

  const isCurrentPreset = createMemo(() => {
    const focus = shareImageModal()?.focusMessageID;
    if (!focus) return false;
    const sel = selectedExchangeIDs();
    const matched = allExchanges().find((ex) => ex.messageIDs.includes(focus));
    return Boolean(matched && sel.size === 1 && sel.has(matched.id));
  });

  const applyScopePreset = (mode: "current" | "recent" | "all") => {
    const list = allExchanges();
    if (mode === "all") {
      setSelectedExchangeIDs(new Set(list.map((ex) => ex.id)));
      return;
    }
    if (mode === "recent") {
      setSelectedExchangeIDs(new Set(list.slice(-3).map((ex) => ex.id)));
      return;
    }
    const focus = shareImageModal()?.focusMessageID;
    setSelectedExchangeIDs(resolveInitialExchangeSelection(list, focus));
  };

  const handleCopy = async () => {
    const page = currentPageObj();
    if (!page) return;
    const ok = await copyShareImageBlob(page.blob);
    setCopyState(ok ? "copied" : "failed");
    if (copyTimer !== undefined) clearTimeout(copyTimer);
    copyTimer = setTimeout(() => setCopyState("idle"), ok ? 2000 : 3500);
  };

  const handleSave = async () => {
    const res = rendered();
    const doc = shareDoc();
    if (!res || res.pages.length === 0 || !doc || saving()) return;
    const startLoadGen = loadGeneration;
    const startRenderGen = renderGeneration;
    setSaving(true);
    setRevealFailed(false);
    const dialogTitle =
      res.pages.length > 1
        ? t("share.saveAllDialogTitle", { count: res.pages.length })
        : t("share.saveDialogTitle");
    try {
      const isCurrent = () => loadGeneration === startLoadGen && renderGeneration === startRenderGen;
      const outcome = await saveShareImagePages(res.pages, doc.title || doc.sessionID, {
        dialogTitle,
        batchDialogTitle: (first, last) => first === last
          ? t("share.savePageDialogTitle", { page: first })
          : t("share.savePageRangeDialogTitle", { first, last }),
        isCurrent,
      });
      if (!isCurrent()) return;
      if (outcome.status === "canceled") return;
      setSaveOutcome(outcome);
    } finally {
      if (loadGeneration === startLoadGen) setSaving(false);
    }
  };

  const handleRevealSaved = async (targetPath: string) => {
    setRevealFailed(false);
    const ok = await revealSavedShareImage(targetPath);
    if (!ok) {
      setRevealFailed(true);
      if (revealTimer !== undefined) clearTimeout(revealTimer);
      revealTimer = setTimeout(() => setRevealFailed(false), 3000);
    }
  };

  return (
    <Presence when={shareImageModal() !== null}>
      {(state) => (
        <Portal>
          <div
            data-state={state()}
            class="fixed inset-0 z-[115] flex items-center justify-center bg-black/55 p-4 md:p-6 backdrop-blur-[2px]"
            onClick={(ev) => {
              if (ev.target === ev.currentTarget) closeShareImageModal();
            }}
          >
            <div class="flex h-[86vh] w-full max-w-[1100px] flex-col overflow-hidden rounded-2xl border border-[var(--border-strong)] bg-[var(--bg-elevated)] text-[var(--text-primary)] shadow-2xl">
              {/* Modal Header */}
              <div class="flex shrink-0 items-center justify-between gap-3 border-b border-[var(--border-subtle)] px-5 py-3.5">
                <div class="flex min-w-0 items-center gap-2.5">
                  <div class="flex h-8 w-8 shrink-0 items-center justify-center rounded-xl bg-[var(--accent-surface)] text-[var(--accent-primary)]">
                    <ImageDown size={16} />
                  </div>
                  <div class="min-w-0">
                    <h2 class="truncate text-[14px] font-semibold leading-tight text-[var(--text-primary)]">
                      {t("share.modalTitle")}
                    </h2>
                    <p class="truncate text-[11.5px] text-[var(--text-muted)]">
                      {t("share.modalSubtitle")}
                    </p>
                  </div>
                </div>
                <button
                  type="button"
                  onClick={closeShareImageModal}
                  aria-label={t("common.close")}
                  class="inline-flex h-7 w-7 items-center justify-center rounded-lg text-[var(--text-muted)] hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)] transition-colors cursor-pointer"
                >
                  <X size={16} />
                </button>
              </div>

              {/* Main 2-column body */}
              <div class="flex min-h-0 flex-1 flex-col md:flex-row">
                {/* Left: Preview Stage */}
                <div class="relative flex min-h-0 flex-1 flex-col bg-[var(--bg-canvas)]">
                  <Show when={rendered()?.autoPaginated}>
                    <div class="flex shrink-0 items-center justify-between gap-2 border-b border-[var(--border-subtle)] bg-[var(--accent-surface)] px-4 py-2 text-[11.5px] text-[var(--accent-primary)]">
                      <span>{t("share.autoPaginatedNotice", { count: rendered()?.pages.length ?? 1 })}</span>
                    </div>
                  </Show>

                  <div class="flex-1 overflow-y-auto p-5">
                    <Show
                      when={!loadingTurns() || turns().length > 0}
                      fallback={
                        <div class="flex h-full items-center justify-center gap-2 text-[13px] text-[var(--text-muted)]">
                          <LoaderCircle size={16} class="animate-spin" />
                          <span>{t("share.loadingSession")}</span>
                        </div>
                      }
                    >
                      <Show
                        when={!loadError()}
                        fallback={
                          <div class="flex h-full items-center justify-center text-center text-[13px] text-[var(--danger-text)]">
                            {t("share.loadFailed", { error: loadError() })}
                          </div>
                        }
                      >
                        <Show
                          when={allExchanges().length > 0}
                          fallback={
                            <div class="flex h-full items-center justify-center text-[13px] text-[var(--text-muted)]">
                              {t("share.emptySession")}
                            </div>
                          }
                        >
                          <Show
                            when={selectedExchangeIDs().size > 0}
                            fallback={
                              <div class="flex h-full items-center justify-center text-[13px] text-[var(--text-muted)]">
                                {t("share.emptySelection")}
                              </div>
                            }
                          >
                            <Show
                              when={!renderError()}
                              fallback={
                                <div class="flex h-full items-center justify-center text-center text-[13px] text-[var(--danger-text)]">
                                  {t("share.renderFailed", { error: renderError() })}
                                </div>
                              }
                            >
                              <Show when={currentPageObj()}>
                                {(page) => (
                                  <div class="mx-auto flex flex-col items-center gap-3">
                                    <img
                                      src={page().objectURL}
                                      alt={shareDoc()?.title || "Hoxi share preview"}
                                      class="max-w-full rounded-xl shadow-md"
                                      style={{ width: `${Math.round(page().width / 2)}px` }}
                                    />
                                    <div class="text-[11px] font-mono text-[var(--text-faint)]">
                                      {page().width} × {page().height} px · {formatFileSize(page().blob.size)}
                                    </div>
                                  </div>
                                )}
                              </Show>
                            </Show>
                          </Show>
                        </Show>
                      </Show>
                    </Show>
                  </div>

                  {/* Floating render indicator */}
                  <Show when={rendering()}>
                    <div class="pointer-events-none absolute right-4 top-4 inline-flex items-center gap-1.5 rounded-full border border-[var(--border-subtle)] bg-[var(--bg-elevated)]/95 px-3 py-1 text-[11.5px] text-[var(--text-secondary)] shadow-md">
                      <LoaderCircle size={12} class="animate-spin text-[var(--accent-primary)]" />
                      <span>{t("share.renderingPreview")}</span>
                    </div>
                  </Show>

                  {/* Multi-page switcher bar */}
                  <Show when={(rendered()?.pages.length ?? 0) > 1}>
                    <div class="flex shrink-0 items-center justify-center gap-3 border-t border-[var(--border-subtle)] bg-[var(--bg-elevated)] px-4 py-2 text-[12px]">
                      <button
                        type="button"
                        disabled={activePage() <= 0}
                        onClick={() => setActivePage((p) => Math.max(0, p - 1))}
                        class="inline-flex h-6 w-6 items-center justify-center rounded-md text-[var(--text-secondary)] hover:bg-[var(--bg-hover)] disabled:opacity-35 cursor-pointer disabled:cursor-not-allowed"
                      >
                        <ChevronLeft size={14} />
                      </button>
                      <span class="font-mono text-[11.5px] text-[var(--text-secondary)]">
                        {t("share.pageIndicator", {
                          page: activePage() + 1,
                          total: rendered()?.pages.length ?? 1,
                        })}
                      </span>
                      <button
                        type="button"
                        disabled={activePage() >= (rendered()?.pages.length ?? 1) - 1}
                        onClick={() =>
                          setActivePage((p) => Math.min((rendered()?.pages.length ?? 1) - 1, p + 1))
                        }
                        class="inline-flex h-6 w-6 items-center justify-center rounded-md text-[var(--text-secondary)] hover:bg-[var(--bg-hover)] disabled:opacity-35 cursor-pointer disabled:cursor-not-allowed"
                      >
                        <ChevronRight size={14} />
                      </button>
                    </div>
                  </Show>
                </div>

                <ShareImageControls
                  allExchanges={allExchanges()}
                  selectedExchangeIDs={selectedExchangeIDs()}
                  hasFocusMessage={Boolean(shareImageModal()?.focusMessageID)}
                  isCurrentPreset={isCurrentPreset()}
                  isRecentPreset={isRecentPreset()}
                  isAllPreset={isAllPreset()}
                  prefs={shareImagePrefs()}
                  redactionCount={shareDoc()?.redactionCount ?? 0}
                  selectedImages={selectedImages()}
                  disabledImageIDs={disabledImageIDs()}
                  onApplyPreset={applyScopePreset}
                  onToggleExchange={toggleExchange}
                  onToggleImage={toggleImage}
                  onUpdatePrefs={updateShareImagePrefs}
                />
              </div>

              {/* Footer Action Bar */}
              <div class="flex shrink-0 flex-wrap items-center justify-between gap-3 border-t border-[var(--border-subtle)] px-5 py-3">
                <div class="flex min-w-0 flex-1 items-center gap-2 text-[12px]">
                  <Show when={copyState() === "failed"}>
                    <span class="truncate text-[var(--warn-text)]">{t("share.copyUnavailable")}</span>
                  </Show>
                  <Show when={copyState() === "copied"}>
                    <span class="truncate text-[var(--ok-text)]">{t("share.copiedImage")}</span>
                  </Show>
                  <Show when={copyState() === "idle" ? saveOutcome() : null} keyed>
                    {(res) => {
                      if (res.status === "error") {
                        return (
                          <span class="truncate text-[var(--danger-text)]" title={res.error}>
                            {t("share.saveFailed", { error: res.error })}
                          </span>
                        );
                      }
                      if (res.status === "partial") {
                        return (
                          <span class="truncate text-[var(--warn-text)]" title={res.error}>
                            {res.error
                              ? t("share.savePartialFailed", { count: res.savedCount, total: res.total, error: res.error })
                              : t("share.savePartial", { count: res.savedCount, total: res.total })}
                          </span>
                        );
                      }
                      if (res.status === "saved" && res.mode === "native") {
                        const primaryPath = res.paths[0];
                        const fullHint = res.paths.length > 1 ? res.directory || primaryPath : primaryPath;
                        const summaryText =
                          res.paths.length > 1
                            ? res.directory
                              ? t("share.savedBatchToDir", { count: res.paths.length, dir: formatSavedDisplayPath(res.directory) })
                              : t("share.savedBatch", { count: res.paths.length })
                            : t("share.savedToPath", {
                                path: formatSavedDisplayPath(primaryPath),
                              });
                        return (
                          <div class="flex min-w-0 items-center gap-2">
                            <span class="truncate text-[var(--ok-text)]" title={fullHint}>
                              {summaryText}
                            </span>
                            <button
                              type="button"
                              onClick={() => void handleRevealSaved(primaryPath)}
                              class="inline-flex shrink-0 items-center gap-1 rounded-lg border border-[var(--border-strong)] bg-[var(--bg-subtle)] px-2 py-0.5 text-[11.5px] font-medium text-[var(--text-primary)] hover:bg-[var(--bg-hover)] transition-colors cursor-pointer"
                            >
                              <FolderOpen size={12} />
                              <span>{isMacOSClient() ? t("share.revealInFinder") : t("share.revealInFolder")}</span>
                            </button>
                            <Show when={revealFailed()}>
                              <span class="shrink-0 text-[11px] text-[var(--warn-text)]">
                                {t("share.revealFailed")}
                              </span>
                            </Show>
                          </div>
                        );
                      }
                      if (res.status === "saved" && res.mode === "browser") {
                        return (
                          <span class="truncate text-[var(--ok-text)]">
                            {res.filenames.length > 1
                              ? t("share.savedBrowserDownloadBatch", { count: res.filenames.length })
                              : t("share.savedBrowserDownload", { filename: res.filenames[0] || "image.png" })}
                          </span>
                        );
                      }
                      return null;
                    }}
                  </Show>
                </div>

                <div class="flex shrink-0 items-center gap-2">
                  <button
                    type="button"
                    disabled={!rendered() || rendering() || saving()}
                    onClick={() => void handleSave()}
                    class="inline-flex items-center gap-1.5 rounded-xl border border-[var(--border-strong)] bg-[var(--bg-subtle)] px-3.5 py-2 text-[12.5px] font-medium text-[var(--text-primary)] hover:bg-[var(--bg-hover)] disabled:opacity-40 transition-colors cursor-pointer disabled:cursor-not-allowed"
                  >
                    <Show when={saving()} fallback={<Download size={14} />}>
                      <LoaderCircle size={14} class="animate-spin" />
                    </Show>
                    <span>
                      {saving()
                        ? t("share.savingPng")
                        : (rendered()?.pages.length ?? 0) > 1
                          ? t("share.saveAllPng", { count: rendered()?.pages.length ?? 0 })
                          : t("share.savePng")}
                    </span>
                  </button>

                  <button
                    type="button"
                    disabled={!currentPageObj() || rendering()}
                    onClick={() => void handleCopy()}
                    class="inline-flex items-center gap-1.5 rounded-xl bg-[var(--accent-primary)] px-4 py-2 text-[12.5px] font-medium text-white hover:opacity-90 disabled:opacity-40 transition-opacity cursor-pointer disabled:cursor-not-allowed"
                  >
                    <Show when={copyState() === "copied"} fallback={<Copy size={14} />}>
                      <Check size={14} />
                    </Show>
                    <span>
                      {copyState() === "copied"
                        ? t("share.copiedImage")
                        : (rendered()?.pages.length ?? 0) > 1
                          ? t("share.copyPageImage", { page: activePage() + 1 })
                          : t("share.copyImage")}
                    </span>
                  </button>
                </div>
              </div>
            </div>
          </div>
        </Portal>
      )}
    </Presence>
  );
}
