// Compact elapsed labels for the task page: sub-minute stays tenths of a second,
// anything longer folds into minutes so a running task does not read as 123.4s.

export function formatElapsedSeconds(sec: number): string {
  if (!Number.isFinite(sec) || sec < 60) return `${Math.max(0, sec).toFixed(1)}s`;
  const total = Math.round(sec);
  const minutes = Math.floor(total / 60);
  const seconds = total % 60;
  if (minutes < 60) return `${minutes}m ${seconds}s`;
  const hours = Math.floor(minutes / 60);
  return `${hours}h ${minutes % 60}m`;
}

/**
 * Starts a timer that ticks every 100ms while elapsed time is under 60s
 * (where tenths of a second are shown) and automatically drops to 1000ms
 * once elapsed time reaches 60s (where only whole seconds/minutes are shown).
 */
export function startAdaptiveElapsedTimer(
  getStartMs: () => number | undefined,
  onTick: (nowMs: number) => void
): () => void {
  let timer: ReturnType<typeof setTimeout> | undefined;
  let cancelled = false;
  const step = () => {
    if (cancelled) return;
    const now = Date.now();
    onTick(now);
    const start = getStartMs();
    const elapsedMs = start && start > 0 ? now - start : 0;
    const delay = elapsedMs >= 60_000 ? 1000 : 100;
    timer = setTimeout(step, delay);
  };
  step();
  return () => {
    cancelled = true;
    if (timer !== undefined) clearTimeout(timer);
  };
}
