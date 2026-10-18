export const MANUAL_TITLES_KEY = "hoxi-session-manual-titles:v1";

export function loadManualSessionTitles(): Map<string, string> {
  try {
    const value: unknown = JSON.parse(localStorage.getItem(MANUAL_TITLES_KEY) || "{}");
    if (!value || typeof value !== "object" || Array.isArray(value)) return new Map();
    return new Map(Object.entries(value).filter(
      (entry): entry is [string, string] => Boolean(entry[0])
        && typeof entry[1] === "string" && Boolean(entry[1].trim())
    ).map(([id, title]) => [id, title.trim()]));
  } catch {
    return new Map();
  }
}

export const manualSessionTitles = loadManualSessionTitles();

export function persistManualSessionTitles(): void {
  try {
    localStorage.setItem(MANUAL_TITLES_KEY, JSON.stringify(Object.fromEntries(manualSessionTitles)));
  } catch {
    // Keep in-memory protection when storage is unavailable.
  }
}
