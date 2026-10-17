// web/src/store/settings.ts
import { createStore } from "solid-js/store";
import { applyThemeClass, setWorkspace, THEME_KEY, workspace } from "./workspace";

export type SettingsTab = "general" | "chat" | "workspace" | "terminal" | "system";
export type ThemeMode = "light" | "dark" | "system";
export type FontScale = "compact" | "default" | "comfortable" | "large";
export type SendShortcutMode = "enter" | "mod-enter";
export type ReasoningDisplayMode = "auto-collapse" | "expanded" | "collapsed";
export type DiffLayoutMode = "unified" | "split";
export type WorktreeApplyStrategy = "patch" | "merge";

export interface AppSettingsState {
  modalOpen: boolean;
  activeTab: SettingsTab;
  themeMode: ThemeMode;
  fontScale: FontScale;
  userLoggedIn: boolean;
  userDisplayName: string;
  userEmail: string;
  detectedSystemUsername: string;
  detectedGitUserName: string;
  detectedGitUserEmail: string;
  sendShortcut: SendShortcutMode;
  reasoningDisplay: ReasoningDisplayMode;
  autoScrollStream: boolean;
  confirmBeforeRevert: boolean;
  customSystemPrompt: string;
  defaultSandboxMode: "local" | "worktree";
  defaultDiffStyle: DiffLayoutMode;
  worktreeApplyMode: WorktreeApplyStrategy;
  terminalFontSize: number;
  terminalCursorBlink: boolean;
  desktopNotifications: boolean;
}

export const APP_SETTINGS_KEY = "hoxi-app-settings:v1";

type PersistedSettings = Omit<
  AppSettingsState,
  | "modalOpen"
  | "activeTab"
  | "detectedSystemUsername"
  | "detectedGitUserName"
  | "detectedGitUserEmail"
>;

const DEFAULT_SETTINGS: PersistedSettings = {
  themeMode: "light",
  fontScale: "default",
  userLoggedIn: true,
  userDisplayName: "",
  userEmail: "",
  sendShortcut: "enter",
  reasoningDisplay: "auto-collapse",
  autoScrollStream: true,
  confirmBeforeRevert: true,
  customSystemPrompt: "",
  defaultSandboxMode: "local",
  defaultDiffStyle: "unified",
  worktreeApplyMode: "patch",
  terminalFontSize: 12,
  terminalCursorBlink: true,
  desktopNotifications: true,
};

function loadInitialSettings(): PersistedSettings {
  let baseTheme: ThemeMode = "light";
  try {
    const savedLegacyTheme = localStorage.getItem(THEME_KEY);
    if (savedLegacyTheme === "dark" || savedLegacyTheme === "light") {
      baseTheme = savedLegacyTheme;
    }
  } catch {
    /* ignore */
  }

  try {
    const raw = localStorage.getItem(APP_SETTINGS_KEY);
    if (!raw) {
      return { ...DEFAULT_SETTINGS, themeMode: baseTheme };
    }
    const parsed = JSON.parse(raw);
    if (!parsed || typeof parsed !== "object") {
      return { ...DEFAULT_SETTINGS, themeMode: baseTheme };
    }
    return {
      themeMode:
        parsed.themeMode === "light" ||
        parsed.themeMode === "dark" ||
        parsed.themeMode === "system"
          ? parsed.themeMode
          : baseTheme,
      fontScale:
        parsed.fontScale === "compact" ||
        parsed.fontScale === "default" ||
        parsed.fontScale === "comfortable" ||
        parsed.fontScale === "large"
          ? parsed.fontScale
          : DEFAULT_SETTINGS.fontScale,
      userLoggedIn:
        typeof parsed.userLoggedIn === "boolean"
          ? parsed.userLoggedIn
          : DEFAULT_SETTINGS.userLoggedIn,
      userDisplayName:
        typeof parsed.userDisplayName === "string" ? parsed.userDisplayName : "",
      userEmail:
        typeof parsed.userEmail === "string" ? parsed.userEmail : "",
      sendShortcut:
        parsed.sendShortcut === "enter" || parsed.sendShortcut === "mod-enter"
          ? parsed.sendShortcut
          : DEFAULT_SETTINGS.sendShortcut,
      reasoningDisplay:
        parsed.reasoningDisplay === "auto-collapse" ||
        parsed.reasoningDisplay === "expanded" ||
        parsed.reasoningDisplay === "collapsed"
          ? parsed.reasoningDisplay
          : DEFAULT_SETTINGS.reasoningDisplay,
      autoScrollStream:
        typeof parsed.autoScrollStream === "boolean"
          ? parsed.autoScrollStream
          : DEFAULT_SETTINGS.autoScrollStream,
      confirmBeforeRevert:
        typeof parsed.confirmBeforeRevert === "boolean"
          ? parsed.confirmBeforeRevert
          : DEFAULT_SETTINGS.confirmBeforeRevert,
      customSystemPrompt:
        typeof parsed.customSystemPrompt === "string"
          ? parsed.customSystemPrompt
          : DEFAULT_SETTINGS.customSystemPrompt,
      defaultSandboxMode:
        parsed.defaultSandboxMode === "local" || parsed.defaultSandboxMode === "worktree"
          ? parsed.defaultSandboxMode
          : DEFAULT_SETTINGS.defaultSandboxMode,
      defaultDiffStyle:
        parsed.defaultDiffStyle === "unified" || parsed.defaultDiffStyle === "split"
          ? parsed.defaultDiffStyle
          : DEFAULT_SETTINGS.defaultDiffStyle,
      worktreeApplyMode:
        parsed.worktreeApplyMode === "patch" || parsed.worktreeApplyMode === "merge"
          ? parsed.worktreeApplyMode
          : DEFAULT_SETTINGS.worktreeApplyMode,
      terminalFontSize:
        typeof parsed.terminalFontSize === "number" &&
        parsed.terminalFontSize >= 10 &&
        parsed.terminalFontSize <= 18
          ? parsed.terminalFontSize
          : DEFAULT_SETTINGS.terminalFontSize,
      terminalCursorBlink:
        typeof parsed.terminalCursorBlink === "boolean"
          ? parsed.terminalCursorBlink
          : DEFAULT_SETTINGS.terminalCursorBlink,
      desktopNotifications:
        typeof parsed.desktopNotifications === "boolean"
          ? parsed.desktopNotifications
          : DEFAULT_SETTINGS.desktopNotifications,
    };
  } catch {
    return { ...DEFAULT_SETTINGS, themeMode: baseTheme };
  }
}

export const [appSettings, setAppSettings] = createStore<AppSettingsState>({
  modalOpen: false,
  activeTab: "general",
  detectedSystemUsername: "",
  detectedGitUserName: "",
  detectedGitUserEmail: "",
  ...loadInitialSettings(),
});

function persistAppSettings() {
  try {
    const payload: PersistedSettings = {
      themeMode: appSettings.themeMode,
      fontScale: appSettings.fontScale,
      userLoggedIn: appSettings.userLoggedIn,
      userDisplayName: appSettings.userDisplayName,
      userEmail: appSettings.userEmail,
      sendShortcut: appSettings.sendShortcut,
      reasoningDisplay: appSettings.reasoningDisplay,
      autoScrollStream: appSettings.autoScrollStream,
      confirmBeforeRevert: appSettings.confirmBeforeRevert,
      customSystemPrompt: appSettings.customSystemPrompt,
      defaultSandboxMode: appSettings.defaultSandboxMode,
      defaultDiffStyle: appSettings.defaultDiffStyle,
      worktreeApplyMode: appSettings.worktreeApplyMode,
      terminalFontSize: appSettings.terminalFontSize,
      terminalCursorBlink: appSettings.terminalCursorBlink,
      desktopNotifications: appSettings.desktopNotifications,
    };
    localStorage.setItem(APP_SETTINGS_KEY, JSON.stringify(payload));
  } catch {
    /* ignore */
  }
}

export function hydrateLocalUserIdentity(info: {
  systemUsername?: string;
  gitUserName?: string;
  gitUserEmail?: string;
}) {
  setAppSettings({
    detectedSystemUsername: info.systemUsername?.trim() || "",
    detectedGitUserName: info.gitUserName?.trim() || "",
    detectedGitUserEmail: info.gitUserEmail?.trim() || "",
  });
}

export function resolvedUserDisplayName(): string {
  const custom = appSettings.userDisplayName.trim();
  if (custom) return custom;
  if (appSettings.detectedGitUserName) return appSettings.detectedGitUserName;
  if (appSettings.detectedSystemUsername) return appSettings.detectedSystemUsername;
  const root = workspace.rootDir || "";
  const match = root.match(/^\/(?:Users|home)\/([^/]+)/);
  if (match?.[1]) return match[1];
  return "developer";
}

export function resolvedUserEmail(): string {
  const custom = appSettings.userEmail.trim();
  if (custom) return custom;
  if (appSettings.detectedGitUserEmail) return appSettings.detectedGitUserEmail;
  const nameSlug = resolvedUserDisplayName()
    .toLowerCase()
    .replace(/[^a-z0-9._-]/g, "");
  return `${nameSlug || "user"}@hoxi.local`;
}

export function signInUserAccount(name?: string, email?: string) {
  if (typeof name === "string") {
    setAppSettings("userDisplayName", name.trim());
  }
  if (typeof email === "string") {
    setAppSettings("userEmail", email.trim());
  }
  setAppSettings("userLoggedIn", true);
  persistAppSettings();
}

export function signOutUserAccount() {
  setAppSettings("userLoggedIn", false);
  persistAppSettings();
}

export function resolveEffectiveTheme(mode: ThemeMode): "light" | "dark" {
  if (mode === "light" || mode === "dark") return mode;
  if (typeof window !== "undefined" && window.matchMedia) {
    return window.matchMedia("(prefers-color-scheme: dark)").matches ? "dark" : "light";
  }
  return "light";
}

export function applyFontScaleToDOM(scale: FontScale) {
  if (typeof document === "undefined") return;
  const map: Record<FontScale, { ui: string; chat: string }> = {
    compact: { ui: "12px", chat: "13px" },
    default: { ui: "13px", chat: "14px" },
    comfortable: { ui: "13.5px", chat: "15px" },
    large: { ui: "14px", chat: "16px" },
  };
  const chosen = map[scale] || map.default;
  document.documentElement.style.setProperty("--app-ui-text-size", chosen.ui);
  document.documentElement.style.setProperty("--app-chat-text-size", chosen.chat);
}

export function syncSettingsSideEffects() {
  const effective = resolveEffectiveTheme(appSettings.themeMode);
  setWorkspace("theme", effective);
  setWorkspace("sessionSandboxMode", appSettings.defaultSandboxMode);
  applyThemeClass(effective);
  try {
    localStorage.setItem(THEME_KEY, effective);
  } catch {
    /* ignore */
  }
  applyFontScaleToDOM(appSettings.fontScale);
}

// Listen for OS theme changes when themeMode === "system"
if (typeof window !== "undefined" && window.matchMedia) {
  const mq = window.matchMedia("(prefers-color-scheme: dark)");
  mq.addEventListener?.("change", () => {
    if (appSettings.themeMode === "system") {
      syncSettingsSideEffects();
    }
  });
}

export function openSettingsModal(tab: SettingsTab = "general") {
  setAppSettings({ activeTab: tab, modalOpen: true });
}

export function closeSettingsModal() {
  setAppSettings("modalOpen", false);
}

export function updateAppSetting<K extends keyof PersistedSettings>(
  key: K,
  value: PersistedSettings[K]
) {
  setAppSettings(key as any, value as any);
  persistAppSettings();
  if (key === "themeMode" || key === "fontScale" || key === "defaultSandboxMode") {
    syncSettingsSideEffects();
  }
}

export function resetAllAppSettings() {
  setAppSettings({
    ...DEFAULT_SETTINGS,
  });
  persistAppSettings();
  syncSettingsSideEffects();
}

export function triggerDesktopNotification(title: string, body: string) {
  if (!appSettings.desktopNotifications) return;
  if (typeof document !== "undefined" && document.hasFocus()) return;
  if (typeof window === "undefined" || !("Notification" in window)) return;
  try {
    if (Notification.permission === "granted") {
      new Notification(title, { body, silent: false });
    } else if (Notification.permission !== "denied") {
      void Notification.requestPermission().then((perm) => {
        if (perm === "granted") {
          new Notification(title, { body, silent: false });
        }
      });
    }
  } catch {
    /* ignore notification errors in embedded webview */
  }
}

export function buildCombinedSystemPrompt(
  workspaceFoldersPrompt?: string
): string | undefined {
  const custom = appSettings.customSystemPrompt.trim();
  const parts: string[] = [];
  if (workspaceFoldersPrompt) parts.push(workspaceFoldersPrompt);
  if (custom) {
    parts.push(`User global custom instructions:\n${custom}`);
  }
  return parts.length > 0 ? parts.join("\n\n") : undefined;
}
