// web/src/components/layout/UpdateWatcher.tsx
import { onCleanup, onMount } from "solid-js";
import { checkDesktopUpdate, refreshDesktopUpdateStatus } from "../../store/workspace";

const STATUS_POLL_MS = 5_000;

/**
 * Headless host for desktop update state: one startup check, then a light poll
 * of the native (Sparkle) admission status so `draining` stays truthful.
 */
export default function UpdateWatcher() {
  onMount(() => {
    void checkDesktopUpdate(false);
    void refreshDesktopUpdateStatus();
    const statusTimer = window.setInterval(() => void refreshDesktopUpdateStatus(), STATUS_POLL_MS);
    onCleanup(() => window.clearInterval(statusTimer));
  });

  return null;
}
