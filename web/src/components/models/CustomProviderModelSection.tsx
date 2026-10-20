// web/src/components/models/CustomProviderModelSection.tsx
import { createMemo, createSignal, For, Index, Show, type Accessor, type Setter } from "solid-js";
import {
  AlertCircle,
  Check,
  Download,
  Image,
  LoaderCircle,
  Play,
  Plus,
  Search,
  Sparkles,
  Trash2,
  X,
} from "lucide-solid";
import type { CustomProviderModelDraft } from "../../store/workspace";
import { t } from "../../i18n";

export interface ModelProbeResult {
  status: "testing" | "ok" | "error";
  latencyMs?: number;
  error?: string;
}

interface CustomProviderModelSectionProps {
  models: Accessor<CustomProviderModelDraft[]>;
  setModels: Setter<CustomProviderModelDraft[]>;
  fetchedModels: Accessor<Array<{ id: string; name: string }>>;
  setFetchedModels: Setter<Array<{ id: string; name: string }>>;
  selectedFetched: Accessor<Record<string, boolean>>;
  setSelectedFetched: Setter<Record<string, boolean>>;
  onApplyFetchedModels: () => void;
  headers: Accessor<Array<{ key: string; value: string }>>;
  setHeaders: Setter<Array<{ key: string; value: string }>>;
  modelProbes?: Accessor<Record<string, ModelProbeResult>>;
  onProbeModel?: (
    modelID: string
  ) => Promise<{ ok: boolean; latencyMs?: number; error?: string }> | void;
  readOnly?: Accessor<boolean>;
}

export function guessFetchedModelDraft(rm: { id: string; name: string }): CustomProviderModelDraft {
  const lower = rm.id.toLowerCase();
  const reasoning =
    lower.includes("reason") ||
    lower.includes("r1") ||
    lower.includes("thinking") ||
    lower.includes("opus") ||
    lower.includes("sonnet") ||
    lower.includes("o1") ||
    lower.includes("o3") ||
    lower.includes("gpt-5") ||
    lower.includes("gpt-6") ||
    lower.includes("gemini") ||
    lower.includes("deepseek") ||
    lower.includes("glm");
  const image =
    lower.includes("vision") ||
    lower.includes("vl") ||
    lower.includes("claude") ||
    lower.includes("gpt-4") ||
    lower.includes("gpt-5") ||
    lower.includes("gpt-6") ||
    lower.includes("gemini");
  return {
    id: rm.id,
    name: rm.name || rm.id,
    reasoning,
    image,
  };
}

export default function CustomProviderModelSection(props: CustomProviderModelSectionProps) {
  const [fetchSearch, setFetchSearch] = createSignal("");
  const [addModalOpen, setAddModalOpen] = createSignal(false);
  const [draftId, setDraftId] = createSignal("");
  const [draftName, setDraftName] = createSignal("");
  const [draftReasoning, setDraftReasoning] = createSignal(false);
  const [draftImage, setDraftImage] = createSignal(false);
  const [manualCaps, setManualCaps] = createSignal(false);
  const [verifying, setVerifying] = createSignal(false);
  const [verifyError, setVerifyError] = createSignal<string | null>(null);

  const openAddModal = () => {
    setDraftId("");
    setDraftName("");
    setDraftReasoning(false);
    setDraftImage(false);
    setManualCaps(false);
    setVerifying(false);
    setVerifyError(null);
    setAddModalOpen(true);
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
    setAddModalOpen(false);
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

  const filteredFetchedModels = createMemo(() => {
    const q = fetchSearch().trim().toLowerCase();
    const list = props.fetchedModels();
    if (!q) return list;
    return list.filter(
      (m) => m.id.toLowerCase().includes(q) || m.name.toLowerCase().includes(q)
    );
  });

  const selectedFetchedCount = createMemo(
    () => Object.values(props.selectedFetched()).filter(Boolean).length
  );

  const updateModelRow = (idx: number, patch: Partial<CustomProviderModelDraft>) => {
    props.setModels((prev) => prev.map((row, i) => (i === idx ? { ...row, ...patch } : row)));
  };

  const removeModelRow = (idx: number) => {
    props.setModels((prev) => prev.filter((_, i) => i !== idx));
  };

  const toggleAllCapability = (field: "reasoning" | "image") => {
    const allEnabled = props.models().every((m) => m[field]);
    props.setModels((prev) => prev.map((m) => ({ ...m, [field]: !allEnabled })));
  };

  return (
    <>
      {/* Kilo Interactive Fetched Models Picker */}
      <Show when={props.fetchedModels().length > 0}>
        <div class="rounded-2xl border border-[var(--accent-border)] bg-[var(--bg-elevated)] p-3.5 space-y-2.5">
          <div class="flex items-center justify-between gap-2">
            <span class="text-[12px] font-semibold text-[var(--text-primary)]">
              {t("models.fetchedModelsCount", { count: props.fetchedModels().length })}
            </span>
            <div class="flex items-center gap-2">
              <button
                type="button"
                onClick={() => {
                  const next: Record<string, boolean> = { ...props.selectedFetched() };
                  for (const m of filteredFetchedModels()) next[m.id] = true;
                  props.setSelectedFetched(next);
                }}
                class="text-[11px] font-medium text-[var(--accent-primary)] hover:underline cursor-pointer"
              >
                {t("models.selectAll")}
              </button>
              <span class="text-[var(--text-faint)]">·</span>
              <button
                type="button"
                onClick={() => {
                  const next: Record<string, boolean> = { ...props.selectedFetched() };
                  for (const m of filteredFetchedModels()) next[m.id] = false;
                  props.setSelectedFetched(next);
                }}
                class="text-[11px] font-medium text-[var(--text-muted)] hover:underline cursor-pointer"
              >
                {t("models.deselectAll")}
              </button>
              <button
                type="button"
                onClick={() => props.setFetchedModels([])}
                class="rounded p-1 text-[var(--text-faint)] hover:text-[var(--text-primary)] cursor-pointer"
              >
                <X size={13} />
              </button>
            </div>
          </div>

          <div class="flex items-center gap-2 rounded-xl border border-[var(--border-subtle)] bg-[var(--bg-panel)] px-2.5 py-1.5">
            <Search size={12} class="text-[var(--text-faint)]" />
            <input
              type="text"
              placeholder={t("models.searchModels")}
              value={fetchSearch()}
              onInput={(e) => setFetchSearch(e.currentTarget.value)}
              class="w-full bg-transparent text-[12px] text-[var(--text-primary)] outline-none"
            />
          </div>

          <div class="max-h-[150px] overflow-y-auto grid grid-cols-2 gap-1.5 pr-1">
            <For each={filteredFetchedModels()}>
              {(rm) => {
                const checked = () => Boolean(props.selectedFetched()[rm.id]);
                return (
                  <label class="flex items-center gap-2 rounded-lg border border-[var(--border-subtle)] bg-[var(--bg-panel)] px-2.5 py-1.5 text-[11.5px] cursor-pointer hover:border-[var(--border-strong)]">
                    <input
                      type="checkbox"
                      checked={checked()}
                      onChange={(e) =>
                        props.setSelectedFetched((prev) => ({
                          ...prev,
                          [rm.id]: e.currentTarget.checked,
                        }))
                      }
                    />
                    <span class="font-mono truncate text-[var(--text-primary)]">{rm.id}</span>
                  </label>
                );
              }}
            </For>
          </div>

          <div class="flex justify-end">
            <button
              type="button"
              disabled={selectedFetchedCount() === 0}
              onClick={() => props.onApplyFetchedModels()}
              class="inline-flex items-center gap-1.5 rounded-xl bg-[var(--accent-primary)] px-3.5 py-1.5 text-[11.5px] font-medium text-[var(--accent-foreground)] disabled:opacity-50 cursor-pointer"
            >
              <Download size={12} />
              {t("models.addSelectedModels", { count: selectedFetchedCount() })}
            </button>
          </div>
        </div>
      </Show>

      {/* Models List Section (Kilo CustomProviderModelCard list) */}
      <div class="space-y-2">
        <div class="flex items-center justify-between">
          <label class="text-[12px] font-semibold text-[var(--text-primary)]">
            {t("models.modelsListTitle")} ({props.models().filter((m) => m.id.trim()).length})
          </label>
          <Show when={!props.readOnly?.()}>
            <div class="flex items-center gap-2">
              <Show when={props.models().length > 0}>
                <button
                  type="button"
                  onClick={() => toggleAllCapability("reasoning")}
                  class="inline-flex items-center gap-1 rounded-lg border border-[var(--border-subtle)] bg-[var(--bg-elevated)] px-2 py-1 text-[11px] text-[var(--text-secondary)] hover:text-[var(--text-primary)] cursor-pointer"
                >
                  <Sparkles size={11} />
                  {t("models.toggleAllReasoning")}
                </button>
                <button
                  type="button"
                  onClick={() => toggleAllCapability("image")}
                  class="inline-flex items-center gap-1 rounded-lg border border-[var(--border-subtle)] bg-[var(--bg-elevated)] px-2 py-1 text-[11px] text-[var(--text-secondary)] hover:text-[var(--text-primary)] cursor-pointer"
                >
                  <Image size={11} />
                  {t("models.toggleAllImage")}
                </button>
              </Show>
              <button
                type="button"
                onClick={openAddModal}
                class="inline-flex items-center gap-1 rounded-lg border border-[var(--accent-border)] bg-[var(--accent-surface)] px-2.5 py-1 text-[11px] font-medium text-[var(--accent-primary)] hover:opacity-90 cursor-pointer"
              >
                <Plus size={11} />
                {t("models.addModelRow")}
              </button>
            </div>
          </Show>
        </div>

        <Show
          when={props.models().length > 0}
          fallback={
            <div class="flex flex-col items-center justify-center gap-2.5 rounded-xl border border-dashed border-[var(--border-subtle)] bg-[var(--bg-elevated)]/30 px-4 py-6 text-center">
              <p class="text-[12px] text-[var(--text-muted)]">
                {t("models.noConfiguredModelsEmpty")}
              </p>
              <Show when={!props.readOnly?.()}>
                <button
                  type="button"
                  onClick={openAddModal}
                  class="inline-flex items-center gap-1.5 rounded-xl border border-[var(--accent-border)] bg-[var(--accent-surface)] px-3 py-1.5 text-[11.5px] font-medium text-[var(--accent-primary)] hover:opacity-90 cursor-pointer"
                >
                  <Plus size={12} />
                  {t("models.addModelRow")}
                </button>
              </Show>
            </div>
          }
        >
        <div class="space-y-1.5 max-h-[320px] overflow-y-auto pr-1">
          <Index each={props.models()}>
            {(row, idx) => {
              const probe = () =>
                props.modelProbes ? props.modelProbes()[row().id.trim()] : undefined;
              return (
                <div class="rounded-xl border border-[var(--border-subtle)] bg-[var(--bg-elevated)]/50 px-2.5 py-1.5 space-y-1">
                  <div class="flex items-center gap-2">
                    <input
                      type="text"
                      disabled={props.readOnly?.()}
                      autocapitalize="off"
                      autocorrect="off"
                      spellcheck={false}
                      placeholder={`${t("models.modelId")} (${t("models.modelIdPlaceholder")})`}
                      value={row().id}
                      onInput={(e) => {
                        const val = e.currentTarget.value;
                        const cur = row();
                        updateModelRow(idx, {
                          id: val,
                          ...(cur.name === "" || cur.name === cur.id ? { name: val } : {}),
                        });
                      }}
                      class="flex-1 min-w-0 rounded-lg border border-[var(--border-subtle)] bg-[var(--bg-panel)] px-2.5 py-1 font-mono text-[12px] text-[var(--text-primary)] disabled:opacity-60 outline-none focus:border-[var(--accent-primary)] selectable-text"
                    />
                    <input
                      type="text"
                      disabled={props.readOnly?.()}
                      autocapitalize="off"
                      autocorrect="off"
                      spellcheck={false}
                      placeholder={t("models.modelName")}
                      value={row().name}
                      onInput={(e) => updateModelRow(idx, { name: e.currentTarget.value })}
                      class="w-[145px] shrink-0 rounded-lg border border-[var(--border-subtle)] bg-[var(--bg-panel)] px-2.5 py-1 text-[12px] text-[var(--text-primary)] disabled:opacity-60 outline-none focus:border-[var(--accent-primary)] selectable-text"
                    />
                    <button
                      type="button"
                      disabled={props.readOnly?.()}
                      onClick={() => updateModelRow(idx, { reasoning: !row().reasoning })}
                      class={`shrink-0 inline-flex items-center gap-1 rounded-lg border px-2 py-1 text-[11px] font-medium transition-colors cursor-pointer disabled:opacity-60 ${
                        row().reasoning
                          ? "border-[var(--accent-border)] bg-[var(--accent-surface)] text-[var(--accent-primary)]"
                          : "border-[var(--border-subtle)] bg-[var(--bg-panel)] text-[var(--text-faint)]"
                      }`}
                      title={t("models.supportsReasoning")}
                    >
                      <Sparkles size={11} />
                      {t("models.reasoningCap")}
                    </button>
                    <button
                      type="button"
                      disabled={props.readOnly?.()}
                      onClick={() => updateModelRow(idx, { image: !row().image })}
                      class={`shrink-0 inline-flex items-center gap-1 rounded-lg border px-2 py-1 text-[11px] font-medium transition-colors cursor-pointer disabled:opacity-60 ${
                        row().image
                          ? "border-[var(--accent-border)] bg-[var(--accent-surface)] text-[var(--accent-primary)]"
                          : "border-[var(--border-subtle)] bg-[var(--bg-panel)] text-[var(--text-faint)]"
                      }`}
                    >
                      <Image size={11} />
                      {t("models.imageCap")}
                    </button>

                    {/* Per-Model Test Button & Status Badge */}
                    <button
                      type="button"
                      disabled={!row().id.trim() || probe()?.status === "testing"}
                      onClick={() => props.onProbeModel?.(row().id.trim())}
                      title={probe()?.error || t("models.testConnection")}
                      class={`shrink-0 inline-flex items-center gap-1 rounded-lg border px-2 py-1 text-[11px] font-medium transition-colors cursor-pointer disabled:opacity-50 ${
                        probe()?.status === "ok"
                          ? "border-[var(--ok-border)] bg-[var(--ok-surface)] text-[var(--ok-text)]"
                          : probe()?.status === "error"
                          ? "border-[var(--danger-border)] bg-[var(--danger-surface)] text-[var(--danger-text)]"
                          : "border-[var(--border-subtle)] bg-[var(--bg-panel)] text-[var(--text-secondary)] hover:text-[var(--text-primary)]"
                      }`}
                    >
                      <Show when={probe()?.status === "testing"}>
                        <LoaderCircle size={11} class="animate-spin" />
                        <span>{t("models.testing")}</span>
                      </Show>
                      <Show when={probe()?.status === "ok"}>
                        <Check size={11} />
                        <span class="font-mono">{probe()?.latencyMs ?? 0}ms</span>
                      </Show>
                      <Show when={probe()?.status === "error"}>
                        <AlertCircle size={11} />
                        <span>{t("common.error")}</span>
                      </Show>
                      <Show when={!probe()}>
                        <Play size={10} />
                        <span>{t("models.testConnection")}</span>
                      </Show>
                    </button>

                    <Show when={!props.readOnly?.()}>
                      <button
                        type="button"
                        onClick={() => removeModelRow(idx)}
                        class="shrink-0 rounded-lg p-1 text-[var(--text-faint)] hover:bg-[var(--danger-surface)] hover:text-[var(--danger-text)] cursor-pointer"
                      >
                        <Trash2 size={13} />
                      </button>
                    </Show>
                  </div>

                  <Show when={probe()?.status === "error" && probe()?.error}>
                    <div class="text-[11px] text-[var(--danger-text)] font-mono px-1 truncate">
                      {probe()!.error}
                    </div>
                  </Show>
                </div>
              );
            }}
          </Index>
        </div>
        </Show>
      </div>

      {/* Add & Verify Model Modal */}
      <Show when={addModalOpen()}>
        <div
          class="hoxi-modal-backdrop fixed inset-0 z-50 flex items-center justify-center bg-black/50 backdrop-blur-xs p-4"
          onClick={() => !verifying() && setAddModalOpen(false)}
          onKeyDown={(e) => {
            if (e.key === "Escape") {
              e.preventDefault();
              e.stopPropagation();
              if (!verifying()) setAddModalOpen(false);
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
                onClick={() => setAddModalOpen(false)}
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
                onClick={() => setAddModalOpen(false)}
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

      {/* Custom HTTP Headers */}
      <Show when={!props.readOnly?.() || props.headers().length > 0}>
        <div class="space-y-1.5">
          <div class="flex items-center justify-between">
            <span class="text-[11.5px] font-medium text-[var(--text-secondary)]">
              {t("models.headersTitle")}
            </span>
            <Show when={!props.readOnly?.()}>
              <button
                type="button"
                onClick={() => props.setHeaders((prev) => [...prev, { key: "", value: "" }])}
                class="inline-flex items-center gap-1 text-[11px] font-medium text-[var(--accent-primary)] hover:underline cursor-pointer"
              >
                <Plus size={11} />
                {t("models.addHeader")}
              </button>
            </Show>
          </div>
          <Index each={props.headers()}>
            {(hdr, idx) => (
              <div class="flex items-center gap-2">
                <input
                  type="text"
                  disabled={props.readOnly?.()}
                  autocapitalize="off"
                  autocorrect="off"
                  spellcheck={false}
                  placeholder={t("models.headerName")}
                  value={hdr().key}
                  onInput={(e) => {
                    const val = e.currentTarget.value;
                    props.setHeaders((prev) =>
                      prev.map((item, i) => (i === idx ? { ...item, key: val } : item))
                    );
                  }}
                  class="w-1/3 rounded-lg border border-[var(--border-subtle)] bg-[var(--bg-elevated)] px-2.5 py-1 font-mono text-[11.5px] text-[var(--text-primary)] disabled:opacity-60 outline-none selectable-text"
                />
                <input
                  type="text"
                  disabled={props.readOnly?.()}
                  autocapitalize="off"
                  autocorrect="off"
                  spellcheck={false}
                  placeholder={t("models.headerValue")}
                  value={hdr().value}
                  onInput={(e) => {
                    const val = e.currentTarget.value;
                    props.setHeaders((prev) =>
                      prev.map((item, i) => (i === idx ? { ...item, value: val } : item))
                    );
                  }}
                  class="flex-1 rounded-lg border border-[var(--border-subtle)] bg-[var(--bg-elevated)] px-2.5 py-1 font-mono text-[11.5px] text-[var(--text-primary)] disabled:opacity-60 outline-none selectable-text"
                />
                <Show when={!props.readOnly?.()}>
                  <button
                    type="button"
                    onClick={() => props.setHeaders((prev) => prev.filter((_, i) => i !== idx))}
                    class="rounded p-1 text-[var(--text-faint)] hover:text-[var(--danger-text)] cursor-pointer"
                  >
                    <Trash2 size={12} />
                  </button>
                </Show>
              </div>
            )}
          </Index>
        </div>
      </Show>
    </>
  );
}
