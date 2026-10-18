// web/src/store/auth-state.ts
import { createStore } from "solid-js/store";
import { authAPI, type AuthStatusSnapshot, type AuthUserProfile } from "../services/api";
import { setAppSettings, updateAppSetting } from "./settings";

export interface DesktopAuthState {
  isLoggedIn: boolean;
  isAuthorizing: boolean;
  user: AuthUserProfile | null;
  expiresAt: string | null;
  serverBaseUrl: string;
  lastCheckedAt: string | null;
  error: string | null;
}

export const [desktopAuth, setDesktopAuth] = createStore<DesktopAuthState>({
  isLoggedIn: false,
  isAuthorizing: false,
  user: null,
  expiresAt: null,
  serverBaseUrl: "https://hoxi.ai",
  lastCheckedAt: null,
  error: null,
});

let pollTimer: ReturnType<typeof setInterval> | null = null;

function stopPolling() {
  if (pollTimer) {
    clearInterval(pollTimer);
    pollTimer = null;
  }
}

export async function refreshAuthStatus(): Promise<AuthStatusSnapshot | null> {
  try {
    const snap = await authAPI.getStatus();
    setDesktopAuth({
      isLoggedIn: snap.isLoggedIn,
      isAuthorizing: snap.isAuthorizing,
      user: snap.user || null,
      expiresAt: snap.expiresAt || null,
      serverBaseUrl: snap.serverBaseUrl,
      lastCheckedAt: new Date().toISOString(),
      error: null,
    });

    if (snap.isLoggedIn && snap.user) {
      setAppSettings("userLoggedIn", true);
      if (snap.user.nickname) {
        updateAppSetting("userDisplayName", snap.user.nickname);
      }
      if (snap.user.email) {
        updateAppSetting("userEmail", snap.user.email);
      }
      stopPolling();
    } else if (!snap.isAuthorizing) {
      stopPolling();
    }

    return snap;
  } catch (err: any) {
    setDesktopAuth("error", err?.message || "Failed to fetch auth status");
    return null;
  }
}

export async function startDesktopAuthorization(): Promise<string | null> {
  try {
    setDesktopAuth({ isAuthorizing: true, error: null });
    const res = await authAPI.startAuth();

    stopPolling();
    // 开启前端每 1.5 秒轻量同步状态
    pollTimer = setInterval(async () => {
      const snap = await refreshAuthStatus();
      if (snap?.isLoggedIn || !snap?.isAuthorizing) {
        stopPolling();
      }
    }, 1500);

    return res.authUrl;
  } catch (err: any) {
    setDesktopAuth({
      isAuthorizing: false,
      error: err?.message || "Failed to start authorization",
    });
    return null;
  }
}

export function cancelDesktopAuthorization() {
  stopPolling();
  setDesktopAuth("isAuthorizing", false);
}

export async function logoutDesktopAccount(): Promise<void> {
  stopPolling();
  try {
    await authAPI.logout();
  } catch {
    /* ignore network error */
  }
  setDesktopAuth({
    isLoggedIn: false,
    isAuthorizing: false,
    user: null,
    expiresAt: null,
    error: null,
  });
  setAppSettings("userLoggedIn", false);
}
