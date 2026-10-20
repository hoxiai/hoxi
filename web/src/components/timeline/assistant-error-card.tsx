// web/src/components/timeline/assistant-error-card.tsx
import { createEffect, createMemo, createSignal, onCleanup, Show } from "solid-js";
import type { MessageInfo, SessionStatus } from "../../services/sse-types";
import {
  computeProviderHealth,
  findBestFallbackModel,
  getEffectivePoolCandidates,
  isModelFree,
  isRunnerStallError,
  isUserAbortError,
  openDiagnostics,
  openProviderModal,
  retryLastUserPrompt,
  selectModel,
  workspace,
} from "../../store/workspace";
import { ProviderHealthBadge } from "../models/ProviderHealthBadge";
import { Icon } from "../icons";
import { t } from "../../i18n";

/**
 * Renders provider / model API errors returned on an assistant turn (`info.error`)
 * with relay quality diagnostics and one-click recovery actions.
 */
export function AssistantErrorCard(props: { info: MessageInfo }) {
  const isStall = () => isRunnerStallError(props.info);
  const errTitle = () =>
    isStall() ? t("timeline.stalledErrorTitle") : props.info.error?.name || t("timeline.apiError");
  const errMessage = () =>
    (isStall() && t("timeline.stalledErrorMessage")) ||
    props.info.error?.data?.message ||
    props.info.error?.name ||
    t("timeline.modelRequestFailed");

  const statusCode = () => props.info.error?.data?.statusCode;
  const providerID = () => props.info.providerID || workspace.selectedModel?.providerID || "";
  const modelID = () => props.info.modelID || workspace.selectedModel?.modelID || "";
  const providerHealth = createMemo(() =>
    providerID() ? computeProviderHealth(providerID(), modelID() || undefined) : null
  );
  // A stall is not a provider failure, so model-switch suggestions are hidden.
  const recommendedFallback = createMemo(() =>
    providerID() && !isStall() ? findBestFallbackModel(providerID()) : null
  );

  const freeFallbackModel = createMemo(() => {
    const curPID = workspace.selectedModel?.providerID;
    const curMID = workspace.selectedModel?.modelID;
    const errPID = props.info.providerID;
    const errMID = props.info.modelID;
    const isSameAsActiveOrFailed = (pid: string, mid: string) =>
      (curPID === pid && curMID === mid) || (errPID === pid && errMID === mid);

    for (const c of getEffectivePoolCandidates("free")) {
      if (!c?.providerID || !c?.modelID || isSameAsActiveOrFailed(c.providerID, c.modelID)) {
        continue;
      }
      const prov = workspace.providers.find((p) => p.id === c.providerID);
      if (prov && isModelFree(c.providerID, c.modelID, prov.models?.[c.modelID])) {
        return { providerID: c.providerID, modelID: c.modelID };
      }
    }
    const connSet = new Set(workspace.connectedProviders);
    const cfgProviders = workspace.globalConfig?.provider ?? {};
    for (const p of workspace.providers) {
      const isConn = connSet.has(p.id) || Boolean(cfgProviders[p.id]);
      for (const [mid, mInfo] of Object.entries(p.models || {})) {
        if (!isConn && p.id !== "opencode") continue;
        if (isModelFree(p.id, mid, mInfo) && !isSameAsActiveOrFailed(p.id, mid)) {
          return { providerID: p.id, modelID: mid };
        }
      }
    }
    return null;
  });

  return (
    <Show when={props.info.error && !isUserAbortError(props.info)}>
      <div class="my-2 rounded-xl border border-[var(--danger-border)] bg-[var(--danger-surface)] px-3.5 py-3 text-[13px] text-[var(--danger-text)]">
        <div class="flex items-start gap-2.5">
          <Icon name="circle-ban-sign" size="small" class="mt-0.5 shrink-0" />
          <div class="min-w-0 flex-1 space-y-1.5">
            <div class="flex items-center gap-2 flex-wrap">
              <span class="font-semibold">
                {errTitle()}
                {statusCode() ? ` (${statusCode()})` : ""}
              </span>
              <Show when={props.info.providerID || props.info.modelID}>
                <span class="rounded bg-black/10 dark:bg-white/10 px-1.5 py-0.5 font-mono text-[11px]">
                  {[props.info.providerID, props.info.modelID].filter(Boolean).join(" / ")}
                </span>
              </Show>
              <Show when={providerID()}>
                <ProviderHealthBadge providerID={providerID()} modelID={modelID() || undefined} />
              </Show>
            </div>

            <div class="font-mono text-[12px] leading-relaxed break-words opacity-95">
              {errMessage()}
            </div>

            <Show
              when={
                !isStall() &&
                providerHealth() &&
                (providerHealth()!.status === "degraded" ||
                  providerHealth()!.status === "warning" ||
                  providerHealth()!.riskTags.length > 0)
              }
            >
              <div class="rounded-lg border border-[var(--warn-border)] bg-[var(--warn-surface)]/80 px-2.5 py-1.5 text-[11px] text-[var(--warn-text)] space-y-0.5">
                <div class="font-semibold">
                  {t("timeline.relayDegradedWarningTitle", {
                    provider: providerID(),
                    score:
                      providerHealth()!.stabilityScore ??
                      providerHealth()!.qualityScore ??
                      "—",
                  })}
                </div>
                <div class="opacity-90">{t("timeline.relayDegradedWarningDesc")}</div>
              </div>
            </Show>

            <div class="pt-1 flex items-center gap-2 flex-wrap">
              <button
                type="button"
                onClick={() => openDiagnostics({ sessionID: props.info.sessionID })}
                class="inline-flex items-center gap-1 rounded-lg border border-[var(--danger-border)] bg-[var(--bg-panel)]/80 px-2.5 py-1 text-[11px] font-medium text-[var(--text-primary)] hover:bg-[var(--bg-hover)] cursor-pointer"
              >
                {t("diagnostics.view")}
              </button>
              <Show when={workspace.selectedModel}>
                {(sel) => (
                  <button
                    type="button"
                    onClick={() => void retryLastUserPrompt(props.info.sessionID)}
                    class="inline-flex items-center gap-1.5 rounded-lg bg-[var(--accent-primary)] px-2.5 py-1 text-[11px] font-medium text-[var(--accent-foreground)] hover:opacity-90 transition-opacity cursor-pointer"
                  >
                    <Icon name="arrow-up" size="small" />
                    <span>{t("timeline.retryWithModel", { model: sel().modelID })}</span>
                  </button>
                )}
              </Show>

              <Show when={recommendedFallback()}>
                {(fb) => (
                  <button
                    type="button"
                    onClick={() => {
                      selectModel(fb(), false);
                      void retryLastUserPrompt(props.info.sessionID);
                    }}
                    class="inline-flex items-center gap-1.5 rounded-lg border border-[var(--ok-border)] bg-[var(--ok-surface)] px-2.5 py-1 text-[11px] font-medium text-[var(--ok-text)] hover:opacity-90 transition-opacity cursor-pointer"
                  >
                    <Icon name="check-small" size="small" />
                    <span>
                      {t("timeline.switchRecommendedModel", {
                        provider: fb().providerID,
                        model: fb().modelID,
                      })}
                    </span>
                  </button>
                )}
              </Show>

              <Show
                when={
                  !isStall() &&
                  !recommendedFallback() &&
                  freeFallbackModel() &&
                  !(
                    workspace.selectedModel?.providerID === freeFallbackModel()!.providerID &&
                    workspace.selectedModel?.modelID === freeFallbackModel()!.modelID
                  )
                }
              >
                <button
                  type="button"
                  onClick={() => {
                    const free = freeFallbackModel();
                    if (!free) return;
                    selectModel(free, false);
                    void retryLastUserPrompt(props.info.sessionID);
                  }}
                  class="inline-flex items-center gap-1.5 rounded-lg border border-[var(--accent-border)] bg-[var(--accent-surface)] px-2.5 py-1 text-[11px] font-medium text-[var(--accent-text)] hover:opacity-90 transition-opacity cursor-pointer"
                >
                  <Icon name="check-small" size="small" />
                  <span>
                    {t("timeline.switchAndRetryFree", {
                      model: freeFallbackModel()?.modelID || "",
                    })}
                  </span>
                </button>
              </Show>

              <button
                type="button"
                onClick={() => openProviderModal("providers")}
                class="inline-flex items-center gap-1 rounded-lg border border-[var(--danger-border)] bg-[var(--bg-panel)]/80 px-2.5 py-1 text-[11px] font-medium text-[var(--text-primary)] hover:bg-[var(--bg-hover)] transition-colors cursor-pointer"
              >
                <Icon name="sliders" size="small" />
                <span>{t("timeline.configureModels")}</span>
              </button>
            </div>
          </div>
        </div>
      </div>
    </Show>
  );
}

/**
 * Ported from @opencode-ai/session-ui SessionRetry
 * Live 1-second countdown card when a session status is `retry` (e.g. rate limit / transient network backoff).
 */
export function SessionRetryCard(props: { status: Extract<SessionStatus, { type: "retry" }> }) {
  const [seconds, setSeconds] = createSignal(
    Math.max(0, Math.round((props.status.next - Date.now()) / 1000))
  );

  createEffect(() => {
    const update = () => {
      setSeconds(Math.max(0, Math.round((props.status.next - Date.now()) / 1000)));
    };
    update();
    const timer = setInterval(update, 1000);
    onCleanup(() => clearInterval(timer));
  });

  const activeProviderID = () => workspace.selectedModel?.providerID || "";

  return (
    <div class="my-2 flex items-start gap-2.5 rounded-xl border border-[var(--warn-border)] bg-[var(--warn-surface)] px-3.5 py-2.5 text-[13px] text-[var(--warn-text)]">
      <Icon name="warning" size="small" class="mt-0.5 shrink-0" />
      <div class="min-w-0 flex-1 space-y-1">
        <div class="flex items-center gap-2 flex-wrap font-medium">
          <span>
            {t("timeline.retryingInSeconds", { seconds: seconds() })}{" "}
            <span class="font-mono text-[11px] opacity-80">
              ({t("timeline.retryAttempt", { attempt: props.status.attempt })})
            </span>
          </span>
          <Show when={activeProviderID()}>
            <ProviderHealthBadge providerID={activeProviderID()} />
          </Show>
        </div>
        <Show when={props.status.message}>
          <div class="text-[12px] opacity-90 break-words font-mono">{props.status.message}</div>
        </Show>
      </div>
    </div>
  );
}
