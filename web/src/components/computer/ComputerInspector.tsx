import { For, Show } from "solid-js";
import { createComputerController } from "../../store/workspace";
import { t } from "../../i18n";

export default function ComputerInspector() {
  const control = createComputerController();
  const button = "rounded border border-[var(--border-subtle)] px-2 py-1 text-xs hover:bg-[var(--bg-hover)] disabled:opacity-40 cursor-pointer";
  const ready = () => control.permissions()?.supported && control.permissions()?.accessibility && control.permissions()?.screenRecording;
  return <div class="flex h-full min-h-0 flex-col text-xs text-[var(--text-primary)]">
    <div class="flex shrink-0 items-center justify-between gap-2 border-b border-[var(--border-subtle)] p-2">
      <span class="font-medium">{t("computer.title")}</span>
      <button class={`${button} text-red-500`} onClick={() => void control.updateAccess("stop")}>{t("computer.stopAll")}</button>
    </div>
    <div class="min-h-0 flex-1 space-y-3 overflow-y-auto p-3">
      <p class="leading-relaxed text-[var(--text-muted)]">{t("computer.foregroundHint")}</p>
      <Show when={control.permissions()}>{permission => <div class="rounded border border-[var(--border-subtle)] p-2 space-y-2">
        <p>{permission().supported ? t("computer.systemStatus") : t("computer.unsupported")}</p>
        <div class="flex flex-wrap gap-2">
          <span>{t("computer.screenPermission")}: {t(permission().screenRecording ? "computer.ready" : "computer.required")}</span>
          <span>{t("computer.accessibilityPermission")}: {t(permission().accessibility ? "computer.ready" : "computer.required")}</span>
        </div>
        <Show when={permission().platform === "darwin"}>
          <div class="flex flex-wrap gap-2"><button class={button} disabled={control.busy() || permission().screenRecording} onClick={() => void control.permission("screen")}>{t("computer.requestScreen")}</button>
            <button class={button} disabled={control.busy() || permission().accessibility} onClick={() => void control.permission("accessibility")}>{t("computer.requestAccessibility")}</button></div>
          <p class="text-[var(--text-muted)]">{t("computer.systemPermissionHint")}</p>
        </Show>
        <Show when={permission().platform === "windows"}><p class="text-[var(--text-muted)]">{t("computer.windowsHint")}</p></Show>
      </div>}</Show>
      <Show when={control.scope()} fallback={<p>{t("computer.selectSession")}</p>}>
        <p class="rounded bg-[var(--bg-inset)] p-2 leading-relaxed">{t("computer.disclosure")}</p>
        <div class="flex gap-2">
          <select class="min-w-0 flex-1 rounded border border-[var(--border-subtle)] bg-[var(--bg-panel)] p-1" value={control.selected()} aria-label={t("computer.targetApp")} onChange={event => control.setSelected(event.currentTarget.value)}>
            <option value="">{t("computer.targetApp")}</option>
            <For each={control.apps()}>{app => <option value={app.id} disabled={app.blocked}>{app.name}{app.blocked ? ` · ${t("computer.protectedApp")}` : ""}</option>}</For>
          </select>
          <button class={button} onClick={() => void control.refreshApps()}>{t("computer.refreshApps")}</button>
        </div>
        <Show when={control.apps().find(app => app.id === control.selected())}>{app => <p class="break-all text-[11px] text-[var(--text-muted)]">{app().path}</p>}</Show>
        <button class={button} disabled={!ready() || !control.selected() || control.busy()} onClick={() => void control.authorize()}>{t("computer.authorize")}</button>
      </Show>
      <Show when={control.grant()}>{grant => <div class="space-y-2 rounded border border-[var(--accent-primary)] p-2">
        <p role="status">{grant().app.name} · {t(grant().paused ? "computer.paused" : "computer.authorized")}</p>
        <div class="flex flex-wrap gap-2"><button class={button} onClick={() => void control.updateAccess("pause")}>{t(grant().paused ? "computer.resume" : "computer.pause")}</button>
          <button class={button} onClick={() => void control.updateAccess("revoke")}>{t("computer.revoke")}</button>
          <button class={button} disabled={control.busy() || grant().paused} onClick={() => void control.inspect()}>{t(control.busy() ? "computer.capturing" : "computer.inspect")}</button></div>
        <p class="text-[var(--text-muted)]">{t("computer.nextStep")}</p>
      </div>}</Show>
      <Show when={control.error()}><p role="alert" class="break-words text-red-500">{control.error()}</p></Show>
      <Show when={control.snapshot()}>{snapshot => <div class="space-y-2">
        <select class="w-full rounded border border-[var(--border-subtle)] bg-[var(--bg-panel)] p-1" aria-label={t("computer.targetWindow")} value={control.windowID()} onChange={event => control.setWindowID(event.currentTarget.value)}>
          <For each={snapshot().windows}>{window => <option value={window.id}>{window.title || window.id}</option>}</For>
        </select>
        <Show when={snapshot().image}><img class="h-auto w-full rounded border border-[var(--border-subtle)]" alt={t("computer.preview")} src={`data:image/jpeg;base64,${snapshot().image}`} /></Show>
        <p class="text-[var(--text-muted)]">{t("computer.elementCount", { count: snapshot().elements?.length || 0 })}</p>
      </div>}</Show>
    </div>
  </div>;
}
