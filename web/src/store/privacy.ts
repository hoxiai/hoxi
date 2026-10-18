import { createStore } from "solid-js/store";
import { t } from "../i18n";
import { privacyAPI, PRIVACY_VERSION } from "../services/privacy";

export const PROMPT_HISTORY_KEY = "hoxi-prompt-history:v1";
export const PROMPT_HISTORY_CLEARED_EVENT = "hoxi:prompt-history-cleared";

export const [privacy, setPrivacy] = createStore({
  accepted: false,
  loaded: false,
  dialogOpen: false,
  saving: false,
  error: "",
});

let loading: Promise<void> | undefined;

export function loadPrivacy(): Promise<void> {
  if (loading) return loading;
  loading = (async () => {
    try {
      const result = await privacyAPI.get();
      const accepted = result.version === PRIVACY_VERSION && result.accepted === true;
      setPrivacy({ accepted, loaded: true, dialogOpen: !accepted, error: "" });
    } catch {
      setPrivacy({ accepted: false, loaded: false, dialogOpen: true, error: t("privacy.loadError") });
    } finally {
      loading = undefined;
    }
  })();
  return loading;
}

export function openPrivacyConsent() {
  setPrivacy("dialogOpen", true);
}

export function dismissPrivacyConsent() {
  if (!privacy.saving) setPrivacy("dialogOpen", false);
}

export function ensureAIConsent(): boolean {
  if (privacy.loaded && privacy.accepted && !privacy.saving) return true;
  openPrivacyConsent();
  return false;
}

export async function savePrivacyConsent(accepted: boolean): Promise<void> {
  if (privacy.saving) return;
  setPrivacy({ saving: true, error: "" });
  // Finish any startup read before saving so it cannot overwrite the user's choice.
  if (loading) await loading;
  if (!accepted) setPrivacy("accepted", false);
  try {
    const result = await privacyAPI.put(accepted);
    if (result.version !== PRIVACY_VERSION || result.accepted !== accepted) throw new Error();
    setPrivacy({ accepted, loaded: true, dialogOpen: false });
  } catch {
    setPrivacy("error", t("privacy.saveError"));
  } finally {
    setPrivacy("saving", false);
  }
}

export function clearLocalPromptHistory(): boolean {
  try {
    localStorage.removeItem(PROMPT_HISTORY_KEY);
    window.dispatchEvent(new Event(PROMPT_HISTORY_CLEARED_EVENT));
    return true;
  } catch {
    return false;
  }
}
