// web/src/components/layout/session-time-helpers.ts
import { createSignal } from "solid-js";
import { t } from "../../i18n";

let sharedNowSignal: (() => number) | null = null;

export function useNowTicker(intervalMs = 30000): () => number {
  if (sharedNowSignal) return sharedNowSignal;
  const [now, setNow] = createSignal(Date.now());
  if (typeof window !== "undefined") {
    setInterval(() => setNow(Date.now()), intervalMs);
  }
  sharedNowSignal = now;
  return now;
}

export function normalizeTimestampMs(ts?: number): number {
  if (!ts || ts <= 0) return 0;
  return ts < 1e11 ? ts * 1000 : ts;
}

export function formatCompactRelativeTime(ts: number, now = Date.now()): string {
  const actualTs = normalizeTimestampMs(ts);
  if (actualTs <= 0) return "";
  const diff = Math.max(0, now - actualTs);
  const sec = Math.floor(diff / 1000);
  if (sec < 60) return t("leftNav.timeNow") || "now";
  const min = Math.floor(sec / 60);
  if (min < 60) return `${min}m`;
  const hr = Math.floor(min / 60);
  if (hr < 24) return `${hr}h`;
  const day = Math.floor(hr / 24);
  if (day < 7) return `${day}d`;
  const week = Math.floor(day / 7);
  if (week < 5) return `${week}w`;
  const month = Math.floor(day / 30);
  if (month < 12) return `${month}mo`;
  const yr = Math.floor(day / 365);
  return `${yr}y`;
}

export function formatExactTime(ts: number): string {
  const actualTs = normalizeTimestampMs(ts);
  if (actualTs <= 0) return "";
  try {
    return new Date(actualTs).toLocaleString();
  } catch {
    return "";
  }
}
