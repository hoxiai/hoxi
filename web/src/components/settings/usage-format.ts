// web/src/components/settings/usage-format.ts
// Re-exports the shared locale-aware formatters so the usage panel and the
// header pulse always agree on units (万/亿 in Chinese, k/M/B in English).

export { formatCompactNumber as formatCompactTokens, formatUsd } from "../../i18n/format";

/** `2026-10-22` → `10-22`; keeps the axis compact without losing the year in tooltips. */
export function shortDayLabel(date: string): string {
  const parts = date.split("-");
  return parts.length === 3 ? `${parts[1]}-${parts[2]}` : date;
}
