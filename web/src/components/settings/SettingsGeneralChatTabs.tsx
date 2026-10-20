// web/src/components/settings/SettingsGeneralChatTabs.tsx
import {
  Bell,
  CheckCircle2,
  CornerDownLeft,
  ExternalLink,
  Globe,
  Monitor,
  Moon,
  Palette,
  RotateCcw,
  ShieldAlert,
  Sparkles,
  Sun,
  Timer,
  Type,
  Volume2,
} from "lucide-solid";
import {
  appSettings,
  playNotificationSound,
  updateAppSetting,
  type ColorScheme,
  type FontScale,
  type LinkOpenTarget,
  type ReasoningDisplayMode,
  type SendShortcutMode,
  type ThemeMode,
} from "../../store/workspace";
import { locale, setLocale, t } from "../../i18n";
import {
  HelpTip,
  SegmentedPills,
  SettingGroup,
  SettingRow,
  ToggleSwitch,
} from "./SettingsModal";

export function GeneralAppearanceTab() {
  return (
    <div class="space-y-6">
      {/* 1. Appearance & Language Group */}
      <SettingGroup>
        <SettingRow
          icon={Globe}
          label={t("settings.sectionLanguageTitle")}
          tip={t("settings.sectionLanguageDesc")}
        >
          <SegmentedPills
            value={locale()}
            options={[
              {
                id: "zh-CN",
                label: () => t("settings.langZhLabel"),
                tip: () => t("settings.langZhDesc"),
              },
              {
                id: "en-US",
                label: () => t("settings.langEnLabel"),
                tip: () => t("settings.langEnDesc"),
              },
            ]}
            onChange={(val) => setLocale(val)}
          />
        </SettingRow>

        <SettingRow
          icon={Sun}
          label={t("settings.sectionThemeTitle")}
          tip={t("settings.sectionThemeDesc")}
        >
          <SegmentedPills<ThemeMode>
            value={appSettings.themeMode}
            options={[
              {
                id: "light",
                label: () => t("settings.themeLight"),
                tip: () => t("settings.themeLightDesc"),
                icon: Sun,
              },
              {
                id: "dark",
                label: () => t("settings.themeDark"),
                tip: () => t("settings.themeDarkDesc"),
                icon: Moon,
              },
              {
                id: "system",
                label: () => t("settings.themeSystem"),
                tip: () => t("settings.themeSystemDesc"),
                icon: Monitor,
              },
            ]}
            onChange={(val) => updateAppSetting("themeMode", val)}
          />
        </SettingRow>

        <SettingRow
          icon={Palette}
          label={t("settings.sectionColorSchemeTitle")}
          tip={t("settings.sectionColorSchemeDesc")}
        >
          <SegmentedPills<ColorScheme>
            value={appSettings.colorScheme}
            options={[
              { id: "blue", label: () => t("settings.colorSchemeBlue") },
              { id: "classic", label: () => t("settings.colorSchemeClassic") },
              { id: "warm", label: () => t("settings.colorSchemeWarm") },
              { id: "midnight", label: () => t("settings.colorSchemeMidnight") },
            ]}
            onChange={(val) => updateAppSetting("colorScheme", val)}
          />
        </SettingRow>

        <SettingRow
          icon={Type}
          label={t("settings.sectionFontScaleTitle")}
          tip={t("settings.sectionFontScaleDesc")}
        >
          <SegmentedPills<FontScale>
            value={appSettings.fontScale}
            options={[
              { id: "compact", label: () => t("settings.fontCompact") },
              { id: "default", label: () => t("settings.fontDefault") },
              { id: "comfortable", label: () => t("settings.fontComfortable") },
              { id: "large", label: () => t("settings.fontLarge") },
            ]}
            onChange={(val) => updateAppSetting("fontScale", val)}
          />
        </SettingRow>
      </SettingGroup>

      <SettingGroup
        title={t("settings.sectionSidebarHistoryTitle")}
        tip={t("settings.sectionSidebarHistoryDesc")}
      >
        <SettingRow
          label={t("settings.sidebarAutoCollapseLabel")}
          tip={t("settings.sidebarAutoCollapseDesc")}
        >
          <ToggleSwitch
            checked={appSettings.sidebarAutoCollapse}
            onChange={(val) => updateAppSetting("sidebarAutoCollapse", val)}
          />
        </SettingRow>
        <SettingRow
          label={t("settings.sidebarMinSessionsLabel")}
          tip={t("settings.sidebarMinSessionsDesc")}
        >
          <input
            type="number"
            min={1}
            max={100}
            step={1}
            aria-label={t("settings.sidebarMinSessionsLabel")}
            value={appSettings.sidebarMinSessions}
            disabled={!appSettings.sidebarAutoCollapse}
            onChange={(ev) => {
              const input = ev.currentTarget;
              if (input.validity.valid && Number.isInteger(input.valueAsNumber)) {
                updateAppSetting("sidebarMinSessions", input.valueAsNumber);
              } else {
                input.value = String(appSettings.sidebarMinSessions);
              }
            }}
            class="w-24 rounded-xl border border-[var(--border-subtle)] bg-[var(--bg-panel)] px-3.5 py-2 text-[13px] text-[var(--text-primary)] outline-none focus:border-[var(--accent-primary)] disabled:opacity-50 disabled:cursor-not-allowed"
          />
        </SettingRow>
        <SettingRow
          label={t("settings.sidebarRecentMinutesLabel")}
          tip={t("settings.sidebarRecentMinutesDesc")}
        >
          <input
            type="number"
            min={0}
            max={10080}
            step={1}
            aria-label={t("settings.sidebarRecentMinutesLabel")}
            value={appSettings.sidebarRecentMinutes}
            disabled={!appSettings.sidebarAutoCollapse}
            onChange={(ev) => {
              const input = ev.currentTarget;
              if (input.validity.valid && Number.isInteger(input.valueAsNumber)) {
                updateAppSetting("sidebarRecentMinutes", input.valueAsNumber);
              } else {
                input.value = String(appSettings.sidebarRecentMinutes);
              }
            }}
            class="w-24 rounded-xl border border-[var(--border-subtle)] bg-[var(--bg-panel)] px-3.5 py-2 text-[13px] text-[var(--text-primary)] outline-none focus:border-[var(--accent-primary)] disabled:opacity-50 disabled:cursor-not-allowed"
          />
        </SettingRow>
      </SettingGroup>
    </div>
  );
}

export function ChatComposerTab() {
  const testNotificationSound = () => {
    playNotificationSound(true);
  };

  const testDesktopNotification = () => {
    if (typeof window === "undefined" || !("Notification" in window)) return;
    if (Notification.permission === "granted") {
      new Notification("Hoxi", { body: t("settings.desktopNotificationsDesc") });
    } else {
      void Notification.requestPermission().then((perm) => {
        if (perm === "granted") {
          new Notification("Hoxi", {
            body: t("settings.desktopNotificationsDesc"),
          });
        }
      });
    }
  };

  return (
    <div class="space-y-6">
      {/* 1. Composer & Stream Preferences */}
      <SettingGroup>
        <SettingRow
          icon={CornerDownLeft}
          label={t("settings.sectionSendShortcutTitle")}
          tip={t("settings.sectionSendShortcutDesc")}
        >
          <SegmentedPills<SendShortcutMode>
            value={appSettings.sendShortcut}
            options={[
              {
                id: "enter",
                label: () => "Enter",
                tip: () => t("settings.shortcutEnterDesc"),
              },
              {
                id: "mod-enter",
                label: () => "⌘/Ctrl + Enter",
                tip: () => t("settings.shortcutModEnterDesc"),
              },
            ]}
            onChange={(val) => updateAppSetting("sendShortcut", val)}
          />
        </SettingRow>

        <SettingRow
          icon={ExternalLink}
          label={t("settings.sectionLinkOpenTitle")}
          tip={t("settings.sectionLinkOpenDesc")}
        >
          <SegmentedPills<LinkOpenTarget>
            value={appSettings.linkOpenTarget}
            options={[
              {
                id: "external",
                label: () => t("settings.linkOpenExternal"),
                tip: () => t("settings.linkOpenExternalDesc"),
                icon: ExternalLink,
              },
              {
                id: "inspector",
                label: () => t("settings.linkOpenInspector"),
                tip: () => t("settings.linkOpenInspectorDesc"),
                icon: Globe,
              },
            ]}
            onChange={(val) => updateAppSetting("linkOpenTarget", val)}
          />
        </SettingRow>

        <SettingRow
          icon={Sparkles}
          label={t("settings.sectionReasoningTitle")}
          tip={t("settings.sectionReasoningDesc")}
        >
          <SegmentedPills<ReasoningDisplayMode>
            value={appSettings.reasoningDisplay}
            options={[
              {
                id: "auto-collapse",
                label: () => t("settings.reasoningAutoCollapse"),
                tip: () => t("settings.reasoningAutoCollapseDesc"),
              },
              {
                id: "expanded",
                label: () => t("settings.reasoningExpanded"),
                tip: () => t("settings.reasoningExpandedDesc"),
              },
              {
                id: "collapsed",
                label: () => t("settings.reasoningCollapsed"),
                tip: () => t("settings.reasoningCollapsedDesc"),
              },
            ]}
            onChange={(val) => updateAppSetting("reasoningDisplay", val)}
          />
        </SettingRow>

        <SettingRow
          label={t("settings.autoScrollLabel")}
          tip={t("settings.autoScrollDesc")}
        >
          <ToggleSwitch
            checked={appSettings.autoScrollStream}
            onChange={(val) => updateAppSetting("autoScrollStream", val)}
          />
        </SettingRow>

        <SettingRow
          icon={RotateCcw}
          label={t("settings.confirmRevertLabel")}
          tip={t("settings.confirmRevertDesc")}
        >
          <ToggleSwitch
            checked={appSettings.confirmBeforeRevert}
            onChange={(val) => updateAppSetting("confirmBeforeRevert", val)}
          />
        </SettingRow>

        <SettingRow
          icon={Bell}
          label={t("settings.desktopNotificationsLabel")}
          tip={t("settings.desktopNotificationsDesc")}
        >
          <button
            type="button"
            onClick={testDesktopNotification}
            class="rounded-lg border border-[var(--border-subtle)] bg-[var(--bg-panel)] px-3 py-1 text-[13px] font-medium text-[var(--text-secondary)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-hover)] transition-colors cursor-pointer"
          >
            {t("settings.testNotificationBtn")}
          </button>
          <ToggleSwitch
            checked={appSettings.desktopNotifications}
            onChange={(val) => updateAppSetting("desktopNotifications", val)}
          />
        </SettingRow>

        <SettingRow
          icon={Volume2}
          label={t("settings.notificationSoundLabel")}
          tip={t("settings.notificationSoundDesc")}
        >
          <button
            type="button"
            onClick={testNotificationSound}
            class="rounded-lg border border-[var(--border-subtle)] bg-[var(--bg-panel)] px-3 py-1 text-[13px] font-medium text-[var(--text-secondary)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-hover)] transition-colors cursor-pointer"
          >
            {t("settings.testSoundBtn")}
          </button>
          <ToggleSwitch
            checked={appSettings.notificationSound}
            onChange={(val) => updateAppSetting("notificationSound", val)}
          />
        </SettingRow>

        <SettingRow
          icon={CheckCircle2}
          label={t("settings.notifyOnCompleteLabel")}
          tip={t("settings.notifyOnCompleteDesc")}
        >
          <ToggleSwitch
            checked={appSettings.notifyOnComplete}
            onChange={(val) => updateAppSetting("notifyOnComplete", val)}
          />
        </SettingRow>

        <SettingRow
          icon={ShieldAlert}
          label={t("settings.notifyOnAttentionLabel")}
          tip={t("settings.notifyOnAttentionDesc")}
        >
          <ToggleSwitch
            checked={appSettings.notifyOnAttention}
            onChange={(val) => updateAppSetting("notifyOnAttention", val)}
          />
        </SettingRow>

        <SettingRow
          icon={Timer}
          label={t("settings.notifyOnStallLabel")}
          tip={t("settings.notifyOnStallDesc")}
        >
          <ToggleSwitch
            checked={appSettings.notifyOnStall}
            onChange={(val) => updateAppSetting("notifyOnStall", val)}
          />
        </SettingRow>

      </SettingGroup>

      {/* 2. Global Custom Instructions */}
      <section class="space-y-2">
        <div class="flex items-center gap-2 px-1">
          <h3 class="text-[13px] font-semibold text-[var(--text-secondary)]">
            {t("settings.sectionCustomInstructionsTitle")}
          </h3>
          <HelpTip text={t("settings.sectionCustomInstructionsDesc")} />
        </div>
        <textarea
          rows={3}
          value={appSettings.customSystemPrompt}
          placeholder={t("settings.customInstructionsPlaceholder")}
          onInput={(ev) =>
            updateAppSetting("customSystemPrompt", ev.currentTarget.value)
          }
          class="w-full rounded-2xl border border-[var(--border-subtle)] bg-[var(--bg-inset)]/35 p-4 font-mono text-[13px] leading-relaxed text-[var(--text-primary)] placeholder:text-[var(--text-faint)] outline-none focus:border-[var(--accent-primary)] resize-y"
        />
      </section>
    </div>
  );
}
