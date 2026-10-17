// web/src/components/models/CustomEndpointTab.tsx
import { createSignal, Show } from "solid-js";
import { LoaderCircle, Server } from "lucide-solid";
import { connectProviderWithKey } from "../../store/workspace";
import { t } from "../../i18n";

export default function CustomEndpointTab() {
  const [customID, setCustomID] = createSignal("");
  const [customName, setCustomName] = createSignal("");
  const [customBaseURL, setCustomBaseURL] = createSignal("");
  const [customKey, setCustomKey] = createSignal("");
  const [customModel, setCustomModel] = createSignal("");
  const [saving, setSaving] = createSignal(false);
  const [feedback, setFeedback] = createSignal<{ ok: boolean; text: string } | null>(null);

  const onSaveCustomProvider = async (ev: Event) => {
    ev.preventDefault();
    if (saving()) return;
    const id = customID().trim().toLowerCase().replace(/[^a-z0-9-_]/g, "-");
    const url = customBaseURL().trim();
    const model = customModel().trim();
    if (!id || !url || !model) {
      setFeedback({
        ok: false,
        text: "Provider ID, Base URL, and Model ID are required.",
      });
      return;
    }

    setSaving(true);
    setFeedback(null);
    try {
      await connectProviderWithKey({
        providerID: id,
        apiKey: customKey(),
        baseURL: url,
        customName: customName().trim() || id,
        customModelID: model,
      });
      setFeedback({
        ok: true,
        text: `Added custom provider "${customName() || id}" (${model})!`,
      });
      setCustomKey("");
    } catch (err: any) {
      setFeedback({ ok: false, text: err?.message || "Failed to save custom provider." });
    } finally {
      setSaving(false);
    }
  };

  return (
    <form onSubmit={onSaveCustomProvider} class="flex-1 overflow-y-auto p-6 space-y-4">
      <div class="rounded-xl border border-[var(--border-subtle)] bg-[var(--bg-elevated)]/60 p-3.5 text-[12px] text-[var(--text-secondary)]">
        {t("models.customEndpointIntro")}
      </div>

      <div class="grid grid-cols-2 gap-4">
        <div>
          <label class="block text-[11.5px] font-medium text-[var(--text-secondary)] mb-1">
            {t("models.providerId")}
          </label>
          <input
            type="text"
            placeholder="e.g. deepseek-custom"
            value={customID()}
            onInput={(e) => setCustomID(e.currentTarget.value)}
            class="w-full rounded-xl border border-[var(--border-strong)] bg-[var(--bg-elevated)] px-3 py-2 text-[13px] text-[var(--text-primary)] outline-none focus:border-[var(--accent-primary)] selectable-text"
          />
        </div>
        <div>
          <label class="block text-[11.5px] font-medium text-[var(--text-secondary)] mb-1">
            {t("models.displayName")}
          </label>
          <input
            type="text"
            placeholder="e.g. DeepSeek Internal"
            value={customName()}
            onInput={(e) => setCustomName(e.currentTarget.value)}
            class="w-full rounded-xl border border-[var(--border-strong)] bg-[var(--bg-elevated)] px-3 py-2 text-[13px] text-[var(--text-primary)] outline-none focus:border-[var(--accent-primary)] selectable-text"
          />
        </div>
      </div>

      <div class="grid grid-cols-2 gap-4">
        <div>
          <label class="block text-[11.5px] font-medium text-[var(--text-secondary)] mb-1">
            {t("models.baseUrl")}
          </label>
          <input
            type="text"
            placeholder="https://api.deepseek.com/v1"
            value={customBaseURL()}
            onInput={(e) => setCustomBaseURL(e.currentTarget.value)}
            class="w-full rounded-xl border border-[var(--border-strong)] bg-[var(--bg-elevated)] px-3 py-2 font-mono text-[12.5px] text-[var(--text-primary)] outline-none focus:border-[var(--accent-primary)] selectable-text"
          />
        </div>
        <div>
          <label class="block text-[11.5px] font-medium text-[var(--text-secondary)] mb-1">
            {t("models.modelId")}
          </label>
          <input
            type="text"
            placeholder="e.g. deepseek-v4.1-flash"
            value={customModel()}
            onInput={(e) => setCustomModel(e.currentTarget.value)}
            class="w-full rounded-xl border border-[var(--border-strong)] bg-[var(--bg-elevated)] px-3 py-2 font-mono text-[12.5px] text-[var(--text-primary)] outline-none focus:border-[var(--accent-primary)] selectable-text"
          />
        </div>
      </div>

      <div>
        <label class="block text-[11.5px] font-medium text-[var(--text-secondary)] mb-1">
          {t("models.apiKeyOptional")}
        </label>
        <input
          type="password"
          placeholder="sk-..."
          value={customKey()}
          onInput={(e) => setCustomKey(e.currentTarget.value)}
          class="w-full rounded-xl border border-[var(--border-strong)] bg-[var(--bg-elevated)] px-3 py-2 font-mono text-[12.5px] text-[var(--text-primary)] outline-none focus:border-[var(--accent-primary)] selectable-text"
        />
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

      <div class="flex justify-end pt-2">
        <button
          type="submit"
          disabled={saving()}
          class="inline-flex items-center gap-2 rounded-xl bg-[var(--accent-primary)] px-4 py-2 text-[12.5px] font-medium text-white hover:opacity-90 disabled:opacity-50 transition-opacity cursor-pointer"
        >
          <Show when={!saving()} fallback={<LoaderCircle size={14} class="animate-spin" />}>
            <Server size={14} />
          </Show>
          {t("models.saveAndSelectModel")}
        </button>
      </div>
    </form>
  );
}
