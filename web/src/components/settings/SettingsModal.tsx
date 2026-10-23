// web/src/components/settings/SettingsModal.tsx
import { createMemo, createSignal, For, Show } from "solid-js";
import { Presence } from "../motion/Presence";
import {
  ArrowLeft,
  BarChart3,
  Blocks,
  Bot,
  Cpu,
  FolderGit2,
  Globe,
  KeyRound,
  MessageSquareText,
  Moon,
  Search,
  Settings,
  ShieldCheck,
  SlidersHorizontal,
  Smartphone,
  Sun,
  X,
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
import { PrivacySettingsTab } from "./PrivacyControls";
import { SettingsExtensionsTab } from "./SettingsExtensionsTab";
import {
  AgentsSettingsTab,
  RemoteSettingsTab,
  SystemSettingsTab,
  UsageSettingsTab,
} from "./SettingsMergedTabs";
import { WorkspaceGitSettingsTab } from "./SettingsWorkspaceTerminalTabs";

export default function SettingsModal() {
  const [searchQuery, setSearchQuery] = createSignal("");

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
      tab === "agents" ||
      tab === "subagents" ||
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
    items: Array<{
      id: SettingsTab;
      label: () => string;
      icon: any;
      badge?: () => number;
      keywords?: () => string[];
    }>;
  }> = [
    {
      groupLabel: () => t("settings.groupPreferencesWorkspace"),
      items: [
        {
          id: "general",
          label: () => t("settings.tabGeneral"),
          icon: Settings,
          keywords: () => [
            t("settings.sectionThemeTitle"),
            t("settings.sectionTerminalTitle"),
            t("settings.sectionSidebarHistoryTitle"),
          ],
        },
        {
          id: "chat",
          label: () => t("settings.tabChat"),
          icon: MessageSquareText,
          keywords: () => [
            t("settings.sectionSendShortcutTitle"),
            t("settings.sectionNotificationsTitle"),
          ],
        },
        {
          id: "workspace",
          label: () => t("settings.tabWorkspace"),
          icon: FolderGit2,
          keywords: () => [
            t("settings.sectionSandboxTitle"),
            t("settings.sectionDiffStyleTitle"),
            t("settings.sectionCodeAuditTitle"),
            "Worktree",
            "Diff",
          ],
        },
      ],
    },
    {
      groupLabel: () => t("settings.groupModels"),
      items: [
        {
          id: "providers",
          label: () => t("models.tabCatalog"),
          icon: KeyRound,
          badge: () => workspace.connectedProviders.length,
          keywords: () => [t("models.tabCustom"), "API Key", "Provider"],
        },
        {
          id: "models",
          label: () => t("models.tabModels"),
          icon: SlidersHorizontal,
          keywords: () => [t("models.selectModel")],
        },
        {
          id: "auto",
          label: () => t("models.tabAutoPools"),
          icon: Zap,
          keywords: () => ["hoxi-auto", t("models.tabAutoPools")],
        },
      ],
    },
    {
      groupLabel: () => t("settings.groupAgentsExtensions"),
      items: [
        {
          id: "agents",
          label: () => t("models.tabAgents"),
          icon: Bot,
          badge: () => workspace.agents.length,
          keywords: () => [
            t("settings.sectionCustomInstructionsTitle"),
            t("models.customAgentsTitle"),
            t("models.tabSubagents"),
            t("models.subagentsTitle"),
          ],
        },
        {
          id: "extensions",
          label: () => t("settings.tabExtensions"),
          icon: Blocks,
          keywords: () => [
            "Skills",
            "MCP",
            t("models.mcpTitle"),
            t("settings.extensionsTitle"),
          ],
        },
      ],
    },
    {
      groupLabel: () => t("settings.groupSafetyPrivacy"),
      items: [
        {
          id: "policy",
          label: () => t("models.tabPolicy"),
          icon: ShieldCheck,
          keywords: () => [
            t("approval.modeAutoLabel"),
            t("models.policyTitle"),
            t("settings.sectionSafetyPolicyTitle"),
            t("settings.sectionAgyHookTitle"),
          ],
        },
        {
          id: "privacy",
          label: () => t("privacy.title"),
          icon: Globe,
          keywords: () => [t("privacy.telemetryTitle")],
        },
      ],
    },
    {
      groupLabel: () => t("settings.groupSystemDevices"),
      items: [
        {
          id: "system",
          label: () => t("settings.tabSystem"),
          icon: Cpu,
          keywords: () => [
            t("settings.sectionUpdaterTitle"),
            t("settings.sectionRuntimeTitle"),
            t("settings.sectionMaintenanceTitle"),
            t("diagnostics.errorLogTitle"),
            "OpenCode",
          ],
        },
        {
          id: "usage",
          label: () => t("settings.tabUsage"),
          icon: BarChart3,
          keywords: () => [
            t("settings.usageTitle"),
            t("settings.usageTotalTokens"),
            t("settings.usageTotalCost"),
            t("diagnostics.title"),
            t("diagnostics.localHint"),
          ],
        },
        {
          id: "remote",
          label: () => t("settings.tabRemote"),
          icon: Smartphone,
          keywords: () => [
            t("remoteTunnel.title"),
            "GoPanel",
            t("relay.title"),
            "OpenAI",
            "127.0.0.1",
          ],
        },
      ],
    },
  ];

  const CHILD_PARENT_TAB: Partial<Record<SettingsTab, SettingsTab>> = {
    subagents: "agents",
    diagnostics: "usage",
    errorLog: "system",
    relay: "remote",
  };

  const CHILD_LABEL: Partial<Record<SettingsTab, () => string>> = {
    subagents: () => t("models.tabSubagents"),
    diagnostics: () => t("diagnostics.title"),
    errorLog: () => t("diagnostics.errorLogTitle"),
    relay: () => t("relay.title"),
  };

  const filteredNavGroups = createMemo(() => {
    const q = searchQuery().trim().toLowerCase();
    if (!q) return navGroups;
    return navGroups
      .map((grp) => {
        const groupMatch = grp.groupLabel().toLowerCase().includes(q);
        const items = grp.items.filter((item) => {
          if (groupMatch) return true;
          if (item.label().toLowerCase().includes(q)) return true;
          if (item.id.toLowerCase().includes(q)) return true;
          const kws = item.keywords ? item.keywords() : [];
          return kws.some((kw) => kw.toLowerCase().includes(q));
        });
        return { ...grp, items };
      })
      .filter((grp) => grp.items.length > 0);
  });

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
        if (CHILD_PARENT_TAB[cur] === item.id) {
          return {
            group: grp.groupLabel(),
            item: CHILD_LABEL[cur] ? CHILD_LABEL[cur]!() : item.label(),
            icon: item.icon,
          };
        }
      }
    }
    return { group: t("settings.modalTitle"), item: t("settings.tabGeneral"), icon: Settings };
  });

  return (
    <Presence when={isOpen()}>
      {(state) => (
      <div
        data-state={state()}
        class="hoxi-screen fixed inset-0 z-40 flex flex-col bg-[var(--bg-canvas)] text-[var(--text-primary)] select-none overflow-hidden"
      >
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
              class="hoxi-press inline-flex items-center gap-2 rounded-xl border border-[var(--border-subtle)] bg-[var(--bg-panel)] hover:bg-[var(--bg-hover)] hover:border-[var(--border-strong)] px-3 py-1.5 text-[13px] font-medium text-[var(--text-primary)] shadow-2xs transition-colors cursor-pointer"
            >
              <ArrowLeft size={14} class="text-[var(--text-secondary)]" />
              <span>{t("settings.backToWorkspace")}</span>
              <kbd class="ml-0.5 rounded bg-[var(--bg-inset)] px-1.5 py-0.5 font-mono text-[11px] text-[var(--text-muted)]">
                Esc
              </kbd>
            </button>
          </div>

          <div class="hidden sm:flex items-center gap-2 min-w-0 text-[13px]">
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
          <nav class="pane-island sm:w-64 shrink-0 p-3 flex sm:flex-col justify-between overflow-x-auto sm:overflow-y-auto bg-[var(--bg-panel)]">
            <div class="flex sm:block gap-2 sm:space-y-4 w-full">
              {/* Settings Search Filter */}
              <div class="hidden sm:block pb-1">
                <div class="relative">
                  <Search
                    size={13}
                    class="pointer-events-none absolute left-2.5 top-1/2 -translate-y-1/2 text-[var(--text-faint)]"
                  />
                  <input
                    type="text"
                    value={searchQuery()}
                    onInput={(e) => setSearchQuery(e.currentTarget.value)}
                    placeholder={t("settings.searchSettingsPlaceholder")}
                    class="w-full rounded-xl border border-[var(--border-subtle)] bg-[var(--bg-inset)] pl-8 pr-7 py-1.5 text-[12px] text-[var(--text-primary)] placeholder:text-[var(--text-faint)] outline-none focus:border-[var(--accent-primary)] transition-colors"
                  />
                  <Show when={searchQuery().trim().length > 0}>
                    <button
                      type="button"
                      onClick={() => setSearchQuery("")}
                      class="absolute right-2 top-1/2 -translate-y-1/2 rounded p-0.5 text-[var(--text-faint)] hover:text-[var(--text-primary)] cursor-pointer"
                    >
                      <X size={12} />
                    </button>
                  </Show>
                </div>
              </div>

              <Show
                when={filteredNavGroups().length > 0}
                fallback={
                  <div class="hidden sm:block px-3 py-6 text-center text-[12px] text-[var(--text-muted)]">
                    {t("settings.searchNoResults")}
                  </div>
                }
              >
                <For each={filteredNavGroups()}>
                  {(group) => (
                    <div class="flex sm:block items-center gap-1 sm:space-y-1">
                      <div class="hidden sm:block px-3 pb-1 text-[11px] font-semibold uppercase tracking-wider text-[var(--text-faint)]">
                        {group.groupLabel()}
                      </div>
                      <For each={group.items}>
                        {(item) => {
                          const IconComp = item.icon;
                          const isActive = () =>
                            (CHILD_PARENT_TAB[activeTab()] ?? activeTab()) === item.id ||
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
                                <span class="shrink-0 rounded-full bg-[var(--bg-inset)] px-2 py-0.5 font-mono text-[11px] text-[var(--text-secondary)]">
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
              </Show>
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
              <Show when={activeTab() === "agents" || activeTab() === "subagents"}>
                <div class="flex-1 min-h-0 flex flex-col hoxi-view-enter">
                  <AgentsSettingsTab />
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
                <div class={`mx-auto w-full ${activeTab() === "diagnostics" || activeTab() === "errorLog" ? "max-w-7xl" : "max-w-4xl"}`}>
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
                  <Show when={activeTab() === "extensions"}>
                    <div class="hoxi-view-enter">
                      <SettingsExtensionsTab />
                    </div>
                  </Show>
                  <Show when={activeTab() === "workspace"}>
                    <div class="hoxi-view-enter">
                      <WorkspaceGitSettingsTab />
                    </div>
                  </Show>
                  <Show when={activeTab() === "system" || activeTab() === "errorLog"}>
                    <div class="hoxi-view-enter">
                      <SystemSettingsTab />
                    </div>
                  </Show>
                  <Show when={activeTab() === "privacy"}>
                    <div class="hoxi-view-enter">
                      <PrivacySettingsTab />
                    </div>
                  </Show>
                  <Show when={activeTab() === "usage" || activeTab() === "diagnostics"}>
                    <div class="hoxi-view-enter">
                      <UsageSettingsTab />
                    </div>
                  </Show>
                  <Show when={activeTab() === "remote" || activeTab() === "relay"}>
                    <div class="hoxi-view-enter">
                      <RemoteSettingsTab />
                    </div>
                  </Show>
                </div>
              </div>
            </Show>
          </div>
        </div>
      </div>
      )}
    </Presence>
  );
}
