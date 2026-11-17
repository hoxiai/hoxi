// web/src/store/wallpaper.ts
// Frosted glass & custom/preset wallpaper store for Hoxi Desktop.
// Metadata (including cached 48x27 regional/percentile luminance metrics) lives
// in localStorage; large user-uploaded images persist in IndexedDB.
import { createStore } from "solid-js/store";
import { readStorageJSON, workspace, writeStorageJSON } from "./workspace-state";
import {
  clampWallpaperInt,
  computeAdaptiveGlassMetrics,
  DEFAULT_BACKDROP_BLUR,
  DEFAULT_PANEL_OPACITY,
  DEFAULT_WALLPAPER_DIM,
  MAX_WALLPAPER_FILE_BYTES,
  probeImageLuminance,
  sanitizeWallpaperLuminance,
  UNMEASURED_WALLPAPER_LUMINANCE,
  WALLPAPER_BLUR_MAX,
  WALLPAPER_BLUR_MIN,
  WALLPAPER_DIM_MAX,
  WALLPAPER_DIM_MIN,
  WALLPAPER_OPACITY_MAX,
  WALLPAPER_OPACITY_MIN,
  WALLPAPER_PRESET_ART,
  WALLPAPER_PRESET_IDS,
  type WallpaperLuminance,
  type WallpaperMaterialMode,
  type WallpaperPresetID,
  type WallpaperSource,
} from "./wallpaper-optics";

export interface WallpaperState {
  enabled: boolean;
  source: WallpaperSource;
  presetId: WallpaperPresetID;
  customUrl: string;
  /** Ephemeral blob:/data: URL hydrated from IndexedDB for local uploads. */
  customObjectUrl: string;
  hasStoredCustomBlob: boolean;
  /** Cached luminance profile of the active custom/URL image. */
  customLuminance: WallpaperLuminance;
  /** Floating pane surface opacity percentage (58..96). */
  panelOpacity: number;
  /** Backdrop blur radius in pixels (0 = crystal mode, 1..40 = frosted mode). */
  backdropBlur: number;
  /** Protective canvas tint veil over wallpaper percentage (0..70). */
  wallpaperDim: number;
}

interface PersistedWallpaperMeta {
  enabled: boolean;
  source: WallpaperSource;
  presetId: WallpaperPresetID;
  customUrl: string;
  hasStoredCustomBlob: boolean;
  customLuminance?: WallpaperLuminance;
  panelOpacity: number;
  backdropBlur: number;
  wallpaperDim: number;
}

export const WALLPAPER_STORAGE_KEY = "hoxi:wallpaper:v3";
const LEGACY_WALLPAPER_KEYS = ["hoxi:wallpaper:v2", "hoxi:wallpaper:v1"] as const;

const IDB_NAME = "hoxi-wallpaper-store";
const IDB_STORE = "blobs";
const IDB_CUSTOM_KEY = "active-custom-wallpaper";

const DEFAULT_WALLPAPER_META: PersistedWallpaperMeta = {
  enabled: false,
  source: "preset",
  presetId: "academy-sky",
  customUrl: "",
  hasStoredCustomBlob: false,
  customLuminance: { ...UNMEASURED_WALLPAPER_LUMINANCE },
  panelOpacity: DEFAULT_PANEL_OPACITY,
  backdropBlur: DEFAULT_BACKDROP_BLUR,
  wallpaperDim: DEFAULT_WALLPAPER_DIM,
};

function isPresetID(val: unknown): val is WallpaperPresetID {
  return typeof val === "string" && (WALLPAPER_PRESET_IDS as string[]).includes(val);
}

function isSafeWallpaperUrl(url: string): boolean {
  const trimmed = url.trim();
  if (!trimmed) return false;
  return (
    trimmed.startsWith("https://") ||
    trimmed.startsWith("http://") ||
    trimmed.startsWith("data:image/") ||
    trimmed.startsWith("blob:")
  );
}

export function loadInitialWallpaperMeta(): PersistedWallpaperMeta {
  try {
    let parsed = readStorageJSON<Record<string, unknown>>(WALLPAPER_STORAGE_KEY);
    let fromLegacy = false;
    if (!parsed || typeof parsed !== "object") {
      for (const legacyKey of LEGACY_WALLPAPER_KEYS) {
        const candidate = readStorageJSON<Record<string, unknown>>(legacyKey);
        if (candidate && typeof candidate === "object") {
          parsed = candidate;
          fromLegacy = true;
          break;
        }
      }
    }
    if (!parsed || typeof parsed !== "object") {
      return {
        ...DEFAULT_WALLPAPER_META,
        customLuminance: { ...UNMEASURED_WALLPAPER_LUMINANCE },
      };
    }
    const source: WallpaperSource =
      parsed.source === "custom" || parsed.source === "url" || parsed.source === "preset"
        ? parsed.source
        : DEFAULT_WALLPAPER_META.source;
    const customUrl = typeof parsed.customUrl === "string" ? parsed.customUrl.trim() : "";
    const clampedOpacity = clampWallpaperInt(
      parsed.panelOpacity,
      WALLPAPER_OPACITY_MIN,
      WALLPAPER_OPACITY_MAX,
      DEFAULT_WALLPAPER_META.panelOpacity
    );
    const clampedBlur = clampWallpaperInt(
      parsed.backdropBlur,
      WALLPAPER_BLUR_MIN,
      WALLPAPER_BLUR_MAX,
      DEFAULT_WALLPAPER_META.backdropBlur
    );
    const clampedDim = clampWallpaperInt(
      parsed.wallpaperDim,
      WALLPAPER_DIM_MIN,
      WALLPAPER_DIM_MAX,
      DEFAULT_WALLPAPER_META.wallpaperDim
    );
    return {
      enabled: typeof parsed.enabled === "boolean" ? parsed.enabled : false,
      source,
      presetId: isPresetID(parsed.presetId) ? parsed.presetId : DEFAULT_WALLPAPER_META.presetId,
      customUrl: isSafeWallpaperUrl(customUrl) ? customUrl : "",
      hasStoredCustomBlob: Boolean(parsed.hasStoredCustomBlob),
      customLuminance: sanitizeWallpaperLuminance(parsed.customLuminance),
      panelOpacity: fromLegacy ? Math.max(DEFAULT_PANEL_OPACITY, clampedOpacity) : clampedOpacity,
      backdropBlur: fromLegacy ? DEFAULT_BACKDROP_BLUR : clampedBlur,
      wallpaperDim: fromLegacy ? Math.max(DEFAULT_WALLPAPER_DIM, clampedDim) : clampedDim,
    };
  } catch {
    return {
      ...DEFAULT_WALLPAPER_META,
      customLuminance: { ...UNMEASURED_WALLPAPER_LUMINANCE },
    };
  }
}

const initialMeta = loadInitialWallpaperMeta();

export const [wallpaperState, setWallpaperState] = createStore<WallpaperState>({
  ...initialMeta,
  customLuminance: initialMeta.customLuminance ?? { ...UNMEASURED_WALLPAPER_LUMINANCE },
  customObjectUrl: "",
});

export function activeWallpaperMaterialMode(): WallpaperMaterialMode {
  return wallpaperState.backdropBlur > 0 ? "frosted" : "crystal";
}

export function applyWallpaperMaterialMode(mode: WallpaperMaterialMode) {
  if (mode === "crystal") {
    setWallpaperState({
      backdropBlur: 0,
      panelOpacity: Math.max(wallpaperState.panelOpacity, DEFAULT_PANEL_OPACITY),
      wallpaperDim: Math.max(wallpaperState.wallpaperDim, DEFAULT_WALLPAPER_DIM),
    });
  } else {
    setWallpaperState({
      backdropBlur: wallpaperState.backdropBlur >= 12 ? wallpaperState.backdropBlur : DEFAULT_BACKDROP_BLUR,
      panelOpacity: Math.max(wallpaperState.panelOpacity, WALLPAPER_OPACITY_MIN),
    });
  }
  persistWallpaperMeta();
  syncWallpaperDOM();
}

function persistWallpaperMeta() {
  const payload: PersistedWallpaperMeta = {
    enabled: wallpaperState.enabled,
    source: wallpaperState.source,
    presetId: wallpaperState.presetId,
    customUrl: wallpaperState.customUrl,
    hasStoredCustomBlob: wallpaperState.hasStoredCustomBlob,
    customLuminance: { ...wallpaperState.customLuminance },
    panelOpacity: wallpaperState.panelOpacity,
    backdropBlur: wallpaperState.backdropBlur,
    wallpaperDim: wallpaperState.wallpaperDim,
  };
  writeStorageJSON(WALLPAPER_STORAGE_KEY, payload);
}

export function resolveActiveWallpaperUrl(): string {
  if (!wallpaperState.enabled) return "";
  if (wallpaperState.source === "custom") {
    if (wallpaperState.customObjectUrl) return wallpaperState.customObjectUrl;
    return WALLPAPER_PRESET_ART[wallpaperState.presetId].imageUri;
  }
  if (wallpaperState.source === "url") {
    if (isSafeWallpaperUrl(wallpaperState.customUrl)) {
      return wallpaperState.customUrl.trim();
    }
    return WALLPAPER_PRESET_ART[wallpaperState.presetId].imageUri;
  }
  return WALLPAPER_PRESET_ART[wallpaperState.presetId].imageUri;
}

export function resolveActiveWallpaperLuminance(): WallpaperLuminance {
  if (wallpaperState.source === "custom") {
    if (wallpaperState.customObjectUrl || wallpaperState.hasStoredCustomBlob) {
      return wallpaperState.customLuminance;
    }
    return WALLPAPER_PRESET_ART[wallpaperState.presetId].luminance;
  }
  if (wallpaperState.source === "url") {
    if (isSafeWallpaperUrl(wallpaperState.customUrl)) {
      return wallpaperState.customLuminance;
    }
    return WALLPAPER_PRESET_ART[wallpaperState.presetId].luminance;
  }
  return WALLPAPER_PRESET_ART[wallpaperState.presetId].luminance;
}

export function syncWallpaperDOM() {
  if (typeof document === "undefined") return;
  const root = document.documentElement;
  const active = Boolean(wallpaperState.enabled && resolveActiveWallpaperUrl());

  const activeTheme: "light" | "dark" =
    (typeof root.classList?.contains === "function" &&
      root.classList.contains("dark")) ||
    workspace.theme === "dark"
      ? "dark"
      : "light";
  const metrics = computeAdaptiveGlassMetrics(
    activeTheme,
    resolveActiveWallpaperLuminance(),
    wallpaperState.panelOpacity,
    wallpaperState.backdropBlur,
    wallpaperState.wallpaperDim
  );

  if (active) {
    root.dataset.wallpaperActive = "true";
    if (metrics.effectiveBlurPx > 0) {
      root.dataset.wallpaperBlur = "true";
    } else {
      delete root.dataset.wallpaperBlur;
    }
  } else {
    delete root.dataset.wallpaperActive;
    delete root.dataset.wallpaperBlur;
  }

  root.style.setProperty("--glass-surface-opacity", `${metrics.effectiveSurfaceOpacity}%`);
  root.style.setProperty("--glass-text-zone-opacity", `${metrics.effectiveTextZoneOpacity}%`);
  root.style.setProperty("--glass-art-zone-opacity", `${metrics.effectiveArtZoneOpacity}%`);
  root.style.setProperty("--glass-nav-opacity", `${metrics.effectiveNavOpacity}%`);
  root.style.setProperty("--glass-composer-opacity", `${metrics.effectiveComposerOpacity}%`);
  root.style.setProperty("--glass-header-opacity", `${metrics.effectiveHeaderOpacity}%`);
  root.style.setProperty("--glass-blur", `${metrics.effectiveBlurPx}px`);
  root.style.setProperty("--glass-backdrop-contrast", String(metrics.backdropContrast));
  root.style.setProperty("--glass-backdrop-saturate", String(metrics.backdropSaturate));
  root.style.setProperty("--wallpaper-img-contrast", String(metrics.imageContrast));
  root.style.setProperty("--wallpaper-img-brightness", String(metrics.imageBrightness));
  root.style.setProperty("--wallpaper-dim-opacity", metrics.effectiveDimOpacity.toFixed(2));
  root.style.setProperty("--wallpaper-header-scrim", metrics.headerScrimOpacity.toFixed(2));
}

let luminanceProbeGeneration = 0;

async function refreshCustomLuminance(url: string): Promise<void> {
  const gen = ++luminanceProbeGeneration;
  const measured = await probeImageLuminance(url);
  if (gen !== luminanceProbeGeneration) return;
  setWallpaperState("customLuminance", measured);
  persistWallpaperMeta();
  syncWallpaperDOM();
}

function revokeCurrentObjectUrl() {
  const prev = wallpaperState.customObjectUrl;
  if (prev && prev.startsWith("blob:") && typeof URL !== "undefined" && URL.revokeObjectURL) {
    try {
      URL.revokeObjectURL(prev);
    } catch {
      /* ignore */
    }
  }
}

function openWallpaperDB(): Promise<IDBDatabase | null> {
  if (typeof indexedDB === "undefined") return Promise.resolve(null);
  return new Promise((resolve) => {
    try {
      const req = indexedDB.open(IDB_NAME, 1);
      req.onupgradeneeded = () => {
        const db = req.result;
        if (!db.objectStoreNames.contains(IDB_STORE)) {
          db.createObjectStore(IDB_STORE);
        }
      };
      req.onsuccess = () => resolve(req.result);
      req.onerror = () => resolve(null);
    } catch {
      resolve(null);
    }
  });
}

async function writeCustomBlobToIDB(blob: Blob): Promise<boolean> {
  const db = await openWallpaperDB();
  if (!db) return false;
  return new Promise((resolve) => {
    try {
      const tx = db.transaction(IDB_STORE, "readwrite");
      tx.objectStore(IDB_STORE).put(blob, IDB_CUSTOM_KEY);
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

async function readCustomBlobFromIDB(): Promise<Blob | null> {
  const db = await openWallpaperDB();
  if (!db) return null;
  return new Promise((resolve) => {
    try {
      const tx = db.transaction(IDB_STORE, "readonly");
      const req = tx.objectStore(IDB_STORE).get(IDB_CUSTOM_KEY);
      req.onsuccess = () => {
        db.close();
        const result = req.result;
        resolve(result instanceof Blob ? result : null);
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

async function deleteCustomBlobFromIDB(): Promise<void> {
  const db = await openWallpaperDB();
  if (!db) return;
  return new Promise((resolve) => {
    try {
      const tx = db.transaction(IDB_STORE, "readwrite");
      tx.objectStore(IDB_STORE).delete(IDB_CUSTOM_KEY);
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

let hydrateGeneration = 0;

export async function hydrateWallpaperFromStorage(): Promise<void> {
  syncWallpaperDOM();
  if (wallpaperState.source === "url" && isSafeWallpaperUrl(wallpaperState.customUrl)) {
    if (!wallpaperState.customLuminance.measured) {
      void refreshCustomLuminance(wallpaperState.customUrl.trim());
    }
  }
  if (!wallpaperState.hasStoredCustomBlob) return;
  const gen = ++hydrateGeneration;
  const blob = await readCustomBlobFromIDB();
  if (gen !== hydrateGeneration) return;
  if (!blob || typeof URL === "undefined" || !URL.createObjectURL) {
    setWallpaperState("hasStoredCustomBlob", false);
    if (wallpaperState.source === "custom") {
      setWallpaperState("source", "preset");
    }
    persistWallpaperMeta();
    syncWallpaperDOM();
    return;
  }
  revokeCurrentObjectUrl();
  const objUrl = URL.createObjectURL(blob);
  setWallpaperState("customObjectUrl", objUrl);
  syncWallpaperDOM();
  if (!wallpaperState.customLuminance.measured) {
    await refreshCustomLuminance(objUrl);
  }
}

export function setWallpaperEnabled(enabled: boolean) {
  setWallpaperState("enabled", enabled);
  persistWallpaperMeta();
  syncWallpaperDOM();
}

export function selectWallpaperPreset(presetId: WallpaperPresetID) {
  if (!isPresetID(presetId)) return;
  ++luminanceProbeGeneration;
  setWallpaperState({
    enabled: true,
    source: "preset",
    presetId,
  });
  persistWallpaperMeta();
  syncWallpaperDOM();
}

export function applyWallpaperCustomUrl(rawUrl: string): boolean {
  const trimmed = rawUrl.trim();
  if (!isSafeWallpaperUrl(trimmed)) return false;
  setWallpaperState({
    enabled: true,
    source: "url",
    customUrl: trimmed,
    customLuminance: { ...UNMEASURED_WALLPAPER_LUMINANCE },
  });
  persistWallpaperMeta();
  syncWallpaperDOM();
  void refreshCustomLuminance(trimmed);
  return true;
}

export async function uploadCustomWallpaperFile(
  file: File
): Promise<{ ok: true } | { ok: false; reason: "invalid_type" | "too_large" | "storage_failed" }> {
  if (!file.type.startsWith("image/")) {
    return { ok: false, reason: "invalid_type" };
  }
  if (file.size > MAX_WALLPAPER_FILE_BYTES) {
    return { ok: false, reason: "too_large" };
  }
  const gen = ++hydrateGeneration;
  const stored = await writeCustomBlobToIDB(file);
  if (gen !== hydrateGeneration) {
    return { ok: true };
  }
  if (typeof URL !== "undefined" && URL.createObjectURL) {
    revokeCurrentObjectUrl();
    const objUrl = URL.createObjectURL(file);
    setWallpaperState({
      enabled: true,
      source: "custom",
      customObjectUrl: objUrl,
      hasStoredCustomBlob: stored,
      customLuminance: { ...UNMEASURED_WALLPAPER_LUMINANCE },
    });
    persistWallpaperMeta();
    syncWallpaperDOM();
    await refreshCustomLuminance(objUrl);
    return { ok: true };
  }
  return { ok: false, reason: "storage_failed" };
}

export async function clearCustomWallpaper(): Promise<void> {
  ++hydrateGeneration;
  ++luminanceProbeGeneration;
  revokeCurrentObjectUrl();
  setWallpaperState({
    source: "preset",
    customUrl: "",
    customObjectUrl: "",
    hasStoredCustomBlob: false,
    customLuminance: { ...UNMEASURED_WALLPAPER_LUMINANCE },
  });
  persistWallpaperMeta();
  syncWallpaperDOM();
  await deleteCustomBlobFromIDB();
}

export function updateWallpaperPanelOpacity(value: number) {
  setWallpaperState(
    "panelOpacity",
    clampWallpaperInt(
      value,
      WALLPAPER_OPACITY_MIN,
      WALLPAPER_OPACITY_MAX,
      DEFAULT_WALLPAPER_META.panelOpacity
    )
  );
  persistWallpaperMeta();
  syncWallpaperDOM();
}

export function updateWallpaperBackdropBlur(value: number) {
  setWallpaperState(
    "backdropBlur",
    clampWallpaperInt(
      value,
      WALLPAPER_BLUR_MIN,
      WALLPAPER_BLUR_MAX,
      DEFAULT_WALLPAPER_META.backdropBlur
    )
  );
  persistWallpaperMeta();
  syncWallpaperDOM();
}

export function updateWallpaperDim(value: number) {
  setWallpaperState(
    "wallpaperDim",
    clampWallpaperInt(
      value,
      WALLPAPER_DIM_MIN,
      WALLPAPER_DIM_MAX,
      DEFAULT_WALLPAPER_META.wallpaperDim
    )
  );
  persistWallpaperMeta();
  syncWallpaperDOM();
}

export function resetWallpaperSliders() {
  setWallpaperState({
    panelOpacity: DEFAULT_WALLPAPER_META.panelOpacity,
    backdropBlur: DEFAULT_WALLPAPER_META.backdropBlur,
    wallpaperDim: DEFAULT_WALLPAPER_META.wallpaperDim,
  });
  persistWallpaperMeta();
  syncWallpaperDOM();
}

export async function resetAllWallpaperSettings(): Promise<void> {
  ++hydrateGeneration;
  ++luminanceProbeGeneration;
  revokeCurrentObjectUrl();
  setWallpaperState({
    ...DEFAULT_WALLPAPER_META,
    customLuminance: { ...UNMEASURED_WALLPAPER_LUMINANCE },
    customObjectUrl: "",
  });
  persistWallpaperMeta();
  syncWallpaperDOM();
  await deleteCustomBlobFromIDB();
}
