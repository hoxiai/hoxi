import { For, onCleanup, onMount, Show } from "solid-js";
import { KeyRound, RefreshCw } from "lucide-solid";
import { t } from "../../i18n";
import { createAppGrantSettings } from "../../store/workspace";
import { HelpTip, SettingGroup } from "./setting-controls";

const btnSecondary =
  "hoxi-press inline-flex items-center gap-1 rounded-xl border border-[var(--border-subtle)] bg-[var(--bg-panel)] px-3 py-1.5 text-[12px] font-medium text-[var(--text-secondary)] hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)] disabled:opacity-45 transition-colors cursor-pointer";
const btnPrimary =
  "hoxi-press inline-flex items-center gap-1 rounded-xl bg-[var(--accent-primary)] px-3 py-1.5 text-[12px] font-medium text-[var(--accent-foreground)] hover:opacity-90 disabled:opacity-45 transition-opacity cursor-pointer";
const btnDanger =
  "hoxi-press inline-flex items-center gap-1 rounded-xl border border-[var(--danger-border)] bg-[var(--danger-surface)] px-3 py-1.5 text-[12px] font-medium text-[var(--danger-text)] hover:opacity-90 disabled:opacity-45 transition-opacity cursor-pointer";

export function AppGrantsTab() {
  const model = createAppGrantSettings();
  onMount(() => void model.refresh());
  onCleanup(model.dispose);

  return (
    <div class="space-y-6 text-[var(--text-primary)]">
      {/* Header matching other Settings tabs */}
      <div class="flex flex-wrap items-center justify-between gap-3 rounded-2xl border border-[var(--border-subtle)] bg-[var(--bg-elevated)]/50 px-4 py-3">
        <div class="flex items-center gap-3 min-w-0">
          <div class="flex h-8 w-8 shrink-0 items-center justify-center rounded-xl bg-[var(--accent-surface)] text-[var(--accent-primary)]">
            <KeyRound size={16} />
          </div>
          <div class="flex items-center gap-2 min-w-0">
            <h3 class="text-[13px] font-semibold text-[var(--text-primary)] truncate">
              {t("settings.tabAppGrants")}
            </h3>
            <HelpTip text={t("settings.appGrantDescription")} />
          </div>
        </div>
        <button
          type="button"
          class={btnSecondary}
          disabled={model.busy()}
          onClick={() => void model.refresh()}
        >
          <RefreshCw size={12} class={model.busy() ? "animate-spin" : ""} />
          <span>{t("settings.appGrantRefresh")}</span>
        </button>
      </div>

      <Show when={model.error()}>
        <p
          role="alert"
          class="rounded-xl border border-[var(--danger-border)] bg-[var(--danger-surface)] px-3.5 py-2 text-[12px] text-[var(--danger-text)]"
        >
          {model.error()}
        </p>
      </Show>

      {/* Pending Connection Requests (shown only when present) */}
      <Show when={model.pending().length > 0}>
        <SettingGroup title={t("settings.appGrantPending")}>
          <div class="divide-y divide-[var(--border-subtle)]/60">
            <For each={model.pending()}>
              {(item) => (
                <div class="p-4 space-y-2.5">
                  <div class="min-w-0">
                    <p class="text-[13px] font-medium break-words" dir="auto">
                      {item.name}{" "}
                      <code class="text-[11.5px] font-normal text-[var(--text-muted)]">
                        ({item.appId})
                      </code>
                    </p>
                    <Show when={item.redirectUri}>
                      <p class="mt-0.5 break-all font-mono text-[11px] text-[var(--text-faint)]" dir="ltr">
                        {item.redirectUri}
                      </p>
                    </Show>
                  </div>
                  <div class="flex flex-wrap gap-2">
                    <For each={item.scopes}>
                      {(scope) => (
                        <label class="inline-flex items-center gap-1.5 rounded-lg border border-[var(--border-subtle)] bg-[var(--bg-panel)] px-2 py-1 text-[11.5px] cursor-pointer">
                          <input
                            type="checkbox"
                            checked={model.selected()[item.requestId]?.includes(scope) ?? false}
                            disabled={model.busy()}
                            onChange={(event) =>
                              model.toggleScope(item.requestId, scope, event.currentTarget.checked)
                            }
                            class="accent-[var(--accent-primary)]"
                          />
                          <code class="font-mono">{scope}</code>
                        </label>
                      )}
                    </For>
                  </div>
                  <div class="flex flex-wrap gap-2 pt-0.5">
                    <button
                      type="button"
                      class={btnPrimary}
                      disabled={model.busy() || !model.selected()[item.requestId]?.length}
                      onClick={() => void model.decideConnect(item.requestId, true)}
                    >
                      {t("settings.appGrantApprove")}
                    </button>
                    <button
                      type="button"
                      class={btnSecondary}
                      disabled={model.busy()}
                      onClick={() => void model.decideConnect(item.requestId, false)}
                    >
                      {t("settings.appGrantDeny")}
                    </button>
                  </div>
                </div>
              )}
            </For>
          </div>
        </SettingGroup>
      </Show>

      {/* Pending One-Time Approvals (shown only when present) */}
      <Show when={model.approvals().length > 0}>
        <SettingGroup title={t("settings.appGrantApprovals")}>
          <div class="divide-y divide-[var(--border-subtle)]/60">
            <For each={model.approvals()}>
              {(item) => (
                <div class="p-4 space-y-2">
                  <div class="flex items-center justify-between gap-2">
                    <p class="text-[12px] text-[var(--text-muted)] break-words" dir="auto">
                      {item.appName} <code class="text-[11px]">({item.appId})</code>
                    </p>
                    <span class="shrink-0 rounded-md bg-[var(--warn-surface)] px-1.5 py-0.5 text-[11px] font-medium text-[var(--warn-text)]">
                      {item.riskLevel}
                    </span>
                  </div>
                  <h4 class="text-[13px] font-medium break-words" dir="auto">
                    {item.title}
                  </h4>
                  <p class="text-[12px] leading-relaxed text-[var(--text-secondary)] whitespace-pre-wrap break-words" dir="auto">
                    {item.summary}
                  </p>
                  <Show when={Object.keys(item.details ?? {}).length > 0}>
                    <div class="rounded-lg bg-[var(--bg-inset)] px-2.5 py-1.5 space-y-0.5 font-mono text-[11px] text-[var(--text-muted)]">
                      <For each={Object.entries(item.details ?? {})}>
                        {([key, value]) => (
                          <p class="break-words" dir="auto">
                            {key}: {value}
                          </p>
                        )}
                      </For>
                    </div>
                  </Show>
                  <div class="flex flex-wrap gap-2 pt-0.5">
                    <button
                      type="button"
                      class={btnPrimary}
                      disabled={model.busy()}
                      onClick={() => void model.decideApproval(item.approvalId, true)}
                    >
                      {t("settings.appGrantAllowOnce")}
                    </button>
                    <button
                      type="button"
                      class={btnSecondary}
                      disabled={model.busy()}
                      onClick={() => void model.decideApproval(item.approvalId, false)}
                    >
                      {t("settings.appGrantDeny")}
                    </button>
                  </div>
                </div>
              )}
            </For>
          </div>
        </SettingGroup>
      </Show>

      {/* Active Grants */}
      <SettingGroup title={t("settings.appGrantActive")}>
        <Show
          when={model.grants().length > 0}
          fallback={
            <p class="px-5 py-4 text-[12.5px] text-[var(--text-muted)]">
              {t("settings.appGrantEmpty")}
            </p>
          }
        >
          <div class="divide-y divide-[var(--border-subtle)]/60">
            <For each={model.grants()}>
              {(item) => (
                <div class="flex flex-wrap items-center justify-between gap-3 px-4 py-3">
                  <div class="min-w-0 space-y-0.5">
                    <p class="text-[13px] font-medium break-words" dir="auto">
                      {item.name}{" "}
                      <code class="text-[11.5px] font-normal text-[var(--text-muted)]">
                        ({item.appId})
                      </code>
                    </p>
                    <p class="break-words font-mono text-[11px] text-[var(--text-faint)]">
                      {item.scopes.join(", ")}
                    </p>
                  </div>
                  <button
                    type="button"
                    class={btnDanger}
                    disabled={model.busy()}
                    onClick={() => void model.revoke(item.id)}
                  >
                    {t("settings.appGrantRevoke")}
                  </button>
                </div>
              )}
            </For>
          </div>
        </Show>
      </SettingGroup>
    </div>
  );
}
