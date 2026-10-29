// web/src/components/models/OfficialAinodeTab.tsx
import { createEffect, createMemo, createSignal, For, Show } from "solid-js";
import { Check, LoaderCircle, RefreshCw, Sparkles } from "lucide-solid";
import {
  AUTO_PROVIDER_ID,
  autoPools,
  connectOfficialAinode,
  desktopAuth,
  formatMemberPoints,
  getAutoPoolDisplayName,
  getStoredPoolCandidates,
  loadOfficialAinodeModels,
  OFFICIAL_PROVIDER_ID,
  officialAinodeState,
  openSettingsModal,
  PRESET_AUTO_POOL_IDS,
  saveAutoPool,
  selectModel,
  workspace,
} from "../../store/workspace";
import { t } from "../../i18n";
import { ProviderIcon } from "../icons";

function modelPointsLabel(
  m: { perCallPoints: number; inputPointsPerM: number; outputPointsPerM: number }
): string {
  if (m.perCallPoints > 0) {
    return t("models.officialPerCallPoints", { points: formatMemberPoints(m.perCallPoints) });
  }
  return t("models.officialTokenPointsPerM", {
    points: `${formatMemberPoints(m.inputPointsPerM)} / ${formatMemberPoints(m.outputPointsPerM)}`,
  });
}

export default function OfficialAinodeTab() {
  const [connecting, setConnecting] = createSignal(false);
  const [feedback, setFeedback] = createSignal<{ ok: boolean; text: string } | null>(null);
  const [poolID, setPoolID] = createSignal<string>(
    workspace.defaultAI.model?.providerID === AUTO_PROVIDER_ID
      ? workspace.defaultAI.model.modelID
      : "frontier"
  );

  const poolIDs = createMemo(() => {
    const ids = new Set<string>(PRESET_AUTO_POOL_IDS as unknown as string[]);
    for (const id of Object.keys(autoPools())) ids.add(id);
    return Array.from(ids);
  });

  const inPool = (modelID: string) =>
    getStoredPoolCandidates(poolID()).some(
      (c) => c.providerID === OFFICIAL_PROVIDER_ID && c.modelID === modelID
    );

  const isSelected = (modelID: string) =>
    workspace.selectedModel?.providerID === OFFICIAL_PROVIDER_ID &&
    workspace.selectedModel?.modelID === modelID;

  createEffect(() => {
    if (desktopAuth.isLoggedIn) {
      void loadOfficialAinodeModels();
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

  const onTogglePool = async (modelID: string) => {
    try {
      const cur = getStoredPoolCandidates(poolID());
      const exists = cur.some(
        (c) => c.providerID === OFFICIAL_PROVIDER_ID && c.modelID === modelID
      );
      const next = exists
        ? cur.filter((c) => !(c.providerID === OFFICIAL_PROVIDER_ID && c.modelID === modelID))
        : [...cur, { providerID: OFFICIAL_PROVIDER_ID, modelID }];
      await saveAutoPool(poolID(), { customized: true, candidates: next });
    } catch (err: any) {
      setFeedback({ ok: false, text: err?.message || t("models.officialConnectFailed") });
    }
  };

  return (
    <div class="flex-1 overflow-y-auto p-6 sm:p-8">
      <div class="mx-auto w-full max-w-4xl space-y-5">
        <div class="flex items-start justify-between gap-4 rounded-2xl border border-[var(--accent-border)] bg-[var(--accent-surface)]/35 p-4">
          <div class="flex items-center gap-3 min-w-0">
            <ProviderIcon id={OFFICIAL_PROVIDER_ID} class="w-6 h-6 shrink-0" />
            <div class="min-w-0">
              <div class="flex items-center gap-1.5 text-[14px] font-semibold text-[var(--text-primary)]">
                <Sparkles size={14} class="text-[var(--accent-primary)] shrink-0" />
                <span>{t("models.officialCardTitle")}</span>
              </div>
              <p class="text-[12px] text-[var(--text-secondary)]">
                {t("models.officialCardDesc")}
              </p>
            </div>
          </div>
          <span class="shrink-0 rounded-full bg-[var(--bg-inset)] px-3 py-1.5 text-[12px] font-semibold text-[var(--accent-primary)]">
            {t("models.officialPointsLabel", { points: formatMemberPoints(desktopAuth.member.points) })}
          </span>
        </div>

        <Show
          when={desktopAuth.isLoggedIn}
          fallback={
            <div class="flex flex-col items-center gap-3 rounded-2xl border border-dashed border-[var(--border-subtle)] bg-[var(--bg-elevated)]/30 p-8 text-center">
              <p class="text-[12px] text-[var(--text-muted)]">
                {t("models.officialConnectLoginRequired")}
              </p>
              <button
                type="button"
                onClick={() => openSettingsModal("account")}
                class="rounded-xl bg-[var(--accent-primary)] px-4 py-2 text-[12px] font-medium text-[var(--accent-foreground)] hover:opacity-90 cursor-pointer"
              >
                {t("settings.accountSignInTitle")}
              </button>
            </div>
          }
        >
          <div class="flex flex-wrap items-center justify-between gap-2">
            <label class="inline-flex items-center gap-2 text-[12px] text-[var(--text-secondary)]">
              {t("models.officialPoolTarget")}
              <select
                value={poolID()}
                onChange={(e) => setPoolID(e.currentTarget.value)}
                class="rounded-lg border border-[var(--border-subtle)] bg-[var(--bg-elevated)] px-2 py-1 text-[12px] text-[var(--text-primary)] outline-none"
              >
                <For each={poolIDs()}>
                  {(id) => <option value={id}>{getAutoPoolDisplayName(id)}</option>}
                </For>
              </select>
            </label>

            <div class="flex items-center gap-2">
              <button
                type="button"
                disabled={officialAinodeState.loading || connecting()}
                onClick={() => void loadOfficialAinodeModels(true)}
                class="inline-flex items-center gap-1.5 rounded-xl border border-[var(--border-subtle)] bg-[var(--bg-panel)] px-3 py-1.5 text-[12px] font-medium text-[var(--text-secondary)] hover:text-[var(--text-primary)] disabled:opacity-50 cursor-pointer"
              >
                <RefreshCw size={12} class={officialAinodeState.loading ? "animate-spin" : ""} />
                {t("models.officialReconnectBtn")}
              </button>
              <button
                type="button"
                disabled={connecting()}
                onClick={() => void onConnect()}
                class="inline-flex items-center gap-1.5 rounded-xl bg-[var(--accent-primary)] px-4 py-1.5 text-[12px] font-medium text-[var(--accent-foreground)] hover:opacity-90 disabled:opacity-60 transition-opacity cursor-pointer"
              >
                <Show when={!connecting()} fallback={<LoaderCircle size={12} class="animate-spin" />}>
                  <Sparkles size={12} />
                </Show>
                {connecting() ? t("models.officialConnecting") : t("models.officialConnectBtn")}
              </button>
            </div>
          </div>

          <Show when={feedback()}>
            <div
              class={`rounded-xl border px-3.5 py-2.5 text-[12px] ${
                feedback()!.ok
                  ? "border-[var(--ok-border)] bg-[var(--ok-surface)] text-[var(--ok-text)]"
                  : "border-[var(--danger-border)] bg-[var(--danger-surface)] text-[var(--danger-text)]"
              }`}
            >
              {feedback()!.text}
            </div>
          </Show>

          <Show
            when={officialAinodeState.models.length > 0}
            fallback={
              <div class="rounded-xl border border-dashed border-[var(--border-subtle)] bg-[var(--bg-elevated)]/30 p-6 text-center text-[12px] text-[var(--text-muted)]">
                {officialAinodeState.loading ? t("models.officialConnecting") : t("models.officialModelsEmpty")}
              </div>
            }
          >
            <div class="divide-y divide-[var(--border-subtle)] overflow-hidden rounded-2xl border border-[var(--border-subtle)] bg-[var(--bg-elevated)]/50">
              <For each={officialAinodeState.models}>
                {(m) => (
                  <div class="flex items-center justify-between gap-3 px-4 py-3">
                    <div class="min-w-0">
                      <div class="flex items-center gap-2 min-w-0">
                        <span class="truncate text-[13px] font-semibold text-[var(--text-primary)]">
                          {m.name || m.id}
                        </span>
                        <Show when={m.vendor}>
                          <span class="shrink-0 rounded-md bg-[var(--bg-inset)] px-1.5 py-0.5 text-[10px] text-[var(--text-muted)]">
                            {m.vendor}
                          </span>
                        </Show>
                      </div>
                      <div class="font-mono text-[11px] text-[var(--text-faint)] truncate">
                        {m.id} · {modelPointsLabel(m)}
                      </div>
                    </div>

                    <div class="flex items-center gap-1.5 shrink-0">
                      <button
                        type="button"
                        disabled={isSelected(m.id)}
                        onClick={() => selectModel({ providerID: OFFICIAL_PROVIDER_ID, modelID: m.id })}
                        class={`inline-flex items-center gap-1 rounded-xl px-3 py-1.5 text-[12px] font-medium transition-colors cursor-pointer ${
                          isSelected(m.id)
                            ? "border border-[var(--ok-border)] bg-[var(--ok-surface)] text-[var(--ok-text)]"
                            : "bg-[var(--accent-primary)] text-[var(--accent-foreground)] hover:opacity-90"
                        }`}
                      >
                        <Show when={isSelected(m.id)} fallback={t("models.officialUseBtn")}>
                          <Check size={12} />
                          {t("models.officialInUse")}
                        </Show>
                      </button>
                      <button
                        type="button"
                        onClick={() => void onTogglePool(m.id)}
                        class={`inline-flex items-center gap-1 rounded-xl border px-3 py-1.5 text-[11px] font-medium transition-colors cursor-pointer ${
                          inPool(m.id)
                            ? "border-[var(--accent-border)] bg-[var(--accent-surface)] text-[var(--accent-primary)]"
                            : "border-[var(--border-subtle)] bg-[var(--bg-panel)] text-[var(--text-secondary)] hover:text-[var(--text-primary)]"
                        }`}
                      >
                        {inPool(m.id) ? t("models.officialInPool") : t("models.officialAddToPool")}
                      </button>
                    </div>
                  </div>
                )}
              </For>
            </div>
          </Show>
        </Show>
      </div>
    </div>
  );
}
