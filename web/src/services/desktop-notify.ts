import { requestJSON } from "./http";

export interface DesktopNotificationRequest {
  title: string;
  body: string;
  sessionID?: string;
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
