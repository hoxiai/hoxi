import { reconcile, unwrap } from "solid-js/store";
import {
  APP_SETTINGS_KEY, appSettings, loadInitialSettings, openSettingsModal, resetAllAppSettings, setAppSettings,
  syncSettingsSideEffects, updateAppSetting,
} from "../store/settings";
import { setWorkspace, workspace } from "../store/workspace";
import { assertEqual, describe, it } from "./test-harness";

function withAppearanceFixture(run: (root: HTMLElement, saved: Map<string, string>) => void) {
  const settings = structuredClone(unwrap(appSettings));
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
    setWorkspace(reconcile(state));
    ["document", "localStorage"].forEach((key, index) => {
      const descriptor = descriptors[index];
      if (descriptor) Object.defineProperty(globalThis, key, descriptor);
      else Reflect.deleteProperty(globalThis, key);
    });
  }
}

describe("appearance preferences", () => {
  it("persists the palette independently of light and dark modes", () => {
    withAppearanceFixture((root, saved) => {
      updateAppSetting("themeMode", "light");
      updateAppSetting("colorScheme", "classic");
      assertEqual(root.dataset.colorScheme, "classic");
      assertEqual(root.classList.contains("dark"), false);
      updateAppSetting("themeMode", "dark");
      assertEqual(root.dataset.colorScheme, "classic");
      assertEqual(root.classList.contains("dark"), true);
      assertEqual(JSON.parse(saved.get(APP_SETTINGS_KEY)!).colorScheme, "classic");
      updateAppSetting("colorScheme", "blue");
      assertEqual(root.classList.contains("dark"), true);
      assertEqual(root.dataset.colorScheme, "blue");
    });
  });

  it("applies restored preferences and resets to default blue", () => {
    withAppearanceFixture((root, saved) => {
      setAppSettings({ colorScheme: "classic", themeMode: "dark" });
      syncSettingsSideEffects();
      assertEqual(root.dataset.colorScheme, "classic");
      assertEqual(root.classList.contains("dark"), true);
      resetAllAppSettings();
      assertEqual(root.dataset.colorScheme, "blue");
      assertEqual(root.classList.contains("dark"), false);
      assertEqual(JSON.parse(saved.get(APP_SETTINGS_KEY)!).colorScheme, "blue");
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
});
