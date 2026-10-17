// web/src/components/layout/ExternalHandoffModal.tsx
import { createEffect, createSignal, For, Show } from "solid-js";
import {
  ArrowRightLeft,
  Check,
  FileCode2,
  FolderGit2,
  GitBranch,
  ListTodo,
  LoaderCircle,
  Search,
  Sparkles,
  Terminal,
  Wrench,
  X,
} from "lucide-solid";
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
  selectSession,
  switchWorkspace,
  workspace,
} from "../../store/workspace";
import { shortenPath } from "./left-nav-helpers";
import { t } from "../../i18n";

export default function ExternalHandoffModal() {
  const [sessions, setSessions] = createSignal<ExternalSessionSummary[]>([]);
  const [cliTools, setCliTools] = createSignal<LocalCLIStatusItem[]>([]);
  const [loadingList, setLoadingList] = createSignal(false);
  const [sourceFilter, setSourceFilter] = createSignal<"all" | ExternalToolID>("all");
  const [onlyCurrent, setOnlyCurrent] = createSignal(false);
  const [query, setQuery] = createSignal("");

  const [selectedItem, setSelectedItem] = createSignal<ExternalSessionSummary | null>(null);
  const [detail, setDetail] = createSignal<ExternalSessionDetail | null>(null);
  const [loadingDetail, setLoadingDetail] = createSignal(false);
  const [importingSession, setImportingSession] = createSignal(false);
  const [importingCliID, setImportingCliID] = createSignal<string | null>(null);
  const [errorMsg, setErrorMsg] = createSignal<string | null>(null);

  const loadSessionsAndStatus = async () => {
    setLoadingList(true);
    setErrorMsg(null);
    try {
      const [sessRes, cliRes] = await Promise.all([
        hoxiAPI.listExternalSessions({
          directory: workspace.rootDir || undefined,
          query: query().trim() || undefined,
          source: sourceFilter() === "all" ? undefined : sourceFilter(),
          onlyCurrent: onlyCurrent(),
        }),
        hoxiAPI.getLocalCLIStatus().catch(() => ({ tools: [] as LocalCLIStatusItem[] })),
      ]);
      const list = sessRes.sessions || [];
      setSessions(list);
      setCliTools(cliRes.tools || []);
      const cur = selectedItem();
      if (!cur || !list.some((s) => s.id === cur.id && s.source === cur.source)) {
        if (list.length > 0) {
          void pickSession(list[0]);
        } else {
          setSelectedItem(null);
          setDetail(null);
        }
      }
    } catch (err: any) {
      setErrorMsg(err?.message || String(err));
    } finally {
      setLoadingList(false);
    }
  };

  const pickSession = async (item: ExternalSessionSummary) => {
    setSelectedItem(item);
    setLoadingDetail(true);
    try {
      const d = await hoxiAPI.getExternalSessionPreview(item.source, item.id, item.cwd);
      setDetail(d);
    } catch (err) {
      console.error("load preview failed:", err);
      setDetail(null);
    } finally {
      setLoadingDetail(false);
    }
  };

  createEffect(() => {
    if (!workspace.handoffModalOpen) return;
    void sourceFilter();
    void onlyCurrent();
    void loadSessionsAndStatus();
  });

  const onTakeOver = async () => {
    const item = selectedItem();
    if (!item || importingSession()) return;
    setImportingSession(true);
    setErrorMsg(null);
    try {
      const res = await hoxiAPI.importExternalSession({
        source: item.source,
        sessionID: item.id,
        sourceCwd: item.cwd,
        targetDirectory: item.cwd || workspace.rootDir || undefined,
      });
      if (!res.ok || !res.session?.id) {
        setErrorMsg(res.error || t("common.error"));
        return;
      }
      const targetDir = res.targetDirectory || item.cwd || workspace.rootDir;
      if (
        targetDir &&
        normalizeFolderPath(targetDir) !== normalizeFolderPath(workspace.rootDir)
      ) {
        await switchWorkspace(targetDir);
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

  const onImportCliModels = async (toolID: ExternalToolID) => {
    if (importingCliID()) return;
    setImportingCliID(toolID);
    setErrorMsg(null);
    try {
      const res = await hoxiAPI.importLocalCLI(toolID);
      if (!res.ok) {
        setErrorMsg(res.error || t("common.error"));
        return;
      }
      await refreshProviders();
      const cliRes = await hoxiAPI.getLocalCLIStatus();
      setCliTools(cliRes.tools || []);
    } catch (err: any) {
      setErrorMsg(err?.message || String(err));
    } finally {
      setImportingCliID(null);
    }
  };

  const sourceBadgeClass = (source: ExternalToolID) => {
    if (source === "grok-cli") return "bg-purple-500/15 text-purple-400 border-purple-500/30";
    if (source === "claude-code") return "bg-amber-500/15 text-amber-400 border-amber-500/30";
    return "bg-emerald-500/15 text-emerald-400 border-emerald-500/30";
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
        <div class="flex h-[82vh] w-full max-w-5xl flex-col overflow-hidden rounded-2xl border border-[var(--border-strong)] bg-[var(--bg-elevated)] shadow-2xl">
          {/* Header */}
          <div class="flex items-center justify-between border-b border-[var(--border-subtle)] px-5 py-3.5">
            <div class="flex items-center gap-2.5 min-w-0">
              <div class="flex h-8 w-8 shrink-0 items-center justify-center rounded-xl bg-[var(--accent-surface)] text-[var(--accent-primary)]">
                <ArrowRightLeft size={16} />
              </div>
              <div class="min-w-0">
                <h2 class="text-[14px] font-semibold text-[var(--text-primary)] truncate">
                  {t("leftNav.handoffModalTitle")}
                </h2>
                <p class="text-[11.5px] text-[var(--text-muted)] truncate">
                  {t("leftNav.handoffModalSubtitle")}
                </p>
              </div>
            </div>
            <button
              type="button"
              onClick={() => closeHandoffModal()}
              class="rounded-lg p-1.5 text-[var(--text-muted)] hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)] cursor-pointer"
            >
              <X size={16} />
            </button>
          </div>

          {/* Local CLI Bridge Quick Bar */}
          <Show when={cliTools().some((item) => item.installed)}>
            <div class="flex flex-wrap items-center justify-between gap-2 border-b border-[var(--border-subtle)] bg-[var(--bg-inset)]/50 px-5 py-2">
              <div class="flex items-center gap-2 text-[11.5px] font-medium text-[var(--text-secondary)]">
                <Terminal size={13} class="text-[var(--accent-primary)]" />
                <span>{t("leftNav.handoffCliBannerTitle")}:</span>
              </div>
              <div class="flex flex-wrap items-center gap-2">
                <For each={cliTools().filter((item) => item.installed)}>
                  {(tool) => (
                    <div class="inline-flex items-center gap-2 rounded-xl border border-[var(--border-subtle)] bg-[var(--bg-panel)] px-2.5 py-1 text-[11px]">
                      <span class="font-semibold text-[var(--text-primary)]">{tool.name}</span>
                      <span class="rounded bg-[var(--bg-inset)] px-1.5 py-0.2 font-mono text-[10px] text-[var(--text-muted)]">
                        {t("leftNav.handoffCliSessionsBadge", { count: tool.sessionCount })}
                      </span>
                      <Show
                        when={!tool.connected && tool.authenticated}
                        fallback={
                          <Show when={tool.connected}>
                            <span class="inline-flex items-center gap-1 text-[10.5px] font-medium text-[var(--ok-text)]">
                              <Check size={11} />
                              {t("leftNav.handoffCliConnected")}
                            </span>
                          </Show>
                        }
                      >
                        <button
                          type="button"
                          disabled={importingCliID() === tool.id}
                          onClick={() => void onImportCliModels(tool.id)}
                          class="inline-flex items-center gap-1 rounded-md bg-[var(--accent-primary)] px-2 py-0.5 text-[10.5px] font-medium text-white hover:opacity-90 cursor-pointer"
                        >
                          <Sparkles size={10} />
                          {importingCliID() === tool.id
                            ? t("leftNav.handoffCliImporting")
                            : t("leftNav.handoffCliImportBtn")}
                        </button>
                      </Show>
                    </div>
                  )}
                </For>
              </div>
            </div>
          </Show>

          {/* Filter & Search Toolbar */}
          <div class="flex flex-wrap items-center justify-between gap-2 border-b border-[var(--border-subtle)] px-5 py-2.5">
            <div class="flex items-center gap-1.5">
              {/* Scope toggle */}
              <div class="inline-flex rounded-lg bg-[var(--bg-inset)] p-0.5 text-[11px]">
                <button
                  type="button"
                  onClick={() => setOnlyCurrent(false)}
                  class={`rounded-md px-2.5 py-1 font-medium transition-colors cursor-pointer ${
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
                  class={`rounded-md px-2.5 py-1 font-medium transition-colors cursor-pointer ${
                    onlyCurrent()
                      ? "bg-[var(--bg-panel)] text-[var(--text-primary)] shadow-2xs"
                      : "text-[var(--text-muted)] hover:text-[var(--text-primary)]"
                  }`}
                >
                  {t("leftNav.handoffScopeCurrent")}
                </button>
              </div>

              {/* Source filter pills */}
              <div class="inline-flex rounded-lg bg-[var(--bg-inset)] p-0.5 text-[11px]">
                {(["all", "grok-cli", "claude-code", "codex-cli"] as const).map((src) => (
                  <button
                    type="button"
                    onClick={() => setSourceFilter(src)}
                    class={`rounded-md px-2.5 py-1 font-medium transition-colors cursor-pointer ${
                      sourceFilter() === src
                        ? "bg-[var(--bg-panel)] text-[var(--text-primary)] shadow-2xs"
                        : "text-[var(--text-muted)] hover:text-[var(--text-primary)]"
                    }`}
                  >
                    {src === "all"
                      ? t("leftNav.handoffSourceAll")
                      : src === "grok-cli"
                      ? "Grok CLI"
                      : src === "claude-code"
                      ? "Claude Code"
                      : "Codex"}
                  </button>
                ))}
              </div>
            </div>

            {/* Search Input */}
            <div class="flex flex-1 max-w-xs items-center gap-1.5 rounded-xl border border-[var(--border-subtle)] bg-[var(--bg-inset)] px-2.5 py-1 text-[12px]">
              <Search size={13} class="text-[var(--text-muted)] shrink-0" />
              <input
                type="text"
                value={query()}
                placeholder={t("leftNav.handoffSearchPlaceholder")}
                onInput={(e) => setQuery(e.currentTarget.value)}
                onKeyDown={(e) => {
                  if (e.key === "Enter") void loadSessionsAndStatus();
                }}
                class="w-full bg-transparent text-[12px] text-[var(--text-primary)] placeholder:text-[var(--text-faint)] outline-none"
              />
            </div>
          </div>

          <Show when={errorMsg()}>
            <div class="bg-[var(--danger-surface)] px-5 py-2 text-[11.5px] text-[var(--danger-text)]">
              {errorMsg()}
            </div>
          </Show>

          {/* Main Split View: Left Session List, Right Detail Preview */}
          <div class="flex flex-1 min-h-0 overflow-hidden">
            {/* Left List */}
            <div class="w-[360px] shrink-0 border-r border-[var(--border-subtle)] overflow-y-auto p-2.5 space-y-1.5">
              <Show
                when={!loadingList()}
                fallback={
                  <div class="flex items-center justify-center py-12 text-[var(--text-muted)]">
                    <LoaderCircle size={18} class="animate-spin" />
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
                          class={`w-full rounded-xl border p-2.5 text-left transition-all cursor-pointer ${
                            active()
                              ? "border-[var(--accent-border)] bg-[var(--accent-surface)]/35 shadow-2xs"
                              : "border-transparent hover:border-[var(--border-subtle)] hover:bg-[var(--bg-hover)]/60"
                          }`}
                        >
                          <div class="flex items-center justify-between gap-1.5 mb-1">
                            <div class="flex items-center gap-1.5 min-w-0">
                              <span
                                class={`rounded-md border px-1.5 py-0.2 text-[10px] font-semibold ${sourceBadgeClass(
                                  item.source
                                )}`}
                              >
                                {item.sourceLabel}
                              </span>
                              <Show when={item.matchesCwd}>
                                <span class="rounded-md bg-[var(--ok-surface)] px-1.5 py-0.2 text-[9.5px] font-medium text-[var(--ok-text)]">
                                  {t("leftNav.handoffMatchesCurrent")}
                                </span>
                              </Show>
                            </div>
                            <span class="text-[10px] font-mono text-[var(--text-faint)] shrink-0">
                              {formatTime(item.updatedAt)}
                            </span>
                          </div>

                          <div class="text-[12.5px] font-semibold text-[var(--text-primary)] line-clamp-1">
                            {item.title}
                          </div>

                          <Show when={item.summary}>
                            <div class="mt-0.5 text-[11px] text-[var(--text-secondary)] line-clamp-2">
                              {item.summary}
                            </div>
                          </Show>

                          <div class="mt-1.5 flex items-center gap-2 text-[10px] font-mono text-[var(--text-faint)]">
                            <span class="inline-flex items-center gap-1 truncate">
                              <FolderGit2 size={11} class="shrink-0" />
                              <span class="truncate">{shortenPath(item.cwd)}</span>
                            </span>
                            <Show when={item.gitBranch}>
                              <span class="inline-flex items-center gap-0.5 shrink-0 rounded bg-[var(--bg-inset)] px-1.5 py-0.2 text-[var(--text-muted)]">
                                <GitBranch size={10} />
                                {item.gitBranch}
                              </span>
                            </Show>
                          </div>
                        </button>
                      );
                    }}
                  </For>
                </Show>
              </Show>
            </div>

            {/* Right Preview & Handoff Action */}
            <div class="flex-1 flex flex-col min-w-0 overflow-hidden bg-[var(--bg-canvas)]/40">
              <Show
                when={selectedItem()}
                fallback={
                  <div class="flex flex-1 items-center justify-center p-8 text-center text-[12.5px] text-[var(--text-muted)]">
                    {t("leftNav.handoffSelectHint")}
                  </div>
                }
              >
                {(cur) => (
                  <>
                    <div class="flex-1 overflow-y-auto p-5 space-y-4">
                      {/* Title & Metadata Card */}
                      <div class="rounded-xl border border-[var(--border-subtle)] bg-[var(--bg-elevated)] p-4 space-y-2">
                        <div class="flex items-center justify-between gap-2">
                          <div class="flex items-center gap-2">
                            <span
                              class={`rounded-md border px-2 py-0.5 text-[10.5px] font-semibold ${sourceBadgeClass(
                                cur().source
                              )}`}
                            >
                              {cur().sourceLabel}
                            </span>
                            <Show when={cur().model}>
                              <span class="rounded bg-[var(--bg-inset)] px-2 py-0.5 font-mono text-[10.5px] text-[var(--text-secondary)]">
                                {cur().model}
                              </span>
                            </Show>
                            <Show when={detail()?.turns}>
                              <span class="text-[11px] text-[var(--text-muted)]">
                                {t("leftNav.handoffTurnsCount", {
                                  count: detail()?.turns.length || 0,
                                })}
                              </span>
                            </Show>
                          </div>
                          <span class="font-mono text-[11px] text-[var(--text-faint)]">
                            {cur().id.slice(0, 12)}
                          </span>
                        </div>

                        <h3 class="text-[15px] font-semibold text-[var(--text-primary)]">
                          {cur().title}
                        </h3>

                        <div class="flex flex-wrap items-center gap-3 text-[11.5px] font-mono text-[var(--text-secondary)]">
                          <span class="inline-flex items-center gap-1">
                            <FolderGit2 size={12} class="text-[var(--accent-primary)]" />
                            {cur().cwd}
                          </span>
                          <Show when={cur().gitBranch}>
                            <span class="inline-flex items-center gap-1 rounded bg-[var(--bg-inset)] px-2 py-0.5">
                              <GitBranch size={11} />
                              {cur().gitBranch}
                              <Show when={cur().gitCommit}>
                                <span class="text-[var(--text-faint)]">({cur().gitCommit})</span>
                              </Show>
                            </span>
                          </Show>
                        </div>

                        <Show when={cur().summary || detail()?.lastTurnSummary}>
                          <div class="mt-2 rounded-lg bg-[var(--bg-inset)]/70 p-3 text-[11.5px] leading-relaxed text-[var(--text-secondary)] whitespace-pre-wrap">
                            <div class="mb-1 text-[10.5px] font-semibold uppercase tracking-wider text-[var(--text-muted)]">
                              {t("leftNav.handoffSummaryTitle")}
                            </div>
                            {cur().summary || detail()?.lastTurnSummary}
                          </div>
                        </Show>
                      </div>

                      {/* Plan & Changed Files */}
                      <Show
                        when={
                          (detail()?.planSteps?.length || 0) > 0 ||
                          (detail()?.changedFiles?.length || 0) > 0
                        }
                      >
                        <div class="grid grid-cols-2 gap-3">
                          <Show when={(detail()?.planSteps?.length || 0) > 0}>
                            <div class="rounded-xl border border-[var(--border-subtle)] bg-[var(--bg-elevated)] p-3.5 space-y-2">
                              <div class="flex items-center gap-1.5 text-[11px] font-semibold text-[var(--text-secondary)]">
                                <ListTodo size={13} class="text-[var(--accent-primary)]" />
                                {t("leftNav.handoffPlanTitle")}
                              </div>
                              <div class="space-y-1 max-h-36 overflow-y-auto">
                                <For each={detail()?.planSteps}>
                                  {(step) => (
                                    <div class="flex items-start gap-1.5 text-[11px] text-[var(--text-secondary)]">
                                      <span class="font-mono text-[10px] text-[var(--text-muted)]">
                                        [{step.status || "todo"}]
                                      </span>
                                      <span class="line-clamp-2">{step.title}</span>
                                    </div>
                                  )}
                                </For>
                              </div>
                            </div>
                          </Show>

                          <Show when={(detail()?.changedFiles?.length || 0) > 0}>
                            <div class="rounded-xl border border-[var(--border-subtle)] bg-[var(--bg-elevated)] p-3.5 space-y-2">
                              <div class="flex items-center gap-1.5 text-[11px] font-semibold text-[var(--text-secondary)]">
                                <FileCode2 size={13} class="text-[var(--accent-primary)]" />
                                {t("leftNav.handoffChangedFilesTitle")} (
                                {detail()?.changedFiles?.length})
                              </div>
                              <div class="space-y-1 max-h-36 overflow-y-auto font-mono text-[10.5px] text-[var(--text-secondary)]">
                                <For each={detail()?.changedFiles}>
                                  {(f) => <div class="truncate">{shortenPath(f)}</div>}
                                </For>
                              </div>
                            </div>
                          </Show>
                        </div>
                      </Show>

                      {/* Conversation Turns Preview */}
                      <div class="space-y-2">
                        <div class="text-[11px] font-semibold uppercase tracking-wider text-[var(--text-muted)] px-1">
                          {t("leftNav.handoffConversationTitle")}
                        </div>
                        <Show
                          when={!loadingDetail()}
                          fallback={
                            <div class="flex items-center justify-center py-10 text-[var(--text-muted)]">
                              <LoaderCircle size={16} class="animate-spin" />
                            </div>
                          }
                        >
                          <div class="space-y-2">
                            <For each={detail()?.turns || []}>
                              {(turn) => (
                                <div
                                  class={`rounded-xl border p-3 text-[12px] ${
                                    turn.role === "user"
                                      ? "border-[var(--accent-border)]/40 bg-[var(--accent-surface)]/20"
                                      : "border-[var(--border-subtle)] bg-[var(--bg-elevated)]"
                                  }`}
                                >
                                  <div class="mb-1 flex items-center justify-between text-[10.5px] font-semibold text-[var(--text-muted)]">
                                    <span>{turn.role === "user" ? "USER" : "ASSISTANT"}</span>
                                    <Show when={(turn.tools?.length || 0) > 0}>
                                      <span class="inline-flex items-center gap-1 font-mono text-[10px] text-[var(--accent-primary)]">
                                        <Wrench size={10} />
                                        {turn.tools?.length} tools
                                      </span>
                                    </Show>
                                  </div>
                                  <Show when={turn.text}>
                                    <div class="whitespace-pre-wrap break-words text-[var(--text-primary)] line-clamp-6">
                                      {turn.text}
                                    </div>
                                  </Show>
                                </div>
                              )}
                            </For>
                          </div>
                        </Show>
                      </div>
                    </div>

                    {/* Footer Action Bar */}
                    <div class="flex items-center justify-between border-t border-[var(--border-subtle)] bg-[var(--bg-elevated)] px-5 py-3">
                      <div class="text-[11.5px] text-[var(--text-muted)] truncate">
                        {shortenPath(cur().cwd)}
                      </div>
                      <button
                        type="button"
                        disabled={importingSession()}
                        onClick={() => void onTakeOver()}
                        class="inline-flex items-center gap-2 rounded-xl bg-[var(--accent-primary)] px-4 py-2 text-[12.5px] font-semibold text-white shadow-sm hover:opacity-90 disabled:opacity-50 transition-opacity cursor-pointer"
                      >
                        <Show
                          when={!importingSession()}
                          fallback={<LoaderCircle size={14} class="animate-spin" />}
                        >
                          <ArrowRightLeft size={14} />
                        </Show>
                        {importingSession()
                          ? t("leftNav.handoffTakingOver")
                          : t("leftNav.handoffTakeOverBtn")}
                      </button>
                    </div>
                  </>
                )}
              </Show>
            </div>
          </div>
        </div>
      </div>
    </Show>
  );
}
