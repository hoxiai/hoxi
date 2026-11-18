// web/src/components/timeline/assistant-error-card.tsx
import { createEffect, createMemo, createSignal, For, onCleanup, Show } from "solid-js";
import type { MessageInfo, SessionStatus } from "../../services/sse-types";
import {
  compactSessionContext,
  computeProviderHealth,
  decideAutoFailover,
  desktopAuth,
  findAutoFailoverNotice,
  findBestFallbackModel,
  getEffectivePoolCandidates,
  isAutoModel,
  isBalanceError,
  isContextOverflowError,
  isModelFree,
  isModelUnavailableError,
  isRunnerStallError,
  isSessionBusy,
  isUserAbortError,
  LOCAL_CLI_RUNTIME_PROVIDERS,
  MAX_TURN_RETRY_DELAY_MS,
  OFFICIAL_PROVIDER_ID,
  openDesktopPortalPage,
  openDiagnostics,
  openProviderModal,
  performDesktopCheckin,
  refreshDesktopMember,
  refreshDesktopMemberIfStale,
  refreshProviders,
  resolveAutoModelTarget,
  retryLastUserPrompt,
  selectModel,
  workspace,
  type AutoFailoverReason,
} from "../../store/workspace";
import { ProviderHealthBadge } from "../models/ProviderHealthBadge";
import { Icon } from "../icons";
import { locale, t } from "../../i18n";
import {
  resolveErrorCardViewState,
  type ErrorCardActionItem,
  type ErrorCardKind,
} from "./assistant-error-card-state";

export {
  resolveErrorCardViewState,
  type ErrorCardActionID,
  type ErrorCardActionItem,
  type ErrorCardKind,
  type ErrorCardViewInput,
  type ErrorCardViewState,
} from "./assistant-error-card-state";

const FAILOVER_REASON_KEYS = {
  rateLimit: "timeline.autoFailoverReasonRateLimit",
  quota: "timeline.autoFailoverReasonQuota",
  auth: "timeline.autoFailoverReasonAuth",
  model: "timeline.autoFailoverReasonModel",
  server: "timeline.autoFailoverReasonServer",
  network: "timeline.autoFailoverReasonNetwork",
  other: "timeline.autoFailoverReasonOther",
} as const satisfies Record<AutoFailoverReason, string>;

// The error card asks for a fresh points balance at most this often.
const BALANCE_REFRESH_MIN_MS = 15_000;

const PRIMARY_BTN_CLASS =
  "inline-flex items-center gap-1.5 rounded-lg bg-[var(--accent-primary)] px-2.5 py-1 text-[11px] font-medium text-[var(--accent-foreground)] hover:opacity-90 transition-opacity disabled:opacity-50 cursor-pointer";
const NEUTRAL_BTN_CLASS =
  "inline-flex items-center gap-1.5 rounded-lg border border-[var(--border-strong)] bg-[var(--bg-panel)]/80 px-2.5 py-1 text-[11px] font-medium text-[var(--text-primary)] hover:bg-[var(--bg-hover)] transition-colors disabled:opacity-50 cursor-pointer";
const DANGER_SECONDARY_BTN_CLASS =
  "inline-flex items-center gap-1.5 rounded-lg border border-[var(--danger-border)] bg-[var(--bg-panel)]/80 px-2.5 py-1 text-[11px] font-medium text-[var(--text-primary)] hover:bg-[var(--bg-hover)] transition-colors disabled:opacity-50 cursor-pointer";

function formatPausedUntil(at: number): string {
  const date = new Date(at);
  const sameDay = date.toDateString() === new Date().toDateString();
  return date.toLocaleString(locale(), {
    ...(sameDay ? {} : { month: "numeric", day: "numeric" }),
    hour: "2-digit",
    minute: "2-digit",
  });
}

/**
 * Renders provider / model API errors returned on an assistant turn (`info.error`)
 * with relay quality diagnostics and one-click recovery actions.
 */
export function AssistantErrorCard(props: { info: MessageInfo }) {
  const isStall = () => isRunnerStallError(props.info);
  const failover = createMemo(() => findAutoFailoverNotice(props.info));
  const superseded = createMemo(() => {
    const created = props.info.time?.created ?? 0;
    return (workspace.messagesBySession[props.info.sessionID] ?? []).some(
      (m) => m.info.role === "user" && (m.info.time?.created ?? 0) > created
    );
  });
  const isOverflow = () => isContextOverflowError(props.info);
  const isModelUnavailable = () => isModelUnavailableError(props.info);
  const isBalance = () => !isStall() && isBalanceError(props.info);
  const isOfficialBalance = () =>
    isBalance() &&
    (props.info.providerID || workspace.selectedModel?.providerID) === OFFICIAL_PROVIDER_ID;

  const [compacting, setCompacting] = createSignal(false);
  const [compactNotice, setCompactNotice] = createSignal("");
  const [reloading, setReloading] = createSignal(false);
  const [baselinePoints, setBaselinePoints] = createSignal<number | null>(null);
  const [topUpInitiated, setTopUpInitiated] = createSignal(false);
  const [configOpened, setConfigOpened] = createSignal(false);

  let trackedMsgID = "";
  createEffect(() => {
    const msgID = props.info.id;
    if (msgID !== trackedMsgID) {
      trackedMsgID = msgID;
      setBaselinePoints(null);
      setTopUpInitiated(false);
      setConfigOpened(false);
      setCompactNotice("");
    }
    if (!isOfficialBalance()) return;
    refreshDesktopMemberIfStale(BALANCE_REFRESH_MIN_MS);
    if (desktopAuth.member.loaded && baselinePoints() === null) {
      setBaselinePoints(desktopAuth.member.points);
    }
  });

  const pointsIncreased = () => {
    const base = baselinePoints();
    return base !== null && desktopAuth.member.loaded && desktopAuth.member.points > base;
  };

  const canCheckin = () =>
    isOfficialBalance() &&
    desktopAuth.isLoggedIn &&
    desktopAuth.member.loaded &&
    !desktopAuth.member.checkedInToday;

  const statusCode = () => props.info.error?.data?.statusCode;
  const providerID = () => props.info.providerID || workspace.selectedModel?.providerID || "";
  const modelID = () => props.info.modelID || workspace.selectedModel?.modelID || "";
  const providerHealth = createMemo(() =>
    providerID() ? computeProviderHealth(providerID(), modelID() || undefined) : null
  );
  const isProviderDegraded = createMemo(() => {
    const h = providerHealth();
    if (!h || h.totalTurns < 2) return false;
    return (
      (h.stabilityScore ?? 100) < 82 &&
      (h.status === "degraded" || h.status === "warning" || h.riskTags.length > 0)
    );
  });
  const isAuthError = createMemo(() =>
    props.info.error ? decideAutoFailover(props.info.error, 0).reason === "auth" : false
  );
  const isCliRuntimeTurn = createMemo(() => LOCAL_CLI_RUNTIME_PROVIDERS.has(providerID()));

  const recommendedFallback = createMemo(() =>
    providerID() && !isStall()
      ? findBestFallbackModel(providerID(), { httpOnly: !isCliRuntimeTurn() })
      : null
  );

  const freeFallbackModel = createMemo(() => {
    const curPID = workspace.selectedModel?.providerID;
    const curMID = workspace.selectedModel?.modelID;
    const errPID = props.info.providerID;
    const errMID = props.info.modelID;
    const httpOnly = !isCliRuntimeTurn();
    const isSameAsActiveOrFailed = (pid: string, mid: string) =>
      (curPID === pid && curMID === mid) || (errPID === pid && errMID === mid);

    for (const c of getEffectivePoolCandidates("free")) {
      if (!c?.providerID || !c?.modelID || isSameAsActiveOrFailed(c.providerID, c.modelID)) {
        continue;
      }
      if (httpOnly && LOCAL_CLI_RUNTIME_PROVIDERS.has(c.providerID)) {
        continue;
      }
      const prov = workspace.providers.find((p) => p.id === c.providerID);
      if (httpOnly && prov?.source === "runner") continue;
      if (prov && isModelFree(c.providerID, c.modelID, prov.models?.[c.modelID])) {
        return { providerID: c.providerID, modelID: c.modelID };
      }
    }
    const connSet = new Set(workspace.connectedProviders);
    const cfgProviders = workspace.globalConfig?.provider ?? {};
    for (const p of workspace.providers) {
      if (httpOnly && (p.source === "runner" || LOCAL_CLI_RUNTIME_PROVIDERS.has(p.id))) continue;
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

  const usableFreeFallback = createMemo(() => {
    if (recommendedFallback()) return null;
    const free = freeFallbackModel();
    if (!free) return null;
    if (
      workspace.selectedModel?.providerID === free.providerID &&
      workspace.selectedModel?.modelID === free.modelID
    ) {
      return null;
    }
    return free;
  });

  const fallbackTarget = createMemo(() => recommendedFallback() ?? usableFreeFallback());
  const fallbackLabel = () => {
    const rec = recommendedFallback();
    if (rec) {
      return t("timeline.switchRecommendedModel", {
        provider: rec.providerID,
        model: rec.modelID,
      });
    }
    const free = usableFreeFallback();
    return free ? t("timeline.switchAndRetryFree", { model: free.modelID }) : "";
  };

  const errorKind = createMemo<ErrorCardKind>(() => {
    if (isStall()) return "stall";
    if (isOverflow()) return "overflow";
    if (isModelUnavailable()) return "model";
    if (isOfficialBalance()) return "officialBalance";
    if (isBalance()) return "providerBalance";
    return "api";
  });

  const view = createMemo(() =>
    resolveErrorCardViewState({
      kind: errorKind(),
      superseded: superseded(),
      hasSelectedModel: Boolean(workspace.selectedModel),
      hasFallbackModel: Boolean(fallbackTarget()),
      isAuthError: isAuthError(),
      isProviderDegraded: isProviderDegraded(),
      configOpened: configOpened(),
      rawErrorName: props.info.error?.name,
      rawErrorMessage: props.info.error?.data?.message,
      memberLoaded: desktopAuth.member.loaded,
      points: desktopAuth.member.points,
      pointsIncreased: pointsIncreased(),
      canCheckin: canCheckin(),
      checkinError: Boolean(desktopAuth.member.error),
      topUpInitiated: topUpInitiated(),
    })
  );

  const onCompactAndRetry = async () => {
    const sessionID = props.info.sessionID;
    const selected = workspace.selectedModel;
    if (!sessionID || !selected || compacting() || isSessionBusy(sessionID)) return;
    const model = isAutoModel(selected)
      ? resolveAutoModelTarget(selected, { advance: false })
      : selected;
    if (!model) {
      setCompactNotice(t("timeline.contextCompactUnavailable"));
      return;
    }
    setCompactNotice("");
    setCompacting(true);
    try {
      const compacted = await compactSessionContext(sessionID, model);
      if (!compacted) {
        setCompactNotice(t("timeline.contextCompactUnavailable"));
        return;
      }
      await retryLastUserPrompt(sessionID);
    } catch (err) {
      console.error("compact before retry failed:", err);
      setCompactNotice(t("timeline.contextCompactFailed"));
    } finally {
      setCompacting(false);
    }
  };

  const onReloadCatalog = async (andRetry: boolean) => {
    if (reloading()) return;
    setReloading(true);
    try {
      await refreshProviders(true);
      if (andRetry && !superseded() && !isSessionBusy(props.info.sessionID)) {
        await retryLastUserPrompt(props.info.sessionID);
      }
    } finally {
      setReloading(false);
    }
  };

  const onCheckinClick = async () => {
    const res = await performDesktopCheckin();
    if (res) setTopUpInitiated(true);
  };

  const onTopUpClick = () => {
    setTopUpInitiated(true);
    void openDesktopPortalPage("rewards");
  };

  const onBalanceRetryClick = () => {
    if (isOfficialBalance()) void refreshDesktopMember();
    void retryLastUserPrompt(props.info.sessionID);
  };

  const onConfigureClick = () => {
    setConfigOpened(true);
    openProviderModal("providers");
  };

  const btnClass = (primary: boolean) =>
    primary
      ? PRIMARY_BTN_CLASS
      : view().tone === "notice"
        ? NEUTRAL_BTN_CLASS
        : DANGER_SECONDARY_BTN_CLASS;

  const renderAction = (action: ErrorCardActionItem) => {
    const cls = () => btnClass(action.primary);
    switch (action.id) {
      case "compactRetry":
        return (
          <button
            type="button"
            disabled={compacting() || isSessionBusy(props.info.sessionID)}
            aria-busy={compacting()}
            onClick={() => void onCompactAndRetry()}
            class={cls()}
          >
            <Icon name={compacting() ? "circle-check" : "compress"} size="small" />
            <span>{t("timeline.contextOverflowCompactRetry")}</span>
          </button>
        );
      case "reloadAndRetry":
      case "reloadCatalog": {
        const andRetry = action.id === "reloadAndRetry";
        return (
          <button
            type="button"
            disabled={reloading()}
            aria-busy={reloading()}
            onClick={() => void onReloadCatalog(andRetry)}
            class={cls()}
          >
            <Icon name={reloading() ? "circle-check" : "reset"} size="small" />
            <span>
              {t(andRetry ? "timeline.reloadCatalogAndRetry" : "timeline.reloadModelCatalog")}
            </span>
          </button>
        );
      }
      case "checkin":
        return (
          <button
            type="button"
            disabled={desktopAuth.member.checkingIn}
            aria-busy={desktopAuth.member.checkingIn}
            onClick={() => void onCheckinClick()}
            class={cls()}
          >
            <Icon name="star-filled" size="small" />
            <span>{t("timeline.balanceCheckin")}</span>
          </button>
        );
      case "topup":
        return (
          <button type="button" onClick={onTopUpClick} class={cls()}>
            <Icon name="square-arrow-top-right" size="small" />
            <span>{t("timeline.balanceTopUp")}</span>
          </button>
        );
      case "balanceRetry":
        return (
          <button type="button" onClick={onBalanceRetryClick} class={cls()}>
            <Icon name="arrow-up" size="small" />
            <span>{t("timeline.balanceRetry")}</span>
          </button>
        );
      case "retryStall":
        return (
          <button
            type="button"
            onClick={() => void retryLastUserPrompt(props.info.sessionID)}
            class={cls()}
          >
            <Icon name="arrow-up" size="small" />
            <span>{t("timeline.retryTask")}</span>
          </button>
        );
      case "retryModel":
        return (
          <Show when={workspace.selectedModel}>
            {(sel) => (
              <button
                type="button"
                onClick={() => void retryLastUserPrompt(props.info.sessionID)}
                class={cls()}
              >
                <Icon name="arrow-up" size="small" />
                <span>{t("timeline.retryWithModel", { model: sel().modelID })}</span>
              </button>
            )}
          </Show>
        );
      case "fallback":
        return (
          <Show when={fallbackTarget()}>
            {(fb) => (
              <button
                type="button"
                onClick={() => {
                  selectModel(fb(), false);
                  void retryLastUserPrompt(props.info.sessionID);
                }}
                class={cls()}
              >
                <span>{fallbackLabel()}</span>
              </button>
            )}
          </Show>
        );
      case "configure":
        return (
          <button type="button" onClick={onConfigureClick} class={cls()}>
            <Icon name="sliders" size="small" />
            <span>{t("timeline.configureModels")}</span>
          </button>
        );
      case "diagnostics":
        return (
          <button
            type="button"
            onClick={() => openDiagnostics({ sessionID: props.info.sessionID })}
            class={cls()}
          >
            <span>{t("diagnostics.view")}</span>
          </button>
        );
    }
  };

  return (
    <Show when={props.info.error && !isUserAbortError(props.info)}>
      <Show when={failover()}>
        {(n) => (
          <div
            role="status"
            class="my-2 rounded-xl border border-[var(--warn-border)] bg-[var(--warn-surface)] px-3.5 py-2.5 text-[12px] text-[var(--warn-text)]"
          >
            <div class="flex items-start gap-2.5">
              <Icon name="reset" size="small" class="mt-0.5 shrink-0" />
              <div class="min-w-0 flex-1 space-y-1">
                <div class="font-semibold">{t("timeline.autoFailoverTitle")}</div>
                <div class="break-words">
                  {t("timeline.autoFailoverDesc", {
                    from: `${n().from.providerID}/${n().from.modelID}`,
                    to: `${n().to.providerID}/${n().to.modelID}`,
                    reason: t(FAILOVER_REASON_KEYS[n().reason] ?? FAILOVER_REASON_KEYS.other),
                  })}
                </div>
                <Show when={(n().cooldownUntil ?? 0) > Date.now()}>
                  <div class="opacity-80">
                    {t("timeline.autoFailoverPausedUntil", {
                      model: n().from.modelID,
                      time: formatPausedUntil(n().cooldownUntil!),
                    })}
                  </div>
                </Show>
                <details class="text-[11px] opacity-80">
                  <summary class="cursor-pointer select-none">{t("timeline.autoFailoverShowError")}</summary>
                  <div class="mt-1 font-mono break-words">
                    {view().title}
                    {statusCode() ? ` (${statusCode()})` : ""}: {view().message}
                  </div>
                </details>
              </div>
            </div>
          </div>
        )}
      </Show>
      <Show when={!failover()}>
        <div
          class={
            view().tone === "notice"
              ? "my-2 rounded-xl border border-[var(--warn-border)] bg-[var(--bg-elevated)]/92 px-3.5 py-2.5 text-[13px] text-[var(--text-primary)]"
              : "my-2 rounded-xl border border-[var(--danger-border)] bg-[var(--danger-surface)] px-3.5 py-3 text-[13px] text-[var(--danger-text)]"
          }
        >
          <div class="flex items-start gap-2.5">
            <Icon
              name={view().tone === "notice" ? "warning" : "circle-ban-sign"}
              size="small"
              class={
                view().tone === "notice"
                  ? "mt-0.5 shrink-0 text-[var(--warn-text)]"
                  : "mt-0.5 shrink-0"
              }
            />
            <div class="min-w-0 flex-1 space-y-1.5">
              <div class="flex items-center gap-2 flex-wrap">
                <span class="font-semibold">
                  {view().title}
                  {view().showStatusCode && statusCode() ? ` (${statusCode()})` : ""}
                </span>
                <Show when={props.info.providerID || props.info.modelID}>
                  <span
                    class={
                      view().tone === "notice"
                        ? "rounded bg-[var(--bg-inset)] px-1.5 py-0.5 font-mono text-[11px] text-[var(--text-secondary)]"
                        : "rounded bg-black/10 dark:bg-white/10 px-1.5 py-0.5 font-mono text-[11px]"
                    }
                  >
                    {[props.info.providerID, props.info.modelID].filter(Boolean).join(" / ")}
                  </span>
                </Show>
                <Show when={view().showHealthBadge && providerID()}>
                  <ProviderHealthBadge providerID={providerID()} modelID={modelID() || undefined} />
                </Show>
              </div>

              <div
                class={
                  view().monoMessage
                    ? "font-mono text-[12px] leading-relaxed break-words opacity-95"
                    : view().tone === "notice"
                      ? "text-[12px] leading-relaxed text-[var(--text-secondary)] break-words"
                      : "text-[12px] leading-relaxed break-words opacity-95"
                }
              >
                {view().message}
              </div>

              <Show when={view().actions.length > 0 || compactNotice()}>
                <div class="pt-0.5 flex items-center gap-2 flex-wrap">
                  <For each={view().actions}>{(action) => renderAction(action)}</For>
                  <Show when={compactNotice()}>
                    <span role="status" class="text-[11px] text-[var(--danger-text)]">
                      {compactNotice()}
                    </span>
                  </Show>
                </div>
              </Show>
            </div>
          </div>
        </div>
      </Show>
    </Show>
  );
}

/**
 * Ported from @opencode-ai/session-ui SessionRetry
 * Live 1-second countdown card when a session status is `retry` (e.g. rate limit / transient network backoff).
 */
export function SessionRetryCard(props: { status: Extract<SessionStatus, { type: "retry" }> }) {
  const maxRetrySeconds = Math.ceil(MAX_TURN_RETRY_DELAY_MS / 1000);
  const clampSeconds = () =>
    Math.min(
      maxRetrySeconds,
      Math.max(0, Math.round((props.status.next - Date.now()) / 1000))
    );
  const [seconds, setSeconds] = createSignal(clampSeconds());

  createEffect(() => {
    const update = () => {
      setSeconds(clampSeconds());
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
