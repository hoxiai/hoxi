// web/src/store/settings.ts
import { createStore } from "solid-js/store";
import { hoxiAPI } from "../services/api";
import type { ApprovalMode } from "./approval-mode";
import {
  applyThemeClass,
  setWorkspace,
  THEME_KEY,
  workspace,
  type ProviderModalTab,
} from "./workspace";

export type SettingsTab =
  | "general"
  | "chat"
  | "workspace"
  | "terminal"
  | "system"
  | "privacy"
  | "diagnostics"
  | ProviderModalTab;
export type ThemeMode = "light" | "dark" | "system";
export type ColorScheme = "blue" | "classic" | "warm" | "midnight";

export const COLOR_SCHEMES: ColorScheme[] = ["blue", "classic", "warm", "midnight"];
export type FontScale = "compact" | "default" | "comfortable" | "large";
export type SendShortcutMode = "enter" | "mod-enter";
export type ReasoningDisplayMode = "auto-collapse" | "expanded" | "collapsed";
export type DiffLayoutMode = "unified" | "split";
export type WorktreeApplyStrategy = "patch" | "merge";
export type LinkOpenTarget = "external" | "inspector";

export interface AppSettingsState {
  modalOpen: boolean;
  activeTab: SettingsTab;
  themeMode: ThemeMode;
  colorScheme: ColorScheme;
  fontScale: FontScale;
  sidebarAutoCollapse: boolean;
  sidebarMinSessions: number;
  sidebarRecentMinutes: number;
  userLoggedIn: boolean;
  userDisplayName: string;
  userEmail: string;
  detectedSystemUsername: string;
  detectedGitUserName: string;
  detectedGitUserEmail: string;
  sendShortcut: SendShortcutMode;
  approvalMode: ApprovalMode;
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
  notificationSound: boolean;
  notifyOnComplete: boolean;
  notifyOnAttention: boolean;
  notifyOnStall: boolean;
  keepAwakeWhileRunning: boolean;
  linkOpenTarget: LinkOpenTarget;
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
  colorScheme: "blue",
  fontScale: "default",
  sidebarAutoCollapse: true,
  sidebarMinSessions: 7,
  sidebarRecentMinutes: 60,
  userLoggedIn: false,
  userDisplayName: "",
  userEmail: "",
  sendShortcut: "enter",
  approvalMode: "auto",
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
  notificationSound: true,
  notifyOnComplete: true,
  notifyOnAttention: true,
  notifyOnStall: true,
  keepAwakeWhileRunning: true,
  linkOpenTarget: "external",
};

function isColorScheme(value: unknown): value is ColorScheme {
  return typeof value === "string" && (COLOR_SCHEMES as string[]).includes(value);
}

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
      colorScheme: isColorScheme(parsed.colorScheme) ? parsed.colorScheme : "blue",
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
      sidebarAutoCollapse:
        typeof parsed.sidebarAutoCollapse === "boolean"
          ? parsed.sidebarAutoCollapse
          : DEFAULT_SETTINGS.sidebarAutoCollapse,
      sidebarMinSessions:
        Number.isInteger(parsed.sidebarMinSessions) &&
        parsed.sidebarMinSessions >= 1 &&
        parsed.sidebarMinSessions <= 100
          ? parsed.sidebarMinSessions
          : DEFAULT_SETTINGS.sidebarMinSessions,
      sidebarRecentMinutes:
        Number.isInteger(parsed.sidebarRecentMinutes) &&
        parsed.sidebarRecentMinutes >= 0 &&
        parsed.sidebarRecentMinutes <= 10080
          ? parsed.sidebarRecentMinutes
          : DEFAULT_SETTINGS.sidebarRecentMinutes,
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
      approvalMode:
        parsed.approvalMode === "auto" ||
        parsed.approvalMode === "smart" ||
        parsed.approvalMode === "manual"
          ? parsed.approvalMode
          : DEFAULT_SETTINGS.approvalMode,
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
      notificationSound:
        typeof parsed.notificationSound === "boolean"
          ? parsed.notificationSound
          : DEFAULT_SETTINGS.notificationSound,
      notifyOnComplete:
        typeof parsed.notifyOnComplete === "boolean"
          ? parsed.notifyOnComplete
          : DEFAULT_SETTINGS.notifyOnComplete,
      notifyOnAttention:
        typeof parsed.notifyOnAttention === "boolean"
          ? parsed.notifyOnAttention
          : DEFAULT_SETTINGS.notifyOnAttention,
      notifyOnStall:
        typeof parsed.notifyOnStall === "boolean"
          ? parsed.notifyOnStall
          : DEFAULT_SETTINGS.notifyOnStall,
      keepAwakeWhileRunning:
        typeof parsed.keepAwakeWhileRunning === "boolean"
          ? parsed.keepAwakeWhileRunning
          : DEFAULT_SETTINGS.keepAwakeWhileRunning,
      linkOpenTarget:
        parsed.linkOpenTarget === "external" || parsed.linkOpenTarget === "inspector"
          ? parsed.linkOpenTarget
          : DEFAULT_SETTINGS.linkOpenTarget,
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
      colorScheme: appSettings.colorScheme,
      fontScale: appSettings.fontScale,
      sidebarAutoCollapse: appSettings.sidebarAutoCollapse,
      sidebarMinSessions: appSettings.sidebarMinSessions,
      sidebarRecentMinutes: appSettings.sidebarRecentMinutes,
      userLoggedIn: appSettings.userLoggedIn,
      userDisplayName: appSettings.userDisplayName,
      userEmail: appSettings.userEmail,
      sendShortcut: appSettings.sendShortcut,
      approvalMode: appSettings.approvalMode,
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
      notificationSound: appSettings.notificationSound,
      notifyOnComplete: appSettings.notifyOnComplete,
      notifyOnAttention: appSettings.notifyOnAttention,
      notifyOnStall: appSettings.notifyOnStall,
      keepAwakeWhileRunning: appSettings.keepAwakeWhileRunning,
      linkOpenTarget: appSettings.linkOpenTarget,
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
  setAppSettings({ userLoggedIn: false, userDisplayName: "", userEmail: "" });
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
  const map: Record<FontScale, { ui: string; chat: string; code: string }> = {
    compact: { ui: "12.5px", chat: "13.5px", code: "12px" },
    default: { ui: "13px", chat: "14.5px", code: "13px" },
    comfortable: { ui: "13.5px", chat: "15.5px", code: "13.5px" },
    large: { ui: "14px", chat: "16.5px", code: "14px" },
  };
  const chosen = map[scale] || map.default;
  document.documentElement.style.setProperty("--app-ui-text-size", chosen.ui);
  document.documentElement.style.setProperty("--app-chat-text-size", chosen.chat);
  document.documentElement.style.setProperty("--app-code-text-size", chosen.code);
}

export function syncSettingsSideEffects() {
  const effective = resolveEffectiveTheme(appSettings.themeMode);
  setWorkspace("theme", effective);
  setWorkspace("sessionSandboxMode", appSettings.defaultSandboxMode);
  applyThemeClass(effective);
  if (typeof document !== "undefined") {
    document.documentElement.dataset.colorScheme = appSettings.colorScheme;
  }
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
  if (
    tab === "providers" ||
    tab === "custom" ||
    tab === "auto" ||
    tab === "models" ||
    tab === "policy"
  ) {
    if (tab !== "custom") {
      setWorkspace("editingProviderID", null);
    }
    setWorkspace({ providerModalTab: tab, providerModalOpen: false });
  } else {
    setWorkspace("providerModalOpen", false);
  }
  setAppSettings({ activeTab: tab, modalOpen: true });
}

export function openProviderModal(
  tab: ProviderModalTab = "providers",
  editProviderID: string | null = null
) {
  setWorkspace({
    providerModalTab: tab,
    editingProviderID: editProviderID,
    providerModalOpen: true,
  });
  setAppSettings({ activeTab: tab, modalOpen: true });
}

export function closeSettingsModal() {
  setAppSettings("modalOpen", false);
  setWorkspace("providerModalOpen", false);
}

export function updateAppSetting<K extends keyof PersistedSettings>(
  key: K,
  value: PersistedSettings[K]
) {
  setAppSettings(key as any, value as any);
  persistAppSettings();
  if (key === "themeMode" || key === "colorScheme" || key === "fontScale" || key === "defaultSandboxMode") {
    syncSettingsSideEffects();
  }
  if (key === "keepAwakeWhileRunning") {
    void hoxiAPI.setKeepAwake(Boolean(value)).catch(() => {});
  }
}

export function setSessionSandboxMode(mode: "local" | "worktree") {
  updateAppSetting("defaultSandboxMode", mode);
}

export function setTheme(theme: "light" | "dark") {
  updateAppSetting("themeMode", theme);
}

export function toggleTheme() {
  setTheme(workspace.theme === "dark" ? "light" : "dark");
}

export function syncKeepAwakeFromBackend(enabled: boolean) {
  if (appSettings.keepAwakeWhileRunning !== enabled) {
    setAppSettings("keepAwakeWhileRunning", enabled);
    persistAppSettings();
  }
}

export function resetAllAppSettings() {
  setAppSettings({
    ...DEFAULT_SETTINGS,
  });
  persistAppSettings();
  syncSettingsSideEffects();
}

export type NotificationKind = "complete" | "attention" | "stall";

export interface DesktopNotificationOptions {
  sessionID?: string;
  kind?: NotificationKind;
}

const NOTIFICATION_DEDUPE_MS = 8000;
const recentNotifications = new Map<string, number>();
let notificationAudioContext: AudioContext | null = null;

function notificationKindEnabled(kind: NotificationKind): boolean {
  if (kind === "complete") return appSettings.notifyOnComplete;
  if (kind === "attention") return appSettings.notifyOnAttention;
  return appSettings.notifyOnStall;
}

export function playNotificationSound(force = false) {
  if (!force && !appSettings.notificationSound) return;
  if (typeof window === "undefined") return;
  const AudioCtor =
    window.AudioContext ||
    (window as typeof window & { webkitAudioContext?: typeof AudioContext }).webkitAudioContext;
  if (!AudioCtor) return;
  try {
    notificationAudioContext ??= new AudioCtor();
    const ctx = notificationAudioContext;
    if (ctx.state === "suspended") void ctx.resume();
    const now = ctx.currentTime;
    const oscillator = ctx.createOscillator();
    const gain = ctx.createGain();
    oscillator.type = "sine";
    oscillator.frequency.setValueAtTime(880, now);
    oscillator.frequency.exponentialRampToValueAtTime(1174.66, now + 0.12);
    gain.gain.setValueAtTime(0.0001, now);
    gain.gain.exponentialRampToValueAtTime(0.12, now + 0.02);
    gain.gain.exponentialRampToValueAtTime(0.0001, now + 0.22);
    oscillator.connect(gain);
    gain.connect(ctx.destination);
    oscillator.start(now);
    oscillator.stop(now + 0.24);
  } catch {
    /* audio unavailable */
  }
}

export function triggerDesktopNotification(
  title: string,
  body: string,
  options: DesktopNotificationOptions = {}
) {
  if (!appSettings.desktopNotifications) return;
  const kind = options.kind ?? "complete";
  if (!notificationKindEnabled(kind)) return;
  if (typeof document !== "undefined" && document.hasFocus()) return;

  const dedupeKey = `${kind}:${options.sessionID || ""}:${title}`;
  const now = Date.now();
  const previous = recentNotifications.get(dedupeKey);
  if (previous && now - previous < NOTIFICATION_DEDUPE_MS) return;
  recentNotifications.set(dedupeKey, now);
  setTimeout(() => {
    if (recentNotifications.get(dedupeKey) === now) recentNotifications.delete(dedupeKey);
  }, NOTIFICATION_DEDUPE_MS);

  playNotificationSound();

  if (typeof window === "undefined" || !("Notification" in window)) return;
  try {
    const show = () => {
      const notification = new Notification(title, { body, silent: true });
      notification.onclick = () => {
        window.focus();
        if (options.sessionID) {
          window.dispatchEvent(
            new CustomEvent("hoxi:notification-click", {
              detail: { sessionID: options.sessionID },
            })
          );
        }
        notification.close();
      };
    };
    if (Notification.permission === "granted") {
      show();
    } else if (Notification.permission !== "denied") {
      void Notification.requestPermission().then((perm) => {
        if (perm === "granted") show();
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
