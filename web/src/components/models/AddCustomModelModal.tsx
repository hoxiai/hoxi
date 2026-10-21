// web/src/components/models/AddCustomModelModal.tsx
import {
  createEffect,
  createMemo,
  createSignal,
  For,
  onCleanup,
  Show,
  type Accessor,
  type Setter,
} from "solid-js";
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
import {
  extractFlattenedModels,
  rankModelSearch,
  workspace,
  type CustomProviderModelDraft,
  type FlattenedModel,
} from "../../store/workspace";
import { t } from "../../i18n";
import { guessFetchedModelDraft } from "./custom-endpoint-helpers";
import { appendModels, verifyBatch, type ModelProbeFn } from "./model-probe-batch";

const DEFAULT_CANDIDATE_LIMIT = 60;
const SEARCH_CANDIDATE_LIMIT = 120;
const SEARCH_DEBOUNCE_MS = 140;

export function AddCustomModelModal(props: {
  open: Accessor<boolean>;
  onClose: () => void;
  models: Accessor<CustomProviderModelDraft[]>;
  setModels: Setter<CustomProviderModelDraft[]>;
  onProbeModel?: ModelProbeFn;
}) {
  const [query, setQuery] = createSignal("");
  const [rankedQuery, setRankedQuery] = createSignal("");
  const [selected, setSelected] = createSignal<Record<string, boolean>>({});
  const [verifying, setVerifying] = createSignal(false);
  const [verifyErrors, setVerifyErrors] = createSignal<Record<string, string>>({});
  const [manualOpen, setManualOpen] = createSignal(false);
  const [draftId, setDraftId] = createSignal("");
  const [draftName, setDraftName] = createSignal("");
  const [draftReasoning, setDraftReasoning] = createSignal(false);
  const [draftImage, setDraftImage] = createSignal(false);
  const [manualCaps, setManualCaps] = createSignal(false);
  const [manualError, setManualError] = createSignal<string | null>(null);

  createEffect(() => {
    if (!props.open()) return;
    setQuery("");
    setRankedQuery("");
    setSelected({});
    setVerifyErrors({});
    setVerifying(false);
    setManualOpen(false);
    setDraftId("");
    setDraftName("");
    setDraftReasoning(false);
    setDraftImage(false);
    setManualCaps(false);
    setManualError(null);
  });

  // Ranking every keystroke over the whole catalog is wasted work while the
  // user is still typing; only the debounced value drives the search.
  createEffect(() => {
    const q = query();
    if (!q) {
      setRankedQuery("");
      return;
    }
    const timer = setTimeout(() => setRankedQuery(q), SEARCH_DEBOUNCE_MS);
    onCleanup(() => clearTimeout(timer));
  });

  const addedIdSet = createMemo(
    () => new Set(props.models().map((m) => m.id.trim()).filter(Boolean))
  );

  // Known models from every provider Hoxi has loaded; runner (CLI) models are
  // not reachable through a custom HTTP endpoint, so they are excluded.
  // Skipped entirely while the modal is closed so the catalog is not flattened
  // on every provider refresh.
  const allCandidates = createMemo<FlattenedModel[]>(() => {
    if (!props.open()) return [];
    const connected = new Set(workspace.connectedProviders);
    const flat = extractFlattenedModels(workspace.providers, connected, workspace.globalConfig || {});
    const byID = new Map<string, FlattenedModel>();
    for (const m of flat) {
      if (m.source === "runner") continue;
      const id = m.modelID.trim();
      if (!id) continue;
      const prev = byID.get(id);
      if (!prev || (!prev.connected && m.connected)) {
        byID.set(id, m);
      }
    }
    return Array.from(byID.values()).sort(
      (a, b) => Number(b.connected) - Number(a.connected) || a.modelID.localeCompare(b.modelID)
    );
  });

  const rankedCandidates = createMemo(() => {
    const q = rankedQuery().trim();
    if (!q) return allCandidates();
    return rankModelSearch(allCandidates(), q);
  });

  const candidateLimit = () =>
    rankedQuery().trim() ? SEARCH_CANDIDATE_LIMIT : DEFAULT_CANDIDATE_LIMIT;

  const filteredCandidates = createMemo(() => rankedCandidates().slice(0, candidateLimit()));

  const resultsTruncated = createMemo(() => rankedCandidates().length > candidateLimit());

  const candidateByID = createMemo(() => {
    const map = new Map<string, FlattenedModel>();
    for (const m of allCandidates()) map.set(m.modelID, m);
    return map;
  });

  const selectableCandidates = createMemo(() =>
    filteredCandidates().filter((m) => !addedIdSet().has(m.modelID))
  );

  const selectedCandidates = createMemo(() => {
    const sel = selected();
    const byID = candidateByID();
    const out: FlattenedModel[] = [];
    for (const id of Object.keys(sel)) {
      if (!sel[id]) continue;
      const found = byID.get(id);
      if (found && !addedIdSet().has(id)) out.push(found);
    }
    return out;
  });

  const allFilteredSelected = createMemo(
    () =>
      selectableCandidates().length > 0 &&
      selectableCandidates().every((m) => selected()[m.modelID])
  );

  const toggleSelect = (id: string) => {
    setSelected((prev) => ({ ...prev, [id]: !prev[id] }));
    setVerifyErrors((prev) => {
      if (!(id in prev)) return prev;
      const next = { ...prev };
      delete next[id];
      return next;
    });
  };

  const toggleSelectAll = () => {
    const list = selectableCandidates();
    if (list.length === 0) return;
    const turnOn = !allFilteredSelected();
    setSelected((prev) => {
      const next = { ...prev };
      for (const m of list) next[m.modelID] = turnOn;
      return next;
    });
  };

  const commitManualModel = () => {
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
    setDraftId("");
    setDraftName("");
    setDraftReasoning(false);
    setDraftImage(false);
    setManualCaps(false);
    setManualError(null);
  };

  const handleVerifyManual = async () => {
    const cleanId = draftId().trim();
    if (!cleanId || verifying()) return;
    if (props.models().some((m) => m.id.trim() === cleanId)) {
      setManualError(t("models.modelAlreadyExists", { id: cleanId }));
      return;
    }
    if (!props.onProbeModel) {
      commitManualModel();
      return;
    }
    setVerifying(true);
    setManualError(null);
    try {
      const res = await props.onProbeModel(cleanId);
      if (!res || res.ok) {
        commitManualModel();
      } else {
        setManualError(res.error || t("models.connectFailed"));
      }
    } catch (err: any) {
      setManualError(err?.message || t("models.connectFailed"));
    } finally {
      setVerifying(false);
    }
  };

  const handleVerifySelected = async () => {
    if (verifying()) return;
    const chosen = selectedCandidates();
    if (chosen.length === 0) return;

    const toDraft = (m: FlattenedModel): CustomProviderModelDraft => ({
      id: m.modelID,
      name: m.modelName || m.modelID,
      reasoning: m.reasoning,
      image: m.image,
    });

    if (!props.onProbeModel) {
      appendModels(props.setModels, chosen.map(toDraft));
      setSelected({});
      return;
    }

    setVerifying(true);
    setVerifyErrors({});
    try {
      const results = await verifyBatch(
        chosen.map((m) => m.modelID),
        props.onProbeModel
      );
      const ok: FlattenedModel[] = [];
      const errors: Record<string, string> = {};
      for (const m of chosen) {
        const res = results[m.modelID];
        if (!res || res.ok) {
          ok.push(m);
        } else {
          errors[m.modelID] = res.error || t("models.connectFailed");
        }
      }
      if (ok.length > 0) appendModels(props.setModels, ok.map(toDraft));
      setSelected((prev) => {
        const next = { ...prev };
        for (const m of ok) delete next[m.modelID];
        return next;
      });
      setVerifyErrors(errors);
    } finally {
      setVerifying(false);
    }
  };

  const handleAddCandidateWithoutVerify = (m: FlattenedModel) => {
    appendModels(props.setModels, [
      { id: m.modelID, name: m.modelName || m.modelID, reasoning: m.reasoning, image: m.image },
    ]);
    setSelected((prev) => {
      const next = { ...prev };
      delete next[m.modelID];
      return next;
    });
    setVerifyErrors((prev) => {
      if (!(m.modelID in prev)) return prev;
      const next = { ...prev };
      delete next[m.modelID];
      return next;
    });
  };

  return (
    <Presence when={props.open()}>
      {(state) => (
      <div
        data-state={state()}
        class="hoxi-modal-backdrop fixed inset-0 z-50 flex items-center justify-center p-4"
        onClick={() => !verifying() && props.onClose()}
        onKeyDown={(e) => {
          if (e.key === "Escape") {
            e.preventDefault();
            e.stopPropagation();
            if (!verifying()) props.onClose();
          }
        }}
      >
        <div
          class="hoxi-modal-panel hoxi-elevation-4 flex w-full max-w-[620px] max-h-[82dvh] flex-col rounded-2xl border border-[var(--border-strong)] bg-[var(--bg-panel)] shadow-2xl overflow-hidden"
          onClick={(e) => e.stopPropagation()}
        >
          <div class="flex items-start justify-between gap-3 border-b border-[var(--border-subtle)] px-5 py-4">
            <div>
              <h3 class="text-[15px] font-semibold text-[var(--text-primary)]">
                {t("models.addModelModalTitle")}
              </h3>
              <p class="mt-0.5 text-[12px] text-[var(--text-muted)]">
                {t("models.addModelModalDesc")}
              </p>
            </div>
            <button
              type="button"
              disabled={verifying()}
              onClick={props.onClose}
              class="rounded-lg p-1 text-[var(--text-muted)] hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)] cursor-pointer disabled:opacity-50"
            >
              <X size={15} />
            </button>
          </div>

          {/* Searchable candidate list */}
          <div class="px-5 pt-3.5 pb-2 space-y-2">
            <div class="flex items-center gap-2 rounded-xl border border-[var(--border-strong)] bg-[var(--bg-elevated)] px-3 py-2">
              <Search size={13} class="text-[var(--text-faint)] shrink-0" />
              <input
                type="text"
                autofocus
                disabled={verifying()}
                placeholder={t("models.searchModels")}
                value={query()}
                onInput={(e) => setQuery(e.currentTarget.value)}
                class="w-full bg-transparent text-[13px] text-[var(--text-primary)] outline-none selectable-text"
              />
            </div>
            <div class="flex items-center justify-between px-1">
              <button
                type="button"
                disabled={selectableCandidates().length === 0 || verifying()}
                onClick={toggleSelectAll}
                class="text-[11px] font-medium text-[var(--accent-primary)] hover:underline disabled:opacity-40 cursor-pointer"
              >
                {allFilteredSelected() ? t("models.deselectAll") : t("models.selectAll")}
              </button>
              <span class="text-[11px] text-[var(--text-muted)]">
                {t("models.selectedCount", { count: selectedCandidates().length })}
              </span>
            </div>
            <Show when={resultsTruncated()}>
              <div class="px-1 text-[11px] text-[var(--text-faint)]">
                {t("models.candidateListTruncated", { limit: candidateLimit() })}
              </div>
            </Show>
          </div>

          <div class="flex-1 min-h-0 overflow-y-auto px-5 py-2 space-y-2">
            <Show
              when={filteredCandidates().length > 0}
              fallback={
                <div class="py-8 text-center text-[12px] text-[var(--text-muted)]">
                  {t("models.noCandidateModels")}
                </div>
              }
            >
              <For each={filteredCandidates()}>
                {(m) => {
                  const isAdded = () => addedIdSet().has(m.modelID);
                  return (
                    <div class="rounded-xl border border-[var(--border-subtle)] bg-[var(--bg-elevated)]/50 px-3.5 py-2.5 hover:border-[var(--border-strong)] transition-colors">
                      <div class="flex items-start gap-2.5">
                        <input
                          type="checkbox"
                          checked={Boolean(selected()[m.modelID])}
                          disabled={isAdded() || verifying()}
                          onChange={() => toggleSelect(m.modelID)}
                          class="mt-0.5 h-3.5 w-3.5 shrink-0 cursor-pointer accent-[var(--accent-primary)] disabled:opacity-40"
                        />
                        <div class="min-w-0 flex-1 space-y-1">
                          <div class="flex items-center gap-2 flex-wrap">
                            <span class="font-mono text-[13px] font-semibold text-[var(--text-primary)] truncate">
                              {m.modelID}
                            </span>
                            <Show when={m.modelName && m.modelName !== m.modelID}>
                              <span class="text-[11px] text-[var(--text-muted)] truncate">
                                {m.modelName}
                              </span>
                            </Show>
                            <span class="rounded-md bg-[var(--bg-inset)] px-1.5 py-0.5 text-[10px] font-medium text-[var(--text-secondary)]">
                              {m.providerName}
                            </span>
                            <Show when={m.reasoning}>
                              <span class="inline-flex items-center gap-0.5 rounded-md bg-[var(--accent-surface)] px-1.5 py-0.5 text-[11px] font-medium text-[var(--accent-primary)]">
                                <Sparkles size={10} />
                                {t("models.reasoningCap")}
                              </span>
                            </Show>
                            <Show when={m.image}>
                              <span class="inline-flex items-center gap-0.5 rounded-md bg-[var(--bg-inset)] px-1.5 py-0.5 text-[11px] font-medium text-[var(--text-secondary)]">
                                <Image size={10} />
                                {t("models.imageCap")}
                              </span>
                            </Show>
                            <Show when={isAdded()}>
                              <span class="inline-flex items-center gap-1 rounded-md bg-[var(--ok-surface)] px-1.5 py-0.5 text-[11px] font-semibold text-[var(--ok-text)]">
                                <Check size={10} />
                                {t("models.modelAddedBadge")}
                              </span>
                            </Show>
                          </div>

                          <Show when={verifyErrors()[m.modelID]}>
                            <div class="flex items-start justify-between gap-2 text-[11px] text-[var(--danger-text)] font-mono">
                              <div class="flex items-start gap-1.5 min-w-0">
                                <AlertCircle size={12} class="shrink-0 mt-0.5" />
                                <span class="break-words">{verifyErrors()[m.modelID]}</span>
                              </div>
                              <button
                                type="button"
                                disabled={verifying()}
                                onClick={() => handleAddCandidateWithoutVerify(m)}
                                class="shrink-0 rounded-lg border border-[var(--border-strong)] bg-[var(--bg-panel)] px-2 py-0.5 text-[11px] font-medium text-[var(--text-secondary)] hover:text-[var(--text-primary)] cursor-pointer disabled:opacity-50"
                              >
                                {t("models.addWithoutVerify")}
                              </button>
                            </div>
                          </Show>
                        </div>
                      </div>
                    </div>
                  );
                }}
              </For>
            </Show>
          </div>

          {/* Manual entry fallback */}
          <div class="border-t border-[var(--border-subtle)] px-5 py-2.5 space-y-2.5">
            <button
              type="button"
              disabled={verifying()}
              onClick={() => setManualOpen((v) => !v)}
              class="text-[11px] font-medium text-[var(--accent-primary)] hover:underline disabled:opacity-50 cursor-pointer"
            >
              {manualOpen() ? t("models.hideManualEntry") : t("models.manualModelEntry")}
            </button>

            <Show when={manualOpen()}>
              <div class="grid grid-cols-2 gap-2">
                <div>
                  <label class="block text-[12px] font-medium text-[var(--text-secondary)] mb-1">
                    {t("models.modelId")}
                  </label>
                  <input
                    type="text"
                    disabled={verifying()}
                    autocapitalize="off"
                    autocorrect="off"
                    spellcheck={false}
                    placeholder={t("models.modelIdPlaceholder")}
                    value={draftId()}
                    onInput={(e) => {
                      const val = e.currentTarget.value;
                      setDraftId(val);
                      setManualError(null);
                      if (!manualCaps()) {
                        const guessed = guessFetchedModelDraft({ id: val, name: val });
                        setDraftReasoning(guessed.reasoning);
                        setDraftImage(guessed.image);
                      }
                    }}
                    onKeyDown={(e) => {
                      if (e.key === "Enter") {
                        e.preventDefault();
                        void handleVerifyManual();
                      }
                    }}
                    class="w-full rounded-xl border border-[var(--border-strong)] bg-[var(--bg-elevated)] px-3 py-2 font-mono text-[13px] text-[var(--text-primary)] outline-none focus:border-[var(--accent-primary)] selectable-text"
                  />
                </div>
                <div>
                  <label class="block text-[12px] font-medium text-[var(--text-secondary)] mb-1">
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
                        void handleVerifyManual();
                      }
                    }}
                    class="w-full rounded-xl border border-[var(--border-subtle)] bg-[var(--bg-elevated)] px-3 py-2 text-[13px] text-[var(--text-primary)] outline-none focus:border-[var(--accent-primary)] selectable-text"
                  />
                </div>
              </div>

              <div class="flex items-center gap-2">
                <button
                  type="button"
                  disabled={verifying()}
                  onClick={() => {
                    setManualCaps(true);
                    setDraftReasoning((v) => !v);
                  }}
                  class={`flex-1 inline-flex items-center justify-center gap-1.5 rounded-xl border px-3 py-1.5 text-[12px] font-medium transition-colors cursor-pointer disabled:opacity-50 ${
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
                  class={`flex-1 inline-flex items-center justify-center gap-1.5 rounded-xl border px-3 py-1.5 text-[12px] font-medium transition-colors cursor-pointer disabled:opacity-50 ${
                    draftImage()
                      ? "border-[var(--accent-border)] bg-[var(--accent-surface)] text-[var(--accent-primary)]"
                      : "border-[var(--border-subtle)] bg-[var(--bg-elevated)] text-[var(--text-muted)]"
                  }`}
                >
                  <Image size={13} />
                  <span>{t("models.imageCap")}</span>
                </button>
                <button
                  type="button"
                  disabled={!draftId().trim() || verifying()}
                  onClick={() => void handleVerifyManual()}
                  class="shrink-0 inline-flex items-center gap-1.5 rounded-xl border border-[var(--accent-border)] bg-[var(--accent-surface)] px-3 py-1.5 text-[12px] font-medium text-[var(--accent-primary)] hover:opacity-90 disabled:opacity-50 cursor-pointer"
                >
                  <Show when={!verifying()} fallback={<LoaderCircle size={13} class="animate-spin" />}>
                    <Check size={13} />
                  </Show>
                  <span>{verifying() ? t("models.verifyingModel") : t("models.verifyAndAddModel")}</span>
                </button>
              </div>

              <Show when={manualError()}>
                <div class="flex items-start justify-between gap-2 rounded-xl border border-[var(--danger-border)] bg-[var(--danger-surface)] px-3 py-2 text-[12px] text-[var(--danger-text)]">
                  <div class="flex items-start gap-2 min-w-0">
                    <AlertCircle size={14} class="shrink-0 mt-0.5" />
                    <span class="break-words">{manualError()}</span>
                  </div>
                  <button
                    type="button"
                    disabled={verifying()}
                    onClick={commitManualModel}
                    class="shrink-0 rounded-lg border border-[var(--border-strong)] bg-[var(--bg-panel)] px-2 py-0.5 text-[11px] font-medium text-[var(--text-primary)] hover:bg-[var(--bg-hover)] cursor-pointer disabled:opacity-50"
                  >
                    {t("models.addWithoutVerify")}
                  </button>
                </div>
              </Show>
            </Show>
          </div>

          <div class="flex items-center justify-between gap-3 border-t border-[var(--border-subtle)] bg-[var(--bg-elevated)]/30 px-5 py-3">
            <span class="text-[12px] text-[var(--text-muted)]">
              {t("models.selectedCount", { count: selectedCandidates().length })}
            </span>
            <div class="flex items-center gap-2">
              <button
                type="button"
                disabled={verifying()}
                onClick={props.onClose}
                class="rounded-xl border border-[var(--border-subtle)] bg-[var(--bg-elevated)] px-3.5 py-1.5 text-[12px] font-medium text-[var(--text-secondary)] hover:text-[var(--text-primary)] cursor-pointer disabled:opacity-50"
              >
                {t("common.cancel")}
              </button>
              <button
                type="button"
                disabled={selectedCandidates().length === 0 || verifying()}
                onClick={() => void handleVerifySelected()}
                class="inline-flex items-center gap-1.5 rounded-xl bg-[var(--accent-primary)] px-4 py-1.5 text-[12px] font-medium text-[var(--accent-foreground)] hover:opacity-90 disabled:opacity-50 cursor-pointer"
              >
                <Show when={!verifying()} fallback={<LoaderCircle size={13} class="animate-spin" />}>
                  <Plus size={13} />
                </Show>
                <span>
                  {verifying()
                    ? t("models.verifyingModel")
                    : t("models.verifyAndAddSelected", { count: selectedCandidates().length })}
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
