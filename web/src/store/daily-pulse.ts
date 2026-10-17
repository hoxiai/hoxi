// web/src/store/daily-pulse.ts
import { createSignal } from "solid-js";
import { hoxiAPI, type GitDailyPulseResponse } from "../services/api";
import type { MessageInfo, MessagePart, SnapshotFileDiff } from "../services/sse-types";
import { activeModelInfo } from "./workspace-providers";
import { setWorkspace, workspace } from "./workspace";

const DAILY_PULSE_STORAGE_KEY = "hoxi-daily-pulse:v1";

export interface DailyTurnSnapshot {
  id: string;
  sessionID: string;
  timestamp: number;
  providerID: string;
  modelID: string;
  inputTokens: number;
  outputTokens: number;
  reasoningTokens: number;
  cacheReadTokens: number;
  cacheWriteTokens: number;
  cost: number;
  savedByCache: number;
  durationMs: number;
  hasError: boolean;
  toolCallsTotal: number;
  toolCallsFailed: number;
  editToolCalls: number;
  bashToolCalls: number;
  readSearchToolCalls: number;
}

export interface DailyModelUsageItem {
  providerID: string;
  modelID: string;
  turns: number;
  totalTokens: number;
  cost: number;
  sharePercent: number;
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
  reasoningRatio: number;
  totalCost: number;
  savedByCache: number;
  avgCostPerTurn: number;
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
  errorTurns: number;
  toolCallsTotal: number;
  toolCallsFailed: number;
  toolSuccessRate: number;
  editToolCalls: number;
  bashToolCalls: number;
  readSearchToolCalls: number;
  avgTps: number;
  avgTtftMs: number;
  revertCount: number;
  avgAuditScore: number | null;
  activeContextUsed: number;
  activeContextLimit: number;
  activeContextPercent: number;
  modelsUsed: DailyModelUsageItem[];
}

interface StoredDailyLedger {
  dateKey: string;
  turns: Record<string, DailyTurnSnapshot>;
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

function loadInitialLedger(): StoredDailyLedger {
  const today = getTodayDateKey();
  try {
    const raw = localStorage.getItem(DAILY_PULSE_STORAGE_KEY);
    if (raw) {
      const parsed = JSON.parse(raw) as StoredDailyLedger;
      if (parsed?.dateKey === today && parsed.turns && typeof parsed.turns === "object") {
        return parsed;
      }
    }
  } catch {
    /* ignore */
  }
  return { dateKey: today, turns: {} };
}

const [dailyLedger, setDailyLedger] = createSignal<StoredDailyLedger>(loadInitialLedger());
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

const hydratedSessionIDs = new Set<string>();
let persistTimer: ReturnType<typeof setTimeout> | null = null;

function schedulePersistLedger(ledger: StoredDailyLedger) {
  if (persistTimer) clearTimeout(persistTimer);
  persistTimer = setTimeout(() => {
    persistTimer = null;
    try {
      localStorage.setItem(DAILY_PULSE_STORAGE_KEY, JSON.stringify(ledger));
    } catch {
      /* ignore */
    }
  }, 400);
}

function normalizeRate(val?: number): number | undefined {
  if (val === undefined || val === null || val < 0) return undefined;
  return val > 0 && val < 0.001 ? val * 1_000_000 : val;
}

function lookupModelPricing(providerID: string, modelID: string) {
  const pid = (providerID || "").toLowerCase();
  const mid = (modelID || "").toLowerCase();

  for (const p of workspace.providers) {
    if (pid && p.id.toLowerCase() !== pid && !p.models?.[modelID]) continue;
    const mInfo = p.models?.[modelID];
    if (mInfo?.cost) {
      const inRate = normalizeRate(mInfo.cost.input);
      const outRate = normalizeRate(mInfo.cost.output);
      if (inRate !== undefined && outRate !== undefined && (inRate > 0 || outRate > 0)) {
        return {
          input: inRate,
          output: outRate,
          cacheRead: normalizeRate(mInfo.cost.cache?.read) ?? inRate * 0.1,
          cacheWrite: normalizeRate(mInfo.cost.cache?.write) ?? inRate * 1.25,
        };
      }
    }
  }

  if (mid.endsWith("-free") || pid === "ollama" || pid === "lmstudio") {
    return { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 };
  }
  if (mid.includes("opus")) return { input: 15, output: 75, cacheRead: 1.5, cacheWrite: 18.75 };
  if (mid.includes("sonnet")) return { input: 3, output: 15, cacheRead: 0.3, cacheWrite: 3.75 };
  if (mid.includes("haiku")) return { input: 0.8, output: 4, cacheRead: 0.08, cacheWrite: 1.0 };
  if (mid.includes("gpt-5") || mid.includes("gpt-4.1") || mid.includes("gpt-4o") || mid.startsWith("o1") || mid.startsWith("o3")) {
    return mid.includes("mini") || mid.includes("nano")
      ? { input: 0.4, output: 1.6, cacheRead: 0.1, cacheWrite: 0.4 }
      : { input: 2.5, output: 10, cacheRead: 0.625, cacheWrite: 2.5 };
  }
  if (mid.includes("codex") || mid.includes("o4-mini")) return { input: 1.5, output: 6, cacheRead: 0.375, cacheWrite: 1.5 };
  if (mid.includes("grok")) {
    return mid.includes("mini") || mid.includes("fast")
      ? { input: 0.3, output: 0.5, cacheRead: 0.075, cacheWrite: 0.3 }
      : { input: 3, output: 15, cacheRead: 0.75, cacheWrite: 3 };
  }
  if (mid.includes("gemini")) {
    return mid.includes("flash")
      ? { input: 0.15, output: 0.6, cacheRead: 0.0375, cacheWrite: 0.15 }
      : { input: 1.25, output: 10, cacheRead: 0.3125, cacheWrite: 1.25 };
  }
  if (mid.includes("deepseek")) {
    return mid.includes("reasoner") || mid.includes("r1")
      ? { input: 0.55, output: 2.19, cacheRead: 0.14, cacheWrite: 0.55 }
      : { input: 0.27, output: 1.1, cacheRead: 0.07, cacheWrite: 0.27 };
  }
  return { input: 1.5, output: 6, cacheRead: 0.3, cacheWrite: 1.5 };
}

function classifyTool(toolName: string): "edit" | "bash" | "read" {
  const lower = (toolName || "").toLowerCase();
  if (/edit|write|patch|replace/.test(lower)) return "edit";
  if (/bash|shell|cmd|exec/.test(lower)) return "bash";
  return "read";
}

function buildTurnSnapshot(info: MessageInfo, parts: MessagePart[]): DailyTurnSnapshot | null {
  if (info.role !== "assistant") return null;
  const ts = normalizeTs(info.time?.completed || info.time?.created);
  if (ts < getStartOfTodayMs()) return null;

  let inputTokens = info.tokens?.input || 0;
  let outputTokens = info.tokens?.output || 0;
  let reasoningTokens = info.tokens?.reasoning || 0;
  const cacheReadTokens = info.tokens?.cache?.read || 0;
  const cacheWriteTokens = info.tokens?.cache?.write || 0;

  let toolCallsTotal = 0;
  let toolCallsFailed = 0;
  let editToolCalls = 0;
  let bashToolCalls = 0;
  let readSearchToolCalls = 0;
  let textChars = 0;
  let reasoningChars = 0;

  for (const part of parts || []) {
    if (part.type === "text") textChars += (part.text || "").length;
    else if (part.type === "reasoning") reasoningChars += (part.text || "").length;
    else if (part.type === "tool") {
      toolCallsTotal += 1;
      if (part.state.status === "error") toolCallsFailed += 1;
      const cat = classifyTool(part.tool);
      if (cat === "edit") editToolCalls += 1;
      else if (cat === "bash") bashToolCalls += 1;
      else readSearchToolCalls += 1;
    }
  }

  if (inputTokens === 0 && outputTokens === 0 && cacheReadTokens === 0 && (textChars > 0 || toolCallsTotal > 0)) {
    outputTokens = Math.max(12, Math.ceil(textChars / 3.6) + toolCallsTotal * 45);
    reasoningTokens = Math.ceil(reasoningChars / 3.6);
    inputTokens = Math.max(120, outputTokens * 3 + toolCallsTotal * 180);
  }

  const providerID = info.providerID || workspace.selectedModel?.providerID || "unknown";
  const modelID = info.modelID || workspace.selectedModel?.modelID || "auto";
  const price = lookupModelPricing(providerID, modelID);
  const computedCost =
    (inputTokens * price.input +
      (outputTokens + reasoningTokens) * price.output +
      cacheReadTokens * price.cacheRead +
      cacheWriteTokens * price.cacheWrite) /
    1_000_000;
  const cost = info.cost && info.cost > 0 ? info.cost : computedCost;
  const savedByCache =
    cacheReadTokens > 0
      ? Math.max(0, (cacheReadTokens * Math.max(0, price.input - price.cacheRead)) / 1_000_000)
      : 0;
  const createdMs = normalizeTs(info.time?.created);
  const completedMs = normalizeTs(info.time?.completed);

  return {
    id: info.id,
    sessionID: info.sessionID,
    timestamp: ts,
    providerID,
    modelID,
    inputTokens,
    outputTokens,
    reasoningTokens,
    cacheReadTokens,
    cacheWriteTokens,
    cost,
    savedByCache,
    durationMs: completedMs > createdMs && createdMs > 0 ? completedMs - createdMs : 0,
    hasError: Boolean(info.error),
    toolCallsTotal,
    toolCallsFailed,
    editToolCalls,
    bashToolCalls,
    readSearchToolCalls,
  };
}

export async function refreshDailyDevPulse(forceHydrate = false): Promise<void> {
  if (syncingPulse()) return;
  setSyncingPulse(true);
  try {
    const startOfToday = getStartOfTodayMs();
    const todaySessions = workspace.sessions.filter(
      (s) => Math.max(normalizeTs(s.time?.updated), normalizeTs(s.time?.created)) >= startOfToday
    );

    await Promise.all([
      hoxiAPI
        .getGitDailyPulse(workspace.rootDir || undefined, workspace.activeSessionID || undefined)
        .then((res) => res && setGitDailyPulse(res))
        .catch(() => {}),
      ...todaySessions.map(async (s) => {
        if (!forceHydrate && (workspace.messagesBySession[s.id] || hydratedSessionIDs.has(s.id))) return;
        hydratedSessionIDs.add(s.id);
        const dir = s.directory || workspace.rootDir || undefined;
        const [turns, diffs] = await Promise.all([
          hoxiAPI.getMessages(s.id, dir).catch(() => []),
          hoxiAPI.getSessionDiff(s.id, dir).catch(() => [] as SnapshotFileDiff[]),
        ]);
        if (turns.length > 0 && !workspace.messagesBySession[s.id]) {
          setWorkspace("messagesBySession", s.id, turns.map((t) => ({ info: t.info, parts: t.parts })));
        }
        if (diffs.length > 0 && !workspace.diffsBySession[s.id]) {
          setWorkspace("diffsBySession", s.id, diffs);
        }
      }),
    ]);
  } finally {
    setSyncingPulse(false);
  }
}

export function isDailyPulseSyncing(): boolean {
  return syncingPulse();
}

export function computeDailyDevPulseStats(): DailyDevPulseStats {
  const todayKey = getTodayDateKey();
  const startOfToday = getStartOfTodayMs();
  const currentLedger = dailyLedger();
  const mergedTurns = new Map<string, DailyTurnSnapshot>();

  if (currentLedger.dateKey === todayKey) {
    for (const [id, snap] of Object.entries(currentLedger.turns)) {
      if (snap && snap.timestamp >= startOfToday) mergedTurns.set(id, snap);
    }
  }

  let ledgerMutated = currentLedger.dateKey !== todayKey;
  for (const turns of Object.values(workspace.messagesBySession)) {
    for (const turn of turns || []) {
      const snap = buildTurnSnapshot(turn.info, turn.parts);
      if (!snap) continue;
      const prev = mergedTurns.get(snap.id);
      if (
        !prev ||
        prev.inputTokens !== snap.inputTokens ||
        prev.outputTokens !== snap.outputTokens ||
        prev.toolCallsTotal !== snap.toolCallsTotal ||
        prev.cost !== snap.cost
      ) {
        ledgerMutated = true;
      }
      mergedTurns.set(snap.id, snap);
    }
  }

  if (ledgerMutated) {
    const nextLedger: StoredDailyLedger = {
      dateKey: todayKey,
      turns: Object.fromEntries(mergedTurns.entries()),
    };
    queueMicrotask(() => {
      setDailyLedger(nextLedger);
      schedulePersistLedger(nextLedger);
    });
  }

  let inputTokens = 0, outputTokens = 0, reasoningTokens = 0, cacheReadTokens = 0, cacheWriteTokens = 0;
  let totalCost = 0, savedByCache = 0, errorTurns = 0;
  let toolCallsTotal = 0, toolCallsFailed = 0, editToolCalls = 0, bashToolCalls = 0, readSearchToolCalls = 0;

  const sessionIDsToday = new Set<string>();
  for (const s of workspace.sessions) {
    if (Math.max(normalizeTs(s.time?.updated), normalizeTs(s.time?.created)) >= startOfToday) {
      sessionIDsToday.add(s.id);
    }
  }

  const byModel = new Map<string, { providerID: string; modelID: string; turns: number; totalTokens: number; cost: number }>();

  for (const snap of mergedTurns.values()) {
    sessionIDsToday.add(snap.sessionID);
    inputTokens += snap.inputTokens;
    outputTokens += snap.outputTokens;
    reasoningTokens += snap.reasoningTokens;
    cacheReadTokens += snap.cacheReadTokens;
    cacheWriteTokens += snap.cacheWriteTokens;
    totalCost += snap.cost;
    savedByCache += snap.savedByCache;
    if (snap.hasError) errorTurns += 1;
    toolCallsTotal += snap.toolCallsTotal;
    toolCallsFailed += snap.toolCallsFailed;
    editToolCalls += snap.editToolCalls;
    bashToolCalls += snap.bashToolCalls;
    readSearchToolCalls += snap.readSearchToolCalls;

    const snapTotal = snap.inputTokens + snap.outputTokens + snap.reasoningTokens + snap.cacheReadTokens + snap.cacheWriteTokens;
    const mKey = `${snap.providerID}/${snap.modelID}`;
    const existing = byModel.get(mKey) || { providerID: snap.providerID, modelID: snap.modelID, turns: 0, totalTokens: 0, cost: 0 };
    existing.turns += 1;
    existing.totalTokens += snapTotal;
    existing.cost += snap.cost;
    byModel.set(mKey, existing);
  }

  const totalTokens = inputTokens + outputTokens + reasoningTokens + cacheReadTokens + cacheWriteTokens;
  const totalInputPool = inputTokens + cacheReadTokens + cacheWriteTokens;
  const cacheHitRate = totalInputPool > 0 ? Math.round((cacheReadTokens / totalInputPool) * 1000) / 10 : 0;
  const totalGeneratedPool = outputTokens + reasoningTokens;
  const reasoningRatio = totalGeneratedPool > 0 ? Math.round((reasoningTokens / totalGeneratedPool) * 1000) / 10 : 0;
  const todayTurnsCount = mergedTurns.size;
  const avgCostPerTurn = todayTurnsCount > 0 ? totalCost / todayTurnsCount : 0;
  const toolSuccessRate = toolCallsTotal > 0 ? Math.round(((toolCallsTotal - toolCallsFailed) / toolCallsTotal) * 1000) / 10 : 100;

  const git = gitDailyPulse();
  let sessionDiffAdd = 0, sessionDiffDel = 0;
  const sessionDiffFiles = new Set<string>();
  const activeDiffs = workspace.activeSessionID ? workspace.diffsBySession[workspace.activeSessionID] : undefined;
  const diffList = activeDiffs && activeDiffs.length > 0 ? activeDiffs : workspace.diffsBySession["__root__"] || [];
  for (const d of diffList) {
    if (!d?.file) continue;
    sessionDiffFiles.add(d.file);
    sessionDiffAdd += d.additions || 0;
    sessionDiffDel += d.deletions || 0;
  }

  const uncommittedAdditions = Math.max(git.uncommittedAdditions || 0, sessionDiffAdd);
  const uncommittedDeletions = Math.max(git.uncommittedDeletions || 0, sessionDiffDel);
  const committedAdditions = git.committedAdditions || 0;
  const committedDeletions = git.committedDeletions || 0;
  const totalAdditions = committedAdditions + uncommittedAdditions;
  const totalDeletions = committedDeletions + uncommittedDeletions;
  const netLines = totalAdditions - totalDeletions;
  const changedFilesCount = Math.max((git.committedFiles || 0) + (git.uncommittedFiles || 0), sessionDiffFiles.size);

  let tpsSum = 0, tpsCount = 0, ttftSum = 0, ttftCount = 0, revertCount = 0, auditSum = 0, auditCount = 0;
  for (const rec of Object.values(workspace.providerTelemetry)) {
    if (!rec) continue;
    revertCount += rec.revertCount || 0;
    for (const v of rec.tpsSamples || []) { tpsSum += v; tpsCount += 1; }
    for (const v of rec.ttftSamplesMs || []) { ttftSum += v; ttftCount += 1; }
    for (const v of rec.auditScores || []) { auditSum += v; auditCount += 1; }
  }

  let activeContextUsed = 0;
  if (workspace.activeSessionID) {
    const activeTurns = workspace.messagesBySession[workspace.activeSessionID] || [];
    for (let i = activeTurns.length - 1; i >= 0; i--) {
      const tk = activeTurns[i].info.tokens;
      if (tk && (tk.input > 0 || tk.output > 0 || (tk.cache?.read ?? 0) > 0)) {
        activeContextUsed = (tk.input || 0) + (tk.output || 0) + (tk.reasoning || 0) + (tk.cache?.read || 0) + (tk.cache?.write || 0);
        break;
      }
    }
  }
  const activeContextLimit = activeModelInfo()?.limit?.context || 200_000;
  const activeContextPercent = Math.min(100, Math.round((activeContextUsed / activeContextLimit) * 100));

  const modelsUsed: DailyModelUsageItem[] = Array.from(byModel.values())
    .sort((a, b) => b.totalTokens - a.totalTokens || b.cost - a.cost)
    .slice(0, 4)
    .map((m) => ({
      ...m,
      sharePercent: totalTokens > 0 ? Math.max(1, Math.round((m.totalTokens / totalTokens) * 100)) : 100,
    }));

  return {
    dateKey: todayKey, totalTokens, inputTokens, outputTokens, reasoningTokens,
    cacheReadTokens, cacheWriteTokens, cacheHitRate, reasoningRatio,
    totalCost, savedByCache, avgCostPerTurn, totalAdditions, totalDeletions,
    netLines, changedFilesCount, todayCommits: git.todayCommits || 0,
    committedAdditions, committedDeletions, uncommittedAdditions, uncommittedDeletions,
    todaySessionsCount: sessionIDsToday.size, todayTurnsCount, errorTurns,
    toolCallsTotal, toolCallsFailed, toolSuccessRate, editToolCalls, bashToolCalls, readSearchToolCalls,
    avgTps: tpsCount > 0 ? Math.round((tpsSum / tpsCount) * 10) / 10 : 0,
    avgTtftMs: ttftCount > 0 ? Math.round(ttftSum / ttftCount) : 0,
    revertCount, avgAuditScore: auditCount > 0 ? Math.round(auditSum / auditCount) : null,
    activeContextUsed, activeContextLimit, activeContextPercent, modelsUsed,
  };
}

