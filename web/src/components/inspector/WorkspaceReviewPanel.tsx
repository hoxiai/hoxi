// web/src/components/inspector/WorkspaceReviewPanel.tsx
import { createSignal, For, Show } from "solid-js";
import { AlertTriangle, CheckCircle2, GitBranch, LoaderCircle } from "lucide-solid";
import { runWorkspaceReview, workspace } from "../../store/workspace";
import type { WorkspaceReviewResult, WorkspaceReviewScope } from "../../services/api";
import { t } from "../../i18n";

const CONTROL_CLASS =
  "inline-flex items-center gap-1 rounded-md border border-[var(--border-strong)] bg-[var(--bg-panel)] px-2 py-1 text-[11px] font-medium text-[var(--text-primary)] hover:bg-[var(--bg-hover)] cursor-pointer disabled:cursor-not-allowed disabled:opacity-50";

function scopeLabel(review: WorkspaceReviewResult): string {
  return review.scope === "branch"
    ? t("inspector.review.scopeBranch", { base: review.baseRef || "" })
    : t("inspector.review.scopeUncommitted");
}

/**
 * Runs a workspace review of the active session's repository: either the
 * uncommitted changes or the branch compared with a base branch.
 */
export default function WorkspaceReviewPanel(props: { sessionID: string }) {
  const [running, setRunning] = createSignal<WorkspaceReviewScope | null>(null);
  const [baseRef, setBaseRef] = createSignal("main");
  const review = () => workspace.reviewsBySession[props.sessionID];

  const start = async (scope: WorkspaceReviewScope) => {
    if (running()) return;
    setRunning(scope);
    try {
      await runWorkspaceReview(props.sessionID, scope, baseRef());
    } finally {
      setRunning(null);
    }
  };

  return (
    <div class="rounded-lg border border-[var(--border-subtle)] bg-[var(--bg-inset)]/60 px-2.5 py-2 space-y-2 text-[11px]">
      <div class="flex items-center gap-1.5 font-medium text-[var(--text-primary)]">
        <GitBranch size={12} class="text-[var(--accent-primary)]" />
        <span>{t("inspector.review.title")}</span>
      </div>

      <div class="flex items-center gap-1.5 flex-wrap">
        <input
          type="text"
          value={baseRef()}
          onInput={(e) => setBaseRef(e.currentTarget.value)}
          aria-label={t("inspector.review.baseLabel")}
          placeholder={t("inspector.review.baseLabel")}
          class="w-28 rounded-md border border-[var(--border-strong)] bg-[var(--bg-panel)] px-2 py-1 font-mono text-[11px] text-[var(--text-primary)] outline-none focus:border-[var(--accent-primary)]"
        />
        <button
          type="button"
          disabled={running() !== null}
          onClick={() => void start("uncommitted")}
          class={CONTROL_CLASS}
        >
          <span>{t("inspector.review.runUncommitted")}</span>
        </button>
        <button
          type="button"
          disabled={running() !== null || !baseRef().trim()}
          onClick={() => void start("branch")}
          class={CONTROL_CLASS}
        >
          <span>{t("inspector.review.runBranch")}</span>
        </button>
      </div>

      <Show when={running()}>
        <div class="inline-flex items-center gap-1.5 text-[var(--text-secondary)]">
          <LoaderCircle size={11} class="animate-spin text-[var(--accent-primary)]" />
          <span>{t("inspector.review.running")}</span>
        </div>
      </Show>

      <Show when={review()}>
        {(res) => (
          <div class="space-y-1.5">
            <div class="font-mono text-[10px] text-[var(--text-muted)]">{scopeLabel(res())}</div>
            <Show
              when={res().ok}
              fallback={
                <div class="text-[var(--danger-text)]">{res().error || t("inspector.review.failed")}</div>
              }
            >
              <span
                class={`inline-flex items-center gap-1 rounded-full border px-2 py-0.5 text-[10.5px] font-medium ${
                  res().pass
                    ? "border-[var(--ok-border)] bg-[var(--ok-surface)] text-[var(--ok-text)]"
                    : "border-[var(--danger-border)] bg-[var(--danger-surface)] text-[var(--danger-text)]"
                }`}
              >
                <Show when={res().pass} fallback={<AlertTriangle size={11} class="shrink-0" />}>
                  <CheckCircle2 size={11} class="shrink-0" />
                </Show>
                <span>
                  {res().pass
                    ? t("timeline.auditPassedBadge", { score: res().score })
                    : t("timeline.auditFailedBadge", { score: res().score })}
                </span>
              </span>
              <Show when={res().summary}>
                <div class="leading-snug text-[var(--text-secondary)]">{res().summary}</div>
              </Show>
              <Show when={(res().issues || []).length > 0}>
                <ul class="list-disc pl-4 space-y-0.5 font-mono text-[10.5px] text-[var(--text-secondary)]">
                  <For each={res().issues}>{(iss) => <li>{iss}</li>}</For>
                </ul>
              </Show>
            </Show>
          </div>
        )}
      </Show>
    </div>
  );
}
