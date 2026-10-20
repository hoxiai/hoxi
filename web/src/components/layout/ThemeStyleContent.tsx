import { Check, Monitor, Moon, Sun } from "lucide-solid";
import { For, Show } from "solid-js";
import {
  appSettings,
  updateAppSetting,
  type ColorScheme,
  type ThemeMode,
} from "../../store/settings";
import { t, type TxKey } from "../../i18n";

const THEME_OPTIONS: Array<{ id: ThemeMode; icon: any; labelKey: TxKey }> = [
  { id: "light", icon: Sun, labelKey: "header.appearanceLight" },
  { id: "dark", icon: Moon, labelKey: "header.appearanceDark" },
  { id: "system", icon: Monitor, labelKey: "header.appearanceSystem" },
];

const STYLE_OPTIONS: Array<{
  id: ColorScheme;
  labelKey: TxKey;
  descKey: TxKey;
  swatch: { canvas: string; surface: string; accent: string };
}> = [
  {
    id: "blue",
    labelKey: "header.styleBlue",
    descKey: "header.styleBlueDesc",
    swatch: { canvas: "#f1f3f6", surface: "#ffffff", accent: "#2563eb" },
  },
  {
    id: "classic",
    labelKey: "header.styleClassic",
    descKey: "header.styleClassicDesc",
    swatch: { canvas: "#f7f7f5", surface: "#ffffff", accent: "#242424" },
  },
  {
    id: "warm",
    labelKey: "header.styleWarm",
    descKey: "header.styleWarmDesc",
    swatch: { canvas: "#f5efe6", surface: "#fffaf3", accent: "#b45309" },
  },
  {
    id: "midnight",
    labelKey: "header.styleMidnight",
    descKey: "header.styleMidnightDesc",
    swatch: { canvas: "#000000", surface: "#141418", accent: "#6366f1" },
  },
];

export default function ThemeStyleContent() {
  return (
    <div class="select-none">
      <div class="grid grid-cols-3 gap-1 rounded-xl border border-[var(--border-subtle)] bg-[var(--bg-inset)] p-1">
        <For each={THEME_OPTIONS}>
          {(option) => {
            const Icon = option.icon;
            return (
              <button
                type="button"
                onClick={() => updateAppSetting("themeMode", option.id)}
                class={`hoxi-press inline-flex flex-col items-center justify-center gap-1 rounded-lg py-1.5 text-[11px] font-medium cursor-pointer ${
                  appSettings.themeMode === option.id
                    ? "bg-[var(--bg-panel)] text-[var(--accent-primary)] shadow-2xs"
                    : "text-[var(--text-muted)] hover:text-[var(--text-primary)]"
                }`}
                aria-pressed={appSettings.themeMode === option.id}
              >
                <Icon size={14} />
                <span>{t(option.labelKey)}</span>
              </button>
            );
          }}
        </For>
      </div>

      <div class="mb-1.5 mt-3 text-[11px] font-semibold uppercase tracking-wider text-[var(--text-faint)]">
        {t("header.appearanceStyleLabel")}
      </div>

      <div class="space-y-0.5">
        <For each={STYLE_OPTIONS}>
          {(style) => {
            const active = () => appSettings.colorScheme === style.id;
            return (
              <button
                type="button"
                onClick={() => updateAppSetting("colorScheme", style.id)}
                class={`hoxi-press flex w-full items-center gap-2.5 rounded-lg px-2 py-1.5 text-left cursor-pointer ${
                  active()
                    ? "bg-[var(--accent-surface)] text-[var(--text-primary)]"
                    : "text-[var(--text-secondary)] hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)]"
                }`}
                aria-pressed={active()}
              >
                <span
                  class="relative inline-flex h-7 w-7 shrink-0 items-center justify-center overflow-hidden rounded-lg border border-[var(--border-subtle)]"
                  style={{ background: style.swatch.canvas }}
                >
                  <span
                    class="h-3.5 w-3.5 rounded-[5px]"
                    style={{
                      background: style.swatch.surface,
                      "box-shadow": "0 0 0 1px rgba(127,127,127,0.22)",
                    }}
                  />
                  <span
                    class="absolute bottom-0.5 right-0.5 h-1.5 w-1.5 rounded-full"
                    style={{ background: style.swatch.accent }}
                  />
                </span>
                <span class="min-w-0 flex-1">
                  <span class="block truncate text-[13px] font-medium">{t(style.labelKey)}</span>
                  <span class="block truncate text-[11px] text-[var(--text-muted)]">
                    {t(style.descKey)}
                  </span>
                </span>
                <Show when={active()}>
                  <Check size={14} class="shrink-0 text-[var(--accent-primary)]" />
                </Show>
              </button>
            );
          }}
        </For>
      </div>
    </div>
  );
}
