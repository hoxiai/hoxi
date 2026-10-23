// web/src/components/settings/usage-format.ts
// Re-exports the shared locale-aware formatters so the usage panel and the
// header pulse always agree on units (万/亿 in Chinese, k/M/B in English).

export { formatCompactNumber as formatCompactTokens, formatUsd } from "../../i18n/format";

/** `2026-10-22` → `10-22`; keeps the axis compact without losing the year in tooltips. */
export function shortDayLabel(date: string): string {
  const parts = date.split("-");
  return parts.length === 3 ? `${parts[1]}-${parts[2]}` : date;
}

/** A duration in milliseconds as a compact human string (`840ms`, `8.4s`, `2m10s`). */
export function formatDurationMs(ms: number): string {
  if (!ms || ms <= 0) return "—";
  if (ms < 1000) return `${Math.round(ms)}ms`;
  if (ms < 60_000) return `${(ms / 1000).toFixed(1)}s`;
  const minutes = Math.floor(ms / 60_000);
  const seconds = Math.round((ms % 60_000) / 1000);
  return `${minutes}m${String(seconds).padStart(2, "0")}s`;
}
