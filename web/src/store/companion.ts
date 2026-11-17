// web/src/store/companion.ts
// State-driven ACGN workspace companion & welcome stage store.
// Subscribes reactively to session streaming, parallel Worktree convoys,
// upstream retry/stall signals, and PreToolUse high-risk command gates.
import { createStore } from "solid-js/store";
import { hoxiAPI, type DesktopCompanionPayload } from "../services/api";
import type { PermissionRequest } from "../services/sse-types";
import { computeDailyDevPulseStats } from "./daily-pulse";
import { getSessionSpawnedConvoyPlans } from "./orchestration";
import { readStorageJSON, workspace, writeStorageJSON } from "./workspace-state";

export type CompanionPersona = "hoxi-chan" | "neko-bot" | "custom";
export type CompanionMood =
  | "gate-danger"
  | "gate-approval"
  | "failover"
  | "convoy"
  | "coding"
  | "celebrate"
  | "idle";
export type CompanionTimeSlot = "morning" | "afternoon" | "evening" | "lateNight";

export interface CompanionState {
  enabled: boolean;
  persona: CompanionPersona;
  showWelcomeStage: boolean;
  showCornerDock: boolean;
  showSpeechBubble: boolean;
  minimized: boolean;
  nativeDesktopSupported: boolean;
  customAvatarObjectUrl: string;
  customAvatarDataUrl: string;
  hasStoredCustomAvatar: boolean;
  pokeCount: number;
  lastPokeAt: number;
}

interface PersistedCompanionMeta {
  enabled: boolean;
  persona: CompanionPersona;
  showWelcomeStage: boolean;
  showCornerDock: boolean;
  showSpeechBubble: boolean;
  minimized: boolean;
  hasStoredCustomAvatar: boolean;
}

export interface CompanionSnapshot {
  mood: CompanionMood;
  targetSessionID: string | null;
  gateTarget: string;
  pendingCount: number;
  parallelCount: number;
  retryAttempt: number;
  timeSlot: CompanionTimeSlot;
  isPoked: boolean;
  pokeSlot: 1 | 2 | 3 | 4;
  tokenLevel: number;
  levelProgressPct: number;
  todayTokens: number;
}

export const COMPANION_STORAGE_KEY = "hoxi:companion:v1";
export const MAX_COMPANION_AVATAR_BYTES = 15 * 1024 * 1024;
export const CELEBRATE_WINDOW_MS = 12_000;
export const POKE_BUBBLE_WINDOW_MS = 9_000;

const COMPANION_IDB_NAME = "hoxi-companion-store";
const COMPANION_IDB_STORE = "avatars";
const COMPANION_IDB_KEY = "active-custom-standee";

const DEFAULT_COMPANION_META: PersistedCompanionMeta = {
  enabled: false,
  persona: "hoxi-chan",
  showWelcomeStage: true,
  showCornerDock: true,
  showSpeechBubble: true,
  minimized: false,
  hasStoredCustomAvatar: false,
};

const DESTRUCTIVE_CMD_RE =
  /(?:^|[\s;|&`(])(rm|sudo|chmod|chown|diskutil|mkfs|dd|killall|pkill|kill)(?:$|[\s;|&`)])|--force\b|\s-[a-zA-Z]*r[a-zA-Z]*f\b|\s-[a-zA-Z]*f[a-zA-Z]*r\b|\bgit\s+(?:reset\s+--hard|clean\s+-[a-zA-Z]*f|push\s+[^\n]*--force)|(?:^|[\/\\])(?:\.env(?:\.[a-zA-Z0-9_-]+)?|\.ssh|id_rsa|id_ed25519)|(?:\.pem|\.key|\.p12|\.pfx)\b/i;

function isPersona(val: unknown): val is CompanionPersona {
  return val === "hoxi-chan" || val === "neko-bot" || val === "custom";
}

export function loadInitialCompanionMeta(): PersistedCompanionMeta {
  try {
    const parsed = readStorageJSON<Record<string, unknown>>(COMPANION_STORAGE_KEY);
    if (!parsed || typeof parsed !== "object") {
      return { ...DEFAULT_COMPANION_META };
    }
    return {
      enabled: typeof parsed.enabled === "boolean" ? parsed.enabled : false,
      persona: isPersona(parsed.persona) ? parsed.persona : DEFAULT_COMPANION_META.persona,
      showWelcomeStage:
        typeof parsed.showWelcomeStage === "boolean"
          ? parsed.showWelcomeStage
          : DEFAULT_COMPANION_META.showWelcomeStage,
      showCornerDock:
        typeof parsed.showCornerDock === "boolean"
          ? parsed.showCornerDock
          : DEFAULT_COMPANION_META.showCornerDock,
      showSpeechBubble:
        typeof parsed.showSpeechBubble === "boolean"
          ? parsed.showSpeechBubble
          : DEFAULT_COMPANION_META.showSpeechBubble,
      minimized:
        typeof parsed.minimized === "boolean"
          ? parsed.minimized
          : DEFAULT_COMPANION_META.minimized,
      hasStoredCustomAvatar: Boolean(parsed.hasStoredCustomAvatar),
    };
  } catch {
    return { ...DEFAULT_COMPANION_META };
  }
}

function detectInitialWailsHost(): boolean {
  if (typeof window === "undefined") return false;
  const proto = window.location?.protocol || "";
  const host = window.location?.hostname || "";
  if (proto === "wails:" || host === "wails.localhost" || host.endsWith(".wails.localhost")) {
    return true;
  }
  const w = window as unknown as Record<string, unknown>;
  return Boolean(w.wails || w.runtime);
}

export const [companionState, setCompanionState] = createStore<CompanionState>({
  ...loadInitialCompanionMeta(),
  nativeDesktopSupported: detectInitialWailsHost(),
  customAvatarObjectUrl: "",
  customAvatarDataUrl: "",
  pokeCount: 0,
  lastPokeAt: 0,
});

function persistCompanionMeta() {
  const payload: PersistedCompanionMeta = {
    enabled: companionState.enabled,
    persona: companionState.persona,
    showWelcomeStage: companionState.showWelcomeStage,
    showCornerDock: companionState.showCornerDock,
    showSpeechBubble: companionState.showSpeechBubble,
    minimized: companionState.minimized,
    hasStoredCustomAvatar: companionState.hasStoredCustomAvatar,
  };
  writeStorageJSON(COMPANION_STORAGE_KEY, payload);
}

export function isHighRiskPermissionRequest(req: PermissionRequest): boolean {
  if (!req) return false;
  if (req.metadata?.destructive === true) return true;
  if (req.permission === "doom_loop" || req.permission === "unsandboxed") return true;
  const parts = [
    req.permission || "",
    ...(Array.isArray(req.patterns) ? req.patterns : []),
    typeof req.metadata?.command === "string" ? req.metadata.command : "",
    typeof req.metadata?.summary === "string" ? req.metadata.summary : "",
    typeof req.metadata?.path === "string" ? req.metadata.path : "",
  ];
  return DESTRUCTIVE_CMD_RE.test(parts.join(" "));
}

export function summarizePermissionTarget(req: PermissionRequest): string {
  const candidate =
    (typeof req.metadata?.command === "string" && req.metadata.command.trim()) ||
    (typeof req.metadata?.summary === "string" && req.metadata.summary.trim()) ||
    (Array.isArray(req.patterns) && req.patterns[0]?.trim()) ||
    req.permission ||
    "tool";
  const singleLine = candidate.replace(/\s+/g, " ").trim();
  return singleLine.length > 30 ? `${singleLine.slice(0, 29)}…` : singleLine;
}

export function resolveTimeSlot(now = new Date()): CompanionTimeSlot {
  const hour = now.getHours();
  if (hour >= 5 && hour < 12) return "morning";
  if (hour >= 12 && hour < 18) return "afternoon";
  if (hour >= 18 && hour < 23) return "evening";
  return "lateNight";
}

export function computeCompanionTokenLevel(totalTokens: number): {
  level: number;
  progressPct: number;
} {
  const safe = Math.max(0, Math.floor(totalTokens || 0));
  const raw = Math.sqrt(safe / 2500);
  const level = Math.min(99, 1 + Math.floor(raw));
  if (level >= 99) return { level: 99, progressPct: 100 };
  const frac = raw - Math.floor(raw);
  return { level, progressPct: Math.min(99, Math.max(4, Math.round(frac * 100))) };
}

export function formatCompactTokens(tokens: number): string {
  const n = Math.max(0, Math.floor(tokens || 0));
  if (n >= 1_000_000) return `${(n / 1_000_000).toFixed(1)}M`;
  if (n >= 1_000) return `${(n / 1_000).toFixed(1)}k`;
  return String(n);
}

export function deriveCompanionSnapshot(nowMs = Date.now()): CompanionSnapshot {
  const activeSid = workspace.activeSessionID;
  const permissions = workspace.pendingPermissions;
  const questions = workspace.pendingQuestions;
  const totalPending = permissions.length + questions.length;

  let todayTokens = 0;
  try {
    todayTokens = computeDailyDevPulseStats().totalTokens || 0;
  } catch {
    todayTokens = 0;
  }
  const { level: tokenLevel, progressPct: levelProgressPct } =
    computeCompanionTokenLevel(todayTokens);
  const timeSlot = resolveTimeSlot(new Date(nowMs));
  const isPoked =
    companionState.lastPokeAt > 0 && nowMs - companionState.lastPokeAt < POKE_BUBBLE_WINDOW_MS;
  const pokeSlot = (((Math.max(1, companionState.pokeCount) - 1) % 4) + 1) as 1 | 2 | 3 | 4;

  // 1. High-risk command interception gate (highest priority)
  const focusedDanger =
    permissions.find((p) => p.sessionID === activeSid && isHighRiskPermissionRequest(p)) ||
    permissions.find((p) => isHighRiskPermissionRequest(p));
  if (focusedDanger) {
    return {
      mood: "gate-danger",
      targetSessionID: focusedDanger.sessionID,
      gateTarget: summarizePermissionTarget(focusedDanger),
      pendingCount: totalPending,
      parallelCount: 0,
      retryAttempt: 0,
      timeSlot,
      isPoked: false,
      pokeSlot,
      tokenLevel,
      levelProgressPct,
      todayTokens,
    };
  }

  // 2. Standard approval or pending clarifying question
  if (totalPending > 0) {
    const firstPerm =
      permissions.find((p) => p.sessionID === activeSid) || permissions[0];
    const firstQuestion =
      questions.find((q) => q.sessionID === activeSid) || questions[0];
    const targetSessionID = firstPerm?.sessionID || firstQuestion?.sessionID || null;
    return {
      mood: "gate-approval",
      targetSessionID,
      gateTarget: firstPerm ? summarizePermissionTarget(firstPerm) : "question",
      pendingCount: totalPending,
      parallelCount: 0,
      retryAttempt: 0,
      timeSlot,
      isPoked: false,
      pokeSlot,
      tokenLevel,
      levelProgressPct,
      todayTokens,
    };
  }

  // 3. Upstream retry / auto-failover / stall detection
  const activeStatus = activeSid ? workspace.statusBySession[activeSid] : undefined;
  if (activeStatus?.type === "retry") {
    return {
      mood: "failover",
      targetSessionID: activeSid,
      gateTarget: "",
      pendingCount: 0,
      parallelCount: 0,
      retryAttempt: Math.max(1, activeStatus.attempt || 1),
      timeSlot,
      isPoked: false,
      pokeSlot,
      tokenLevel,
      levelProgressPct,
      todayTokens,
    };
  }
  if (activeSid && (workspace.stalledSinceBySession[activeSid] || 0) > 0) {
    return {
      mood: "failover",
      targetSessionID: activeSid,
      gateTarget: "",
      pendingCount: 0,
      parallelCount: 0,
      retryAttempt: 1,
      timeSlot,
      isPoked: false,
      pokeSlot,
      tokenLevel,
      levelProgressPct,
      todayTokens,
    };
  }
  for (const [sid, st] of Object.entries(workspace.statusBySession)) {
    if (st?.type === "retry") {
      return {
        mood: "failover",
        targetSessionID: sid,
        gateTarget: "",
        pendingCount: 0,
        parallelCount: 0,
        retryAttempt: Math.max(1, st.attempt || 1),
        timeSlot,
        isPoked: false,
        pokeSlot,
        tokenLevel,
        levelProgressPct,
        todayTokens,
      };
    }
  }

  // 4. Parallel Worktree Convoy / Multi-Session Arena
  const busySessionIDs = Object.entries(workspace.statusBySession)
    .filter(([, st]) => st?.type === "busy")
    .map(([sid]) => sid);
  const convoyPlans = getSessionSpawnedConvoyPlans(activeSid);
  const runningConvoyTasks = convoyPlans.reduce(
    (acc, plan) =>
      acc +
      (plan.status === "running"
        ? plan.tasks.filter((task) => task.status === "running").length
        : 0),
    0
  );
  const parallelCount = Math.max(busySessionIDs.length, runningConvoyTasks);
  if (workspace.arenaModalOpen || parallelCount >= 2) {
    return {
      mood: "convoy",
      targetSessionID: activeSid || busySessionIDs[0] || null,
      gateTarget: "",
      pendingCount: 0,
      parallelCount: Math.max(2, parallelCount),
      retryAttempt: 0,
      timeSlot,
      isPoked: false,
      pokeSlot,
      tokenLevel,
      levelProgressPct,
      todayTokens,
    };
  }

  // 5. Focused single-session streaming / tool execution
  if (busySessionIDs.length === 1) {
    return {
      mood: "coding",
      targetSessionID: busySessionIDs[0],
      gateTarget: "",
      pendingCount: 0,
      parallelCount: 1,
      retryAttempt: 0,
      timeSlot,
      isPoked: false,
      pokeSlot,
      tokenLevel,
      levelProgressPct,
      todayTokens,
    };
  }

  // 6. Recent turn completion celebration window
  const finishedAt = activeSid ? workspace.sessionFinishedAt[activeSid] || 0 : 0;
  if (finishedAt > 0 && nowMs - finishedAt >= 0 && nowMs - finishedAt < CELEBRATE_WINDOW_MS) {
    return {
      mood: "celebrate",
      targetSessionID: activeSid,
      gateTarget: "",
      pendingCount: 0,
      parallelCount: 0,
      retryAttempt: 0,
      timeSlot,
      isPoked: false,
      pokeSlot,
      tokenLevel,
      levelProgressPct,
      todayTokens,
    };
  }

  // 7. Calm standby
  return {
    mood: "idle",
    targetSessionID: activeSid,
    gateTarget: "",
    pendingCount: 0,
    parallelCount: 0,
    retryAttempt: 0,
    timeSlot,
    isPoked,
    pokeSlot,
    tokenLevel,
    levelProgressPct,
    todayTokens,
  };
}

export function setCompanionEnabled(enabled: boolean) {
  setCompanionState("enabled", enabled);
  persistCompanionMeta();
}

export function setCompanionPersona(persona: CompanionPersona) {
  if (!isPersona(persona)) return;
  setCompanionState({
    enabled: true,
    persona,
  });
  persistCompanionMeta();
}

export function setCompanionWelcomeStage(show: boolean) {
  setCompanionState("showWelcomeStage", show);
  persistCompanionMeta();
}

export function setCompanionCornerDock(show: boolean) {
  setCompanionState("showCornerDock", show);
  persistCompanionMeta();
}

export function setCompanionSpeechBubble(show: boolean) {
  setCompanionState("showSpeechBubble", show);
  persistCompanionMeta();
}

export function setCompanionMinimized(minimized: boolean) {
  setCompanionState("minimized", minimized);
  persistCompanionMeta();
}

export function pokeCompanion(nowMs = Date.now()) {
  setCompanionState({
    pokeCount: companionState.pokeCount + 1,
    lastPokeAt: nowMs,
  });
}

function revokeCustomAvatarUrl() {
  const prev = companionState.customAvatarObjectUrl;
  if (prev && prev.startsWith("blob:") && typeof URL !== "undefined" && URL.revokeObjectURL) {
    try {
      URL.revokeObjectURL(prev);
    } catch {
      /* ignore */
    }
  }
}

function openCompanionDB(): Promise<IDBDatabase | null> {
  if (typeof indexedDB === "undefined") return Promise.resolve(null);
  return new Promise((resolve) => {
    try {
      const req = indexedDB.open(COMPANION_IDB_NAME, 1);
      req.onupgradeneeded = () => {
        const db = req.result;
        if (!db.objectStoreNames.contains(COMPANION_IDB_STORE)) {
          db.createObjectStore(COMPANION_IDB_STORE);
        }
      };
      req.onsuccess = () => resolve(req.result);
      req.onerror = () => resolve(null);
    } catch {
      resolve(null);
    }
  });
}

async function writeStandeeBlob(blob: Blob): Promise<boolean> {
  const db = await openCompanionDB();
  if (!db) return false;
  return new Promise((resolve) => {
    try {
      const tx = db.transaction(COMPANION_IDB_STORE, "readwrite");
      tx.objectStore(COMPANION_IDB_STORE).put(blob, COMPANION_IDB_KEY);
      tx.oncomplete = () => {
        db.close();
        resolve(true);
      };
      tx.onerror = () => {
        db.close();
        resolve(false);
      };
    } catch {
      db.close();
      resolve(false);
    }
  });
}

async function readStandeeBlob(): Promise<Blob | null> {
  const db = await openCompanionDB();
  if (!db) return null;
  return new Promise((resolve) => {
    try {
      const tx = db.transaction(COMPANION_IDB_STORE, "readonly");
      const req = tx.objectStore(COMPANION_IDB_STORE).get(COMPANION_IDB_KEY);
      req.onsuccess = () => {
        db.close();
        resolve(req.result instanceof Blob ? req.result : null);
      };
      req.onerror = () => {
        db.close();
        resolve(null);
      };
    } catch {
      db.close();
      resolve(null);
    }
  });
}

async function deleteStandeeBlob(): Promise<void> {
  const db = await openCompanionDB();
  if (!db) return;
  return new Promise((resolve) => {
    try {
      const tx = db.transaction(COMPANION_IDB_STORE, "readwrite");
      tx.objectStore(COMPANION_IDB_STORE).delete(COMPANION_IDB_KEY);
      tx.oncomplete = () => {
        db.close();
        resolve();
      };
      tx.onerror = () => {
        db.close();
        resolve();
      };
    } catch {
      db.close();
      resolve();
    }
  });
}

function blobToRawDataUrl(blob: Blob): Promise<string> {
  if (typeof FileReader === "undefined") return Promise.resolve("");
  return new Promise((resolve) => {
    const reader = new FileReader();
    reader.onload = () => resolve(typeof reader.result === "string" ? reader.result : "");
    reader.onerror = () => resolve("");
    reader.readAsDataURL(blob);
  });
}

async function encodeBlobForNativePet(blob: Blob, objectUrl: string): Promise<string> {
  if (blob.type === "image/gif") {
    return blobToRawDataUrl(blob);
  }
  if (
    !objectUrl ||
    typeof Image === "undefined" ||
    typeof document === "undefined" ||
    typeof document.createElement !== "function"
  ) {
    return blobToRawDataUrl(blob);
  }
  return new Promise((resolve) => {
    const img = new Image();
    img.onload = () => {
      try {
        const size = 264;
        const canvas = document.createElement("canvas");
        canvas.width = size;
        canvas.height = size;
        const ctx = canvas.getContext("2d");
        if (!ctx) {
          void blobToRawDataUrl(blob).then(resolve);
          return;
        }
        const scale = Math.min(
          size / Math.max(1, img.naturalWidth),
          size / Math.max(1, img.naturalHeight)
        );
        const w = img.naturalWidth * scale;
        const h = img.naturalHeight * scale;
        ctx.drawImage(img, (size - w) / 2, (size - h) / 2, w, h);
        resolve(canvas.toDataURL("image/png"));
      } catch {
        void blobToRawDataUrl(blob).then(resolve);
      }
    };
    img.onerror = () => void blobToRawDataUrl(blob).then(resolve);
    img.src = objectUrl;
  });
}

let avatarHydrateGen = 0;
let lastSyncedAvatarKey = "";
let desktopSyncSeq = 0;

export async function syncCompanionToDesktopHost(
  basePayload: DesktopCompanionPayload,
  resolveAvatarDataURL: () => Promise<string>
): Promise<void> {
  if (typeof window === "undefined" || typeof fetch !== "function") return;
  const seq = ++desktopSyncSeq;
  try {
    let avatarDataURL: string | undefined;
    if (
      basePayload.visible &&
      basePayload.avatarKey &&
      basePayload.avatarKey !== lastSyncedAvatarKey
    ) {
      avatarDataURL = (await resolveAvatarDataURL()) || undefined;
    }
    if (seq !== desktopSyncSeq) return;
    const res = await hoxiAPI.syncDesktopCompanion({
      ...basePayload,
      ...(avatarDataURL ? { avatarDataURL } : {}),
    });
    if (seq !== desktopSyncSeq) return;
    setCompanionState("nativeDesktopSupported", Boolean(res?.supported));
    if (!res?.supported) return;
    if (avatarDataURL && !res.needAvatar) {
      lastSyncedAvatarKey = basePayload.avatarKey;
    } else if (res.needAvatar && basePayload.visible && basePayload.avatarKey) {
      const retryDataUrl = await resolveAvatarDataURL();
      if (seq !== desktopSyncSeq || !retryDataUrl) return;
      const res2 = await hoxiAPI.syncDesktopCompanion({
        ...basePayload,
        avatarDataURL: retryDataUrl,
      });
      if (seq === desktopSyncSeq && res2?.supported && !res2.needAvatar) {
        lastSyncedAvatarKey = basePayload.avatarKey;
      }
    }
  } catch {
    /* headless or unit test environment without loopback server */
  }
}

export async function resetCompanionDesktopPosition(): Promise<void> {
  try {
    await hoxiAPI.resetDesktopCompanionPosition();
  } catch {
    /* ignore */
  }
}

export async function hydrateCompanionFromStorage(): Promise<void> {
  if (!companionState.hasStoredCustomAvatar) return;
  const gen = ++avatarHydrateGen;
  const blob = await readStandeeBlob();
  if (gen !== avatarHydrateGen) return;
  if (!blob || typeof URL === "undefined" || !URL.createObjectURL) {
    setCompanionState({
      hasStoredCustomAvatar: false,
      persona: companionState.persona === "custom" ? "hoxi-chan" : companionState.persona,
    });
    persistCompanionMeta();
    return;
  }
  revokeCustomAvatarUrl();
  const objUrl = URL.createObjectURL(blob);
  const dataUrl = await encodeBlobForNativePet(blob, objUrl);
  if (gen !== avatarHydrateGen) return;
  setCompanionState({
    customAvatarObjectUrl: objUrl,
    customAvatarDataUrl: dataUrl,
  });
}

export async function uploadCustomCompanionStandee(
  file: File
): Promise<{ ok: true } | { ok: false; reason: "invalid_type" | "too_large" | "storage_failed" }> {
  if (!file.type.startsWith("image/")) {
    return { ok: false, reason: "invalid_type" };
  }
  if (file.size > MAX_COMPANION_AVATAR_BYTES) {
    return { ok: false, reason: "too_large" };
  }
  const gen = ++avatarHydrateGen;
  const stored = await writeStandeeBlob(file);
  if (gen !== avatarHydrateGen) return { ok: true };
  if (typeof URL !== "undefined" && URL.createObjectURL) {
    revokeCustomAvatarUrl();
    const objUrl = URL.createObjectURL(file);
    const dataUrl = await encodeBlobForNativePet(file, objUrl);
    if (gen !== avatarHydrateGen) return { ok: true };
    lastSyncedAvatarKey = "";
    setCompanionState({
      enabled: true,
      persona: "custom",
      customAvatarObjectUrl: objUrl,
      customAvatarDataUrl: dataUrl,
      hasStoredCustomAvatar: stored,
    });
    persistCompanionMeta();
    return { ok: true };
  }
  return { ok: false, reason: "storage_failed" };
}

export async function clearCustomCompanionStandee(): Promise<void> {
  ++avatarHydrateGen;
  lastSyncedAvatarKey = "";
  revokeCustomAvatarUrl();
  setCompanionState({
    persona: "hoxi-chan",
    customAvatarObjectUrl: "",
    customAvatarDataUrl: "",
    hasStoredCustomAvatar: false,
  });
  persistCompanionMeta();
  await deleteStandeeBlob();
}

export async function resetAllCompanionSettings(): Promise<void> {
  ++avatarHydrateGen;
  lastSyncedAvatarKey = "";
  revokeCustomAvatarUrl();
  setCompanionState({
    ...DEFAULT_COMPANION_META,
    customAvatarObjectUrl: "",
    customAvatarDataUrl: "",
    pokeCount: 0,
    lastPokeAt: 0,
  });
  persistCompanionMeta();
  await deleteStandeeBlob();
}
