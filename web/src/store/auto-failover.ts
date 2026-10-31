// web/src/store/auto-failover.ts
//
// When a turn sent through an auto pool fails, decide whether moving to the
// next pool candidate can help, how long the failed candidate should rest, and
// remember each switch so the timeline shows "switched from X to Y" instead of
// a dead-end error with manual retry buttons.
import { createSignal } from "solid-js";
import { getEffectivePoolCandidates } from "./auto-models";

export interface AutoFailoverError {
  name: string;
  data?: { message?: string; statusCode?: number; isRetryable?: boolean };
}

export type AutoFailoverReason =
  | "rateLimit"
  | "quota"
  | "auth"
  | "model"
  | "server"
  | "network"
  | "other";

export interface AutoFailoverDecision {
  /** Retry the prompt on the next pool candidate. */
  failover: boolean;
  /** The failure is the endpoint's fault, so the candidate should rest. */
  penalize: boolean;
  reason: AutoFailoverReason;
}

// An unknown error that arrives this fast is almost always the endpoint, not
// the conversation, so it is worth one more candidate.
const FAST_FAILURE_MS = 45_000;
// Every candidate in the pool may be tried once, but a pool of many look-alike
// endpoints should not turn one prompt into a long chain of resends.
const MAX_FAILOVER_ATTEMPTS = 5;
const MAX_RESET_WAIT_MS = 24 * 60 * 60 * 1000;

// Errors caused by the conversation or by the user: another endpoint of the
// same pool would fail the same way, so the error card handles them.
const NEVER_SWITCH_NAMES = new Set([
  "runnerstallederror",
  "repeatedtoolfailureerror",
  "messageabortederror",
  "contextoverflowerror",
  "messageoutputlengtherror",
]);
// Chinese phrases below are \u-escaped for the i18n lint. They cover upstream
// wording such as 上下文过长, 内容安全, 违规, 余额不足, 额度, 均已限额, 使用量已超出,
// 频率, 限流, 请求过于频繁, 鉴权, 认证失败, 服务繁忙/不可用, 连接失败/中断.
const ABORT_RE = /\baborted?\b/;
const CONTEXT_RE =
  /context (limit|length|window)|maximum context|too many tokens|prompt is too long|reduce the length|\u4e0a\u4e0b\u6587.*(\u8d85|\u8fc7\u957f)/;
const POLICY_RE = /content.?(policy|filter|management)|safety system|\u5185\u5bb9\u5b89\u5168|\u8fdd\u89c4|\u654f\u611f\u5185\u5bb9/;
const MODEL_RE = /model not found|model_not_found|model.*not.*available|is not loaded in the running engine|unknown model/;
const QUOTA_RE =
  /insufficient.?(quota|balance)|exceeded your current quota|credit balance|billing|\u4f59\u989d\u4e0d\u8db3|\u989d\u5ea6|\u5747\u5df2\u9650\u989d|\u4f7f\u7528\u91cf\u5df2\u8d85\u51fa/;
const RATE_RE = /\b429\b|rate.?limit|too many requests|\u9891\u7387|\u9650\u6d41|\u8bf7\u6c42\u8fc7\u4e8e\u9891\u7e41/;
const AUTH_RE = /unauthorized|invalid api key|incorrect api key|authentication|forbidden|\u9274\u6743|\u8ba4\u8bc1\u5931\u8d25|\u65e0\u6548.{0,4}(key|\u4ee4\u724c)/;
const SERVER_RE = /overloaded|service unavailable|bad gateway|\b50[234]\b|\b529\b|internal server error|\u670d\u52a1.{0,4}(\u7e41\u5fd9|\u4e0d\u53ef\u7528)/;
const NETWORK_RE = /timeout|timed out|econn|fetch failed|network|socket|stream was interrupted|\u8fde\u63a5.{0,4}(\u5931\u8d25|\u4e2d\u65ad)/;

export function decideAutoFailover(
  error: AutoFailoverError,
  elapsedMs: number
): AutoFailoverDecision {
  const name = (error.name || "").toLowerCase();
  const msg = `${error.data?.message || ""}`.toLowerCase();
  const status = error.data?.statusCode;
  // Not the endpoint's fault: neither switch nor bench the candidate.
  const notEndpoint = { failover: false, penalize: false, reason: "other" as const };
  const yes = (reason: AutoFailoverReason) => ({ failover: true, penalize: true, reason });

  if (NEVER_SWITCH_NAMES.has(name) || ABORT_RE.test(name) || ABORT_RE.test(msg)) return notEndpoint;
  if (CONTEXT_RE.test(msg) || POLICY_RE.test(msg)) return notEndpoint;
  if (name === "providermodelnotfounderror" || MODEL_RE.test(msg)) return yes("model");
  if (status === 402 || QUOTA_RE.test(msg)) return yes("quota");
  if (status === 429 || RATE_RE.test(msg)) return yes("rateLimit");
  if (status === 401 || status === 403 || AUTH_RE.test(msg)) return yes("auth");
  if (status === 404) return yes("model");
  if ((status !== undefined && status >= 500) || SERVER_RE.test(msg)) return yes("server");
  if (status === 408 || NETWORK_RE.test(msg)) return yes("network");
  if (error.data?.isRetryable) return yes("server");
  if (status === 400 || status === 413 || status === 422) return notEndpoint;
  return elapsedMs < FAST_FAILURE_MS
    ? yes("other")
    : { failover: false, penalize: true, reason: "other" };
}

/** How many candidates one prompt may go through, counting the first. */
export function maxAutoFailoverAttempts(poolID: string): number {
  const size = getEffectivePoolCandidates(poolID).length;
  return Math.max(1, Math.min(size, MAX_FAILOVER_ATTEMPTS));
}

/**
 * Reads the reset time an upstream states in its error ("将在 2026-10-30
 * 22:48:16 UTC+8 重置", "try again in 20s") and returns the wait in ms, capped
 * at a day. Mirrors internal/provider/quota_reset.go.
 */
export function parseQuotaResetMs(message: string, now = Date.now()): number | undefined {
  const text = (message || "").toLowerCase();
  if (!/\u91cd\u7f6e|\u91cd\u8bd5|\u6062\u590d|reset|retry|try again|available|until/.test(text)) return undefined;
  const cap = (ms: number) => (ms > 0 ? Math.min(ms, MAX_RESET_WAIT_MS) : undefined);

  const abs = text.match(
    /(\d{4})-(\d{2})-(\d{2})[ t](\d{2}):(\d{2})(?::(\d{2}))?(?:\.\d+)?\s*(z|(?:utc|gmt)?\s*[+-]\d{1,2}(?::?\d{2})?|utc|gmt)?/
  );
  if (abs) {
    const [, y, mo, d, h, mi, s, zone] = abs;
    const fields = [Number(y), Number(mo) - 1, Number(d), Number(h), Number(mi), Number(s || 0)] as const;
    let at: number | undefined;
    if (!zone) {
      at = new Date(...fields).getTime();
    } else {
      const offset = parseZoneOffsetMinutes(zone);
      if (offset !== undefined) at = Date.UTC(...fields) - offset * 60_000;
    }
    if (at !== undefined && at > now) return cap(at - now);
  }

  const rel =
    text.match(/(?:in|after)\s+(\d+(?:\.\d+)?)\s*(ms|milliseconds?|s|secs?|seconds?|m|mins?|minutes?|h|hrs?|hours?)\b/) ||
    text.match(/(\d+(?:\.\d+)?)\s*(\u79d2|\u5206\u949f|\u5c0f\u65f6)\s*\u540e/);
  if (rel) {
    const n = Number(rel[1]);
    const unit = rel[2];
    const base =
      unit === "ms" || unit.startsWith("milli")
        ? 1
        : unit === "\u79d2" || unit.startsWith("s")
          ? 1000
          : unit === "\u5206\u949f" || unit.startsWith("m")
            ? 60_000
            : 3_600_000;
    return cap(n * base);
  }
  return undefined;
}

function parseZoneOffsetMinutes(zone: string): number | undefined {
  const z = zone.trim().replace(/^(utc|gmt)/, "").trim();
  if (!z || z === "z") return 0;
  const m = z.match(/^([+-])(\d{1,2})(?::?(\d{2}))?$/);
  if (!m) return undefined;
  const hours = Number(m[2]);
  const minutes = Number(m[3] || 0);
  if (hours > 14 || minutes > 59) return undefined;
  return (m[1] === "-" ? -1 : 1) * (hours * 60 + minutes);
}

// ---------------------------------------------------------------------------
// Failover notices shown on the failed turn.

export interface AutoFailoverNotice {
  sessionID: string;
  from: { providerID: string; modelID: string };
  to: { providerID: string; modelID: string };
  reason: AutoFailoverReason;
  at: number;
  cooldownUntil?: number;
}

export const AUTO_FAILOVER_STORAGE_KEY = "hoxi:auto-failover:v1";
const NOTICE_TTL_MS = 7 * 24 * 60 * 60 * 1000;
const MAX_NOTICES = 200;
// A failed turn is marked completed just before session.error arrives and the
// next candidate is dispatched, so the switch lands shortly after it.
const NOTICE_MATCH_WINDOW_MS = 120_000;
const AUTO_FAILOVER_PENDING_TTL_MS = 5_000;
const autoFailoverPendingAtBySession = new Map<string, number>();

export function markAutoFailoverPending(sessionID: string): void {
  if (!sessionID) return;
  autoFailoverPendingAtBySession.set(sessionID, Date.now());
}

export function clearAutoFailoverPending(sessionID: string): void {
  if (!sessionID) return;
  autoFailoverPendingAtBySession.delete(sessionID);
}

export function isAutoFailoverPending(sessionID: string): boolean {
  if (!sessionID) return false;
  const at = autoFailoverPendingAtBySession.get(sessionID);
  if (at === undefined) return false;
  if (Date.now() - at > AUTO_FAILOVER_PENDING_TTL_MS) {
    autoFailoverPendingAtBySession.delete(sessionID);
    return false;
  }
  return true;
}

function loadNotices(): AutoFailoverNotice[] {
  try {
    const raw = localStorage.getItem(AUTO_FAILOVER_STORAGE_KEY);
    const parsed = raw ? JSON.parse(raw) : [];
    const cutoff = Date.now() - NOTICE_TTL_MS;
    return Array.isArray(parsed) ? parsed.filter((n) => n && n.at > cutoff) : [];
  } catch {
    return [];
  }
}

const [autoFailoverNotices, setAutoFailoverNotices] = createSignal<AutoFailoverNotice[]>(loadNotices());

export function recordAutoFailover(notice: AutoFailoverNotice): void {
  const next = [...autoFailoverNotices(), notice].slice(-MAX_NOTICES);
  setAutoFailoverNotices(next);
  try {
    localStorage.setItem(AUTO_FAILOVER_STORAGE_KEY, JSON.stringify(next));
  } catch {
    /* ignore */
  }
}

/** The switch made after this failed assistant turn, if the pool made one. */
export function findAutoFailoverNotice(info: {
  sessionID: string;
  providerID?: string;
  modelID?: string;
  time?: { created?: number; completed?: number };
}): AutoFailoverNotice | undefined {
  const created = info.time?.created ?? 0;
  const end = (info.time?.completed ?? created) + NOTICE_MATCH_WINDOW_MS;
  const list = autoFailoverNotices();
  for (let i = list.length - 1; i >= 0; i--) {
    const n = list[i];
    if (
      n.sessionID === info.sessionID &&
      n.from.providerID === info.providerID &&
      n.from.modelID === info.modelID &&
      n.at >= created &&
      n.at <= end
    ) {
      return n;
    }
  }
  return undefined;
}
