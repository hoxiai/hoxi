import { authAPI } from "../services/api";
import type { MemberCheckinStatus } from "../services/platform-api";
import {
  desktopAuth,
  performDesktopCheckin,
  refreshDesktopMember,
  setDesktopAuth,
} from "../store/auth-state";
import { appSettings, setAppSettings } from "../store/settings";
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
});
