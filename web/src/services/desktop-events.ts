// web/src/services/desktop-events.ts
// Wails runtime event transport. The desktop host emits payload-less wake-ups;
// callers fetch the actual data through the gateway so early events are never lost.

export const DESKTOP_INTENT_EVENT = "hoxi:desktop-intent";

type WailsEventsOn = (eventName: string, callback: (...data: unknown[]) => void) => (() => void) | void;

function wailsEventsOn(): WailsEventsOn | null {
  if (typeof window === "undefined") return null;
  const runtime = (window as unknown as { runtime?: { EventsOn?: WailsEventsOn } }).runtime;
  return typeof runtime?.EventsOn === "function" ? runtime.EventsOn : null;
}

/**
 * Subscribes to desktop intent wake-ups. Returns the listener-specific
 * unsubscribe from `EventsOn`; never uses `EventsOff(name)`, which would drop
 * every listener registered for the event.
 */
export function onDesktopIntent(callback: () => void): () => void {
  if (typeof window === "undefined") return () => {};
  const listener = () => callback();
  const eventsOn = wailsEventsOn();
  const offWails = eventsOn ? eventsOn(DESKTOP_INTENT_EVENT, listener) : undefined;
  const hasDOMEvents =
    typeof window.addEventListener === "function" &&
    typeof window.removeEventListener === "function";
  if (hasDOMEvents) {
    window.addEventListener(DESKTOP_INTENT_EVENT, listener);
    window.addEventListener("focus", listener);
  }
  return () => {
    if (typeof offWails === "function") {
      offWails();
    }
    if (hasDOMEvents) {
      window.removeEventListener(DESKTOP_INTENT_EVENT, listener);
      window.removeEventListener("focus", listener);
    }
  };
}

/** Opens an external URL through the Wails host browser bridge or `window.open` fallback. */
export function openExternalBrowserURL(url: string): void {
  if (typeof window === "undefined" || !url) return;
  const runtime = (window as unknown as { runtime?: { BrowserOpenURL?: (target: string) => void } }).runtime;
  if (typeof runtime?.BrowserOpenURL === "function") {
    runtime.BrowserOpenURL(url);
    return;
  }
  window.open(url, "_blank", "noopener,noreferrer");
}

// Called only by explicit copy/paste gestures. Prefer Wails' native clipboard:
// WebKit's browser clipboard permission can expire while a page RPC is pending.
export async function writeDesktopClipboard(text: string): Promise<void> {
  const runtime = (typeof window === "undefined" ? undefined : window as unknown as {
    runtime?: { ClipboardSetText?: (value: string) => Promise<boolean> };
  })?.runtime;
  if (typeof runtime?.ClipboardSetText === "function") {
    if (!await runtime.ClipboardSetText(text)) throw new Error("Clipboard write failed");
    return;
  }
  if (typeof navigator === "undefined" || !navigator.clipboard?.writeText) throw new Error("Clipboard unavailable");
  await navigator.clipboard.writeText(text);
}

export async function readDesktopClipboard(): Promise<string> {
  const runtime = (typeof window === "undefined" ? undefined : window as unknown as {
    runtime?: { ClipboardGetText?: () => Promise<string> };
  })?.runtime;
  if (typeof runtime?.ClipboardGetText === "function") return runtime.ClipboardGetText();
  if (typeof navigator === "undefined" || !navigator.clipboard?.readText) throw new Error("Clipboard unavailable");
  return navigator.clipboard.readText();
}
