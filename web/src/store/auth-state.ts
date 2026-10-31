// web/src/store/auth-state.ts
import { createStore } from "solid-js/store";
import { t } from "../i18n";
import { authAPI, type AuthStatusSnapshot, type AuthUserProfile } from "../services/api";
import { openExternalBrowserURL } from "../services/desktop-events";
import { errorMessageOf } from "../services/http";
import type { MemberCheckinStatus, PortalTarget } from "../services/platform-api";
import { setAppSettings, signOutUserAccount, updateAppSetting } from "./settings";
import { OFFICIAL_PROVIDER_ID, workspace } from "./workspace-state";

export interface DesktopMemberState {
  loaded: boolean;
  loading: boolean;
  checkingIn: boolean;
  checkedInToday: boolean;
  streak: number;
  maxStreak: number;
  todayPoints: number;
  nextPoints: number;
  todayPointsRange: { min: number; max: number };
  nextPointsRange: { min: number; max: number };
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
  todayPoints: 5000,
  nextPoints: 100000,
  todayPointsRange: { min: 5000, max: 100000 },
  nextPointsRange: { min: 5000, max: 100000 },
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
let authGeneration = 0;
let memberRequestSeq = 0;

// Points change outside this window (official-model turns, top-ups on the
// website), so the balance is re-fetched on these triggers.
export const MEMBER_FOCUS_REFRESH_MIN_MS = 30_000;
export const MEMBER_POLL_INTERVAL_MS = 5 * 60_000;
export const MEMBER_TURN_REFRESH_DELAY_MS = 1_500;
let lastMemberRefreshAt = 0;
let turnRefreshTimer: ReturnType<typeof setTimeout> | null = null;

function clearTurnRefreshTimer() {
  if (turnRefreshTimer) {
    clearTimeout(turnRefreshTimer);
    turnRefreshTimer = null;
  }
}

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

const authChangeListeners = new Set<() => void>();

export function onDesktopAuthChanged(fn: () => void): () => void {
  authChangeListeners.add(fn);
  return () => authChangeListeners.delete(fn);
}

export function desktopAuthGeneration(): number {
  return authGeneration;
}

function notifyAccountChanged(): void {
  for (const fn of authChangeListeners) fn();
}

export function formatMemberPoints(value: number): string {
  if (!Number.isFinite(value) || value <= 0) return "0";
  return value.toLocaleString(undefined, {
    minimumFractionDigits: 0,
    maximumFractionDigits: 2,
  });
}

export function formatMemberPointsRange(
  range: { min: number; max: number } | undefined,
  fallback: number
): string {
  const min = Number(range?.min || 0);
  const max = Number(range?.max || 0);
  if (min > 0 && max > min) {
    return `${formatMemberPoints(min)}~${formatMemberPoints(max)}`;
  }
  return formatMemberPoints(max > 0 ? max : fallback);
}

export function desktopUserDisplayName(): string {
  return (desktopAuth.user?.nickname || desktopAuth.user?.username || "Developer").trim();
}

export function desktopUserEmail(): string {
  return desktopAuth.user?.email || "";
}

export function desktopUserAvatar(): string {
  return desktopAuth.user?.avatar || desktopAuth.user?.avatarUrl || "";
}

export function desktopUserPlan(): string {
  return desktopAuth.user?.plan || t("settings.accountPlanBadge");
}

export function desktopUserInitial(): string {
  const name = desktopUserDisplayName();
  return name ? name.charAt(0).toUpperCase() : "U";
}

export function desktopCheckinSubtitle(): string {
  const m = desktopAuth.member;
  if (m.nextMilestone && m.nextMilestone.day > 0) {
    return t("settings.accountCheckinMilestoneDesc", {
      streak: m.streak,
      day: m.nextMilestone.day,
      bonus: m.nextMilestone.points,
    });
  }
  return t("settings.accountCheckinStreakDesc", {
    streak: m.streak,
    next: formatMemberPointsRange(m.nextPointsRange, m.nextPoints),
  });
}

function resetLoggedOutState() {
  stopPolling();
  clearTurnRefreshTimer();
  authGeneration++;
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
  notifyAccountChanged();
}

function applyMemberStatus(status: MemberCheckinStatus) {
  const walletPts =
    typeof status.walletPoints === "number" && Number.isFinite(status.walletPoints)
      ? status.walletPoints
      : toFiniteNumber(status.balance, 0);
  const todayPts = toFiniteNumber(status.todayPoints, 5);
  const nextPts = toFiniteNumber(status.nextPoints, 6);
  const todayMin = toFiniteNumber(status.todayPointsRange?.min, todayPts);
  const todayMax = Math.max(todayMin, toFiniteNumber(status.todayPointsRange?.max, todayPts));
  const nextMin = toFiniteNumber(status.nextPointsRange?.min, nextPts);
  const nextMax = Math.max(nextMin, toFiniteNumber(status.nextPointsRange?.max, nextPts));
  setDesktopAuth("member", {
    loaded: true,
    loading: false,
    checkingIn: false,
    checkedInToday: Boolean(status.checkedInToday),
    streak: toFiniteNumber(status.streak, 0),
    maxStreak: toFiniteNumber(status.maxStreak, 0),
    todayPoints: todayPts,
    nextPoints: nextPts,
    todayPointsRange: { min: todayMin, max: todayMax },
    nextPointsRange: { min: nextMin, max: nextMax },
    points: Number.isFinite(walletPts) ? walletPts : 0,
    pendingPoints: toFiniteNumber(status.pendingPoints, 0),
    rewardStatus: String(status.rewardStatus || "none"),
    nextMilestone: status.nextMilestone ?? null,
    error: null,
  });
}

export async function refreshDesktopMember(): Promise<MemberCheckinStatus | null> {
  if (!desktopAuth.isLoggedIn || desktopAuth.member.checkingIn) return null;
  const generation = authGeneration;
  const seq = ++memberRequestSeq;
  lastMemberRefreshAt = Date.now();
  setDesktopAuth("member", "loading", true);
  setDesktopAuth("member", "error", null);
  try {
    const status = await authAPI.getMemberStatus();
    if (generation !== authGeneration || seq !== memberRequestSeq) return null;
    applyMemberStatus(status);
    return status;
  } catch (err: any) {
    if (generation !== authGeneration) return null;
    if (isUnauthorizedError(err)) {
      resetLoggedOutState();
      return null;
    }
    if (seq !== memberRequestSeq) return null;
    setDesktopAuth("member", {
      loading: false,
      error: errorMessageOf(err, "Failed to load member status"),
    });
    return null;
  }
}

/** Re-fetches the balance unless it was fetched within `minIntervalMs`. */
export function refreshDesktopMemberIfStale(minIntervalMs: number): void {
  if (!desktopAuth.isLoggedIn) return;
  if (Date.now() - lastMemberRefreshAt < minIntervalMs) return;
  void refreshDesktopMember();
}

/** Refreshes on window focus / tab wake and on a slow interval while visible. */
export function startDesktopMemberAutoRefresh(): () => void {
  if (typeof window === "undefined" || typeof document === "undefined") return () => {};
  const onFocus = () => refreshDesktopMemberIfStale(MEMBER_FOCUS_REFRESH_MIN_MS);
  const onVisibility = () => {
    if (document.visibilityState === "visible") onFocus();
  };
  window.addEventListener("focus", onFocus);
  document.addEventListener("visibilitychange", onVisibility);
  const timer = setInterval(() => {
    if (document.visibilityState === "hidden") return;
    refreshDesktopMemberIfStale(MEMBER_POLL_INTERVAL_MS - 1_000);
  }, MEMBER_POLL_INTERVAL_MS);
  return () => {
    window.removeEventListener("focus", onFocus);
    document.removeEventListener("visibilitychange", onVisibility);
    clearInterval(timer);
  };
}

/** Whether the latest turn (assistant replies after the last user message) used the official provider. */
export function sessionLastTurnUsedOfficialModel(sessionID: string): boolean {
  const turns = workspace.messagesBySession[sessionID] || [];
  for (let i = turns.length - 1; i >= 0; i--) {
    const info = turns[i]?.info;
    if (!info) continue;
    if (info.role === "user") break;
    if (info.providerID === OFFICIAL_PROVIDER_ID) return true;
  }
  return false;
}

/** Called when a session turn ends; only official-model turns spend points. */
export function refreshDesktopMemberAfterTurn(sessionID: string): void {
  if (!desktopAuth.isLoggedIn || !sessionLastTurnUsedOfficialModel(sessionID)) return;
  clearTurnRefreshTimer();
  turnRefreshTimer = setTimeout(() => {
    turnRefreshTimer = null;
    void refreshDesktopMember();
  }, MEMBER_TURN_REFRESH_DELAY_MS);
}

export async function performDesktopCheckin(): Promise<MemberCheckinStatus | null> {
  if (!desktopAuth.isLoggedIn || desktopAuth.member.checkingIn || desktopAuth.member.checkedInToday) {
    return null;
  }
  const generation = authGeneration;
  const seq = ++memberRequestSeq;
  setDesktopAuth("member", { loading: false, checkingIn: true, error: null });
  try {
    const status = await authAPI.checkin();
    if (generation !== authGeneration || seq !== memberRequestSeq) return null;
    applyMemberStatus(status);
    return status;
  } catch (err: any) {
    if (generation !== authGeneration) return null;
    if (isUnauthorizedError(err)) {
      resetLoggedOutState();
      return null;
    }
    if (seq !== memberRequestSeq) return null;
    setDesktopAuth("member", {
      checkingIn: false,
      error: errorMessageOf(err, "Check-in failed"),
    });
    return null;
  }
}

export function resolveOfficialWebURL(path: string): string {
  const base = (desktopAuth.serverBaseUrl || "https://hoxi.ai").replace(/\/+$/, "");
  const cleanPath = path.startsWith("/") ? path : `/${path}`;
  return `${base}${cleanPath}`;
}

export async function openDesktopPortalPage(target: PortalTarget): Promise<void> {
  try {
    await authAPI.openPortalPage(target);
  } catch {
    const fallbackUrl =
      target === "feedback"
        ? "https://github.com/hoxiai/hoxi/issues"
        : resolveOfficialWebURL(`/user/${target}`);
    openExternalBrowserURL(fallbackUrl);
  }
}

export async function refreshAuthStatus(): Promise<AuthStatusSnapshot | null> {
  const generation = authGeneration;
  try {
    const snap = await authAPI.getStatus();
    if (generation !== authGeneration) return null;
    const wasLoggedIn = desktopAuth.isLoggedIn;
    const accountSwitched =
      wasLoggedIn &&
      (!snap.isLoggedIn ||
        desktopAuth.user?.id !== snap.user?.id ||
        desktopAuth.serverBaseUrl !== snap.serverBaseUrl);
    const changed =
      wasLoggedIn !== snap.isLoggedIn ||
      desktopAuth.user?.id !== snap.user?.id ||
      desktopAuth.serverBaseUrl !== snap.serverBaseUrl;
    if (changed) {
      authGeneration++;
      memberRequestSeq++;
      if (accountSwitched) notifyAccountChanged();
    }
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
      notifyAccountChanged();
    }

    return snap;
  } catch (err: any) {
    if (generation !== authGeneration) return null;
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
      error: errorMessageOf(err, "Failed to start authorization"),
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
    // The device token may already be gone while dependent credential cleanup failed.
    await refreshAuthStatus();
    setDesktopAuth("error", err instanceof Error ? err.message : "Logout failed");
    throw err;
  }
  resetLoggedOutState();
}
