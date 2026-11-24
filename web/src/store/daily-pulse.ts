// web/src/store/daily-pulse.ts
import { createSignal } from "solid-js";
import { hoxiAPI, type GitDailyPulseResponse } from "../services/api";
import { syncOfficialModelPricing } from "./leaderboard-state";
import { estimateTokenCostUsd, resolveUsdPriceSpec } from "./model-pricing";
import { isImportedSessionTitle, workspace } from "./workspace-state";

export interface DailyModelUsageItem {
  providerID: string;
  modelID: string;
  turns: number;
  totalTokens: number;
  cost: number;
}

export interface DailyTokenBreakdown {
  input: number;
  output: number;
  reasoning: number;
  cacheRead: number;
  cacheWrite: number;
}

export interface DailyDevPulseStats {
  dateKey: string;
  totalTokens: number;
  inputTokens: number;
  outputTokens: number;
  reasoningTokens: number;
  cacheReadTokens: number;
  cacheWriteTokens: number;
  cacheHitRate: number;
  totalCost: number;
  totalAdditions: number;
  totalDeletions: number;
  netLines: number;
  changedFilesCount: number;
  todayCommits: number;
  committedAdditions: number;
  committedDeletions: number;
  uncommittedAdditions: number;
  uncommittedDeletions: number;
  todaySessionsCount: number;
  todayTurnsCount: number;
  toolCallsTotal: number;
  toolSuccessRate: number;
  modelsUsed: DailyModelUsageItem[];
  /** Model ids the synced catalog cannot price at all; their cost is excluded. */
  unpricedModels: string[];
}

export function getTodayDateKey(): string {
  const now = new Date();
  return `${now.getFullYear()}-${String(now.getMonth() + 1).padStart(2, "0")}-${String(now.getDate()).padStart(2, "0")}`;
}

export function getStartOfTodayMs(): number {
  const now = new Date();
  return new Date(now.getFullYear(), now.getMonth(), now.getDate()).getTime();
}

function normalizeTs(ts?: number): number {
  if (!ts || ts <= 0) return 0;
  return ts < 1e12 ? ts * 1000 : ts;
}

export function estimateModelMarketCostUsd(
  modelID: string,
  tokens: DailyTokenBreakdown
): number | null {
  return estimateTokenCostUsd(modelID, tokens);
}

/** True when the model id cannot be priced from the synced catalog at all. */
export function isModelPriceKnown(modelID: string): boolean {
  return resolveUsdPriceSpec(modelID) !== undefined;
}

const [gitDailyPulse, setGitDailyPulse] = createSignal<GitDailyPulseResponse>({
  todayCommits: 0,
  committedAdditions: 0,
  committedDeletions: 0,
  committedFiles: 0,
  uncommittedAdditions: 0,
  uncommittedDeletions: 0,
  uncommittedFiles: 0,
});
const [syncingPulse, setSyncingPulse] = createSignal(false);

let pulseInFlight = false;
let pulseGeneration = 0;
let lastPulseSignature = "";

export async function refreshDailyDevPulse(manual = false): Promise<void> {
  if (pulseInFlight && !manual) return;
  const gen = ++pulseGeneration;
  pulseInFlight = true;
  if (manual) setSyncingPulse(true);
  void syncOfficialModelPricing();
  try {
    const res = await hoxiAPI.getGitDailyPulse(undefined, undefined, manual);
    if (gen !== pulseGeneration) return;
    if (res) {
      const sig = JSON.stringify(res);
      if (sig !== lastPulseSignature) {
        lastPulseSignature = sig;
        setGitDailyPulse(res);
      }
    }
  } catch {
    /* ignore */
  } finally {
    if (gen === pulseGeneration) {
      pulseInFlight = false;
      setSyncingPulse(false);
    }
  }
}

export function isDailyPulseSyncing(): boolean {
  return syncingPulse();
}

export function computeDailyDevPulseStats(): DailyDevPulseStats {
  const todayKey = getTodayDateKey();
  const startOfToday = getStartOfTodayMs();
  const pulse = gitDailyPulse();

  let inputTokens = pulse.dbInputTokens || 0;
  let outputTokens = pulse.dbOutputTokens || 0;
  let reasoningTokens = pulse.dbReasoningTokens || 0;
  let cacheReadTokens = pulse.dbCacheReadTokens || 0;
  let cacheWriteTokens = pulse.dbCacheWriteTokens || 0;
  let totalCost = 0;
  let todayTurnsCount = pulse.dbTurnsCount || 0;
  const latestDbTs = pulse.dbLatestTurnTs || 0;
  const unpricedModels = new Set<string>();

  const byModel = new Map<string, DailyModelUsageItem>();
  const dbModels = pulse.dbModels || [];
  for (const m of dbModels) {
    const tokens: DailyTokenBreakdown = {
      input: m.inputTokens || 0,
      output: m.outputTokens || 0,
      reasoning: m.reasoningTokens || 0,
      cacheRead: m.cacheReadTokens || 0,
      cacheWrite: m.cacheWriteTokens || 0,
    };
    const hasBreakdown = Object.values(tokens).some((v) => v > 0);
    const estimated = hasBreakdown ? estimateModelMarketCostUsd(m.modelID, tokens) : null;
    if (hasBreakdown && estimated === null) unpricedModels.add(m.modelID);
    const cost = estimated ?? (m.cost || 0);
    totalCost += cost;
    const key = `${m.providerID}/${m.modelID}`;
    byModel.set(key, {
      providerID: m.providerID,
      modelID: m.modelID,
      turns: m.turns,
      totalTokens: m.totalTokens,
      cost,
    });
  }
  if (dbModels.length === 0) totalCost = pulse.dbTotalCost || 0;

  // Merge live in-memory token deltas across loaded top-level sessions (excluding
  // subagent child sessions whose usage is already folded into the parent turn,
  // and excluding historical turns from imported external sessions).
  const subagentSessionIDs = new Set<string>();
  const importedSessionIDs = new Set<string>();
  for (const [sid, meta] of Object.entries(workspace.sessionIndex)) {
    if (meta?.parentID) subagentSessionIDs.add(sid);
    if (meta?.imported) importedSessionIDs.add(sid);
  }
  for (const sess of workspace.sessions) {
    if (sess.parentID) subagentSessionIDs.add(sess.id);
    if (isImportedSessionTitle(sess.title)) importedSessionIDs.add(sess.id);
  }

  const activeTurnsSnapshot = pulse.dbActiveTurns;
  const seenLiveTurnKeys = new Set<string>();
  const activeSid = workspace.activeSessionID;
  for (const [sid, turns] of Object.entries(workspace.messagesBySession)) {
    if (!turns || turns.length === 0 || subagentSessionIDs.has(sid)) continue;
    const isImportedSession = importedSessionIDs.has(sid);
    for (let i = turns.length - 1; i >= 0; i--) {
      const turn = turns[i];
      const info = turn.info;
      if (info.role !== "assistant" || info.imported) continue;
      if (isImportedSession && !info.id?.startsWith("msg_a_")) continue;
      if (
        !info.id?.startsWith("msg_a_") &&
        turn.parts?.some(
          (p) =>
            p.type === "tool" &&
            "metadata" in p.state &&
            Boolean(p.state.metadata?.imported)
        )
      ) {
        continue;
      }
      const createdTs = normalizeTs(info.time?.created);
      if (createdTs > 0 && createdTs < startOfToday) break;
      if (createdTs < startOfToday) continue;

      const tk = info.tokens;
      const inTok = tk?.input || 0;
      const outTok = tk?.output || 0;
      const reasonTok = tk?.reasoning || 0;
      const cRead = tk?.cache?.read || 0;
      const cWrite = tk?.cache?.write || 0;
      if (inTok + outTok + reasonTok + cRead + cWrite <= 0) continue;

      const dedupKey = info.id || `${sid}:${createdTs}:${info.modelID || ""}:${inTok}:${outTok}`;
      if (seenLiveTurnKeys.has(dedupKey)) continue;
      seenLiveTurnKeys.add(dedupKey);

      const activeBase = info.id ? activeTurnsSnapshot?.[info.id] : undefined;
      let dIn = inTok;
      let dOut = outTok;
      let dReason = reasonTok;
      let dRead = cRead;
      let dWrite = cWrite;
      let turnCountDelta = 1;
      if (activeBase !== undefined) {
        dIn = Math.max(0, inTok - (activeBase.input || 0));
        dOut = Math.max(0, outTok - (activeBase.output || 0));
        dReason = Math.max(0, reasonTok - (activeBase.reasoning || 0));
        dRead = Math.max(0, cRead - (activeBase.cacheRead || 0));
        dWrite = Math.max(0, cWrite - (activeBase.cacheWrite || 0));
        if (dIn + dOut + dReason + dRead + dWrite <= 0) continue;
        const baseHadTokens =
          (activeBase.input || 0) +
            (activeBase.output || 0) +
            (activeBase.reasoning || 0) +
            (activeBase.cacheRead || 0) +
            (activeBase.cacheWrite || 0) >
          0;
        turnCountDelta = baseHadTokens ? 0 : 1;
      } else {
        if (latestDbTs > 0 && createdTs <= latestDbTs) continue;
        if (latestDbTs === 0 && (pulse.dbTurnsCount || 0) > 0) continue;
      }

      const turnTotal = dIn + dOut + dReason + dRead + dWrite;
      const pid =
        info.providerID ||
        (sid === activeSid ? workspace.selectedModel?.providerID : "") ||
        "unknown";
      const mid =
        info.modelID ||
        (sid === activeSid ? workspace.selectedModel?.modelID : "") ||
        "auto";
      const mKey = `${pid}/${mid}`;
      const estimatedTurn = estimateModelMarketCostUsd(mid, {
        input: dIn,
        output: dOut,
        reasoning: dReason,
        cacheRead: dRead,
        cacheWrite: dWrite,
      });
      if (estimatedTurn === null) unpricedModels.add(mid);
      const turnCost =
        estimatedTurn ??
        (turnCountDelta > 0 && typeof info.cost === "number" && info.cost > 0 ? info.cost : 0);

      inputTokens += dIn;
      outputTokens += dOut;
      reasoningTokens += dReason;
      cacheReadTokens += dRead;
      cacheWriteTokens += dWrite;
      totalCost += turnCost;
      todayTurnsCount += turnCountDelta;

      const existing = byModel.get(mKey) || {
        providerID: pid,
        modelID: mid,
        turns: 0,
        totalTokens: 0,
        cost: 0,
      };
      existing.turns += turnCountDelta;
      existing.totalTokens += turnTotal;
      existing.cost += turnCost;
      byModel.set(mKey, existing);
    }
  }

  const totalTokens = inputTokens + outputTokens + reasoningTokens + cacheReadTokens + cacheWriteTokens;
  const totalInputPool = inputTokens + cacheReadTokens + cacheWriteTokens;
  const cacheHitRate = totalInputPool > 0 ? Math.round((cacheReadTokens / totalInputPool) * 100) : 0;

  const toolCallsTotal = pulse.dbToolCallsTotal || 0;
  const toolCallsFailed = pulse.dbToolCallsFailed || 0;
  const toolSuccessRate =
    toolCallsTotal > 0 ? Math.round(((toolCallsTotal - toolCallsFailed) / toolCallsTotal) * 1000) / 10 : 100;

  const committedAdditions = pulse.committedAdditions || 0;
  const committedDeletions = pulse.committedDeletions || 0;
  const uncommittedAdditions = pulse.uncommittedAdditions || 0;
  const uncommittedDeletions = pulse.uncommittedDeletions || 0;
  const totalAdditions = committedAdditions + uncommittedAdditions;
  const totalDeletions = committedDeletions + uncommittedDeletions;
  const netLines = totalAdditions - totalDeletions;
  const changedFilesCount =
    pulse.changedFiles || (pulse.committedFiles || 0) + (pulse.uncommittedFiles || 0);

  const modelsUsed = Array.from(byModel.values())
    .sort((a, b) => b.totalTokens - a.totalTokens || b.cost - a.cost)
    .slice(0, 3);

  return {
    dateKey: todayKey,
    totalTokens,
    inputTokens,
    outputTokens,
    reasoningTokens,
    cacheReadTokens,
    cacheWriteTokens,
    cacheHitRate,
    totalCost,
    totalAdditions,
    totalDeletions,
    netLines,
    changedFilesCount,
    todayCommits: pulse.todayCommits || 0,
    committedAdditions,
    committedDeletions,
    uncommittedAdditions,
    uncommittedDeletions,
    todaySessionsCount: pulse.dbSessionsCount || 0,
    todayTurnsCount,
    toolCallsTotal,
    toolSuccessRate,
    modelsUsed,
    unpricedModels: Array.from(unpricedModels),
  };
}
