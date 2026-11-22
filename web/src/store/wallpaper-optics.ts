// web/src/store/wallpaper-optics.ts
// Pure optical luminance, tail-percentile contrast hazard, and adaptive glass
// parameter engine for Hoxi Desktop wallpapers (zero store imports).

export type WallpaperSource = "preset" | "custom" | "url";
export type WallpaperMaterialMode = "crystal" | "frosted";
export type WallpaperPresetID =
  | "academy-sky"
  | "cyber-shibuya"
  | "sakura-breeze"
  | "obsidian-gold";

export interface WallpaperLuminance {
  /** Overall WCAG sRGB relative luminance (0..1). */
  mean: number;
  /** Top header band (top ~20% of image) relative luminance (0..1). */
  top: number;
  /** 10th-percentile darkest patch luminance (0..1) — guards dark text in Light mode. */
  p10: number;
  /** 90th-percentile brightest patch luminance (0..1) — guards light text in Dark mode. */
  p90: number;
  /** Standard deviation of luminance (0..0.5), capturing harsh line-art/highlight contrast. */
  spread: number;
  /** True when derived from preset metadata or Canvas readback; false on opaque cross-origin URLs. */
  measured: boolean;
}

export interface AdaptiveGlassMetrics {
  materialMode: WallpaperMaterialMode;
  effectiveSurfaceOpacity: number;
  effectiveTextZoneOpacity: number;
  effectiveArtZoneOpacity: number;
  effectiveNavOpacity: number;
  effectiveComposerOpacity: number;
  effectiveHeaderOpacity: number;
  effectiveBlurPx: number;
  effectiveDimOpacity: number;
  headerScrimOpacity: number;
  backdropContrast: number;
  backdropSaturate: number;
  imageContrast: number;
  imageBrightness: number;
  polarityMatched: boolean;
}

export const MAX_WALLPAPER_FILE_BYTES = 25 * 1024 * 1024;
export const WALLPAPER_OPACITY_MIN = 58;
export const WALLPAPER_OPACITY_MAX = 96;
export const WALLPAPER_BLUR_MIN = 0;
export const WALLPAPER_BLUR_MAX = 40;
export const WALLPAPER_DIM_MIN = 0;
export const WALLPAPER_DIM_MAX = 70;

export const DEFAULT_PANEL_OPACITY = 78;
export const DEFAULT_BACKDROP_BLUR = 18;
export const DEFAULT_WALLPAPER_DIM = 20;

export const UNMEASURED_WALLPAPER_LUMINANCE: WallpaperLuminance = {
  mean: 0.5,
  top: 0.5,
  p10: 0.12,
  p90: 0.88,
  spread: 0.3,
  measured: false,
};

export const WALLPAPER_PRESET_IDS: WallpaperPresetID[] = [
  "academy-sky",
  "cyber-shibuya",
  "sakura-breeze",
  "obsidian-gold",
];

export interface WallpaperPresetConfig {
  previewGradient: string;
  imageUri: string;
  /** Optional 2x lossless RGBA character standee layered over the vector SVG backdrop. */
  standeeUri?: string;
  /** Horizontal CSS anchor for the standee layer (defaults to "84%"). */
  standeePositionX?: string;
  /** Responsive viewport CSS `background-size` for the standee (caps width on narrow split-screen windows while keeping ~92vh on widescreen/ultrawide). */
  standeeViewportSize?: string;
  recommendedScheme: string;
  recommendedThemeMode: "light" | "dark";
  luminance: WallpaperLuminance;
}

export const WALLPAPER_PRESET_ART: Record<WallpaperPresetID, WallpaperPresetConfig> = {
  "academy-sky": {
    recommendedScheme: "blue",
    recommendedThemeMode: "light",
    luminance: { mean: 0.56, top: 0.57, p10: 0.2, p90: 0.9, spread: 0.27, measured: true },
    previewGradient: "linear-gradient(135deg, #0284c7 0%, #7dd3fc 52%, #f0f9ff 100%)",
    imageUri: "/wallpapers/academy-sky.svg",
    standeeUri: "/wallpapers/academy-sky-standee.png",
    standeePositionX: "84%",
    standeeViewportSize: "min(43%, 54vh) auto",
  },
  "cyber-shibuya": {
    recommendedScheme: "cyber",
    recommendedThemeMode: "dark",
    luminance: { mean: 0.12, top: 0.08, p10: 0.03, p90: 0.76, spread: 0.25, measured: true },
    previewGradient: "linear-gradient(135deg, #0f172a 0%, #3b0764 52%, #0891b2 100%)",
    imageUri: "/wallpapers/cyber-shibuya.svg",
    standeeUri: "/wallpapers/cyber-shibuya-standee.png",
    standeePositionX: "84%",
    standeeViewportSize: "min(40%, 51vh) auto",
  },
  "sakura-breeze": {
    recommendedScheme: "sakura",
    recommendedThemeMode: "light",
    luminance: { mean: 0.34, top: 0.21, p10: 0.1, p90: 0.88, spread: 0.27, measured: true },
    previewGradient: "linear-gradient(135deg, #831843 0%, #ec4899 52%, #fde68a 100%)",
    imageUri: "/wallpapers/sakura-breeze.svg",
    standeeUri: "/wallpapers/sakura-breeze-standee.png",
    standeePositionX: "85%",
    standeeViewportSize: "min(44%, 60vh) auto",
  },
  "obsidian-gold": {
    recommendedScheme: "obsidian",
    recommendedThemeMode: "dark",
    luminance: { mean: 0.09, top: 0.06, p10: 0.02, p90: 0.8, spread: 0.24, measured: true },
    previewGradient: "linear-gradient(135deg, #09090b 0%, #1c1917 58%, #d97706 100%)",
    imageUri: "/wallpapers/obsidian-gold.svg",
    standeeUri: "/wallpapers/obsidian-gold-standee.png",
    standeePositionX: "86%",
    standeeViewportSize: "min(46%, 68vh) auto",
  },
};

function escapeCssUrl(raw: string): string {
  return raw.replace(/[\r\n]/g, "").replace(/\\/g, "\\\\").replace(/"/g, '\\"');
}

export function composeLayeredWallpaperStyle(
  backdropUrl: string,
  standeeUrl?: string,
  standeePositionX = "84%",
  backdropPosition = "72% center",
  options?: {
    standeeSize?: string;
    fallbackGradient?: string;
  }
): Record<string, string> {
  const escBackdrop = escapeCssUrl(backdropUrl);
  const standeeSize = options?.standeeSize || "auto 92%";
  const grad = options?.fallbackGradient?.trim() || "";

  if (standeeUrl) {
    const escStandee = escapeCssUrl(standeeUrl);
    if (grad) {
      return {
        "background-image": `url("${escStandee}"), url("${escBackdrop}"), ${grad}`,
        "background-repeat": "no-repeat, no-repeat, no-repeat",
        "background-size": `${standeeSize}, cover, cover`,
        "background-position": `${standeePositionX} bottom, ${backdropPosition}, center`,
      };
    }
    return {
      "background-image": `url("${escStandee}"), url("${escBackdrop}")`,
      "background-repeat": "no-repeat, no-repeat",
      "background-size": `${standeeSize}, cover`,
      "background-position": `${standeePositionX} bottom, ${backdropPosition}`,
    };
  }
  if (grad) {
    return {
      "background-image": `url("${escBackdrop}"), ${grad}`,
      "background-repeat": "no-repeat, no-repeat",
      "background-size": "cover, cover",
      "background-position": `${backdropPosition}, center`,
    };
  }
  return {
    "background-image": `url("${escBackdrop}")`,
    "background-repeat": "no-repeat",
    "background-size": "cover",
    "background-position": backdropPosition,
  };
}

export function clampWallpaperInt(val: unknown, min: number, max: number, fallback: number): number {
  if (typeof val !== "number" || !Number.isFinite(val)) return fallback;
  return Math.min(max, Math.max(min, Math.round(val)));
}

function clamp01(val: number): number {
  if (!Number.isFinite(val)) return 0;
  return Math.min(1, Math.max(0, val));
}

export function sanitizeWallpaperLuminance(raw: unknown): WallpaperLuminance {
  if (!raw || typeof raw !== "object") {
    return { ...UNMEASURED_WALLPAPER_LUMINANCE };
  }
  const obj = raw as Record<string, unknown>;
  if (
    typeof obj.mean !== "number" ||
    typeof obj.top !== "number" ||
    typeof obj.spread !== "number"
  ) {
    return { ...UNMEASURED_WALLPAPER_LUMINANCE };
  }
  const mean = clamp01(obj.mean);
  const spread = Math.min(0.5, Math.max(0, obj.spread));
  const p10 = typeof obj.p10 === "number" ? clamp01(obj.p10) : clamp01(mean - spread);
  const p90 = typeof obj.p90 === "number" ? clamp01(obj.p90) : clamp01(mean + spread);
  return {
    mean,
    top: clamp01(obj.top),
    p10,
    p90: Math.max(p10, p90),
    spread,
    measured: Boolean(obj.measured),
  };
}

export function srgbPixelLuminance(r255: number, g255: number, b255: number): number {
  const toLinear = (v: number) => {
    const s = clamp01(v / 255);
    return s <= 0.04045 ? s / 12.92 : Math.pow((s + 0.055) / 1.055, 2.4);
  };
  return 0.2126 * toLinear(r255) + 0.7152 * toLinear(g255) + 0.0722 * toLinear(b255);
}

export function computeAdaptiveGlassMetrics(
  theme: "light" | "dark",
  luminance: WallpaperLuminance,
  panelOpacity: number,
  backdropBlur: number,
  userDim: number
): AdaptiveGlassMetrics {
  const userSurface = clampWallpaperInt(
    panelOpacity,
    WALLPAPER_OPACITY_MIN,
    WALLPAPER_OPACITY_MAX,
    DEFAULT_PANEL_OPACITY
  );
  const userBlurPx = clampWallpaperInt(
    backdropBlur,
    WALLPAPER_BLUR_MIN,
    WALLPAPER_BLUR_MAX,
    DEFAULT_BACKDROP_BLUR
  );
  const userDimPct = clampWallpaperInt(
    userDim,
    WALLPAPER_DIM_MIN,
    WALLPAPER_DIM_MAX,
    DEFAULT_WALLPAPER_DIM
  );

  const materialMode: WallpaperMaterialMode = userBlurPx > 0 ? "frosted" : "crystal";
  const isCrystal = materialMode === "crystal";

  const effectiveLum: WallpaperLuminance = luminance.measured
    ? luminance
    : theme === "light"
      ? { mean: 0.18, top: 0.15, p10: 0.05, p90: 0.9, spread: 0.3, measured: false }
      : { mean: 0.82, top: 0.85, p10: 0.1, p90: 0.95, spread: 0.3, measured: false };

  const meanConflict =
    theme === "light"
      ? clamp01((0.68 - effectiveLum.mean) / 0.58)
      : clamp01((effectiveLum.mean - 0.32) / 0.58);

  const tailHazard =
    theme === "light"
      ? clamp01((0.62 - effectiveLum.p10) / 0.54)
      : clamp01((effectiveLum.p90 - 0.38) / 0.54);

  const conflict = clamp01(meanConflict * 0.68 + tailHazard * 0.32);

  const topConflict =
    theme === "light"
      ? clamp01((0.72 - effectiveLum.top) / 0.58)
      : clamp01((effectiveLum.top - 0.28) / 0.58);

  const spreadPenalty = clamp01((effectiveLum.spread - 0.12) / 0.24);
  const crystalDimBonus = isCrystal ? 0.08 : 0;
  const crystalSurfaceBonus = isCrystal ? 8 : 0;

  const minSafeDim =
    0.14 +
    crystalDimBonus +
    meanConflict * 0.42 +
    tailHazard * 0.12 +
    spreadPenalty * 0.08;
  const effectiveDimOpacity = Number(
    Math.min(0.82, Math.max(userDimPct / 100, minSafeDim)).toFixed(2)
  );

  const headerScrimOpacity = Number(
    Math.min(
      0.88,
      Math.max(
        0.5,
        0.44 + crystalDimBonus + topConflict * 0.34 + spreadPenalty * 0.1,
        (userSurface / 100) * 0.68
      )
    ).toFixed(2)
  );

  const minSafeSurface = Math.min(
    88,
    WALLPAPER_OPACITY_MIN +
      crystalSurfaceBonus +
      Math.round(conflict * 16 + spreadPenalty * 8)
  );
  const effectiveSurfaceOpacity = Math.max(userSurface, minSafeSurface);
  const effectiveTextZoneOpacity = Math.min(
    96,
    effectiveSurfaceOpacity + (isCrystal ? 8 : 6)
  );
  const effectiveArtZoneOpacity = Math.max(
    isCrystal ? 72 : 68,
    effectiveSurfaceOpacity - Math.round(6 - conflict * 4)
  );
  const effectiveNavOpacity = Math.max(
    isCrystal ? 76 : 70,
    Math.round(effectiveSurfaceOpacity * 0.9)
  );
  const effectiveComposerOpacity = Math.min(96, effectiveSurfaceOpacity + 8);
  const effectiveHeaderOpacity = Math.max(
    70,
    Math.min(90, Math.round(effectiveSurfaceOpacity * 0.84 + topConflict * 12))
  );

  const effectiveBlurPx = isCrystal
    ? 0
    : Math.max(userBlurPx, Math.min(24, 12 + Math.round(spreadPenalty * 6)));

  const backdropContrast = Number((0.88 - spreadPenalty * 0.08).toFixed(2));
  const backdropSaturate = Number((1.1 - spreadPenalty * 0.06).toFixed(2));
  const imageContrast = Number(
    (0.92 - tailHazard * 0.1 - (isCrystal ? 0.04 : 0)).toFixed(2)
  );
  const imageBrightness = Number(
    (theme === "light"
      ? 1 + meanConflict * 0.1 + tailHazard * 0.04
      : 1 - meanConflict * 0.14 - tailHazard * 0.06
    ).toFixed(2)
  );

  return {
    materialMode,
    effectiveSurfaceOpacity,
    effectiveTextZoneOpacity,
    effectiveArtZoneOpacity,
    effectiveNavOpacity,
    effectiveComposerOpacity,
    effectiveHeaderOpacity,
    effectiveBlurPx,
    effectiveDimOpacity,
    headerScrimOpacity,
    backdropContrast,
    backdropSaturate,
    imageContrast,
    imageBrightness,
    polarityMatched: meanConflict < 0.35,
  };
}

export function probeImageLuminance(url: string): Promise<WallpaperLuminance> {
  if (
    !url ||
    typeof Image === "undefined" ||
    typeof document === "undefined" ||
    typeof document.createElement !== "function"
  ) {
    return Promise.resolve({ ...UNMEASURED_WALLPAPER_LUMINANCE });
  }
  return new Promise((resolve) => {
    const img = new Image();
    if (url.startsWith("http://") || url.startsWith("https://")) {
      img.crossOrigin = "anonymous";
    }
    const finishFallback = () => resolve({ ...UNMEASURED_WALLPAPER_LUMINANCE });
    img.onload = () => {
      try {
        const w = 48;
        const h = 27;
        const topRows = 5;
        const canvas = document.createElement("canvas");
        canvas.width = w;
        canvas.height = h;
        const ctx = canvas.getContext("2d", { willReadFrequently: true });
        if (!ctx) {
          finishFallback();
          return;
        }
        ctx.drawImage(img, 0, 0, w, h);
        const data = ctx.getImageData(0, 0, w, h).data;
        const total = w * h;
        const topCount = topRows * w;
        const samples = new Float32Array(total);
        let sum = 0;
        let topSum = 0;
        for (let i = 0; i < total; i++) {
          const offset = i * 4;
          const alpha = data[offset + 3] / 255;
          const rawLum = srgbPixelLuminance(data[offset], data[offset + 1], data[offset + 2]);
          const lum = rawLum * alpha + 0.5 * (1 - alpha);
          samples[i] = lum;
          sum += lum;
          if (i < topCount) topSum += lum;
        }
        const mean = sum / total;
        const top = topSum / topCount;
        let varianceSum = 0;
        for (let i = 0; i < total; i++) {
          const diff = samples[i] - mean;
          varianceSum += diff * diff;
        }
        const sorted = Float32Array.from(samples).sort();
        const p10 = sorted[Math.floor(total * 0.1)] ?? mean;
        const p90 = sorted[Math.min(total - 1, Math.floor(total * 0.9))] ?? mean;
        const spread = Math.min(0.5, Math.sqrt(varianceSum / total));
        resolve({
          mean: Number(clamp01(mean).toFixed(3)),
          top: Number(clamp01(top).toFixed(3)),
          p10: Number(clamp01(p10).toFixed(3)),
          p90: Number(clamp01(p90).toFixed(3)),
          spread: Number(spread.toFixed(3)),
          measured: true,
        });
      } catch {
        finishFallback();
      }
    };
    img.onerror = finishFallback;
    img.src = url;
  });
}
