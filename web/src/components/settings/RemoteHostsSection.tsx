// web/src/components/settings/RemoteHostsSection.tsx
import { createSignal, For, onMount, Show } from "solid-js";
import { Activity, Plus, Server, Trash2 } from "lucide-solid";
import { t } from "../../i18n";
import {
  loadRemoteHosts,
  probeRemoteHostById,
  remoteHosts,
  removeRemoteHost,
  upsertRemoteHost,
} from "../../store/workspace";
import { SettingGroup } from "./setting-controls";

export function RemoteHostsSection() {
  const [target, setTarget] = createSignal("");
  const [name, setName] = createSignal("");
  const [token, setToken] = createSignal("");
  const [saving, setSaving] = createSignal(false);
  const [probingId, setProbingId] = createSignal<string | null>(null);
  const [feedback, setFeedback] = createSignal<{ ok: boolean; text: string } | null>(null);

  onMount(() => {
    void loadRemoteHosts().catch(() => {});
  });

  const onMountHost = async (ev: Event) => {
    ev.preventDefault();
    const rawTarget = target().trim();
    if (!rawTarget || saving()) return;
    setSaving(true);
    setFeedback(null);
    try {
      const isPairUri = rawTarget.toLowerCase().startsWith("hoxi-host://");
      const saved = await upsertRemoteHost(
        isPairUri
          ? { pairUri: rawTarget, ...(name().trim() ? { name: name().trim() } : {}) }
          : {
              url: rawTarget,
              name: name().trim() || undefined,
              token: token().trim() || undefined,
            }
      );
      setTarget("");
      setName("");
      setToken("");
      const probe = await probeRemoteHostById(saved.id).catch(() => null);
      if (probe && !probe.ok && probe.error) {
        setFeedback({ ok: false, text: t("settings.remoteHostOffline", { error: probe.error }) });
      } else {
        setFeedback({ ok: true, text: t("settings.remoteHostSaved", { name: saved.name || saved.id }) });
      }
    } catch (err: any) {
      setFeedback({ ok: false, text: err?.message || String(err) });
    } finally {
      setSaving(false);
    }
  };

  const onProbe = async (id: string) => {
    if (probingId()) return;
    setProbingId(id);
    setFeedback(null);
    try {
      const res = await probeRemoteHostById(id);
      if (res.ok) {
        setFeedback({ ok: true, text: t("settings.remoteHostOnline", { ms: String(res.latencyMs) }) });
      } else {
        setFeedback({ ok: false, text: t("settings.remoteHostOffline", { error: res.error || "failed" }) });
      }
    } catch (err: any) {
      setFeedback({ ok: false, text: err?.message || String(err) });
    } finally {
      setProbingId(null);
    }
  };

  const onRemove = async (id: string) => {
    setFeedback(null);
    try {
      await removeRemoteHost(id);
      setFeedback({ ok: true, text: t("settings.remoteHostDeleted", { id }) });
    } catch (err: any) {
      setFeedback({ ok: false, text: err?.message || String(err) });
    }
  };

  return (
    <SettingGroup
      title={t("settings.remoteHostsTitle")}
      tip={t("settings.remoteHostsSubtitle")}
    >
      <div class="p-4 space-y-3">
        <form onSubmit={(e) => void onMountHost(e)} class="flex flex-wrap items-center gap-2">
          <input
            type="text"
            placeholder={t("settings.remoteHostPairPlaceholder")}
            value={target()}
            onInput={(e) => setTarget(e.currentTarget.value)}
            class="min-w-[240px] flex-1 rounded-xl border border-[var(--border-strong)] bg-[var(--bg-panel)] px-3 py-1.5 font-mono text-[12px] text-[var(--text-primary)] outline-none focus:border-[var(--accent-primary)]"
          />
          <input
            type="text"
            placeholder={t("settings.remoteHostNamePlaceholder")}
            value={name()}
            onInput={(e) => setName(e.currentTarget.value)}
            class="w-36 rounded-xl border border-[var(--border-strong)] bg-[var(--bg-panel)] px-3 py-1.5 text-[12px] text-[var(--text-primary)] outline-none focus:border-[var(--accent-primary)]"
          />
          <Show when={!target().trim().toLowerCase().startsWith("hoxi-host://")}>
            <input
              type="password"
              placeholder={t("settings.remoteHostTokenPlaceholder")}
              value={token()}
              onInput={(e) => setToken(e.currentTarget.value)}
              class="w-44 rounded-xl border border-[var(--border-strong)] bg-[var(--bg-panel)] px-3 py-1.5 font-mono text-[12px] text-[var(--text-primary)] outline-none focus:border-[var(--accent-primary)]"
            />
          </Show>
          <button
            type="submit"
            disabled={saving() || !target().trim()}
            class="inline-flex items-center gap-1 rounded-xl bg-[var(--accent-primary)] px-3.5 py-1.5 text-[12px] font-medium text-[var(--accent-foreground)] disabled:opacity-40 cursor-pointer"
          >
            <Plus size={13} />
            {t("settings.remoteHostMountBtn")}
          </button>
        </form>

        <div class="space-y-1.5">
          <Show
            when={remoteHosts().length > 0}
            fallback={
              <div class="rounded-xl border border-[var(--border-subtle)] bg-[var(--bg-inset)] px-3.5 py-3 text-[12px] text-[var(--text-muted)]">
                {t("settings.remoteHostsEmpty")}
              </div>
            }
          >
            <For each={remoteHosts()}>
              {(host) => (
                <div class="flex items-center justify-between gap-2 rounded-xl border border-[var(--border-subtle)] bg-[var(--bg-inset)] px-3.5 py-2">
                  <div class="flex items-center gap-2 min-w-0">
                    <Server size={14} class="text-[var(--accent-primary)] shrink-0" />
                    <span class="text-[12px] font-semibold text-[var(--text-primary)]">
                      {host.name || host.id}
                    </span>
                    <Show when={host.platform}>
                      <span class="rounded-md bg-[var(--bg-panel)] px-1.5 py-0.5 font-mono text-[10px] text-[var(--text-secondary)]">
                        {host.platform}/{host.arch}
                      </span>
                    </Show>
                    <Show when={typeof host.lastLatencyMs === "number" && host.lastSeenAt}>
                      <span class="rounded-md bg-[var(--ok-surface)] px-1.5 py-0.5 font-mono text-[10px] text-[var(--ok-text)]">
                        {host.lastLatencyMs}ms
                      </span>
                    </Show>
                    <span class="truncate font-mono text-[11px] text-[var(--text-muted)]">
                      {host.url}
                      {host.defaultDir ? ` · ${host.defaultDir}` : ""}
                    </span>
                  </div>
                  <div class="flex items-center gap-1.5 shrink-0">
                    <button
                      type="button"
                      disabled={probingId() === host.id}
                      onClick={() => void onProbe(host.id)}
                      class="inline-flex items-center gap-1 rounded-lg border border-[var(--border-subtle)] bg-[var(--bg-panel)] px-2.5 py-1 text-[11px] text-[var(--text-secondary)] hover:text-[var(--text-primary)] cursor-pointer"
                    >
                      <Activity size={12} />
                      {probingId() === host.id ? t("settings.remoteHostProbingBtn") : t("settings.remoteHostProbeBtn")}
                    </button>
                    <button
                      type="button"
                      onClick={() => void onRemove(host.id)}
                      class="text-[var(--text-muted)] hover:text-[var(--danger-text)] p-1 cursor-pointer"
                    >
                      <Trash2 size={13} />
                    </button>
                  </div>
                </div>
              )}
            </For>
          </Show>
        </div>

        <Show when={feedback()}>
          <div
            class={`rounded-xl border px-3.5 py-2 text-[12px] ${
              feedback()!.ok
                ? "border-[var(--ok-border)] bg-[var(--ok-surface)] text-[var(--ok-text)]"
                : "border-[var(--danger-border)] bg-[var(--danger-surface)] text-[var(--danger-text)]"
            }`}
          >
            {feedback()!.text}
          </div>
        </Show>
      </div>
    </SettingGroup>
  );
}
