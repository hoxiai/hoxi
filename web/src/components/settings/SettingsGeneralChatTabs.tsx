// web/src/components/settings/SettingsGeneralChatTabs.tsx
import { For, Show } from "solid-js";
import { Check, Languages, Monitor, Moon, Sun, User } from "lucide-solid";
import {
  appSettings,
  updateAppSetting,
  type FontScale,
  type ReasoningDisplayMode,
  type ThemeMode,
} from "../../store/workspace";
import { locale, LOCALE_METADATA, setLocale, SUPPORTED_LOCALES, t } from "../../i18n";

export function GeneralAppearanceTab() {
  return (
    <div class="space-y-6">
      {/* 1. Interface Language */}
      <section class="space-y-2.5">
        <div>
          <h3 class="flex items-center gap-1.5 text-[13.5px] font-semibold text-[var(--text-primary)]">
            <Languages size={14} class="text-[var(--accent-primary)]" />
            {t("settings.sectionLanguageTitle")}
          </h3>
          <p class="text-[11.5px] text-[var(--text-muted)]">
            {t("settings.sectionLanguageDesc")}
          </p>
        </div>

        <div class="grid grid-cols-2 gap-3">
          <For each={SUPPORTED_LOCALES}>
            {(loc) => {
              const meta = LOCALE_METADATA[loc];
              const isSelected = () => locale() === loc;
              return (
                <button
                  type="button"
                  onClick={() => setLocale(loc)}
                  class={`flex flex-col items-start gap-1 rounded-xl border p-3.5 text-left transition-all cursor-pointer ${
                    isSelected()
                      ? "border-[var(--accent-border)] bg-[var(--accent-surface)]"
                      : "border-[var(--border-subtle)] bg-[var(--bg-inset)] hover:border-[var(--border-strong)]"
                  }`}
                >
                  <div class="flex w-full items-center justify-between">
                    <span class="text-[13px] font-semibold text-[var(--text-primary)]">
                      {meta.label}
                    </span>
                    <div class="flex items-center gap-1.5">
                      <span class="rounded-md bg-[var(--bg-panel)] px-1.5 py-0.5 font-mono text-[10px] text-[var(--text-muted)]">
                        {meta.langTag}
                      </span>
                      <Show when={isSelected()}>
                        <Check size={14} class="text-[var(--accent-primary)]" />
                      </Show>
                    </div>
                  </div>
                  <span class="text-[11px] text-[var(--text-muted)]">
                    {loc === "zh-CN" ? t("settings.langZhDesc") : t("settings.langEnDesc")}
                  </span>
                </button>
              );
            }}
          </For>
        </div>
      </section>

      {/* 2. Color Theme */}
      <section class="border-t border-[var(--border-subtle)] pt-5 space-y-2.5">
        <div>
          <h3 class="text-[13.5px] font-semibold text-[var(--text-primary)]">
            {t("settings.sectionThemeTitle")}
          </h3>
          <p class="text-[11.5px] text-[var(--text-muted)]">
            {t("settings.sectionThemeDesc")}
          </p>
        </div>

        <div class="grid grid-cols-3 gap-3">
          <For
            each={[
              {
                mode: "light" as ThemeMode,
                label: () => t("settings.themeLight"),
                desc: () => t("settings.themeLightDesc"),
                icon: Sun,
              },
              {
                mode: "dark" as ThemeMode,
                label: () => t("settings.themeDark"),
                desc: () => t("settings.themeDarkDesc"),
                icon: Moon,
              },
              {
                mode: "system" as ThemeMode,
                label: () => t("settings.themeSystem"),
                desc: () => t("settings.themeSystemDesc"),
                icon: Monitor,
              },
            ]}
          >
            {(item) => {
              const IconComp = item.icon;
              const active = () => appSettings.themeMode === item.mode;
              return (
                <button
                  type="button"
                  onClick={() => updateAppSetting("themeMode", item.mode)}
                  class={`flex flex-col items-start gap-1.5 rounded-xl border p-3 text-left transition-all cursor-pointer ${
                    active()
                      ? "border-[var(--accent-border)] bg-[var(--accent-surface)]"
                      : "border-[var(--border-subtle)] bg-[var(--bg-inset)] hover:border-[var(--border-strong)]"
                  }`}
                >
                  <div class="flex w-full items-center justify-between">
                    <span class="inline-flex items-center gap-1.5 text-[12.5px] font-semibold text-[var(--text-primary)]">
                      <IconComp size={13.5} class="text-[var(--accent-primary)]" />
                      {item.label()}
                    </span>
                    <Show when={active()}>
                      <Check size={13} class="text-[var(--accent-primary)]" />
                    </Show>
                  </div>
                  <span class="text-[11px] text-[var(--text-muted)] leading-snug">
                    {item.desc()}
                  </span>
                </button>
              );
            }}
          </For>
        </div>
      </section>

      {/* 3. Font Scale */}
      <section class="border-t border-[var(--border-subtle)] pt-5 space-y-2.5">
        <div>
          <h3 class="text-[13.5px] font-semibold text-[var(--text-primary)]">
            {t("settings.sectionFontScaleTitle")}
          </h3>
          <p class="text-[11.5px] text-[var(--text-muted)]">
            {t("settings.sectionFontScaleDesc")}
          </p>
        </div>

        <div class="grid grid-cols-4 gap-2.5">
          <For
            each={[
              { id: "compact" as FontScale, label: () => t("settings.fontCompact") },
              { id: "default" as FontScale, label: () => t("settings.fontDefault") },
              { id: "comfortable" as FontScale, label: () => t("settings.fontComfortable") },
              { id: "large" as FontScale, label: () => t("settings.fontLarge") },
            ]}
          >
            {(opt) => (
              <button
                type="button"
                onClick={() => updateAppSetting("fontScale", opt.id)}
                class={`rounded-xl border px-3 py-2.5 text-center text-[12px] font-medium transition-all cursor-pointer ${
                  appSettings.fontScale === opt.id
                    ? "border-[var(--accent-border)] bg-[var(--accent-surface)] text-[var(--accent-primary)]"
                    : "border-[var(--border-subtle)] bg-[var(--bg-inset)] text-[var(--text-secondary)] hover:text-[var(--text-primary)]"
                }`}
              >
                {opt.label()}
              </button>
            )}
          </For>
        </div>
      </section>

      {/* 4. Local User Display Name */}
      <section class="border-t border-[var(--border-subtle)] pt-5 space-y-2.5">
        <div>
          <h3 class="flex items-center gap-1.5 text-[13.5px] font-semibold text-[var(--text-primary)]">
            <User size={14} class="text-[var(--accent-primary)]" />
            {t("settings.sectionProfileTitle")}
          </h3>
          <p class="text-[11.5px] text-[var(--text-muted)]">
            {t("settings.sectionProfileDesc")}
          </p>
        </div>
        <input
          type="text"
          value={appSettings.userDisplayName}
          placeholder={t("settings.profileNamePlaceholder")}
          onInput={(ev) => updateAppSetting("userDisplayName", ev.currentTarget.value)}
          class="w-full max-w-sm rounded-xl border border-[var(--border-strong)] bg-[var(--bg-elevated)] px-3.5 py-2 text-[12.5px] text-[var(--text-primary)] placeholder:text-[var(--text-faint)] outline-none focus:border-[var(--accent-primary)]"
        />
      </section>
    </div>
  );
}

export function ChatComposerTab() {
  return (
    <div class="space-y-6">
      {/* 1. Send Prompt Shortcut */}
      <section class="space-y-2.5">
        <div>
          <h3 class="text-[13.5px] font-semibold text-[var(--text-primary)]">
            {t("settings.sectionSendShortcutTitle")}
          </h3>
          <p class="text-[11.5px] text-[var(--text-muted)]">
            {t("settings.sectionSendShortcutDesc")}
          </p>
        </div>

        <div class="grid grid-cols-2 gap-3">
          <button
            type="button"
            onClick={() => updateAppSetting("sendShortcut", "enter")}
            class={`flex flex-col items-start gap-1 rounded-xl border p-3.5 text-left transition-all cursor-pointer ${
              appSettings.sendShortcut === "enter"
                ? "border-[var(--accent-border)] bg-[var(--accent-surface)]"
                : "border-[var(--border-subtle)] bg-[var(--bg-inset)] hover:border-[var(--border-strong)]"
            }`}
          >
            <div class="flex w-full items-center justify-between">
              <span class="text-[12.5px] font-semibold text-[var(--text-primary)]">
                {t("settings.shortcutEnterTitle")}
              </span>
              <Show when={appSettings.sendShortcut === "enter"}>
                <Check size={14} class="text-[var(--accent-primary)]" />
              </Show>
            </div>
            <span class="text-[11px] text-[var(--text-muted)]">
              {t("settings.shortcutEnterDesc")}
            </span>
          </button>

          <button
            type="button"
            onClick={() => updateAppSetting("sendShortcut", "mod-enter")}
            class={`flex flex-col items-start gap-1 rounded-xl border p-3.5 text-left transition-all cursor-pointer ${
              appSettings.sendShortcut === "mod-enter"
                ? "border-[var(--accent-border)] bg-[var(--accent-surface)]"
                : "border-[var(--border-subtle)] bg-[var(--bg-inset)] hover:border-[var(--border-strong)]"
            }`}
          >
            <div class="flex w-full items-center justify-between">
              <span class="text-[12.5px] font-semibold text-[var(--text-primary)]">
                {t("settings.shortcutModEnterTitle")}
              </span>
              <Show when={appSettings.sendShortcut === "mod-enter"}>
                <Check size={14} class="text-[var(--accent-primary)]" />
              </Show>
            </div>
            <span class="text-[11px] text-[var(--text-muted)]">
              {t("settings.shortcutModEnterDesc")}
            </span>
          </button>
        </div>
      </section>

      {/* 2. Reasoning Stream Display */}
      <section class="border-t border-[var(--border-subtle)] pt-5 space-y-2.5">
        <div>
          <h3 class="text-[13.5px] font-semibold text-[var(--text-primary)]">
            {t("settings.sectionReasoningTitle")}
          </h3>
          <p class="text-[11.5px] text-[var(--text-muted)]">
            {t("settings.sectionReasoningDesc")}
          </p>
        </div>

        <div class="grid grid-cols-3 gap-3">
          <For
            each={[
              {
                mode: "auto-collapse" as ReasoningDisplayMode,
                title: () => t("settings.reasoningAutoCollapse"),
                desc: () => t("settings.reasoningAutoCollapseDesc"),
              },
              {
                mode: "expanded" as ReasoningDisplayMode,
                title: () => t("settings.reasoningExpanded"),
                desc: () => t("settings.reasoningExpandedDesc"),
              },
              {
                mode: "collapsed" as ReasoningDisplayMode,
                title: () => t("settings.reasoningCollapsed"),
                desc: () => t("settings.reasoningCollapsedDesc"),
              },
            ]}
          >
            {(item) => (
              <button
                type="button"
                onClick={() => updateAppSetting("reasoningDisplay", item.mode)}
                class={`flex flex-col items-start gap-1 rounded-xl border p-3 text-left transition-all cursor-pointer ${
                  appSettings.reasoningDisplay === item.mode
                    ? "border-[var(--accent-border)] bg-[var(--accent-surface)]"
                    : "border-[var(--border-subtle)] bg-[var(--bg-inset)] hover:border-[var(--border-strong)]"
                }`}
              >
                <div class="flex w-full items-center justify-between">
                  <span class="text-[12px] font-semibold text-[var(--text-primary)]">
                    {item.title()}
                  </span>
                  <Show when={appSettings.reasoningDisplay === item.mode}>
                    <Check size={13} class="text-[var(--accent-primary)]" />
                  </Show>
                </div>
                <span class="text-[11px] text-[var(--text-muted)] leading-snug">
                  {item.desc()}
                </span>
              </button>
            )}
          </For>
        </div>
      </section>

      {/* 3. Timeline & Interaction Toggles */}
      <section class="border-t border-[var(--border-subtle)] pt-5 space-y-2.5">
        <h3 class="text-[13.5px] font-semibold text-[var(--text-primary)]">
          {t("settings.sectionStreamPrefsTitle")}
        </h3>

        <label class="flex items-center justify-between gap-3 rounded-xl border border-[var(--border-subtle)] bg-[var(--bg-inset)] px-4 py-2.5 cursor-pointer">
          <div class="min-w-0">
            <div class="text-[12.5px] font-medium text-[var(--text-primary)]">
              {t("settings.autoScrollLabel")}
            </div>
            <div class="text-[11px] text-[var(--text-muted)]">
              {t("settings.autoScrollDesc")}
            </div>
          </div>
          <input
            type="checkbox"
            checked={appSettings.autoScrollStream}
            onChange={(ev) => updateAppSetting("autoScrollStream", ev.currentTarget.checked)}
            class="h-4 w-4 accent-[var(--accent-primary)] cursor-pointer shrink-0"
          />
        </label>

        <label class="flex items-center justify-between gap-3 rounded-xl border border-[var(--border-subtle)] bg-[var(--bg-inset)] px-4 py-2.5 cursor-pointer">
          <div class="min-w-0">
            <div class="text-[12.5px] font-medium text-[var(--text-primary)]">
              {t("settings.confirmRevertLabel")}
            </div>
            <div class="text-[11px] text-[var(--text-muted)]">
              {t("settings.confirmRevertDesc")}
            </div>
          </div>
          <input
            type="checkbox"
            checked={appSettings.confirmBeforeRevert}
            onChange={(ev) => updateAppSetting("confirmBeforeRevert", ev.currentTarget.checked)}
            class="h-4 w-4 accent-[var(--accent-primary)] cursor-pointer shrink-0"
          />
        </label>

        <label class="flex items-center justify-between gap-3 rounded-xl border border-[var(--border-subtle)] bg-[var(--bg-inset)] px-4 py-2.5 cursor-pointer">
          <div class="min-w-0">
            <div class="text-[12.5px] font-medium text-[var(--text-primary)]">
              {t("settings.desktopNotificationsLabel")}
            </div>
            <div class="text-[11px] text-[var(--text-muted)]">
              {t("settings.desktopNotificationsDesc")}
            </div>
          </div>
          <input
            type="checkbox"
            checked={appSettings.desktopNotifications}
            onChange={(ev) =>
              updateAppSetting("desktopNotifications", ev.currentTarget.checked)
            }
            class="h-4 w-4 accent-[var(--accent-primary)] cursor-pointer shrink-0"
          />
        </label>
      </section>

      {/* 4. Global Custom Instructions */}
      <section class="border-t border-[var(--border-subtle)] pt-5 space-y-2">
        <div>
          <h3 class="text-[13.5px] font-semibold text-[var(--text-primary)]">
            {t("settings.sectionCustomInstructionsTitle")}
          </h3>
          <p class="text-[11.5px] text-[var(--text-muted)]">
            {t("settings.sectionCustomInstructionsDesc")}
          </p>
        </div>
        <textarea
          rows={3}
          value={appSettings.customSystemPrompt}
          placeholder={t("settings.customInstructionsPlaceholder")}
          onInput={(ev) => updateAppSetting("customSystemPrompt", ev.currentTarget.value)}
          class="w-full rounded-xl border border-[var(--border-strong)] bg-[var(--bg-elevated)] px-3.5 py-2.5 text-[12px] leading-relaxed text-[var(--text-primary)] placeholder:text-[var(--text-faint)] outline-none focus:border-[var(--accent-primary)] resize-y selectable-text"
        />
      </section>
    </div>
  );
}
