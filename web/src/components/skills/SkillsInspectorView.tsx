// web/src/components/skills/SkillsInspectorView.tsx
import { createEffect, createMemo, createSignal, For, Show } from "solid-js";
import {
  AlertCircle,
  ArrowLeft,
  Check,
  Eye,
  EyeOff,
  FileCode2,
  FolderOpen,
  Info,
  LoaderCircle,
  RefreshCw,
  Search,
  Sparkles,
} from "lucide-solid";
import { t } from "../../i18n";
import { hoxiAPI, type SkillDescriptor, type SkillSource } from "../../services/api";
import {
  activeDirectory,
  activeSkillDetail,
  activeSkillDetailError,
  activeSkillDetailKey,
  activeSkillDetailLoading,
  closeSkillDetail,
  getComposerSkill,
  hiddenSkillKeys,
  inspectFileInPanel,
  isSkillHidden,
  loadSkillDetail,
  loadSkillsCatalog,
  selectComposerSkill,
  sessionRuntimeForID,
  setComposerSkillNotice,
  skillsCatalogError,
  skillsCatalogItems,
  skillsCatalogLoading,
  skillsCatalogRuntime,
  skillsCatalogSupported,
  toggleSkillHidden,
  workspace,
} from "../../store/workspace";
import { invalidateComposerSkillsAutocompleteCache } from "../timeline/composer-autocomplete";

const SOURCE_ORDER: SkillSource[] = ["project", "user", "builtin", "external"];

function sourceLabel(source: SkillSource): string {
  switch (source) {
    case "project":
      return t("skills.sourceProject");
    case "user":
      return t("skills.sourceUser");
    case "builtin":
      return t("skills.sourceBuiltin");
    case "external":
      return t("skills.sourceExternal");
  }
}

function sourceBadgeClass(source: SkillSource): string {
  switch (source) {
    case "project":
      return "border-emerald-500/30 bg-emerald-500/10 text-emerald-500";
    case "user":
      return "border-sky-500/30 bg-sky-500/10 text-sky-500";
    case "builtin":
      return "border-[var(--accent-border)] bg-[var(--accent-surface)] text-[var(--accent-text)]";
    case "external":
      return "border-amber-500/30 bg-amber-500/10 text-amber-500";
  }
}

export default function SkillsInspectorView() {
  const [query, setQuery] = createSignal("");
  const [showHidden, setShowHidden] = createSignal(false);
  const [showRefreshHint, setShowRefreshHint] = createSignal(false);

  const sessionID = () => workspace.activeSessionID;
  const directory = () => activeDirectory() || workspace.rootDir || "";
  const runtime = () => sessionRuntimeForID(sessionID());
  const selectedInComposer = createMemo(() => getComposerSkill(sessionID()));

  createEffect(() => {
    const sid = sessionID();
    const dir = directory();
    const rt = runtime();
    void loadSkillsCatalog({ sessionID: sid, directory: dir, runtime: rt });
  });

  const hiddenCount = createMemo(() => {
    const hidden = new Set(hiddenSkillKeys());
    return skillsCatalogItems().filter((item) => hidden.has(item.key)).length;
  });

  const filteredGroups = createMemo(() => {
    const q = query().trim().toLowerCase();
    const includeHidden = showHidden();
    const all = skillsCatalogItems().filter((item) => {
      if (!includeHidden && isSkillHidden(item.key)) return false;
      if (!q) return true;
      return (
        item.name.toLowerCase().includes(q) ||
        (item.description || "").toLowerCase().includes(q) ||
        (item.locationLabel || "").toLowerCase().includes(q)
      );
    });
    return SOURCE_ORDER.map((source) => ({
      source,
      items: all.filter((item) => item.source === source),
    })).filter((g) => g.items.length > 0);
  });

  const handleRefresh = async () => {
    invalidateComposerSkillsAutocompleteCache();
    setShowRefreshHint(true);
    await loadSkillsCatalog(
      { sessionID: sessionID(), directory: directory(), runtime: runtime() },
      { force: true }
    );
    const activeKey = activeSkillDetailKey();
    if (activeKey) {
      await loadSkillDetail({
        key: activeKey,
        sessionID: sessionID(),
        directory: directory(),
        runtime: runtime(),
      });
    }
  };

  const handleToggleComposerSkill = (item: { key: string; name: string }) => {
    const sid = sessionID();
    const current = selectedInComposer();
    if (current?.key === item.key) {
      selectComposerSkill(sid, null);
    } else {
      selectComposerSkill(sid, { key: item.key, name: item.name });
      setComposerSkillNotice(sid, null);
    }
  };

  const handleOpenSkillDetail = (item: SkillDescriptor) => {
    void loadSkillDetail({
      key: item.key,
      sessionID: sessionID(),
      directory: directory(),
      runtime: runtime(),
    });
  };

  return (
    <div class="flex h-full w-full flex-col overflow-hidden">
      {/* Header Bar */}
      <div class="shrink-0 border-b border-[var(--border-subtle)] px-3 py-2.5 space-y-2">
        <div class="flex items-center justify-between gap-2">
          <div class="flex items-center gap-1.5 min-w-0">
            <Sparkles size={14} class="shrink-0 text-[var(--accent-primary)]" />
            <span class="text-xs font-semibold text-[var(--text-primary)] truncate">
              {t("skills.title")}
            </span>
          </div>
          <div class="flex items-center gap-1">
            <Show when={hiddenCount() > 0}>
              <button
                type="button"
                onClick={() => setShowHidden((v) => !v)}
                class={`inline-flex items-center gap-1 rounded-md px-2 py-1 text-[11px] transition-colors cursor-pointer ${
                  showHidden()
                    ? "bg-[var(--accent-surface)] text-[var(--accent-text)]"
                    : "text-[var(--text-muted)] hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)]"
                }`}
                title={t("skills.showHidden")}
              >
                <Show when={showHidden()} fallback={<EyeOff size={12} />}>
                  <Eye size={12} />
                </Show>
                <span>{t("skills.hiddenCount", { count: hiddenCount() })}</span>
              </button>
            </Show>
            <button
              type="button"
              onClick={() => void handleRefresh()}
              disabled={skillsCatalogLoading()}
              class="inline-flex items-center gap-1 rounded-md px-2 py-1 text-[11px] text-[var(--text-muted)] hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)] disabled:opacity-50 transition-colors cursor-pointer"
              title={t("skills.refresh")}
            >
              <RefreshCw size={12} class={skillsCatalogLoading() ? "animate-spin" : ""} />
              <span>{t("skills.refresh")}</span>
            </button>
          </div>
        </div>

        <Show when={skillsCatalogSupported() && !activeSkillDetailKey()}>
          <div class="relative flex items-center">
            <Search size={13} class="absolute left-2.5 text-[var(--text-faint)] pointer-events-none" />
            <input
              type="text"
              value={query()}
              onInput={(e) => setQuery(e.currentTarget.value)}
              placeholder={t("skills.searchPlaceholder")}
              class="w-full rounded-lg border border-[var(--border-subtle)] bg-[var(--bg-inset)] pl-8 pr-3 py-1.5 text-xs text-[var(--text-primary)] placeholder:text-[var(--text-faint)] outline-none focus:border-[var(--accent-border)]"
            />
          </div>
        </Show>

        <Show when={showRefreshHint()}>
          <div class="flex items-start gap-1.5 rounded-lg border border-[var(--border-subtle)] bg-[var(--bg-inset)] px-2.5 py-1.5 text-[11px] text-[var(--text-muted)]">
            <Info size={12} class="mt-0.5 shrink-0 text-[var(--accent-primary)]" />
            <span>{t("skills.refreshNotice")}</span>
          </div>
        </Show>
      </div>

      {/* Body */}
      <div class="flex-1 min-h-0 overflow-y-auto p-3">
        <Show
          when={!skillsCatalogLoading() || skillsCatalogItems().length > 0}
          fallback={
            <div class="flex flex-col items-center justify-center py-12 text-xs text-[var(--text-muted)] gap-2">
              <LoaderCircle size={18} class="animate-spin text-[var(--accent-primary)]" />
              <span>{t("skills.loading")}</span>
            </div>
          }
        >
          <Show
            when={!skillsCatalogError()}
            fallback={
              <div class="rounded-xl border border-[var(--danger-border)] bg-[var(--danger-surface)] p-3 text-xs text-[var(--danger-text)] flex items-start gap-2">
                <AlertCircle size={15} class="shrink-0 mt-0.5" />
                <span>{t("skills.loadFailed", { error: skillsCatalogError() || "" })}</span>
              </div>
            }
          >
            <Show
              when={skillsCatalogSupported()}
              fallback={
                <div class="rounded-xl border border-[var(--border-subtle)] bg-[var(--bg-panel)] p-4 text-center space-y-1.5">
                  <div class="text-xs font-semibold text-[var(--text-primary)]">
                    {t("skills.unsupportedTitle")}
                  </div>
                  <p class="text-[11.5px] leading-relaxed text-[var(--text-muted)]">
                    {t("skills.unsupportedBody", { runtime: skillsCatalogRuntime() })}
                  </p>
                </div>
              }
            >
              {/* Detail View */}
              <Show
                when={activeSkillDetailKey()}
                fallback={
                  <Show
                    when={skillsCatalogItems().length > 0}
                    fallback={
                      <div class="rounded-xl border border-[var(--border-subtle)] bg-[var(--bg-panel)] p-4 text-center space-y-1.5">
                        <div class="text-xs font-semibold text-[var(--text-primary)]">
                          {t("skills.emptyTitle")}
                        </div>
                        <p class="text-[11.5px] leading-relaxed text-[var(--text-muted)]">
                          {t("skills.emptyBody")}
                        </p>
                      </div>
                    }
                  >
                    <Show
                      when={filteredGroups().length > 0}
                      fallback={
                        <div class="py-8 text-center text-xs text-[var(--text-muted)]">
                          {t("skills.noSearchResults")}
                        </div>
                      }
                    >
                      <div class="space-y-4">
                        <For each={filteredGroups()}>
                          {(group) => (
                            <div class="space-y-1.5">
                              <div class="flex items-center justify-between px-1">
                                <span class="text-[10.5px] font-semibold uppercase tracking-wider text-[var(--text-faint)]">
                                  {sourceLabel(group.source)}
                                </span>
                                <span class="text-[10px] font-mono text-[var(--text-faint)]">
                                  {group.items.length}
                                </span>
                              </div>
                              <div class="space-y-1.5">
                                <For each={group.items}>
                                  {(item) => {
                                    const isSelected = () => selectedInComposer()?.key === item.key;
                                    const hidden = () => isSkillHidden(item.key);
                                    return (
                                      <div
                                        class={`group rounded-xl border p-2.5 transition-colors ${
                                          isSelected()
                                            ? "border-[var(--accent-border)] bg-[var(--accent-surface)]/40"
                                            : "border-[var(--border-subtle)] bg-[var(--bg-panel)] hover:border-[var(--border-strong)]"
                                        } ${hidden() ? "opacity-60" : ""}`}
                                      >
                                        <div class="flex items-start justify-between gap-2">
                                          <button
                                            type="button"
                                            onClick={() => handleOpenSkillDetail(item)}
                                            class="min-w-0 flex-1 text-left cursor-pointer"
                                          >
                                            <div class="flex items-center gap-1.5 flex-wrap">
                                              <span class="font-mono text-xs font-semibold text-[var(--text-primary)] group-hover:text-[var(--accent-primary)] truncate">
                                                /{item.name}
                                              </span>
                                              <span
                                                class={`rounded border px-1.5 py-0.5 text-[9.5px] font-medium ${sourceBadgeClass(
                                                  item.source
                                                )}`}
                                              >
                                                {sourceLabel(item.source)}
                                              </span>
                                            </div>
                                            <Show when={item.description}>
                                              <p class="mt-1 line-clamp-2 text-[11.5px] leading-relaxed text-[var(--text-secondary)]">
                                                {item.description}
                                              </p>
                                            </Show>
                                            <Show when={item.locationLabel}>
                                              <div class="mt-1 truncate font-mono text-[10px] text-[var(--text-faint)]">
                                                {item.locationLabel}
                                              </div>
                                            </Show>
                                          </button>

                                          <div class="flex items-center gap-1 shrink-0">
                                            <button
                                              type="button"
                                              onClick={() => {
                                                toggleSkillHidden(item.key);
                                                invalidateComposerSkillsAutocompleteCache();
                                              }}
                                              class="rounded-md p-1 text-[var(--text-muted)] hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)] cursor-pointer"
                                              title={
                                                hidden()
                                                  ? t("skills.unhideSkill")
                                                  : t("skills.hideSkill")
                                              }
                                            >
                                              <Show when={hidden()} fallback={<Eye size={13} />}>
                                                <EyeOff size={13} />
                                              </Show>
                                            </button>
                                            <button
                                              type="button"
                                              onClick={() => handleToggleComposerSkill(item)}
                                              class={`inline-flex items-center gap-1 rounded-lg px-2 py-1 text-[11px] font-medium transition-colors cursor-pointer ${
                                                isSelected()
                                                  ? "bg-[var(--accent-primary)] text-[var(--accent-foreground)]"
                                                  : "border border-[var(--border-subtle)] bg-[var(--bg-inset)] text-[var(--text-primary)] hover:border-[var(--accent-border)]"
                                              }`}
                                            >
                                              <Show when={isSelected()}>
                                                <Check size={11} />
                                              </Show>
                                              <span>
                                                {isSelected()
                                                  ? t("skills.removeFromComposer")
                                                  : t("skills.useInComposer")}
                                              </span>
                                            </button>
                                          </div>
                                        </div>
                                      </div>
                                    );
                                  }}
                                </For>
                              </div>
                            </div>
                          )}
                        </For>
                      </div>
                    </Show>
                  </Show>
                }
              >
                <div class="space-y-3">
                  <button
                    type="button"
                    onClick={() => closeSkillDetail()}
                    class="inline-flex items-center gap-1.5 text-xs font-medium text-[var(--text-muted)] hover:text-[var(--text-primary)] cursor-pointer"
                  >
                    <ArrowLeft size={13} />
                    <span>{t("skills.backToList")}</span>
                  </button>

                  <Show
                    when={!activeSkillDetailLoading()}
                    fallback={
                      <div class="flex items-center justify-center py-10 text-xs text-[var(--text-muted)] gap-2">
                        <LoaderCircle size={16} class="animate-spin text-[var(--accent-primary)]" />
                        <span>{t("skills.detailLoading")}</span>
                      </div>
                    }
                  >
                    <Show
                      when={!activeSkillDetailError() && activeSkillDetail()}
                      fallback={
                        <div class="rounded-xl border border-[var(--danger-border)] bg-[var(--danger-surface)] p-3 text-xs text-[var(--danger-text)]">
                          {t("skills.detailLoadFailed", {
                            error: activeSkillDetailError() || "",
                          })}
                        </div>
                      }
                    >
                      {(detail) => {
                        const isSelected = () => selectedInComposer()?.key === detail().key;
                        return (
                          <div class="space-y-3">
                            <div class="rounded-xl border border-[var(--border-subtle)] bg-[var(--bg-panel)] p-3 space-y-2.5">
                              <div class="flex items-start justify-between gap-2">
                                <div class="min-w-0">
                                  <div class="flex items-center gap-1.5 flex-wrap">
                                    <span class="font-mono text-sm font-semibold text-[var(--text-primary)]">
                                      /{detail().name}
                                    </span>
                                    <span
                                      class={`rounded border px-1.5 py-0.5 text-[10px] font-medium ${sourceBadgeClass(
                                        detail().source
                                      )}`}
                                    >
                                      {sourceLabel(detail().source)}
                                    </span>
                                  </div>
                                  <Show when={detail().description}>
                                    <p class="mt-1 text-xs leading-relaxed text-[var(--text-secondary)]">
                                      {detail().description}
                                    </p>
                                  </Show>
                                </div>
                                <button
                                  type="button"
                                  onClick={() => handleToggleComposerSkill(detail())}
                                  class={`shrink-0 inline-flex items-center gap-1 rounded-lg px-2.5 py-1 text-xs font-medium transition-colors cursor-pointer ${
                                    isSelected()
                                      ? "bg-[var(--accent-primary)] text-[var(--accent-foreground)]"
                                      : "border border-[var(--border-subtle)] bg-[var(--bg-inset)] text-[var(--text-primary)] hover:border-[var(--accent-border)]"
                                  }`}
                                >
                                  <Show when={isSelected()}>
                                    <Check size={12} />
                                  </Show>
                                  <span>
                                    {isSelected()
                                      ? t("skills.removeFromComposer")
                                      : t("skills.useInComposer")}
                                  </span>
                                </button>
                              </div>

                              <div class="flex flex-wrap items-center gap-2 pt-1 border-t border-[var(--border-subtle)] text-[11px] text-[var(--text-muted)]">
                                <span class="font-mono truncate max-w-full">
                                  {detail().locationLabel}
                                </span>
                                <Show when={detail().contentDigest}>
                                  <span class="rounded bg-[var(--bg-inset)] px-1.5 py-0.5 font-mono text-[10px] text-[var(--text-faint)]">
                                    {t("skills.digestLabel")}: {detail().contentDigest.slice(0, 8)}
                                  </span>
                                </Show>
                              </div>

                              <Show
                                when={detail().canOpenLocation && detail().location}
                                fallback={
                                  <div class="text-[11px] text-[var(--text-faint)]">
                                    {t("skills.builtinReadOnly")}
                                  </div>
                                }
                              >
                                <div class="flex flex-wrap items-center gap-1.5">
                                  <Show when={detail().source === "project"}>
                                    <button
                                      type="button"
                                      onClick={() =>
                                        inspectFileInPanel(detail().locationLabel || detail().location!)
                                      }
                                      class="inline-flex items-center gap-1 rounded-md border border-[var(--border-subtle)] bg-[var(--bg-inset)] px-2 py-1 text-[11px] text-[var(--text-primary)] hover:border-[var(--accent-border)] cursor-pointer"
                                    >
                                      <FileCode2 size={12} />
                                      <span>{t("skills.openInFiles")}</span>
                                    </button>
                                  </Show>
                                  <button
                                    type="button"
                                    onClick={() => void hoxiAPI.revealInFinder(detail().location!)}
                                    class="inline-flex items-center gap-1 rounded-md border border-[var(--border-subtle)] bg-[var(--bg-inset)] px-2 py-1 text-[11px] text-[var(--text-primary)] hover:border-[var(--accent-border)] cursor-pointer"
                                  >
                                    <FolderOpen size={12} />
                                    <span>{t("skills.revealInFinder")}</span>
                                  </button>
                                </div>
                              </Show>
                            </div>

                            <div class="rounded-xl border border-[var(--border-subtle)] bg-[var(--bg-inset)] px-3 py-2 text-[11px] leading-relaxed text-[var(--text-muted)]">
                              {t("skills.permissionNotice")}
                            </div>

                            <div class="space-y-1.5">
                              <div class="px-1 text-[10.5px] font-semibold uppercase tracking-wider text-[var(--text-faint)]">
                                {t("skills.instructionsHeader")}
                              </div>
                              <pre class="max-h-[420px] overflow-auto rounded-xl border border-[var(--border-subtle)] bg-[var(--bg-inset)] p-3 font-mono text-[11.5px] leading-relaxed text-[var(--text-primary)] whitespace-pre-wrap break-words selectable-text">
                                {detail().content}
                              </pre>
                            </div>
                          </div>
                        );
                      }}
                    </Show>
                  </Show>
                </div>
              </Show>
            </Show>
          </Show>
        </Show>
      </div>
    </div>
  );
}
