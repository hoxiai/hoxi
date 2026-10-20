// web/src/components/models/AutoModelPoolTab.tsx
import { createEffect, createMemo, createSignal, For, Show } from "solid-js";
import {
  ArrowDown,
  ArrowUp,
  Check,
  ExternalLink,
  KeyRound,
  Plus,
  RefreshCw,
  RotateCcw,
  Sparkles,
  Trash2,
  Zap,
} from "lucide-solid";
import {
  AUTO_PROVIDER_ID,
  autoPools,
  clearAutoPoolCooldowns,
  closeSettingsModal,
  createCustomAutoPool,
  deleteCustomAutoPool,
  extractFlattenedModels,
  fetchFreeTierGuides,
  getAutoPoolDescription,
  getAutoPoolDisplayName,
  getCandidateCooldownRemainingMs,
  getEffectivePoolCandidates,
  getFreeTierGuideItems,
  isCandidateCoolingDown,
  openExternalURL,
  openProviderModal,
  peekAutoModelTarget,
  PRESET_AUTO_POOL_IDS,
  providerKeyCounts,
  resetAutoPoolToDefault,
  saveAutoPool,
  selectModel,
  setWorkspace,
  stepAutoPoolNext,
  workspace,
  type AutoPoolCandidate,
  type AutoRotationStrategy,
  type FreeTierGuideItem,
} from "../../store/workspace";
import { hoxiAPI } from "../../services/api";
import { ProviderIcon } from "../icons";
import { locale, t } from "../../i18n";

function formatCooldownRemain(ms: number): string {
  const sec = Math.max(1, Math.ceil(ms / 1000));
  if (sec < 60) return `${sec}s`;
  const min = Math.ceil(sec / 60);
  if (min < 60) return `${min}m`;
  return `${Math.ceil(min / 60)}h`;
}

export default function AutoModelPoolTab() {
  const [selectedPoolID, setSelectedPoolID] = createSignal<string>("free");
  const [freeFilterOnly, setFreeFilterOnly] = createSignal<boolean>(true);
  const [selectedAddKey, setSelectedAddKey] = createSignal<string>("");
  const [creatingCustom, setCreatingCustom] = createSignal<boolean>(false);
  const [newPoolID, setNewPoolID] = createSignal<string>("");
  const [newPoolName, setNewPoolName] = createSignal<string>("");

  createEffect(() => {
    void fetchFreeTierGuides(locale());
  });

  const poolIDs = createMemo<string[]>(() => {
    const map = autoPools();
    return [
      ...PRESET_AUTO_POOL_IDS,
      ...Object.keys(map).filter((id) => !(PRESET_AUTO_POOL_IDS as readonly string[]).includes(id)),
    ];
  });

  const activePool = createMemo(() => {
    const id = selectedPoolID();
    return (
      autoPools()[id] || {
        id,
        preset: (PRESET_AUTO_POOL_IDS as readonly string[]).includes(id),
        strategy: "round-robin" as AutoRotationStrategy,
        customized: false,
        candidates: [],
      }
    );
  });

  const connectedSet = createMemo(() => new Set(workspace.connectedProviders));

  const realConnectedModels = createMemo(() =>
    extractFlattenedModels(workspace.providers, connectedSet(), workspace.globalConfig).filter(
      (m) => m.providerID !== AUTO_PROVIDER_ID && m.connected
    )
  );

  const effectiveCandidates = createMemo<AutoPoolCandidate[]>(() =>
    getEffectivePoolCandidates(selectedPoolID())
  );

  const readyCount = createMemo(
    () =>
      effectiveCandidates().filter((c) => !isCandidateCoolingDown(c.providerID, c.modelID)).length
  );

  const activeTarget = createMemo(() => peekAutoModelTarget(selectedPoolID()));

  const addableModels = createMemo(() => {
    const existingSet = new Set(
      effectiveCandidates().map((c) => `${c.providerID}/${c.modelID}`)
    );
    return realConnectedModels().filter((m) => {
      if (existingSet.has(`${m.providerID}/${m.modelID}`)) return false;
      if (freeFilterOnly() && !m.free) return false;
      return true;
    });
  });

  const lookupModelMeta = (providerID: string, modelID: string) => {
    const found = realConnectedModels().find(
      (m) => m.providerID === providerID && m.modelID === modelID
    );
    if (found) return found;
    const prov = workspace.providers.find((p) => p.id === providerID);
    const mInfo = prov?.models?.[modelID];
    return {
      providerID,
      providerName: prov?.name || providerID,
      modelID,
      modelName: mInfo?.name || modelID,
      free: Boolean((mInfo as any)?.free),
    };
  };

  const handleStrategyChange = async (strategy: AutoRotationStrategy) => {
    const pool = activePool();
    await saveAutoPool(pool.id, {
      ...pool,
      strategy,
    });
  };

  const handleAddCandidate = async () => {
    const rawKey = selectedAddKey() || (addableModels()[0] ? `${addableModels()[0].providerID}/${addableModels()[0].modelID}` : "");
    if (!rawKey || !rawKey.includes("/")) return;
    const idx = rawKey.indexOf("/");
    const providerID = rawKey.slice(0, idx);
    const modelID = rawKey.slice(idx + 1);
    const curList = [...effectiveCandidates()];
    if (curList.some((c) => c.providerID === providerID && c.modelID === modelID)) return;
    curList.push({ providerID, modelID });
    await saveAutoPool(selectedPoolID(), {
      ...activePool(),
      customized: true,
      candidates: curList,
    });
    setSelectedAddKey("");
  };

  const handleRemoveCandidate = async (index: number) => {
    const curList = [...effectiveCandidates()];
    curList.splice(index, 1);
    await saveAutoPool(selectedPoolID(), {
      ...activePool(),
      customized: true,
      candidates: curList,
    });
  };

  const handleMoveCandidate = async (index: number, delta: -1 | 1) => {
    const curList = [...effectiveCandidates()];
    const targetIdx = index + delta;
    if (targetIdx < 0 || targetIdx >= curList.length) return;
    const [moved] = curList.splice(index, 1);
    curList.splice(targetIdx, 0, moved);
    await saveAutoPool(selectedPoolID(), {
      ...activePool(),
      customized: true,
      candidates: curList,
    });
  };

  const handleCreatePool = async () => {
    const id = newPoolID().trim();
    if (!id) return;
    const createdID = await createCustomAutoPool(id, newPoolName().trim() || id, "round-robin");
    setNewPoolID("");
    setNewPoolName("");
    setCreatingCustom(false);
    setSelectedPoolID(createdID);
  };

  const handleConnectGuideItem = async (item: FreeTierGuideItem) => {
    if (item.customMode && item.baseURL) {
      const existing = workspace.hoxiProviders?.[item.providerID];
      if (!existing) {
        const modelsObj: Record<string, any> = {};
        for (const m of item.defaultModels || []) {
          modelsObj[m.id] = {
            name: m.name,
            reasoning: m.reasoning,
            cost: m.free ? { input: 0, output: 0 } : undefined,
          };
        }
        await hoxiAPI
          .saveHoxiProviderConfig(item.providerID, {
            name: item.title || (item.providerID === "siliconflow" ? "SiliconFlow" : item.providerID),
            npm: "@ai-sdk/openai-compatible",
            options: { baseURL: item.baseURL },
            models: modelsObj,
          })
          .catch(() => {});
      }
      openProviderModal("custom", item.providerID);
      return;
    }
    openProviderModal("custom", item.providerID);
  };

  const isCurrentlySelectedInWorkspace = () =>
    workspace.selectedModel?.providerID === AUTO_PROVIDER_ID &&
    workspace.selectedModel?.modelID === selectedPoolID();

  return (
    <div class="flex-1 overflow-y-auto p-6 sm:p-8">
      <div class="mx-auto w-full max-w-5xl space-y-5">
      {/* Pool Selector Tabs */}
      <div class="flex flex-wrap items-center justify-between gap-2">
        <div class="flex flex-wrap items-center gap-1.5">
          <For each={poolIDs()}>
            {(pid) => {
              const active = () => selectedPoolID() === pid;
              const cands = () => getEffectivePoolCandidates(pid);
              return (
                <button
                  type="button"
                  onClick={() => {
                    setSelectedPoolID(pid);
                    if (pid === "free") setFreeFilterOnly(true);
                    else setFreeFilterOnly(false);
                  }}
                  class={`hoxi-press inline-flex items-center gap-1.5 rounded-xl border px-3 py-1.5 text-[12px] font-semibold transition-colors cursor-pointer ${
                    active()
                      ? "border-[var(--accent-border)] bg-[var(--accent-surface)] text-[var(--accent-primary)]"
                      : "border-[var(--border-subtle)] bg-[var(--bg-elevated)] text-[var(--text-secondary)] hover:text-[var(--text-primary)]"
                  }`}
                >
                  <Zap size={12} class={pid === "free" ? "text-[var(--ok)]" : "text-[var(--accent-primary)]"} />
                  <span>{getAutoPoolDisplayName(pid)}</span>
                  <span class="rounded-md bg-[var(--bg-inset)] px-1.5 py-0.2 font-mono text-[11px] opacity-80">
                    {cands().length}
                  </span>
                </button>
              );
            }}
          </For>
        </div>

        <button
          type="button"
          onClick={() => setCreatingCustom((v) => !v)}
          class="inline-flex items-center gap-1 rounded-xl border border-[var(--border-subtle)] bg-[var(--bg-elevated)] px-2.5 py-1.5 text-[12px] font-medium text-[var(--text-secondary)] hover:text-[var(--text-primary)] cursor-pointer"
        >
          <Plus size={12} />
          {t("models.autoPoolNewCustomBtn")}
        </button>
      </div>

      {/* Inline Custom Pool Creator */}
      <Show when={creatingCustom()}>
        <div class="flex flex-wrap items-center gap-2 rounded-xl border border-[var(--accent-border)] bg-[var(--accent-surface)]/30 p-3">
          <input
            type="text"
            value={newPoolID()}
            onInput={(e) => setNewPoolID(e.currentTarget.value)}
            placeholder={t("models.autoPoolNewIdPlaceholder")}
            class="w-44 rounded-lg border border-[var(--border-strong)] bg-[var(--bg-panel)] px-2.5 py-1 text-[12px] text-[var(--text-primary)] outline-none focus:border-[var(--accent-primary)]"
          />
          <input
            type="text"
            value={newPoolName()}
            onInput={(e) => setNewPoolName(e.currentTarget.value)}
            placeholder={t("models.autoPoolNewNamePlaceholder")}
            class="flex-1 min-w-[180px] rounded-lg border border-[var(--border-strong)] bg-[var(--bg-panel)] px-2.5 py-1 text-[12px] text-[var(--text-primary)] outline-none focus:border-[var(--accent-primary)]"
          />
          <button
            type="button"
            onClick={() => void handleCreatePool()}
            class="rounded-lg bg-[var(--accent-primary)] px-3 py-1 text-[12px] font-semibold text-white hover:opacity-90 cursor-pointer"
          >
            {t("models.autoPoolCreateConfirm")}
          </button>
        </div>
      </Show>

      {/* Active Pool Configuration Card */}
      <div class="rounded-2xl border border-[var(--border-subtle)] bg-[var(--bg-elevated)]/60 p-4 space-y-3">
        <div class="flex flex-wrap items-center justify-between gap-3">
          <div class="min-w-0 flex items-center gap-2 flex-wrap">
            <span class="text-[13px] font-semibold text-[var(--text-primary)]">
              {getAutoPoolDisplayName(selectedPoolID())}
            </span>
            <span class="text-[12px] text-[var(--text-muted)]">
              · {getAutoPoolDescription(selectedPoolID())}
            </span>
          </div>

          <div class="flex items-center gap-1.5 shrink-0 flex-wrap">
            <select
              value={activePool().strategy}
              onChange={(e) =>
                void handleStrategyChange(e.currentTarget.value as AutoRotationStrategy)
              }
              title={t("models.autoStrategyLabel")}
              class="rounded-xl border border-[var(--border-subtle)] bg-[var(--bg-panel)] px-2.5 py-1 text-[12px] font-medium text-[var(--text-primary)] outline-none focus:border-[var(--accent-primary)]"
            >
              <option value="round-robin">{t("models.autoStrategyRoundRobin")}</option>
              <option value="priority">{t("models.autoStrategyPriority")}</option>
              <option value="smart-health">{t("models.autoStrategySmartHealth")}</option>
            </select>

            <Show when={readyCount() < effectiveCandidates().length}>
              <button
                type="button"
                onClick={() => clearAutoPoolCooldowns(selectedPoolID())}
                class="inline-flex items-center gap-1 rounded-xl border border-[var(--border-subtle)] bg-[var(--bg-panel)] px-2.5 py-1 text-[11px] text-[var(--text-secondary)] hover:text-[var(--text-primary)] cursor-pointer"
              >
                <RefreshCw size={11} />
                {t("models.autoPoolClearCooldowns")}
              </button>
            </Show>

            <Show when={activePool().customized && activePool().preset}>
              <button
                type="button"
                onClick={() => void resetAutoPoolToDefault(selectedPoolID())}
                class="inline-flex items-center gap-1 rounded-xl border border-[var(--border-subtle)] bg-[var(--bg-panel)] px-2.5 py-1 text-[11px] text-[var(--text-secondary)] hover:text-[var(--text-primary)] cursor-pointer"
              >
                <RotateCcw size={11} />
                {t("models.autoPoolResetDefault")}
              </button>
            </Show>

            <Show when={!activePool().preset}>
              <button
                type="button"
                onClick={() => {
                  const idToDelete = selectedPoolID();
                  setSelectedPoolID("free");
                  void deleteCustomAutoPool(idToDelete);
                }}
                class="inline-flex items-center gap-1 rounded-xl border border-[var(--err)]/30 bg-[var(--err-surface)] px-2.5 py-1 text-[11px] text-[var(--err)] hover:opacity-90 cursor-pointer"
              >
                <Trash2 size={11} />
                {t("models.autoPoolDeleteCustom")}
              </button>
            </Show>

            <Show when={activeTarget()}>
              {(target) => (
                <button
                  type="button"
                  onClick={() => stepAutoPoolNext(selectedPoolID())}
                  class="inline-flex items-center gap-1 rounded-xl border border-[var(--border-subtle)] bg-[var(--bg-panel)] px-2.5 py-1 text-[11px] font-medium text-[var(--text-secondary)] hover:text-[var(--text-primary)] cursor-pointer"
                  title={t("models.autoPoolActiveTarget", {
                    model: `${target().providerID}/${target().modelID}`,
                  })}
                >
                  <RefreshCw size={11} />
                  <span>{t("models.autoPoolStepNext")}</span>
                </button>
              )}
            </Show>

            <button
              type="button"
              onClick={() => {
                selectModel({ providerID: AUTO_PROVIDER_ID, modelID: selectedPoolID() });
                closeSettingsModal();
              }}
              class={`inline-flex items-center gap-1.5 rounded-xl px-3 py-1 text-[12px] font-semibold cursor-pointer ${
                isCurrentlySelectedInWorkspace()
                  ? "bg-[var(--ok-surface)] text-[var(--ok-text)] border border-[var(--ok)]/30"
                  : "bg-[var(--accent-primary)] text-white hover:opacity-90"
              }`}
            >
              <Show when={isCurrentlySelectedInWorkspace()} fallback={<Sparkles size={12} />}>
                <Check size={12} />
              </Show>
              <span>{t("models.autoPoolUseNow")}</span>
            </button>
          </div>
        </div>

        {/* Candidates List */}
        <div class="space-y-1.5">
          <div class="flex items-center justify-between text-[12px] font-medium text-[var(--text-secondary)]">
            <span>
              {t("models.autoPoolCandidatesTitle", {
                ready: readyCount(),
                total: effectiveCandidates().length,
              })}
            </span>
            <Show when={activeTarget()}>
              {(target) => (
                <span class="font-mono text-[11px] text-[var(--accent-primary)]">
                  {t("models.autoPoolActiveTarget", {
                    model: `${target().providerID}/${target().modelID}`,
                  })}
                </span>
              )}
            </Show>
          </div>

          <Show
            when={effectiveCandidates().length > 0}
            fallback={
              <div class="rounded-xl border border-dashed border-[var(--border-subtle)] p-3 text-center text-[12px] text-[var(--text-muted)]">
                {t("models.autoPoolNoCandidates")}
              </div>
            }
          >
            <div class="max-h-[168px] overflow-y-auto space-y-1 pr-0.5">
              <For each={effectiveCandidates()}>
                {(cand, idx) => {
                  const meta = () => lookupModelMeta(cand.providerID, cand.modelID);
                  const cooling = () => isCandidateCoolingDown(cand.providerID, cand.modelID);
                  const remainMs = () =>
                    getCandidateCooldownRemainingMs(cand.providerID, cand.modelID);
                  const isCurrentTarget = () =>
                    activeTarget()?.providerID === cand.providerID &&
                    activeTarget()?.modelID === cand.modelID;

                  return (
                    <div
                      class={`flex items-center justify-between gap-2 rounded-xl border px-3 py-1.5 text-[12px] ${
                        isCurrentTarget()
                          ? "border-[var(--accent-border)] bg-[var(--accent-surface)]/40"
                          : "border-[var(--border-subtle)] bg-[var(--bg-panel)]"
                      }`}
                    >
                      <div class="flex items-center gap-2 min-w-0">
                        <span class="w-4 font-mono text-[11px] text-[var(--text-faint)]">
                          #{idx() + 1}
                        </span>
                        <ProviderIcon id={cand.providerID} class="w-3.5 h-3.5 shrink-0" />
                        <span class="font-medium text-[var(--text-primary)] truncate">
                          {meta().modelName}
                        </span>
                        <span class="text-[11px] text-[var(--text-muted)] truncate">
                          ({meta().providerName})
                        </span>
                        <Show when={meta().free}>
                          <span class="rounded bg-[var(--ok-surface)] px-1.5 py-0.2 text-[11px] font-semibold text-[var(--ok-text)]">
                            {t("models.freeModel")}
                          </span>
                        </Show>
                      </div>

                      <div class="flex items-center gap-1 shrink-0">
                        <span
                          class={`rounded-md px-1.5 py-0.5 text-[11px] font-semibold ${
                            cooling()
                              ? "bg-amber-500/15 text-amber-500"
                              : "bg-[var(--ok-surface)] text-[var(--ok-text)]"
                          }`}
                        >
                          {cooling()
                            ? t("models.autoPoolCoolingDown", {
                                remain: formatCooldownRemain(remainMs()),
                              })
                            : t("models.autoPoolReadyStatus")}
                        </span>
                        <button
                          type="button"
                          onClick={() => void handleMoveCandidate(idx(), -1)}
                          disabled={idx() === 0}
                          class="rounded p-1 text-[var(--text-muted)] hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)] disabled:opacity-30 cursor-pointer"
                        >
                          <ArrowUp size={11} />
                        </button>
                        <button
                          type="button"
                          onClick={() => void handleMoveCandidate(idx(), 1)}
                          disabled={idx() === effectiveCandidates().length - 1}
                          class="rounded p-1 text-[var(--text-muted)] hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)] disabled:opacity-30 cursor-pointer"
                        >
                          <ArrowDown size={11} />
                        </button>
                        <button
                          type="button"
                          onClick={() => void handleRemoveCandidate(idx())}
                          class="rounded p-1 text-[var(--text-muted)] hover:bg-[var(--err-surface)] hover:text-[var(--err)] cursor-pointer"
                        >
                          <Trash2 size={11} />
                        </button>
                      </div>
                    </div>
                  );
                }}
              </For>
            </div>
          </Show>

          {/* Add Candidate Control */}
          <Show when={addableModels().length > 0 || freeFilterOnly()}>
            <div class="flex items-center gap-2 pt-1">
              <button
                type="button"
                onClick={() => setFreeFilterOnly((v) => !v)}
                class={`shrink-0 rounded-lg border px-2 py-1 text-[11px] font-medium cursor-pointer ${
                  freeFilterOnly()
                    ? "border-[var(--ok)]/40 bg-[var(--ok-surface)] text-[var(--ok-text)]"
                    : "border-[var(--border-subtle)] bg-[var(--bg-panel)] text-[var(--text-muted)]"
                }`}
              >
                {t("models.autoPoolFilterFreeOnly")}
              </button>
              <select
                value={selectedAddKey()}
                onChange={(e) => setSelectedAddKey(e.currentTarget.value)}
                class="flex-1 min-w-0 rounded-xl border border-[var(--border-strong)] bg-[var(--bg-panel)] px-2.5 py-1 text-[12px] text-[var(--text-primary)] outline-none"
              >
                <For each={addableModels()}>
                  {(m) => (
                    <option value={`${m.providerID}/${m.modelID}`}>
                      {m.providerName} / {m.modelName} ({m.modelID})
                      {m.free ? ` · [${t("models.freeModel")}]` : ""}
                    </option>
                  )}
                </For>
              </select>
              <button
                type="button"
                disabled={addableModels().length === 0}
                onClick={() => void handleAddCandidate()}
                class="shrink-0 inline-flex items-center gap-1 rounded-xl border border-[var(--accent-border)] bg-[var(--accent-surface)] px-2.5 py-1 text-[12px] font-semibold text-[var(--accent-primary)] hover:opacity-90 disabled:opacity-40 cursor-pointer"
              >
                <Plus size={12} />
                {t("models.autoPoolAddCandidate")}
              </button>
            </div>
          </Show>
        </div>
      </div>

      {/* Free Channels (fetched dynamically from official Hoxi API) */}
      <Show when={getFreeTierGuideItems().length > 0}>
        <div class="space-y-2.5">
          <div class="flex items-center gap-2 px-0.5">
            <h3 class="text-[13px] font-semibold text-[var(--text-primary)]">
              {t("models.freeHubTitle")}
            </h3>
            <span class="text-[12px] text-[var(--text-muted)]">
              · {t("models.freeHubSubtitle")}
            </span>
          </div>

          <div class="grid grid-cols-1 sm:grid-cols-2 gap-2.5">
            <For each={getFreeTierGuideItems()}>
              {(item) => {
                const keyCount = () => providerKeyCounts()[item.providerID] || 0;
                return (
                  <div
                    title={item.step}
                    class="flex items-center justify-between gap-3 rounded-xl border border-[var(--border-subtle)] bg-[var(--bg-elevated)]/45 px-3.5 py-2.5"
                  >
                    <div class="flex items-center gap-2.5 min-w-0">
                      <ProviderIcon id={item.providerID} class="w-4 h-4 shrink-0" />
                      <div class="min-w-0">
                        <div class="flex items-center gap-1.5">
                          <span class="text-[13px] font-semibold text-[var(--text-primary)] truncate">
                            {item.title}
                          </span>
                          <Show when={keyCount() > 1}>
                            <span class="shrink-0 rounded bg-[var(--accent-surface)] px-1.5 py-0.2 text-[11px] font-semibold text-[var(--accent-primary)]">
                              {t("models.multiKeyActiveBadge", { count: keyCount() })}
                            </span>
                          </Show>
                        </div>
                        <div class="text-[11px] text-[var(--text-muted)] truncate">
                          {item.quota}
                        </div>
                      </div>
                    </div>

                    <div class="flex items-center gap-1.5 shrink-0">
                      <button
                        type="button"
                        onClick={() => openExternalURL(item.signupURL)}
                        class="inline-flex items-center gap-1 rounded-lg border border-[var(--border-subtle)] bg-[var(--bg-panel)] px-2 py-1 text-[11px] font-medium text-[var(--text-secondary)] hover:text-[var(--text-primary)] cursor-pointer"
                      >
                        <ExternalLink size={10} />
                        {t("models.freeHubGetKeyBtn")}
                      </button>
                      <button
                        type="button"
                        onClick={() => void handleConnectGuideItem(item)}
                        class="inline-flex items-center gap-1 rounded-lg border border-[var(--accent-border)] bg-[var(--accent-surface)] px-2.5 py-1 text-[11px] font-semibold text-[var(--accent-primary)] hover:opacity-90 cursor-pointer"
                      >
                        <KeyRound size={10} />
                        {t("models.freeHubConfigureBtn")}
                      </button>
                    </div>
                  </div>
                );
              }}
            </For>
          </div>
        </div>
      </Show>
      </div>
    </div>
  );
}
