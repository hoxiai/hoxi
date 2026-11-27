import { createMemo, mapArray, type Accessor } from "solid-js";
import type { MessagePart, MessageTokens } from "../services/sse-types";
import type { MessageTurn } from "./workspace-state";

export interface ContextContributor {
  key: string;
  part: MessagePart;
  role: "user" | "assistant";
  tokens: number;
}

export interface SessionContextWatermarkStats {
  used: number;
  limit: number;
  percent: number;
  overLimit: boolean;
  input: number;
  output: number;
  reasoning: number;
  cacheRead: number;
  cacheWrite: number;
  totalCost: number;
  cacheHitPercent: number | null;
  cacheHitPartial: boolean;
  lastCacheDrop?: MessageTokens["cacheDrop"];
}

/**
 * Computes the active context-window occupancy watermark from session turns,
 * separating single-step window occupancy (`tokens.context` / `tokens.total`)
 * from multi-step cumulative billing buckets (`input`, `cache.read`, etc.).
 */
export function computeSessionContextWatermark(
  turns: MessageTurn[],
  limit: number
): SessionContextWatermarkStats {
  let latestInput = 0;
  let latestOutput = 0;
  let latestReasoning = 0;
  let latestCacheRead = 0;
  let latestCacheWrite = 0;
  let latestTotal = 0;
  let latestContext = 0;
  let latestReasoningPartCount = 0;
  let totalCost = 0;
  let cacheHitPercent: number | null = null;
  let cacheHitPartial = false;
  let lastCacheDrop: MessageTokens["cacheDrop"];
  const cacheTelemetryModels = new Set<string>();

  for (const turn of turns) {
    if (typeof turn.info.cost === "number") {
      totalCost += turn.info.cost;
    }
    const t = turn.info.tokens;
    if (t?.cacheDrop) lastCacheDrop = t.cacheDrop;
    if (
      t &&
      ((t.context ?? 0) > 0 ||
        (t.total ?? 0) > 0 ||
        t.input > 0 ||
        t.output > 0 ||
        (t.cache?.read ?? 0) > 0)
    ) {
      latestInput = t.input || 0;
      latestOutput = t.output || 0;
      latestReasoning = t.reasoning || 0;
      latestCacheRead = t.cache?.read || 0;
      latestCacheWrite = t.cache?.write || 0;
      const inputPool = latestInput + latestCacheRead + latestCacheWrite;
      const reported = t.reportedCacheInput ?? 0;
      const modelKey = `${turn.info.providerID ?? ""}\0${turn.info.modelID ?? ""}`;
      const hasValidReported = reported > 0 && reported <= inputPool;
      const turnHasCacheSignal =
        latestCacheRead > 0 || latestCacheWrite > 0 || hasValidReported;
      if (turnHasCacheSignal) {
        cacheTelemetryModels.add(modelKey);
      }
      if (inputPool > 0 && reported === inputPool) {
        cacheHitPercent = Math.round((100 * latestCacheRead) / inputPool);
        cacheHitPartial = false;
      } else if (
        inputPool > 0 &&
        reported >= 0 &&
        reported <= inputPool &&
        (turnHasCacheSignal || cacheTelemetryModels.has(modelKey))
      ) {
        cacheHitPercent = Math.round((100 * latestCacheRead) / inputPool);
        cacheHitPartial = true;
      } else {
        cacheHitPercent = null;
        cacheHitPartial = false;
      }
      latestTotal = t.total || 0;
      latestContext = t.context || 0;
      latestReasoningPartCount = turn.parts.filter((p) => p.type === "reasoning").length;
    }
  }

  let used = 0;
  if (latestContext > 0) {
    used = latestContext;
  } else if (latestTotal > 0) {
    const legacyNoCacheSum = latestInput + latestOutput + latestReasoning;
    const fullTurnPool = legacyNoCacheSum + latestCacheRead + latestCacheWrite;
    if (
      latestReasoningPartCount >= 2 &&
      (latestTotal === legacyNoCacheSum || latestTotal === fullTurnPool)
    ) {
      used = Math.round(fullTurnPool / latestReasoningPartCount);
    } else {
      used = latestTotal;
    }
  } else {
    used =
      latestInput +
      latestOutput +
      latestReasoning +
      latestCacheRead +
      latestCacheWrite;
  }

  const rawPercent = limit > 0 ? Math.round((used / limit) * 100) : 0;
  const percent = Math.min(100, Math.max(0, rawPercent));

  return {
    used,
    limit,
    percent,
    overLimit: rawPercent > 100,
    input: latestInput,
    output: latestOutput,
    reasoning: latestReasoning,
    cacheRead: latestCacheRead,
    cacheWrite: latestCacheWrite,
    totalCost,
    cacheHitPercent,
    cacheHitPartial,
    lastCacheDrop,
  };
}

function partTokenEstimate(part: MessagePart, inputChars: number): number {
  let chars = 0;
  switch (part.type) {
    case "text": case "reasoning": chars = part.text?.length ?? 0; break;
    case "tool":
      chars = inputChars;
      if (part.state.status === "completed") chars += (part.state.output ?? "").length;
      if (part.state.status === "error") chars += (part.state.error ?? "").length;
      break;
    case "file": chars = (part.filename?.length ?? 0)+(part.url?.length ?? 0); break;
    case "patch": chars = (part.files ?? []).join(",").length; break;
  }
  return Math.ceil(chars/4);
}

/** Each input is serialized only when that input changes, and only while open. */
export function createContextContributors(turns: Accessor<MessageTurn[]>, enabled: Accessor<boolean>): Accessor<ContextContributor[]> {
  const groups = mapArray(() => enabled() ? turns() : [], (turn) => mapArray(() => turn.parts, (part) => {
    const input = createMemo(() => part.type === "tool" ? part.state.input : undefined);
    const inputChars = createMemo(() => {
      if (part.type !== "tool") return 0;
      try { return JSON.stringify(input() ?? {}).length; } catch { return 0; }
    });
    return createMemo<ContextContributor>(() => ({
      key: part.id, part, role: turn.info.role, tokens: partTokenEstimate(part, inputChars()),
    }));
  }));
  return createMemo(() => {
    const top: ContextContributor[] = [];
    // Always read groups, including while closed, so mapArray disposes the old
    // part computations instead of retaining subscriptions to historical inputs.
    for (const group of groups()) {
      for (const candidate of group()) {
        const item = candidate();
        if (item.tokens <= 0) continue;
        const index = top.findIndex((previous) => item.tokens > previous.tokens);
        if (index >= 0) top.splice(index, 0, item);
        else if (top.length < 3) top.push(item);
        if (top.length > 3) top.pop();
      }
    }
    return top;
  }, [], { equals: (a, b) => a.length === b.length && a.every((item, i) => item === b[i]) });
}
