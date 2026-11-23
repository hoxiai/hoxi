// web/src/components/timeline/PendingRequestRecoveryBanner.tsx
import { createEffect, createMemo, createSignal, on, onCleanup, Show } from "solid-js";
import { AlertTriangle, Check, LoaderCircle, RefreshCw, Unlock } from "lucide-solid";
import {
  dismissPendingPrompt,
  errorMessageOf,
  getPendingPrompt,
  isPendingPromptStorageCorrupt,
  loadSessionMessages,
  recheckPendingPrompt,
  resetCorruptPendingPrompts,
  workspace,
} from "../../store/workspace";
import { t } from "../../i18n";

function shortRequestID(id: string): string {
  const trimmed = id.trim();
  if (trimmed.length <= 22) return trimmed;
  return `${trimmed.slice(0, 14)}…${trimmed.slice(-6)}`;
}

/**
 * Surfaces lingering unconfirmed/outcome_unknown prompt receipts right above
 * the composer so a session is never permanently deadlocked: users can re-check
 * host admission status or explicitly unlock sending after verifying history
 * and workspace state (without ever auto-replaying the interrupted command).
 */
export default function PendingRequestRecoveryBanner() {
  const [checking, setChecking] = createSignal(false);
  const [confirmingUnlock, setConfirmingUnlock] = createSignal(false);
  const [statusNote, setStatusNote] = createSignal<string | null>(null);
  let checkGen = 0;
  onCleanup(() => {
    checkGen++;
  });

  const sessionID = () => workspace.activeSessionID;
  const pending = createMemo(() => getPendingPrompt(sessionID()));
  const storageCorrupt = createMemo(() => isPendingPromptStorageCorrupt());
  const pendingTargetKey = createMemo(
    () => `${sessionID() ?? ""}|${pending()?.clientRequestId ?? ""}|${storageCorrupt() ? "1" : "0"}`
  );

  createEffect(on(pendingTargetKey, () => {
    // Reset inline state and invalidate any in-flight recheck when the target record changes.
    checkGen++;
    setChecking(false);
    setConfirmingUnlock(false);
    setStatusNote(null);
  }));

  const onRecheck = async () => {
    const sid = sessionID();
    if (!sid || checking()) return;
    const gen = ++checkGen;
    setChecking(true);
    setStatusNote(null);
    setConfirmingUnlock(false);
    try {
      const result = await recheckPendingPrompt(sid);
      if (result === "completed") {
        await loadSessionMessages(sid);
      } else if (result === "still_unknown" && gen === checkGen && sessionID() === sid) {
        setStatusNote(t("timeline.pendingRequestStillUnknown"));
      }
    } catch (err) {
      if (gen === checkGen && sessionID() === sid) {
        setStatusNote(errorMessageOf(err, t("common.error")));
      }
    } finally {
      if (gen === checkGen) {
        setChecking(false);
      }
    }
  };

  const onConfirmUnlock = () => {
    if (storageCorrupt()) {
      resetCorruptPendingPrompts();
      setConfirmingUnlock(false);
      setStatusNote(null);
      return;
    }
    const sid = sessionID();
    const rec = pending();
    if (!sid || !rec) return;
    dismissPendingPrompt(sid, rec.clientRequestId);
    setConfirmingUnlock(false);
    setStatusNote(null);
  };

  return (
    <Show when={storageCorrupt() || pending()}>
      <div
        role="alert"
        class="mb-2.5 rounded-xl border border-[var(--warn-border)] bg-[var(--warn-surface)]/95 px-3.5 py-2.5 shadow-sm space-y-2"
      >
        <div class="flex items-start justify-between gap-2.5 flex-wrap">
          <div class="flex items-start gap-2 min-w-0 flex-1">
            <AlertTriangle size={14} class="text-[var(--warn-text)] shrink-0 mt-0.5" />
            <div class="min-w-0 space-y-0.5">
              <div class="flex items-center gap-2 flex-wrap">
                <span class="text-[12px] font-medium text-[var(--warn-text)]">
                  {storageCorrupt()
                    ? t("timeline.pendingRequestCorruptTitle")
                    : t("timeline.pendingRequestBannerTitle")}
                </span>
                <Show when={!storageCorrupt() && pending()}>
                  {(rec) => (
                    <code
                      title={rec().clientRequestId}
                      class="rounded border border-[var(--warn-border)]/70 bg-[var(--bg-panel)]/75 px-1.5 py-0.5 font-mono text-[10.5px] text-[var(--text-secondary)]"
                    >
                      {shortRequestID(rec().clientRequestId)}
                    </code>
                  )}
                </Show>
              </div>
              <p class="text-[11.5px] leading-relaxed text-[var(--text-secondary)]">
                {storageCorrupt()
                  ? t("timeline.pendingRequestsUnreadable")
                  : t("timeline.pendingRequestBannerHint")}
              </p>
              <Show when={!storageCorrupt() && (statusNote() || pending()?.lastError)}>
                {(detail) => (
                  <p class="text-[11px] leading-relaxed text-[var(--text-muted)] break-words">
                    {detail()}
                  </p>
                )}
              </Show>
            </div>
          </div>

          <div class="flex items-center gap-1.5 shrink-0">
            <Show when={!storageCorrupt() && !confirmingUnlock()}>
              <button
                type="button"
                disabled={checking()}
                onClick={() => void onRecheck()}
                class="hoxi-press inline-flex items-center gap-1 rounded-lg bg-[var(--warn-text)] px-2.5 py-1 text-[11px] font-medium text-white hover:opacity-90 disabled:opacity-50 cursor-pointer"
              >
                <Show when={!checking()} fallback={<LoaderCircle size={11} class="animate-spin" />}>
                  <RefreshCw size={11} />
                </Show>
                <span>
                  {checking()
                    ? t("timeline.pendingRequestRecheckingBtn")
                    : t("timeline.pendingRequestRecheckBtn")}
                </span>
              </button>
            </Show>

            <Show
              when={confirmingUnlock()}
              fallback={
                <button
                  type="button"
                  disabled={checking()}
                  onClick={() => setConfirmingUnlock(true)}
                  class="hoxi-press inline-flex items-center gap-1 rounded-lg border border-[var(--border-subtle)] bg-[var(--bg-panel)]/85 px-2.5 py-1 text-[11px] font-medium text-[var(--text-primary)] hover:bg-[var(--bg-hover)] disabled:opacity-50 cursor-pointer"
                >
                  <Unlock size={11} />
                  <span>
                    {storageCorrupt()
                      ? t("timeline.pendingRequestResetStorageBtn")
                      : t("timeline.pendingRequestUnlockBtn")}
                  </span>
                </button>
              }
            >
              <button
                type="button"
                onClick={onConfirmUnlock}
                class="hoxi-press inline-flex items-center gap-1 rounded-lg bg-[var(--warn-text)] px-2.5 py-1 text-[11px] font-medium text-white hover:opacity-90 cursor-pointer"
              >
                <Check size={11} />
                <span>{t("timeline.pendingRequestConfirmUnlockBtn")}</span>
              </button>
              <button
                type="button"
                onClick={() => setConfirmingUnlock(false)}
                class="inline-flex items-center rounded-lg px-2 py-1 text-[11px] text-[var(--text-muted)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-hover)] cursor-pointer"
              >
                <span>{t("common.cancel")}</span>
              </button>
            </Show>
          </div>
        </div>

        <Show when={confirmingUnlock()}>
          <p class="text-[11px] leading-relaxed text-[var(--warn-text)]">
            {storageCorrupt()
              ? t("timeline.pendingRequestResetConfirmHint")
              : t("timeline.pendingRequestUnlockConfirmHint")}
          </p>
        </Show>
      </div>
    </Show>
  );
}
