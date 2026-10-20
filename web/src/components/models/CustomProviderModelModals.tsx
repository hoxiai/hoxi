// web/src/components/models/CustomProviderModelModals.tsx
import { createMemo, createSignal, For, Show, type Accessor, type Setter } from "solid-js";
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
import { guessFetchedModelDraft, type ModelProbeResult } from "./CustomProviderModelSection";

export function FetchedModelsModal(props: {
  models: Accessor<CustomProviderModelDraft[]>;
  setModels: Setter<CustomProviderModelDraft[]>;
  fetchedModels: Accessor<Array<{ id: string; name: string }>>;
  setFetchedModels: Setter<Array<{ id: string; name: string }>>;
  modelProbes?: Accessor<Record<string, ModelProbeResult>>;
  onProbeModel?: (
    modelID: string
  ) => Promise<{ ok: boolean; latencyMs?: number; error?: string }> | void;
}) {
  const [fetchSearch, setFetchSearch] = createSignal("");

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

  const appendFetchedModel = (rm: { id: string; name: string }) => {
    const cleanId = rm.id.trim();
    if (!cleanId) return;
    props.setModels((prev) => {
      if (prev.some((m) => m.id.trim() === cleanId)) return prev;
      return [
        ...prev.filter((m) => m.id.trim().length > 0),
        guessFetchedModelDraft(rm),
      ];
    });
  };

  const handleVerifyAndAddFetched = async (rm: { id: string; name: string }) => {
    const cleanId = rm.id.trim();
    if (!cleanId || addedIdSet().has(cleanId)) return;
    const currentProbe = props.modelProbes?.()[cleanId];
    if (currentProbe?.status === "testing") return;

    if (!props.onProbeModel) {
      appendFetchedModel(rm);
      return;
    }
    const res = await props.onProbeModel(cleanId);
    if (!res || res.ok) {
      appendFetchedModel(rm);
    }
  };

  return (
    <Show when={props.fetchedModels().length > 0}>
      <div
        class="hoxi-modal-backdrop fixed inset-0 z-50 flex items-center justify-center bg-black/50 backdrop-blur-xs p-4"
        onClick={() => props.setFetchedModels([])}
        onKeyDown={(e) => {
          if (e.key === "Escape") {
            e.preventDefault();
            e.stopPropagation();
            props.setFetchedModels([]);
          }
        }}
      >
        <div
          class="hoxi-modal-panel flex w-full max-w-[620px] max-h-[82dvh] flex-col rounded-2xl border border-[var(--border-strong)] bg-[var(--bg-panel)] shadow-2xl overflow-hidden"
          onClick={(e) => e.stopPropagation()}
        >
          {/* Modal Header */}
          <div class="flex items-start justify-between gap-3 border-b border-[var(--border-subtle)] px-5 py-4">
            <div>
              <h3 class="text-[14px] font-semibold text-[var(--text-primary)]">
                {t("models.fetchedModelsCount", { count: props.fetchedModels().length })}
              </h3>
              <p class="mt-0.5 text-[11.5px] text-[var(--text-muted)]">
                {t("models.fetchedModelsModalSubtitle")}
              </p>
            </div>
            <button
              type="button"
              onClick={() => props.setFetchedModels([])}
              class="rounded-lg p-1 text-[var(--text-muted)] hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)] cursor-pointer"
            >
              <X size={16} />
            </button>
          </div>

          {/* Search Bar */}
          <div class="px-5 pt-3.5 pb-2">
            <div class="flex items-center gap-2 rounded-xl border border-[var(--border-strong)] bg-[var(--bg-elevated)] px-3 py-2">
              <Search size={13} class="text-[var(--text-faint)] shrink-0" />
              <input
                type="text"
                autofocus
                placeholder={t("models.searchModels")}
                value={fetchSearch()}
                onInput={(e) => setFetchSearch(e.currentTarget.value)}
                class="w-full bg-transparent text-[12.5px] text-[var(--text-primary)] outline-none selectable-text"
              />
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
                  const isAdded = () => addedIdSet().has(rm.id.trim());
                  const probe = () => props.modelProbes?.()[rm.id.trim()];
                  const guessed = () => guessFetchedModelDraft(rm);
                  const isTesting = () => probe()?.status === "testing";

                  return (
                    <div class="rounded-xl border border-[var(--border-subtle)] bg-[var(--bg-elevated)]/50 px-3.5 py-2.5 space-y-1.5 hover:border-[var(--border-strong)] transition-colors">
                      <div class="flex items-center justify-between gap-3">
                        <div class="min-w-0 flex-1">
                          <div class="flex items-center gap-2 flex-wrap">
                            <span class="font-mono text-[12.5px] font-semibold text-[var(--text-primary)] truncate">
                              {rm.id}
                            </span>
                            <Show when={guessed().reasoning}>
                              <span class="inline-flex items-center gap-0.5 rounded-md bg-[var(--accent-surface)] px-1.5 py-0.5 text-[10px] font-medium text-[var(--accent-primary)]">
                                <Sparkles size={10} />
                                {t("models.reasoningCap")}
                              </span>
                            </Show>
                            <Show when={guessed().image}>
                              <span class="inline-flex items-center gap-0.5 rounded-md bg-[var(--bg-inset)] px-1.5 py-0.5 text-[10px] font-medium text-[var(--text-secondary)]">
                                <Image size={10} />
                                {t("models.imageCap")}
                              </span>
                            </Show>
                            <Show when={probe()?.status === "ok"}>
                              <span class="inline-flex items-center gap-0.5 rounded-md bg-[var(--ok-surface)] px-1.5 py-0.5 font-mono text-[10px] font-medium text-[var(--ok-text)]">
                                <Check size={10} />
                                {probe()?.latencyMs ?? 0}ms
                              </span>
                            </Show>
                          </div>
                        </div>

                        <div class="flex items-center gap-1.5 shrink-0">
                          <Show
                            when={!isAdded()}
                            fallback={
                              <span class="inline-flex items-center gap-1 rounded-xl border border-[var(--ok-border)] bg-[var(--ok-surface)] px-3 py-1 text-[11.5px] font-semibold text-[var(--ok-text)]">
                                <Check size={12} />
                                {t("models.modelAddedBadge")}
                              </span>
                            }
                          >
                            <Show when={probe()?.status === "error"}>
                              <button
                                type="button"
                                onClick={() => appendFetchedModel(rm)}
                                class="rounded-xl border border-[var(--border-strong)] bg-[var(--bg-panel)] px-2.5 py-1 text-[11px] font-medium text-[var(--text-secondary)] hover:text-[var(--text-primary)] cursor-pointer"
                              >
                                {t("models.addWithoutVerify")}
                              </button>
                            </Show>
                            <button
                              type="button"
                              disabled={isTesting()}
                              onClick={() => void handleVerifyAndAddFetched(rm)}
                              class="inline-flex items-center gap-1.5 rounded-xl bg-[var(--accent-primary)] px-3 py-1 text-[11.5px] font-medium text-[var(--accent-foreground)] hover:opacity-90 disabled:opacity-50 cursor-pointer"
                            >
                              <Show
                                when={!isTesting()}
                                fallback={<LoaderCircle size={12} class="animate-spin" />}
                              >
                                <Plus size={12} />
                              </Show>
                              <span>
                                {isTesting()
                                  ? t("models.verifyingModel")
                                  : t("models.verifyAndAddModel")}
                              </span>
                            </button>
                          </Show>
                        </div>
                      </div>

                      <Show when={probe()?.status === "error" && probe()?.error}>
                        <div class="flex items-start gap-1.5 text-[11px] text-[var(--danger-text)] font-mono">
                          <AlertCircle size={12} class="shrink-0 mt-0.5" />
                          <span class="break-words">{probe()!.error}</span>
                        </div>
                      </Show>
                    </div>
                  );
                }}
              </For>
            </Show>
          </div>

          {/* Modal Footer */}
          <div class="flex items-center justify-between border-t border-[var(--border-subtle)] bg-[var(--bg-elevated)]/30 px-5 py-3">
            <span class="text-[11.5px] text-[var(--text-muted)]">
              {t("models.modelsListTitle")}: {addedIdSet().size}
            </span>
            <button
              type="button"
              onClick={() => props.setFetchedModels([])}
              class="rounded-xl bg-[var(--accent-primary)] px-4 py-1.5 text-[12px] font-medium text-[var(--accent-foreground)] hover:opacity-90 cursor-pointer"
            >
              {t("common.confirm")}
            </button>
          </div>
        </div>
      </div>
    </Show>
  );
}

export function AddCustomModelModal(props: {
  open: Accessor<boolean>;
  onClose: () => void;
  models: Accessor<CustomProviderModelDraft[]>;
  setModels: Setter<CustomProviderModelDraft[]>;
  onProbeModel?: (
    modelID: string
  ) => Promise<{ ok: boolean; latencyMs?: number; error?: string }> | void;
}) {
  const [draftId, setDraftId] = createSignal("");
  const [draftName, setDraftName] = createSignal("");
  const [draftReasoning, setDraftReasoning] = createSignal(false);
  const [draftImage, setDraftImage] = createSignal(false);
  const [manualCaps, setManualCaps] = createSignal(false);
  const [verifying, setVerifying] = createSignal(false);
  const [verifyError, setVerifyError] = createSignal<string | null>(null);

  const resetAndClose = () => {
    setDraftId("");
    setDraftName("");
    setDraftReasoning(false);
    setDraftImage(false);
    setManualCaps(false);
    setVerifying(false);
    setVerifyError(null);
    props.onClose();
  };

  const commitDraftModel = () => {
    const cleanId = draftId().trim();
    if (!cleanId) return;
    const cleanName = draftName().trim() || cleanId;
    props.setModels((prev) => [
      ...prev.filter((m) => m.id.trim().length > 0 && m.id.trim() !== cleanId),
      {
        id: cleanId,
        name: cleanName,
        reasoning: draftReasoning(),
        image: draftImage(),
      },
    ]);
    resetAndClose();
  };

  const handleVerifyAndAdd = async () => {
    const cleanId = draftId().trim();
    if (!cleanId || verifying()) return;
    if (props.models().some((m) => m.id.trim() === cleanId)) {
      setVerifyError(t("models.modelAlreadyExists", { id: cleanId }));
      return;
    }
    if (!props.onProbeModel) {
      commitDraftModel();
      return;
    }
    setVerifying(true);
    setVerifyError(null);
    try {
      const res = await props.onProbeModel(cleanId);
      if (!res || res.ok) {
        commitDraftModel();
      } else {
        setVerifyError(res.error || t("models.connectFailed"));
      }
    } catch (err: any) {
      setVerifyError(err?.message || t("models.connectFailed"));
    } finally {
      setVerifying(false);
    }
  };

  return (
    <Show when={props.open()}>
      <div
        class="hoxi-modal-backdrop fixed inset-0 z-50 flex items-center justify-center bg-black/50 backdrop-blur-xs p-4"
        onClick={() => !verifying() && resetAndClose()}
        onKeyDown={(e) => {
          if (e.key === "Escape") {
            e.preventDefault();
            e.stopPropagation();
            if (!verifying()) resetAndClose();
          }
        }}
      >
        <div
          class="hoxi-modal-panel w-full max-w-[460px] rounded-2xl border border-[var(--border-strong)] bg-[var(--bg-panel)] p-5 shadow-2xl space-y-4"
          onClick={(e) => e.stopPropagation()}
        >
          <div class="flex items-start justify-between gap-3">
            <div>
              <h3 class="text-[14px] font-semibold text-[var(--text-primary)]">
                {t("models.addModelModalTitle")}
              </h3>
              <p class="mt-0.5 text-[11.5px] text-[var(--text-muted)]">
                {t("models.addModelModalDesc")}
              </p>
            </div>
            <button
              type="button"
              disabled={verifying()}
              onClick={resetAndClose}
              class="rounded-lg p-1 text-[var(--text-muted)] hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)] cursor-pointer"
            >
              <X size={15} />
            </button>
          </div>

          <div class="space-y-3">
            <div>
              <label class="block text-[11.5px] font-medium text-[var(--text-secondary)] mb-1">
                {t("models.modelId")}
              </label>
              <input
                type="text"
                autofocus
                disabled={verifying()}
                autocapitalize="off"
                autocorrect="off"
                spellcheck={false}
                placeholder={t("models.modelIdPlaceholder")}
                value={draftId()}
                onInput={(e) => {
                  const val = e.currentTarget.value;
                  setDraftId(val);
                  setVerifyError(null);
                  if (!manualCaps()) {
                    const guessed = guessFetchedModelDraft({ id: val, name: val });
                    setDraftReasoning(guessed.reasoning);
                    setDraftImage(guessed.image);
                  }
                }}
                onKeyDown={(e) => {
                  if (e.key === "Enter") {
                    e.preventDefault();
                    void handleVerifyAndAdd();
                  }
                }}
                class="w-full rounded-xl border border-[var(--border-strong)] bg-[var(--bg-elevated)] px-3 py-2 font-mono text-[12.5px] text-[var(--text-primary)] outline-none focus:border-[var(--accent-primary)] selectable-text"
              />
            </div>

            <div>
              <label class="block text-[11.5px] font-medium text-[var(--text-secondary)] mb-1">
                {t("models.modelName")}{" "}
                <span class="font-normal text-[var(--text-faint)]">({t("models.optional")})</span>
              </label>
              <input
                type="text"
                disabled={verifying()}
                placeholder={draftId().trim() || t("models.modelName")}
                value={draftName()}
                onInput={(e) => setDraftName(e.currentTarget.value)}
                onKeyDown={(e) => {
                  if (e.key === "Enter") {
                    e.preventDefault();
                    void handleVerifyAndAdd();
                  }
                }}
                class="w-full rounded-xl border border-[var(--border-subtle)] bg-[var(--bg-elevated)] px-3 py-2 text-[12.5px] text-[var(--text-primary)] outline-none focus:border-[var(--accent-primary)] selectable-text"
              />
            </div>

            <div class="flex items-center gap-2 pt-0.5">
              <button
                type="button"
                disabled={verifying()}
                onClick={() => {
                  setManualCaps(true);
                  setDraftReasoning((v) => !v);
                }}
                class={`flex-1 inline-flex items-center justify-center gap-1.5 rounded-xl border px-3 py-2 text-[12px] font-medium transition-colors cursor-pointer ${
                  draftReasoning()
                    ? "border-[var(--accent-border)] bg-[var(--accent-surface)] text-[var(--accent-primary)]"
                    : "border-[var(--border-subtle)] bg-[var(--bg-elevated)] text-[var(--text-muted)]"
                }`}
              >
                <Sparkles size={13} />
                <span>{t("models.supportsReasoning")}</span>
              </button>
              <button
                type="button"
                disabled={verifying()}
                onClick={() => {
                  setManualCaps(true);
                  setDraftImage((v) => !v);
                }}
                class={`flex-1 inline-flex items-center justify-center gap-1.5 rounded-xl border px-3 py-2 text-[12px] font-medium transition-colors cursor-pointer ${
                  draftImage()
                    ? "border-[var(--accent-border)] bg-[var(--accent-surface)] text-[var(--accent-primary)]"
                    : "border-[var(--border-subtle)] bg-[var(--bg-elevated)] text-[var(--text-muted)]"
                }`}
              >
                <Image size={13} />
                <span>{t("models.imageCap")}</span>
              </button>
            </div>

            <Show when={verifyError()}>
              <div class="flex items-start gap-2 rounded-xl border border-[var(--danger-border)] bg-[var(--danger-surface)] px-3 py-2 text-[11.5px] text-[var(--danger-text)]">
                <AlertCircle size={14} class="shrink-0 mt-0.5" />
                <span class="break-words">{verifyError()}</span>
              </div>
            </Show>
          </div>

          <div class="flex items-center justify-end gap-2 border-t border-[var(--border-subtle)] pt-3.5">
            <button
              type="button"
              disabled={verifying()}
              onClick={resetAndClose}
              class="rounded-xl border border-[var(--border-subtle)] bg-[var(--bg-elevated)] px-3.5 py-1.5 text-[12px] font-medium text-[var(--text-secondary)] hover:text-[var(--text-primary)] cursor-pointer"
            >
              {t("common.cancel")}
            </button>
            <Show when={verifyError() && draftId().trim()}>
              <button
                type="button"
                disabled={verifying()}
                onClick={commitDraftModel}
                class="rounded-xl border border-[var(--border-strong)] bg-[var(--bg-panel)] px-3.5 py-1.5 text-[12px] font-medium text-[var(--text-primary)] hover:bg-[var(--bg-hover)] cursor-pointer"
              >
                {t("models.addWithoutVerify")}
              </button>
            </Show>
            <button
              type="button"
              disabled={!draftId().trim() || verifying()}
              onClick={() => void handleVerifyAndAdd()}
              class="inline-flex items-center gap-1.5 rounded-xl bg-[var(--accent-primary)] px-4 py-1.5 text-[12px] font-medium text-[var(--accent-foreground)] hover:opacity-90 disabled:opacity-50 cursor-pointer"
            >
              <Show when={!verifying()} fallback={<LoaderCircle size={13} class="animate-spin" />}>
                <Check size={13} />
              </Show>
              <span>
                {verifying() ? t("models.verifyingModel") : t("models.verifyAndAddModel")}
              </span>
            </button>
          </div>
        </div>
      </div>
    </Show>
  );
}
