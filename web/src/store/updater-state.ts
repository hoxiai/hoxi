// web/src/store/updater-state.ts
import { createSignal } from "solid-js";
import { createStore } from "solid-js/store";
import { updaterAPI, type NativeUpdateProgress, type UpdateCheckResult } from "../services/api";
import { errorMessageOf } from "../services/http";
import { t } from "../i18n";
import { appSettings, sendDesktopNotification } from "./settings";

export interface DesktopUpdaterState {
  isChecking: boolean;
  isInstalling: boolean;
  nativeAvailable: boolean;
  draining: boolean;
  hasChecked: boolean;
  result: UpdateCheckResult | null;
  error: string | null;
  progress: NativeUpdateProgress | null;
  downloadSpeedBps: number;
  downloadSlow: boolean;
}

export const [desktopUpdater, setDesktopUpdater] = createStore<DesktopUpdaterState>({
  isChecking: false,
  isInstalling: false,
  nativeAvailable: false,
  draining: false,
  hasChecked: false,
  result: null,
  error: null,
  progress: null,
  downloadSpeedBps: 0,
  downloadSlow: false,
});

/** Minimum spacing between successful checks; startup, focus and timers all share it. */
export const UPDATE_CHECK_MIN_GAP_MS = 10 * 60_000;
export const UPDATE_SLOW_CONNECT_MS = 8_000;
export const UPDATE_SLOW_SPEED_MS = 10_000;
export const UPDATE_SLOW_SPEED_BPS = 150 * 1024;
const INSTALL_ACTIVE_POLL_MS = 500;

const NOTIFIED_VERSION_KEY = "hoxi:update-notified-version";

let lastSuccessfulCheckAt = 0;
let inFlightCheck: Promise<UpdateCheckResult | null> | null = null;
let statusGeneration = 0;
let downloadStartedAtMs = 0;
let lastSampleAtMs = 0;
let lastSampleBytes = 0;
let smoothedSpeedBps = 0;

export function resetUpdateDownloadMetrics(): void {
  downloadStartedAtMs = 0;
  lastSampleAtMs = 0;
  lastSampleBytes = 0;
  smoothedSpeedBps = 0;
}

export function recordUpdateDownloadSample(
  progress: NativeUpdateProgress | null,
  nowMs = Date.now()
): { speedBps: number; slow: boolean } {
  if (!progress || (progress.phase !== "preparing" && progress.phase !== "downloading")) {
    resetUpdateDownloadMetrics();
    return { speedBps: 0, slow: false };
  }
  const curBytes = Math.max(0, progress.downloadedBytes || 0);
  if (downloadStartedAtMs <= 0 || curBytes < lastSampleBytes) {
    downloadStartedAtMs = nowMs;
    lastSampleAtMs = nowMs;
    lastSampleBytes = curBytes;
    smoothedSpeedBps = 0;
  } else if (progress.phase === "downloading") {
    const dBytes = curBytes - lastSampleBytes;
    const dtMs = nowMs - lastSampleAtMs;
    if (dBytes > 0 && dtMs > 0) {
      const instantBps = Math.round((dBytes * 1000) / dtMs);
      smoothedSpeedBps =
        smoothedSpeedBps > 0
          ? Math.round(smoothedSpeedBps * 0.4 + instantBps * 0.6)
          : instantBps;
      lastSampleAtMs = nowMs;
      lastSampleBytes = curBytes;
    } else if (dtMs >= 3_000 && dBytes === 0) {
      smoothedSpeedBps = smoothedSpeedBps >= 2048 ? Math.round(smoothedSpeedBps * 0.5) : 0;
      lastSampleAtMs = nowMs;
    }
  }
  const elapsedMs = Math.max(0, nowMs - downloadStartedAtMs);
  const slowConnect = curBytes <= 0 && elapsedMs >= UPDATE_SLOW_CONNECT_MS;
  const slowTransfer =
    curBytes > 0 && elapsedMs >= UPDATE_SLOW_SPEED_MS && smoothedSpeedBps < UPDATE_SLOW_SPEED_BPS;
  return { speedBps: smoothedSpeedBps, slow: slowConnect || slowTransfer };
}

export function formatUpdateBytesMB(bytes: number): string {
  return `${(Math.max(0, bytes || 0) / 1048576).toFixed(1)} MB`;
}

export function formatUpdateSpeed(bps: number): string {
  if (!Number.isFinite(bps) || bps <= 0) return "";
  if (bps >= 1048576) return `${(bps / 1048576).toFixed(1)} MB/s`;
  return `${Math.max(1, Math.round(bps / 1024))} KB/s`;
}

export function isSelfUpdateActive(progress: NativeUpdateProgress | null): boolean {
  return Boolean(progress && progress.phase !== "failed");
}

/** True from the install click until the self-updater fails or Hoxi restarts, surviving page reloads. */
export function isDesktopUpdateInstalling(): boolean {
  return desktopUpdater.isInstalling || isSelfUpdateActive(desktopUpdater.progress);
}

/** Label for install buttons: the current phase, plus the download percentage when known. */
export function desktopUpdateButtonLabel(): string {
  const percent = desktopUpdateProgressPercent(desktopUpdater.progress);
  const label = desktopUpdateStatusLabel();
  return percent === undefined ? label : `${label} ${percent}%`;
}

export function desktopUpdateProgressPercent(progress: NativeUpdateProgress | null): number | undefined {
  if (!progress || progress.phase !== "downloading" || progress.totalBytes <= 0) return undefined;
  return Math.max(0, Math.min(100, Math.floor(progress.downloadedBytes / progress.totalBytes * 100)));
}

export function desktopUpdateStatusLabel(): string {
  switch (desktopUpdater.progress?.phase) {
    case "preparing": return t("header.updatePreparing");
    case "downloading":
      if ((desktopUpdater.progress.downloadedBytes || 0) <= 0 && (desktopUpdater.progress.totalBytes || 0) <= 0) {
        return t("header.updateConnecting");
      }
      return t("header.updateDownloading");
    case "verifying": return t("header.updateVerifying");
    case "staging": return t("header.updateStaging");
    case "restarting": return t("header.updateRestarting");
    default: return t("header.updateOpening");
  }
}

export function desktopUpdateDetailLabel(): string {
  switch (desktopUpdater.progress?.phase) {
    case "preparing":
      return t("header.updatePreparingDetail");
    case "downloading": {
      const downloaded = desktopUpdater.progress.downloadedBytes || 0;
      const total = desktopUpdater.progress.totalBytes || 0;
      if (downloaded <= 0) return t("header.updateConnectingDetail");
      return total > 0
        ? `${formatUpdateBytesMB(downloaded)} / ${formatUpdateBytesMB(total)}`
        : formatUpdateBytesMB(downloaded);
    }
    case "verifying":
      return t("header.updateVerifyingDetail");
    case "staging":
      return t("header.updateStagingDetail");
    case "restarting":
      return t("header.updateRestartingDetail");
    default:
      return t("header.updateOpeningDetail");
  }
}

export function desktopUpdateSpeedEtaLabel(): string {
  if (desktopUpdater.progress?.phase !== "downloading" || desktopUpdater.downloadSpeedBps <= 0) {
    return "";
  }
  const speed = formatUpdateSpeed(desktopUpdater.downloadSpeedBps);
  if (!speed) return "";
  const downloaded = desktopUpdater.progress.downloadedBytes || 0;
  const total = desktopUpdater.progress.totalBytes || 0;
  if (total > downloaded) {
    const remainingSec = Math.max(1, Math.ceil((total - downloaded) / desktopUpdater.downloadSpeedBps));
    if (remainingSec < 60) {
      return t("header.updateSpeedEtaSeconds", { speed, seconds: remainingSec });
    }
    return t("header.updateSpeedEtaMinutes", { speed, minutes: Math.ceil(remainingSec / 60) });
  }
  return speed;
}

function readNotifiedVersion(): string {
  try {
    return localStorage.getItem(NOTIFIED_VERSION_KEY) || "";
  } catch {
    return "";
  }
}

function rememberNotifiedVersion(version: string): void {
  try {
    localStorage.setItem(NOTIFIED_VERSION_KEY, version);
  } catch {
    /* storage unavailable */
  }
}

/** True when an automatic re-check is worth the request. */
export function shouldAutoCheckUpdate(
  nowMs: number,
  lastSuccessMs: number,
  checking: boolean
): boolean {
  if (checking) return false;
  if (lastSuccessMs <= 0) return true;
  return nowMs - lastSuccessMs >= UPDATE_CHECK_MIN_GAP_MS;
}

/** Notify once per published version, and only while the window is unattended. */
export function shouldNotifyUpdate(
  needUpdate: boolean,
  latestVersion: string,
  notifiedVersion: string,
  focused: boolean,
  notificationsEnabled: boolean
): boolean {
  if (!needUpdate || !notificationsEnabled) return false;
  if (focused) return false;
  const version = latestVersion.trim();
  if (!version) return false;
  return version !== notifiedVersion;
}

function isWindowFocused(): boolean {
  return typeof document !== "undefined" && document.hasFocus();
}

function notifyDesktopUpdateIfNeeded(res: UpdateCheckResult): void {
  if (
    !shouldNotifyUpdate(
      res.needUpdate,
      res.latestVersion,
      readNotifiedVersion(),
      isWindowFocused(),
      appSettings.desktopNotifications
    )
  ) {
    return;
  }
  rememberNotifiedVersion(res.latestVersion.trim());
  sendDesktopNotification({
    title: t("header.updateNotificationTitle"),
    body: t("header.updateNotificationBody", { version: `v${res.latestVersion.trim()}` }),
  });
}

export async function refreshDesktopUpdateStatus(): Promise<void> {
  const generation = ++statusGeneration;
  try {
    const status = await updaterAPI.nativeStatus();
    if (generation !== statusGeneration) return;
    const nextProgress = status.progress ?? null;
    const metrics = recordUpdateDownloadSample(nextProgress);
    setDesktopUpdater({
      nativeAvailable: status.available,
      draining: status.draining,
      progress: nextProgress,
      downloadSpeedBps: metrics.speedBps,
      downloadSlow: metrics.slow,
      ...(nextProgress?.phase === "failed" ? { error: nextProgress.error || t("header.updateInstallFailed") } : {}),
    });
  } catch {
    // Keep the last known status during startup or a temporary disconnect.
  }
}

export async function startDesktopUpdate(): Promise<void> {
  if (isDesktopUpdateInstalling() || !desktopUpdater.nativeAvailable) return;

  ++statusGeneration;
  resetUpdateDownloadMetrics();
  setDesktopUpdater({
    isInstalling: true,
    error: null,
    progress: null,
    downloadSpeedBps: 0,
    downloadSlow: false,
  });
  const earlyPoll = setTimeout(() => void refreshDesktopUpdateStatus(), 120);
  const activePoll = setInterval(() => void refreshDesktopUpdateStatus(), INSTALL_ACTIVE_POLL_MS);
  (earlyPoll as { unref?: () => void }).unref?.();
  (activePoll as { unref?: () => void }).unref?.();
  try {
    await updaterAPI.install();
  } catch (err: unknown) {
    setDesktopUpdater("error", errorMessageOf(err, t("header.updateInstallFailed")));
  } finally {
    clearTimeout(earlyPoll);
    clearInterval(activePoll);
    await refreshDesktopUpdateStatus();
    setDesktopUpdater("isInstalling", false);
  }
}

async function runUpdateCheck(): Promise<UpdateCheckResult | null> {
  setDesktopUpdater("isChecking", true);
  void refreshDesktopUpdateStatus();

  try {
    const res = await updaterAPI.checkUpdate();
    lastSuccessfulCheckAt = Date.now();
    setDesktopUpdater({
      isChecking: false,
      hasChecked: true,
      result: res,
      error: null,
    });
    notifyDesktopUpdateIfNeeded(res);
    return res;
  } catch (err: unknown) {
    // A transient background failure must not replace a known-good result.
    const message =
      err instanceof Error ? err.message : t("header.updateCheckFailed");
    setDesktopUpdater({
      isChecking: false,
      hasChecked: true,
      error: desktopUpdater.result ? null : message,
    });
    return null;
  }
}

export async function checkDesktopUpdate(force = false): Promise<UpdateCheckResult | null> {
  if (inFlightCheck) return inFlightCheck;
  if (!force && !shouldAutoCheckUpdate(Date.now(), lastSuccessfulCheckAt, desktopUpdater.isChecking)) {
    return desktopUpdater.result;
  }

  const task = runUpdateCheck();
  inFlightCheck = task;
  try {
    return await task;
  } finally {
    if (inFlightCheck === task) inFlightCheck = null;
  }
}

const [updateModalOpen, setUpdateModalOpen] = createSignal(false);

export function isUpdateModalOpen(): boolean {
  return updateModalOpen();
}

export function openDesktopUpdateModal(): void {
  setUpdateModalOpen(true);
}

export function closeDesktopUpdateModal(): void {
  setUpdateModalOpen(false);
}

/** True while a release newer than the running build is published. */
export function hasDesktopUpdate(): boolean {
  return Boolean(desktopUpdater.result?.needUpdate);
}

/** True once Sparkle accepted the install; new mutations stay paused until relaunch. */
export function isUpdateDraining(): boolean {
  return desktopUpdater.draining;
}
