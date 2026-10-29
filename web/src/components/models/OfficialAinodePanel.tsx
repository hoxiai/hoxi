// web/src/components/models/OfficialAinodePanel.tsx
import { createEffect, createSignal, For, Show } from "solid-js";
import { LoaderCircle, RefreshCw, Sparkles } from "lucide-solid";
import {
  connectOfficialAinode,
  desktopAuth,
  loadOfficialAinodeModels,
  officialAinodeState,
  workspace,
} from "../../store/workspace";
import { t } from "../../i18n";
import { ProviderIcon } from "../icons";

function formatPoints(value: number): string {
  if (!Number.isFinite(value) || value <= 0) return "0";
  return value.toLocaleString(undefined, { maximumFractionDigits: 2 });
}

export default function OfficialAinodePanel() {
  const [connecting, setConnecting] = createSignal(false);
  const [feedback, setFeedback] = createSignal<{ ok: boolean; text: string } | null>(null);

  const isConnected = () =>
    Boolean(workspace.hoxiProviders?.["ainode"]) ||
    Boolean(workspace.connectedProviders?.includes("ainode"));

  createEffect(() => {
    if (desktopAuth.isLoggedIn) {
      void loadOfficialAinodeModels();
    } else {
      setFeedback(null);
    }
  });

  const onConnect = async () => {
    if (connecting()) return;
    setConnecting(true);
    setFeedback(null);
    try {
      const res = await connectOfficialAinode();
      setFeedback({ ok: true, text: t("models.officialConnectSuccess", { count: res.modelCount }) });
    } catch (err: any) {
      setFeedback({ ok: false, text: err?.message || t("models.officialConnectFailed") });
    } finally {
      setConnecting(false);
    }
  };

  return (
    <Show when={desktopAuth.isLoggedIn}>
      <div class="rounded-2xl border border-[var(--accent-border)] bg-[var(--accent-surface)]/35 p-4 space-y-3">
        <div class="flex items-start justify-between gap-3">
          <div class="flex items-center gap-2.5 min-w-0">
            <ProviderIcon id="ainode" class="w-5 h-5 shrink-0" />
            <div class="min-w-0">
              <div class="flex items-center gap-1.5 text-[13px] font-semibold text-[var(--text-primary)]">
                <Sparkles size={13} class="text-[var(--accent-primary)] shrink-0" />
                <span>{t("models.officialCardTitle")}</span>
              </div>
              <p class="text-[12px] text-[var(--text-secondary)] line-clamp-2">
                {t("models.officialCardDesc")}
              </p>
            </div>
          </div>
          <span
            class="shrink-0 rounded-full bg-[var(--bg-inset)] px-2.5 py-1 text-[11px] font-semibold text-[var(--accent-primary)]"
            title={t("models.officialPointsLabel", { points: formatPoints(desktopAuth.member.points) })}
          >
            {t("models.officialPointsLabel", { points: formatPoints(desktopAuth.member.points) })}
          </span>
        </div>

        <Show
          when={officialAinodeState.models.length > 0}
          fallback={
            <div class="text-[11px] text-[var(--text-muted)]">
              <Show when={officialAinodeState.loading} fallback={t("models.officialModelsEmpty")}>
                <span class="inline-flex items-center gap-1.5">
                  <LoaderCircle size={11} class="animate-spin" />
                  {t("models.officialConnecting")}
                </span>
              </Show>
            </div>
          }
        >
          <div class="flex flex-wrap gap-1.5">
            <For each={officialAinodeState.models.slice(0, 8)}>
              {(m) => (
                <span
                  title={
                    m.perCallPoints > 0
                      ? t("models.officialPerCallPoints", { points: formatPoints(m.perCallPoints) })
                      : t("models.officialTokenPointsPerM", {
                          points: `${formatPoints(m.inputPointsPerM)} / ${formatPoints(m.outputPointsPerM)}`,
                        })
                  }
                  class="rounded-md bg-[var(--bg-inset)] px-1.5 py-0.5 font-mono text-[10px] text-[var(--text-muted)]"
                >
                  {m.name || m.id}
                </span>
              )}
            </For>
            <Show when={officialAinodeState.models.length > 8}>
              <span class="rounded-md bg-[var(--bg-inset)] px-1.5 py-0.5 text-[10px] text-[var(--text-faint)]">
                +{officialAinodeState.models.length - 8}
              </span>
            </Show>
          </div>
        </Show>

        <Show when={feedback()}>
          <div
            class={`rounded-xl border px-3 py-2 text-[11px] ${
              feedback()!.ok
                ? "border-[var(--ok-border)] bg-[var(--ok-surface)] text-[var(--ok-text)]"
                : "border-[var(--danger-border)] bg-[var(--danger-surface)] text-[var(--danger-text)]"
            }`}
          >
            {feedback()!.text}
          </div>
        </Show>

        <div class="flex items-center justify-end gap-2">
          <button
            type="button"
            disabled={officialAinodeState.loading || connecting()}
            onClick={() => void loadOfficialAinodeModels(true)}
            title={t("models.officialReconnectBtn")}
            class="inline-flex items-center gap-1 rounded-xl border border-[var(--border-subtle)] bg-[var(--bg-panel)] px-2.5 py-1.5 text-[11px] font-medium text-[var(--text-secondary)] hover:text-[var(--text-primary)] disabled:opacity-50 cursor-pointer"
          >
            <RefreshCw size={11} class={officialAinodeState.loading ? "animate-spin" : ""} />
          </button>
          <button
            type="button"
            disabled={connecting()}
            onClick={() => void onConnect()}
            class="inline-flex items-center gap-1.5 rounded-xl bg-[var(--accent-primary)] px-3.5 py-1.5 text-[12px] font-medium text-[var(--accent-foreground)] hover:opacity-90 disabled:opacity-60 transition-opacity cursor-pointer"
          >
            <Show when={!connecting()} fallback={<LoaderCircle size={12} class="animate-spin" />}>
              <Sparkles size={12} />
            </Show>
            {connecting()
              ? t("models.officialConnecting")
              : isConnected()
                ? t("models.officialReconnectBtn")
                : t("models.officialConnectBtn")}
          </button>
        </div>
      </div>
    </Show>
  );
}
