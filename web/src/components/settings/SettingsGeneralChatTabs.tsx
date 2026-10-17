// web/src/components/settings/SettingsGeneralChatTabs.tsx
import {
  Bell,
  CornerDownLeft,
  Globe,
  Mail,
  Monitor,
  Moon,
  RotateCcw,
  Sparkles,
  Sun,
  Type,
  User,
} from "lucide-solid";
import {
  appSettings,
  updateAppSetting,
  type FontScale,
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
    <div class="space-y-5">
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
                label: () => t("header.langZh"),
                tip: () => t("settings.langZhDesc"),
              },
              {
                id: "en-US",
                label: () => t("header.langEn"),
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

      {/* 2. Account & Developer Profile Group */}
      <SettingGroup
        title={t("settings.sectionProfileTitle")}
        tip={t("settings.sectionProfileDesc")}
      >
        <SettingRow icon={User} label={t("settings.accountNameInputLabel")}>
          <input
            type="text"
            value={appSettings.userDisplayName}
            placeholder={t("settings.profileNamePlaceholder")}
            onInput={(ev) =>
              updateAppSetting("userDisplayName", ev.currentTarget.value)
            }
            class="w-56 rounded-lg border border-[var(--border-subtle)] bg-[var(--bg-panel)] px-2.5 py-1 text-[12px] text-[var(--text-primary)] placeholder:text-[var(--text-faint)] outline-none focus:border-[var(--accent-primary)]"
          />
        </SettingRow>

        <SettingRow icon={Mail} label={t("settings.profileEmailLabel")}>
          <input
            type="email"
            value={appSettings.userEmail}
            placeholder={t("settings.profileEmailPlaceholder", {
              email: appSettings.detectedGitUserEmail || "you@example.com",
            })}
            onInput={(ev) => updateAppSetting("userEmail", ev.currentTarget.value)}
            class="w-56 rounded-lg border border-[var(--border-subtle)] bg-[var(--bg-panel)] px-2.5 py-1 font-mono text-[11.5px] text-[var(--text-primary)] placeholder:text-[var(--text-faint)] outline-none focus:border-[var(--accent-primary)]"
          />
        </SettingRow>
      </SettingGroup>
    </div>
  );
}

export function ChatComposerTab() {
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
    <div class="space-y-5">
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
            class="rounded-md border border-[var(--border-subtle)] bg-[var(--bg-panel)] px-2 py-0.5 text-[11px] text-[var(--text-secondary)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-hover)] transition-colors cursor-pointer"
          >
            Test
          </button>
          <ToggleSwitch
            checked={appSettings.desktopNotifications}
            onChange={(val) => updateAppSetting("desktopNotifications", val)}
          />
        </SettingRow>
      </SettingGroup>

      {/* 2. Global Custom Instructions */}
      <section class="space-y-1.5">
        <div class="flex items-center gap-1.5 px-1">
          <h3 class="text-[11.5px] font-semibold text-[var(--text-secondary)]">
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
          class="w-full rounded-xl border border-[var(--border-subtle)] bg-[var(--bg-inset)]/35 p-3 font-mono text-[11.5px] leading-relaxed text-[var(--text-primary)] placeholder:text-[var(--text-faint)] outline-none focus:border-[var(--accent-primary)] resize-y"
        />
      </section>
    </div>
  );
}
