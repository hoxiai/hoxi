// web/src/components/models/FetchedModelsModal.tsx
import { createEffect, createMemo, createSignal, For, Show, type Accessor, type Setter } from "solid-js";
import { Presence } from "../motion/Presence";
import {
  AlertCircle,
  Check,
  Image,
  LoaderCircle,
  Plus,
  Search,
  Sparkles,
  X,
} from "lucide-solid";
import type { CustomProviderModelDraft } from "../../store/workspace";
import { t } from "../../i18n";
import { guessFetchedModelDraft, type ModelProbeResult } from "./custom-endpoint-helpers";
import { appendModels, verifyBatch, type ModelProbeFn } from "./model-probe-batch";

export function FetchedModelsModal(props: {
  models: Accessor<CustomProviderModelDraft[]>;
  setModels: Setter<CustomProviderModelDraft[]>;
  fetchedModels: Accessor<Array<{ id: string; name: string }>>;
  setFetchedModels: Setter<Array<{ id: string; name: string }>>;
  modelProbes?: Accessor<Record<string, ModelProbeResult>>;
  onProbeModel?: ModelProbeFn;
}) {
  const [fetchSearch, setFetchSearch] = createSignal("");
  const [selected, setSelected] = createSignal<Record<string, boolean>>({});
  const [verifying, setVerifying] = createSignal(false);
  const [verifyErrors, setVerifyErrors] = createSignal<Record<string, string>>({});

  createEffect(() => {
    const key = props.fetchedModels().map((m) => m.id).join("\u0000");
    if (!key) return;
    setSelected({});
    setVerifyErrors({});
  });

  const filteredFetchedModels = createMemo(() => {
    const q = fetchSearch().trim().toLowerCase();
    const list = props.fetchedModels();
    if (!q) return list;
    return list.filter(
      (m) => m.id.toLowerCase().includes(q) || m.name.toLowerCase().includes(q)
    );
  });

  const addedIdSet = createMemo(
    () => new Set(props.models().map((m) => m.id.trim()).filter(Boolean))
  );

  const selectableModels = createMemo(() =>
    filteredFetchedModels().filter((m) => m.id.trim() && !addedIdSet().has(m.id.trim()))
  );

  const selectedModels = createMemo(() => {
    const sel = selected();
    return props
      .fetchedModels()
      .filter((m) => sel[m.id.trim()] && !addedIdSet().has(m.id.trim()));
  });

  const allFilteredSelected = createMemo(
    () =>
      selectableModels().length > 0 && selectableModels().every((m) => selected()[m.id.trim()])
  );

  const toggleSelect = (id: string) => {
    const clean = id.trim();
    setSelected((prev) => ({ ...prev, [clean]: !prev[clean] }));
    setVerifyErrors((prev) => {
      if (!(clean in prev)) return prev;
      const next = { ...prev };
      delete next[clean];
      return next;
    });
  };

  const toggleSelectAll = () => {
    const list = selectableModels();
    if (list.length === 0) return;
    const turnOn = !allFilteredSelected();
    setSelected((prev) => {
      const next = { ...prev };
      for (const m of list) next[m.id.trim()] = turnOn;
      return next;
    });
  };

  const handleVerifySelected = async () => {
    if (verifying()) return;
    const chosen = selectedModels();
    if (chosen.length === 0) return;

    if (!props.onProbeModel) {
      appendModels(props.setModels, chosen.map(guessFetchedModelDraft));
      setSelected({});
      return;
    }

    setVerifying(true);
    setVerifyErrors({});
    try {
      const results = await verifyBatch(
        chosen.map((m) => m.id.trim()),
        props.onProbeModel
      );
      const ok: Array<{ id: string; name: string }> = [];
      const errors: Record<string, string> = {};
      for (const m of chosen) {
        const id = m.id.trim();
        const res = results[id];
        if (!res || res.ok) {
          ok.push(m);
        } else {
          errors[id] = res.error || t("models.connectFailed");
        }
      }
      if (ok.length > 0) appendModels(props.setModels, ok.map(guessFetchedModelDraft));
      setSelected((prev) => {
        const next = { ...prev };
        for (const m of ok) delete next[m.id.trim()];
        return next;
      });
      setVerifyErrors(errors);
    } finally {
      setVerifying(false);
    }
  };

  const handleAddWithoutVerify = (rm: { id: string; name: string }) => {
    appendModels(props.setModels, [guessFetchedModelDraft(rm)]);
    setSelected((prev) => {
      const next = { ...prev };
      delete next[rm.id.trim()];
      return next;
    });
    setVerifyErrors((prev) => {
      if (!(rm.id.trim() in prev)) return prev;
      const next = { ...prev };
      delete next[rm.id.trim()];
      return next;
    });
  };

  return (
    <Presence when={props.fetchedModels().length > 0}>
      {(state) => (
      <div
        data-state={state()}
        class="hoxi-modal-backdrop fixed inset-0 z-50 flex items-center justify-center p-4"
        onClick={() => !verifying() && props.setFetchedModels([])}
        onKeyDown={(e) => {
          if (e.key === "Escape" && !verifying()) {
            e.preventDefault();
            e.stopPropagation();
            props.setFetchedModels([]);
          }
        }}
      >
        <div
          class="hoxi-modal-panel hoxi-elevation-4 flex w-full max-w-[620px] max-h-[82dvh] flex-col rounded-2xl border border-[var(--border-strong)] bg-[var(--bg-panel)] shadow-2xl overflow-hidden"
          onClick={(e) => e.stopPropagation()}
        >
          {/* Modal Header */}
          <div class="flex items-start justify-between gap-3 border-b border-[var(--border-subtle)] px-5 py-4">
            <div>
              <h3 class="text-[15px] font-semibold text-[var(--text-primary)]">
                {t("models.fetchedModelsCount", { count: props.fetchedModels().length })}
              </h3>
              <p class="mt-0.5 text-[12px] text-[var(--text-muted)]">
                {t("models.fetchedModelsModalSubtitle")}
              </p>
            </div>
            <button
              type="button"
              disabled={verifying()}
              onClick={() => props.setFetchedModels([])}
              class="rounded-lg p-1 text-[var(--text-muted)] hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)] cursor-pointer disabled:opacity-50"
            >
              <X size={16} />
            </button>
          </div>

          {/* Search Bar */}
          <div class="px-5 pt-3.5 pb-2 space-y-2">
            <div class="flex items-center gap-2 rounded-xl border border-[var(--border-strong)] bg-[var(--bg-elevated)] px-3 py-2">
              <Search size={13} class="text-[var(--text-faint)] shrink-0" />
              <input
                type="text"
                autofocus
                placeholder={t("models.searchModels")}
                value={fetchSearch()}
                onInput={(e) => setFetchSearch(e.currentTarget.value)}
                class="w-full bg-transparent text-[13px] text-[var(--text-primary)] outline-none selectable-text"
              />
            </div>
            <div class="flex items-center justify-between px-1">
              <button
                type="button"
                disabled={selectableModels().length === 0 || verifying()}
                onClick={toggleSelectAll}
                class="text-[11px] font-medium text-[var(--accent-primary)] hover:underline disabled:opacity-40 cursor-pointer"
              >
                {allFilteredSelected() ? t("models.deselectAll") : t("models.selectAll")}
              </button>
              <span class="text-[11px] text-[var(--text-muted)]">
                {t("models.selectedCount", { count: selectedModels().length })}
              </span>
            </div>
          </div>

          {/* Fetched Models List */}
          <div class="flex-1 min-h-0 overflow-y-auto px-5 py-2 space-y-2">
            <Show
              when={filteredFetchedModels().length > 0}
              fallback={
                <div class="py-8 text-center text-[12px] text-[var(--text-muted)]">
                  {t("models.noModelsFound")}
                </div>
              }
            >
              <For each={filteredFetchedModels()}>
                {(rm) => {
                  const cleanId = () => rm.id.trim();
                  const isAdded = () => addedIdSet().has(cleanId());
                  const probe = () => props.modelProbes?.()[cleanId()];
                  const guessed = () => guessFetchedModelDraft(rm);
                  const isTesting = () => probe()?.status === "testing";

                  return (
                    <div class="rounded-xl border border-[var(--border-subtle)] bg-[var(--bg-elevated)]/50 px-3.5 py-2.5 space-y-1.5 hover:border-[var(--border-strong)] transition-colors">
                      <div class="flex items-start gap-2.5">
                        <input
                          type="checkbox"
                          checked={Boolean(selected()[cleanId()])}
                          disabled={isAdded() || isTesting()}
                          onChange={() => toggleSelect(cleanId())}
                          class="mt-0.5 h-3.5 w-3.5 shrink-0 cursor-pointer accent-[var(--accent-primary)] disabled:opacity-40"
                        />
                        <div class="min-w-0 flex-1 space-y-1">
                          <div class="flex items-center gap-2 flex-wrap">
                            <span class="font-mono text-[13px] font-semibold text-[var(--text-primary)] truncate">
                              {rm.id}
                            </span>
                            <Show when={guessed().reasoning}>
                              <span class="inline-flex items-center gap-0.5 rounded-md bg-[var(--accent-surface)] px-1.5 py-0.5 text-[11px] font-medium text-[var(--accent-primary)]">
                                <Sparkles size={10} />
                                {t("models.reasoningCap")}
                              </span>
                            </Show>
                            <Show when={guessed().image}>
                              <span class="inline-flex items-center gap-0.5 rounded-md bg-[var(--bg-inset)] px-1.5 py-0.5 text-[11px] font-medium text-[var(--text-secondary)]">
                                <Image size={10} />
                                {t("models.imageCap")}
                              </span>
                            </Show>
                            <Show when={probe()?.status === "ok"}>
                              <span class="inline-flex items-center gap-0.5 rounded-md bg-[var(--ok-surface)] px-1.5 py-0.5 font-mono text-[11px] font-medium text-[var(--ok-text)]">
                                <Check size={10} />
                                {probe()?.latencyMs ?? 0}ms
                              </span>
                            </Show>
                            <Show when={isAdded()}>
                              <span class="inline-flex items-center gap-1 rounded-md bg-[var(--ok-surface)] px-1.5 py-0.5 text-[11px] font-semibold text-[var(--ok-text)]">
                                <Check size={10} />
                                {t("models.modelAddedBadge")}
                              </span>
                            </Show>
                          </div>

                          <Show when={verifyErrors()[cleanId()]}>
                            <div class="flex items-start justify-between gap-2 text-[11px] text-[var(--danger-text)] font-mono">
                              <div class="flex items-start gap-1.5 min-w-0">
                                <AlertCircle size={12} class="shrink-0 mt-0.5" />
                                <span class="break-words">{verifyErrors()[cleanId()]}</span>
                              </div>
                              <button
                                type="button"
                                disabled={isTesting()}
                                onClick={() => handleAddWithoutVerify(rm)}
                                class="shrink-0 rounded-lg border border-[var(--border-strong)] bg-[var(--bg-panel)] px-2 py-0.5 text-[11px] font-medium text-[var(--text-secondary)] hover:text-[var(--text-primary)] cursor-pointer disabled:opacity-50"
                              >
                                {t("models.addWithoutVerify")}
                              </button>
                            </div>
                          </Show>
                        </div>

                        <Show when={isTesting()}>
                          <LoaderCircle size={13} class="shrink-0 mt-0.5 animate-spin text-[var(--accent-primary)]" />
                        </Show>
                      </div>
                    </div>
                  );
                }}
              </For>
            </Show>
          </div>

          {/* Modal Footer */}
          <div class="flex items-center justify-between gap-3 border-t border-[var(--border-subtle)] bg-[var(--bg-elevated)]/30 px-5 py-3">
            <span class="text-[12px] text-[var(--text-muted)]">
              {t("models.modelsListTitle")}: {addedIdSet().size}
            </span>
            <div class="flex items-center gap-2">
              <button
                type="button"
                disabled={verifying()}
                onClick={() => props.setFetchedModels([])}
                class="rounded-xl border border-[var(--border-subtle)] bg-[var(--bg-elevated)] px-3.5 py-1.5 text-[12px] font-medium text-[var(--text-secondary)] hover:text-[var(--text-primary)] cursor-pointer disabled:opacity-50"
              >
                {t("common.confirm")}
              </button>
              <button
                type="button"
                disabled={selectedModels().length === 0 || verifying()}
                onClick={() => void handleVerifySelected()}
                class="inline-flex items-center gap-1.5 rounded-xl bg-[var(--accent-primary)] px-4 py-1.5 text-[12px] font-medium text-[var(--accent-foreground)] hover:opacity-90 disabled:opacity-50 cursor-pointer"
              >
                <Show when={!verifying()} fallback={<LoaderCircle size={13} class="animate-spin" />}>
                  <Plus size={13} />
                </Show>
                <span>
                  {verifying()
                    ? t("models.verifyingModel")
                    : t("models.verifyAndAddSelected", { count: selectedModels().length })}
                </span>
              </button>
            </div>
          </div>
        </div>
      </div>
      )}
    </Presence>
  );
}
