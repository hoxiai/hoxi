export const PROMPT_HISTORY_KEY = "hoxi-prompt-history:v1";
export const PROMPT_HISTORY_CLEARED_EVENT = "hoxi:prompt-history-cleared";

export function clearLocalPromptHistory(): boolean {
  try {
    localStorage.removeItem(PROMPT_HISTORY_KEY);
    window.dispatchEvent(new Event(PROMPT_HISTORY_CLEARED_EVENT));
    return true;
  } catch {
    return false;
  }
}
