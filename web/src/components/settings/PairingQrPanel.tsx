// web/src/components/settings/PairingQrPanel.tsx
// Shows the LAN pairing QR the Hoxi mobile app scans to reach this Mac
// directly on the local network. The code is fetched on demand so its token is
// only rendered when the user asks for it.
import { createSignal, Show } from "solid-js";
import { QrCode, RefreshCw } from "lucide-solid";
import { t } from "../../i18n";
import { loadPairingCode, pairing } from "../../store/workspace";
import { SettingGroup } from "./setting-controls";

const buttonClass =
  "hoxi-press inline-flex items-center gap-1.5 rounded-xl border border-[var(--border-strong)] px-4 py-2 text-[13px] font-medium hover:bg-[var(--bg-hover)] disabled:opacity-50 cursor-pointer";

export function PairingQrPanel() {
  const [revealed, setRevealed] = createSignal(false);

  const onShow = () => {
    setRevealed(true);
    void loadPairingCode();
  };

  return (
    <SettingGroup title={t("pairing.title")} tip={t("pairing.tip")}>
      <div class="space-y-4 px-5 py-4">
        <Show
          when={revealed()}
          fallback={
            <button type="button" class={buttonClass} onClick={onShow}>
              <QrCode size={14} />
              <span>{t("pairing.showAction")}</span>
            </button>
          }
        >
          <Show when={pairing.busy}>
            <p class="text-[13px] text-[var(--text-muted)]">{t("pairing.loading")}</p>
          </Show>
          <Show when={pairing.error}>
            <p role="alert" class="text-[12px] text-rose-500">
              {pairing.error}
            </p>
          </Show>
          <Show when={pairing.code}>
            {(code) => (
              <Show
                when={code().available}
                fallback={<p class="text-[13px] text-amber-600">{t("pairing.noLanHint")}</p>}
              >
                <div class="flex flex-col items-center gap-4 sm:flex-row sm:items-start">
                  <img
                    src={code().qrPng}
                    alt={t("pairing.qrAlt")}
                    class="h-56 w-56 shrink-0 rounded-xl border border-[var(--border-subtle)] bg-white p-2"
                  />
                  <div class="space-y-2 text-[13px]">
                    <p class="font-medium text-[var(--text-primary)]">{t("pairing.scanHint")}</p>
                    <p class="break-all font-mono text-[12px] text-[var(--text-muted)]">{code().url}</p>
                    <button
                      type="button"
                      class={buttonClass}
                      disabled={pairing.busy}
                      onClick={() => void loadPairingCode()}
                    >
                      <RefreshCw size={14} />
                      <span>{t("pairing.refreshAction")}</span>
                    </button>
                  </div>
                </div>
              </Show>
            )}
          </Show>
        </Show>
      </div>
    </SettingGroup>
  );
}
