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
  const eventsOn = wailsEventsOn();
  if (!eventsOn) return () => {};
  const off = eventsOn(DESKTOP_INTENT_EVENT, () => callback());
  return typeof off === "function" ? off : () => {};
}
