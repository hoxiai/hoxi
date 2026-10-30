import { authAPI } from "../services/api";
import type { MemberCheckinStatus } from "../services/platform-api";
import {
  desktopAuth,
  desktopUserDisplayName,
  desktopUserInitial,
  desktopUserPlan,
  formatMemberPoints,
  formatMemberPointsRange,
  logoutDesktopAccount,
  performDesktopCheckin,
  refreshAuthStatus,
  refreshDesktopMember,
  refreshDesktopMemberAfterTurn,
  refreshDesktopMemberIfStale,
  sessionLastTurnUsedOfficialModel,
  setDesktopAuth,
} from "../store/auth-state";
import { officialAinodeState, setOfficialAinodeState } from "../store/provider-official";
import { appSettings, setAppSettings } from "../store/settings";
import { OFFICIAL_PROVIDER_ID, setWorkspace, workspace } from "../store/workspace-state";
import { assertEqual, describe, it } from "./test-harness";

describe("desktop auth member state & race protection", () => {
  it("resets login state when member status or check-in returns HTTP 401", async () => {
    const origGetMember = authAPI.getMemberStatus;
    const origCheckin = authAPI.checkin;
    try {
      setDesktopAuth({
        isLoggedIn: true,
        user: { id: 1, email: "dev@hoxi.ai", nickname: "Dev" },
      });
      setAppSettings("userLoggedIn", true);

      authAPI.getMemberStatus = async () => {
        throw new Error('HTTP 401: {"error":"not logged in"}');
      };

      const res = await refreshDesktopMember();
      assertEqual(res, null);
      assertEqual(desktopAuth.isLoggedIn, false);
      assertEqual(desktopAuth.user, null);
      assertEqual(appSettings.userLoggedIn, false);

      setDesktopAuth({
        isLoggedIn: true,
        user: { id: 1, email: "dev@hoxi.ai", nickname: "Dev" },
      });
      setAppSettings("userLoggedIn", true);

      authAPI.checkin = async () => {
        throw new Error('HTTP 401: {"error":"not logged in"}');
      };

      const checkinRes = await performDesktopCheckin();
      assertEqual(checkinRes, null);
      assertEqual(desktopAuth.isLoggedIn, false);
      assertEqual(desktopAuth.user, null);
      assertEqual(appSettings.userLoggedIn, false);
    } finally {
      authAPI.getMemberStatus = origGetMember;
      authAPI.checkin = origCheckin;
    }
  });

  it("ignores stale refreshDesktopMember response when performDesktopCheckin starts and preserves zero points", async () => {
    const origGetMember = authAPI.getMemberStatus;
    const origCheckin = authAPI.checkin;
    try {
      setDesktopAuth({
        isLoggedIn: true,
        user: { id: 1, email: "dev@hoxi.ai", nickname: "Dev" },
        member: {
          ...desktopAuth.member,
          checkingIn: false,
          checkedInToday: false,
          points: 10,
        },
      });

      let resolveStaleGet!: (v: MemberCheckinStatus) => void;
      authAPI.getMemberStatus = () =>
        new Promise<MemberCheckinStatus>((resolve) => {
          resolveStaleGet = resolve;
        });

      authAPI.checkin = async (): Promise<MemberCheckinStatus> => ({
        checkedInToday: true,
        streak: 4,
        maxStreak: 4,
        todayPoints: 0,
        nextPoints: 0,
        balance: 25,
        walletPoints: 25,
      });

      const pendingRefresh = refreshDesktopMember();
      const checkinResult = await performDesktopCheckin();
      assertEqual(checkinResult?.checkedInToday, true);
      assertEqual(desktopAuth.member.checkedInToday, true);
      assertEqual(desktopAuth.member.points, 25);
      assertEqual(desktopAuth.member.todayPoints, 0);
      assertEqual(desktopAuth.member.nextPoints, 0);

      // Now let the earlier GET /member resolve with pre-checkin stale data.
      resolveStaleGet({
        checkedInToday: false,
        streak: 3,
        maxStreak: 3,
        todayPoints: 8,
        nextPoints: 9,
        balance: 10,
        walletPoints: 10,
      });
      await pendingRefresh;

      assertEqual(desktopAuth.member.checkedInToday, true);
      assertEqual(desktopAuth.member.points, 25);
      assertEqual(desktopAuth.member.streak, 4);
      assertEqual(desktopAuth.member.todayPoints, 0);
      assertEqual(desktopAuth.member.nextPoints, 0);
    } finally {
      authAPI.getMemberStatus = origGetMember;
      authAPI.checkin = origCheckin;
    }
  });

  it("purges hoxi-official provider state and catalog on logout or HTTP 401", async () => {
    const origLogout = authAPI.logout;
    const prevHoxiProviders = workspace.hoxiProviders;
    const prevConnected = workspace.connectedProviders;
    const prevProviders = workspace.providers;
    const prevDefaultModel = workspace.defaultAI.model;
    try {
      authAPI.logout = async () => ({ success: true });
      setDesktopAuth({
        isLoggedIn: true,
        user: { id: 1, email: "dev@hoxi.ai", nickname: "Dev" },
      });
      setOfficialAinodeState({
        loaded: true,
        loading: false,
        baseUrl: "https://api.ainode.run/v1",
        models: [{ id: "gpt-5.4", name: "GPT-5.4", inputPointsPerM: 25000, outputPointsPerM: 100000, perCallPoints: 0 }],
        error: null,
      });
      setWorkspace({
        globalConfig: { provider: { [OFFICIAL_PROVIDER_ID]: { models: { "gpt-5.4": {} } } } },
        hoxiProviders: {
          [OFFICIAL_PROVIDER_ID]: { name: "Hoxi Official", models: { "gpt-5.4": { name: "GPT-5.4" } } },
          anthropic: { name: "Anthropic", models: { "claude-sonnet-4-6": { name: "Claude Sonnet 4.6" } } },
        },
        connectedProviders: [OFFICIAL_PROVIDER_ID, "anthropic"],
        providers: [
          { id: OFFICIAL_PROVIDER_ID, name: "Hoxi Official", models: { "gpt-5.4": { id: "gpt-5.4", name: "GPT-5.4" } } },
          { id: "anthropic", name: "Anthropic", models: { "claude-sonnet-4-6": { id: "claude-sonnet-4-6", name: "Claude Sonnet 4.6" } } },
        ],
      });
      setWorkspace("defaultAI", "model", { providerID: OFFICIAL_PROVIDER_ID, modelID: "gpt-5.4" });

      await logoutDesktopAccount();

      assertEqual(desktopAuth.isLoggedIn, false);
      assertEqual(officialAinodeState.loaded, false);
      assertEqual(officialAinodeState.models.length, 0);
      assertEqual(Boolean(workspace.hoxiProviders[OFFICIAL_PROVIDER_ID]), false);
      assertEqual(Boolean(workspace.globalConfig?.provider?.[OFFICIAL_PROVIDER_ID]), false);
      assertEqual(workspace.connectedProviders.includes(OFFICIAL_PROVIDER_ID), false);
      assertEqual(workspace.providers.some((p) => p.id === OFFICIAL_PROVIDER_ID), false);
      assertEqual(workspace.selectedModel, null);
    } finally {
      authAPI.logout = origLogout;
      setWorkspace({
        hoxiProviders: prevHoxiProviders,
        connectedProviders: prevConnected,
        providers: prevProviders,
      });
      setWorkspace("defaultAI", "model", prevDefaultModel);
    }
  });

  it("formats member points and user identity selectors consistently", () => {
    assertEqual(formatMemberPoints(1200), "1,200");
    assertEqual(formatMemberPoints(12.5), "12.5");
    assertEqual(formatMemberPoints(12.567), "12.57");
    assertEqual(formatMemberPointsRange({ min: 5, max: 5 }, 5), "5");
    assertEqual(formatMemberPointsRange({ min: 5, max: 20 }, 5), "5~20");
    assertEqual(formatMemberPointsRange(undefined, 8), "8");

    setDesktopAuth({
      isLoggedIn: true,
      user: { id: 1, email: "alice@hoxi.ai", nickname: "Alice", plan: "Pro" },
    });
    assertEqual(desktopUserDisplayName(), "Alice");
    assertEqual(desktopUserPlan(), "Pro");
    assertEqual(desktopUserInitial(), "A");
  });
});


describe("logout model and credential cleanup", () => {
  it("clears official session models without selecting an unconnected provider", async () => {
    const origLogout = authAPI.logout;
    const saved = { providers: workspace.providers, connectedProviders: workspace.connectedProviders, model: workspace.defaultAI.model };
    const sid = "ses_logout_regression";
    try {
      authAPI.logout = async () => ({ success: true });
      setDesktopAuth({ isLoggedIn: true });
      setWorkspace({ providers: [{ id: "opencode", name: "OpenCode", models: { free: { id: "free", name: "Free" } } }], connectedProviders: [OFFICIAL_PROVIDER_ID] });
      setWorkspace("defaultAI", "model", { providerID: OFFICIAL_PROVIDER_ID, modelID: "old" });
      setWorkspace("aiBySession", sid, { ...workspace.defaultAI });
      setWorkspace("statusBySession", sid, { type: "busy" });
      await logoutDesktopAccount();
      assertEqual(workspace.defaultAI.model, null);
      assertEqual(workspace.aiBySession[sid]?.model, null);
      assertEqual(workspace.connectedProviders.length, 0);
    } finally {
      authAPI.logout = origLogout;
      setWorkspace("providers", saved.providers);
      setWorkspace("connectedProviders", saved.connectedProviders);
      setWorkspace("defaultAI", "model", saved.model);
      setWorkspace("aiBySession", sid, undefined as any);
      setWorkspace("statusBySession", sid, undefined as any);
    }
  });

  it("shows cleanup errors while synchronizing an already-cleared device login", async () => {
    const origLogout = authAPI.logout;
    const origStatus = authAPI.getStatus;
    try {
      setDesktopAuth({ isLoggedIn: true });
      authAPI.logout = async () => { throw new Error("provider cleanup failed"); };
      authAPI.getStatus = async () => ({ isLoggedIn: false, isAuthorizing: false, serverBaseUrl: "https://hoxi.ai" });
      let failed = false;
      try { await logoutDesktopAccount(); } catch { failed = true; }
      assertEqual(failed, true);
      assertEqual(desktopAuth.isLoggedIn, false);
      assertEqual(desktopAuth.error, "provider cleanup failed");
    } finally {
      authAPI.logout = origLogout;
      authAPI.getStatus = origStatus;
    }
  });
});

describe("member points auto refresh", () => {
  const memberStatus = (walletPoints: number) => ({ walletPoints } as unknown as MemberCheckinStatus);
  const turn = (id: string, role: "user" | "assistant", providerID?: string) => ({
    info: { id, sessionID: "", role, time: { created: 0 }, providerID },
    parts: [],
  });

  it("refreshes after a turn only when that turn used the official provider", async () => {
    const origGetMember = authAPI.getMemberStatus;
    const origSetTimeout = globalThis.setTimeout;
    const official = "ses_points_official";
    const other = "ses_points_other";
    const scheduled: Array<() => void> = [];
    let calls = 0;
    try {
      globalThis.setTimeout = ((fn: () => void) => {
        scheduled.push(fn);
        return scheduled.length as any;
      }) as typeof setTimeout;
      authAPI.getMemberStatus = async () => {
        calls++;
        return memberStatus(42);
      };
      setDesktopAuth({ isLoggedIn: true, member: { ...desktopAuth.member, checkingIn: false, points: 100 } });
      setWorkspace("messagesBySession", official, [
        turn("msg_1", "user"),
        turn("msg_2", "assistant", OFFICIAL_PROVIDER_ID),
      ] as any);
      // An earlier official turn must not count once a later turn used another provider.
      setWorkspace("messagesBySession", other, [
        turn("msg_1", "user"),
        turn("msg_2", "assistant", OFFICIAL_PROVIDER_ID),
        turn("msg_3", "user"),
        turn("msg_4", "assistant", "anthropic"),
      ] as any);

      assertEqual(sessionLastTurnUsedOfficialModel(official), true);
      assertEqual(sessionLastTurnUsedOfficialModel(other), false);
      assertEqual(sessionLastTurnUsedOfficialModel("ses_points_missing"), false);

      refreshDesktopMemberAfterTurn(other);
      assertEqual(scheduled.length, 0);

      refreshDesktopMemberAfterTurn(official);
      assertEqual(scheduled.length, 1);
      scheduled[0]();
      await Promise.resolve();
      await Promise.resolve();
      assertEqual(calls, 1);
      assertEqual(desktopAuth.member.points, 42);

      setDesktopAuth("isLoggedIn", false);
      refreshDesktopMemberAfterTurn(official);
      assertEqual(scheduled.length, 1);
    } finally {
      globalThis.setTimeout = origSetTimeout;
      authAPI.getMemberStatus = origGetMember;
      setWorkspace("messagesBySession", official, undefined as any);
      setWorkspace("messagesBySession", other, undefined as any);
    }
  });

  it("throttles stale refreshes and skips them when logged out", async () => {
    const origGetMember = authAPI.getMemberStatus;
    let calls = 0;
    try {
      authAPI.getMemberStatus = async () => {
        calls++;
        return memberStatus(7);
      };
      setDesktopAuth({ isLoggedIn: false });
      refreshDesktopMemberIfStale(0);
      assertEqual(calls, 0);

      setDesktopAuth({ isLoggedIn: true, member: { ...desktopAuth.member, checkingIn: false } });
      await refreshDesktopMember();
      assertEqual(calls, 1);
      refreshDesktopMemberIfStale(60_000);
      assertEqual(calls, 1);
      refreshDesktopMemberIfStale(0);
      await Promise.resolve();
      assertEqual(calls, 2);
    } finally {
      authAPI.getMemberStatus = origGetMember;
    }
  });
});
