// web/src/components/handoff/ExternalHandoffPreviewPane.tsx
import { For, Show } from "solid-js";
import { ArrowRightLeft, GitBranch, LoaderCircle, Wrench } from "lucide-solid";
import type {
  ExternalSessionDetail,
  ExternalSessionSummary,
  ExternalToolID,
} from "../../services/api";
import { shortenPath } from "../layout/left-nav-helpers";
import { t } from "../../i18n";

export function handoffSourceBadgeClass(source: ExternalToolID): string {
  if (source === "grok-cli") return "bg-purple-500/15 text-purple-400 border-purple-500/30";
  if (source === "claude-code") return "bg-amber-500/15 text-amber-400 border-amber-500/30";
  if (source === "gemini-antigravity") return "bg-sky-500/15 text-sky-400 border-sky-500/30";
  return "bg-emerald-500/15 text-emerald-400 border-emerald-500/30";
}

export function resolveOriginalModel(
  item: ExternalSessionSummary | null,
  detail: ExternalSessionDetail | null,
  cliDefaultModel?: string
): string {
  if (!item) return "";
  return (
    detail?.summary?.model ||
    item.model ||
    detail?.turns?.find((t) => t.modelID)?.modelID ||
    cliDefaultModel ||
    item.sourceLabel
  );
}

export default function ExternalHandoffPreviewPane(props: {
  selectedItem: ExternalSessionSummary | null;
  detail: ExternalSessionDetail | null;
  loadingDetail: boolean;
  importingSession: boolean;
  modelMode: "original" | "local";
  onChangeModelMode: (mode: "original" | "local") => void;
  originalModelLabel: string;
  localModelLabel: string;
  onTakeOver: () => void;
}) {
  return (
    <div class="flex-1 flex flex-col min-w-0 overflow-hidden bg-[var(--bg-canvas)]/30">
      <Show
        when={props.selectedItem}
        fallback={
          <div class="flex flex-1 items-center justify-center p-8 text-center text-[12px] text-[var(--text-muted)]">
            {t("leftNav.handoffSelectHint")}
          </div>
        }
      >
        {(cur) => (
          <>
            {/* Compact Session Header */}
            <div class="border-b border-[var(--border-subtle)] bg-[var(--bg-elevated)] px-5 py-3 space-y-1.5">
              <div class="text-[14px] font-semibold text-[var(--text-primary)] truncate">
                {cur().title}
              </div>

              <div class="flex flex-wrap items-center gap-2 text-[11px] font-mono text-[var(--text-muted)]">
                <span
                  class={`rounded border px-1.5 py-0.2 text-[10px] font-sans font-medium ${handoffSourceBadgeClass(
                    cur().source
                  )}`}
                >
                  {cur().sourceLabel}
                </span>
                <span class="truncate max-w-[260px]" title={cur().cwd}>
                  {shortenPath(cur().cwd)}
                </span>
                <Show when={cur().gitBranch}>
                  <span class="inline-flex items-center gap-1 rounded bg-[var(--bg-inset)] px-1.5 py-0.2 text-[var(--text-secondary)]">
                    <GitBranch size={10} />
                    {cur().gitBranch}
                  </span>
                </Show>
                <Show when={props.detail?.turns}>
                  <span class="font-sans">
                    · {t("leftNav.handoffTurnsCount", { count: props.detail?.turns.length || 0 })}
                  </span>
                </Show>
                <Show when={(props.detail?.changedFiles?.length || 0) > 0}>
                  <span class="font-sans">
                    · {t("leftNav.handoffChangedFilesTitle")} {props.detail?.changedFiles?.length}
                  </span>
                </Show>
              </div>
            </div>

            {/* Streamlined Conversation Preview */}
            <div class="flex-1 overflow-y-auto px-5 py-3.5">
              <Show
                when={!props.loadingDetail}
                fallback={
                  <div class="flex items-center justify-center py-12 text-[var(--text-muted)]">
                    <LoaderCircle size={16} class="animate-spin" />
                  </div>
                }
              >
                <div class="space-y-2.5">
                  <For each={props.detail?.turns || []}>
                    {(turn) => (
                      <div
                        class={`rounded-xl px-3.5 py-2.5 text-[12px] ${
                          turn.role === "user"
                            ? "bg-[var(--accent-surface)]/25 border border-[var(--accent-border)]/30"
                            : "bg-[var(--bg-elevated)] border border-[var(--border-subtle)]"
                        }`}
                      >
                        <div class="mb-1 flex items-center justify-between text-[10.5px] font-medium text-[var(--text-muted)]">
                          <span>{turn.role === "user" ? "User" : cur().sourceLabel}</span>
                          <Show when={(turn.tools?.length || 0) > 0}>
                            <span class="inline-flex items-center gap-1 font-mono text-[10px] text-[var(--text-muted)]">
                              <Wrench size={10} />
                              {turn.tools?.length}
                            </span>
                          </Show>
                        </div>
                        <Show when={turn.text}>
                          <div class="whitespace-pre-wrap break-words text-[var(--text-primary)] line-clamp-5 leading-relaxed">
                            {turn.text}
                          </div>
                        </Show>
                      </div>
                    )}
                  </For>
                </div>
              </Show>
            </div>

            {/* Footer Action Bar */}
            <div class="flex items-center justify-between gap-2 border-t border-[var(--border-subtle)] bg-[var(--bg-elevated)] px-5 py-2.5">
              <div class="inline-flex rounded-lg bg-[var(--bg-inset)] p-0.5 text-[11px]">
                <button
                  type="button"
                  onClick={() => props.onChangeModelMode("original")}
                  class={`rounded-md px-2.5 py-1 font-medium transition-colors cursor-pointer ${
                    props.modelMode === "original"
                      ? "bg-[var(--bg-elevated)] text-[var(--text-primary)] shadow-2xs"
                      : "text-[var(--text-muted)] hover:text-[var(--text-primary)]"
                  }`}
                >
                  {t("leftNav.handoffModelModeOriginal", {
                    model: props.originalModelLabel,
                  })}
                </button>
                <button
                  type="button"
                  onClick={() => props.onChangeModelMode("local")}
                  class={`rounded-md px-2.5 py-1 font-medium transition-colors cursor-pointer ${
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
                class="inline-flex items-center gap-1.5 rounded-lg bg-[var(--accent-primary)] px-3.5 py-1.5 text-[12px] font-medium text-white hover:opacity-90 disabled:opacity-50 transition-opacity cursor-pointer"
              >
                <Show
                  when={!props.importingSession}
                  fallback={<LoaderCircle size={13} class="animate-spin" />}
                >
                  <ArrowRightLeft size={13} />
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
