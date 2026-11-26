// web/src/components/share/ShareImageModal.tsx
// Local-only session share image preview & export modal.
import { createEffect, createMemo, createSignal, For, onCleanup, Show, untrack } from "solid-js";
import { Portal } from "solid-js/web";
import {
  Check,
  ChevronLeft,
  ChevronRight,
  Copy,
  Download,
  Image as ImageIcon,
  ImageDown,
  LoaderCircle,
  ShieldCheck,
  X,
} from "lucide-solid";
import { Presence } from "../motion/Presence";
import { t, locale } from "../../i18n";
import {
  appSettings,
  closeShareImageModal,
  fetchSessionExportTurns,
  resolveEffectiveTheme,
  sessionDirectory,
  shareImageModal,
  shareImagePrefs,
  updateShareImagePrefs,
  workspace,
  type ShareImageLayoutOption,
  type ShareImageThemeOption,
  type ShareImageWidthOption,
} from "../../store/workspace";
import {
  buildShareDocument,
  groupTurnsIntoExchanges,
  resolveInitialExchangeSelection,
  type ShareExchange,
  type ShareTurnInput,
} from "./share-image-model";
import {
  copyPngBlobToClipboard,
  downloadSharePages,
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
  const [savedToast, setSavedToast] = createSignal(false);

  let loadGeneration = 0;
  let renderGeneration = 0;
  let activeAbort: AbortController | null = null;
  let copyTimer: ReturnType<typeof setTimeout> | undefined;
  let saveTimer: ReturnType<typeof setTimeout> | undefined;

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
    if (saveTimer !== undefined) clearTimeout(saveTimer);
    disposeRendered(null);
  });

  // Escape closes modal cleanly before SettingsModal sees it.
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

  // Fetch full turns ONLY when the modal request changes (open/close/session switch).
  // Untrack prefs/messages so toggling checkboxes or streaming SSE never resets selections.
  createEffect(() => {
    const req = shareImageModal();
    loadGeneration++;
    const gen = loadGeneration;
    activeAbort?.abort();
    activeAbort = null;
    setCopyState("idle");
    setSavedToast(false);
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

  // Re-render preview whenever doc or options change (debounced slightly for rapid checkbox clicks).
  createEffect(() => {
    const req = shareImageModal();
    const doc = shareDoc();
    const p = shareImagePrefs();
    const theme = effectiveTheme();
    const workDir = currentWorkDir();
    void locale(); // re-render labels when UI language toggles

    if (!req || !doc || doc.exchanges.length === 0) {
      activeAbort?.abort();
      activeAbort = null;
      setRendering(false);
      setRenderError("");
      disposeRendered(null);
      return;
    }

    renderGeneration++;
    const gen = renderGeneration;
    activeAbort?.abort();
    const controller = new AbortController();
    activeAbort = controller;
    disposeRendered(null);
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
    const ok = await copyPngBlobToClipboard(page.blob);
    setCopyState(ok ? "copied" : "failed");
    if (copyTimer !== undefined) clearTimeout(copyTimer);
    copyTimer = setTimeout(() => setCopyState("idle"), ok ? 2000 : 3500);
  };

  const handleSave = () => {
    const res = rendered();
    const doc = shareDoc();
    if (!res || res.pages.length === 0 || !doc) return;
    downloadSharePages(res.pages, doc.title || doc.sessionID);
    setSavedToast(true);
    if (saveTimer !== undefined) clearTimeout(saveTimer);
    saveTimer = setTimeout(() => setSavedToast(false), 2000);
  };

  const pillClass = (active: boolean) =>
    `flex-1 rounded-lg px-2.5 py-1.5 text-[11.5px] font-medium transition-colors cursor-pointer ${
      active
        ? "bg-[var(--accent-surface)] text-[var(--accent-primary)] border border-[var(--accent-border)]"
        : "bg-[var(--bg-subtle)] text-[var(--text-secondary)] border border-transparent hover:text-[var(--text-primary)] hover:bg-[var(--bg-hover)]"
    }`;

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

                {/* Right: Controls Panel */}
                <aside class="flex w-full shrink-0 flex-col gap-5 overflow-y-auto border-t border-[var(--border-subtle)] bg-[var(--bg-elevated)] p-4 md:w-[320px] md:border-l md:border-t-0">
                  {/* Scope Selection */}
                  <section class="space-y-2">
                    <div class="flex items-center justify-between">
                      <span class="text-[11px] font-semibold uppercase tracking-wider text-[var(--text-faint)]">
                        {t("share.sectionScope", {
                          selected: selectedExchangeIDs().size,
                          total: allExchanges().length,
                        })}
                      </span>
                    </div>
                    <div class="flex gap-1.5">
                      <Show when={shareImageModal()?.focusMessageID}>
                        <button type="button" onClick={() => applyScopePreset("current")} class={pillClass(isCurrentPreset())}>
                          {t("share.presetCurrent")}
                        </button>
                      </Show>
                      <button type="button" onClick={() => applyScopePreset("recent")} class={pillClass(isRecentPreset())}>
                        {t("share.presetRecent")}
                      </button>
                      <button type="button" onClick={() => applyScopePreset("all")} class={pillClass(isAllPreset())}>
                        {t("share.presetAll")}
                      </button>
                    </div>
                    <Show when={allExchanges().length > 0}>
                      <div class="max-h-[164px] space-y-1 overflow-y-auto rounded-xl border border-[var(--border-subtle)] bg-[var(--bg-canvas)] p-1.5">
                        <For each={allExchanges()}>
                          {(ex) => (
                            <label class="flex items-start gap-2 rounded-lg px-2 py-1.5 text-[11.5px] hover:bg-[var(--bg-hover)] cursor-pointer select-none">
                              <input
                                type="checkbox"
                                checked={selectedExchangeIDs().has(ex.id)}
                                onChange={() => toggleExchange(ex.id)}
                                class="mt-0.5 accent-[var(--accent-primary)] cursor-pointer"
                              />
                              <div class="min-w-0 flex-1">
                                <div class="truncate font-medium text-[var(--text-primary)]">
                                  {ex.userPreview || t("share.exchangeLabel", { index: ex.index })}
                                </div>
                                <div class="truncate text-[10.5px] text-[var(--text-muted)]">
                                  {ex.assistantPreview || t("share.exchangeFallback")}
                                </div>
                              </div>
                            </label>
                          )}
                        </For>
                      </div>
                    </Show>
                  </section>

                  {/* Style & Layout */}
                  <section class="space-y-3">
                    <div class="text-[11px] font-semibold uppercase tracking-wider text-[var(--text-faint)]">
                      {t("share.sectionStyle")}
                    </div>

                    <div class="space-y-1.5">
                      <div class="text-[11.5px] text-[var(--text-muted)]">{t("share.themeLabel")}</div>
                      <div class="flex gap-1.5">
                        {(["light", "dark", "system"] as ShareImageThemeOption[]).map((opt) => (
                          <button
                            type="button"
                            onClick={() => updateShareImagePrefs({ theme: opt })}
                            class={pillClass(shareImagePrefs().theme === opt)}
                          >
                            {opt === "light"
                              ? t("share.themeLight")
                              : opt === "dark"
                                ? t("share.themeDark")
                                : t("share.themeSystem")}
                          </button>
                        ))}
                      </div>
                    </div>

                    <div class="space-y-1.5">
                      <div class="text-[11.5px] text-[var(--text-muted)]">{t("share.widthLabel")}</div>
                      <div class="flex gap-1.5">
                        {(["standard", "wide"] as ShareImageWidthOption[]).map((opt) => (
                          <button
                            type="button"
                            onClick={() => updateShareImagePrefs({ width: opt })}
                            class={pillClass(shareImagePrefs().width === opt)}
                          >
                            {opt === "standard" ? t("share.widthStandard") : t("share.widthWide")}
                          </button>
                        ))}
                      </div>
                    </div>

                    <div class="space-y-1.5">
                      <div class="text-[11.5px] text-[var(--text-muted)]">{t("share.layoutLabel")}</div>
                      <div class="flex gap-1.5">
                        {(["single", "paged"] as ShareImageLayoutOption[]).map((opt) => (
                          <button
                            type="button"
                            onClick={() => updateShareImagePrefs({ layout: opt })}
                            class={pillClass(shareImagePrefs().layout === opt)}
                          >
                            {opt === "single" ? t("share.layoutSingle") : t("share.layoutPaged")}
                          </button>
                        ))}
                      </div>
                    </div>
                  </section>

                  {/* Content & Privacy Toggles */}
                  <section class="space-y-2">
                    <div class="flex items-center justify-between gap-2">
                      <span class="text-[11px] font-semibold uppercase tracking-wider text-[var(--text-faint)]">
                        {t("share.sectionContent")}
                      </span>
                      <Show when={shareImagePrefs().redactSecrets && (shareDoc()?.redactionCount ?? 0) > 0}>
                        <span class="inline-flex items-center gap-1 rounded-full bg-[var(--ok-surface)] px-2 py-0.5 text-[10.5px] font-medium text-[var(--ok-text)]">
                          <ShieldCheck size={11} />
                          <span>{t("share.redactedBadge", { count: shareDoc()?.redactionCount ?? 0 })}</span>
                        </span>
                      </Show>
                    </div>

                    <div class="space-y-1">
                      <ToggleRow
                        label={t("share.toggleTools")}
                        checked={shareImagePrefs().includeTools}
                        onChange={(v) => updateShareImagePrefs({ includeTools: v })}
                      />
                      <ToggleRow
                        label={t("share.toggleThinking")}
                        checked={shareImagePrefs().includeThinking}
                        onChange={(v) => updateShareImagePrefs({ includeThinking: v })}
                      />
                      <ToggleRow
                        label={t("share.toggleImages")}
                        checked={shareImagePrefs().includeImages}
                        onChange={(v) => updateShareImagePrefs({ includeImages: v })}
                      />
                      <ToggleRow
                        label={t("share.toggleHidePaths")}
                        checked={shareImagePrefs().hideLocalPaths}
                        onChange={(v) => updateShareImagePrefs({ hideLocalPaths: v })}
                      />
                      <ToggleRow
                        label={t("share.toggleRedactSecrets")}
                        checked={shareImagePrefs().redactSecrets}
                        onChange={(v) => updateShareImagePrefs({ redactSecrets: v })}
                      />
                      <ToggleRow
                        label={t("share.toggleBrandFooter")}
                        checked={shareImagePrefs().showBrandFooter}
                        onChange={(v) => updateShareImagePrefs({ showBrandFooter: v })}
                      />
                    </div>
                  </section>

                  {/* Per-image privacy picker when embedded images exist */}
                  <Show when={shareImagePrefs().includeImages && selectedImages().length > 0}>
                    <section class="space-y-1.5">
                      <div class="text-[11px] font-semibold uppercase tracking-wider text-[var(--text-faint)]">
                        {t("share.sectionImages", {
                          selected: selectedImages().filter((img) => !disabledImageIDs().has(img.id)).length,
                          total: selectedImages().length,
                        })}
                      </div>
                      <div class="max-h-[112px] space-y-1 overflow-y-auto rounded-xl border border-[var(--border-subtle)] bg-[var(--bg-canvas)] p-1.5">
                        <For each={selectedImages()}>
                          {(img) => (
                            <label class="flex items-center gap-2 rounded-lg px-2 py-1 text-[11.5px] hover:bg-[var(--bg-hover)] cursor-pointer select-none">
                              <input
                                type="checkbox"
                                checked={!disabledImageIDs().has(img.id)}
                                onChange={() => toggleImage(img.id)}
                                class="accent-[var(--accent-primary)] cursor-pointer"
                              />
                              <ImageIcon size={12} class="shrink-0 text-[var(--text-muted)]" />
                              <span class="min-w-0 flex-1 truncate text-[var(--text-secondary)]">
                                {img.label}
                              </span>
                            </label>
                          )}
                        </For>
                      </div>
                    </section>
                  </Show>
                </aside>
              </div>

              {/* Footer Action Bar */}
              <div class="flex shrink-0 flex-wrap items-center justify-between gap-3 border-t border-[var(--border-subtle)] px-5 py-3">
                <div class="min-w-0 text-[12px]">
                  <Show when={copyState() === "failed"}>
                    <span class="text-[var(--warn-text)]">{t("share.copyUnavailable")}</span>
                  </Show>
                  <Show when={copyState() === "copied"}>
                    <span class="text-[var(--ok-text)]">{t("share.copiedImage")}</span>
                  </Show>
                  <Show when={copyState() === "idle" && savedToast()}>
                    <span class="text-[var(--ok-text)]">{t("share.savedPng")}</span>
                  </Show>
                </div>

                <div class="flex items-center gap-2">
                  <button
                    type="button"
                    disabled={!rendered() || rendering()}
                    onClick={handleSave}
                    class="inline-flex items-center gap-1.5 rounded-xl border border-[var(--border-strong)] bg-[var(--bg-subtle)] px-3.5 py-2 text-[12.5px] font-medium text-[var(--text-primary)] hover:bg-[var(--bg-hover)] disabled:opacity-40 transition-colors cursor-pointer disabled:cursor-not-allowed"
                  >
                    <Download size={14} />
                    <span>
                      {(rendered()?.pages.length ?? 0) > 1
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

function ToggleRow(props: { label: string; checked: boolean; onChange: (checked: boolean) => void }) {
  return (
    <label class="flex items-center justify-between gap-2 rounded-lg px-2 py-1.5 text-[12px] text-[var(--text-secondary)] hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)] transition-colors cursor-pointer select-none">
      <span>{props.label}</span>
      <input
        type="checkbox"
        checked={props.checked}
        onChange={(ev) => props.onChange(ev.currentTarget.checked)}
        class="h-3.5 w-3.5 accent-[var(--accent-primary)] cursor-pointer"
      />
    </label>
  );
}
