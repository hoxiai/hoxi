// web/src/components/settings/RemoteTunnelControls.tsx
// Pairs this desktop with a public GoPanel so the mobile app can reach it
// through the reverse tunnel. Pairing codes are issued in GoPanel → Nodes.
import { createSignal, onCleanup, onMount, Show } from "solid-js";
import { Link2, Unlink } from "lucide-solid";
import { t } from "../../i18n";
import {
  disableRemoteTunnel,
  loadRemoteTunnelStatus,
  pairRemoteTunnel,
  remoteTunnel,
} from "../../store/workspace";
import { SettingGroup } from "./setting-controls";

const inputClass =
  "w-full rounded-xl border border-[var(--border-subtle)] bg-[var(--bg-panel)] px-3 py-2 text-[13px] text-[var(--text-primary)] placeholder:text-[var(--text-faint)] focus:border-[var(--accent-border)] focus:outline-none";
const buttonClass =
  "hoxi-press inline-flex items-center gap-1.5 rounded-xl border border-[var(--border-strong)] px-4 py-2 text-[13px] font-medium hover:bg-[var(--bg-hover)] disabled:opacity-50 cursor-pointer";

function stateLabel(): string {
  switch (remoteTunnel.status.state) {
    case "online":
      return t("remoteTunnel.stateOnline");
    case "connecting":
      return t("remoteTunnel.stateConnecting");
    case "offline":
      return t("remoteTunnel.stateOffline");
    default:
      return t("remoteTunnel.stateDisabled");
  }
}

function stateDotClass(): string {
  switch (remoteTunnel.status.state) {
    case "online":
      return "bg-emerald-500";
    case "connecting":
    case "offline":
      return "bg-amber-500";
    default:
      return "bg-[var(--text-faint)]";
  }
}

export function RemoteTunnelControls() {
  const [controllerUrl, setControllerUrl] = createSignal("");
  const [code, setCode] = createSignal("");
  const [name, setName] = createSignal("");

  onMount(() => {
    void loadRemoteTunnelStatus();
    // Reconnects happen in the backend; refresh so the badge follows them.
    const timer = setInterval(() => void loadRemoteTunnelStatus(), 5000);
    onCleanup(() => clearInterval(timer));
  });

  const onPair = async (event: Event) => {
    event.preventDefault();
    const ok = await pairRemoteTunnel({ controllerUrl: controllerUrl().trim(), code: code().trim(), name: name().trim() });
    if (ok) setCode("");
  };

  const onDisable = () => {
    if (!confirm(t("remoteTunnel.disableConfirm"))) return;
    void disableRemoteTunnel();
  };

  return (
    <SettingGroup title={t("remoteTunnel.title")} tip={t("remoteTunnel.tip")}>
      <div class="space-y-4 px-5 py-4">
        <div class="flex flex-wrap items-center justify-between gap-3">
          <div class="flex min-w-0 items-center gap-2 text-[13px]">
            <span class={`h-2 w-2 shrink-0 rounded-full ${stateDotClass()}`} />
            <span class="font-medium text-[var(--text-primary)]">{stateLabel()}</span>
            <Show when={remoteTunnel.status.enabled && remoteTunnel.status.controllerUrl}>
              <span class="truncate font-mono text-[12px] text-[var(--text-muted)]">{remoteTunnel.status.controllerUrl}</span>
            </Show>
            <Show when={remoteTunnel.status.nodeId}>
              <span class="text-[12px] text-[var(--text-faint)]">
                {t("remoteTunnel.nodeLabel", { id: String(remoteTunnel.status.nodeId) })}
              </span>
            </Show>
          </div>
          <Show when={remoteTunnel.status.enabled}>
            <button type="button" class={buttonClass} disabled={remoteTunnel.busy} onClick={onDisable}>
              <Unlink size={14} />
              <span>{t("remoteTunnel.disableAction")}</span>
            </button>
          </Show>
        </div>
        <Show when={remoteTunnel.status.state === "offline" && remoteTunnel.status.message}>
          <p class="text-[12px] text-amber-600">{remoteTunnel.status.message}</p>
        </Show>

        <Show when={!remoteTunnel.status.enabled}>
          <form class="grid gap-3 sm:grid-cols-2" onSubmit={(event) => void onPair(event)}>
            <label class="space-y-1 sm:col-span-2">
              <span class="text-[12px] text-[var(--text-secondary)]">{t("remoteTunnel.controllerLabel")}</span>
              <input
                class={inputClass}
                type="url"
                required
                value={controllerUrl()}
                placeholder={t("remoteTunnel.controllerPlaceholder")}
                onInput={(event) => setControllerUrl(event.currentTarget.value)}
              />
            </label>
            <label class="space-y-1">
              <span class="text-[12px] text-[var(--text-secondary)]">{t("remoteTunnel.codeLabel")}</span>
              <input
                class={`${inputClass} font-mono`}
                required
                autocomplete="off"
                spellcheck={false}
                value={code()}
                placeholder={t("remoteTunnel.codePlaceholder")}
                onInput={(event) => setCode(event.currentTarget.value)}
              />
            </label>
            <label class="space-y-1">
              <span class="text-[12px] text-[var(--text-secondary)]">{t("remoteTunnel.nameLabel")}</span>
              <input
                class={inputClass}
                value={name()}
                placeholder={t("remoteTunnel.namePlaceholder")}
                onInput={(event) => setName(event.currentTarget.value)}
              />
            </label>
            <div class="sm:col-span-2">
              <button type="submit" class={buttonClass} disabled={remoteTunnel.busy}>
                <Link2 size={14} />
                <span>{remoteTunnel.busy ? t("remoteTunnel.pairing") : t("remoteTunnel.pairAction")}</span>
              </button>
            </div>
          </form>
        </Show>

        <Show when={remoteTunnel.error}>
          <p role="alert" class="text-[12px] text-rose-500">{remoteTunnel.error}</p>
        </Show>
      </div>
    </SettingGroup>
  );
}
