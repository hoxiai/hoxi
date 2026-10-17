// web/src/components/settings/SettingsWorkspaceTerminalTabs.tsx
import { createSignal, For, onMount, Show } from "solid-js";
import {
  Activity,
  Check,
  Cpu,
  FolderGit2,
  GitMerge,
  KeyRound,
  RefreshCw,
  RotateCcw,
  ShieldCheck,
  SlidersHorizontal,
  Terminal as TerminalIcon,
  Trash2,
} from "lucide-solid";
import { hoxiAPI, type SystemRuntimeInfo } from "../../services/api";
import {
  appSettings,
  closeSettingsModal,
  loadAgents,
  loadProviders,
  openProviderModal,
  resetAllAppSettings,
  resetProviderTelemetry,
  setObserverAutoAudit,
  setSidebarProjectMode,
  switchActiveSessionSandboxMode,
  updateAppSetting,
  workspace,
} from "../../store/workspace";
import { t } from "../../i18n";

type PermMode = "allow" | "ask";

export function WorkspaceGitSettingsTab() {
  return (
    <div class="space-y-6">
      {/* Default New Session Sandbox */}
      <section class="space-y-2.5">
        <div>
          <h3 class="text-[13.5px] font-semibold text-[var(--text-primary)]">
            {t("settings.sectionSandboxTitle")}
          </h3>
          <p class="text-[11.5px] text-[var(--text-muted)]">
            {t("settings.sectionSandboxDesc")}
          </p>
        </div>
        <div class="grid grid-cols-2 gap-3">
          <button
            type="button"
            onClick={() => void switchActiveSessionSandboxMode("local")}
            class={`flex flex-col items-start gap-1 rounded-xl border p-3.5 text-left transition-all cursor-pointer ${
              workspace.sessionSandboxMode === "local"
                ? "border-[var(--accent-border)] bg-[var(--accent-surface)]"
                : "border-[var(--border-subtle)] bg-[var(--bg-inset)] hover:border-[var(--border-strong)]"
            }`}
          >
            <div class="flex w-full items-center justify-between">
              <span class="text-[12.5px] font-semibold text-[var(--text-primary)]">
                {t("settings.sandboxLocalTitle")}
              </span>
              <Show when={workspace.sessionSandboxMode === "local"}>
                <Check size={14} class="text-[var(--accent-primary)]" />
              </Show>
            </div>
            <span class="text-[11px] text-[var(--text-muted)]">
              {t("settings.sandboxLocalDesc")}
            </span>
          </button>

          <button
            type="button"
            onClick={() => void switchActiveSessionSandboxMode("worktree")}
            class={`flex flex-col items-start gap-1 rounded-xl border p-3.5 text-left transition-all cursor-pointer ${
              workspace.sessionSandboxMode === "worktree"
                ? "border-[var(--accent-border)] bg-[var(--accent-surface)]"
                : "border-[var(--border-subtle)] bg-[var(--bg-inset)] hover:border-[var(--border-strong)]"
            }`}
          >
            <div class="flex w-full items-center justify-between">
              <span class="text-[12.5px] font-semibold text-[var(--text-primary)]">
                {t("settings.sandboxWorktreeTitle")}
              </span>
              <Show when={workspace.sessionSandboxMode === "worktree"}>
                <Check size={14} class="text-[var(--accent-primary)]" />
              </Show>
            </div>
            <span class="text-[11px] text-[var(--text-muted)]">
              {t("settings.sandboxWorktreeDesc")}
            </span>
          </button>
        </div>
      </section>

      {/* Sidebar Project Mode */}
      <section class="border-t border-[var(--border-subtle)] pt-5 space-y-2.5">
        <div>
          <h3 class="text-[13.5px] font-semibold text-[var(--text-primary)]">
            {t("settings.sectionSidebarModeTitle")}
          </h3>
          <p class="text-[11.5px] text-[var(--text-muted)]">
            {t("settings.sectionSidebarModeDesc")}
          </p>
        </div>
        <div class="grid grid-cols-2 gap-3">
          <button
            type="button"
            onClick={() => setSidebarProjectMode("multi")}
            class={`flex flex-col items-start gap-1 rounded-xl border p-3.5 text-left transition-all cursor-pointer ${
              workspace.sidebarProjectMode === "multi"
                ? "border-[var(--accent-border)] bg-[var(--accent-surface)]"
                : "border-[var(--border-subtle)] bg-[var(--bg-inset)] hover:border-[var(--border-strong)]"
            }`}
          >
            <div class="flex w-full items-center justify-between">
              <span class="text-[12.5px] font-semibold text-[var(--text-primary)]">
                {t("leftNav.multiProject")}
              </span>
              <Show when={workspace.sidebarProjectMode === "multi"}>
                <Check size={14} class="text-[var(--accent-primary)]" />
              </Show>
            </div>
            <span class="text-[11px] text-[var(--text-muted)]">
              {t("settings.sidebarMultiDesc")}
            </span>
          </button>

          <button
            type="button"
            onClick={() => setSidebarProjectMode("single")}
            class={`flex flex-col items-start gap-1 rounded-xl border p-3.5 text-left transition-all cursor-pointer ${
              workspace.sidebarProjectMode === "single"
                ? "border-[var(--accent-border)] bg-[var(--accent-surface)]"
                : "border-[var(--border-subtle)] bg-[var(--bg-inset)] hover:border-[var(--border-strong)]"
            }`}
          >
            <div class="flex w-full items-center justify-between">
              <span class="text-[12.5px] font-semibold text-[var(--text-primary)]">
                {t("leftNav.singleProject")}
              </span>
              <Show when={workspace.sidebarProjectMode === "single"}>
                <Check size={14} class="text-[var(--accent-primary)]" />
              </Show>
            </div>
            <span class="text-[11px] text-[var(--text-muted)]">
              {t("settings.sidebarSingleDesc")}
            </span>
          </button>
        </div>
      </section>

      {/* Default Diff Viewer Layout */}
      <section class="border-t border-[var(--border-subtle)] pt-5 space-y-2.5">
        <div>
          <h3 class="text-[13.5px] font-semibold text-[var(--text-primary)]">
            {t("settings.sectionDiffStyleTitle")}
          </h3>
          <p class="text-[11.5px] text-[var(--text-muted)]">
            {t("settings.sectionDiffStyleDesc")}
          </p>
        </div>
        <div class="grid grid-cols-2 gap-3">
          <button
            type="button"
            onClick={() => updateAppSetting("defaultDiffStyle", "unified")}
            class={`flex flex-col items-start gap-1 rounded-xl border p-3.5 text-left transition-all cursor-pointer ${
              appSettings.defaultDiffStyle === "unified"
                ? "border-[var(--accent-border)] bg-[var(--accent-surface)]"
                : "border-[var(--border-subtle)] bg-[var(--bg-inset)] hover:border-[var(--border-strong)]"
            }`}
          >
            <div class="flex w-full items-center justify-between">
              <span class="text-[12.5px] font-semibold text-[var(--text-primary)]">
                {t("settings.diffUnifiedTitle")}
              </span>
              <Show when={appSettings.defaultDiffStyle === "unified"}>
                <Check size={14} class="text-[var(--accent-primary)]" />
              </Show>
            </div>
            <span class="text-[11px] text-[var(--text-muted)]">
              {t("settings.diffUnifiedDesc")}
            </span>
          </button>

          <button
            type="button"
            onClick={() => updateAppSetting("defaultDiffStyle", "split")}
            class={`flex flex-col items-start gap-1 rounded-xl border p-3.5 text-left transition-all cursor-pointer ${
              appSettings.defaultDiffStyle === "split"
                ? "border-[var(--accent-border)] bg-[var(--accent-surface)]"
                : "border-[var(--border-subtle)] bg-[var(--bg-inset)] hover:border-[var(--border-strong)]"
            }`}
          >
            <div class="flex w-full items-center justify-between">
              <span class="text-[12.5px] font-semibold text-[var(--text-primary)]">
                {t("settings.diffSplitTitle")}
              </span>
              <Show when={appSettings.defaultDiffStyle === "split"}>
                <Check size={14} class="text-[var(--accent-primary)]" />
              </Show>
            </div>
            <span class="text-[11px] text-[var(--text-muted)]">
              {t("settings.diffSplitDesc")}
            </span>
          </button>
        </div>
      </section>

      {/* Worktree Apply-to-Main Strategy */}
      <section class="border-t border-[var(--border-subtle)] pt-5 space-y-2.5">
        <div>
          <h3 class="text-[13.5px] font-semibold text-[var(--text-primary)]">
            {t("settings.sectionApplyModeTitle")}
          </h3>
          <p class="text-[11.5px] text-[var(--text-muted)]">
            {t("settings.sectionApplyModeDesc")}
          </p>
        </div>
        <div class="grid grid-cols-2 gap-3">
          <button
            type="button"
            onClick={() => updateAppSetting("worktreeApplyMode", "patch")}
            class={`flex flex-col items-start gap-1 rounded-xl border p-3.5 text-left transition-all cursor-pointer ${
              appSettings.worktreeApplyMode === "patch"
                ? "border-[var(--accent-border)] bg-[var(--accent-surface)]"
                : "border-[var(--border-subtle)] bg-[var(--bg-inset)] hover:border-[var(--border-strong)]"
            }`}
          >
            <div class="flex w-full items-center justify-between">
              <span class="inline-flex items-center gap-1.5 text-[12.5px] font-semibold text-[var(--text-primary)]">
                <FolderGit2 size={13} class="text-[var(--accent-primary)]" />
                {t("settings.applyPatchTitle")}
              </span>
              <Show when={appSettings.worktreeApplyMode === "patch"}>
                <Check size={14} class="text-[var(--accent-primary)]" />
              </Show>
            </div>
            <span class="text-[11px] text-[var(--text-muted)]">
              {t("settings.applyPatchDesc")}
            </span>
          </button>

          <button
            type="button"
            onClick={() => updateAppSetting("worktreeApplyMode", "merge")}
            class={`flex flex-col items-start gap-1 rounded-xl border p-3.5 text-left transition-all cursor-pointer ${
              appSettings.worktreeApplyMode === "merge"
                ? "border-[var(--accent-border)] bg-[var(--accent-surface)]"
                : "border-[var(--border-subtle)] bg-[var(--bg-inset)] hover:border-[var(--border-strong)]"
            }`}
          >
            <div class="flex w-full items-center justify-between">
              <span class="inline-flex items-center gap-1.5 text-[12.5px] font-semibold text-[var(--text-primary)]">
                <GitMerge size={13} class="text-[var(--accent-primary)]" />
                {t("settings.applyMergeTitle")}
              </span>
              <Show when={appSettings.worktreeApplyMode === "merge"}>
                <Check size={14} class="text-[var(--accent-primary)]" />
              </Show>
            </div>
            <span class="text-[11px] text-[var(--text-muted)]">
              {t("settings.applyMergeDesc")}
            </span>
          </button>
        </div>
      </section>

      {/* Observer AI Code Quality Audit */}
      <section class="border-t border-[var(--border-subtle)] pt-5 space-y-3">
        <div class="flex items-center justify-between gap-2">
          <div>
            <h3 class="text-[13.5px] font-semibold text-[var(--text-primary)]">
              {t("settings.sectionCodeAuditTitle")}
            </h3>
            <p class="text-[11.5px] text-[var(--text-muted)]">
              {t("settings.sectionCodeAuditDesc")}
            </p>
          </div>
          <button
            type="button"
            onClick={() => {
              closeSettingsModal();
              openProviderModal("models");
            }}
            class="shrink-0 inline-flex items-center gap-1.5 rounded-xl border border-[var(--border-strong)] bg-[var(--bg-elevated)] hover:bg-[var(--bg-hover)] px-3 py-1.5 text-[11.5px] font-medium text-[var(--text-primary)] transition-colors cursor-pointer"
          >
            <SlidersHorizontal size={12} />
            {t("settings.configureObserverBtn")}
          </button>
        </div>

        <label class="flex items-center justify-between gap-3 rounded-xl border border-[var(--border-subtle)] bg-[var(--bg-inset)] px-4 py-3 cursor-pointer">
          <div class="min-w-0">
            <div class="text-[12.5px] font-medium text-[var(--text-primary)]">
              {t("settings.autoAuditLabel")}
            </div>
            <div class="text-[11px] text-[var(--text-muted)]">
              {t("settings.autoAuditDesc")}
            </div>
          </div>
          <input
            type="checkbox"
            checked={workspace.observerAutoAudit}
            onChange={(ev) => setObserverAutoAudit(ev.currentTarget.checked)}
            class="h-4 w-4 accent-[var(--accent-primary)] cursor-pointer shrink-0"
          />
        </label>
      </section>
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
      const cfg = await hoxiAPI.getGlobalConfig();
      const perm = cfg?.permission ?? {};
      if (perm.edit === "allow" || perm.edit === "ask") setPermEdit(perm.edit);
      if (perm.bash === "allow" || perm.bash === "ask") setPermBash(perm.bash);
      if (perm.webfetch === "allow" || perm.webfetch === "ask") setPermWebfetch(perm.webfetch);
    } catch {
      /* ignore */
    }
  });

  const updatePerm = async (kind: "edit" | "bash" | "webfetch", mode: PermMode) => {
    if (kind === "edit") setPermEdit(mode);
    if (kind === "bash") setPermBash(mode);
    if (kind === "webfetch") setPermWebfetch(mode);
    if (savingPerm()) return;
    setSavingPerm(true);
    setPermNote(null);
    try {
      const cfg = await hoxiAPI.getGlobalConfig();
      const nextPerm = {
        ...(cfg?.permission ?? {}),
        edit: kind === "edit" ? mode : permEdit(),
        bash: kind === "bash" ? mode : permBash(),
        webfetch: kind === "webfetch" ? mode : permWebfetch(),
      };
      await hoxiAPI.updateGlobalConfig({ ...cfg, permission: nextPerm });
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
      {/* Terminal Preferences */}
      <section class="space-y-3">
        <div>
          <h3 class="text-[13.5px] font-semibold text-[var(--text-primary)]">
            {t("settings.sectionTerminalTitle")}
          </h3>
          <p class="text-[11.5px] text-[var(--text-muted)]">
            {t("settings.sectionTerminalDesc")}
          </p>
        </div>

        <div class="flex items-center justify-between gap-4 rounded-xl border border-[var(--border-subtle)] bg-[var(--bg-inset)] px-4 py-3">
          <div class="min-w-0">
            <div class="flex items-center gap-1.5 text-[12.5px] font-medium text-[var(--text-primary)]">
              <TerminalIcon size={13} class="text-[var(--accent-primary)]" />
              {t("settings.terminalFontSizeLabel")}
            </div>
            <div class="text-[11px] text-[var(--text-muted)]">
              {t("settings.terminalFontSizeDesc")}
            </div>
          </div>
          <div class="inline-flex rounded-lg border border-[var(--border-subtle)] bg-[var(--bg-panel)] p-0.5 shrink-0">
            <For each={[11, 12, 13, 14, 15, 16]}>
              {(sz) => (
                <button
                  type="button"
                  onClick={() => updateAppSetting("terminalFontSize", sz)}
                  class={`rounded-md px-2.5 py-1 font-mono text-[11px] font-medium transition-colors cursor-pointer ${
                    appSettings.terminalFontSize === sz
                      ? "bg-[var(--accent-surface)] text-[var(--accent-primary)]"
                      : "text-[var(--text-muted)] hover:text-[var(--text-primary)]"
                  }`}
                >
                  {sz}px
                </button>
              )}
            </For>
          </div>
        </div>

        <label class="flex items-center justify-between gap-3 rounded-xl border border-[var(--border-subtle)] bg-[var(--bg-inset)] px-4 py-3 cursor-pointer">
          <div class="min-w-0">
            <div class="text-[12.5px] font-medium text-[var(--text-primary)]">
              {t("settings.terminalCursorBlinkLabel")}
            </div>
            <div class="text-[11px] text-[var(--text-muted)]">
              {t("settings.terminalCursorBlinkDesc")}
            </div>
          </div>
          <input
            type="checkbox"
            checked={appSettings.terminalCursorBlink}
            onChange={(ev) =>
              updateAppSetting("terminalCursorBlink", ev.currentTarget.checked)
            }
            class="h-4 w-4 accent-[var(--accent-primary)] cursor-pointer shrink-0"
          />
        </label>
      </section>

      {/* Safety Gate & Auto-Approve Policy */}
      <section class="border-t border-[var(--border-subtle)] pt-5 space-y-3">
        <div class="flex items-center justify-between gap-3">
          <div>
            <h3 class="text-[13.5px] font-semibold text-[var(--text-primary)]">
              {t("settings.sectionSafetyPolicyTitle")}
            </h3>
            <p class="text-[11.5px] text-[var(--text-muted)]">
              {t("settings.sectionSafetyPolicyDesc")}
            </p>
          </div>
          <button
            type="button"
            onClick={() => {
              closeSettingsModal();
              openProviderModal("policy");
            }}
            class="shrink-0 inline-flex items-center gap-1.5 rounded-xl border border-[var(--border-strong)] bg-[var(--bg-elevated)] hover:bg-[var(--bg-hover)] px-3 py-1.5 text-[11.5px] font-medium text-[var(--text-primary)] transition-colors cursor-pointer"
          >
            <ShieldCheck size={13} class="text-[var(--accent-primary)]" />
            {t("settings.openFullPolicyModalBtn")}
          </button>
        </div>

        <div class="grid grid-cols-3 gap-3">
          <For
            each={[
              {
                key: "edit" as const,
                title: t("models.fileEdits"),
                hint: t("models.fileEditsHint"),
                val: permEdit,
              },
              {
                key: "bash" as const,
                title: t("models.shellCommands"),
                hint: t("models.shellCommandsHint"),
                val: permBash,
              },
              {
                key: "webfetch" as const,
                title: t("models.webFetch"),
                hint: t("models.webFetchHint"),
                val: permWebfetch,
              },
            ]}
          >
            {(item) => (
              <div class="rounded-xl border border-[var(--border-subtle)] bg-[var(--bg-inset)] p-3 space-y-2 flex flex-col justify-between">
                <div>
                  <div class="text-[12px] font-medium text-[var(--text-primary)]">
                    {item.title}
                  </div>
                  <div class="text-[11px] text-[var(--text-muted)] mt-0.5">
                    {item.hint}
                  </div>
                </div>
                <div class="inline-flex rounded-lg border border-[var(--border-subtle)] bg-[var(--bg-panel)] p-0.5 w-full">
                  <For each={["allow", "ask"] as PermMode[]}>
                    {(m) => (
                      <button
                        type="button"
                        onClick={() => void updatePerm(item.key, m)}
                        class={`flex-1 rounded-md py-1 text-[11px] font-medium cursor-pointer transition-colors ${
                          item.val() === m
                            ? "bg-[var(--accent-surface)] text-[var(--accent-primary)]"
                            : "text-[var(--text-muted)] hover:text-[var(--text-primary)]"
                        }`}
                      >
                        {m === "allow" ? t("models.autoAllow") : t("models.ask")}
                      </button>
                    )}
                  </For>
                </div>
              </div>
            )}
          </For>
        </div>

        <Show when={permNote()}>
          <div class="text-[11.5px] text-[var(--ok-text)]">{permNote()}</div>
        </Show>
      </section>
    </div>
  );
}

export function SystemRuntimeSettingsTab() {
  const [sysInfo, setSysInfo] = createSignal<SystemRuntimeInfo | null>(null);
  const [reloading, setReloading] = createSignal(false);
  const [actionToast, setActionToast] = createSignal<string | null>(null);

  const fetchInfo = async () => {
    try {
      const info = await hoxiAPI.getSystemInfo();
      setSysInfo(info);
    } catch {
      /* ignore */
    }
  };

  onMount(() => {
    void fetchInfo();
  });

  const showToast = (msg: string) => {
    setActionToast(msg);
    setTimeout(() => setActionToast(null), 3000);
  };

  const onReloadEngine = async () => {
    if (reloading()) return;
    setReloading(true);
    try {
      await hoxiAPI.disposeGlobal();
      await Promise.allSettled([
        loadProviders(),
        loadAgents(workspace.rootDir || undefined),
        fetchInfo(),
      ]);
      showToast(t("settings.reloadEngineSuccess"));
    } catch {
      /* ignore */
    } finally {
      setReloading(false);
    }
  };

  const onClearTelemetry = async () => {
    await resetProviderTelemetry();
    showToast(t("settings.resetTelemetrySuccess"));
  };

  const onResetSettings = () => {
    if (!confirm(t("settings.resetSettingsConfirm"))) return;
    resetAllAppSettings();
    showToast(t("settings.resetSettingsSuccess"));
  };

  return (
    <div class="space-y-6">
      <section class="space-y-3">
        <div class="flex items-center justify-between">
          <div>
            <h3 class="text-[13.5px] font-semibold text-[var(--text-primary)]">
              {t("settings.sectionRuntimeTitle")}
            </h3>
            <p class="text-[11.5px] text-[var(--text-muted)]">
              {t("settings.sectionRuntimeDesc")}
            </p>
          </div>
          <button
            type="button"
            onClick={() => void fetchInfo()}
            class="rounded-lg p-1.5 text-[var(--text-muted)] hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)] transition-colors cursor-pointer"
            title={t("common.refresh")}
          >
            <RefreshCw size={13} />
          </button>
        </div>

        <div class="rounded-xl border border-[var(--border-subtle)] bg-[var(--bg-inset)] divide-y divide-[var(--border-subtle)] text-[12px]">
          <div class="flex items-center justify-between px-4 py-2.5">
            <span class="text-[var(--text-muted)]">{t("settings.runtimeStatusLabel")}</span>
            <span class="inline-flex items-center gap-1.5 font-medium text-[var(--text-primary)]">
              <span
                class={`h-2 w-2 rounded-full ${
                  workspace.connected ? "bg-[var(--ok)]" : "bg-[var(--warn)] animate-pulse"
                }`}
              />
              {workspace.connected ? t("common.ready") : t("common.connecting")}
            </span>
          </div>

          <div class="flex items-center justify-between gap-4 px-4 py-2.5">
            <span class="text-[var(--text-muted)] shrink-0">{t("settings.runtimeVersionLabel")}</span>
            <span class="font-mono text-[11.5px] text-[var(--text-primary)] truncate">
              {sysInfo()?.openCodeVersion || t("settings.runtimeNotDetected")}
            </span>
          </div>

          <div class="flex items-center justify-between gap-4 px-4 py-2.5">
            <span class="text-[var(--text-muted)] shrink-0">{t("settings.runtimeBinaryLabel")}</span>
            <span class="font-mono text-[11px] text-[var(--text-secondary)] truncate selectable-text">
              {sysInfo()?.openCodeBinPath || t("settings.runtimeNotDetected")}
            </span>
          </div>

          <div class="flex items-center justify-between gap-4 px-4 py-2.5">
            <span class="text-[var(--text-muted)] shrink-0">{t("settings.runtimePortLabel")}</span>
            <span class="font-mono text-[11.5px] text-[var(--text-primary)]">
              127.0.0.1:{sysInfo()?.openCodePort || "—"}
            </span>
          </div>

          <div class="flex items-center justify-between gap-4 px-4 py-2.5">
            <span class="text-[var(--text-muted)] shrink-0">{t("settings.runtimeGatewayLabel")}</span>
            <span class="font-mono text-[11px] text-[var(--text-secondary)] truncate selectable-text">
              {sysInfo()?.wsBaseURL || workspace.wsBaseURL || "—"}
            </span>
          </div>

          <div class="flex items-center justify-between gap-4 px-4 py-2.5">
            <span class="text-[var(--text-muted)] shrink-0">{t("settings.runtimeConfigPathLabel")}</span>
            <span class="font-mono text-[11px] text-[var(--text-secondary)] truncate selectable-text">
              {sysInfo()?.userConfigPath || "~/.hoxi/models.json"}
            </span>
          </div>

          <div class="flex items-center justify-between gap-4 px-4 py-2.5">
            <span class="text-[var(--text-muted)] shrink-0">{t("settings.runtimePlatformLabel")}</span>
            <span class="font-mono text-[11.5px] text-[var(--text-primary)]">
              {sysInfo() ? `${sysInfo()!.os}/${sysInfo()!.arch} (${sysInfo()!.goVersion})` : "—"}
            </span>
          </div>
        </div>
      </section>

      {/* Maintenance Actions */}
      <section class="border-t border-[var(--border-subtle)] pt-5 space-y-3">
        <div>
          <h3 class="text-[13.5px] font-semibold text-[var(--text-primary)]">
            {t("settings.sectionMaintenanceTitle")}
          </h3>
          <p class="text-[11.5px] text-[var(--text-muted)]">
            {t("settings.sectionMaintenanceDesc")}
          </p>
        </div>

        <div class="flex flex-wrap items-center gap-2.5">
          <button
            type="button"
            disabled={reloading()}
            onClick={() => void onReloadEngine()}
            class="inline-flex items-center gap-1.5 rounded-xl bg-[var(--accent-primary)] px-3.5 py-2 text-[12px] font-medium text-white hover:opacity-90 disabled:opacity-50 transition-opacity cursor-pointer"
          >
            <Cpu size={13} />
            {reloading() ? t("settings.reloadingEngineBtn") : t("settings.reloadEngineBtn")}
          </button>

          <button
            type="button"
            onClick={() => {
              closeSettingsModal();
              openProviderModal("providers");
            }}
            class="inline-flex items-center gap-1.5 rounded-xl border border-[var(--border-strong)] bg-[var(--bg-elevated)] hover:bg-[var(--bg-hover)] px-3.5 py-2 text-[12px] font-medium text-[var(--text-primary)] transition-colors cursor-pointer"
          >
            <KeyRound size={13} class="text-[var(--accent-primary)]" />
            {t("settings.manageModelsAndKeysBtn")}
          </button>

          <button
            type="button"
            onClick={() => void onClearTelemetry()}
            class="inline-flex items-center gap-1.5 rounded-xl border border-[var(--border-subtle)] bg-[var(--bg-inset)] hover:bg-[var(--bg-hover)] px-3.5 py-2 text-[12px] font-medium text-[var(--text-secondary)] hover:text-[var(--text-primary)] transition-colors cursor-pointer"
          >
            <Activity size={13} />
            {t("settings.resetTelemetryBtn")}
          </button>

          <button
            type="button"
            onClick={onResetSettings}
            class="inline-flex items-center gap-1.5 rounded-xl border border-[var(--danger-border)] bg-[var(--danger-surface)] px-3.5 py-2 text-[12px] font-medium text-[var(--danger-text)] hover:opacity-90 transition-opacity cursor-pointer"
          >
            <RotateCcw size={13} />
            {t("settings.resetSettingsBtn")}
          </button>
        </div>

        <Show when={actionToast()}>
          <div class="rounded-xl border border-[var(--ok-border)] bg-[var(--ok-surface)] px-3.5 py-2 text-[12px] text-[var(--ok-text)]">
            {actionToast()}
          </div>
        </Show>
      </section>
    </div>
  );
}
