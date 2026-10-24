// web/src/store/auth-state.ts
import { createStore } from "solid-js/store";
import { authAPI, type AuthStatusSnapshot, type AuthUserProfile } from "../services/api";
import type { MemberCheckinStatus, PortalTarget } from "../services/platform-api";
import { setAppSettings, signOutUserAccount, updateAppSetting } from "./settings";

export interface DesktopMemberState {
  loaded: boolean;
  loading: boolean;
  checkingIn: boolean;
  checkedInToday: boolean;
  streak: number;
  maxStreak: number;
  todayPoints: number;
  nextPoints: number;
  points: number;
  pendingPoints: number;
  rewardStatus: string;
  nextMilestone: { day: number; points: number; daysLeft: number } | null;
  error: string | null;
}

export interface DesktopAuthState {
  isLoggedIn: boolean;
  isAuthorizing: boolean;
  user: AuthUserProfile | null;
  expiresAt: string | null;
  serverBaseUrl: string;
  lastCheckedAt: string | null;
  error: string | null;
  member: DesktopMemberState;
}

const DEFAULT_MEMBER_STATE: DesktopMemberState = {
  loaded: false,
  loading: false,
  checkingIn: false,
  checkedInToday: false,
  streak: 0,
  maxStreak: 0,
  todayPoints: 5,
  nextPoints: 6,
  points: 0,
  pendingPoints: 0,
  rewardStatus: "none",
  nextMilestone: { day: 3, points: 10, daysLeft: 3 },
  error: null,
};

export const [desktopAuth, setDesktopAuth] = createStore<DesktopAuthState>({
  isLoggedIn: false,
  isAuthorizing: false,
  user: null,
  expiresAt: null,
  serverBaseUrl: "https://hoxi.ai",
  lastCheckedAt: null,
  error: null,
  member: { ...DEFAULT_MEMBER_STATE },
});

let pollTimer: ReturnType<typeof setInterval> | null = null;
let logoutGeneration = 0;
let memberRequestSeq = 0;

function stopPolling() {
  if (pollTimer) {
    clearInterval(pollTimer);
    pollTimer = null;
  }
}

function toFiniteNumber(val: unknown, fallback: number): number {
  if (val === null || val === undefined || val === "") return fallback;
  const n = typeof val === "number" ? val : Number(val);
  return Number.isFinite(n) ? n : fallback;
}

function isUnauthorizedError(err: unknown): boolean {
  const msg = err instanceof Error ? err.message : String(err ?? "");
  return /^HTTP 401\b/i.test(msg) || msg.toLowerCase().includes("not logged in");
}

function resetLoggedOutState() {
  stopPolling();
  logoutGeneration++;
  memberRequestSeq++;
  setDesktopAuth({
    isLoggedIn: false,
    isAuthorizing: false,
    user: null,
    expiresAt: null,
    error: null,
    member: { ...DEFAULT_MEMBER_STATE },
  });
  signOutUserAccount();
}

function applyMemberStatus(status: MemberCheckinStatus) {
  const walletPts =
    typeof status.walletPoints === "number" && Number.isFinite(status.walletPoints)
      ? status.walletPoints
      : toFiniteNumber(status.balance, 0);
  setDesktopAuth("member", {
    loaded: true,
    loading: false,
    checkingIn: false,
    checkedInToday: Boolean(status.checkedInToday),
    streak: toFiniteNumber(status.streak, 0),
    maxStreak: toFiniteNumber(status.maxStreak, 0),
    todayPoints: toFiniteNumber(status.todayPoints, 5),
    nextPoints: toFiniteNumber(status.nextPoints, 6),
    points: Number.isFinite(walletPts) ? walletPts : 0,
    pendingPoints: toFiniteNumber(status.pendingPoints, 0),
    rewardStatus: String(status.rewardStatus || "none"),
    nextMilestone: status.nextMilestone ?? null,
    error: null,
  });
}

export async function refreshDesktopMember(): Promise<MemberCheckinStatus | null> {
  if (!desktopAuth.isLoggedIn || desktopAuth.member.checkingIn) return null;
  const generation = logoutGeneration;
  const seq = ++memberRequestSeq;
  setDesktopAuth("member", "loading", true);
  setDesktopAuth("member", "error", null);
  try {
    const status = await authAPI.getMemberStatus();
    if (generation !== logoutGeneration || seq !== memberRequestSeq) return null;
    applyMemberStatus(status);
    return status;
  } catch (err: any) {
    if (generation !== logoutGeneration) return null;
    if (isUnauthorizedError(err)) {
      resetLoggedOutState();
      return null;
    }
    if (seq !== memberRequestSeq) return null;
    setDesktopAuth("member", {
      loading: false,
      error: err?.message || "Failed to load member status",
    });
    return null;
  }
}

export async function performDesktopCheckin(): Promise<MemberCheckinStatus | null> {
  if (!desktopAuth.isLoggedIn || desktopAuth.member.checkingIn || desktopAuth.member.checkedInToday) {
    return null;
  }
  const generation = logoutGeneration;
  const seq = ++memberRequestSeq;
  setDesktopAuth("member", { loading: false, checkingIn: true, error: null });
  try {
    const status = await authAPI.checkin();
    if (generation !== logoutGeneration || seq !== memberRequestSeq) return null;
    applyMemberStatus(status);
    return status;
  } catch (err: any) {
    if (generation !== logoutGeneration) return null;
    if (isUnauthorizedError(err)) {
      resetLoggedOutState();
      return null;
    }
    if (seq !== memberRequestSeq) return null;
    setDesktopAuth("member", {
      checkingIn: false,
      error: err?.message || "Check-in failed",
    });
    return null;
  }
}

export async function openDesktopPortalPage(target: PortalTarget): Promise<void> {
  try {
    await authAPI.openPortalPage(target);
  } catch {
    const base = (desktopAuth.serverBaseUrl || "https://hoxi.ai").replace(/\/+$/, "");
    const fallbackUrl =
      target === "feedback"
        ? "https://github.com/hoxiai/hoxi/issues"
        : `${base}/user/${target}`;
    if (typeof window !== "undefined") {
      window.open(fallbackUrl, "_blank", "noopener,noreferrer");
    }
  }
}

export async function refreshAuthStatus(): Promise<AuthStatusSnapshot | null> {
  const generation = logoutGeneration;
  try {
    const snap = await authAPI.getStatus();
    if (generation !== logoutGeneration) return null;
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
      void refreshDesktopMember();
    } else if (!snap.isAuthorizing) {
      stopPolling();
      memberRequestSeq++;
      setDesktopAuth("member", { ...DEFAULT_MEMBER_STATE });
      signOutUserAccount();
    }

    return snap;
  } catch (err: any) {
    if (generation !== logoutGeneration) return null;
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
    const result = await authAPI.logout();
    if (!result.success) throw new Error("Logout was not confirmed");
  } catch (err) {
    setDesktopAuth("error", err instanceof Error ? err.message : "Logout failed");
    throw err;
  }
  resetLoggedOutState();
}
