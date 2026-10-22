// web/src/components/settings/SettingsWorkspaceTerminalTabs.tsx
import { onMount, Show } from "solid-js";
import {
  Columns2,
  FolderGit2,
  GitMerge,
  LayoutList,
  Signature,
  SlidersHorizontal,
} from "lucide-solid";
import {
  appSettings,
  gitAttribution,
  loadGitAttribution,
  openProviderModal,
  saveGitAttribution,
  setObserverAutoAudit,
  setSessionSandboxMode,
  setSidebarProjectMode,
  updateAppSetting,
  workspace,
  type DiffLayoutMode,
  type SidebarProjectMode,
  type WorktreeApplyStrategy,
} from "../../store/workspace";
import { t } from "../../i18n";
import {
  SegmentedPills,
  SettingGroup,
  SettingRow,
  ToggleSwitch,
} from "./setting-controls";

export function WorkspaceGitSettingsTab() {
  onMount(() => {
    void loadGitAttribution();
  });
  return (
    <div class="space-y-5">
      {/* 1. Workspace, Sandbox & Diffs Group */}
      <SettingGroup>
        <SettingRow
          icon={FolderGit2}
          label={t("settings.sectionSandboxTitle")}
          tip={t("settings.sectionSandboxDesc")}
        >
          <SegmentedPills<"local" | "worktree">
            value={appSettings.defaultSandboxMode}
            options={[
              {
                id: "local",
                label: () => t("settings.sandboxLocalTitle"),
                tip: () => t("settings.sandboxLocalDesc"),
              },
              {
                id: "worktree",
                label: () => t("settings.sandboxWorktreeTitle"),
                tip: () => t("settings.sandboxWorktreeDesc"),
              },
            ]}
            onChange={(val) => setSessionSandboxMode(val)}
          />
        </SettingRow>

        <SettingRow
          icon={LayoutList}
          label={t("settings.sectionSidebarModeTitle")}
          tip={t("settings.sectionSidebarModeDesc")}
        >
          <SegmentedPills<SidebarProjectMode>
            value={workspace.sidebarProjectMode}
            options={[
              {
                id: "multi",
                label: () => t("leftNav.multiProject"),
                tip: () => t("settings.sidebarMultiDesc"),
              },
              {
                id: "attention",
                label: () => t("leftNav.attentionTasks"),
                tip: () => t("settings.sidebarAttentionDesc"),
              },
              {
                id: "running",
                label: () => t("leftNav.runningTasks"),
                tip: () => t("settings.sidebarRunningDesc"),
              },
              {
                id: "single",
                label: () => t("leftNav.singleProject"),
                tip: () => t("settings.sidebarSingleDesc"),
              },
              {
                id: "recent",
                label: () => t("leftNav.recentTasks"),
                tip: () => t("leftNav.recentTasks"),
              },
            ]}
            onChange={(val) => setSidebarProjectMode(val)}
          />
        </SettingRow>

        <SettingRow
          icon={Columns2}
          label={t("settings.sectionDiffStyleTitle")}
          tip={t("settings.sectionDiffStyleDesc")}
        >
          <SegmentedPills<DiffLayoutMode>
            value={appSettings.defaultDiffStyle}
            options={[
              {
                id: "unified",
                label: () => t("settings.diffUnifiedTitle"),
                tip: () => t("settings.diffUnifiedDesc"),
              },
              {
                id: "split",
                label: () => t("settings.diffSplitTitle"),
                tip: () => t("settings.diffSplitDesc"),
              },
            ]}
            onChange={(val) => updateAppSetting("defaultDiffStyle", val)}
          />
        </SettingRow>

        <SettingRow
          icon={GitMerge}
          label={t("settings.sectionApplyModeTitle")}
          tip={t("settings.sectionApplyModeDesc")}
        >
          <SegmentedPills<WorktreeApplyStrategy>
            value={appSettings.worktreeApplyMode}
            options={[
              {
                id: "patch",
                label: () => t("settings.applyPatchTitle"),
                tip: () => t("settings.applyPatchDesc"),
              },
              {
                id: "merge",
                label: () => t("settings.applyMergeTitle"),
                tip: () => t("settings.applyMergeDesc"),
              },
            ]}
            onChange={(val) => updateAppSetting("worktreeApplyMode", val)}
          />
        </SettingRow>

        <SettingRow
          icon={Signature}
          label={t("settings.gitAttributionTitle")}
          tip={t("settings.gitAttributionDesc")}
        >
          <ToggleSwitch
            checked={gitAttribution.enabled}
            onChange={(val) => void saveGitAttribution(val)}
          />
        </SettingRow>
      </SettingGroup>
      <Show when={gitAttribution.error}>
        <p role="alert" class="px-1 text-[13px] text-red-500">{gitAttribution.error}</p>
      </Show>

      {/* 2. Observer AI Code Audit */}
      <SettingGroup
        title={t("settings.sectionCodeAuditTitle")}
        tip={t("settings.sectionCodeAuditDesc")}
        action={
          <button
            type="button"
            onClick={() => {
              openProviderModal("models");
            }}
            class="inline-flex items-center gap-1.5 rounded-xl border border-[var(--border-subtle)] bg-[var(--bg-panel)] hover:bg-[var(--bg-hover)] px-3 py-1 text-[13px] font-medium text-[var(--text-secondary)] hover:text-[var(--text-primary)] transition-colors cursor-pointer"
          >
            <SlidersHorizontal size={13} />
            {t("settings.configureObserverBtn")}
          </button>
        }
      >
        <SettingRow
          label={t("settings.autoAuditLabel")}
          tip={t("settings.autoAuditDesc")}
        >
          <ToggleSwitch
            checked={workspace.observerAutoAudit}
            onChange={(val) => setObserverAutoAudit(val)}
          />
        </SettingRow>
      </SettingGroup>
    </div>
  );
}
