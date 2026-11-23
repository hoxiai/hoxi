import {
  UPDATE_CHECK_MIN_GAP_MS,
  UPDATE_SLOW_CONNECT_MS,
  UPDATE_SLOW_SPEED_MS,
  shouldAutoCheckUpdate,
  shouldNotifyUpdate,
  desktopUpdater,
  desktopUpdateButtonLabel,
  desktopUpdateDetailLabel,
  desktopUpdateProgressPercent,
  desktopUpdateSpeedEtaLabel,
  desktopUpdateStatusLabel,
  formatUpdateSpeed,
  getAcceleratedUpdateMirrorUrl,
  getGitHubUpdateMirrorUrl,
  isDesktopUpdateInstalling,
  isSelfUpdateActive,
  recordUpdateDownloadSample,
  fetchCliLinkStatus,
  installCliLink,
  refreshDesktopUpdateStatus,
  resetUpdateDownloadMetrics,
  setDesktopUpdateSource,
  setDesktopUpdater,
  startDesktopUpdate,
  uninstallCliLink,
} from "../store/updater-state";
import { cliLinkAPI, updaterAPI, type NativeUpdateStatus } from "../services/platform-api";
import { assertEqual, describe, it } from "./test-harness";

describe("desktop update re-check policy", () => {
  it("checks on first run and throttles later automatic checks", () => {
    assertEqual(shouldAutoCheckUpdate(1_000, 0, false), true);
    assertEqual(shouldAutoCheckUpdate(UPDATE_CHECK_MIN_GAP_MS, 0, false), true);
    assertEqual(shouldAutoCheckUpdate(1_000, 1_000, false), false);
    assertEqual(
      shouldAutoCheckUpdate(UPDATE_CHECK_MIN_GAP_MS + 1_000, 1_000, false),
      true
    );
  });

  it("never stacks checks while one is already in flight", () => {
    assertEqual(shouldAutoCheckUpdate(1_000, 0, true), false);
    assertEqual(shouldAutoCheckUpdate(UPDATE_CHECK_MIN_GAP_MS * 10, 0, true), false);
  });
});

describe("desktop self-update progress", () => {
  it("shows actual download percentages and leaves unknown lengths indeterminate", () => {
    const progress = { phase: "downloading" as const, downloadedBytes: 25, totalBytes: 100 };
    assertEqual(desktopUpdateProgressPercent(progress), 25);
    assertEqual(desktopUpdateProgressPercent({ ...progress, totalBytes: 0 }), undefined);
    assertEqual(desktopUpdateProgressPercent({ ...progress, downloadedBytes: 200 }), 100);
    assertEqual(desktopUpdateProgressPercent({ ...progress, phase: "staging" }), undefined);
    assertEqual(isSelfUpdateActive(progress), true);
    assertEqual(isSelfUpdateActive({ ...progress, phase: "failed" }), false);
    assertEqual(isSelfUpdateActive(null), false);
  });

  it("recovers a running download from status and prevents a duplicate install", async () => {
    const oldStatus = updaterAPI.nativeStatus;
    const oldInstall = updaterAPI.install;
    const saved = { ...desktopUpdater };
    let installs = 0;
    try {
      updaterAPI.nativeStatus = async () => ({
        available: true, draining: true,
        progress: { phase: "downloading", downloadedBytes: 25, totalBytes: 100 },
      });
      updaterAPI.install = async () => { installs++; return { started: true }; };
      setDesktopUpdater({ isInstalling: false, progress: null });
      await refreshDesktopUpdateStatus();
      await startDesktopUpdate();
      assertEqual(desktopUpdater.progress?.downloadedBytes, 25);
      assertEqual(desktopUpdater.draining, true);
      assertEqual(installs, 0);
      assertEqual(isDesktopUpdateInstalling(), true);
      assertEqual(desktopUpdateButtonLabel().endsWith(" 25%"), true);
    } finally {
      updaterAPI.nativeStatus = oldStatus;
      updaterAPI.install = oldInstall;
      setDesktopUpdater(saved);
    }
  });

  it("immediately enters preparing state on install click and stays active while Sparkle runs asynchronously", async () => {
    const oldStatus = updaterAPI.nativeStatus;
    const oldInstall = updaterAPI.install;
    const saved = { ...desktopUpdater };
    let observedDuringInstall: string | undefined;
    try {
      setDesktopUpdater({
        nativeAvailable: true,
        isInstalling: false,
        draining: false,
        progress: null,
        error: null,
      });
      updaterAPI.install = async () => {
        observedDuringInstall = desktopUpdater.progress?.phase;
        return { started: true };
      };
      updaterAPI.nativeStatus = async () => ({
        available: true,
        draining: false,
        progress: { phase: "preparing", downloadedBytes: 0, totalBytes: 0 },
      });
      await startDesktopUpdate();
      assertEqual(observedDuringInstall, "preparing");
      assertEqual(desktopUpdater.progress?.phase, "preparing");
      assertEqual(isDesktopUpdateInstalling(), true);
    } finally {
      updaterAPI.nativeStatus = oldStatus;
      updaterAPI.install = oldInstall;
      setDesktopUpdater(saved);
    }
  });

  it("surfaces a failure while entering the preparing state instead of silently skipping the install", async () => {
    // Regression: rendering the progress bar threw in WebKit, the click handler
    // swallowed it, and no install request was ever sent.
    const oldStatus = updaterAPI.nativeStatus;
    const oldInstall = updaterAPI.install;
    const oldSetInterval = globalThis.setInterval;
    const saved = { ...desktopUpdater };
    let installs = 0;
    try {
      setDesktopUpdater({ nativeAvailable: true, isInstalling: false, draining: false, progress: null, error: null });
      updaterAPI.install = async () => { installs++; return { started: true }; };
      updaterAPI.nativeStatus = async () => ({ available: true, draining: false });
      globalThis.setInterval = (() => {
        throw new TypeError("The provided value is non-finite");
      }) as unknown as typeof setInterval;
      await startDesktopUpdate();
      assertEqual(installs, 0);
      assertEqual(desktopUpdater.error, "The provided value is non-finite");
      assertEqual(desktopUpdater.progress, null);
      assertEqual(isDesktopUpdateInstalling(), false);
    } finally {
      globalThis.setInterval = oldSetInterval;
      updaterAPI.nativeStatus = oldStatus;
      updaterAPI.install = oldInstall;
      setDesktopUpdater(saved);
    }
  });

  it("discards a late poll that would overwrite a download failure", async () => {
    const oldStatus = updaterAPI.nativeStatus;
    const saved = { ...desktopUpdater };
    let resolveOld!: (status: NativeUpdateStatus) => void;
    try {
      updaterAPI.nativeStatus = () => new Promise((resolve) => { resolveOld = resolve; });
      const older = refreshDesktopUpdateStatus();
      updaterAPI.nativeStatus = async () => ({
        available: true, draining: false,
        progress: { phase: "failed", downloadedBytes: 0, totalBytes: 0, error: "download timed out" },
      });
      await refreshDesktopUpdateStatus();
      resolveOld({ available: true, draining: true, progress: { phase: "downloading", downloadedBytes: 10, totalBytes: 100 } });
      await older;
      assertEqual(desktopUpdater.progress?.phase, "failed");
      assertEqual(desktopUpdater.draining, false);
      assertEqual(desktopUpdater.error, "download timed out");
      assertEqual(isDesktopUpdateInstalling(), false);
    } finally {
      updaterAPI.nativeStatus = oldStatus;
      setDesktopUpdater(saved);
    }
  });

  it("tracks download speed, ETA, clear phase details and slow-download detection", () => {
    const saved = { ...desktopUpdater };
    try {
      resetUpdateDownloadMetrics();
      const s0 = recordUpdateDownloadSample(
        { phase: "downloading", downloadedBytes: 0, totalBytes: 0 },
        1_000
      );
      assertEqual(s0.speedBps, 0);
      assertEqual(s0.slow, false);
      setDesktopUpdater({
        progress: { phase: "downloading", downloadedBytes: 0, totalBytes: 0 },
        downloadSpeedBps: s0.speedBps,
        downloadSlow: s0.slow,
      });
      assertEqual(desktopUpdateStatusLabel().length > 0, true);
      assertEqual(desktopUpdateDetailLabel().includes("MB"), false);

      // Stuck connecting past UPDATE_SLOW_CONNECT_MS flags slow=true
      const sSlowConnect = recordUpdateDownloadSample(
        { phase: "downloading", downloadedBytes: 0, totalBytes: 10 * 1048576 },
        1_000 + UPDATE_SLOW_CONNECT_MS + 100
      );
      assertEqual(sSlowConnect.slow, true);

      // Fast transfer (2 MB in 1s) clears slow flag and computes speed + ETA
      const sFast = recordUpdateDownloadSample(
        { phase: "downloading", downloadedBytes: 2 * 1048576, totalBytes: 10 * 1048576 },
        1_000 + UPDATE_SLOW_CONNECT_MS + 1_100
      );
      assertEqual(sFast.slow, false);
      assertEqual(formatUpdateSpeed(sFast.speedBps), "2.0 MB/s");
      setDesktopUpdater({
        progress: { phase: "downloading", downloadedBytes: 2 * 1048576, totalBytes: 10 * 1048576 },
        downloadSpeedBps: sFast.speedBps,
        downloadSlow: sFast.slow,
      });
      assertEqual(desktopUpdateDetailLabel(), "2.0 MB / 10.0 MB");
      assertEqual(desktopUpdateSpeedEtaLabel().includes("2.0 MB/s"), true);

      // Slow transfer (< 150 KB/s after UPDATE_SLOW_SPEED_MS) flags slow=true
      resetUpdateDownloadMetrics();
      recordUpdateDownloadSample(
        { phase: "downloading", downloadedBytes: 0, totalBytes: 20 * 1048576 },
        1_000
      );
      const sSlowRate = recordUpdateDownloadSample(
        { phase: "downloading", downloadedBytes: 200 * 1024, totalBytes: 20 * 1048576 },
        1_000 + UPDATE_SLOW_SPEED_MS + 1_000
      );
      assertEqual(sSlowRate.slow, true);
    } finally {
      resetUpdateDownloadMetrics();
      setDesktopUpdater(saved);
    }
  });
});

describe("desktop update notification policy", () => {
  it("notifies once per version only while the window is unattended", () => {
    assertEqual(shouldNotifyUpdate(true, "0.3.0", "", false, true), true);
    assertEqual(shouldNotifyUpdate(true, "0.3.0", "0.3.0", false, true), false);
    assertEqual(shouldNotifyUpdate(true, "0.3.1", "0.3.0", false, true), true);
    assertEqual(shouldNotifyUpdate(true, "0.3.0", "", true, true), false);
  });

  it("stays silent without an update, without a version or when notifications are off", () => {
    assertEqual(shouldNotifyUpdate(false, "0.3.0", "", false, true), false);
    assertEqual(shouldNotifyUpdate(true, "   ", "", false, true), false);
    assertEqual(shouldNotifyUpdate(true, "0.3.0", "", false, false), false);
  });

  it("defers self-update and surfaces upgradeCommand when managed by Homebrew/Scoop/WinGet", async () => {
    const oldStatus = updaterAPI.nativeStatus;
    const oldInstall = updaterAPI.install;
    const saved = { ...desktopUpdater };
    let installCalls = 0;
    try {
      updaterAPI.nativeStatus = async () => ({
        available: false,
        draining: false,
        managedBy: "Homebrew Cask",
        upgradeCommand: "brew upgrade --cask hoxi",
      });
      updaterAPI.install = async () => {
        installCalls++;
        return { started: true };
      };
      await refreshDesktopUpdateStatus();
      assertEqual(desktopUpdater.nativeAvailable, false);
      assertEqual(desktopUpdater.managedBy, "Homebrew Cask");
      assertEqual(desktopUpdater.upgradeCommand, "brew upgrade --cask hoxi");
      await startDesktopUpdate();
      assertEqual(installCalls, 0);
    } finally {
      updaterAPI.nativeStatus = oldStatus;
      updaterAPI.install = oldInstall;
      setDesktopUpdater(saved);
    }
  });

  it("delegates CLI symlink status, install, and uninstall through store actions", async () => {
    const oldGet = cliLinkAPI.getStatus;
    const oldInstall = cliLinkAPI.install;
    const oldUninstall = cliLinkAPI.uninstall;
    const calls: string[] = [];
    try {
      cliLinkAPI.getStatus = async () => {
        calls.push("get");
        return { installed: false, current: false, inPath: false, suggestedDir: "/usr/local/bin" };
      };
      cliLinkAPI.install = async () => {
        calls.push("install");
        return { installed: true, current: true, inPath: true, linkPath: "/usr/local/bin/hoxi" };
      };
      cliLinkAPI.uninstall = async () => {
        calls.push("uninstall");
        return { installed: false, current: false, inPath: false };
      };
      const st0 = await fetchCliLinkStatus();
      assertEqual(st0.installed, false);
      const st1 = await installCliLink();
      assertEqual(st1.installed && st1.current, true);
      const st2 = await uninstallCliLink();
      assertEqual(st2.installed, false);
      assertEqual(calls.join(","), "get,install,uninstall");
    } finally {
      cliLinkAPI.getStatus = oldGet;
      cliLinkAPI.install = oldInstall;
      cliLinkAPI.uninstall = oldUninstall;
    }
  });

  it("resolves CDN and GitHub mirrors and forwards explicit download source options", async () => {
    const oldStatus = updaterAPI.nativeStatus;
    const oldInstall = updaterAPI.install;
    const saved = { ...desktopUpdater };
    const installCalls: Array<{ source?: string; useSelfUpdater?: boolean } | undefined> = [];
    try {
      const sample = {
        currentVersion: "0.7.2",
        latestVersion: "0.7.3",
        versionCode: 7003,
        needUpdate: true,
        title: "Hoxi v0.7.3",
        releaseNotes: "notes",
        downloadUrl: "https://cdn.hoxi.dev/hoxi-desktop/v0.7.3/Hoxi-0.7.3-darwin-arm64.dmg",
        mirrors: {
          cdn: "https://cdn.hoxi.dev/hoxi-desktop/v0.7.3/Hoxi-0.7.3-darwin-arm64.dmg",
          global: "https://github.com/hoxiai/hoxi/releases/download/v0.7.3/Hoxi-0.7.3-darwin-arm64.dmg",
        },
        mandatory: false,
        checkedAt: "2026-11-22T00:00:00Z",
      };
      assertEqual(getAcceleratedUpdateMirrorUrl(sample), sample.mirrors.cdn);
      assertEqual(getGitHubUpdateMirrorUrl(sample), sample.mirrors.global);
      assertEqual(
        getAcceleratedUpdateMirrorUrl({
          ...sample,
          mirrors: { ...sample.mirrors, cn: "https://dl.example.cn/hoxi.dmg" },
        }),
        "https://dl.example.cn/hoxi.dmg"
      );

      updaterAPI.nativeStatus = async () => ({ available: true, draining: false });
      updaterAPI.install = async (opts) => {
        installCalls.push(opts);
        return { started: true };
      };
      setDesktopUpdater({ nativeAvailable: true, isInstalling: false, progress: null, error: null });
      setDesktopUpdateSource("cdn");
      await startDesktopUpdate();
      setDesktopUpdater({ isInstalling: false, progress: null, error: null });
      await startDesktopUpdate("global", true);
      assertEqual(installCalls.length, 2);
      assertEqual(installCalls[0]?.source, "cdn");
      assertEqual(installCalls[0]?.useSelfUpdater, undefined);
      assertEqual(installCalls[1]?.source, "global");
      assertEqual(installCalls[1]?.useSelfUpdater, true);
    } finally {
      updaterAPI.nativeStatus = oldStatus;
      updaterAPI.install = oldInstall;
      setDesktopUpdater(saved);
    }
  });
});
