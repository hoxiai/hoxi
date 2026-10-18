import { createEffect, createSignal, Show } from "solid-js";
import { t } from "../../i18n";
import {
  clearLocalPromptHistory,
  dismissPrivacyConsent,
  openPrivacyConsent,
  privacy,
  savePrivacyConsent,
  workspace,
} from "../../store/workspace";

const buttonClass = "rounded-lg border border-[var(--border-strong)] px-4 py-2 text-sm font-medium hover:bg-[var(--bg-hover)] disabled:opacity-50 cursor-pointer";

function PrivacyDisclosure() {
  const provider = () => workspace.providers.find((item) => item.id === workspace.selectedModel?.providerID);
  const providerName = () => provider()?.name || workspace.selectedModel?.providerID || t("privacy.noProvider");
  const endpoint = () => {
    const id = workspace.selectedModel?.providerID;
    const raw = id && (workspace.hoxiProviders[id]?.options?.baseURL || workspace.globalConfig.provider?.[id]?.options?.baseURL || provider()?.options?.baseURL);
    if (typeof raw !== "string") return t("privacy.defaultEndpoint");
    try {
      // Never display URL credentials, query parameters or fragments.
      const url = new URL(raw);
      return url.origin;
    } catch {
      return t("privacy.customEndpoint");
    }
  };
  return (
    <div class="space-y-3 text-sm leading-relaxed text-[var(--text-secondary)]">
      <p>{t("privacy.company")}</p>
      <p>{t("privacy.disclosure")}</p>
      <div class="rounded-xl border border-[var(--border-subtle)] bg-[var(--bg-inset)] p-3 break-all">
        <p>{t("privacy.selectedProvider", { provider: providerName() })}</p>
        <p>{t("privacy.endpoint", { endpoint: endpoint() })}</p>
      </div>
      <p>{t("privacy.destinations")}</p>
      <p>{t("privacy.ancillary")}</p>
      <p>{t("privacy.independentConnections")}</p>
      <p>{t("privacy.choice")}</p>
    </div>
  );
}

function LegalLinks() {
  const linkClass = "text-[var(--accent-primary)] underline underline-offset-2";
  return (
    <div class="flex flex-wrap gap-x-4 gap-y-2 text-sm">
      <a class={linkClass} href="/legal/privacy-en.html" target="_blank" rel="noopener noreferrer">{t("privacy.privacyEnglish")}</a>
      <a class={linkClass} href="/legal/privacy-zh.html" target="_blank" rel="noopener noreferrer">{t("privacy.privacyChinese")}</a>
      <a class={linkClass} href="/legal/terms-en.html" target="_blank" rel="noopener noreferrer">{t("privacy.termsEnglish")}</a>
      <a class={linkClass} href="/legal/terms-zh.html" target="_blank" rel="noopener noreferrer">{t("privacy.termsChinese")}</a>
      <a class={linkClass} href="mailto:support@hoxi.ai">{t("privacy.contact")}</a>
    </div>
  );
}

export function PrivacySettingsTab() {
  const [historyResult, setHistoryResult] = createSignal("");
  return (
    <div class="space-y-6 text-[var(--text-primary)]">
      <h3 class="text-base font-semibold">{t("privacy.title")}</h3>
      <PrivacyDisclosure />
      <p class="text-sm font-medium" role="status">{privacy.accepted ? t("privacy.accepted") : t("privacy.notAccepted")}</p>
      <div class="flex flex-wrap gap-2">
        <button class={buttonClass} onClick={openPrivacyConsent}>{t("privacy.review")}</button>
        <Show when={privacy.accepted}>
          <button class={buttonClass} disabled={privacy.saving} onClick={() => void savePrivacyConsent(false)}>{t("privacy.withdraw")}</button>
        </Show>
      </div>
      <Show when={privacy.error}><p role="alert" class="text-sm text-red-500">{privacy.error}</p></Show>
      <LegalLinks />
      <section class="space-y-3 border-t border-[var(--border-subtle)] pt-5">
        <h4 class="font-semibold">{t("privacy.localData")}</h4>
        <p class="text-sm text-[var(--text-secondary)]">{t("privacy.historyScope")}</p>
        <button class={buttonClass} onClick={() => setHistoryResult(clearLocalPromptHistory() ? t("privacy.historyCleared") : t("privacy.historyError"))}>{t("privacy.clearHistory")}</button>
        <p class="text-sm" role="status">{historyResult()}</p>
      </section>
      <section class="space-y-3 border-t border-[var(--border-subtle)] pt-5">
        <p class="text-sm text-[var(--text-secondary)]">{t("privacy.deletionExplanation")}</p>
        <a class={`${buttonClass} inline-block`} href="mailto:support@hoxi.ai?subject=Account%20deletion%20request">{t("privacy.requestDeletion")}</a>
      </section>
    </div>
  );
}

export function PrivacyConsentDialog() {
  let dialog!: HTMLDialogElement;
  createEffect(() => {
    if (privacy.dialogOpen && !dialog.open) dialog.showModal();
    else if (!privacy.dialogOpen && dialog.open) dialog.close();
  });
  return (
    <dialog ref={dialog} aria-labelledby="privacy-consent-title" aria-describedby="privacy-consent-intro"
      class="m-auto w-[calc(100%-2rem)] max-w-xl max-h-[90dvh] overflow-y-auto rounded-2xl border border-[var(--border-strong)] bg-[var(--bg-panel)] p-6 text-[var(--text-primary)] shadow-2xl backdrop:bg-black/60"
      onCancel={(event) => { event.preventDefault(); dismissPrivacyConsent(); }}>
      <div class="space-y-5">
        <h2 id="privacy-consent-title" class="text-xl font-semibold">{t("privacy.consentTitle")}</h2>
        <p id="privacy-consent-intro" class="text-sm font-medium">{t("privacy.consentIntro")}</p>
        <PrivacyDisclosure />
        <LegalLinks />
        <Show when={privacy.error}><p role="alert" class="text-sm text-red-500">{privacy.error}</p></Show>
        <div class="flex flex-wrap justify-end gap-2">
          <button autofocus class={buttonClass} disabled={privacy.saving} onClick={dismissPrivacyConsent}>{t("privacy.notNow")}</button>
          <button class={buttonClass} disabled={privacy.saving} onClick={() => void savePrivacyConsent(false)}>{privacy.accepted ? t("privacy.withdraw") : t("privacy.decline")}</button>
          <button class={`${buttonClass} bg-[var(--accent-primary)] text-[var(--accent-foreground)]`} disabled={privacy.saving} onClick={() => void savePrivacyConsent(true)}>{privacy.saving ? t("privacy.saving") : t("privacy.accept")}</button>
        </div>
      </div>
    </dialog>
  );
}
