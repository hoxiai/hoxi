// web/src/components/settings/SettingsModal.tsx
import { For, type JSX, Show } from "solid-js";
import {
  CircleHelp,
  ScrollText,
  Cpu,
  FolderGit2,
  KeyRound,
  MessageSquareText,
  Settings,
  ShieldCheck,
  SlidersHorizontal,
  Terminal as TerminalIcon,
  X,
} from "lucide-solid";
import {
  appSettings,
  closeSettingsModal,
  openProviderModal,
  setAppSettings,
  workspace,
  type SettingsTab,
} from "../../store/workspace";
import { t } from "../../i18n";
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
              class={`inline-flex items-center gap-1.5 rounded-lg px-3.5 py-1.5 text-[13px] font-medium transition-all cursor-pointer ${
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
  const navItems: Array<{ id: SettingsTab; label: () => string; icon: any }> = [
    { id: "general", label: () => t("settings.tabGeneral"), icon: SlidersHorizontal },
    { id: "chat", label: () => t("settings.tabChat"), icon: MessageSquareText },
    { id: "workspace", label: () => t("settings.tabWorkspace"), icon: FolderGit2 },
    { id: "terminal", label: () => t("settings.tabTerminal"), icon: TerminalIcon },
    { id: "system", label: () => t("settings.tabSystem"), icon: Cpu },
    { id: "privacy", label: () => t("privacy.title"), icon: ShieldCheck },
    { id: "diagnostics", label: () => t("diagnostics.title"), icon: ScrollText },
  ];

  return (
    <Show when={appSettings.modalOpen}>
      <div
        class="fixed inset-0 z-50 flex items-center justify-center bg-black/50 backdrop-blur-xs p-4 select-none"
        onClick={closeSettingsModal}
      >
        <div
          class="flex h-[620px] max-h-[90dvh] w-full max-w-[860px] flex-col overflow-hidden rounded-2xl border border-[var(--border-strong)] bg-[var(--bg-panel)] shadow-2xl"
          onClick={(e) => e.stopPropagation()}
        >
          {/* Header */}
          <div class="flex items-center justify-between border-b border-[var(--border-subtle)] px-6 py-4">
            <div class="flex items-center gap-3 min-w-0">
              <div class="flex h-8 w-8 shrink-0 items-center justify-center rounded-xl bg-[var(--accent-surface)] text-[var(--accent-primary)]">
                <Settings size={17} />
              </div>
              <h2 class="truncate text-[15px] font-semibold text-[var(--text-primary)]">
                {t("settings.modalTitle")}
              </h2>
              <HelpTip text={t("settings.modalSubtitle")} />
            </div>

            <div class="flex items-center gap-2.5 shrink-0">
              <span class="hidden sm:inline-block rounded-lg border border-[var(--border-subtle)] bg-[var(--bg-inset)] px-2.5 py-1 font-mono text-[11.5px] text-[var(--text-faint)]">
                {t("settings.shortcutHint")}
              </span>
              <button
                type="button"
                onClick={closeSettingsModal}
                class="rounded-lg p-1.5 text-[var(--text-muted)] hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)] transition-colors cursor-pointer"
              >
                <X size={17} />
              </button>
            </div>
          </div>

          {/* Body: Left Category Sidebar + Right Active Pane */}
          <div class="flex flex-col sm:flex-row flex-1 min-h-0">
            <nav class="sm:w-52 shrink-0 border-b sm:border-b-0 sm:border-r border-[var(--border-subtle)] bg-[var(--bg-inset)]/35 p-3.5 flex flex-col justify-between">
              <div class="flex overflow-x-auto sm:block sm:space-y-1">
                <For each={navItems}>
                  {(item) => {
                    const IconComp = item.icon;
                    const isActive = () => appSettings.activeTab === item.id;
                    return (
                      <button
                        type="button"
                        onClick={() => setAppSettings("activeTab", item.id)}
                        class={`flex w-full items-center gap-2.5 rounded-xl px-3.5 py-2.5 text-left text-[13.5px] font-medium transition-colors cursor-pointer ${
                          isActive()
                            ? "bg-[var(--accent-surface)] text-[var(--accent-primary)]"
                            : "text-[var(--text-secondary)] hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)]"
                        }`}
                      >
                        <IconComp size={16} class="shrink-0" />
                        <span class="whitespace-nowrap sm:truncate">{item.label()}</span>
                      </button>
                    );
                  }}
                </For>
              </div>

              {/* Bottom shortcut to Models & Providers Modal */}
              <div class="hidden sm:block border-t border-[var(--border-subtle)] pt-3">
                <button
                  type="button"
                  onClick={() => {
                    closeSettingsModal();
                    openProviderModal("providers");
                  }}
                  class="flex w-full items-center justify-between gap-2 rounded-xl border border-[var(--border-subtle)] bg-[var(--bg-panel)] hover:bg-[var(--bg-hover)] px-3.5 py-2.5 text-left text-[13px] font-medium text-[var(--text-primary)] transition-colors cursor-pointer shadow-2xs"
                >
                  <span class="inline-flex items-center gap-2 truncate">
                    <KeyRound size={14} class="text-[var(--accent-primary)] shrink-0" />
                    <span class="truncate">{t("settings.manageModelsAndKeysBtn")}</span>
                  </span>
                  <Show when={workspace.connectedProviders.length > 0}>
                    <span class="rounded-full bg-[var(--accent-surface)] px-2 py-0.5 font-mono text-[11px] text-[var(--accent-text)]">
                      {workspace.connectedProviders.length}
                    </span>
                  </Show>
                </button>
              </div>
            </nav>

            <div class="min-w-0 flex-1 overflow-y-auto p-4 sm:p-7">
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
              <Show when={appSettings.activeTab === "privacy"}>
                <PrivacySettingsTab />
              </Show>
              <Show when={appSettings.activeTab === "diagnostics"}>
                <DiagnosticsTab />
              </Show>
            </div>
          </div>
        </div>
      </div>
    </Show>
  );
}
