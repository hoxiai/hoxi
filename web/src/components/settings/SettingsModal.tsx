// web/src/components/settings/SettingsModal.tsx
import { createMemo, For, type JSX, Show } from "solid-js";
import {
  ArrowLeft,
  Bot,
  CircleHelp,
  Cpu,
  FolderGit2,
  KeyRound,
  MessageSquareText,
  Moon,
  ScrollText,
  Settings,
  ShieldCheck,
  SlidersHorizontal,
  Sun,
  Terminal as TerminalIcon,
  Zap,
} from "lucide-solid";
import {
  appSettings,
  closeSettingsModal,
  openProviderModal,
  openSettingsModal,
  toggleTheme,
  workspace,
  type SettingsTab,
} from "../../store/workspace";
import { t } from "../../i18n";
import AgentPolicyMcpTab from "../models/AgentPolicyMcpTab";
import AutoModelPoolTab from "../models/AutoModelPoolTab";
import CustomEndpointTab from "../models/CustomEndpointTab";
import ModelsRoleTab from "../models/ModelsRoleTab";
import ProviderCatalogTab from "../models/ProviderCatalogTab";
import { ChatComposerTab, GeneralAppearanceTab } from "./SettingsGeneralChatTabs";
import { SystemRuntimeSettingsTab } from "./SettingsSystemRuntimeTab";
import { PrivacySettingsTab } from "./PrivacyControls";
import DiagnosticsTab from "./DiagnosticsTab";
import {
  TerminalSafetySettingsTab,
  WorkspaceGitSettingsTab,
} from "./SettingsWorkspaceTerminalTabs";

export function HelpTip(props: { text: string }) {
  return (
    <span class="relative inline-flex items-center group/tip shrink-0">
      <CircleHelp
        size={15}
        class="text-[var(--text-faint)] hover:text-[var(--text-secondary)] transition-colors cursor-help"
      />
      <span class="pointer-events-none invisible opacity-0 group-hover/tip:visible group-hover/tip:opacity-100 transition-all duration-150 absolute left-1/2 -translate-x-1/2 bottom-full mb-2.5 z-50 w-68 rounded-xl border border-[var(--border-strong)] bg-[var(--bg-panel)] px-3.5 py-2.5 text-[12.5px] font-normal leading-relaxed text-[var(--text-secondary)] shadow-xl">
        {props.text}
      </span>
    </span>
  );
}

export function SettingGroup(props: {
  title?: string;
  tip?: string;
  action?: JSX.Element;
  children: JSX.Element;
}) {
  return (
    <section class="space-y-2.5">
      <Show when={props.title || props.action}>
        <div class="flex items-center justify-between px-1">
          <div class="flex items-center gap-2">
            <Show when={props.title}>
              <h3 class="text-[13px] font-semibold text-[var(--text-secondary)]">
                {props.title}
              </h3>
            </Show>
            <Show when={props.tip}>
              <HelpTip text={props.tip!} />
            </Show>
          </div>
          <Show when={props.action}>{props.action}</Show>
        </div>
      </Show>
      <div class="rounded-2xl border border-[var(--border-subtle)] bg-[var(--bg-inset)]/35 divide-y divide-[var(--border-subtle)]/60">
        {props.children}
      </div>
    </section>
  );
}

export function SettingRow(props: {
  label: string;
  tip?: string;
  icon?: any;
  children: JSX.Element;
}) {
  const IconComp = props.icon;
  return (
    <div class="flex items-center justify-between gap-6 px-5 py-4">
      <div class="flex items-center gap-2.5 min-w-0">
        <Show when={IconComp}>
          <IconComp size={16} class="text-[var(--text-muted)] shrink-0" />
        </Show>
        <span class="text-[14px] font-medium text-[var(--text-primary)] truncate">
          {props.label}
        </span>
        <Show when={props.tip}>
          <HelpTip text={props.tip!} />
        </Show>
      </div>
      <div class="flex items-center gap-2.5 shrink-0">{props.children}</div>
    </div>
  );
}

export function SegmentedPills<T extends string>(props: {
  value: T;
  options: Array<{ id: T; label: () => string; tip?: () => string; icon?: any }>;
  onChange: (val: T) => void;
}) {
  return (
    <div class="inline-flex items-center gap-0.5 rounded-xl border border-[var(--border-subtle)] bg-[var(--bg-inset)] p-1">
      <For each={props.options}>
        {(opt) => {
          const IconComp = opt.icon;
          const active = () => props.value === opt.id;
          return (
            <button
              type="button"
              onClick={() => props.onChange(opt.id)}
              title={opt.tip ? opt.tip() : undefined}
              class={`hoxi-press inline-flex items-center gap-1.5 rounded-lg px-3.5 py-1.5 text-[13px] font-medium cursor-pointer ${
                active()
                  ? "bg-[var(--bg-panel)] text-[var(--accent-primary)] shadow-2xs"
                  : "text-[var(--text-muted)] hover:text-[var(--text-primary)]"
              }`}
            >
              <Show when={IconComp}>
                <IconComp size={14} />
              </Show>
              <span>{opt.label()}</span>
            </button>
          );
        }}
      </For>
    </div>
  );
}

export function ToggleSwitch(props: {
  checked: boolean;
  onChange: (next: boolean) => void;
}) {
  return (
    <button
      type="button"
      role="switch"
      aria-checked={props.checked}
      onClick={() => props.onChange(!props.checked)}
      class={`relative inline-flex h-6 w-11 shrink-0 cursor-pointer rounded-full border-2 border-transparent transition-colors duration-200 ease-in-out ${
        props.checked ? "bg-[var(--accent-primary)]" : "bg-[var(--border-strong)]"
      }`}
    >
      <span
        class={`pointer-events-none inline-block h-5 w-5 transform rounded-full ${props.checked ? "bg-[var(--accent-foreground)]" : "bg-white"} shadow-xs transition duration-200 ease-in-out ${
          props.checked ? "translate-x-5" : "translate-x-0"
        }`}
      />
    </button>
  );
}

export default function SettingsModal() {
  const isMac =
    typeof navigator !== "undefined" &&
    /Mac|iPhone|iPad/i.test(navigator.platform || navigator.userAgent);

  const isOpen = () => appSettings.modalOpen || workspace.providerModalOpen;

  const activeTab = createMemo<SettingsTab>(() =>
    !appSettings.modalOpen && workspace.providerModalOpen
      ? workspace.providerModalTab
      : appSettings.activeTab
  );

  const isModelTab = () => {
    const tab = activeTab();
    return (
      tab === "providers" ||
      tab === "custom" ||
      tab === "auto" ||
      tab === "models" ||
      tab === "policy"
    );
  };

  const selectTab = (tab: SettingsTab) => {
    if (tab === "custom") {
      openProviderModal("custom", workspace.editingProviderID);
      return;
    }
    openSettingsModal(tab);
  };

  const navGroups: Array<{
    groupLabel: () => string;
    items: Array<{ id: SettingsTab; label: () => string; icon: any; badge?: () => number }>;
  }> = [
    {
      groupLabel: () => t("settings.groupModels"),
      items: [
        {
          id: "providers",
          label: () => t("models.tabCatalog"),
          icon: KeyRound,
          badge: () => workspace.connectedProviders.length,
        },
        { id: "auto", label: () => t("models.tabAutoPools"), icon: Zap },
        { id: "models", label: () => t("models.tabModels"), icon: SlidersHorizontal },
      ],
    },
    {
      groupLabel: () => t("settings.groupAgentSafety"),
      items: [
        { id: "policy", label: () => t("models.tabPolicy"), icon: Bot },
        { id: "terminal", label: () => t("settings.tabTerminal"), icon: TerminalIcon },
        { id: "privacy", label: () => t("privacy.title"), icon: ShieldCheck },
      ],
    },
    {
      groupLabel: () => t("settings.groupWorkspace"),
      items: [
        { id: "general", label: () => t("settings.tabGeneral"), icon: Settings },
        { id: "chat", label: () => t("settings.tabChat"), icon: MessageSquareText },
        { id: "workspace", label: () => t("settings.tabWorkspace"), icon: FolderGit2 },
      ],
    },
    {
      groupLabel: () => t("settings.groupSystem"),
      items: [
        { id: "system", label: () => t("settings.tabSystem"), icon: Cpu },
        { id: "diagnostics", label: () => t("diagnostics.title"), icon: ScrollText },
      ],
    },
  ];

  const activeBreadcrumb = createMemo(() => {
    const cur = activeTab();
    if (cur === "custom") {
      return {
        group: t("settings.groupModels"),
        item: `${t("models.tabCatalog")} / ${t("models.tabCustom")}`,
        icon: KeyRound,
      };
    }
    for (const grp of navGroups) {
      for (const item of grp.items) {
        if (item.id === cur) {
          return { group: grp.groupLabel(), item: item.label(), icon: item.icon };
        }
      }
    }
    return { group: t("settings.modalTitle"), item: t("settings.tabGeneral"), icon: Settings };
  });

  return (
    <Show when={isOpen()}>
      <div class="fixed inset-0 z-40 flex flex-col bg-[var(--bg-canvas)] text-[var(--text-primary)] select-none overflow-hidden hoxi-view-enter">
        {/* Top Window Header (respects macOS traffic light drag area) */}
        <header
          class={`shrink-0 bg-transparent flex items-center justify-between gap-3 px-4 ${
            isMac ? "h-[52px] pl-[96px]" : "h-[44px] pl-4"
          }`}
          style={{ "--wails-draggable": "drag" } as any}
        >
          <div class="flex items-center gap-3 min-w-0">
            <button
              type="button"
              onClick={closeSettingsModal}
              style={{ "--wails-draggable": "no-drag" } as any}
              class="hoxi-press inline-flex items-center gap-2 rounded-xl border border-[var(--border-subtle)] bg-[var(--bg-panel)] hover:bg-[var(--bg-hover)] hover:border-[var(--border-strong)] px-3 py-1.5 text-[12.5px] font-medium text-[var(--text-primary)] shadow-2xs transition-colors cursor-pointer"
            >
              <ArrowLeft size={14} class="text-[var(--text-secondary)]" />
              <span>{t("settings.backToWorkspace")}</span>
              <kbd class="ml-0.5 rounded bg-[var(--bg-inset)] px-1.5 py-0.5 font-mono text-[10px] text-[var(--text-muted)]">
                Esc
              </kbd>
            </button>
          </div>

          <div class="hidden sm:flex items-center gap-2 min-w-0 text-[12.5px]">
            <span class="truncate text-[var(--text-muted)]">
              {activeBreadcrumb().group}
            </span>
            <span class="text-[var(--text-faint)]">/</span>
            <span class="truncate font-semibold text-[var(--text-primary)]">
              {activeBreadcrumb().item}
            </span>
          </div>

          <div class="flex items-center gap-2 shrink-0">
            <span class="hidden sm:inline-block rounded-lg border border-[var(--border-subtle)] bg-[var(--bg-panel)]/70 px-2.5 py-1 font-mono text-[11px] text-[var(--text-faint)]">
              {t("settings.shortcutHint")}
            </span>
            <button
              type="button"
              class="hoxi-press inline-flex items-center justify-center w-7 h-7 rounded-lg text-[var(--text-secondary)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-hover)] cursor-pointer"
              style={{ "--wails-draggable": "no-drag" } as any}
              onClick={toggleTheme}
              title={
                workspace.theme === "dark"
                  ? t("header.themeLightTooltip")
                  : t("header.themeDarkTooltip")
              }
            >
              {workspace.theme === "dark" ? <Sun size={15} /> : <Moon size={15} />}
            </button>
          </div>
        </header>

        {/* Full-Screen Body: Left Grouped Sidebar Island + Right Content Island */}
        <div class="flex-1 flex flex-col sm:flex-row min-h-0 px-2 pb-2 gap-2">
          <nav class="pane-island sm:w-60 shrink-0 p-3 flex sm:flex-col justify-between overflow-x-auto sm:overflow-y-auto bg-[var(--bg-panel)]">
            <div class="flex sm:block gap-2 sm:space-y-4">
              <For each={navGroups}>
                {(group) => (
                  <div class="flex sm:block items-center gap-1 sm:space-y-1">
                    <div class="hidden sm:block px-3 pb-1 text-[10.5px] font-semibold uppercase tracking-wider text-[var(--text-faint)]">
                      {group.groupLabel()}
                    </div>
                    <For each={group.items}>
                      {(item) => {
                        const IconComp = item.icon;
                        const isActive = () =>
                          activeTab() === item.id ||
                          (item.id === "providers" && activeTab() === "custom");
                        const badgeVal = () => (item.badge ? item.badge() : 0);
                        return (
                          <button
                            type="button"
                            onClick={() => selectTab(item.id)}
                            class={`hoxi-press flex w-full items-center justify-between gap-2 rounded-xl px-3 py-2 text-left text-[13px] font-medium cursor-pointer ${
                              isActive()
                                ? "bg-[var(--accent-surface)] text-[var(--accent-primary)]"
                                : "text-[var(--text-secondary)] hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)]"
                            }`}
                          >
                            <span class="inline-flex items-center gap-2.5 min-w-0">
                              <IconComp
                                size={15}
                                class={`shrink-0 ${
                                  item.id === "auto" && !isActive() ? "text-[var(--ok)]" : ""
                                }`}
                              />
                              <span class="whitespace-nowrap sm:truncate">{item.label()}</span>
                            </span>
                            <Show when={badgeVal() > 0}>
                              <span class="shrink-0 rounded-full bg-[var(--bg-inset)] px-2 py-0.5 font-mono text-[10.5px] text-[var(--text-secondary)]">
                                {badgeVal()}
                              </span>
                            </Show>
                          </button>
                        );
                      }}
                    </For>
                  </div>
                )}
              </For>
            </div>
          </nav>

          {/* Right Content Island */}
          <div class="pane-island flex-1 min-w-0 h-full flex flex-col overflow-hidden bg-[var(--bg-panel)]">
            <Show when={isModelTab()}>
              <Show when={activeTab() === "providers"}>
                <div class="flex-1 min-h-0 flex flex-col hoxi-view-enter">
                  <ProviderCatalogTab />
                </div>
              </Show>
              <Show when={activeTab() === "custom"}>
                <div class="flex-1 min-h-0 flex flex-col hoxi-view-enter">
                  <CustomEndpointTab />
                </div>
              </Show>
              <Show when={activeTab() === "auto"}>
                <div class="flex-1 min-h-0 flex flex-col hoxi-view-enter">
                  <AutoModelPoolTab />
                </div>
              </Show>
              <Show when={activeTab() === "models"}>
                <div class="flex-1 min-h-0 flex flex-col hoxi-view-enter">
                  <ModelsRoleTab />
                </div>
              </Show>
              <Show when={activeTab() === "policy"}>
                <div class="flex-1 min-h-0 flex flex-col hoxi-view-enter">
                  <AgentPolicyMcpTab />
                </div>
              </Show>
            </Show>

            <Show when={!isModelTab()}>
              <div class="min-w-0 flex-1 overflow-y-auto p-6 sm:p-8">
                <div class="mx-auto w-full max-w-4xl">
                  <Show when={activeTab() === "general"}>
                    <div class="hoxi-view-enter">
                      <GeneralAppearanceTab />
                    </div>
                  </Show>
                  <Show when={activeTab() === "chat"}>
                    <div class="hoxi-view-enter">
                      <ChatComposerTab />
                    </div>
                  </Show>
                  <Show when={activeTab() === "workspace"}>
                    <div class="hoxi-view-enter">
                      <WorkspaceGitSettingsTab />
                    </div>
                  </Show>
                  <Show when={activeTab() === "terminal"}>
                    <div class="hoxi-view-enter">
                      <TerminalSafetySettingsTab />
                    </div>
                  </Show>
                  <Show when={activeTab() === "system"}>
                    <div class="hoxi-view-enter">
                      <SystemRuntimeSettingsTab />
                    </div>
                  </Show>
                  <Show when={activeTab() === "privacy"}>
                    <div class="hoxi-view-enter">
                      <PrivacySettingsTab />
                    </div>
                  </Show>
                  <Show when={activeTab() === "diagnostics"}>
                    <div class="hoxi-view-enter">
                      <DiagnosticsTab />
                    </div>
                  </Show>
                </div>
              </div>
            </Show>
          </div>
        </div>
      </div>
    </Show>
  );
}
