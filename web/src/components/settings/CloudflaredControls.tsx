// web/src/components/settings/CloudflaredControls.tsx
// Supervises a Cloudflare Tunnel connector so the phone can reach this desktop
// off the local network. The connector token is stored on the desktop backend
// and never returned here.
import { createSignal, onCleanup, onMount, Show } from "solid-js";
import { Cloud, Unlink } from "lucide-solid";
import { t } from "../../i18n";
import {
  cloudflared,
  disableCloudflared,
  enableCloudflared,
  loadCloudflaredStatus,
} from "../../store/workspace";
import { SettingGroup } from "./setting-controls";
import { confirmAction } from "./confirm-dialog";

const inputClass =
  "w-full rounded-xl border border-[var(--border-subtle)] bg-[var(--bg-panel)] px-3 py-2 text-[13px] text-[var(--text-primary)] placeholder:text-[var(--text-faint)] focus:border-[var(--accent-border)] focus:outline-none";
const buttonClass =
  "hoxi-press inline-flex items-center gap-1.5 rounded-xl border border-[var(--border-strong)] px-4 py-2 text-[13px] font-medium hover:bg-[var(--bg-hover)] disabled:opacity-50 cursor-pointer";

function stateLabel(): string {
  switch (cloudflared.status.state) {
    case "online":
      return t("cloudflared.stateOnline");
    case "starting":
      return t("cloudflared.stateStarting");
    case "missing":
      return t("cloudflared.stateMissing");
    case "offline":
      return t("cloudflared.stateOffline");
    default:
      return t("cloudflared.stateDisabled");
  }
}

function stateDotClass(): string {
  switch (cloudflared.status.state) {
    case "online":
      return "bg-emerald-500";
    case "starting":
    case "offline":
      return "bg-amber-500";
    case "missing":
      return "bg-rose-500";
    default:
      return "bg-[var(--text-faint)]";
  }
}

export function CloudflaredControls() {
  const [token, setToken] = createSignal("");
  const [hostname, setHostname] = createSignal("");

  onMount(() => {
    void loadCloudflaredStatus();
    const timer = setInterval(() => void loadCloudflaredStatus(), 5000);
    onCleanup(() => clearInterval(timer));
  });

  const onEnable = async (event: Event) => {
    event.preventDefault();
    const ok = await enableCloudflared(token().trim(), hostname().trim());
    if (ok) setToken("");
  };

  const onDisable = async () => {
    if (!(await confirmAction(t("cloudflared.disableConfirm")))) return;
    void disableCloudflared();
  };

  return (
    <SettingGroup title={t("cloudflared.title")} tip={t("cloudflared.tip")}>
      <div class="space-y-4 px-5 py-4">
        <div class="flex flex-wrap items-center justify-between gap-3">
          <div class="flex min-w-0 items-center gap-2 text-[13px]">
            <span class={`h-2 w-2 shrink-0 rounded-full ${stateDotClass()}`} />
            <span class="font-medium text-[var(--text-primary)]">{stateLabel()}</span>
            <Show when={cloudflared.status.hostname}>
              <span class="truncate font-mono text-[12px] text-[var(--text-muted)]">
                {cloudflared.status.hostname}
              </span>
            </Show>
          </div>
          <Show when={cloudflared.status.enabled}>
            <button type="button" class={buttonClass} disabled={cloudflared.busy} onClick={onDisable}>
              <Unlink size={14} />
              <span>{t("cloudflared.disableAction")}</span>
            </button>
          </Show>
        </div>

        <Show when={cloudflared.status.state === "missing"}>
          <p class="text-[12px] text-rose-500">{t("cloudflared.missingHint")}</p>
        </Show>
        <Show when={cloudflared.status.state === "offline" && cloudflared.status.message}>
          <p class="text-[12px] text-amber-600">{cloudflared.status.message}</p>
        </Show>

        <p class="text-[12px] text-[var(--text-muted)]">
          {t("cloudflared.ingressHint", { url: cloudflared.status.ingressUrl ?? "http://127.0.0.1:5418" })}
        </p>

        <Show when={!cloudflared.status.enabled}>
          <form class="grid gap-3 sm:grid-cols-2" onSubmit={(event) => void onEnable(event)}>
            <label class="space-y-1 sm:col-span-2">
              <span class="text-[12px] text-[var(--text-secondary)]">{t("cloudflared.tokenLabel")}</span>
              <input
                class={`${inputClass} font-mono`}
                required
                autocomplete="off"
                spellcheck={false}
                value={token()}
                placeholder={t("cloudflared.tokenPlaceholder")}
                onInput={(event) => setToken(event.currentTarget.value)}
              />
            </label>
            <label class="space-y-1 sm:col-span-2">
              <span class="text-[12px] text-[var(--text-secondary)]">{t("cloudflared.hostnameLabel")}</span>
              <input
                class={inputClass}
                autocomplete="off"
                spellcheck={false}
                value={hostname()}
                placeholder={t("cloudflared.hostnamePlaceholder")}
                onInput={(event) => setHostname(event.currentTarget.value)}
              />
            </label>
            <div class="sm:col-span-2">
              <button type="submit" class={buttonClass} disabled={cloudflared.busy}>
                <Cloud size={14} />
                <span>{t("cloudflared.enableAction")}</span>
              </button>
            </div>
          </form>
        </Show>

        <Show when={cloudflared.error}>
          <p role="alert" class="text-[12px] text-rose-500">
            {cloudflared.error}
          </p>
        </Show>
      </div>
    </SettingGroup>
  );
}
