// web/src/components/models/CustomEndpointTab.tsx
import { createEffect, createMemo, createSignal, For, Show, untrack } from "solid-js";
import { Check, LoaderCircle, Lock, Plus, RefreshCw, Trash2 } from "lucide-solid";
import { hoxiAPI } from "../../services/api";
import {
  disconnectProvider,
  isImportedOpenCodeProvider,
  MASKED_SECRET,
  recordProbeTelemetry,
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
import ProviderQualityCard from "./ProviderQualityCard";
import {
  extractCustomProviderFormState,
  listExistingCustomProviders,
  PROVIDER_VARIANTS,
} from "./custom-endpoint-helpers";

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

  const existingCustomProviders = createMemo(() => listExistingCustomProviders());

  const isEditing = createMemo(() => Boolean(workspace.editingProviderID));
  const isReadOnlyImported = createMemo(() =>
    Boolean(workspace.editingProviderID && isImportedOpenCodeProvider(workspace.editingProviderID))
  );

  const loadProviderIntoForm = (editID: string | null) => {
    setFeedback(null);
    setFetchedModels([]);
    setSelectedFetched({});
    setModelProbes({});

    const next = extractCustomProviderFormState(editID);
    setProviderID(next.providerID);
    setDisplayName(next.displayName);
    setNpmPkg(next.npmPkg);
    setBaseURL(next.baseURL);
    setApiKey(next.apiKey);
    setPreserveExistingKey(next.preserveExistingKey);
    setModels(next.models);
    setHeaders(next.headers);
  };

  createEffect(() => {
    const editID = workspace.editingProviderID;
    untrack(() => loadProviderIntoForm(editID));
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
    const newlyAdded: string[] = [];
    for (const rm of chosen) {
      if (!existingMap.has(rm.id)) {
        existingMap.set(rm.id, guessFetchedModelDraft(rm));
        newlyAdded.push(rm.id);
      }
    }
    setModels(Array.from(existingMap.values()));
    setFetchedModels([]);
    for (const mid of newlyAdded.slice(0, 3)) void onProbeModel(mid);
  };

  const onProbeModel = async (mid: string) => {
    if (!mid.trim()) return;
    setModelProbes((prev) => ({ ...prev, [mid]: { status: "testing" } }));
    const hdrs: Record<string, string> = {};
    for (const h of headers()) if (h.key.trim() && h.value.trim()) hdrs[h.key.trim()] = h.value.trim();
    const pid = providerID().trim();
    try {
      const res = await hoxiAPI.testProviderModel({
        providerID: pid || undefined,
        baseURL: baseURL().trim() || undefined,
        apiKey: apiKey().trim() === MASKED_SECRET ? undefined : apiKey().trim() || undefined,
        npm: npmPkg(),
        modelID: mid.trim(),
        headers: Object.keys(hdrs).length ? hdrs : undefined,
      });
      if (pid) recordProbeTelemetry(pid, res.ok, res.latencyMs, res.error, mid.trim());
      setModelProbes((prev) => ({
        ...prev,
        [mid]: res.ok ? { status: "ok", latencyMs: res.latencyMs } : { status: "error", error: res.error || "Failed" },
      }));
    } catch (err: any) {
      const errMsg = err?.message || String(err);
      if (pid) recordProbeTelemetry(pid, false, undefined, errMsg, mid.trim());
      setModelProbes((prev) => ({ ...prev, [mid]: { status: "error", error: errMsg } }));
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
    for (const h of headers()) if (h.key.trim() && h.value.trim()) hdrMap[h.key.trim()] = h.value.trim();

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
      setFeedback({ ok: true, text: t("models.providerSaved", { name, count: validModels.length }) });
    } catch (err: any) {
      setFeedback({ ok: false, text: err?.message || t("models.saveCustomProviderFailed") });
    } finally {
      setSaving(false);
    }
  };

  const onForkToHoxiEdit = async () => {
    const pid = providerID().trim();
    if (!pid || saving()) return;
    const name = displayName().trim() || pid;
    const url = baseURL().trim();
    const validModels = models().filter((m) => m.id.trim().length > 0);
    const hdrMap: Record<string, string> = {};
    for (const h of headers()) if (h.key.trim() && h.value.trim()) hdrMap[h.key.trim()] = h.value.trim();

    setSaving(true);
    setFeedback(null);
    try {
      await saveCustomProvider({
        providerID: pid,
        name,
        npm: npmPkg(),
        baseURL: url,
        apiKey: apiKey(),
        preserveExistingKey: true,
        headers: hdrMap,
        models: validModels,
      });
      setFeedback({ ok: true, text: t("models.forkToHoxiSuccess", { name }) });
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
              {isReadOnlyImported()
                ? t("models.viewImportedEndpoint")
                : isEditing()
                ? t("models.editCustomEndpoint")
                : t("models.addCustomEndpoint")}
            </div>
            <div class="text-[11px] text-[var(--text-muted)] truncate">
              {t("models.customEndpointIntro")}
            </div>
          </div>

          <Show when={existingCustomProviders().length > 0}>
            <div class="flex items-center gap-2 shrink-0">
              <select
                value={workspace.editingProviderID || ""}
                onChange={(e) => setWorkspace("editingProviderID", e.currentTarget.value || null)}
                class="max-w-[240px] rounded-lg border border-[var(--border-strong)] bg-[var(--bg-panel)] px-2.5 py-1 text-[11.5px] font-medium text-[var(--text-primary)] outline-none focus:border-[var(--accent-primary)]"
              >
                <option value="">+ {t("models.newCustomProvider")}</option>
                <For each={existingCustomProviders()}>
                  {(item) => (
                    <option value={item.id}>
                      {item.name} ({item.id}) ·{" "}
                      {item.imported ? t("models.sourceOpenCode") : t("models.sourceHoxi")}
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

        <Show when={isReadOnlyImported()}>
          <div class="flex items-center justify-between gap-3 rounded-xl border border-amber-500/35 bg-amber-500/10 px-3.5 py-2.5 text-[12px] text-amber-600 dark:text-amber-300">
            <div class="flex items-start gap-2.5 min-w-0">
              <Lock size={14} class="shrink-0 mt-0.5" />
              <span>{t("models.importedOpenCodeReadonlyBanner")}</span>
            </div>
            <button
              type="button"
              disabled={saving()}
              onClick={() => void onForkToHoxiEdit()}
              class="shrink-0 inline-flex items-center gap-1.5 rounded-lg bg-[var(--accent-primary)] px-3 py-1.5 text-[11.5px] font-medium text-[var(--accent-foreground)] hover:opacity-90 disabled:opacity-50 transition-opacity cursor-pointer"
            >
              {t("models.forkToHoxiEdit")}
            </button>
          </div>
        </Show>

        <ProviderQualityCard providerID={providerID()} />

        {/* Row 1: Provider ID, Display Name, Provider API */}
        <div class="grid grid-cols-3 gap-3">
          <div>
            <label class="block text-[11.5px] font-medium text-[var(--text-secondary)] mb-1">
              {t("models.providerId")}
            </label>
            <input
              type="text"
              disabled={isEditing()}
              autocapitalize="off"
              autocorrect="off"
              spellcheck={false}
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
              disabled={isReadOnlyImported()}
              placeholder={t("models.displayNamePlaceholder")}
              value={displayName()}
              onInput={(e) => setDisplayName(e.currentTarget.value)}
              class="w-full rounded-xl border border-[var(--border-strong)] bg-[var(--bg-elevated)] px-3 py-2 text-[12.5px] text-[var(--text-primary)] disabled:opacity-60 outline-none focus:border-[var(--accent-primary)] selectable-text"
            />
          </div>
          <div>
            <label class="block text-[11.5px] font-medium text-[var(--text-secondary)] mb-1">
              {t("models.providerApi")}
            </label>
            <select
              disabled={isReadOnlyImported()}
              value={npmPkg()}
              onChange={(e) => setNpmPkg(e.currentTarget.value)}
              class="w-full rounded-xl border border-[var(--border-strong)] bg-[var(--bg-elevated)] px-2.5 py-2 text-[12px] text-[var(--text-primary)] disabled:opacity-60 outline-none focus:border-[var(--accent-primary)]"
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
              disabled={isReadOnlyImported()}
              autocapitalize="off"
              autocorrect="off"
              spellcheck={false}
              placeholder="https://api.example.com/v1"
              value={baseURL()}
              onInput={(e) => setBaseURL(e.currentTarget.value)}
              class="w-full rounded-xl border border-[var(--border-strong)] bg-[var(--bg-elevated)] px-3 py-2 font-mono text-[12px] text-[var(--text-primary)] disabled:opacity-60 outline-none focus:border-[var(--accent-primary)] selectable-text"
            />
          </div>
          <div class="col-span-4">
            <label class="block text-[11.5px] font-medium text-[var(--text-secondary)] mb-1">
              {t("models.apiKeyOptional")}
            </label>
            <input
              type="password"
              disabled={isReadOnlyImported()}
              placeholder={t("models.customApiKeyPlaceholder")}
              value={apiKey()}
              onFocus={() => {
                if (apiKey() === MASKED_SECRET) setApiKey("");
              }}
              onBlur={() => {
                if (!apiKey().trim() && preserveExistingKey()) setApiKey(MASKED_SECRET);
              }}
              onInput={(e) => setApiKey(e.currentTarget.value)}
              class="w-full rounded-xl border border-[var(--border-strong)] bg-[var(--bg-elevated)] px-3 py-2 font-mono text-[12px] text-[var(--text-primary)] disabled:opacity-60 outline-none focus:border-[var(--accent-primary)] selectable-text"
            />
          </div>
          <div class="col-span-2">
            <button
              type="button"
              disabled={isReadOnlyImported() || fetching() || !baseURL().trim()}
              onClick={() => void onFetchModels()}
              class="w-full inline-flex items-center justify-center gap-1.5 rounded-xl border border-[var(--accent-border)] bg-[var(--accent-surface)] px-3 py-2 text-[12px] font-medium text-[var(--accent-primary)] hover:opacity-90 disabled:opacity-50 transition-opacity cursor-pointer"
            >
              <Show when={!fetching()} fallback={<LoaderCircle size={13} class="animate-spin" />}>
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
          readOnly={isReadOnlyImported}
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
        <div class="flex items-center gap-3">
          <Show when={isEditing() && !isReadOnlyImported()}>
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
          <span class="font-mono text-[11px] text-[var(--text-faint)]">
            {isReadOnlyImported() ? t("models.openCodeReadonlyNote") : t("models.hoxiConfigNote")}
          </span>
        </div>
        <Show when={!isReadOnlyImported()}>
          <button
            type="submit"
            disabled={saving()}
            class="inline-flex items-center gap-1.5 rounded-xl bg-[var(--accent-primary)] px-4 py-2 text-[12.5px] font-medium text-[var(--accent-foreground)] hover:opacity-90 disabled:opacity-50 transition-opacity cursor-pointer"
          >
            <Show when={!saving()} fallback={<LoaderCircle size={14} class="animate-spin" />}>
              <Check size={14} />
            </Show>
            {t("models.saveAndSelectModel")}
          </button>
        </Show>
      </div>
    </form>
  );
}
