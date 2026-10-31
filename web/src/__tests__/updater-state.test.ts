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
  isDesktopUpdateInstalling,
  isSelfUpdateActive,
  recordUpdateDownloadSample,
  refreshDesktopUpdateStatus,
  resetUpdateDownloadMetrics,
  setDesktopUpdater,
  startDesktopUpdate,
} from "../store/updater-state";
import { updaterAPI, type NativeUpdateStatus } from "../services/platform-api";
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
});
