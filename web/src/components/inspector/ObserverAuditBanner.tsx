// web/src/components/inspector/ObserverAuditBanner.tsx
import { createMemo, For, Show } from "solid-js";
import { AlertTriangle, CheckCircle2, LoaderCircle, Sparkles, Wrench } from "lucide-solid";
import {
  applyAuditFeedbackToSession,
  findBestFallbackModel,
  openDiffInInspector,
  workspace,
} from "../../store/workspace";
import { t } from "../../i18n";

/**
 * Compact inline badge for AssistantTurnFooter showing live Observer audit status
 * and one-click "Fix Issues" trigger when the audit fails.
 */
export function ObserverAuditBadge(props: { sessionID: string }) {
  const auditing = () => Boolean(workspace.auditingBySession[props.sessionID]);
  const audit = () => workspace.auditsBySession[props.sessionID];

  return (
    <Show
      when={!auditing()}
      fallback={
        <span class="inline-flex items-center gap-1 rounded-full border border-[var(--border-subtle)] bg-[var(--bg-inset)] px-2 py-0.5 text-[10.5px] font-medium text-[var(--text-secondary)]">
          <LoaderCircle size={11} class="animate-spin text-[var(--accent-primary)]" />
          <span>{t("timeline.auditRunningBadge")}</span>
        </span>
      }
    >
      <Show when={audit()}>
        {(res) => (
          <div class="inline-flex items-center gap-1.5 flex-wrap">
            <button
              type="button"
              onClick={() => openDiffInInspector()}
              title={`${res().summary} (${t("inspector.diffs.auditObserverBy", {
                observer: `${res().observerProvider}/${res().observerModel}`,
              })})`}
              class={`inline-flex items-center gap-1 rounded-full border px-2 py-0.5 text-[10.5px] font-medium cursor-pointer transition-opacity hover:opacity-90 ${
                res().pass
                  ? "border-[var(--ok-border)] bg-[var(--ok-surface)] text-[var(--ok-text)]"
                  : "border-[var(--danger-border)] bg-[var(--danger-surface)] text-[var(--danger-text)]"
              }`}
            >
              <Show
                when={res().pass}
                fallback={<AlertTriangle size={11} class="shrink-0" />}
              >
                <CheckCircle2 size={11} class="shrink-0" />
              </Show>
              <span>
                {res().pass
                  ? t("timeline.auditPassedBadge", { score: res().score })
                  : t("timeline.auditFailedBadge", { score: res().score })}
              </span>
            </button>

            <Show when={!res().pass}>
              <button
                type="button"
                onClick={() => void applyAuditFeedbackToSession(props.sessionID)}
                class="inline-flex items-center gap-1 rounded-full border border-[var(--accent-border)] bg-[var(--accent-surface)] px-2 py-0.5 text-[10.5px] font-semibold text-[var(--accent-primary)] hover:opacity-90 cursor-pointer"
              >
                <Wrench size={10} />
                <span>{t("timeline.auditFixWithFeedbackBtn")}</span>
              </button>
            </Show>
          </div>
        )}
      </Show>
    </Show>
  );
}

/**
 * Detailed Observer Code Audit banner for PierreDiffViewer.
 */
export default function ObserverAuditBanner(props: { sessionID: string }) {
  const audit = () => workspace.auditsBySession[props.sessionID];
  const fallbackModel = createMemo(() => {
    const a = audit();
    if (!a || a.pass) return null;
    return findBestFallbackModel(a.workerProviderID || workspace.selectedModel?.providerID);
  });

  return (
    <Show when={audit()}>
      {(res) => (
        <details
          class={`rounded-lg border px-2.5 py-1.5 text-[11px] space-y-1.5 ${
            res().pass
              ? "border-[var(--ok-border)] bg-[var(--ok-surface)]/75 text-[var(--ok-text)]"
              : "border-[var(--danger-border)] bg-[var(--danger-surface)]/85 text-[var(--danger-text)]"
          }`}
        >
          <summary class="cursor-pointer marker:text-[var(--text-muted)]">
            <span class="inline-flex items-center gap-1.5 font-medium align-middle">
              <Show when={res().pass} fallback={<AlertTriangle size={12} />}>
                <CheckCircle2 size={12} />
              </Show>
              {res().pass
                ? t("inspector.diffs.auditCardTitlePass", { score: res().score })
                : t("inspector.diffs.auditCardTitleFail", { score: res().score })}
            </span>
          </summary>
          <div class="border-t border-current/10 pt-2">
            <span class="font-mono text-[10px] opacity-80">
              {t("inspector.diffs.auditObserverBy", {
                observer: `${res().observerProvider}/${res().observerModel}`,
              })}
            </span>
          </div>

          <Show when={res().summary}>
            <div class="text-[11px] opacity-95 leading-snug">{res().summary}</div>
          </Show>

          <Show when={(res().issues || []).length > 0}>
            <ul class="list-disc pl-4 space-y-0.5 text-[11px] font-mono opacity-90">
              <For each={res().issues}>{(iss) => <li>{iss}</li>}</For>
            </ul>
          </Show>

          <Show when={!res().pass}>
            <div class="pt-1 flex items-center gap-2 flex-wrap">
              <button
                type="button"
                onClick={() => void applyAuditFeedbackToSession(props.sessionID)}
                class="inline-flex items-center gap-1 rounded-lg bg-[var(--accent-primary)] px-2.5 py-1 text-[11px] font-semibold text-[var(--accent-foreground)] hover:opacity-90 cursor-pointer"
              >
                <Wrench size={11} />
                <span>{t("timeline.auditFixWithFeedbackBtn")}</span>
              </button>

              <Show when={fallbackModel()}>
                {(fb) => (
                  <button
                    type="button"
                    onClick={() =>
                      void applyAuditFeedbackToSession(props.sessionID, fb())
                    }
                    class="inline-flex items-center gap-1 rounded-lg border border-[var(--ok-border)] bg-[var(--ok-surface)] px-2.5 py-1 text-[11px] font-semibold text-[var(--ok-text)] hover:opacity-90 cursor-pointer"
                  >
                    <Sparkles size={11} />
                    <span>
                      {t("timeline.auditSwitchAndFixBtn", {
                        model: `${fb().providerID}/${fb().modelID}`,
                      })}
                    </span>
                  </button>
                )}
              </Show>
            </div>
          </Show>
        </details>
      )}
    </Show>
  );
}
