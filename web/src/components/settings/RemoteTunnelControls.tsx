// web/src/components/settings/RemoteTunnelControls.tsx
// Pairs this desktop with a public GoPanel so the mobile app can reach it
// through the reverse tunnel. The primary path is one-click: fill in the
// GoPanel host:port, click Authorize, then approve in GoPanel → Host →
// Tunnel → Pending pair requests (the panel is opened automatically).
// Pasting a code by hand stays available as a fallback.
import { createEffect, createSignal, onCleanup, onMount, Show } from "solid-js";
import { ExternalLink, Link2, Unlink } from "lucide-solid";
import { t } from "../../i18n";
import {
  cancelRemoteTunnel,
  disableRemoteTunnel,
  loadRemoteTunnelStatus,
  openExternalURL,
  pairRemoteTunnel,
  remoteTunnel,
  requestRemoteTunnel,
} from "../../store/workspace";
import { SettingGroup } from "./setting-controls";
import { confirmAction } from "./confirm-dialog";

const inputClass =
  "w-full rounded-xl border border-[var(--border-subtle)] bg-[var(--bg-panel)] px-3 py-2 text-[13px] text-[var(--text-primary)] placeholder:text-[var(--text-faint)] focus:border-[var(--accent-border)] focus:outline-none";
const buttonClass =
  "hoxi-press inline-flex items-center gap-1.5 rounded-xl border border-[var(--border-strong)] px-4 py-2 text-[13px] font-medium hover:bg-[var(--bg-hover)] disabled:opacity-50 cursor-pointer";
const linkButtonClass =
  "hoxi-press inline-flex items-center gap-1 rounded-lg px-2 py-1 text-[12px] text-[var(--text-secondary)] hover:bg-[var(--bg-hover)] cursor-pointer";

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
  const [now, setNow] = createSignal(Date.now());

  const pending = () => remoteTunnel.status.pending;
  const waiting = () => pending()?.state === "waiting";
  const showForm = () => !remoteTunnel.status.enabled && !waiting();
  const panelBaseUrl = () => (controllerUrl().trim() || remoteTunnel.status.controllerUrl || "").replace(/\/+$/, "");
  // Deep-link into the GoPanel tunnel page, opening the pending pair-request
  // tab directly so an admin only has to click Allow.
  const panelUrl = () => {
    const base = panelBaseUrl();
    return base ? `${base}/host/tunnel?tab=requests` : "";
  };

  let refreshTimer: number | undefined;

  onMount(() => {
    void loadRemoteTunnelStatus();
    // Ticks the authorization-code countdown.
    const clock = setInterval(() => setNow(Date.now()), 1000);
    onCleanup(() => clearInterval(clock));
  });

  // Poll faster (2s) while an approval is pending, otherwise follow reconnects
  // at a calmer 5s.
  createEffect(() => {
    const active = waiting();
    if (refreshTimer !== undefined) clearInterval(refreshTimer);
    refreshTimer = setInterval(() => void loadRemoteTunnelStatus(), active ? 2000 : 5000);
  });

  onCleanup(() => {
    if (refreshTimer !== undefined) clearInterval(refreshTimer);
  });

  const remaining = () => {
    const expiresAt = pending()?.expiresAt;
    if (!expiresAt) return "";
    const ms = new Date(expiresAt).getTime() - now();
    if (ms <= 0) return "00:00";
    const total = Math.floor(ms / 1000);
    return `${String(Math.floor(total / 60)).padStart(2, "0")}:${String(total % 60).padStart(2, "0")}`;
  };

  const onAuthorize = async (event: Event) => {
    event.preventDefault();
    const ok = await requestRemoteTunnel({ controllerUrl: controllerUrl().trim(), name: name().trim() });
    // Jump straight to GoPanel so an admin can approve the pending request
    // without opening the console by hand.
    if (ok) {
      const target = panelUrl();
      if (target) openExternalURL(target);
    }
  };

  const onPair = async (event: Event) => {
    event.preventDefault();
    const ok = await pairRemoteTunnel({ controllerUrl: controllerUrl().trim(), code: code().trim(), name: name().trim() });
    if (ok) setCode("");
  };

  const onDisable = async () => {
    if (!(await confirmAction(t("remoteTunnel.disableConfirm")))) return;
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

        <Show when={waiting()}>
          <div class="space-y-3 rounded-xl border border-[var(--border-subtle)] bg-[var(--bg-panel)] px-4 py-4">
            <p class="text-[13px] font-medium text-[var(--text-primary)]">{t("remoteTunnel.waitingTitle")}</p>
            <div class="flex items-baseline gap-3">
              <span class="text-[12px] text-[var(--text-secondary)]">{t("remoteTunnel.userCodeLabel")}</span>
              <span class="font-mono text-[22px] font-semibold tracking-[0.2em] text-[var(--text-primary)]">{pending()?.userCode}</span>
            </div>
            <p class="text-[12px] text-[var(--text-muted)]">{t("remoteTunnel.expiresIn", { time: remaining() })}</p>
            <p class="text-[12px] text-[var(--text-secondary)]">{t("remoteTunnel.waitingHint")}</p>
            <div class="flex items-center gap-2">
              <button type="button" class={buttonClass} disabled={remoteTunnel.busy} onClick={() => void cancelRemoteTunnel()}>
                <span>{t("remoteTunnel.cancelAction")}</span>
              </button>
              <Show when={panelUrl()}>
                <button type="button" class={linkButtonClass} onClick={() => openExternalURL(panelUrl())}>
                  <ExternalLink size={13} />
                  <span>{t("remoteTunnel.openPanelAction")}</span>
                </button>
              </Show>
            </div>
          </div>
        </Show>

        <Show when={pending()?.state === "denied"}>
          <p class="text-[12px] text-rose-500">{t("remoteTunnel.pendingDenied")}</p>
        </Show>
        <Show when={pending()?.state === "expired"}>
          <p class="text-[12px] text-amber-600">{t("remoteTunnel.pendingExpired")}</p>
        </Show>

        <Show when={showForm()}>
          <form class="grid gap-3 sm:grid-cols-2" onSubmit={(event) => void onAuthorize(event)}>
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
            <label class="space-y-1 sm:col-span-2">
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
                <span>{remoteTunnel.busy ? t("remoteTunnel.requesting") : t("remoteTunnel.requestAction")}</span>
              </button>
            </div>
          </form>

          <details class="rounded-xl border border-[var(--border-subtle)] px-4 py-3">
            <summary class="cursor-pointer text-[12px] text-[var(--text-secondary)]">{t("remoteTunnel.manualToggle")}</summary>
            <form class="mt-3 grid gap-3 sm:grid-cols-2" onSubmit={(event) => void onPair(event)}>
              <label class="space-y-1 sm:col-span-2">
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
              <div class="sm:col-span-2">
                <button type="submit" class={buttonClass} disabled={remoteTunnel.busy}>
                  <Link2 size={14} />
                  <span>{remoteTunnel.busy ? t("remoteTunnel.pairing") : t("remoteTunnel.pairAction")}</span>
                </button>
              </div>
            </form>
          </details>
        </Show>

        <Show when={remoteTunnel.error}>
          <p role="alert" class="text-[12px] text-rose-500">{remoteTunnel.error}</p>
        </Show>
      </div>
    </SettingGroup>
  );
}
