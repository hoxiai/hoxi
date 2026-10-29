// web/src/store/updater-state.ts
import { createSignal } from "solid-js";
import { createStore } from "solid-js/store";
import { updaterAPI, type NativeUpdateProgress, type UpdateCheckResult } from "../services/api";
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
});

/** Minimum spacing between successful checks; startup, focus and timers all share it. */
export const UPDATE_CHECK_MIN_GAP_MS = 10 * 60_000;

const NOTIFIED_VERSION_KEY = "hoxi:update-notified-version";

let lastSuccessfulCheckAt = 0;
let inFlightCheck: Promise<UpdateCheckResult | null> | null = null;
let statusGeneration = 0;

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
    case "downloading": return t("header.updateDownloading");
    case "verifying": return t("header.updateVerifying");
    case "staging": return t("header.updateStaging");
    case "restarting": return t("header.updateRestarting");
    default: return t("header.updateOpening");
  }
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
    setDesktopUpdater({
      nativeAvailable: status.available,
      draining: status.draining,
      progress: status.progress ?? null,
      ...(status.progress?.phase === "failed" ? { error: status.progress.error || t("header.updateInstallFailed") } : {}),
    });
  } catch {
    // Keep the last known status during startup or a temporary disconnect.
  }
}

export async function startDesktopUpdate(): Promise<void> {
  if (isDesktopUpdateInstalling() || !desktopUpdater.nativeAvailable) return;

  ++statusGeneration;
  setDesktopUpdater({ isInstalling: true, error: null, progress: null });
  try {
    await updaterAPI.install();
  } catch (err: unknown) {
    setDesktopUpdater("error", err instanceof Error ? err.message : t("header.updateInstallFailed"));
  } finally {
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
