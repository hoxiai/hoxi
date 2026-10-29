// web/src/components/layout/UpdateWatcher.tsx
import { onCleanup, onMount } from "solid-js";
import { checkDesktopUpdate, isDesktopUpdateInstalling, refreshDesktopUpdateStatus } from "../../store/workspace";

const STATUS_POLL_MS = 5_000;
const INSTALL_POLL_MS = 1_000;
const CHECK_INTERVAL_MS = 30 * 60_000;

/**
 * Headless host for desktop update state: startup check, then a throttled
 * re-check on a timer and whenever the window regains focus, becomes visible
 * or the network comes back. A light poll keeps Sparkle's `draining` truthful.
 */
export default function UpdateWatcher() {
  onMount(() => {
    const recheck = () => void checkDesktopUpdate(false);
    const recheckOnWake = () => {
      if (typeof document !== "undefined" && document.hidden) return;
      recheck();
    };

    void checkDesktopUpdate(false);

    // Self-schedule so the poll speeds up while the self-updater downloads.
    let statusTimer: number | undefined;
    let disposed = false;
    const pollStatus = async () => {
      await refreshDesktopUpdateStatus();
      if (disposed) return;
      const delay = isDesktopUpdateInstalling() ? INSTALL_POLL_MS : STATUS_POLL_MS;
      statusTimer = window.setTimeout(() => void pollStatus(), delay);
    };
    void pollStatus();

    const checkTimer = window.setInterval(recheck, CHECK_INTERVAL_MS);

    window.addEventListener("focus", recheck);
    window.addEventListener("online", recheck);
    document.addEventListener("visibilitychange", recheckOnWake);

    onCleanup(() => {
      disposed = true;
      window.clearTimeout(statusTimer);
      window.clearInterval(checkTimer);
      window.removeEventListener("focus", recheck);
      window.removeEventListener("online", recheck);
      document.removeEventListener("visibilitychange", recheckOnWake);
    });
  });

  return null;
}
