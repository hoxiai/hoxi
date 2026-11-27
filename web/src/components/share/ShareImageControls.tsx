// web/src/components/share/ShareImageControls.tsx
// Right-hand scope, appearance, privacy, and image selection controls for ShareImageModal.
import { For, Show } from "solid-js";
import { Image as ImageIcon, ShieldCheck } from "lucide-solid";
import { t } from "../../i18n";
import type {
  ShareImageLayoutOption,
  ShareImagePreferences,
  ShareImageThemeOption,
  ShareImageWidthOption,
} from "../../store/workspace";
import type { ShareExchange, ShareImageAsset } from "./share-image-model";

export interface ShareImageControlsProps {
  allExchanges: ShareExchange[];
  selectedExchangeIDs: ReadonlySet<string>;
  hasFocusMessage: boolean;
  isCurrentPreset: boolean;
  isRecentPreset: boolean;
  isAllPreset: boolean;
  prefs: ShareImagePreferences;
  redactionCount: number;
  selectedImages: ShareImageAsset[];
  disabledImageIDs: ReadonlySet<string>;
  onApplyPreset: (mode: "current" | "recent" | "all") => void;
  onToggleExchange: (id: string) => void;
  onToggleImage: (id: string) => void;
  onUpdatePrefs: (patch: Partial<ShareImagePreferences>) => void;
}

const pillClass = (active: boolean) =>
  `flex-1 rounded-lg px-2.5 py-1.5 text-[11.5px] font-medium transition-colors cursor-pointer ${
    active
      ? "bg-[var(--accent-surface)] text-[var(--accent-primary)] border border-[var(--accent-border)]"
      : "bg-[var(--bg-subtle)] text-[var(--text-secondary)] border border-transparent hover:text-[var(--text-primary)] hover:bg-[var(--bg-hover)]"
  }`;

export default function ShareImageControls(props: ShareImageControlsProps) {
  return (
    <aside class="flex w-full shrink-0 flex-col gap-5 overflow-y-auto border-t border-[var(--border-subtle)] bg-[var(--bg-elevated)] p-4 md:w-[320px] md:border-l md:border-t-0">
      {/* Scope Selection */}
      <section class="space-y-2">
        <div class="flex items-center justify-between">
          <span class="text-[11px] font-semibold uppercase tracking-wider text-[var(--text-faint)]">
            {t("share.sectionScope", {
              selected: props.selectedExchangeIDs.size,
              total: props.allExchanges.length,
            })}
          </span>
        </div>
        <div class="flex gap-1.5">
          <Show when={props.hasFocusMessage}>
            <button
              type="button"
              onClick={() => props.onApplyPreset("current")}
              class={pillClass(props.isCurrentPreset)}
            >
              {t("share.presetCurrent")}
            </button>
          </Show>
          <button
            type="button"
            onClick={() => props.onApplyPreset("recent")}
            class={pillClass(props.isRecentPreset)}
          >
            {t("share.presetRecent")}
          </button>
          <button
            type="button"
            onClick={() => props.onApplyPreset("all")}
            class={pillClass(props.isAllPreset)}
          >
            {t("share.presetAll")}
          </button>
        </div>
        <Show when={props.allExchanges.length > 0}>
          <div class="max-h-[164px] space-y-1 overflow-y-auto rounded-xl border border-[var(--border-subtle)] bg-[var(--bg-canvas)] p-1.5">
            <For each={props.allExchanges}>
              {(ex) => (
                <label class="flex items-start gap-2 rounded-lg px-2 py-1.5 text-[11.5px] hover:bg-[var(--bg-hover)] cursor-pointer select-none">
                  <input
                    type="checkbox"
                    checked={props.selectedExchangeIDs.has(ex.id)}
                    onChange={() => props.onToggleExchange(ex.id)}
                    class="mt-0.5 accent-[var(--accent-primary)] cursor-pointer"
                  />
                  <div class="min-w-0 flex-1">
                    <div class="truncate font-medium text-[var(--text-primary)]">
                      {ex.userPreview || t("share.exchangeLabel", { index: ex.index })}
                    </div>
                    <div class="truncate text-[10.5px] text-[var(--text-muted)]">
                      {ex.assistantPreview || t("share.exchangeFallback")}
                    </div>
                  </div>
                </label>
              )}
            </For>
          </div>
        </Show>
      </section>

      {/* Style & Layout */}
      <section class="space-y-3">
        <div class="text-[11px] font-semibold uppercase tracking-wider text-[var(--text-faint)]">
          {t("share.sectionStyle")}
        </div>

        <div class="space-y-1.5">
          <div class="text-[11.5px] text-[var(--text-muted)]">{t("share.themeLabel")}</div>
          <div class="flex gap-1.5">
            {(["light", "dark", "system"] as ShareImageThemeOption[]).map((opt) => (
              <button
                type="button"
                onClick={() => props.onUpdatePrefs({ theme: opt })}
                class={pillClass(props.prefs.theme === opt)}
              >
                {opt === "light"
                  ? t("share.themeLight")
                  : opt === "dark"
                    ? t("share.themeDark")
                    : t("share.themeSystem")}
              </button>
            ))}
          </div>
        </div>

        <div class="space-y-1.5">
          <div class="text-[11.5px] text-[var(--text-muted)]">{t("share.widthLabel")}</div>
          <div class="flex gap-1.5">
            {(["standard", "wide"] as ShareImageWidthOption[]).map((opt) => (
              <button
                type="button"
                onClick={() => props.onUpdatePrefs({ width: opt })}
                class={pillClass(props.prefs.width === opt)}
              >
                {opt === "standard" ? t("share.widthStandard") : t("share.widthWide")}
              </button>
            ))}
          </div>
        </div>

        <div class="space-y-1.5">
          <div class="text-[11.5px] text-[var(--text-muted)]">{t("share.layoutLabel")}</div>
          <div class="flex gap-1.5">
            {(["single", "paged"] as ShareImageLayoutOption[]).map((opt) => (
              <button
                type="button"
                onClick={() => props.onUpdatePrefs({ layout: opt })}
                class={pillClass(props.prefs.layout === opt)}
              >
                {opt === "single" ? t("share.layoutSingle") : t("share.layoutPaged")}
              </button>
            ))}
          </div>
        </div>
      </section>

      {/* Content & Privacy Toggles */}
      <section class="space-y-2">
        <div class="flex items-center justify-between gap-2">
          <span class="text-[11px] font-semibold uppercase tracking-wider text-[var(--text-faint)]">
            {t("share.sectionContent")}
          </span>
          <Show when={props.prefs.redactSecrets && props.redactionCount > 0}>
            <span class="inline-flex items-center gap-1 rounded-full bg-[var(--ok-surface)] px-2 py-0.5 text-[10.5px] font-medium text-[var(--ok-text)]">
              <ShieldCheck size={11} />
              <span>{t("share.redactedBadge", { count: props.redactionCount })}</span>
            </span>
          </Show>
        </div>

        <div class="space-y-1">
          <ToggleRow
            label={t("share.toggleTools")}
            checked={props.prefs.includeTools}
            onChange={(v) => props.onUpdatePrefs({ includeTools: v })}
          />
          <ToggleRow
            label={t("share.toggleThinking")}
            checked={props.prefs.includeThinking}
            onChange={(v) => props.onUpdatePrefs({ includeThinking: v })}
          />
          <ToggleRow
            label={t("share.toggleImages")}
            checked={props.prefs.includeImages}
            onChange={(v) => props.onUpdatePrefs({ includeImages: v })}
          />
          <ToggleRow
            label={t("share.toggleHidePaths")}
            checked={props.prefs.hideLocalPaths}
            onChange={(v) => props.onUpdatePrefs({ hideLocalPaths: v })}
          />
          <ToggleRow
            label={t("share.toggleRedactSecrets")}
            checked={props.prefs.redactSecrets}
            onChange={(v) => props.onUpdatePrefs({ redactSecrets: v })}
          />
          <ToggleRow
            label={t("share.toggleBrandFooter")}
            checked={props.prefs.showBrandFooter}
            onChange={(v) => props.onUpdatePrefs({ showBrandFooter: v })}
          />
        </div>
      </section>

      {/* Per-image privacy picker when embedded images exist */}
      <Show when={props.prefs.includeImages && props.selectedImages.length > 0}>
        <section class="space-y-1.5">
          <div class="text-[11px] font-semibold uppercase tracking-wider text-[var(--text-faint)]">
            {t("share.sectionImages", {
              selected: props.selectedImages.filter((img) => !props.disabledImageIDs.has(img.id)).length,
              total: props.selectedImages.length,
            })}
          </div>
          <div class="max-h-[112px] space-y-1 overflow-y-auto rounded-xl border border-[var(--border-subtle)] bg-[var(--bg-canvas)] p-1.5">
            <For each={props.selectedImages}>
              {(img) => (
                <label class="flex items-center gap-2 rounded-lg px-2 py-1 text-[11.5px] hover:bg-[var(--bg-hover)] cursor-pointer select-none">
                  <input
                    type="checkbox"
                    checked={!props.disabledImageIDs.has(img.id)}
                    onChange={() => props.onToggleImage(img.id)}
                    class="accent-[var(--accent-primary)] cursor-pointer"
                  />
                  <ImageIcon size={12} class="shrink-0 text-[var(--text-muted)]" />
                  <span class="min-w-0 flex-1 truncate text-[var(--text-secondary)]">
                    {img.label}
                  </span>
                </label>
              )}
            </For>
          </div>
        </section>
      </Show>
    </aside>
  );
}

function ToggleRow(props: { label: string; checked: boolean; onChange: (checked: boolean) => void }) {
  return (
    <label class="flex items-center justify-between gap-2 rounded-lg px-2 py-1.5 text-[12px] text-[var(--text-secondary)] hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)] transition-colors cursor-pointer select-none">
      <span>{props.label}</span>
      <input
        type="checkbox"
        checked={props.checked}
        onChange={(ev) => props.onChange(ev.currentTarget.checked)}
        class="h-3.5 w-3.5 accent-[var(--accent-primary)] cursor-pointer"
      />
    </label>
  );
}
