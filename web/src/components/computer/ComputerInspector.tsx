import { createEffect, createMemo, createSignal, For, Show } from "solid-js";
import {
  ArrowLeftRight,
  Camera,
  Check,
  ChevronDown,
  ChevronRight,
  FolderOpen,
  Info,
  ListTree,
  Pause,
  Play,
  RefreshCw,
  Search,
  ShieldAlert,
  ShieldCheck,
  Square,
  XCircle,
} from "lucide-solid";
import { createComputerController } from "../../store/workspace";
import { t } from "../../i18n";
import ComputerSnapshotView from "./ComputerSnapshotView";

export default function ComputerInspector() {
  const control = createComputerController();
  const [showInfo, setShowInfo] = createSignal(false);
  const [showPermissionHelp, setShowPermissionHelp] = createSignal(false);
  const [showSwitcher, setShowSwitcher] = createSignal(false);
  const [showElements, setShowElements] = createSignal(false);

  const btnBase =
    "inline-flex items-center gap-1 rounded-md bg-[var(--bg-panel)] px-2 py-1 text-[11.5px] font-medium text-[var(--text-secondary)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-hover)] disabled:opacity-40 disabled:cursor-not-allowed transition-colors cursor-pointer";
  const iconBtn =
    "inline-flex h-7 w-7 shrink-0 items-center justify-center rounded-md bg-[var(--bg-panel)] text-[var(--text-secondary)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-hover)] disabled:opacity-40 disabled:cursor-not-allowed transition-colors cursor-pointer";

  const ready = createMemo(() =>
    Boolean(
      control.permissions()?.supported &&
        control.permissions()?.accessibility &&
        control.permissions()?.screenRecording
    )
  );

  const missingMacPermission = createMemo(() => {
    const p = control.permissions();
    return p && p.supported && p.platform === "darwin" && (!p.accessibility || !p.screenRecording)
      ? p
      : null;
  });

  const missingWindowsPermission = createMemo(() => {
    const p = control.permissions();
    return Boolean(p && p.supported && p.platform === "windows" && (!p.accessibility || !p.screenRecording));
  });

  const grantedAppID = createMemo(() => control.grant()?.app.id ?? "");

  const selectedVisibleApp = createMemo(() =>
    control.filteredApps().find((app) => app.id === control.selected() && !app.blocked)
  );

  const permissionTooltip = createMemo(() => {
    const p = control.permissions();
    if (!p) return "";
    if (!p.supported) return t("computer.unsupported");
    const summary = `${t("computer.screenPermission")}: ${t(
      p.screenRecording ? "computer.ready" : "computer.required"
    )} · ${t("computer.accessibilityPermission")}: ${t(
      p.accessibility ? "computer.ready" : "computer.required"
    )}`;
    return p.platform === "windows" ? `${summary}\n${t("computer.windowsHint")}` : summary;
  });

  createEffect(() => {
    control.scope();
    setShowSwitcher(false);
    setShowElements(false);
  });

  createEffect(() => {
    const appID = grantedAppID();
    setShowSwitcher(false);
    if (!appID) setShowElements(false);
  });

  createEffect(() => {
    if (ready()) setShowPermissionHelp(false);
  });

  const handleAuthorize = async () => {
    const targetID = selectedVisibleApp()?.id;
    if (!targetID) return;
    await control.authorize();
    if (control.grant()?.app.id === targetID) {
      setShowSwitcher(false);
    }
  };

  const AppPicker = () => (
    <div class="space-y-1.5">
      <Show when={control.apps().length > 6 || control.appFilter().trim().length > 0}>
        <div class="relative">
          <Search
            size={12}
            class="pointer-events-none absolute left-2.5 top-1/2 -translate-y-1/2 text-[var(--text-faint)]"
          />
          <input
            type="text"
            value={control.appFilter()}
            onInput={(e) => control.setAppFilter(e.currentTarget.value)}
            placeholder={t("computer.filterAppsPlaceholder")}
            class="w-full rounded-md border border-transparent bg-[var(--bg-panel)] pl-7 pr-2.5 py-1 text-[11.5px] text-[var(--text-primary)] placeholder:text-[var(--text-faint)] outline-none focus:border-[var(--accent-border)]"
          />
        </div>
      </Show>

      <div class="flex items-center gap-1.5">
        <select
          class="min-w-0 flex-1 h-7 rounded-md border border-transparent bg-[var(--bg-panel)] px-2 text-[11.5px] text-[var(--text-primary)] outline-none focus:border-[var(--accent-border)]"
          value={selectedVisibleApp()?.id ?? ""}
          aria-label={t("computer.targetApp")}
          title={selectedVisibleApp()?.path || t("computer.targetApp")}
          onChange={(event) => control.setSelected(event.currentTarget.value)}
        >
          <option value="" selected={!selectedVisibleApp()}>
            {t("computer.targetApp")}
          </option>
          <For each={control.filteredApps()}>
            {(app) => (
              <option value={app.id} disabled={app.blocked} selected={app.id === control.selected()}>
                {app.name} (PID {app.pid})
                {app.blocked ? ` · ${t("computer.protectedApp")}` : ""}
              </option>
            )}
          </For>
        </select>
        <button
          type="button"
          class={iconBtn}
          onClick={() => void control.refreshApps()}
          title={t("computer.refreshApps")}
          aria-label={t("computer.refreshApps")}
        >
          <RefreshCw size={12} />
        </button>
        <button
          type="button"
          class="inline-flex h-7 shrink-0 items-center justify-center rounded-md bg-[var(--accent-surface)] px-2.5 text-[11.5px] font-medium text-[var(--accent-primary)] hover:opacity-90 disabled:opacity-40 disabled:cursor-not-allowed transition-opacity cursor-pointer"
          disabled={!control.permissions()?.supported || !selectedVisibleApp() || control.busy()}
          onClick={() => void handleAuthorize()}
        >
          {t("computer.authorize")}
        </button>
      </div>
    </div>
  );

  return (
    <div class="flex h-full min-h-0 flex-col text-xs text-[var(--text-primary)]">
      {/* Compact Status Strip */}
      <div class="flex shrink-0 items-center justify-between gap-2 px-3 pt-1.5 pb-1">
        <div class="flex items-center gap-1.5 min-w-0">
          <Show when={control.permissions()}>
            {(permission) => (
              <span
                class="inline-flex items-center gap-1 text-[11px] font-medium text-[var(--text-muted)] truncate"
                title={permissionTooltip()}
              >
                <Show
                  when={ready()}
                  fallback={<ShieldAlert size={12} class="text-[var(--warn-text)] shrink-0" />}
                >
                  <ShieldCheck size={12} class="text-[var(--ok-text)] shrink-0" />
                </Show>
                <span class="truncate">
                  {!permission().supported
                    ? t("computer.unsupported")
                    : ready()
                      ? t("computer.permissionsReady")
                      : t("computer.permissionsMissing")}
                </span>
              </span>
            )}
          </Show>
          <button
            type="button"
            class={`inline-flex h-5 w-5 shrink-0 items-center justify-center rounded text-[var(--text-faint)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-hover)] transition-colors cursor-pointer ${
              showInfo() ? "bg-[var(--bg-hover)] text-[var(--text-primary)]" : ""
            }`}
            onClick={() => setShowInfo((prev) => !prev)}
            title={t("computer.aboutControl")}
            aria-label={t("computer.aboutControl")}
            aria-expanded={showInfo()}
          >
            <Info size={12} />
          </button>
        </div>

        <button
          type="button"
          class={`inline-flex items-center gap-1 rounded-md px-2 py-1 text-[11px] font-medium transition-colors cursor-pointer shrink-0 ${
            control.grant()
              ? "bg-[var(--danger-surface)] text-[var(--danger-text)] hover:opacity-90"
              : "text-[var(--text-muted)] hover:bg-[var(--danger-surface)] hover:text-[var(--danger-text)]"
          }`}
          title={t("computer.stopAllTitle")}
          onClick={() => void control.updateAccess("stop")}
        >
          <Square size={9} class="fill-current" />
          <span>{t("computer.stopAll")}</span>
        </button>
      </div>

      {/* Main Content */}
      <div class="min-h-0 flex-1 space-y-2.5 overflow-y-auto px-3 py-1.5">
        {/* On-Demand Control Info */}
        <Show when={showInfo()}>
          <div class="rounded-xl bg-[var(--bg-inset)]/60 p-2.5 space-y-1.5 text-[11px] leading-relaxed text-[var(--text-secondary)]">
            <p>{t("computer.disclosure")}</p>
            <Show when={control.permissions()?.platform === "windows"}>
              <p class="text-[var(--text-muted)]">{t("computer.windowsHint")}</p>
            </Show>
          </div>
        </Show>

        {/* Windows Non-Interactive Warning (only shown when desktop is locked/unavailable) */}
        <Show when={missingWindowsPermission()}>
          <div class="rounded-xl bg-[var(--warn-surface)]/45 px-2.5 py-2 text-[11px] leading-relaxed text-[var(--warn-text)]">
            {t("computer.windowsHint")}
          </div>
        </Show>

        {/* System Permissions Setup Card (only shown when missing on macOS) */}
        <Show when={missingMacPermission()}>
          {(permission) => (
            <div class="rounded-xl bg-[var(--bg-inset)]/60 p-2.5 space-y-2">
              <div class="flex flex-wrap items-center gap-1.5 text-[11px]">
                <span
                  class={`inline-flex items-center gap-1 rounded px-1.5 py-0.5 font-medium ${
                    permission().screenRecording
                      ? "bg-[var(--ok-surface)] text-[var(--ok-text)]"
                      : "bg-[var(--warn-surface)] text-[var(--warn-text)]"
                  }`}
                >
                  {t("computer.screenPermission")}:{" "}
                  {t(permission().screenRecording ? "computer.ready" : "computer.required")}
                </span>
                <span
                  class={`inline-flex items-center gap-1 rounded px-1.5 py-0.5 font-medium ${
                    permission().accessibility
                      ? "bg-[var(--ok-surface)] text-[var(--ok-text)]"
                      : "bg-[var(--warn-surface)] text-[var(--warn-text)]"
                  }`}
                >
                  {t("computer.accessibilityPermission")}:{" "}
                  {t(permission().accessibility ? "computer.ready" : "computer.required")}
                </span>
              </div>

              <div class="flex flex-wrap gap-1.5">
                <button
                  type="button"
                  class={btnBase}
                  disabled={control.busy() || permission().screenRecording}
                  onClick={() => void control.permission("screen")}
                >
                  {t("computer.requestScreen")}
                </button>
                <button
                  type="button"
                  class={btnBase}
                  disabled={control.busy() || permission().accessibility}
                  onClick={() => void control.permission("accessibility")}
                >
                  {t("computer.requestAccessibility")}
                </button>
              </div>

              <p class="text-[11px] leading-relaxed text-[var(--text-muted)]">
                {t("computer.systemPermissionHint")}
              </p>

              <div class="border-t border-[var(--border-subtle)]/50 pt-1.5">
                <button
                  type="button"
                  class="inline-flex items-center gap-1 text-[11px] text-[var(--text-muted)] hover:text-[var(--text-primary)] cursor-pointer"
                  aria-expanded={showPermissionHelp()}
                  onClick={() => setShowPermissionHelp((prev) => !prev)}
                >
                  <Show when={showPermissionHelp()} fallback={<ChevronRight size={12} />}>
                    <ChevronDown size={12} />
                  </Show>
                  <span>{t("computer.permissionTroubleshoot")}</span>
                </button>

                <Show when={showPermissionHelp()}>
                  <div class="mt-2 space-y-2">
                    <p class="text-[11px] leading-relaxed text-[var(--warn-text)]">
                      {t("computer.resetPermissionHint")}
                    </p>
                    <Show when={permission().appName || permission().appPath}>
                      <div class="rounded-lg bg-[var(--bg-panel)]/70 px-2 py-1.5 space-y-0.5 text-[10.5px]">
                        <Show when={permission().appName}>
                          <div class="font-medium text-[var(--text-secondary)] truncate">
                            {t("computer.currentAppLabel", { name: permission().appName ?? "Hoxi" })}
                            <Show when={permission().bundleID}>
                              <span class="ml-1 font-mono text-[10px] text-[var(--text-muted)]">
                                ({permission().bundleID})
                              </span>
                            </Show>
                          </div>
                        </Show>
                        <Show when={permission().appPath}>
                          <div class="truncate font-mono text-[10px] text-[var(--text-faint)]">
                            {permission().appPath}
                          </div>
                        </Show>
                      </div>
                    </Show>
                    <div class="flex flex-wrap gap-1.5">
                      <button
                        type="button"
                        class="inline-flex items-center gap-1 rounded-md bg-[var(--accent-surface)] px-2 py-1 text-[11px] font-medium text-[var(--accent-primary)] hover:opacity-90 disabled:opacity-40 disabled:cursor-not-allowed transition-opacity cursor-pointer"
                        disabled={control.busy()}
                        onClick={() => void control.permission("reset")}
                      >
                        <RefreshCw size={11} />
                        <span>{t("computer.resetPermissions")}</span>
                      </button>
                      <Show when={permission().appPath}>
                        <button
                          type="button"
                          class={btnBase}
                          disabled={control.busy()}
                          onClick={() => void control.permission("reveal")}
                        >
                          <FolderOpen size={11} />
                          <span>{t("computer.revealApp")}</span>
                        </button>
                      </Show>
                    </div>
                  </div>
                </Show>
              </div>
            </div>
          )}
        </Show>

        {/* Primary Area: Active Grant OR App Picker */}
        <Show
          when={control.canAuthorize()}
          fallback={
            <div class="rounded-xl bg-[var(--bg-inset)]/60 p-3 text-center text-[11.5px] text-[var(--text-muted)]">
              {t("computer.selectSession")}
            </div>
          }
        >
          <Show
            when={control.grant()}
            fallback={
              <div class="rounded-xl bg-[var(--bg-inset)]/60 p-2.5 space-y-2">
                <AppPicker />
                <p class="text-[11px] text-[var(--text-muted)]">{t("computer.briefDisclosure")}</p>
              </div>
            }
          >
            {(grant) => (
              <div class="space-y-2 rounded-xl bg-[var(--accent-surface)]/30 p-2.5">
                {/* Active App Header */}
                <div class="flex items-center justify-between gap-2">
                  <div class="flex items-center gap-1.5 min-w-0">
                    <span
                      class={`inline-block h-2 w-2 rounded-full shrink-0 ${
                        grant().paused ? "bg-[var(--warn-text)]" : "bg-[var(--ok-text)]"
                      }`}
                    />
                    <p
                      role="status"
                      class="font-semibold text-[12px] truncate"
                      title={grant().app.path}
                    >
                      {grant().app.name}
                      <span class="ml-1 font-normal text-[11px] text-[var(--text-muted)]">
                        · {t(grant().paused ? "computer.paused" : "computer.authorized")}
                      </span>
                    </p>
                  </div>
                  <div class="flex items-center gap-1 shrink-0">
                    <Show when={grant().lastAction}>
                      <span
                        class="rounded bg-[var(--bg-panel)]/80 px-1.5 py-0.5 font-mono text-[10px] text-[var(--text-muted)]"
                        title={t("computer.lastActionLabel", { action: grant().lastAction ?? "" })}
                      >
                        {grant().lastAction}
                      </span>
                    </Show>
                    <button
                      type="button"
                      class={`inline-flex h-6 w-6 items-center justify-center rounded-md transition-colors cursor-pointer ${
                        showSwitcher()
                          ? "bg-[var(--accent-surface)] text-[var(--accent-primary)]"
                          : "text-[var(--text-muted)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-panel)]"
                      }`}
                      title={t("computer.switchApp")}
                      aria-label={t("computer.switchApp")}
                      aria-expanded={showSwitcher()}
                      onClick={() => {
                        const next = !showSwitcher();
                        setShowSwitcher(next);
                        if (next) {
                          control.setAppFilter("");
                          if (control.selected() === grant().app.id) control.setSelected("");
                          void control.refreshApps();
                        }
                      }}
                    >
                      <ArrowLeftRight size={12} />
                    </button>
                  </div>
                </div>

                {/* Inline App Switcher */}
                <Show when={showSwitcher()}>
                  <div class="rounded-lg bg-[var(--bg-inset)]/70 p-2">
                    <AppPicker />
                  </div>
                </Show>

                {/* Compact Action Row */}
                <div class="flex flex-wrap gap-1.5">
                  <button
                    type="button"
                    class="inline-flex items-center gap-1 rounded-md bg-[var(--accent-surface)] px-2 py-1 text-[11.5px] font-medium text-[var(--accent-primary)] hover:opacity-90 disabled:opacity-40 cursor-pointer"
                    disabled={control.busy() || grant().paused}
                    onClick={() => void control.inspect(true)}
                  >
                    <Camera size={12} />
                    <span>{t(control.busy() ? "computer.capturing" : "computer.inspect")}</span>
                  </button>
                  <button
                    type="button"
                    class={btnBase}
                    disabled={control.busy() || grant().paused}
                    onClick={() => {
                      setShowElements(true);
                      void control.inspect(false);
                    }}
                  >
                    <ListTree size={12} />
                    <span>{t("computer.inspectElements")}</span>
                  </button>
                  <button
                    type="button"
                    class={btnBase}
                    onClick={() => void control.updateAccess("pause")}
                  >
                    <Show when={grant().paused} fallback={<Pause size={12} />}>
                      <Play size={12} />
                    </Show>
                    <span>{t(grant().paused ? "computer.resume" : "computer.pause")}</span>
                  </button>
                  <button
                    type="button"
                    class={btnBase}
                    onClick={() => void control.updateAccess("revoke")}
                  >
                    <XCircle size={12} />
                    <span>{t("computer.revoke")}</span>
                  </button>
                </div>

                {/* Single-Row Continuous Input Toggle */}
                <div class="border-t border-[var(--border-subtle)]/50 pt-2 space-y-1">
                  <label class="flex items-center justify-between gap-2 cursor-pointer select-none">
                    <span
                      class={`inline-flex items-center gap-1 text-[11.5px] ${
                        grant().autoApprove
                          ? "font-medium text-[var(--accent-primary)]"
                          : "text-[var(--text-secondary)]"
                      }`}
                      title={t("computer.autoApproveHint")}
                    >
                      <span>{t("computer.autoApprove")}</span>
                      <Info size={11} class="text-[var(--text-faint)] shrink-0" />
                    </span>
                    <input
                      type="checkbox"
                      role="switch"
                      checked={Boolean(grant().autoApprove)}
                      disabled={grant().paused || control.busy()}
                      onChange={() => void control.toggleAutoApprove()}
                      class="h-3.5 w-3.5 accent-[var(--accent-primary)] cursor-pointer disabled:cursor-not-allowed"
                    />
                  </label>
                  <Show when={grant().autoApprove}>
                    <p class="text-[10.5px] text-[var(--text-muted)]">
                      {t("computer.autoApproveActiveNote")}
                    </p>
                  </Show>
                </div>
              </div>
            )}
          </Show>
        </Show>

        {/* Error Banner */}
        <Show when={control.error()}>
          <p
            role="alert"
            class="rounded-lg border border-[var(--danger-border)] bg-[var(--danger-surface)] p-2 break-words text-[11.5px] text-[var(--danger-text)]"
          >
            {control.error()}
          </p>
        </Show>

        {/* Copied Toast Pill */}
        <Show when={control.copiedValue()}>
          <div class="inline-flex items-center gap-1.5 rounded-md bg-[var(--ok-surface)] px-2 py-0.5 font-mono text-[11px] text-[var(--ok-text)]">
            <Check size={11} />
            <span>{t("computer.copiedValue", { value: control.copiedValue() })}</span>
          </div>
        </Show>

        {/* Window Snapshot & Collapsible Element Inspector */}
        <Show when={control.snapshot()}>
          {(snapshot) => (
            <ComputerSnapshotView
              snapshot={snapshot()}
              activeWindow={control.activeWindow()}
              windowID={control.windowID()}
              onWindowChange={control.setWindowID}
              cursorPoint={control.cursorPoint()}
              onCursorPoint={control.setCursorPoint}
              hoveredElement={control.hoveredElement()}
              hoveredElementID={control.hoveredElementID()}
              onHoveredElementID={control.setHoveredElementID}
              elementFilter={control.elementFilter()}
              onElementFilter={control.setElementFilter}
              filteredElements={control.filteredElements()}
              showElements={showElements()}
              onToggleElements={() => {
                const next = !showElements();
                setShowElements(next);
                if (!next) control.setHoveredElementID(null);
              }}
              onCopy={control.copyToClipboard}
            />
          )}
        </Show>
      </div>
    </div>
  );
}
