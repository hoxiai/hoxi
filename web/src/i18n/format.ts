// web/src/i18n/format.ts
// Locale-aware compact number formatting.
//
// Token counts are the one place where the unit itself changes per language:
// Chinese counts in 万 (10^4) and 亿 (10^8), English in k/M/B. Keeping the
// scale here means every counter reads naturally in its own language instead
// of showing `1281.36M` to a Chinese user.
import { locale, t } from "./index";

export interface CompactNumberOptions {
  /** Upper bound on decimal places; the scale rule may ask for fewer. */
  maxDecimals?: number;
}

interface UnitStep {
  value: number;
  label: string;
}

function steps(): UnitStep[] {
  if (locale() === "zh-CN") {
    // 万亿 must be tested before 亿: 1e12 >= 1e8 as well.
    return [
      { value: 1e12, label: t("common.unitWanYi") },
      { value: 1e8, label: t("common.unitYi") },
      { value: 1e4, label: t("common.unitWan") },
    ];
  }
  return [
    { value: 1e12, label: t("common.unitT") },
    { value: 1e9, label: t("common.unitB") },
    { value: 1e6, label: t("common.unitM") },
    { value: 1e3, label: t("common.unitK") },
  ];
}

/**
 * Two decimals below 100 keeps 12.81亿 readable; above that a fraction of a
 * unit is noise (3093亿, not 3092.90亿).
 */
function decimalsFor(scaled: number, maxDecimals: number): number {
  const wanted = scaled < 100 ? 2 : 0;
  return Math.max(0, Math.min(wanted, maxDecimals));
}

function trimZeros(text: string): string {
  return text.includes(".") ? text.replace(/\.?0+$/, "") : text;
}

/**
 * Formats a count with the compact unit used by the active locale.
 * `1281360000` → `12.81亿` (zh-CN) or `1.28B` (en-US).
 */
export function formatCompactNumber(value: number, opts: CompactNumberOptions = {}): string {
  if (!Number.isFinite(value) || value <= 0) return "0";
  const maxDecimals = opts.maxDecimals ?? 2;
  const abs = Math.abs(value);
  for (const step of steps()) {
    if (abs < step.value) continue;
    const scaled = abs / step.value;
    return `${trimZeros(scaled.toFixed(decimalsFor(scaled, maxDecimals)))}${step.label}`;
  }
  return String(Math.round(value));
}

/** Exact value with thousands separators, for tooltips. */
export function formatExactNumber(value: number): string {
  if (!Number.isFinite(value)) return "0";
  const rounded = Math.round(value);
  const grouped = String(Math.abs(rounded)).replace(/\B(?=(\d{3})+(?!\d))/g, ",");
  return rounded < 0 ? `-${grouped}` : grouped;
}

/** Estimated USD spend; keeps sub-cent values readable instead of rounding to 0. */
export function formatUsd(amount: number): string {
  if (!Number.isFinite(amount) || amount <= 0) return "$0.00";
  if (amount >= 1000) return `$${amount.toFixed(0)}`;
  if (amount >= 0.01) return `$${amount.toFixed(2)}`;
  if (amount >= 0.001) return `$${amount.toFixed(3)}`;
  return "<$0.001";
}
