import { createMemo, For, Show } from "solid-js";
import {
  Camera,
  Check,
  Copy,
  Crosshair,
  Lock,
  Maximize2,
  Monitor,
  Pause,
  Play,
  RefreshCw,
  Search,
  ShieldAlert,
  ShieldCheck,
  Square,
  XCircle,
} from "lucide-solid";
import {
  createComputerController,
  openImagePreview,
  resolveElementWindowBounds,
} from "../../store/workspace";
import { t } from "../../i18n";

export default function ComputerInspector() {
  const control = createComputerController();
  const btnBase =
    "inline-flex items-center gap-1.5 rounded-md border border-[var(--border-subtle)] bg-[var(--bg-panel)] px-2.5 py-1 text-[11.5px] font-medium text-[var(--text-secondary)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-hover)] disabled:opacity-40 disabled:cursor-not-allowed transition-colors cursor-pointer";
  const ready = () =>
    Boolean(
      control.permissions()?.supported &&
        control.permissions()?.accessibility &&
        control.permissions()?.screenRecording
    );

  const selectedApp = createMemo(() =>
    control.apps().find((app) => app.id === control.selected())
  );

  const hoveredBoxStyle = createMemo(() => {
    const el = control.hoveredElement();
    const win = control.activeWindow();
    if (!el || !win || win.bounds.width <= 0 || win.bounds.height <= 0) return null;
    const wb = resolveElementWindowBounds(el, win.bounds);
    if (!wb || wb.width <= 0 || wb.height <= 0) return null;
    const left = Math.max(0, Math.min(100, (wb.x / win.bounds.width) * 100));
    const top = Math.max(0, Math.min(100, (wb.y / win.bounds.height) * 100));
    const width = Math.max(0.5, Math.min(100 - left, (wb.width / win.bounds.width) * 100));
    const height = Math.max(0.5, Math.min(100 - top, (wb.height / win.bounds.height) * 100));
    return {
      left: `${left}%`,
      top: `${top}%`,
      width: `${width}%`,
      height: `${height}%`,
    };
  });

  const handlePreviewMouseMove = (e: MouseEvent & { currentTarget: HTMLDivElement }) => {
    const rect = e.currentTarget.getBoundingClientRect();
    if (rect.width <= 0 || rect.height <= 0) return;
    const win = control.activeWindow();
    const snap = control.snapshot();
    const targetW = win?.bounds.width || snap?.width || rect.width;
    const targetH = win?.bounds.height || snap?.height || rect.height;
    const x = Math.max(
      0,
      Math.min(Math.round(targetW - 1), Math.round(((e.clientX - rect.left) / rect.width) * targetW))
    );
    const y = Math.max(
      0,
      Math.min(Math.round(targetH - 1), Math.round(((e.clientY - rect.top) / rect.height) * targetH))
    );
    control.setCursorPoint({ x, y });
  };

  return (
    <div class="flex h-full min-h-0 flex-col text-xs text-[var(--text-primary)]">
      {/* Top Toolbar */}
      <div class="flex shrink-0 items-center justify-between gap-2 border-b border-[var(--border-subtle)] bg-[var(--bg-panel)]/60 px-3 py-2">
        <div class="flex items-center gap-2 min-w-0">
          <Monitor size={14} class="text-[var(--accent-primary)] shrink-0" />
          <span class="font-semibold text-[12.5px] truncate">{t("computer.title")}</span>
        </div>
        <button
          type="button"
          class="inline-flex items-center gap-1 rounded-md border border-[var(--danger-border)] bg-[var(--danger-surface)] px-2.5 py-1 text-[11px] font-medium text-[var(--danger-text)] hover:opacity-90 transition-opacity cursor-pointer shrink-0"
          onClick={() => void control.updateAccess("stop")}
        >
          <Square size={10} class="fill-current" />
          <span>{t("computer.stopAll")}</span>
        </button>
      </div>

      {/* Main Content */}
      <div class="min-h-0 flex-1 space-y-3 overflow-y-auto p-3">
        <p class="leading-relaxed text-[11.5px] text-[var(--text-muted)]">
          {t("computer.foregroundHint")}
        </p>

        {/* System Permissions Card */}
        <Show when={control.permissions()}>
          {(permission) => (
            <div class="rounded-lg border border-[var(--border-subtle)] bg-[var(--bg-panel)]/50 p-2.5 space-y-2">
              <div class="flex flex-wrap items-center justify-between gap-2">
                <span class="inline-flex items-center gap-1.5 font-medium text-[11.5px] text-[var(--text-secondary)]">
                  <Show
                    when={ready()}
                    fallback={<ShieldAlert size={13} class="text-[var(--warn-text)] shrink-0" />}
                  >
                    <ShieldCheck size={13} class="text-[var(--ok-text)] shrink-0" />
                  </Show>
                  <span>
                    {permission().supported ? t("computer.systemStatus") : t("computer.unsupported")}
                  </span>
                </span>
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
              </div>

              <Show when={permission().platform === "darwin" && !ready()}>
                <div class="flex flex-wrap gap-2 pt-0.5">
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
              </Show>
              <Show when={permission().platform === "windows"}>
                <p class="text-[11px] leading-relaxed text-[var(--text-muted)]">
                  {t("computer.windowsHint")}
                </p>
              </Show>
            </div>
          )}
        </Show>

        {/* Session App Grant Controls */}
        <Show
          when={control.scope()}
          fallback={
            <div class="rounded-lg border border-[var(--border-subtle)] bg-[var(--bg-inset)] p-3 text-center text-[var(--text-muted)]">
              {t("computer.selectSession")}
            </div>
          }
        >
          <div class="rounded-lg border border-[var(--border-subtle)] bg-[var(--bg-inset)]/70 p-2.5 text-[11.5px] leading-relaxed text-[var(--text-secondary)]">
            {t("computer.disclosure")}
          </div>

          <div class="rounded-lg border border-[var(--border-subtle)] bg-[var(--bg-panel)]/50 p-2.5 space-y-2">
            <Show when={control.apps().length > 6}>
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
                  class="w-full rounded-md border border-[var(--border-subtle)] bg-[var(--bg-panel)] pl-7 pr-2.5 py-1 text-[11.5px] text-[var(--text-primary)] placeholder:text-[var(--text-faint)] outline-none focus:border-[var(--accent-border)]"
                />
              </div>
            </Show>

            <div class="flex items-center gap-2">
              <select
                class="min-w-0 flex-1 rounded-md border border-[var(--border-subtle)] bg-[var(--bg-panel)] px-2 py-1.5 text-[12px] text-[var(--text-primary)] outline-none focus:border-[var(--accent-border)]"
                value={control.selected()}
                aria-label={t("computer.targetApp")}
                onChange={(event) => control.setSelected(event.currentTarget.value)}
              >
                <option value="">{t("computer.targetApp")}</option>
                <For each={control.filteredApps()}>
                  {(app) => (
                    <option value={app.id} disabled={app.blocked}>
                      {app.name} (PID {app.pid})
                      {app.blocked ? ` · ${t("computer.protectedApp")}` : ""}
                    </option>
                  )}
                </For>
              </select>
              <button
                type="button"
                class={btnBase}
                onClick={() => void control.refreshApps()}
                title={t("computer.refreshApps")}
              >
                <RefreshCw size={12} />
                <span>{t("computer.refreshApps")}</span>
              </button>
            </div>

            <Show when={selectedApp()}>
              {(app) => (
                <div class="rounded bg-[var(--bg-inset)] px-2 py-1.5 space-y-0.5 font-mono text-[10.5px] text-[var(--text-muted)]">
                  <Show when={app().bundleID}>
                    <div class="truncate text-[var(--text-secondary)]">
                      {app().bundleID} · PID {app().pid}
                    </div>
                  </Show>
                  <p class="break-all">{app().path}</p>
                </div>
              )}
            </Show>

            <button
              type="button"
              class="w-full inline-flex items-center justify-center gap-1.5 rounded-md border border-[var(--accent-border)] bg-[var(--accent-surface)] px-3 py-1.5 text-[12px] font-medium text-[var(--accent-primary)] hover:opacity-90 disabled:opacity-40 disabled:cursor-not-allowed transition-opacity cursor-pointer"
              disabled={!ready() || !control.selected() || control.busy()}
              onClick={() => void control.authorize()}
            >
              <ShieldCheck size={13} />
              <span>{t("computer.authorize")}</span>
            </button>
          </div>
        </Show>

        {/* Active Grant Card */}
        <Show when={control.grant()}>
          {(grant) => (
            <div class="space-y-2.5 rounded-lg border border-[var(--accent-border)] bg-[var(--accent-surface)]/35 p-3">
              <div class="flex flex-wrap items-center justify-between gap-2">
                <div class="flex items-center gap-2 min-w-0">
                  <span
                    class={`inline-block h-2 w-2 rounded-full shrink-0 ${
                      grant().paused ? "bg-[var(--warn-text)]" : "bg-[var(--ok-text)]"
                    }`}
                  />
                  <p role="status" class="font-semibold text-[12px] truncate">
                    {grant().app.name} ·{" "}
                    {t(grant().paused ? "computer.paused" : "computer.authorized")}
                  </p>
                </div>
                <Show when={grant().lastAction}>
                  <span class="rounded bg-[var(--bg-panel)]/80 px-1.5 py-0.5 font-mono text-[10.5px] text-[var(--text-muted)]">
                    {t("computer.lastActionLabel", { action: grant().lastAction ?? "" })}
                  </span>
                </Show>
              </div>

              <div class="flex flex-wrap gap-1.5">
                <button
                  type="button"
                  class="inline-flex items-center gap-1.5 rounded-md border border-[var(--accent-border)] bg-[var(--accent-surface)] px-2.5 py-1 text-[11.5px] font-medium text-[var(--accent-primary)] hover:opacity-90 disabled:opacity-40 cursor-pointer"
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
                  onClick={() => void control.inspect(false)}
                >
                  <RefreshCw size={12} />
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

              <p class="text-[11px] leading-relaxed text-[var(--text-muted)]">
                {t("computer.nextStep")}
              </p>
            </div>
          )}
        </Show>

        {/* Error Banner */}
        <Show when={control.error()}>
          <p
            role="alert"
            class="rounded-lg border border-[var(--danger-border)] bg-[var(--danger-surface)] p-2.5 break-words text-[11.5px] text-[var(--danger-text)]"
          >
            {control.error()}
          </p>
        </Show>

        {/*Copied Toast Pill */}
        <Show when={control.copiedValue()}>
          <div class="inline-flex items-center gap-1.5 rounded-md border border-[var(--ok-border)] bg-[var(--ok-surface)] px-2.5 py-1 font-mono text-[11px] text-[var(--ok-text)]">
            <Check size={11} />
            <span>{t("computer.copiedValue", { value: control.copiedValue() })}</span>
          </div>
        </Show>

        {/* Window Snapshot & Interactive Element Inspector */}
        <Show when={control.snapshot()}>
          {(snapshot) => (
            <div class="space-y-3">
              {/* Target Window Selector */}
              <div class="flex items-center gap-2">
                <select
                  class="min-w-0 flex-1 rounded-md border border-[var(--border-subtle)] bg-[var(--bg-panel)] px-2 py-1.5 text-[12px] text-[var(--text-primary)] outline-none focus:border-[var(--accent-border)]"
                  aria-label={t("computer.targetWindow")}
                  value={control.windowID()}
                  onChange={(event) => control.setWindowID(event.currentTarget.value)}
                >
                  <For each={snapshot().windows}>
                    {(window) => (
                      <option value={window.id}>
                        {window.title || window.id} (
                        {Math.round(window.bounds.width)}×{Math.round(window.bounds.height)})
                      </option>
                    )}
                  </For>
                </select>
                <Show when={control.activeWindow()}>
                  {(win) => (
                    <span class="shrink-0 rounded bg-[var(--bg-inset)] px-2 py-1 font-mono text-[10.5px] text-[var(--text-muted)]">
                      {t("computer.windowBoundsLabel", {
                        width: Math.round(win().bounds.width),
                        height: Math.round(win().bounds.height),
                      })}
                    </span>
                  )}
                </Show>
              </div>

              {/* Screenshot Preview with Live Coordinate Readout & Element Overlay */}
              <Show when={snapshot().image}>
                <div class="space-y-1.5">
                  <div
                    class="group/preview relative overflow-hidden rounded-lg border border-[var(--border-subtle)] bg-[var(--bg-inset)] cursor-crosshair select-none"
                    onMouseMove={handlePreviewMouseMove}
                    onMouseLeave={() => control.setCursorPoint(null)}
                    onClick={() => {
                      const pt = control.cursorPoint();
                      if (pt) control.copyToClipboard(`(${pt.x}, ${pt.y})`);
                    }}
                    title={t("computer.clickToCopyCoords")}
                  >
                    <img
                      class="block h-auto w-full"
                      alt={t("computer.preview")}
                      src={`data:image/jpeg;base64,${snapshot().image}`}
                    />

                    {/* Highlighted Element Bounding Box */}
                    <Show when={hoveredBoxStyle()}>
                      {(style) => (
                        <div
                          class="pointer-events-none absolute border-2 border-[var(--accent-primary)] bg-[var(--accent-primary)]/15 rounded-sm transition-all duration-75"
                          style={style()}
                        >
                          <Show when={control.hoveredElement()}>
                            {(el) => (
                              <span class="absolute -top-5 left-0 whitespace-nowrap rounded bg-[var(--accent-primary)] px-1.5 py-0.5 font-mono text-[10px] font-semibold text-white shadow">
                                #{el().id} {el().role}
                              </span>
                            )}
                          </Show>
                        </div>
                      )}
                    </Show>

                    {/* Fullscreen Zoom Button */}
                    <button
                      type="button"
                      onClick={(e) => {
                        e.stopPropagation();
                        openImagePreview({
                          src: `data:image/jpeg;base64,${snapshot().image}`,
                          alt: t("computer.preview"),
                        });
                      }}
                      title={t("computer.zoomPreview")}
                      class="absolute right-2 top-2 inline-flex h-6 w-6 items-center justify-center rounded-md bg-black/60 text-white opacity-0 group-hover/preview:opacity-100 hover:bg-black/80 transition-opacity cursor-pointer"
                    >
                      <Maximize2 size={12} />
                    </button>
                  </div>

                  {/* Coordinate Bar */}
                  <div class="flex items-center justify-between text-[11px] text-[var(--text-muted)]">
                    <span class="inline-flex items-center gap-1 font-mono">
                      <Crosshair size={11} class="text-[var(--accent-primary)]" />
                      <Show
                        when={control.cursorPoint()}
                        fallback={<span>{t("computer.clickToCopyCoords")}</span>}
                      >
                        {(pt) => (
                          <span>
                            {t("computer.cursorCoords", { x: pt().x, y: pt().y })}
                          </span>
                        )}
                      </Show>
                    </span>
                  </div>
                </div>
              </Show>

              {/* Accessible Elements Section */}
              <div class="rounded-lg border border-[var(--border-subtle)] bg-[var(--bg-panel)]/50 p-2.5 space-y-2">
                <div class="flex items-center justify-between gap-2">
                  <p class="font-medium text-[11.5px] text-[var(--text-secondary)]">
                    {t("computer.elementCount", { count: snapshot().elements?.length || 0 })}
                  </p>
                </div>

                <Show when={(snapshot().elements?.length || 0) > 0}>
                  <div class="relative">
                    <Search
                      size={12}
                      class="pointer-events-none absolute left-2.5 top-1/2 -translate-y-1/2 text-[var(--text-faint)]"
                    />
                    <input
                      type="text"
                      value={control.elementFilter()}
                      onInput={(e) => control.setElementFilter(e.currentTarget.value)}
                      placeholder={t("computer.filterElementsPlaceholder")}
                      class="w-full rounded-md border border-[var(--border-subtle)] bg-[var(--bg-panel)] pl-7 pr-2.5 py-1 text-[11.5px] text-[var(--text-primary)] placeholder:text-[var(--text-faint)] outline-none focus:border-[var(--accent-border)]"
                    />
                  </div>

                  <div class="max-h-60 space-y-1 overflow-y-auto pr-0.5">
                    <Show
                      when={control.filteredElements().length > 0}
                      fallback={
                        <div class="py-3 text-center text-[11px] text-[var(--text-faint)]">
                          {t("computer.noMatchingElements")}
                        </div>
                      }
                    >
                      <For each={control.filteredElements()}>
                        {(el) => {
                          const wb = () =>
                            resolveElementWindowBounds(el, control.activeWindow()?.bounds);
                          return (
                            <div
                              onMouseEnter={() => control.setHoveredElementID(el.id)}
                              onMouseLeave={() => {
                                if (control.hoveredElementID() === el.id) {
                                  control.setHoveredElementID(null);
                                }
                              }}
                              onClick={() => control.copyToClipboard(`elementID: ${el.id}`)}
                              title={t("computer.copyElementTarget", { id: el.id })}
                              class={`flex items-center justify-between gap-2 rounded-md border px-2 py-1 text-[11px] transition-colors cursor-pointer ${
                                control.hoveredElementID() === el.id
                                  ? "border-[var(--accent-border)] bg-[var(--accent-surface)]/40"
                                  : "border-transparent bg-[var(--bg-inset)]/60 hover:bg-[var(--bg-hover)]"
                              }`}
                            >
                              <div class="flex items-center gap-1.5 min-w-0 flex-1">
                                <span class="shrink-0 rounded bg-[var(--bg-panel)] px-1.5 py-0.5 font-mono text-[10px] font-semibold text-[var(--accent-primary)]">
                                  #{el.id}
                                </span>
                                <span class="shrink-0 font-mono text-[10.5px] text-[var(--text-muted)]">
                                  {el.role}
                                </span>
                                <Show
                                  when={!el.secure}
                                  fallback={
                                    <span class="inline-flex items-center gap-1 rounded bg-[var(--warn-surface)] px-1.5 py-0.5 text-[10px] font-medium text-[var(--warn-text)]">
                                      <Lock size={9} />
                                      <span>{t("computer.secureElement")}</span>
                                    </span>
                                  }
                                >
                                  <Show when={el.name || el.value}>
                                    <span class="truncate text-[var(--text-primary)]">
                                      {el.name || el.value}
                                    </span>
                                  </Show>
                                </Show>
                              </div>
                              <div class="flex items-center gap-1.5 shrink-0 font-mono text-[10px] text-[var(--text-faint)]">
                                <Show when={wb()}>
                                  {(b) => (
                                    <span>
                                      ({Math.round(b().x + b().width / 2)},{" "}
                                      {Math.round(b().y + b().height / 2)})
                                    </span>
                                  )}
                                </Show>
                                <Copy size={10} class="opacity-60" />
                              </div>
                            </div>
                          );
                        }}
                      </For>
                    </Show>
                  </div>
                </Show>
              </div>
            </div>
          )}
        </Show>
      </div>
    </div>
  );
}
