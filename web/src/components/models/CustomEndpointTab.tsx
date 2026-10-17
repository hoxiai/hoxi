// web/src/components/models/CustomEndpointTab.tsx
import { createEffect, createMemo, createSignal, For, Show } from "solid-js";
import { Check, LoaderCircle, Plus, RefreshCw, Trash2 } from "lucide-solid";
import { hoxiAPI } from "../../services/api";
import {
  disconnectProvider,
  isCustomOrConfiguredProvider,
  MASKED_SECRET,
  saveCustomProvider,
  setWorkspace,
  workspace,
  type CustomProviderModelDraft,
} from "../../store/workspace";
import { t } from "../../i18n";
import CustomProviderModelSection, {
  guessFetchedModelDraft,
  type ModelProbeResult,
} from "./CustomProviderModelSection";

const PROVIDER_VARIANTS = [
  { value: "@ai-sdk/openai-compatible", label: "OpenAI Compatible (/v1/chat/completions)" },
  { value: "@ai-sdk/openai", label: "OpenAI Responses (/v1/responses)" },
  { value: "@ai-sdk/anthropic", label: "Anthropic Messages (/v1/messages)" },
] as const;

export default function CustomEndpointTab() {
  const [providerID, setProviderID] = createSignal("");
  const [displayName, setDisplayName] = createSignal("");
  const [npmPkg, setNpmPkg] = createSignal<string>("@ai-sdk/openai-compatible");
  const [baseURL, setBaseURL] = createSignal("");
  const [apiKey, setApiKey] = createSignal("");
  const [preserveExistingKey, setPreserveExistingKey] = createSignal(false);
  const [models, setModels] = createSignal<CustomProviderModelDraft[]>([
    { id: "", name: "", reasoning: false, image: false },
  ]);
  const [headers, setHeaders] = createSignal<Array<{ key: string; value: string }>>([]);

  // Fetch & probe models state
  const [fetching, setFetching] = createSignal(false);
  const [fetchedModels, setFetchedModels] = createSignal<Array<{ id: string; name: string }>>([]);
  const [selectedFetched, setSelectedFetched] = createSignal<Record<string, boolean>>({});
  const [modelProbes, setModelProbes] = createSignal<Record<string, ModelProbeResult>>({});
  const [saving, setSaving] = createSignal(false);
  const [feedback, setFeedback] = createSignal<{ ok: boolean; text: string } | null>(null);

  const existingCustomProviders = createMemo(() => {
    const out: Array<{ id: string; name: string }> = [];
    const cfgProviders = workspace.globalConfig?.provider ?? {};
    for (const [id, cfg] of Object.entries<any>(cfgProviders)) {
      if (cfg && typeof cfg === "object" && isCustomOrConfiguredProvider(id)) {
        out.push({ id, name: cfg.name || id });
      }
    }
    return out;
  });

  const isEditing = createMemo(() => Boolean(workspace.editingProviderID));

  const loadProviderIntoForm = (editID: string | null) => {
    setFeedback(null);
    setFetchedModels([]);
    setSelectedFetched({});
    setModelProbes({});

    if (!editID) {
      setProviderID("");
      setDisplayName("");
      setNpmPkg("@ai-sdk/openai-compatible");
      setBaseURL("");
      setApiKey("");
      setPreserveExistingKey(false);
      setModels([{ id: "", name: "", reasoning: false, image: false }]);
      setHeaders([]);
      return;
    }

    const cfgProv = workspace.globalConfig?.provider?.[editID] ?? {};
    const runtimeProv = workspace.providers.find((p) => p.id === editID);
    const hasConnectedAuth =
      workspace.connectedProviders.includes(editID) ||
      Boolean(runtimeProv?.key) ||
      Boolean(cfgProv?.options?.apiKey);

    setProviderID(editID);
    setDisplayName(cfgProv.name || runtimeProv?.name || editID);
    setNpmPkg(cfgProv.npm || runtimeProv?.npm || "@ai-sdk/openai-compatible");
    setBaseURL(cfgProv?.options?.baseURL || runtimeProv?.options?.baseURL || "");

    if (typeof cfgProv?.options?.apiKey === "string" && cfgProv.options.apiKey.startsWith("{env:")) {
      setApiKey(cfgProv.options.apiKey);
      setPreserveExistingKey(false);
    } else if (hasConnectedAuth) {
      setApiKey(MASKED_SECRET);
      setPreserveExistingKey(true);
    } else {
      setApiKey("");
      setPreserveExistingKey(false);
    }

    const rawModels = cfgProv.models || runtimeProv?.models || {};
    const loadedModels: CustomProviderModelDraft[] = [];
    for (const [mID, mVal] of Object.entries<any>(rawModels)) {
      if (!mVal || typeof mVal !== "object") continue;
      const rtModel = runtimeProv?.models?.[mID];
      const reasoning = Boolean(
        mVal.reasoning ??
          mVal.capabilities?.reasoning ??
          rtModel?.capabilities?.reasoning ??
          rtModel?.reasoning
      );
      const image = Boolean(
        (Array.isArray(mVal.modalities?.input) && mVal.modalities.input.includes("image")) ||
          mVal.capabilities?.input?.image ||
          rtModel?.capabilities?.input?.image
      );
      loadedModels.push({
        id: mID,
        name: mVal.name || rtModel?.name || mID,
        reasoning,
        image,
      });
    }
    setModels(
      loadedModels.length > 0
        ? loadedModels
        : [{ id: "", name: "", reasoning: false, image: false }]
    );

    const rawHeaders = cfgProv?.options?.headers || runtimeProv?.options?.headers || {};
    const loadedHeaders = Object.entries<any>(rawHeaders)
      .filter(([, v]) => typeof v === "string")
      .map(([k, v]) => ({ key: k, value: String(v) }));
    setHeaders(loadedHeaders);
  };

  createEffect(() => {
    loadProviderIntoForm(workspace.editingProviderID);
  });

  const onFetchModels = async () => {
    const url = baseURL().trim();
    if (!url) {
      setFeedback({ ok: false, text: t("models.fillBaseUrlPrompt") });
      return;
    }
    setFetching(true);
    setFeedback(null);
    try {
      const hdrMap: Record<string, string> = {};
      for (const h of headers()) {
        if (h.key.trim() && h.value.trim()) {
          hdrMap[h.key.trim()] = h.value.trim();
        }
      }
      const res = await hoxiAPI.fetchCustomProviderModels({
        baseURL: url,
        apiKey: apiKey().trim() || undefined,
        headers: hdrMap,
        providerID: providerID().trim() || undefined,
        directory: workspace.rootDir || undefined,
      });
      if (res.error || res.ok === false) {
        setFeedback({ ok: false, text: res.error || t("models.fetchModelsFailed") });
        return;
      }
      const remoteList = Array.isArray(res.models) ? res.models : [];
      setFetchedModels(remoteList);
      const existingIDs = new Set(models().map((m) => m.id.trim()).filter(Boolean));
      const initialSel: Record<string, boolean> = {};
      for (const rm of remoteList) {
        initialSel[rm.id] = existingIDs.has(rm.id);
      }
      setSelectedFetched(initialSel);
    } catch (err: any) {
      setFeedback({ ok: false, text: err?.message || t("models.fetchModelsFailed") });
    } finally {
      setFetching(false);
    }
  };

  const applyFetchedModels = () => {
    const sel = selectedFetched();
    const chosen = fetchedModels().filter((m) => sel[m.id]);
    if (chosen.length === 0) return;

    const existingMap = new Map<string, CustomProviderModelDraft>();
    for (const m of models()) {
      const id = m.id.trim();
      if (id) existingMap.set(id, m);
    }
    for (const rm of chosen) {
      if (!existingMap.has(rm.id)) {
        existingMap.set(rm.id, guessFetchedModelDraft(rm));
      }
    }
    setModels(Array.from(existingMap.values()));
    setFetchedModels([]);
  };

  const onProbeModel = async (mid: string) => {
    if (!mid.trim()) return;
    setModelProbes((prev) => ({ ...prev, [mid]: { status: "testing" } }));
    const hdrs: Record<string, string> = {};
    for (const h of headers()) if (h.key.trim() && h.value.trim()) hdrs[h.key.trim()] = h.value.trim();
    try {
      const res = await hoxiAPI.testProviderModel({
        providerID: providerID().trim() || undefined,
        baseURL: baseURL().trim() || undefined,
        apiKey: apiKey().trim() === MASKED_SECRET ? undefined : apiKey().trim() || undefined,
        npm: npmPkg(),
        modelID: mid.trim(),
        headers: Object.keys(hdrs).length ? hdrs : undefined,
      });
      setModelProbes((prev) => ({
        ...prev,
        [mid]: res.ok ? { status: "ok", latencyMs: res.latencyMs } : { status: "error", error: res.error || "Failed" },
      }));
    } catch (err: any) {
      setModelProbes((prev) => ({ ...prev, [mid]: { status: "error", error: err?.message || String(err) } }));
    }
  };

  const onSave = async (ev: Event) => {
    ev.preventDefault();
    if (saving()) return;

    const pid = providerID().trim();
    const name = displayName().trim() || pid;
    const url = baseURL().trim();
    const validModels = models().filter((m) => m.id.trim().length > 0);

    if (!pid || !/^[a-zA-Z0-9._-]+$/.test(pid)) {
      setFeedback({ ok: false, text: t("models.providerIdRequired") });
      return;
    }
    if (!url || !/^https?:\/\/.+/.test(url)) {
      setFeedback({ ok: false, text: t("models.validBaseUrlRequired") });
      return;
    }
    if (validModels.length === 0) {
      setFeedback({ ok: false, text: t("models.atLeastOneModelRequired") });
      return;
    }

    const hdrMap: Record<string, string> = {};
    for (const h of headers()) {
      if (h.key.trim() && h.value.trim()) {
        hdrMap[h.key.trim()] = h.value.trim();
      }
    }

    setSaving(true);
    setFeedback(null);
    try {
      await saveCustomProvider({
        providerID: pid,
        name,
        npm: npmPkg(),
        baseURL: url,
        apiKey: apiKey(),
        preserveExistingKey: preserveExistingKey() && apiKey() === MASKED_SECRET,
        headers: hdrMap,
        models: validModels,
      });
      setWorkspace("editingProviderID", pid);
      setFeedback({
        ok: true,
        text: t("models.providerSaved", { name, count: validModels.length }),
      });
    } catch (err: any) {
      setFeedback({ ok: false, text: err?.message || t("models.saveCustomProviderFailed") });
    } finally {
      setSaving(false);
    }
  };

  const onDeleteProvider = async () => {
    const pid = providerID().trim();
    if (!pid || saving()) return;
    setSaving(true);
    try {
      await disconnectProvider(pid);
      setWorkspace("editingProviderID", null);
      setWorkspace("providerModalTab", "providers");
    } finally {
      setSaving(false);
    }
  };

  return (
    <form onSubmit={onSave} class="flex-1 flex flex-col min-h-0 overflow-hidden">
      <div class="flex-1 overflow-y-auto p-5 space-y-4">
        {/* Clean Single-Line Header + Compact Switcher */}
        <div class="flex items-center justify-between gap-3 rounded-xl border border-[var(--border-subtle)] bg-[var(--bg-elevated)]/50 px-3.5 py-2.5">
          <div class="min-w-0">
            <div class="text-[12.5px] font-semibold text-[var(--text-primary)]">
              {isEditing() ? t("models.editCustomEndpoint") : t("models.addCustomEndpoint")}
            </div>
            <div class="text-[11px] text-[var(--text-muted)] truncate">
              {t("models.customEndpointIntro")}
            </div>
          </div>

          <Show when={existingCustomProviders().length > 0}>
            <div class="flex items-center gap-2 shrink-0">
              <select
                value={workspace.editingProviderID || ""}
                onChange={(e) =>
                  setWorkspace("editingProviderID", e.currentTarget.value || null)
                }
                class="max-w-[210px] rounded-lg border border-[var(--border-strong)] bg-[var(--bg-panel)] px-2.5 py-1 text-[11.5px] font-medium text-[var(--text-primary)] outline-none focus:border-[var(--accent-primary)]"
              >
                <option value="">+ {t("models.newCustomProvider")}</option>
                <For each={existingCustomProviders()}>
                  {(item) => (
                    <option value={item.id}>
                      {item.name} ({item.id})
                    </option>
                  )}
                </For>
              </select>
              <Show when={isEditing()}>
                <button
                  type="button"
                  onClick={() => setWorkspace("editingProviderID", null)}
                  class="inline-flex items-center gap-1 rounded-lg border border-[var(--accent-border)] bg-[var(--accent-surface)] px-2.5 py-1 text-[11.5px] font-medium text-[var(--accent-primary)] hover:opacity-90 cursor-pointer"
                >
                  <Plus size={12} />
                  {t("models.newCustomProvider")}
                </button>
              </Show>
            </div>
          </Show>
        </div>

        {/* Row 1: Provider ID, Display Name, Provider API */}
        <div class="grid grid-cols-3 gap-3">
          <div>
            <label class="block text-[11.5px] font-medium text-[var(--text-secondary)] mb-1">
              {t("models.providerId")}
            </label>
            <input
              type="text"
              disabled={isEditing()}
              placeholder={t("models.providerIdPlaceholder")}
              value={providerID()}
              onInput={(e) => setProviderID(e.currentTarget.value)}
              class="w-full rounded-xl border border-[var(--border-strong)] bg-[var(--bg-elevated)] px-3 py-2 font-mono text-[12.5px] text-[var(--text-primary)] disabled:opacity-60 outline-none focus:border-[var(--accent-primary)] selectable-text"
            />
          </div>
          <div>
            <label class="block text-[11.5px] font-medium text-[var(--text-secondary)] mb-1">
              {t("models.displayName")}
            </label>
            <input
              type="text"
              placeholder={t("models.displayNamePlaceholder")}
              value={displayName()}
              onInput={(e) => setDisplayName(e.currentTarget.value)}
              class="w-full rounded-xl border border-[var(--border-strong)] bg-[var(--bg-elevated)] px-3 py-2 text-[12.5px] text-[var(--text-primary)] outline-none focus:border-[var(--accent-primary)] selectable-text"
            />
          </div>
          <div>
            <label class="block text-[11.5px] font-medium text-[var(--text-secondary)] mb-1">
              {t("models.providerApi")}
            </label>
            <select
              value={npmPkg()}
              onChange={(e) => setNpmPkg(e.currentTarget.value)}
              class="w-full rounded-xl border border-[var(--border-strong)] bg-[var(--bg-elevated)] px-2.5 py-2 text-[12px] text-[var(--text-primary)] outline-none focus:border-[var(--accent-primary)]"
            >
              <For each={PROVIDER_VARIANTS}>
                {(v) => <option value={v.value}>{v.label}</option>}
              </For>
            </select>
          </div>
        </div>

        {/* Row 2: Base URL + API Key + Fetch Models Button */}
        <div class="grid grid-cols-12 gap-3 items-end">
          <div class="col-span-6">
            <label class="block text-[11.5px] font-medium text-[var(--text-secondary)] mb-1">
              {t("models.baseUrl")}
            </label>
            <input
              type="text"
              placeholder="https://api.ainode.run/v1"
              value={baseURL()}
              onInput={(e) => setBaseURL(e.currentTarget.value)}
              class="w-full rounded-xl border border-[var(--border-strong)] bg-[var(--bg-elevated)] px-3 py-2 font-mono text-[12px] text-[var(--text-primary)] outline-none focus:border-[var(--accent-primary)] selectable-text"
            />
          </div>
          <div class="col-span-4">
            <label class="block text-[11.5px] font-medium text-[var(--text-secondary)] mb-1">
              {t("models.apiKeyOptional")}
            </label>
            <input
              type="password"
              placeholder={t("models.customApiKeyPlaceholder")}
              value={apiKey()}
              onFocus={() => {
                if (apiKey() === MASKED_SECRET) setApiKey("");
              }}
              onBlur={() => {
                if (!apiKey().trim() && preserveExistingKey()) {
                  setApiKey(MASKED_SECRET);
                }
              }}
              onInput={(e) => setApiKey(e.currentTarget.value)}
              class="w-full rounded-xl border border-[var(--border-strong)] bg-[var(--bg-elevated)] px-3 py-2 font-mono text-[12px] text-[var(--text-primary)] outline-none focus:border-[var(--accent-primary)] selectable-text"
            />
          </div>
          <div class="col-span-2">
            <button
              type="button"
              disabled={fetching() || !baseURL().trim()}
              onClick={() => void onFetchModels()}
              class="w-full inline-flex items-center justify-center gap-1.5 rounded-xl border border-[var(--accent-border)] bg-[var(--accent-surface)] px-3 py-2 text-[12px] font-medium text-[var(--accent-primary)] hover:opacity-90 disabled:opacity-50 transition-opacity cursor-pointer"
            >
              <Show
                when={!fetching()}
                fallback={<LoaderCircle size={13} class="animate-spin" />}
              >
                <RefreshCw size={13} />
              </Show>
              {fetching() ? t("models.fetchingModels") : t("models.fetchModels")}
            </button>
          </div>
        </div>

        <CustomProviderModelSection
          models={models}
          setModels={setModels}
          fetchedModels={fetchedModels}
          setFetchedModels={setFetchedModels}
          selectedFetched={selectedFetched}
          setSelectedFetched={setSelectedFetched}
          onApplyFetchedModels={applyFetchedModels}
          headers={headers}
          setHeaders={setHeaders}
          modelProbes={modelProbes}
          onProbeModel={onProbeModel}
        />

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

      {/* Footer */}
      <div class="flex items-center justify-between border-t border-[var(--border-subtle)] bg-[var(--bg-elevated)]/30 px-5 py-3">
        <div>
          <Show when={isEditing()}>
            <button
              type="button"
              disabled={saving()}
              onClick={() => void onDeleteProvider()}
              class="inline-flex items-center gap-1.5 rounded-xl border border-[var(--danger-border)] bg-[var(--danger-surface)] px-3 py-1.5 text-[12px] font-medium text-[var(--danger-text)] hover:opacity-90 cursor-pointer"
            >
              <Trash2 size={13} />
              {t("common.delete")}
            </button>
          </Show>
        </div>
        <button
          type="submit"
          disabled={saving()}
          class="inline-flex items-center gap-1.5 rounded-xl bg-[var(--accent-primary)] px-4 py-2 text-[12.5px] font-medium text-white hover:opacity-90 disabled:opacity-50 transition-opacity cursor-pointer"
        >
          <Show when={!saving()} fallback={<LoaderCircle size={14} class="animate-spin" />}>
            <Check size={14} />
          </Show>
          {t("models.saveAndSelectModel")}
        </button>
      </div>
    </form>
  );
}
