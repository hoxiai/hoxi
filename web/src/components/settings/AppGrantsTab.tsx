import { For, onCleanup, onMount, Show } from "solid-js";
import { t } from "../../i18n";
import { createAppGrantSettings } from "../../store/workspace";

const card = "rounded-lg border border-[var(--border-default)] p-3 space-y-2";
const button = "rounded-md border border-[var(--border-default)] px-3 py-1.5 text-xs disabled:opacity-50";

export function AppGrantsTab() {
  const model = createAppGrantSettings();
  onMount(() => void model.refresh());
  onCleanup(model.dispose);
  return (
    <section class="space-y-4">
      <header class="flex items-start justify-between gap-3">
        <div>
          <h2 class="font-semibold">{t("settings.tabAppGrants")}</h2>
          <p class="text-sm text-[var(--text-secondary)]">{t("settings.appGrantDescription")}</p>
        </div>
        <button type="button" class={button} disabled={model.busy()} onClick={() => void model.refresh()}>{t("settings.appGrantRefresh")}</button>
      </header>
      <Show when={model.error()}><p role="alert" class="text-sm text-red-500">{model.error()}</p></Show>
      <h3 class="text-sm font-semibold">{t("settings.appGrantPending")}</h3>
      <For each={model.pending()} fallback={<p class="text-xs opacity-60">{t("settings.appGrantEmpty")}</p>}>{(item) => (
        <div class={card}>
          <p class="font-medium break-words" dir="auto">{item.name} <code class="text-xs">({item.appId})</code></p>
          <Show when={item.redirectUri}><p class="break-all text-xs" dir="ltr">{item.redirectUri}</p></Show>
          <div class="flex flex-wrap gap-2">
            <For each={item.scopes}>{(scope) => (
              <label class="flex gap-1 items-center text-xs">
                <input type="checkbox" checked={model.selected()[item.requestId]?.includes(scope) ?? false} disabled={model.busy()}
                  onChange={(event) => model.toggleScope(item.requestId, scope, event.currentTarget.checked)} />
                <code>{scope}</code>
              </label>
            )}</For>
          </div>
          <div class="flex gap-2">
            <button type="button" class={button} disabled={model.busy()} onClick={() => void model.decideConnect(item.requestId, false)}>{t("settings.appGrantDeny")}</button>
            <button type="button" class={button} disabled={model.busy() || !model.selected()[item.requestId]?.length}
              onClick={() => void model.decideConnect(item.requestId, true)}>{t("settings.appGrantApprove")}</button>
          </div>
        </div>
      )}</For>
      <h3 class="text-sm font-semibold">{t("settings.appGrantApprovals")}</h3>
      <For each={model.approvals()} fallback={<p class="text-xs opacity-60">{t("settings.appGrantEmpty")}</p>}>{(item) => (
        <div class={card}>
          <p class="break-words" dir="auto">{item.appName} <code class="text-xs">({item.appId})</code> · {item.riskLevel}</p>
          <h4 class="font-medium break-words" dir="auto">{item.title}</h4>
          <p class="text-sm whitespace-pre-wrap break-words" dir="auto">{item.summary}</p>
          <For each={Object.entries(item.details ?? {})}>{([key, value]) => <p class="text-xs break-words" dir="auto">{key}: {value}</p>}</For>
          <div class="flex gap-2">
            <button type="button" class={button} disabled={model.busy()} onClick={() => void model.decideApproval(item.approvalId, false)}>{t("settings.appGrantDeny")}</button>
            <button type="button" class={button} disabled={model.busy()} onClick={() => void model.decideApproval(item.approvalId, true)}>{t("settings.appGrantAllowOnce")}</button>
          </div>
        </div>
      )}</For>
      <h3 class="text-sm font-semibold">{t("settings.appGrantActive")}</h3>
      <For each={model.grants()} fallback={<p class="text-xs opacity-60">{t("settings.appGrantEmpty")}</p>}>{(item) => (
        <div class={card}>
          <p class="break-words" dir="auto">{item.name} <code class="text-xs">({item.appId})</code></p>
          <p class="break-words text-xs"><code>{item.scopes.join(", ")}</code></p>
          <button type="button" class={button} disabled={model.busy()} onClick={() => void model.revoke(item.id)}>{t("settings.appGrantRevoke")}</button>
        </div>
      )}</For>
    </section>
  );
}
