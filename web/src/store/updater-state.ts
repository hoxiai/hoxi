// web/src/store/updater-state.ts
import { createSignal } from "solid-js";
import { createStore } from "solid-js/store";
import { updaterAPI, type UpdateCheckResult } from "../services/api";
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
}

export const [desktopUpdater, setDesktopUpdater] = createStore<DesktopUpdaterState>({
  isChecking: false,
  isInstalling: false,
  nativeAvailable: false,
  draining: false,
  hasChecked: false,
  result: null,
  error: null,
});

/** Minimum spacing between successful checks; startup, focus and timers all share it. */
export const UPDATE_CHECK_MIN_GAP_MS = 10 * 60_000;

const NOTIFIED_VERSION_KEY = "hoxi:update-notified-version";

let lastSuccessfulCheckAt = 0;
let inFlightCheck: Promise<UpdateCheckResult | null> | null = null;

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
  try {
    const status = await updaterAPI.nativeStatus();
    setDesktopUpdater({ nativeAvailable: status.available, draining: status.draining });
  } catch {
    // Keep the last known status during startup or a temporary disconnect.
  }
}

export async function startDesktopUpdate(): Promise<void> {
  if (desktopUpdater.isInstalling || !desktopUpdater.nativeAvailable) return;

  setDesktopUpdater({ isInstalling: true, error: null });
  try {
    await updaterAPI.install();
    await refreshDesktopUpdateStatus();
  } catch (err: unknown) {
    setDesktopUpdater("error", err instanceof Error ? err.message : t("header.updateInstallFailed"));
  } finally {
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
