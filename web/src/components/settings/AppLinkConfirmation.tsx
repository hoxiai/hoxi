import { Show } from "solid-js";
import { t } from "../../i18n";
import { appLinkConfirmation, resolveAppLinkConfirmation } from "../../store/workspace";

export function AppLinkConfirmation() {
  return <Show when={appLinkConfirmation()} keyed>{(confirmation) => (
    <div class="fixed inset-0 z-[100] grid place-items-center bg-black/45 p-4">
      <section role="alertdialog" aria-modal="true" aria-labelledby="app-link-confirmation"
        class="max-w-lg rounded-xl border border-[var(--border-default)] bg-[var(--bg-canvas)] p-4 shadow-lg"
        onKeyDown={(event) => { if (event.key === "Escape") resolveAppLinkConfirmation(false); }}>
        <p id="app-link-confirmation" class="max-h-60 overflow-auto break-all">{confirmation.message}</p>
        <div class="mt-4 flex justify-end gap-3">
          <button type="button" ref={(el) => queueMicrotask(() => el.focus())} onClick={() => resolveAppLinkConfirmation(false)}>{t("settings.appGrantDeny")}</button>
          <button type="button" onClick={() => resolveAppLinkConfirmation(true)}>{t("settings.appGrantAllowOnce")}</button>
        </div>
      </section>
    </div>
  )}</Show>;
}
