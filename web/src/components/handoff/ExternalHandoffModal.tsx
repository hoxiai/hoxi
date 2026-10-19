// web/src/components/handoff/ExternalHandoffModal.tsx
import { createEffect, createMemo, createSignal, For, onCleanup, Show } from "solid-js";
import { LoaderCircle, Search, X } from "lucide-solid";
import {
  hoxiAPI,
  type ExternalSessionDetail,
  type ExternalSessionSummary,
  type ExternalToolID,
  type LocalCLIStatusItem,
} from "../../services/api";
import {
  closeHandoffModal,
  normalizeFolderPath,
  refreshProviders,
  replaceSession,
  selectModel,
  selectSession,
  switchWorkspace,
  workspace,
} from "../../store/workspace";
import { recordCustomSessionTitle } from "../../store/session-title";
import { shortenPath } from "../layout/left-nav-helpers";
import ExternalHandoffPreviewPane, {
  handoffSourceBadgeClass,
  resolveOriginalModel,
} from "./ExternalHandoffPreviewPane";
import { t } from "../../i18n";

export default function ExternalHandoffModal() {
  const [allSessions, setAllSessions] = createSignal<ExternalSessionSummary[]>([]);
  const [cliTools, setCliTools] = createSignal<LocalCLIStatusItem[]>([]);
  const [loadingList, setLoadingList] = createSignal(false);
  const [sourceFilter, setSourceFilter] = createSignal<"all" | ExternalToolID>("all");
  const [onlyCurrent, setOnlyCurrent] = createSignal(false);
  const [query, setQuery] = createSignal("");
  const [modelMode, setModelMode] = createSignal<"original" | "local">("original");

  const [selectedItem, setSelectedItem] = createSignal<ExternalSessionSummary | null>(null);
  const [detail, setDetail] = createSignal<ExternalSessionDetail | null>(null);
  const [loadingDetail, setLoadingDetail] = createSignal(false);
  const [importingSession, setImportingSession] = createSignal(false);
  const [errorMsg, setErrorMsg] = createSignal<string | null>(null);

  const detailCache = new Map<string, ExternalSessionDetail>();
  let listRequestGen = 0;
  let detailRequestGen = 0;
  onCleanup(() => {
    listRequestGen++;
    detailRequestGen++;
  });

  const sessions = createMemo(() => {
    const src = sourceFilter();
    const curOnly = onlyCurrent();
    const q = query().trim().toLowerCase();
    return allSessions().filter((item) => {
      if (src !== "all" && item.source !== src) return false;
      if (curOnly && !item.matchesCwd) return false;
      if (q) {
        const hay = `${item.title} ${item.cwd} ${item.sourceLabel} ${item.model || ""} ${item.gitBranch || ""}`.toLowerCase();
        if (!hay.includes(q)) return false;
      }
      return true;
    });
  });

  const pickSession = async (item: ExternalSessionSummary) => {
    const reqID = ++detailRequestGen;
    setSelectedItem(item);
    const cacheKey = `${item.source}:${item.id}:${item.cwd || ""}`;
    const cached = detailCache.get(cacheKey);
    if (cached) {
      setDetail(cached);
      setLoadingDetail(false);
      return;
    }
    setLoadingDetail(true);
    try {
      const d = await hoxiAPI.getExternalSessionPreview(item.source, item.id, item.cwd);
      if (reqID !== detailRequestGen) return;
      detailCache.set(cacheKey, d);
      setDetail(d);
    } catch (err) {
      if (reqID !== detailRequestGen) return;
      console.error("load preview failed:", err);
      setDetail(null);
    } finally {
      if (reqID === detailRequestGen) {
        setLoadingDetail(false);
      }
    }
  };

  const loadSessionsAndStatus = async (serverQuery?: string) => {
    const reqID = ++listRequestGen;
    setLoadingList(true);
    setErrorMsg(null);
    try {
      const [sessRes, cliRes] = await Promise.all([
        hoxiAPI.listExternalSessions({
          directory: workspace.rootDir || undefined,
          query: serverQuery?.trim() || undefined,
        }),
        cliTools().length > 0
          ? Promise.resolve({ tools: cliTools() })
          : hoxiAPI.getLocalCLIStatus().catch(() => ({ tools: [] as LocalCLIStatusItem[] })),
      ]);
      if (reqID !== listRequestGen) return;
      setAllSessions(sessRes.sessions || []);
      setCliTools(cliRes.tools || []);
    } catch (err: any) {
      if (reqID !== listRequestGen) return;
      setErrorMsg(err?.message || String(err));
    } finally {
      if (reqID === listRequestGen) {
        setLoadingList(false);
      }
    }
  };

  createEffect(() => {
    if (!workspace.handoffModalOpen) return;
    void loadSessionsAndStatus();
  });

  createEffect(() => {
    if (!workspace.handoffModalOpen) return;
    const list = sessions();
    const cur = selectedItem();
    if (!cur || !list.some((s) => s.id === cur.id && s.source === cur.source)) {
      if (list.length > 0) {
        void pickSession(list[0]);
      } else {
        detailRequestGen++;
        setSelectedItem(null);
        setDetail(null);
        setLoadingDetail(false);
      }
    }
  });

  const currentOriginalModel = () => {
    const item = selectedItem();
    const cli = cliTools().find((c) => c.id === item?.source);
    return resolveOriginalModel(item, detail(), cli?.defaultModel || cli?.models?.[0]);
  };

  const onTakeOver = async () => {
    const item = selectedItem();
    if (!item || importingSession()) return;
    setImportingSession(true);
    setErrorMsg(null);
    try {
      const useOriginal = modelMode() === "original";
      const cli = cliTools().find((c) => c.id === item.source);
      if (useOriginal && cli && cli.authenticated && !cli.connected) {
        await hoxiAPI.importLocalCLI(item.source).catch(() => null);
      }
      const origModel = currentOriginalModel();
      const slashIdx = item.source === "opencode-cli" ? origModel.indexOf("/") : -1;
      const preferredProviderID = useOriginal
        ? slashIdx > 0
          ? origModel.slice(0, slashIdx)
          : cli?.providerID ||
            (item.source === "codex-cli"
              ? "openai"
              : item.source === "gemini-antigravity"
              ? "antigravity"
              : item.source)
        : workspace.selectedModel?.providerID;
      const preferredModelID = useOriginal
        ? slashIdx > 0
          ? origModel.slice(slashIdx + 1)
          : origModel
        : workspace.selectedModel?.modelID;

      const res = await hoxiAPI.importExternalSession({
        source: item.source,
        sessionID: item.id,
        sourceCwd: item.cwd,
        targetDirectory: item.cwd || workspace.rootDir || undefined,
        preferredProviderID,
        preferredModelID,
      });
      if (!res.ok || !res.session?.id) {
        setErrorMsg(res.error || t("common.error"));
        return;
      }
      await refreshProviders().catch(() => null);
      if (
        res.resolvedProviderID &&
        res.resolvedModelID &&
        workspace.providers.some(
          (p) => p.id === res.resolvedProviderID && Boolean(p.models?.[res.resolvedModelID!])
        )
      ) {
        selectModel({
          providerID: res.resolvedProviderID,
          modelID: res.resolvedModelID,
        });
      }
      const targetDir = res.targetDirectory || item.cwd || workspace.rootDir;
      if (
        targetDir &&
        normalizeFolderPath(targetDir) !== normalizeFolderPath(workspace.rootDir)
      ) {
        // The imported session is selected below. Do not bootstrap and select a
        // second session here, which would load two full transcripts at once.
        await switchWorkspace(targetDir, false);
      }
      if (res.session.title) {
        recordCustomSessionTitle(res.session.id, res.session.title);
      }
      replaceSession(res.session);
      await selectSession(res.session.id);
      closeHandoffModal();
    } catch (err: any) {
      setErrorMsg(err?.message || String(err));
    } finally {
      setImportingSession(false);
    }
  };

  const formatTime = (ms: number) => {
    if (!ms) return "";
    return new Date(ms).toLocaleString([], {
      month: "short",
      day: "numeric",
      hour: "2-digit",
      minute: "2-digit",
    });
  };

  return (
    <Show when={workspace.handoffModalOpen}>
      <div
        class="fixed inset-0 z-50 flex items-center justify-center bg-black/60 backdrop-blur-xs p-4"
        onClick={(e) => {
          if (e.target === e.currentTarget) closeHandoffModal();
        }}
      >
        <div class="flex h-[78vh] w-full max-w-4xl flex-col overflow-hidden rounded-2xl border border-[var(--border-strong)] bg-[var(--bg-elevated)] shadow-2xl">
          {/* Single Compact Header & Filter Bar */}
          <div class="flex flex-wrap items-center justify-between gap-2.5 border-b border-[var(--border-subtle)] px-4 py-2.5">
            <div class="flex items-center gap-3 min-w-0">
              <h2 class="text-[13.5px] font-semibold text-[var(--text-primary)] shrink-0">
                {t("leftNav.handoffModalTitle")}
              </h2>

              <div class="inline-flex flex-wrap rounded-lg bg-[var(--bg-inset)] p-0.5 text-[11px]">
                {(
                  [
                    "all",
                    "opencode-cli",
                    "grok-cli",
                    "claude-code",
                    "codex-cli",
                    "gemini-antigravity",
                    "codebuddy",
                    "trae",
                    "qoder",
                  ] as const
                ).map((src) => (
                  <button
                    type="button"
                    onClick={() => setSourceFilter(src)}
                    class={`rounded-md px-2 py-0.5 font-medium transition-colors cursor-pointer ${
                      sourceFilter() === src
                        ? "bg-[var(--bg-panel)] text-[var(--text-primary)] shadow-2xs"
                        : "text-[var(--text-muted)] hover:text-[var(--text-primary)]"
                    }`}
                  >
                    {src === "all"
                      ? t("leftNav.handoffSourceAll")
                      : src === "opencode-cli"
                      ? "OpenCode"
                      : src === "grok-cli"
                      ? "Grok"
                      : src === "claude-code"
                      ? "Claude"
                      : src === "codex-cli"
                      ? "Codex"
                      : src === "gemini-antigravity"
                      ? "Antigravity"
                      : src === "codebuddy"
                      ? "CodeBuddy"
                      : src === "trae"
                      ? "Trae"
                      : "Qoder"}
                  </button>
                ))}
              </div>
            </div>

            <div class="flex items-center gap-2">
              <div class="inline-flex rounded-lg bg-[var(--bg-inset)] p-0.5 text-[11px]">
                <button
                  type="button"
                  onClick={() => setOnlyCurrent(false)}
                  class={`rounded-md px-2 py-0.5 font-medium transition-colors cursor-pointer ${
                    !onlyCurrent()
                      ? "bg-[var(--bg-panel)] text-[var(--text-primary)] shadow-2xs"
                      : "text-[var(--text-muted)] hover:text-[var(--text-primary)]"
                  }`}
                >
                  {t("leftNav.handoffScopeAll")}
                </button>
                <button
                  type="button"
                  onClick={() => setOnlyCurrent(true)}
                  class={`rounded-md px-2 py-0.5 font-medium transition-colors cursor-pointer ${
                    onlyCurrent()
                      ? "bg-[var(--bg-panel)] text-[var(--text-primary)] shadow-2xs"
                      : "text-[var(--text-muted)] hover:text-[var(--text-primary)]"
                  }`}
                >
                  {t("leftNav.handoffScopeCurrent")}
                </button>
              </div>

              <div class="flex w-44 items-center gap-1.5 rounded-lg border border-[var(--border-subtle)] bg-[var(--bg-inset)] px-2.5 py-1 text-[11.5px]">
                <Search size={12} class="text-[var(--text-muted)] shrink-0" />
                <input
                  type="text"
                  value={query()}
                  placeholder={t("leftNav.handoffSearchPlaceholder")}
                  onInput={(e) => setQuery(e.currentTarget.value)}
                  onKeyDown={(e) => {
                    if (e.key === "Enter") void loadSessionsAndStatus(query());
                  }}
                  class="w-full bg-transparent text-[11.5px] text-[var(--text-primary)] placeholder:text-[var(--text-faint)] outline-none"
                />
              </div>

              <button
                type="button"
                onClick={() => closeHandoffModal()}
                class="rounded-lg p-1 text-[var(--text-muted)] hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)] cursor-pointer"
              >
                <X size={15} />
              </button>
            </div>
          </div>

          <Show when={errorMsg()}>
            <div class="bg-[var(--danger-surface)] px-4 py-1.5 text-[11.5px] text-[var(--danger-text)]">
              {errorMsg()}
            </div>
          </Show>

          {/* Main Split View */}
          <div class="flex flex-1 min-h-0 overflow-hidden">
            <div class="w-[300px] shrink-0 border-r border-[var(--border-subtle)] overflow-y-auto p-2 space-y-1">
              <Show
                when={!loadingList()}
                fallback={
                  <div class="flex items-center justify-center py-12 text-[var(--text-muted)]">
                    <LoaderCircle size={16} class="animate-spin" />
                  </div>
                }
              >
                <Show
                  when={sessions().length > 0}
                  fallback={
                    <div class="px-4 py-12 text-center text-[12px] text-[var(--text-muted)]">
                      {t("leftNav.handoffNoSessions")}
                    </div>
                  }
                >
                  <For each={sessions()}>
                    {(item) => {
                      const active = () =>
                        selectedItem()?.id === item.id && selectedItem()?.source === item.source;
                      return (
                        <button
                          type="button"
                          onClick={() => void pickSession(item)}
                          class={`w-full rounded-xl px-2.5 py-2 text-left transition-colors cursor-pointer ${
                            active()
                              ? "bg-[var(--accent-surface)]/45 text-[var(--text-primary)]"
                              : "hover:bg-[var(--bg-hover)]/60 text-[var(--text-secondary)]"
                          }`}
                        >
                          <div class="flex items-center justify-between gap-2">
                            <span class="text-[12.5px] font-medium text-[var(--text-primary)] truncate">
                              {item.title}
                            </span>
                            <span class="text-[10px] font-mono text-[var(--text-faint)] shrink-0">
                              {formatTime(item.updatedAt)}
                            </span>
                          </div>

                          <div class="mt-1 flex items-center gap-1.5 text-[10.5px] text-[var(--text-muted)] min-w-0">
                            <span
                              class={`shrink-0 rounded border px-1.5 py-0.2 text-[9.5px] font-medium ${handoffSourceBadgeClass(
                                item.source
                              )}`}
                            >
                              {item.sourceLabel}
                            </span>
                            <span class="font-mono truncate">{shortenPath(item.cwd)}</span>
                            <Show when={item.matchesCwd}>
                              <span
                                class="ml-auto w-1.5 h-1.5 rounded-full bg-[var(--ok)] shrink-0"
                                title={t("leftNav.handoffMatchesCurrent")}
                              />
                            </Show>
                          </div>
                        </button>
                      );
                    }}
                  </For>
                </Show>
              </Show>
            </div>

            <ExternalHandoffPreviewPane
              selectedItem={selectedItem()}
              detail={detail()}
              loadingDetail={loadingDetail()}
              importingSession={importingSession()}
              modelMode={modelMode()}
              onChangeModelMode={setModelMode}
              originalModelLabel={currentOriginalModel()}
              localModelLabel={workspace.selectedModel?.modelID || "default"}
              onTakeOver={() => void onTakeOver()}
            />
          </div>
        </div>
      </div>
    </Show>
  );
}
