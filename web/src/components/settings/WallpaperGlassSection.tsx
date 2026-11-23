// web/src/components/settings/WallpaperGlassSection.tsx
import { Check, ChevronRight, Eye, ImagePlus, Link2, RotateCcw, Sparkles, Trash2 } from "lucide-solid";
import { createSignal, For, Show } from "solid-js";
import { t, type TxKey } from "../../i18n";
import {
  activeWallpaperMaterialMode,
  applyWallpaperCustomUrl,
  applyWallpaperMaterialMode,
  clearCustomWallpaper,
  composeLayeredWallpaperStyle,
  resetWallpaperSliders,
  selectWallpaperPreset,
  setWallpaperEnabled,
  updateAppSetting,
  updateWallpaperBackdropBlur,
  updateWallpaperDim,
  updateWallpaperPanelOpacity,
  uploadCustomWallpaperFile,
  WALLPAPER_BLUR_MAX,
  WALLPAPER_BLUR_MIN,
  WALLPAPER_DIM_MAX,
  WALLPAPER_DIM_MIN,
  WALLPAPER_OPACITY_MAX,
  WALLPAPER_OPACITY_MIN,
  WALLPAPER_PRESET_ART,
  WALLPAPER_PRESET_IDS,
  wallpaperState,
  type ColorScheme,
  type WallpaperMaterialMode,
  type WallpaperPresetID,
} from "../../store/workspace";
import { HelpTip, SegmentedPills, SettingGroup, SettingRow, ToggleSwitch } from "./setting-controls";

const PRESET_LABEL_KEYS: Record<WallpaperPresetID, TxKey> = {
  "academy-sky": "settings.wallpaperPresetAcademy",
  "cyber-shibuya": "settings.wallpaperPresetCyber",
  "sakura-breeze": "settings.wallpaperPresetSakura",
  "obsidian-gold": "settings.wallpaperPresetObsidian",
};

export function WallpaperGlassSection() {
  const [urlDraft, setUrlDraft] = createSignal(wallpaperState.customUrl);
  const [errorMsg, setErrorMsg] = createSignal("");
  let fileInputRef: HTMLInputElement | undefined;

  const onPickPreset = (presetId: WallpaperPresetID) => {
    setErrorMsg("");
    selectWallpaperPreset(presetId);
    const preset = WALLPAPER_PRESET_ART[presetId];
    if (preset?.recommendedScheme) {
      updateAppSetting("colorScheme", preset.recommendedScheme as ColorScheme);
    }
    if (preset?.recommendedThemeMode) {
      updateAppSetting("themeMode", preset.recommendedThemeMode);
    }
  };

  const onFileSelected = async (ev: Event) => {
    const input = ev.currentTarget as HTMLInputElement;
    const file = input.files?.[0];
    input.value = "";
    if (!file) return;
    setErrorMsg("");
    const res = await uploadCustomWallpaperFile(file);
    if (!res.ok) {
      if (res.reason === "invalid_type") setErrorMsg(t("settings.wallpaperErrInvalidType"));
      else if (res.reason === "too_large") setErrorMsg(t("settings.wallpaperErrTooLarge"));
      else setErrorMsg(t("settings.wallpaperErrStorage"));
    }
  };

  const onApplyUrl = (ev: Event) => {
    ev.preventDefault();
    setErrorMsg("");
    const ok = applyWallpaperCustomUrl(urlDraft());
    if (!ok) {
      setErrorMsg(t("settings.wallpaperErrInvalidUrl"));
    }
  };

  const onClearCustom = async () => {
    setErrorMsg("");
    setUrlDraft("");
    await clearCustomWallpaper();
  };

  return (
    <SettingGroup
      title={t("settings.sectionWallpaperTitle")}
      tip={t("settings.sectionWallpaperDesc")}
      action={
        <Show when={wallpaperState.enabled}>
          <button
            type="button"
            onClick={() => resetWallpaperSliders()}
            class="inline-flex items-center gap-1.5 rounded-lg border border-[var(--border-subtle)] bg-[var(--bg-panel)] px-2.5 py-1 text-[12px] font-medium text-[var(--text-secondary)] hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)] transition-colors cursor-pointer"
          >
            <RotateCcw size={12} />
            <span>{t("settings.wallpaperResetSlidersBtn")}</span>
          </button>
        </Show>
      }
    >
      <SettingRow
        icon={Sparkles}
        label={t("settings.wallpaperEnableLabel")}
        tip={t("settings.wallpaperEnableDesc")}
      >
        <ToggleSwitch
          checked={wallpaperState.enabled}
          onChange={(val) => setWallpaperEnabled(val)}
        />
      </SettingRow>

      <Show when={wallpaperState.enabled}>
        <SettingRow
          icon={Eye}
          label={t("settings.wallpaperMaterialModeLabel")}
          tip={t("settings.wallpaperMaterialModeDesc")}
        >
          <SegmentedPills<WallpaperMaterialMode>
            value={activeWallpaperMaterialMode()}
            options={[
              {
                id: "crystal",
                label: () => t("settings.wallpaperModeCrystal"),
                tip: () => t("settings.wallpaperModeCrystalDesc"),
              },
              {
                id: "frosted",
                label: () => t("settings.wallpaperModeFrosted"),
                tip: () => t("settings.wallpaperModeFrostedDesc"),
              },
            ]}
            onChange={(mode) => applyWallpaperMaterialMode(mode)}
          />
        </SettingRow>

        <div class="px-5 py-4 space-y-4">
        <div class="space-y-2">
          <div class="text-[12px] font-medium text-[var(--text-secondary)]">
            {t("settings.wallpaperPresetsLabel")}
          </div>
          <div class="grid grid-cols-2 sm:grid-cols-4 gap-2.5">
            <For each={WALLPAPER_PRESET_IDS}>
              {(presetId) => {
                const isSelected = () =>
                  wallpaperState.enabled &&
                  wallpaperState.source === "preset" &&
                  wallpaperState.presetId === presetId;
                return (
                  <button
                    type="button"
                    onClick={() => onPickPreset(presetId)}
                    class={`group relative flex flex-col overflow-hidden rounded-xl border text-left transition-all cursor-pointer ${
                      isSelected()
                        ? "border-[var(--accent-primary)] ring-2 ring-[var(--accent-primary)]/25"
                        : "border-[var(--border-subtle)] hover:border-[var(--border-strong)]"
                    }`}
                  >
                    <div
                      class="aspect-video w-full transition-transform duration-300 group-hover:scale-105"
                      style={composeLayeredWallpaperStyle(
                        WALLPAPER_PRESET_ART[presetId].imageUri,
                        WALLPAPER_PRESET_ART[presetId].standeeUri,
                        WALLPAPER_PRESET_ART[presetId].standeePositionX,
                        "right center",
                        {
                          standeeSize: "auto 92%",
                          fallbackGradient: WALLPAPER_PRESET_ART[presetId].previewGradient,
                        }
                      )}
                    />
                    <div class="flex items-center justify-between gap-1.5 bg-[var(--bg-panel)] px-2.5 py-1.5">
                      <span class="truncate text-[12px] font-medium text-[var(--text-primary)]">
                        {t(PRESET_LABEL_KEYS[presetId])}
                      </span>
                      <Show when={isSelected()}>
                        <Check size={13} class="shrink-0 text-[var(--accent-primary)]" />
                      </Show>
                    </div>
                  </button>
                );
              }}
            </For>
          </div>
        </div>

        {/* Custom Local Upload & URL */}
        <div class="rounded-xl border border-[var(--border-subtle)] bg-[var(--bg-panel)]/70 p-3.5 space-y-3">
          <div class="flex flex-wrap items-center justify-between gap-2">
            <div class="flex items-center gap-2 min-w-0">
              <span class="text-[13px] font-medium text-[var(--text-primary)] truncate">
                {t("settings.wallpaperCustomLabel")}
              </span>
              <HelpTip text={t("settings.wallpaperCustomDesc")} />
              <Show when={wallpaperState.source === "custom" && Boolean(wallpaperState.customObjectUrl)}>
                <span class="shrink-0 rounded-full border border-[var(--accent-border)] bg-[var(--accent-surface)] px-2 py-0.5 text-[11px] font-medium text-[var(--accent-text)]">
                  {t("settings.wallpaperCustomActiveBadge")}
                </span>
              </Show>
            </div>

            <div class="flex items-center gap-2 shrink-0">
              <input
                ref={fileInputRef}
                type="file"
                accept="image/*"
                class="hidden"
                onChange={(ev) => void onFileSelected(ev)}
              />
              <button
                type="button"
                onClick={() => fileInputRef?.click()}
                class="hoxi-press inline-flex items-center gap-1.5 rounded-lg border border-[var(--border-strong)] bg-[var(--bg-panel)] px-3 py-1.5 text-[12.5px] font-medium text-[var(--text-primary)] hover:bg-[var(--bg-hover)] cursor-pointer"
              >
                <ImagePlus size={14} class="text-[var(--accent-primary)]" />
                <span>{t("settings.wallpaperUploadBtn")}</span>
              </button>

              <Show when={wallpaperState.source === "custom" || wallpaperState.source === "url"}>
                <button
                  type="button"
                  onClick={() => void onClearCustom()}
                  class="hoxi-press inline-flex items-center gap-1 rounded-lg border border-[var(--border-subtle)] bg-[var(--bg-panel)] px-2.5 py-1.5 text-[12px] text-[var(--text-muted)] hover:text-[var(--danger-text)] hover:border-[var(--danger-border)] cursor-pointer"
                >
                  <Trash2 size={13} />
                  <span>{t("settings.wallpaperClearCustomBtn")}</span>
                </button>
              </Show>
            </div>
          </div>

          <form onSubmit={onApplyUrl} class="flex items-center gap-2">
            <div class="relative flex-1 min-w-0">
              <Link2
                size={13.5}
                class="pointer-events-none absolute left-3 top-1/2 -translate-y-1/2 text-[var(--text-faint)]"
              />
              <input
                type="url"
                value={urlDraft()}
                onInput={(e) => setUrlDraft(e.currentTarget.value)}
                placeholder={t("settings.wallpaperUrlPlaceholder")}
                class="w-full rounded-lg border border-[var(--border-subtle)] bg-[var(--bg-inset)] pl-8 pr-3 py-1.5 text-[12.5px] text-[var(--text-primary)] placeholder:text-[var(--text-faint)] outline-none focus:border-[var(--accent-primary)]"
              />
            </div>
            <button
              type="submit"
              disabled={!urlDraft().trim()}
              class="hoxi-press shrink-0 rounded-lg border border-[var(--border-subtle)] bg-[var(--bg-panel)] px-3 py-1.5 text-[12.5px] font-medium text-[var(--text-secondary)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-hover)] disabled:opacity-45 disabled:cursor-not-allowed cursor-pointer"
            >
              {t("settings.wallpaperApplyUrlBtn")}
            </button>
          </form>

          <Show when={errorMsg()}>
            <div class="text-[12px] font-medium text-[var(--danger-text)]">{errorMsg()}</div>
          </Show>
        </div>
      </div>

        {/* Three Precision Glass Tuning Sliders (collapsible) */}
        <details class="group/glass-tuning">
          <summary class="flex cursor-pointer list-none items-center gap-1.5 px-5 py-2.5 text-[12px] font-medium text-[var(--text-muted)] hover:text-[var(--text-primary)] [&::-webkit-details-marker]:hidden">
            <ChevronRight size={13} class="transition-transform group-open/glass-tuning:rotate-90" />
            <span>{t("settings.wallpaperFineTuning")}</span>
            <span class="ml-auto font-mono text-[11px] text-[var(--text-faint)]">
              {wallpaperState.panelOpacity}% · {wallpaperState.backdropBlur}px · {wallpaperState.wallpaperDim}%
            </span>
          </summary>
          <div class="divide-y divide-[var(--border-subtle)]/55 border-t border-[var(--border-subtle)]/55">
            <SettingRow
              label={t("settings.wallpaperOpacityLabel")}
              tip={t("settings.wallpaperOpacityDesc")}
            >
              <div class="flex items-center gap-3 w-52 sm:w-64">
                <input
                  type="range"
                  min={WALLPAPER_OPACITY_MIN}
                  max={WALLPAPER_OPACITY_MAX}
                  step={1}
                  aria-label={t("settings.wallpaperOpacityLabel")}
                  value={wallpaperState.panelOpacity}
                  onInput={(e) => updateWallpaperPanelOpacity(e.currentTarget.valueAsNumber)}
                  class="flex-1 accent-[var(--accent-primary)] cursor-pointer"
                />
                <span class="w-11 text-right font-mono text-[12.5px] text-[var(--text-secondary)]">
                  {wallpaperState.panelOpacity}%
                </span>
              </div>
            </SettingRow>

            <SettingRow
              label={t("settings.wallpaperBlurLabel")}
              tip={t("settings.wallpaperBlurDesc")}
            >
              <div class="flex items-center gap-3 w-52 sm:w-64">
                <input
                  type="range"
                  min={WALLPAPER_BLUR_MIN}
                  max={WALLPAPER_BLUR_MAX}
                  step={1}
                  aria-label={t("settings.wallpaperBlurLabel")}
                  value={wallpaperState.backdropBlur}
                  onInput={(e) => updateWallpaperBackdropBlur(e.currentTarget.valueAsNumber)}
                  class="flex-1 accent-[var(--accent-primary)] cursor-pointer"
                />
                <span class="w-11 text-right font-mono text-[12.5px] text-[var(--text-secondary)]">
                  {wallpaperState.backdropBlur}px
                </span>
              </div>
            </SettingRow>

            <SettingRow
              label={t("settings.wallpaperDimLabel")}
              tip={t("settings.wallpaperDimDesc")}
            >
              <div class="flex items-center gap-3 w-52 sm:w-64">
                <input
                  type="range"
                  min={WALLPAPER_DIM_MIN}
                  max={WALLPAPER_DIM_MAX}
                  step={1}
                  aria-label={t("settings.wallpaperDimLabel")}
                  value={wallpaperState.wallpaperDim}
                  onInput={(e) => updateWallpaperDim(e.currentTarget.valueAsNumber)}
                  class="flex-1 accent-[var(--accent-primary)] cursor-pointer"
                />
                <span class="w-11 text-right font-mono text-[12.5px] text-[var(--text-secondary)]">
                  {wallpaperState.wallpaperDim}%
                </span>
              </div>
            </SettingRow>
          </div>
        </details>
      </Show>
    </SettingGroup>
  );
}
