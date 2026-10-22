import { requestJSON } from "./http";

export interface DesktopNotificationRequest {
  title: string;
  body: string;
  sessionID?: string;
}

export type DesktopNotificationPermissionStatus =
  | "unavailable"
  | "unknown"
  | "notDetermined"
  | "denied"
  | "authorized"
  | "provisional"
  | "ephemeral";

export interface DesktopNotificationPermission {
  available: boolean;
  status: DesktopNotificationPermissionStatus;
}

const PERMISSION_STATUSES: ReadonlySet<string> = new Set([
  "unavailable",
  "unknown",
  "notDetermined",
  "denied",
  "authorized",
  "provisional",
  "ephemeral",
]);

const UNAVAILABLE_PERMISSION: DesktopNotificationPermission = {
  available: false,
  status: "unavailable",
};

function normalizePermission(raw: unknown): DesktopNotificationPermission {
  const value = (raw ?? {}) as { available?: unknown; status?: unknown };
  const status = typeof value.status === "string" && PERMISSION_STATUSES.has(value.status)
    ? (value.status as DesktopNotificationPermissionStatus)
    : "unavailable";
  return { available: Boolean(value.available), status };
}

// The desktop gateway owns native delivery (macOS Notification Center) and the
// click-to-session callback. Outside the desktop shell the same call fails and
// we fall back to the browser notification API.
export async function deliverDesktopNotification(request: DesktopNotificationRequest): Promise<void> {
  const title = request.title.trim();
  if (!title) return;
  const body = request.body.trim();
  const sessionID = (request.sessionID || "").trim();
  if (await deliverNative(title, body, sessionID)) return;
  showBrowserNotification(title, body, sessionID);
}

// Reads the host's notification authorization without prompting the user.
export async function fetchDesktopNotificationPermission(): Promise<DesktopNotificationPermission> {
  try {
    const result = await requestJSON<DesktopNotificationPermission>(
      "/api/hoxi/desktop/notify/permission"
    );
    return normalizePermission(result);
  } catch {
    return UNAVAILABLE_PERMISSION;
  }
}

// Asks the host to prompt for notification authorization (no-op when already
// decided) and returns the resulting status.
export async function requestDesktopNotificationPermission(): Promise<DesktopNotificationPermission> {
  try {
    const result = await requestJSON<DesktopNotificationPermission>(
      "/api/hoxi/desktop/notify/permission",
      { method: "POST" }
    );
    return normalizePermission(result);
  } catch {
    return UNAVAILABLE_PERMISSION;
  }
}

async function deliverNative(title: string, body: string, sessionID: string): Promise<boolean> {
  try {
    const result = await requestJSON<{ ok?: boolean }>("/api/hoxi/desktop/notify", {
      method: "POST",
      body: JSON.stringify({ title, body, sessionID }),
    });
    return Boolean(result?.ok);
  } catch {
    return false;
  }
}

function showBrowserNotification(title: string, body: string, sessionID: string): void {
  if (typeof window === "undefined" || !("Notification" in window)) return;
  try {
    const show = () => {
      const notification = new Notification(title, { body, silent: true });
      notification.onclick = () => {
        window.focus();
        if (sessionID) {
          window.dispatchEvent(
            new CustomEvent("hoxi:notification-click", {
              detail: { sessionID },
            })
          );
        }
        notification.close();
      };
    };
    if (Notification.permission === "granted") {
      show();
    } else if (Notification.permission !== "denied") {
      void Notification.requestPermission().then((permission) => {
        if (permission === "granted") show();
      });
    }
  } catch {
    /* ignore notification errors in embedded webview */
  }
}
