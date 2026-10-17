// web/src/components/layout/ExternalHandoffPreviewPane.tsx
import { For, Show } from "solid-js";
import {
  ArrowRightLeft,
  FileCode2,
  FolderGit2,
  GitBranch,
  ListTodo,
  LoaderCircle,
  Wrench,
} from "lucide-solid";
import type {
  ExternalSessionDetail,
  ExternalSessionSummary,
  ExternalToolID,
} from "../../services/api";
import { shortenPath } from "./left-nav-helpers";
import { t } from "../../i18n";

export function handoffSourceBadgeClass(source: ExternalToolID): string {
  if (source === "grok-cli") return "bg-purple-500/15 text-purple-400 border-purple-500/30";
  if (source === "claude-code") return "bg-amber-500/15 text-amber-400 border-amber-500/30";
  return "bg-emerald-500/15 text-emerald-400 border-emerald-500/30";
}

export function defaultSourceModel(source: ExternalToolID, rawModel?: string): string {
  if (rawModel) return rawModel;
  if (source === "grok-cli") return "grok-4.7";
  if (source === "claude-code") return "claude-sonnet-4-6";
  return "gpt-5.3-codex";
}

export default function ExternalHandoffPreviewPane(props: {
  selectedItem: ExternalSessionSummary | null;
  detail: ExternalSessionDetail | null;
  loadingDetail: boolean;
  importingSession: boolean;
  modelMode: "original" | "local";
  onChangeModelMode: (mode: "original" | "local") => void;
  localModelLabel: string;
  onTakeOver: () => void;
}) {
  return (
    <div class="flex-1 flex flex-col min-w-0 overflow-hidden bg-[var(--bg-canvas)]/40">
      <Show
        when={props.selectedItem}
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
                      class={`rounded-md border px-2 py-0.5 text-[10.5px] font-semibold ${handoffSourceBadgeClass(
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
                    <Show when={props.detail?.turns}>
                      <span class="text-[11px] text-[var(--text-muted)]">
                        {t("leftNav.handoffTurnsCount", {
                          count: props.detail?.turns.length || 0,
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

                <Show when={cur().summary || props.detail?.lastTurnSummary}>
                  <div class="mt-2 rounded-lg bg-[var(--bg-inset)]/70 p-3 text-[11.5px] leading-relaxed text-[var(--text-secondary)] whitespace-pre-wrap">
                    <div class="mb-1 text-[10.5px] font-semibold uppercase tracking-wider text-[var(--text-muted)]">
                      {t("leftNav.handoffSummaryTitle")}
                    </div>
                    {cur().summary || props.detail?.lastTurnSummary}
                  </div>
                </Show>
              </div>

              {/* Plan & Changed Files */}
              <Show
                when={
                  (props.detail?.planSteps?.length || 0) > 0 ||
                  (props.detail?.changedFiles?.length || 0) > 0
                }
              >
                <div class="grid grid-cols-2 gap-3">
                  <Show when={(props.detail?.planSteps?.length || 0) > 0}>
                    <div class="rounded-xl border border-[var(--border-subtle)] bg-[var(--bg-elevated)] p-3.5 space-y-2">
                      <div class="flex items-center gap-1.5 text-[11px] font-semibold text-[var(--text-secondary)]">
                        <ListTodo size={13} class="text-[var(--accent-primary)]" />
                        {t("leftNav.handoffPlanTitle")}
                      </div>
                      <div class="space-y-1 max-h-36 overflow-y-auto">
                        <For each={props.detail?.planSteps}>
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

                  <Show when={(props.detail?.changedFiles?.length || 0) > 0}>
                    <div class="rounded-xl border border-[var(--border-subtle)] bg-[var(--bg-elevated)] p-3.5 space-y-2">
                      <div class="flex items-center gap-1.5 text-[11px] font-semibold text-[var(--text-secondary)]">
                        <FileCode2 size={13} class="text-[var(--accent-primary)]" />
                        {t("leftNav.handoffChangedFilesTitle")} (
                        {props.detail?.changedFiles?.length})
                      </div>
                      <div class="space-y-1 max-h-36 overflow-y-auto font-mono text-[10.5px] text-[var(--text-secondary)]">
                        <For each={props.detail?.changedFiles}>
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
                  when={!props.loadingDetail}
                  fallback={
                    <div class="flex items-center justify-center py-10 text-[var(--text-muted)]">
                      <LoaderCircle size={16} class="animate-spin" />
                    </div>
                  }
                >
                  <div class="space-y-2">
                    <For each={props.detail?.turns || []}>
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
            <div class="flex flex-wrap items-center justify-between gap-2 border-t border-[var(--border-subtle)] bg-[var(--bg-elevated)] px-5 py-3">
              <div class="inline-flex rounded-xl border border-[var(--border-subtle)] bg-[var(--bg-inset)] p-0.5 text-[11px]">
                <button
                  type="button"
                  onClick={() => props.onChangeModelMode("original")}
                  class={`rounded-lg px-2.5 py-1 font-medium transition-colors cursor-pointer ${
                    props.modelMode === "original"
                      ? "bg-[var(--bg-elevated)] text-[var(--text-primary)] shadow-2xs"
                      : "text-[var(--text-muted)] hover:text-[var(--text-primary)]"
                  }`}
                >
                  {t("leftNav.handoffModelModeOriginal", {
                    model: defaultSourceModel(cur().source, cur().model),
                  })}
                </button>
                <button
                  type="button"
                  onClick={() => props.onChangeModelMode("local")}
                  class={`rounded-lg px-2.5 py-1 font-medium transition-colors cursor-pointer ${
                    props.modelMode === "local"
                      ? "bg-[var(--bg-elevated)] text-[var(--text-primary)] shadow-2xs"
                      : "text-[var(--text-muted)] hover:text-[var(--text-primary)]"
                  }`}
                >
                  {t("leftNav.handoffModelModeLocal", {
                    model: props.localModelLabel,
                  })}
                </button>
              </div>
              <button
                type="button"
                disabled={props.importingSession}
                onClick={props.onTakeOver}
                class="inline-flex items-center gap-2 rounded-xl bg-[var(--accent-primary)] px-4 py-2 text-[12.5px] font-semibold text-white shadow-sm hover:opacity-90 disabled:opacity-50 transition-opacity cursor-pointer"
              >
                <Show
                  when={!props.importingSession}
                  fallback={<LoaderCircle size={14} class="animate-spin" />}
                >
                  <ArrowRightLeft size={14} />
                </Show>
                {props.importingSession
                  ? t("leftNav.handoffTakingOver")
                  : t("leftNav.handoffTakeOverBtn")}
              </button>
            </div>
          </>
        )}
      </Show>
    </div>
  );
}
