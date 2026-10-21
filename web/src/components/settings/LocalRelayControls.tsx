// web/src/components/settings/LocalRelayControls.tsx
// Local OpenAI-compatible relay: endpoint discovery plus hk-* key management.
// The relay listens on loopback only and is never opened over the GoPanel tunnel.
import { createSignal, For, onMount, Show } from "solid-js";
import { Check, Copy, KeyRound, Plus, ShieldAlert, Trash2 } from "lucide-solid";
import { t } from "../../i18n";
import {
  createLocalRelayKey,
  dismissCreatedRelayToken,
  loadLocalRelay,
  localRelay,
  revokeLocalRelayKey,
} from "../../store/workspace";
import { SettingGroup, SettingRow } from "./setting-controls";

const inputClass =
  "w-full rounded-xl border border-[var(--border-subtle)] bg-[var(--bg-panel)] px-3 py-2 text-[13px] text-[var(--text-primary)] placeholder:text-[var(--text-faint)] focus:border-[var(--accent-border)] focus:outline-none";
const buttonClass =
  "hoxi-press inline-flex items-center gap-1.5 rounded-xl border border-[var(--border-strong)] px-4 py-2 text-[13px] font-medium hover:bg-[var(--bg-hover)] disabled:opacity-50 cursor-pointer";

function formatTime(ms?: number): string {
  if (!ms) return "";
  try {
    return new Date(ms).toLocaleString();
  } catch {
    return "";
  }
}

function CopyField(props: { label: string; value: string }) {
  const [copied, setCopied] = createSignal(false);
  const onCopy = async () => {
    if (!props.value) return;
    try {
      await navigator.clipboard.writeText(props.value);
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    } catch {
      setCopied(false);
    }
  };
  return (
    <div class="space-y-1.5">
      <div class="text-[12px] font-medium text-[var(--text-secondary)]">{props.label}</div>
      <div class="flex items-center gap-2">
        <code class="min-w-0 flex-1 truncate rounded-lg border border-[var(--border-subtle)] bg-[var(--bg-panel)] px-3 py-2 font-mono text-[12px] text-[var(--text-primary)]">
          {props.value || "—"}
        </code>
        <button type="button" class={buttonClass} onClick={() => void onCopy()} disabled={!props.value}>
          {copied() ? <Check size={14} /> : <Copy size={14} />}
          <span>{copied() ? t("relay.copied") : t("relay.copy")}</span>
        </button>
      </div>
    </div>
  );
}

export function LocalRelaySettingsTab() {
  const [label, setLabel] = createSignal("");

  onMount(() => {
    void loadLocalRelay();
  });

  const onCreate = async (event: Event) => {
    event.preventDefault();
    const ok = await createLocalRelayKey(label());
    if (ok) setLabel("");
  };

  return (
    <div class="space-y-6">
      <div class="space-y-2">
        <h3 class="text-[15px] font-semibold text-[var(--text-primary)]">{t("relay.title")}</h3>
        <p class="max-w-3xl text-[13px] leading-relaxed text-[var(--text-secondary)]">
          {t("relay.intro")}
        </p>
      </div>

      <SettingGroup title={t("relay.endpointTitle")} tip={t("relay.endpointDesc")}>
        <div class="space-y-4 px-5 py-4">
          <CopyField label={t("relay.baseURL")} value={localRelay.baseURL} />
          <CopyField label={t("relay.chatURL")} value={localRelay.chatURL} />
          <CopyField label={t("relay.modelsURL")} value={localRelay.modelsURL} />
          <p class="text-[12px] text-[var(--text-muted)]">
            {t("relay.modelHint", { auto: "hoxi-auto" })}
          </p>
        </div>
        <SettingRow label={t("relay.loopbackOnly")} tip={t("relay.loopbackOnlyDesc")} icon={ShieldAlert}>
          <span class="rounded-lg border border-emerald-500/25 bg-emerald-500/10 px-2.5 py-1 text-[12px] font-medium text-emerald-500">
            127.0.0.1
          </span>
        </SettingRow>
      </SettingGroup>

      <SettingGroup title={t("relay.keysTitle")} tip={t("relay.keysDesc")}>
        <div class="space-y-4 px-5 py-4">
          <form class="flex flex-wrap items-center gap-2" onSubmit={(e) => void onCreate(e)}>
            <input
              class={`${inputClass} max-w-xs flex-1`}
              value={label()}
              placeholder={t("relay.keyLabelPlaceholder")}
              onInput={(e) => setLabel(e.currentTarget.value)}
            />
            <button type="submit" class={buttonClass} disabled={localRelay.busy}>
              <Plus size={14} />
              <span>{localRelay.busy ? t("relay.creating") : t("relay.createKey")}</span>
            </button>
          </form>

          <Show when={localRelay.createdToken}>
            <div class="space-y-2 rounded-xl border border-[var(--accent-border)] bg-[var(--accent-surface)] px-4 py-3">
              <div class="text-[13px] font-semibold text-[var(--accent-primary)]">
                {t("relay.createdTitle")}
              </div>
              <p class="text-[12px] text-[var(--text-secondary)]">{t("relay.createdDesc")}</p>
              <CopyField label={t("relay.createKey")} value={localRelay.createdToken} />
              <button type="button" class={buttonClass} onClick={dismissCreatedRelayToken}>
                {t("relay.dismiss")}
              </button>
            </div>
          </Show>

          <Show when={localRelay.error}>
            <p role="alert" class="text-[13px] text-red-500">
              {localRelay.error}
            </p>
          </Show>

          <Show
            when={localRelay.keys.length > 0}
            fallback={<p class="text-[13px] text-[var(--text-secondary)]">{t("relay.noKeys")}</p>}
          >
            <ul class="divide-y divide-[var(--border-subtle)]/60 rounded-xl border border-[var(--border-subtle)]">
              <For each={localRelay.keys}>
                {(key) => (
                  <li class="flex flex-wrap items-center justify-between gap-3 px-4 py-3">
                    <div class="flex min-w-0 items-center gap-3">
                      <KeyRound size={15} class="shrink-0 text-[var(--text-muted)]" />
                      <div class="min-w-0">
                        <div class="flex items-center gap-2">
                          <code class="font-mono text-[13px] text-[var(--text-primary)]">{key.prefix}</code>
                          <Show when={key.label}>
                            <span class="truncate text-[12px] text-[var(--text-muted)]">{key.label}</span>
                          </Show>
                        </div>
                        <div class="text-[11px] text-[var(--text-faint)]">
                          {t("relay.created")} {formatTime(key.createdAt)}
                          {" · "}
                          {key.lastUsed ? `${t("relay.lastUsed")} ${formatTime(key.lastUsed)}` : t("relay.neverUsed")}
                        </div>
                      </div>
                    </div>
                    <button
                      type="button"
                      class={buttonClass}
                      disabled={localRelay.busy}
                      onClick={() => {
                        if (confirm(t("relay.revokeConfirm"))) void revokeLocalRelayKey(key.id);
                      }}
                    >
                      <Trash2 size={14} />
                      <span>{t("relay.revoke")}</span>
                    </button>
                  </li>
                )}
              </For>
            </ul>
          </Show>
        </div>
      </SettingGroup>

      <SettingGroup title={t("relay.disclaimerTitle")}>
        <p class="px-5 py-4 text-[13px] leading-relaxed text-[var(--text-secondary)]">
          {t("relay.disclaimerDesc")}
        </p>
      </SettingGroup>
    </div>
  );
}
