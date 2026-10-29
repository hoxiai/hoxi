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
  refreshDesktopMember,
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
      assertEqual(workspace.connectedProviders.includes(OFFICIAL_PROVIDER_ID), false);
      assertEqual(workspace.providers.some((p) => p.id === OFFICIAL_PROVIDER_ID), false);
      assertEqual(workspace.selectedModel?.providerID, "anthropic");
      assertEqual(workspace.selectedModel?.modelID, "claude-sonnet-4-6");
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
