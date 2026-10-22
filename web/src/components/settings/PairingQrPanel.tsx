// web/src/components/settings/PairingQrPanel.tsx
// Shows the LAN pairing QR the Hoxi mobile app scans to reach this Mac on the
// local network. The QR is shown directly: opening this panel is the deliberate
// action. The address and token stay out of the visible text and appear in a
// tooltip instead, so the panel stays clean.
import { onMount, Show } from "solid-js";
import { RefreshCw } from "lucide-solid";
import { t } from "../../i18n";
import { loadPairingCode, pairing, rotatePairingToken } from "../../store/workspace";
import { HelpTip, SettingGroup } from "./setting-controls";
import { confirmAction } from "./confirm-dialog";

export function PairingQrPanel() {
  onMount(() => {
    void loadPairingCode();
  });

  const onRotate = async () => {
    if (!(await confirmAction(t("pairing.rotateConfirm")))) return;
    void rotatePairingToken();
  };

  return (
    <SettingGroup title={t("pairing.title")} tip={t("pairing.tip")}>
      <div class="flex flex-col items-center gap-3 px-5 py-6">
        <Show when={pairing.error}>
          <p role="alert" class="text-[12px] text-rose-500">
            {pairing.error}
          </p>
        </Show>

        <Show when={pairing.code}>
          {(code) => (
            <Show
              when={code().available}
              fallback={
                <p class="text-center text-[13px] text-amber-600">
                  {t("pairing.noLanHint")}
                </p>
              }
            >
              <img
                src={code().qrPng}
                alt={t("pairing.qrAlt")}
                class="h-56 w-56 rounded-xl border border-[var(--border-subtle)] bg-white p-2"
              />
              <div class="flex items-center gap-2 text-[13px]">
                <span class="text-[var(--text-secondary)]">{t("pairing.scanHint")}</span>
                <HelpTip text={t("pairing.addressTip", { url: code().url })} />
                <button
                  type="button"
                  class="hoxi-press inline-flex cursor-pointer items-center rounded-lg p-1 text-[var(--text-muted)] hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)] disabled:opacity-50"
                  disabled={pairing.busy}
                  aria-label={t("pairing.refreshAction")}
                  onClick={() => void loadPairingCode()}
                >
                  <RefreshCw size={14} />
                </button>
              </div>
              <button
                type="button"
                class="cursor-pointer text-[12px] text-[var(--text-muted)] underline-offset-2 hover:text-[var(--text-primary)] hover:underline disabled:opacity-50"
                disabled={pairing.busy}
                onClick={onRotate}
              >
                {t("pairing.rotateAction")}
              </button>
            </Show>
          )}
        </Show>

        <Show when={!pairing.code && !pairing.error}>
          <p class="text-[13px] text-[var(--text-muted)]">{t("pairing.loading")}</p>
        </Show>
      </div>
    </SettingGroup>
  );
}
