// web/src/components/models/CustomProviderModelSection.tsx
import { createSignal, Index, Show, type Accessor, type Setter } from "solid-js";
import {
  AlertCircle,
  Check,
  Image,
  LoaderCircle,
  Play,
  Plus,
  Sparkles,
  Trash2,
} from "lucide-solid";
import type { CustomProviderModelDraft } from "../../store/workspace";
import { t } from "../../i18n";
import { AddCustomModelModal, FetchedModelsModal } from "./CustomProviderModelModals";

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
  selectedFetched?: Accessor<Record<string, boolean>>;
  setSelectedFetched?: Setter<Record<string, boolean>>;
  onApplyFetchedModels?: () => void;
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
  const [addModalOpen, setAddModalOpen] = createSignal(false);

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
      <FetchedModelsModal
        models={props.models}
        setModels={props.setModels}
        fetchedModels={props.fetchedModels}
        setFetchedModels={props.setFetchedModels}
        modelProbes={props.modelProbes}
        onProbeModel={props.onProbeModel}
      />

      {/* Models List Section */}
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
                onClick={() => setAddModalOpen(true)}
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
                  onClick={() => setAddModalOpen(true)}
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
                        onClick={() => void props.onProbeModel?.(row().id.trim())}
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

      <AddCustomModelModal
        open={addModalOpen}
        onClose={() => setAddModalOpen(false)}
        models={props.models}
        setModels={props.setModels}
        onProbeModel={props.onProbeModel}
      />

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
