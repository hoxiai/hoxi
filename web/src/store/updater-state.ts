// web/src/store/updater-state.ts
import { createStore } from "solid-js/store";
import { updaterAPI, type UpdateCheckResult } from "../services/api";

export interface DesktopUpdaterState {
  isChecking: boolean;
  hasChecked: boolean;
  result: UpdateCheckResult | null;
  error: string | null;
}

export const [desktopUpdater, setDesktopUpdater] = createStore<DesktopUpdaterState>({
  isChecking: false,
  hasChecked: false,
  result: null,
  error: null,
});

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
      error: err?.message || "Failed to check update",
    });
    return null;
  }
}
