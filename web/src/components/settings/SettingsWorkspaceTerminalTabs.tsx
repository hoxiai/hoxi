// web/src/components/settings/SettingsWorkspaceTerminalTabs.tsx
import { createSignal, For, onMount, Show } from "solid-js";
import {
  Columns2,
  FolderGit2,
  GitMerge,
  LayoutList,
  ShieldCheck,
  SlidersHorizontal,
  Terminal as TerminalIcon,
} from "lucide-solid";
import {
  appSettings,
  closeSettingsModal,
  fetchGlobalConfig,
  openProviderModal,
  setObserverAutoAudit,
  setSidebarProjectMode,
  switchActiveSessionSandboxMode,
  updateAppSetting,
  updateGlobalPermissions,
  workspace,
  type DiffLayoutMode,
  type WorktreeApplyStrategy,
} from "../../store/workspace";
import { t } from "../../i18n";
import { AgyApprovalHookControls } from "./AgyApprovalHookControls";
import {
  SegmentedPills,
  SettingGroup,
  SettingRow,
  ToggleSwitch,
} from "./SettingsModal";

type PermMode = "allow" | "ask";

export function WorkspaceGitSettingsTab() {
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
            value={workspace.sessionSandboxMode}
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
            onChange={(val) => void switchActiveSessionSandboxMode(val).catch(() => {
              // The shared session error is displayed in the composer shelf.
            })}
          />
        </SettingRow>

        <SettingRow
          icon={LayoutList}
          label={t("settings.sectionSidebarModeTitle")}
          tip={t("settings.sectionSidebarModeDesc")}
        >
          <SegmentedPills<"multi" | "single" | "recent">
            value={workspace.sidebarProjectMode}
            options={[
              {
                id: "multi",
                label: () => t("leftNav.multiProject"),
                tip: () => t("settings.sidebarMultiDesc"),
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
      </SettingGroup>

      {/* 2. Observer AI Code Audit */}
      <SettingGroup
        title={t("settings.sectionCodeAuditTitle")}
        tip={t("settings.sectionCodeAuditDesc")}
        action={
          <button
            type="button"
            onClick={() => {
              closeSettingsModal();
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

export function TerminalSafetySettingsTab() {
  const [permEdit, setPermEdit] = createSignal<PermMode>("allow");
  const [permBash, setPermBash] = createSignal<PermMode>("ask");
  const [permWebfetch, setPermWebfetch] = createSignal<PermMode>("allow");
  const [savingPerm, setSavingPerm] = createSignal(false);
  const [permNote, setPermNote] = createSignal<string | null>(null);

  onMount(async () => {
    try {
      const cfg = await fetchGlobalConfig();
      const perm = cfg?.permission ?? {};
      if (perm.edit === "allow" || perm.edit === "ask") setPermEdit(perm.edit);
      if (perm.bash === "allow" || perm.bash === "ask") setPermBash(perm.bash);
      if (perm.webfetch === "allow" || perm.webfetch === "ask")
        setPermWebfetch(perm.webfetch);
    } catch {
      /* ignore */
    }
  });

  const updatePerm = async (
    kind: "edit" | "bash" | "webfetch",
    mode: PermMode
  ) => {
    if (kind === "edit") setPermEdit(mode);
    if (kind === "bash") setPermBash(mode);
    if (kind === "webfetch") setPermWebfetch(mode);
    if (savingPerm()) return;
    setSavingPerm(true);
    setPermNote(null);
    try {
      await updateGlobalPermissions({
        edit: kind === "edit" ? mode : permEdit(),
        bash: kind === "bash" ? mode : permBash(),
        webfetch: kind === "webfetch" ? mode : permWebfetch(),
      });
      setPermNote(t("models.policyUpdated"));
      setTimeout(() => setPermNote(null), 2500);
    } catch {
      setPermNote(t("models.policyUpdateFailed"));
    } finally {
      setSavingPerm(false);
    }
  };

  return (
    <div class="space-y-6">
      {/* 1. Terminal Preferences */}
      <SettingGroup
        title={t("settings.sectionTerminalTitle")}
        tip={t("settings.sectionTerminalDesc")}
      >
        <SettingRow
          icon={TerminalIcon}
          label={t("settings.terminalFontSizeLabel")}
          tip={t("settings.terminalFontSizeDesc")}
        >
          <div class="inline-flex gap-0.5 rounded-xl border border-[var(--border-subtle)] bg-[var(--bg-inset)] p-1">
            <For each={[11, 12, 13, 14, 15, 16]}>
              {(sz) => (
                <button
                  type="button"
                  onClick={() => updateAppSetting("terminalFontSize", sz)}
                  class={`rounded-lg px-3 py-1 font-mono text-[13px] font-medium transition-colors cursor-pointer ${
                    appSettings.terminalFontSize === sz
                      ? "bg-[var(--bg-panel)] text-[var(--accent-primary)] shadow-2xs"
                      : "text-[var(--text-muted)] hover:text-[var(--text-primary)]"
                  }`}
                >
                  {sz}px
                </button>
              )}
            </For>
          </div>
        </SettingRow>

        <SettingRow
          label={t("settings.terminalCursorBlinkLabel")}
          tip={t("settings.terminalCursorBlinkDesc")}
        >
          <ToggleSwitch
            checked={appSettings.terminalCursorBlink}
            onChange={(val) => updateAppSetting("terminalCursorBlink", val)}
          />
        </SettingRow>
      </SettingGroup>

      {/* 2. Safety Gate & Tool Permission Policy */}
      <SettingGroup
        title={t("settings.sectionSafetyPolicyTitle")}
        tip={t("settings.sectionSafetyPolicyDesc")}
        action={
          <button
            type="button"
            onClick={() => {
              closeSettingsModal();
              openProviderModal("policy");
            }}
            class="inline-flex items-center gap-1.5 rounded-xl border border-[var(--border-subtle)] bg-[var(--bg-panel)] hover:bg-[var(--bg-hover)] px-3 py-1 text-[13px] font-medium text-[var(--text-secondary)] hover:text-[var(--text-primary)] transition-colors cursor-pointer"
          >
            <ShieldCheck size={13} class="text-[var(--accent-primary)]" />
            {t("settings.openFullPolicyModalBtn")}
          </button>
        }
      >
        <SettingRow
          label={t("models.fileEdits")}
          tip={t("models.fileEditsHint")}
        >
          <SegmentedPills<PermMode>
            value={permEdit()}
            options={[
              { id: "allow", label: () => t("models.autoAllow") },
              { id: "ask", label: () => t("models.ask") },
            ]}
            onChange={(m) => void updatePerm("edit", m)}
          />
        </SettingRow>

        <SettingRow
          label={t("models.shellCommands")}
          tip={t("models.shellCommandsHint")}
        >
          <SegmentedPills<PermMode>
            value={permBash()}
            options={[
              { id: "allow", label: () => t("models.autoAllow") },
              { id: "ask", label: () => t("models.ask") },
            ]}
            onChange={(m) => void updatePerm("bash", m)}
          />
        </SettingRow>

        <SettingRow label={t("models.webFetch")} tip={t("models.webFetchHint")}>
          <SegmentedPills<PermMode>
            value={permWebfetch()}
            options={[
              { id: "allow", label: () => t("models.autoAllow") },
              { id: "ask", label: () => t("models.ask") },
            ]}
            onChange={(m) => void updatePerm("webfetch", m)}
          />
        </SettingRow>
      </SettingGroup>

      <Show when={permNote()}>
        <div class="px-1 text-[12px] text-[var(--ok-text)]">{permNote()}</div>
      </Show>

      {/* 3. Antigravity approval hook (agy headless PreToolUse gate) */}
      <AgyApprovalHookControls />
    </div>
  );
}
