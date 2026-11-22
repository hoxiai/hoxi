import { reconcile, unwrap } from "solid-js/store";
import { hoxiAPI } from "../services/api";
import {
  APP_SETTINGS_KEY, appSettings, loadInitialSettings, openSettingsModal, resetAllAppSettings, setAppSettings,
  syncBrowserIdleMinutesFromBackend, syncSettingsSideEffects, updateAppSetting,
} from "../store/settings";
import {
  COMPANION_STORAGE_KEY,
  companionState,
  computeCompanionTokenLevel,
  deriveCompanionSnapshot,
  isHighRiskPermissionRequest,
  loadInitialCompanionMeta,
  pokeCompanion,
  setCompanionEnabled,
  setCompanionPersona,
  setCompanionState,
} from "../store/companion";
import {
  composeLayeredWallpaperStyle,
  computeAdaptiveGlassMetrics,
  srgbPixelLuminance,
  UNMEASURED_WALLPAPER_LUMINANCE,
  WALLPAPER_PRESET_ART,
} from "../store/wallpaper-optics";
import {
  activeWallpaperMaterialMode,
  applyWallpaperCustomUrl,
  applyWallpaperMaterialMode,
  loadInitialWallpaperMeta,
  resetWallpaperSliders,
  resolveActiveWallpaperStandee,
  resolveActiveWallpaperUrl,
  selectWallpaperPreset,
  setWallpaperEnabled,
  setWallpaperState,
  syncWallpaperDOM,
  updateWallpaperBackdropBlur,
  updateWallpaperDim,
  updateWallpaperPanelOpacity,
  WALLPAPER_STORAGE_KEY,
  wallpaperState,
} from "../store/wallpaper";
import { applyDesktopIntent, setWorkspace, workspace } from "../store/workspace";
import { computeHelpTipCoords } from "../components/settings/setting-controls";
import { assertEqual, describe, it } from "./test-harness";

function withAppearanceFixture(run: (root: HTMLElement, saved: Map<string, string>) => void) {
  const settings = structuredClone(unwrap(appSettings));
  const wp = structuredClone(unwrap(wallpaperState));
  const comp = structuredClone(unwrap(companionState));
  const state = structuredClone(unwrap(workspace));
  const descriptors = ["document", "localStorage"].map((key) =>
    Object.getOwnPropertyDescriptor(globalThis, key));
  const saved = new Map<string, string>();
  const classes = new Set<string>();
  const root = {
    dataset: {},
    style: { setProperty() {} },
    classList: {
      toggle(name: string, enabled: boolean) {
        if (enabled) classes.add(name);
        else classes.delete(name);
      },
      contains: (name: string) => classes.has(name),
    },
  } as unknown as HTMLElement;
  Object.defineProperty(globalThis, "document", {
    configurable: true, value: { documentElement: root },
  });
  Object.defineProperty(globalThis, "localStorage", {
    configurable: true, value: {
      getItem: (key: string) => saved.get(key) ?? null,
      setItem: (key: string, value: string) => saved.set(key, value),
    },
  });
  try {
    run(root, saved);
  } finally {
    setAppSettings(reconcile(settings));
    setWallpaperState(reconcile(wp));
    setCompanionState(reconcile(comp));
    setWorkspace(reconcile(state));
    ["document", "localStorage"].forEach((key, index) => {
      const descriptor = descriptors[index];
      if (descriptor) Object.defineProperty(globalThis, key, descriptor);
      else Reflect.deleteProperty(globalThis, key);
    });
  }
}

describe("appearance preferences", () => {
  it("persists the palette independently of light and dark modes, including sakura, cyber, and obsidian", () => {
    withAppearanceFixture((root, saved) => {
      updateAppSetting("themeMode", "light");
      updateAppSetting("colorScheme", "classic");
      assertEqual(root.dataset.colorScheme, "classic");
      assertEqual(root.classList.contains("dark"), false);
      updateAppSetting("colorScheme", "sakura");
      assertEqual(root.dataset.colorScheme, "sakura");
      updateAppSetting("themeMode", "dark");
      updateAppSetting("colorScheme", "cyber");
      assertEqual(root.dataset.colorScheme, "cyber");
      assertEqual(root.classList.contains("dark"), true);
      updateAppSetting("colorScheme", "obsidian");
      assertEqual(root.dataset.colorScheme, "obsidian");
      assertEqual(JSON.parse(saved.get(APP_SETTINGS_KEY)!).colorScheme, "obsidian");
      updateAppSetting("colorScheme", "blue");
      assertEqual(root.classList.contains("dark"), true);
      assertEqual(root.dataset.colorScheme, "blue");
    });
  });

  it("defaults to optical frosted mode, migrates legacy low-contrast caches, and switches crystal/frosted modes safely", () => {
    withAppearanceFixture((root, saved) => {
      resetWallpaperSliders();
      assertEqual(wallpaperState.enabled, false);
      assertEqual(resolveActiveWallpaperStandee(), null);
      assertEqual(root.dataset.wallpaperActive, undefined);
      assertEqual(root.dataset.wallpaperBlur, undefined);
      selectWallpaperPreset("cyber-shibuya");
      assertEqual(wallpaperState.enabled, true);
      assertEqual(root.dataset.wallpaperActive, "true");
      assertEqual(wallpaperState.backdropBlur, 18);
      assertEqual(activeWallpaperMaterialMode(), "frosted");
      assertEqual(root.dataset.wallpaperBlur, "true");
      assertEqual(resolveActiveWallpaperUrl(), "/wallpapers/cyber-shibuya.svg");
      assertEqual(resolveActiveWallpaperStandee()?.url, "/wallpapers/cyber-shibuya-standee.png");

      selectWallpaperPreset("sakura-breeze");
      assertEqual(resolveActiveWallpaperUrl(), "/wallpapers/sakura-breeze.svg");
      assertEqual(resolveActiveWallpaperStandee()?.url, "/wallpapers/sakura-breeze-standee.png");
      assertEqual(resolveActiveWallpaperStandee()?.positionX, "85%");

      selectWallpaperPreset("academy-sky");
      assertEqual(resolveActiveWallpaperUrl(), "/wallpapers/academy-sky.svg");
      assertEqual(resolveActiveWallpaperStandee()?.url, "/wallpapers/academy-sky-standee.png");
      const layeredAcademy = composeLayeredWallpaperStyle(
        resolveActiveWallpaperUrl(),
        resolveActiveWallpaperStandee()?.url,
        resolveActiveWallpaperStandee()?.positionX,
        "right center",
        {
          standeeSize: "auto 92%",
          fallbackGradient: WALLPAPER_PRESET_ART["academy-sky"].previewGradient,
        }
      );
      assertEqual(
        layeredAcademy["background-image"],
        `url("/wallpapers/academy-sky-standee.png"), url("/wallpapers/academy-sky.svg"), ${WALLPAPER_PRESET_ART["academy-sky"].previewGradient}`
      );
      assertEqual(layeredAcademy["background-size"], "auto 92%, cover, cover");

      const escapedCustom = composeLayeredWallpaperStyle('https://example.com/a\\"b\nc.png');
      assertEqual(escapedCustom["background-image"], 'url("https://example.com/a\\\\\\"bc.png")');

      selectWallpaperPreset("obsidian-gold");
      assertEqual(resolveActiveWallpaperStandee()?.url, "/wallpapers/obsidian-gold-standee.png");
      assertEqual(resolveActiveWallpaperStandee()?.positionX, "86%");
      assertEqual(resolveActiveWallpaperStandee()?.viewportSize, "min(46%, 68vh) auto");

      setWallpaperState({ source: "custom", hasStoredCustomBlob: true, customObjectUrl: "" });
      assertEqual(resolveActiveWallpaperStandee(), null);
      selectWallpaperPreset("obsidian-gold");

      applyWallpaperMaterialMode("crystal");
      assertEqual(activeWallpaperMaterialMode(), "crystal");
      assertEqual(wallpaperState.backdropBlur, 0);
      assertEqual(root.dataset.wallpaperBlur, undefined);

      applyWallpaperMaterialMode("frosted");
      assertEqual(activeWallpaperMaterialMode(), "frosted");
      assertEqual(wallpaperState.backdropBlur, 18);
      assertEqual(root.dataset.wallpaperBlur, "true");

      updateWallpaperPanelOpacity(10);
      assertEqual(wallpaperState.panelOpacity, 58);
      updateWallpaperPanelOpacity(99);
      assertEqual(wallpaperState.panelOpacity, 96);
      updateWallpaperBackdropBlur(60);
      assertEqual(wallpaperState.backdropBlur, 40);
      updateWallpaperDim(-10);
      assertEqual(wallpaperState.wallpaperDim, 0);
      assertEqual(applyWallpaperCustomUrl("invalid-scheme"), false);
      assertEqual(applyWallpaperCustomUrl("data:image/png;base64,AAAA"), true);
      assertEqual(resolveActiveWallpaperStandee(), null);
      assertEqual(loadInitialWallpaperMeta().source, "url");
      assertEqual(JSON.parse(saved.get(WALLPAPER_STORAGE_KEY)!).enabled, true);
      resetWallpaperSliders();
      assertEqual(wallpaperState.panelOpacity, 78);
      assertEqual(wallpaperState.backdropBlur, 18);
      assertEqual(wallpaperState.wallpaperDim, 20);

      saved.delete(WALLPAPER_STORAGE_KEY);
      saved.set("hoxi:wallpaper:v2", JSON.stringify({ enabled: true, presetId: "sakura-breeze", panelOpacity: 35, backdropBlur: 0, wallpaperDim: 6 }));
      const migrated = loadInitialWallpaperMeta();
      assertEqual(migrated.enabled, true);
      assertEqual(migrated.backdropBlur, 18);
      assertEqual(migrated.panelOpacity, 78);
      assertEqual(migrated.wallpaperDim, 20);

      setWallpaperEnabled(false);
      syncWallpaperDOM();
      assertEqual(root.dataset.wallpaperActive, undefined);
      assertEqual(root.dataset.wallpaperBlur, undefined);
    });
  });

  it("computes polarity-matched adaptive veil and tail-contrast protection across extreme anime backdrops", () => {
    assertEqual(srgbPixelLuminance(0, 0, 0), 0);
    assertEqual(Math.round(srgbPixelLuminance(255, 255, 255) * 100), 100);

    // 1. Dark wallpaper (Cyber Shibuya) under Light mode -> auto-lifts light veil & surface floor
    const darkOnLight = computeAdaptiveGlassMetrics(
      "light",
      WALLPAPER_PRESET_ART["cyber-shibuya"].luminance,
      60,
      16,
      10
    );
    assertEqual(darkOnLight.polarityMatched, false);
    assertEqual(darkOnLight.effectiveDimOpacity >= 0.6, true);
    assertEqual(darkOnLight.effectiveSurfaceOpacity >= 76, true);
    assertEqual(darkOnLight.imageBrightness > 1.08, true);

    // 2. Bright snow/sky art under Dark mode -> auto-boosts dark scrim & dims bright peaks
    const brightOnDark = computeAdaptiveGlassMetrics(
      "dark",
      { mean: 0.88, top: 0.92, p10: 0.65, p90: 0.98, spread: 0.22, measured: true },
      60,
      16,
      10
    );
    assertEqual(brightOnDark.polarityMatched, false);
    assertEqual(brightOnDark.effectiveDimOpacity >= 0.65, true);
    assertEqual(brightOnDark.headerScrimOpacity >= 0.78, true);
    assertEqual(brightOnDark.imageBrightness < 0.86, true);

    // 3. Crystal (0px blur) mode over silver-haired character -> compresses contrast & keeps art-zone >= 72%
    const crystalSilverHair = computeAdaptiveGlassMetrics(
      "dark",
      WALLPAPER_PRESET_ART["obsidian-gold"].luminance,
      78,
      0,
      20
    );
    assertEqual(crystalSilverHair.materialMode, "crystal");
    assertEqual(crystalSilverHair.effectiveBlurPx, 0);
    assertEqual(crystalSilverHair.effectiveArtZoneOpacity >= 72, true);
    assertEqual(crystalSilverHair.imageContrast <= 0.84, true);

    // 4. Opaque cross-origin URL (unmeasured) -> conservative protection in both themes
    const unmeasuredLight = computeAdaptiveGlassMetrics("light", UNMEASURED_WALLPAPER_LUMINANCE, 62, 18, 12);
    const unmeasuredDark = computeAdaptiveGlassMetrics("dark", UNMEASURED_WALLPAPER_LUMINANCE, 62, 18, 12);
    assertEqual(unmeasuredLight.effectiveDimOpacity >= 0.58, true);
    assertEqual(unmeasuredDark.effectiveDimOpacity >= 0.58, true);
  });

  it("applies restored preferences and resets to default blue, disabled wallpaper, and disabled companion", () => {
    withAppearanceFixture((root, saved) => {
      setAppSettings({ colorScheme: "sakura", themeMode: "dark" });
      selectWallpaperPreset("sakura-breeze");
      setCompanionPersona("neko-bot");
      assertEqual(companionState.enabled, true);
      assertEqual(companionState.persona, "neko-bot");
      syncSettingsSideEffects();
      assertEqual(root.dataset.colorScheme, "sakura");
      assertEqual(root.classList.contains("dark"), true);
      assertEqual(root.dataset.wallpaperActive, "true");
      resetAllAppSettings();
      assertEqual(root.dataset.colorScheme, "blue");
      assertEqual(root.classList.contains("dark"), false);
      assertEqual(root.dataset.wallpaperActive, undefined);
      assertEqual(wallpaperState.enabled, false);
      assertEqual(companionState.enabled, false);
      assertEqual(JSON.parse(saved.get(APP_SETTINGS_KEY)!).colorScheme, "blue");
    });
  });

  it("derives companion mood priority across high-risk gates, approvals, failover, parallel convoys, coding, celebrate, and idle", () => {
    withAppearanceFixture((_root, saved) => {
      setCompanionEnabled(true);
      assertEqual(JSON.parse(saved.get(COMPANION_STORAGE_KEY)!).enabled, true);
      assertEqual(loadInitialCompanionMeta().enabled, true);

      const baseNow = 1_760_000_000_000;
      setWorkspace({
        activeSessionID: "ses_main",
        statusBySession: {},
        stalledSinceBySession: {},
        sessionFinishedAt: {},
        pendingPermissions: [],
        pendingQuestions: [],
        arenaModalOpen: false,
      });

      assertEqual(deriveCompanionSnapshot(baseNow).mood, "idle");
      pokeCompanion(baseNow);
      const poked = deriveCompanionSnapshot(baseNow + 1000);
      assertEqual(poked.isPoked, true);
      assertEqual(poked.pokeSlot, 1);

      setWorkspace("sessionFinishedAt", "ses_main", baseNow - 3000);
      assertEqual(deriveCompanionSnapshot(baseNow).mood, "celebrate");

      setWorkspace("statusBySession", "ses_main", { type: "busy" });
      assertEqual(deriveCompanionSnapshot(baseNow).mood, "coding");

      setWorkspace("statusBySession", "ses_worker", { type: "busy" });
      const convoySnap = deriveCompanionSnapshot(baseNow);
      assertEqual(convoySnap.mood, "convoy");
      assertEqual(convoySnap.parallelCount, 2);

      setWorkspace("statusBySession", "ses_main", {
        type: "retry",
        attempt: 2,
        message: "502 Bad Gateway",
        next: baseNow + 2000,
      });
      const failoverSnap = deriveCompanionSnapshot(baseNow);
      assertEqual(failoverSnap.mood, "failover");
      assertEqual(failoverSnap.retryAttempt, 2);

      const safePerm = {
        id: "per_safe",
        sessionID: "ses_main",
        permission: "webfetch",
        patterns: ["https://docs.example.com"],
        metadata: {},
        always: [],
      };
      assertEqual(isHighRiskPermissionRequest(safePerm), false);
      setWorkspace("pendingPermissions", [safePerm]);
      assertEqual(deriveCompanionSnapshot(baseNow).mood, "gate-approval");

      const dangerPerm = {
        id: "per_danger",
        sessionID: "ses_worker",
        permission: "bash",
        patterns: ["rm -rf ./dist"],
        metadata: { command: "rm -rf ./dist" },
        always: [],
      };
      assertEqual(isHighRiskPermissionRequest(dangerPerm), true);
      setWorkspace("pendingPermissions", [safePerm, dangerPerm]);
      const dangerSnap = deriveCompanionSnapshot(baseNow);
      assertEqual(dangerSnap.mood, "gate-danger");
      assertEqual(dangerSnap.targetSessionID, "ses_worker");
      assertEqual(dangerSnap.gateTarget, "rm -rf ./dist");

      assertEqual(computeCompanionTokenLevel(0).level, 1);
      assertEqual(computeCompanionTokenLevel(10_000).level, 3);
      assertEqual(computeCompanionTokenLevel(100_000_000).level, 99);

      const prevPokes = companionState.pokeCount;
      applyDesktopIntent({ action: "companion-tap" });
      assertEqual(companionState.pokeCount, prevPokes + 1);
      assertEqual(companionState.minimized, false);
      applyDesktopIntent({ action: "companion-toggle-min" });
      assertEqual(companionState.minimized, true);
      assertEqual(companionState.showCornerDock, true);
      applyDesktopIntent({ action: "companion-dismiss" });
      assertEqual(companionState.showCornerDock, false);
    });
  });

  it("preserves explicit manual/smart approvalMode on load without forced auto migration", () => {
    withAppearanceFixture((_root, saved) => {
      saved.set(APP_SETTINGS_KEY, JSON.stringify({ approvalMode: "manual", defaultSandboxMode: "worktree" }));
      const loadedManual = loadInitialSettings();
      assertEqual(loadedManual.approvalMode, "manual");
      assertEqual(loadedManual.defaultSandboxMode, "worktree");

      saved.set(APP_SETTINGS_KEY, JSON.stringify({ approvalMode: "smart" }));
      const loadedSmart = loadInitialSettings();
      assertEqual(loadedSmart.approvalMode, "smart");
    });
  });

  it("normalizes legacy terminal settings tab to general and routes new domain tabs", () => {
    withAppearanceFixture(() => {
      openSettingsModal("terminal");
      assertEqual(appSettings.activeTab, "general");

      openSettingsModal("extensions");
      assertEqual(appSettings.activeTab, "extensions");

      openSettingsModal("remote");
      assertEqual(appSettings.activeTab, "remote");
    });
  });

  it("defaults Chrome idle timeout to 30 minutes and syncs valid overrides with backend", () => {
    const origSetIdle = hoxiAPI.setBrowserIdleMinutes;
    const sentMinutes: number[] = [];
    hoxiAPI.setBrowserIdleMinutes = async (mins: number) => {
      sentMinutes.push(mins);
      return { idleMinutes: mins };
    };
    try {
      withAppearanceFixture((_root, saved) => {
        assertEqual(loadInitialSettings().browserIdleMinutes, 30);

        saved.set(APP_SETTINGS_KEY, JSON.stringify({ browserIdleMinutes: 0 }));
        assertEqual(loadInitialSettings().browserIdleMinutes, 0);

        saved.set(APP_SETTINGS_KEY, JSON.stringify({ browserIdleMinutes: -5 }));
        assertEqual(loadInitialSettings().browserIdleMinutes, 30);

        saved.set(APP_SETTINGS_KEY, JSON.stringify({ browserIdleMinutes: 2000 }));
        assertEqual(loadInitialSettings().browserIdleMinutes, 30);

        updateAppSetting("browserIdleMinutes", 15);
        assertEqual(appSettings.browserIdleMinutes, 15);
        assertEqual(JSON.parse(saved.get(APP_SETTINGS_KEY)!).browserIdleMinutes, 15);
        assertEqual(sentMinutes[sentMinutes.length - 1], 15);

        syncBrowserIdleMinutesFromBackend(60);
        assertEqual(appSettings.browserIdleMinutes, 60);
        assertEqual(JSON.parse(saved.get(APP_SETTINGS_KEY)!).browserIdleMinutes, 60);

        resetAllAppSettings();
        assertEqual(appSettings.browserIdleMinutes, 30);
        assertEqual(sentMinutes[sentMinutes.length - 1], 30);
      });
    } finally {
      hoxiAPI.setBrowserIdleMinutes = origSetIdle;
    }
  });

  it("positions HelpTip above by default, flips below near top edge, and clamps horizontally", () => {
    const mid = computeHelpTipCoords({ left: 493, width: 14, top: 300, bottom: 314 }, 1000, 800);
    assertEqual(mid.above, true);
    assertEqual(mid.x, 500);
    assertEqual(mid.y, 800 - 300 + 8);

    const nearTop = computeHelpTipCoords({ left: 320, width: 14, top: 72, bottom: 86 }, 1000, 800);
    assertEqual(nearTop.above, false);
    assertEqual(nearTop.y, 86 + 8);

    const nearLeft = computeHelpTipCoords({ left: 20, width: 14, top: 240, bottom: 254 }, 1000, 800);
    assertEqual(nearLeft.x, 12 + 140);

    const nearRight = computeHelpTipCoords({ left: 970, width: 14, top: 240, bottom: 254 }, 1000, 800);
    assertEqual(nearRight.x, 1000 - 12 - 140);
  });
});
