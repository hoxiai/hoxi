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
import { ProviderIcon } from "../icons";
import { t } from "../../i18n";
import CustomProviderModelSection from "./CustomProviderModelSection";

const PROVIDER_VARIANTS = [
  {
    value: "@ai-sdk/openai-compatible",
    label: "OpenAI Compatible (/v1/chat/completions)",
  },
  {
    value: "@ai-sdk/openai",
    label: "OpenAI Responses (/v1/responses)",
  },
  {
    value: "@ai-sdk/anthropic",
    label: "Anthropic Messages (/v1/messages)",
  },
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

  // Fetch models state
  const [fetching, setFetching] = createSignal(false);
  const [fetchedModels, setFetchedModels] = createSignal<Array<{ id: string; name: string }>>([]);
  const [selectedFetched, setSelectedFetched] = createSignal<Record<string, boolean>>({});
  const [saving, setSaving] = createSignal(false);
  const [feedback, setFeedback] = createSignal<{ ok: boolean; text: string } | null>(null);

  const existingCustomProviders = createMemo(() => {
    const map = new Map<string, { id: string; name: string }>();
    const cfgProviders = workspace.globalConfig?.provider ?? {};
    for (const [id, cfg] of Object.entries<any>(cfgProviders)) {
      if (cfg && typeof cfg === "object") {
        map.set(id, { id, name: cfg.name || id });
      }
    }
    for (const p of workspace.providers) {
      if (isCustomOrConfiguredProvider(p) && !map.has(p.id)) {
        map.set(p.id, { id: p.id, name: p.name || p.id });
      }
    }
    return Array.from(map.values());
  });

  const isEditing = createMemo(() => Boolean(workspace.editingProviderID));

  const loadProviderIntoForm = (editID: string | null) => {
    setFeedback(null);
    setFetchedModels([]);
    setSelectedFetched({});

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
      setFeedback({ ok: false, text: "请先填写基础 URL (Base URL)。" });
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
        setFeedback({ ok: false, text: res.error || "获取模型失败" });
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
      setFeedback({ ok: false, text: err?.message || "获取模型失败" });
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
        const lower = rm.id.toLowerCase();
        const guessReasoning =
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
        const guessImage =
          lower.includes("vision") ||
          lower.includes("vl") ||
          lower.includes("claude") ||
          lower.includes("gpt-4") ||
          lower.includes("gpt-5") ||
          lower.includes("gpt-6") ||
          lower.includes("gemini");
        existingMap.set(rm.id, {
          id: rm.id,
          name: rm.name || rm.id,
          reasoning: guessReasoning,
          image: guessImage,
        });
      }
    }

    setModels(Array.from(existingMap.values()));
    setFetchedModels([]);
  };

  const onSave = async (ev: Event) => {
    ev.preventDefault();
    if (saving()) return;

    const pid = providerID().trim();
    const name = displayName().trim() || pid;
    const url = baseURL().trim();
    const validModels = models().filter((m) => m.id.trim().length > 0);

    if (!pid || !/^[a-zA-Z0-9._-]+$/.test(pid)) {
      setFeedback({
        ok: false,
        text: "提供商 ID 必填，且仅允许包含字母、数字、点、下划线或连字符。",
      });
      return;
    }
    if (!url || !/^https?:\/\/.+/.test(url)) {
      setFeedback({ ok: false, text: "请输入以 http:// 或 https:// 开头的有效 Base URL。" });
      return;
    }
    if (validModels.length === 0) {
      setFeedback({ ok: false, text: "请至少添加或拉取一个模型。" });
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
        text: `已保存提供商 "${name}"（共 ${validModels.length} 个模型）！`,
      });
    } catch (err: any) {
      setFeedback({ ok: false, text: err?.message || "保存自定义提供商失败" });
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
        {/* Existing Custom Providers Quick Switcher */}
        <Show when={existingCustomProviders().length > 0}>
          <div class="flex items-center gap-1.5 flex-wrap rounded-xl border border-[var(--border-subtle)] bg-[var(--bg-elevated)]/50 px-3 py-2">
            <span class="text-[11px] font-medium text-[var(--text-muted)] mr-1">
              已配置自定义提供商:
            </span>
            <For each={existingCustomProviders()}>
              {(item) => (
                <button
                  type="button"
                  onClick={() => setWorkspace("editingProviderID", item.id)}
                  class={`inline-flex items-center gap-1.5 rounded-lg px-2.5 py-1 text-[11.5px] font-medium transition-colors cursor-pointer ${
                    workspace.editingProviderID === item.id
                      ? "bg-[var(--accent-primary)] text-white"
                      : "bg-[var(--bg-panel)] text-[var(--text-secondary)] hover:text-[var(--text-primary)] border border-[var(--border-subtle)]"
                  }`}
                >
                  <ProviderIcon id={item.id} class="w-3.5 h-3.5 shrink-0" />
                  <span>{item.name}</span>
                  <span class="opacity-70 font-mono text-[10px]">({item.id})</span>
                </button>
              )}
            </For>
            <button
              type="button"
              onClick={() => setWorkspace("editingProviderID", null)}
              class={`inline-flex items-center gap-1 rounded-lg px-2.5 py-1 text-[11.5px] font-medium transition-colors cursor-pointer ${
                !workspace.editingProviderID
                  ? "bg-[var(--accent-surface)] text-[var(--accent-primary)] border border-[var(--accent-border)]"
                  : "text-[var(--text-muted)] hover:text-[var(--text-primary)]"
              }`}
            >
              <Plus size={12} />
              新建
            </button>
          </div>
        </Show>

        {/* Row 1: Provider ID, Display Name, Provider API */}
        <div class="grid grid-cols-3 gap-3">
          <div>
            <label class="block text-[11.5px] font-medium text-[var(--text-secondary)] mb-1">
              {t("models.providerId")}
            </label>
            <input
              type="text"
              disabled={isEditing()}
              placeholder="例如 ainode 或 deepseek-custom"
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
              placeholder="例如 AINode"
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
              placeholder="sk-... 或 {env:API_KEY}"
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
