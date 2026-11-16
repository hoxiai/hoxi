// web/src/components/settings/usage-format.ts
// Re-exports the shared locale-aware formatters so the usage panel and the
// header pulse always agree on units (万/亿 in Chinese, k/M/B in English).
import { t } from "../../i18n";

export { formatCompactNumber as formatCompactTokens, formatUsd } from "../../i18n/format";

/** `2026-10-22` → `10-22`; keeps the axis compact without losing the year in tooltips. */
export function shortDayLabel(date: string): string {
  const parts = date.split("-");
  return parts.length === 3 ? `${parts[1]}-${parts[2]}` : date;
}

/** A duration in milliseconds as a compact human string (`840ms`, `8.4s`, `2m10s`). */
export function formatDurationMs(ms: number): string {
  if (!ms || ms <= 0) return "—";
  if (ms < 1000) return t("diagnostics.durationMilliseconds", { milliseconds: Math.round(ms) });
  const totalSeconds = Math.round(ms / 1000);
  if (totalSeconds < 60) return t("diagnostics.durationSeconds", { seconds: totalSeconds });
  return t("diagnostics.durationMinutes", {
    minutes: Math.floor(totalSeconds / 60),
    seconds: totalSeconds % 60,
  });
}
