// web/src/components/settings/SettingsMergedTabs.tsx
// Parent pages that fold a thin sibling tab behind a segmented pill switcher so
// the settings sidebar stays short: Agents (+Subagents), Usage (+Request log),
// System (+Error log) and Remote (+Local relay). Deep links still open the child
// tab id directly; the switcher reflects it via the active settings tab.
import { Show } from "solid-js";
import {
  BarChart3,
  Bot,
  Cpu,
  RadioTower,
  ScrollText,
  ShieldCheck,
  Smartphone,
  Users,
} from "lucide-solid";
import { t } from "../../i18n";
import { appSettings, openSettingsModal } from "../../store/workspace";
import AgentRolesTab from "../models/AgentRolesTab";
import SubagentSettingsTab from "../models/SubagentSettingsTab";
import DiagnosticsTab from "./DiagnosticsTab";
import ErrorLogTab from "./ErrorLogTab";
import { LocalRelaySettingsTab } from "./LocalRelayControls";
import { PairingQrPanel } from "./PairingQrPanel";
import { RemoteAccessTabs } from "./RemoteAccessTabs";
import { SegmentedPills } from "./setting-controls";
import { SystemRuntimeSettingsTab } from "./SettingsSystemRuntimeTab";
import UsageHistoryTab from "./UsageHistoryTab";

export function AgentsSettingsTab() {
  return (
    <div class="flex-1 min-h-0 flex flex-col">
      <div class="shrink-0 px-6 sm:px-8 pt-6 sm:pt-8">
        <div class="mx-auto w-full max-w-4xl">
          <SegmentedPills
            value={appSettings.activeTab as "agents" | "subagents"}
            onChange={(tab) => openSettingsModal(tab)}
            options={[
              { id: "agents", label: () => t("models.tabAgents"), icon: Bot },
              { id: "subagents", label: () => t("models.tabSubagents"), icon: Users },
            ]}
          />
        </div>
      </div>
      <Show when={appSettings.activeTab === "subagents"} fallback={<AgentRolesTab />}>
        <SubagentSettingsTab />
      </Show>
    </div>
  );
}

export function UsageSettingsTab() {
  return (
    <div class="space-y-5">
      <SegmentedPills
        value={appSettings.activeTab as "usage" | "diagnostics"}
        onChange={(tab) => openSettingsModal(tab)}
        options={[
          { id: "usage", label: () => t("settings.tabUsage"), icon: BarChart3 },
          { id: "diagnostics", label: () => t("diagnostics.title"), icon: ScrollText },
        ]}
      />
      <Show when={appSettings.activeTab === "diagnostics"} fallback={<UsageHistoryTab />}>
        <DiagnosticsTab />
      </Show>
    </div>
  );
}

export function SystemSettingsTab() {
  return (
    <div class="space-y-6">
      <SegmentedPills
        value={appSettings.activeTab as "system" | "errorLog"}
        onChange={(tab) => openSettingsModal(tab)}
        options={[
          { id: "system", label: () => t("settings.tabSystem"), icon: Cpu },
          { id: "errorLog", label: () => t("diagnostics.errorLogTitle"), icon: ShieldCheck },
        ]}
      />
      <Show when={appSettings.activeTab === "errorLog"} fallback={<SystemRuntimeSettingsTab />}>
        <ErrorLogTab />
      </Show>
    </div>
  );
}

export function RemoteSettingsTab() {
  return (
    <div class="space-y-6">
      <SegmentedPills
        value={appSettings.activeTab as "remote" | "relay"}
        onChange={(tab) => openSettingsModal(tab)}
        options={[
          { id: "remote", label: () => t("settings.tabRemote"), icon: Smartphone },
          { id: "relay", label: () => t("relay.title"), icon: RadioTower },
        ]}
      />
      <Show
        when={appSettings.activeTab === "relay"}
        fallback={
          <>
            <PairingQrPanel />
            <RemoteAccessTabs />
          </>
        }
      >
        <LocalRelaySettingsTab />
      </Show>
    </div>
  );
}
