// web/src/store/updater-state.ts
import { createStore } from "solid-js/store";
import { updaterAPI, type UpdateCheckResult } from "../services/api";
import { t } from "../i18n";

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

export async function checkDesktopUpdate(force = false): Promise<UpdateCheckResult | null> {
  if (desktopUpdater.isChecking && !force) {
    return desktopUpdater.result;
  }

  setDesktopUpdater({ isChecking: true, error: null });

  try {
    const res = await updaterAPI.checkUpdate();
    setDesktopUpdater({
      isChecking: false,
      hasChecked: true,
      result: res,
      error: null,
    });
    return res;
  } catch (err: any) {
    setDesktopUpdater({
      isChecking: false,
      hasChecked: true,
      error: err?.message || t("header.updateCheckFailed"),
    });
    return null;
  }
}
