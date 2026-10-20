import { type Accessor, createSignal, onCleanup } from "solid-js";

export const PROMPT_HISTORY_KEY = "hoxi-prompt-history:v1";
export const PROMPT_HISTORY_CLEARED_EVENT = "hoxi:prompt-history-cleared";

const MAX_HISTORY = 50;

function loadStoredHistory(): string[] {
  try {
    if (typeof localStorage === "undefined") return [];
    const raw = localStorage.getItem(PROMPT_HISTORY_KEY);
    if (!raw) return [];
    const parsed = JSON.parse(raw);
    if (Array.isArray(parsed)) return parsed.filter((s) => typeof s === "string");
  } catch {
    /* ignore */
  }
  return [];
}

export function clearLocalPromptHistory(): boolean {
  try {
    localStorage.removeItem(PROMPT_HISTORY_KEY);
    if (typeof window !== "undefined") {
      window.dispatchEvent(new Event(PROMPT_HISTORY_CLEARED_EVENT));
    }
    return true;
  } catch {
    return false;
  }
}

/**
 * Shell-style Up/Down arrow history navigation in Composer.
 */
export function createPromptHistory(sessionID: Accessor<string | null>) {
  const [entries, setEntries] = createSignal<string[]>(loadStoredHistory());
  const [cursorIdx, setCursorIdx] = createSignal(-1);
  let savedDraft = "";
  let lastSession: string | null = null;

  const clearHistory = () => {
    setEntries([]);
    setCursorIdx(-1);
    savedDraft = "";
  };
  if (typeof window !== "undefined") {
    window.addEventListener(PROMPT_HISTORY_CLEARED_EVENT, clearHistory);
    onCleanup(() => window.removeEventListener(PROMPT_HISTORY_CLEARED_EVENT, clearHistory));
  }

  const syncSession = () => {
    const cur = sessionID();
    if (cur !== lastSession) {
      lastSession = cur;
      setCursorIdx(-1);
      savedDraft = "";
    }
  };

  const recordPrompt = (text: string) => {
    const cleaned = text.trim();
    if (!cleaned || cleaned.length > 4000) return;
    setCursorIdx(-1);
    savedDraft = "";
    setEntries((prev) => {
      const deduped = [cleaned, ...prev.filter((item) => item !== cleaned)].slice(
        0,
        MAX_HISTORY
      );
      try {
        localStorage.setItem(PROMPT_HISTORY_KEY, JSON.stringify(deduped));
      } catch {
        /* ignore */
      }
      return deduped;
    });
  };

  const navigateHistory = (
    direction: "up" | "down",
    currentText: string,
    selectionStart: number
  ): string | null => {
    syncSession();
    const list = entries();
    if (list.length === 0) return null;

    const browsing = cursorIdx() >= 0;
    if (direction === "up") {
      const onFirstLine = !currentText.slice(0, selectionStart).includes("\n");
      if (!browsing && currentText.trim().length > 0 && !onFirstLine) return null;

      if (cursorIdx() === -1) {
        savedDraft = currentText;
        setCursorIdx(0);
        return list[0] ?? null;
      }
      const next = cursorIdx() + 1;
      if (next >= list.length) return null;
      setCursorIdx(next);
      return list[next] ?? null;
    }

    if (!browsing) return null;
    const onLastLine = !currentText.slice(selectionStart).includes("\n");
    if (!onLastLine) return null;

    if (cursorIdx() > 0) {
      const next = cursorIdx() - 1;
      setCursorIdx(next);
      return list[next] ?? null;
    }
    setCursorIdx(-1);
    return savedDraft;
  };

  return {
    recordPrompt,
    navigateHistory,
    resetHistoryCursor: () => setCursorIdx(-1),
  };
}
