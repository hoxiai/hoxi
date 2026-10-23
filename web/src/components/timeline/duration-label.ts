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
