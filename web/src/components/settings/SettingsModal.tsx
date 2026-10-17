// web/src/components/settings/SettingsModal.tsx
import { For, onCleanup, onMount, Show } from "solid-js";
import {
  Cpu,
  FolderGit2,
  KeyRound,
  MessageSquareText,
  Settings,
  SlidersHorizontal,
  Terminal as TerminalIcon,
  X,
} from "lucide-solid";
import {
  appSettings,
  closeSettingsModal,
  openProviderModal,
  openSettingsModal,
  setAppSettings,
  workspace,
  type SettingsTab,
} from "../../store/workspace";
import { t } from "../../i18n";
import { ChatComposerTab, GeneralAppearanceTab } from "./SettingsGeneralChatTabs";
import { SystemRuntimeSettingsTab } from "./SettingsSystemRuntimeTab";
import {
  TerminalSafetySettingsTab,
  WorkspaceGitSettingsTab,
} from "./SettingsWorkspaceTerminalTabs";

export default function SettingsModal() {
  onMount(() => {
    const onKeyDown = (ev: KeyboardEvent) => {
      if ((ev.metaKey || ev.ctrlKey) && ev.key === ",") {
        ev.preventDefault();
        if (appSettings.modalOpen) {
          closeSettingsModal();
        } else {
          openSettingsModal("general");
        }
        return;
      }
      if (ev.key === "Escape" && appSettings.modalOpen) {
        ev.preventDefault();
        closeSettingsModal();
      }
    };
    window.addEventListener("keydown", onKeyDown);
    onCleanup(() => window.removeEventListener("keydown", onKeyDown));
  });

  const navItems: Array<{ id: SettingsTab; label: () => string; icon: any }> = [
    { id: "general", label: () => t("settings.tabGeneral"), icon: SlidersHorizontal },
    { id: "chat", label: () => t("settings.tabChat"), icon: MessageSquareText },
    { id: "workspace", label: () => t("settings.tabWorkspace"), icon: FolderGit2 },
    { id: "terminal", label: () => t("settings.tabTerminal"), icon: TerminalIcon },
    { id: "system", label: () => t("settings.tabSystem"), icon: Cpu },
  ];

  return (
    <Show when={appSettings.modalOpen}>
      <div
        class="fixed inset-0 z-50 flex items-center justify-center bg-black/50 backdrop-blur-xs p-4 select-none"
        onClick={closeSettingsModal}
      >
        <div
          class="flex h-[640px] w-full max-w-[880px] flex-col overflow-hidden rounded-2xl border border-[var(--border-strong)] bg-[var(--bg-panel)] shadow-2xl"
          onClick={(e) => e.stopPropagation()}
        >
          {/* Header */}
          <div class="flex items-center justify-between border-b border-[var(--border-subtle)] px-5 py-3.5">
            <div class="flex items-center gap-3 min-w-0">
              <div class="flex h-8 w-8 shrink-0 items-center justify-center rounded-xl bg-[var(--accent-surface)] text-[var(--accent-primary)]">
                <Settings size={16} />
              </div>
              <div class="min-w-0">
                <h2 class="truncate text-[14px] font-semibold text-[var(--text-primary)]">
                  {t("settings.modalTitle")}
                </h2>
                <p class="truncate text-[11.5px] text-[var(--text-muted)]">
                  {t("settings.modalSubtitle")}
                </p>
              </div>
            </div>

            <div class="flex items-center gap-2 shrink-0">
              <span class="hidden sm:inline-block rounded-lg border border-[var(--border-subtle)] bg-[var(--bg-inset)] px-2 py-0.5 font-mono text-[10.5px] text-[var(--text-faint)]">
                {t("settings.shortcutHint")}
              </span>
              <button
                type="button"
                onClick={closeSettingsModal}
                class="rounded-lg p-1.5 text-[var(--text-muted)] hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)] transition-colors cursor-pointer"
              >
                <X size={16} />
              </button>
            </div>
          </div>

          {/* Body: Left Category Sidebar + Right Active Pane */}
          <div class="flex flex-1 min-h-0">
            <nav class="w-52 shrink-0 border-r border-[var(--border-subtle)] bg-[var(--bg-inset)]/40 p-3 flex flex-col justify-between">
              <div class="space-y-1">
                <For each={navItems}>
                  {(item) => {
                    const IconComp = item.icon;
                    const isActive = () => appSettings.activeTab === item.id;
                    return (
                      <button
                        type="button"
                        onClick={() => setAppSettings("activeTab", item.id)}
                        class={`flex w-full items-center gap-2.5 rounded-xl px-3 py-2 text-left text-[12.5px] font-medium transition-colors cursor-pointer ${
                          isActive()
                            ? "bg-[var(--accent-surface)] text-[var(--accent-primary)]"
                            : "text-[var(--text-secondary)] hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)]"
                        }`}
                      >
                        <IconComp size={14} class="shrink-0" />
                        <span class="truncate">{item.label()}</span>
                      </button>
                    );
                  }}
                </For>
              </div>

              {/* Bottom shortcut to Models & Providers Modal */}
              <div class="border-t border-[var(--border-subtle)] pt-2.5">
                <button
                  type="button"
                  onClick={() => {
                    closeSettingsModal();
                    openProviderModal("providers");
                  }}
                  class="flex w-full items-center justify-between gap-2 rounded-xl border border-[var(--border-subtle)] bg-[var(--bg-panel)] hover:bg-[var(--bg-hover)] px-3 py-2 text-left text-[12px] font-medium text-[var(--text-primary)] transition-colors cursor-pointer shadow-2xs"
                >
                  <span class="inline-flex items-center gap-2 truncate">
                    <KeyRound size={13} class="text-[var(--accent-primary)] shrink-0" />
                    <span class="truncate">{t("settings.manageModelsAndKeysBtn")}</span>
                  </span>
                  <Show when={workspace.connectedProviders.length > 0}>
                    <span class="rounded-full bg-[var(--accent-surface)] px-1.5 py-0.5 font-mono text-[10px] text-[var(--accent-text)]">
                      {workspace.connectedProviders.length}
                    </span>
                  </Show>
                </button>
              </div>
            </nav>

            <div class="flex-1 overflow-y-auto p-6">
              <Show when={appSettings.activeTab === "general"}>
                <GeneralAppearanceTab />
              </Show>
              <Show when={appSettings.activeTab === "chat"}>
                <ChatComposerTab />
              </Show>
              <Show when={appSettings.activeTab === "workspace"}>
                <WorkspaceGitSettingsTab />
              </Show>
              <Show when={appSettings.activeTab === "terminal"}>
                <TerminalSafetySettingsTab />
              </Show>
              <Show when={appSettings.activeTab === "system"}>
                <SystemRuntimeSettingsTab />
              </Show>
            </div>
          </div>
        </div>
      </div>
    </Show>
  );
}
