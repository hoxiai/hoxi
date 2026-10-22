// web/src/components/layout/UpdateWatcher.tsx
import { onCleanup, onMount } from "solid-js";
import { checkDesktopUpdate, refreshDesktopUpdateStatus } from "../../store/workspace";

const STATUS_POLL_MS = 5_000;
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
    void refreshDesktopUpdateStatus();

    const statusTimer = window.setInterval(() => void refreshDesktopUpdateStatus(), STATUS_POLL_MS);
    const checkTimer = window.setInterval(recheck, CHECK_INTERVAL_MS);

    window.addEventListener("focus", recheck);
    window.addEventListener("online", recheck);
    document.addEventListener("visibilitychange", recheckOnWake);

    onCleanup(() => {
      window.clearInterval(statusTimer);
      window.clearInterval(checkTimer);
      window.removeEventListener("focus", recheck);
      window.removeEventListener("online", recheck);
      document.removeEventListener("visibilitychange", recheckOnWake);
    });
  });

  return null;
}
