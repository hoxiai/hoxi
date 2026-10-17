// web/src/components/models/ProviderCatalogTab.tsx
import { createMemo, createSignal, For, Show } from "solid-js";
import { Check, LoaderCircle, Search, Trash2 } from "lucide-solid";
import {
  connectProviderWithKey,
  disconnectProvider,
  workspace,
} from "../../store/workspace";
import { ProviderIcon } from "../icons";

const POPULAR_PROVIDER_IDS = [
  { id: "deepseek", name: "DeepSeek", hint: "deepseek-chat, deepseek-reasoner, v4.1-flash" },
  { id: "anthropic", name: "Anthropic", hint: "Claude 4.5 / 4 Sonnet & Opus" },
  { id: "openai", name: "OpenAI", hint: "GPT-5, o3, GPT-4.1" },
  { id: "openrouter", name: "OpenRouter", hint: "Unified gateway for 300+ models" },
  { id: "google", name: "Google Gemini", hint: "Gemini 2.5 Pro & Flash" },
  { id: "xai", name: "xAI (Grok)", hint: "Grok 3 & Grok 3 Mini" },
  { id: "alibaba", name: "Alibaba DashScope", hint: "Qwen3 / Qwen-Max / Qwen-Coder" },
  { id: "zai", name: "Z.AI (Zhipu GLM)", hint: "GLM-4.5 / GLM-5" },
  { id: "moonshotai", name: "Moonshot AI (Kimi)", hint: "Kimi K2 / moonshot-v1" },
  { id: "minimax", name: "MiniMax", hint: "MiniMax-M1 / Text-01" },
];

export default function ProviderCatalogTab() {
  const [search, setSearch] = createSignal("");
  const [selectedID, setSelectedID] = createSignal("deepseek");
  const [apiKey, setApiKey] = createSignal("");
  const [baseURL, setBaseURL] = createSignal("");
  const [extraModelID, setExtraModelID] = createSignal("");
  const [saving, setSaving] = createSignal(false);
  const [feedback, setFeedback] = createSignal<{ ok: boolean; text: string } | null>(null);

  const connectedSet = createMemo(() => new Set(workspace.connectedProviders));

  const providerList = createMemo(() => {
    const q = search().trim().toLowerCase();
    const map = new Map<string, { id: string; name: string; hint: string; connected: boolean }>();

    for (const p of POPULAR_PROVIDER_IDS) {
      map.set(p.id, {
        ...p,
        connected: connectedSet().has(p.id),
      });
    }

    for (const p of workspace.providers) {
      if (!map.has(p.id)) {
        const modelNames = Object.keys(p.models || {}).slice(0, 3).join(", ");
        map.set(p.id, {
          id: p.id,
          name: p.name || p.id,
          hint: modelNames || "OpenCode built-in provider",
          connected: connectedSet().has(p.id),
        });
      } else {
        const existing = map.get(p.id)!;
        existing.connected = connectedSet().has(p.id);
      }
    }

    const list = Array.from(map.values()).sort((a, b) => {
      if (a.connected !== b.connected) return a.connected ? -1 : 1;
      return 0;
    });

    if (!q) return list;
    return list.filter(
      (item) =>
        item.id.toLowerCase().includes(q) ||
        item.name.toLowerCase().includes(q) ||
        item.hint.toLowerCase().includes(q)
    );
  });

  const activeProvider = createMemo(
    () =>
      providerList().find((p) => p.id === selectedID()) || {
        id: selectedID(),
        name: selectedID(),
        hint: "",
        connected: connectedSet().has(selectedID()),
      }
  );

  const onSaveCatalogProvider = async (ev: Event) => {
    ev.preventDefault();
    if (saving()) return;
    if (!apiKey().trim() && !baseURL().trim() && !extraModelID().trim()) {
      setFeedback({ ok: false, text: "Please enter an API Key or custom configuration." });
      return;
    }

    setSaving(true);
    setFeedback(null);
    try {
      await connectProviderWithKey({
        providerID: selectedID(),
        apiKey: apiKey(),
        baseURL: baseURL() || undefined,
        customModelID: extraModelID() || undefined,
      });
      setApiKey("");
      setFeedback({ ok: true, text: `Connected ${activeProvider().name} successfully!` });
    } catch (err: any) {
      setFeedback({ ok: false, text: err?.message || "Failed to connect provider." });
    } finally {
      setSaving(false);
    }
  };

  const onDisconnect = async (providerID: string) => {
    if (saving()) return;
    setSaving(true);
    setFeedback(null);
    try {
      await disconnectProvider(providerID);
      setFeedback({ ok: true, text: `Disconnected ${providerID}.` });
    } catch (err: any) {
      setFeedback({ ok: false, text: err?.message || "Failed to disconnect." });
    } finally {
      setSaving(false);
    }
  };

  return (
    <div class="flex flex-1 min-h-0">
      {/* Left Provider List */}
      <div class="w-[250px] shrink-0 border-r border-[var(--border-subtle)] flex flex-col">
        <div class="p-2.5 border-b border-[var(--border-subtle)]">
          <div class="flex items-center gap-2 rounded-xl border border-[var(--border-subtle)] bg-[var(--bg-elevated)] px-2.5 py-1.5">
            <Search size={13} class="text-[var(--text-faint)] shrink-0" />
            <input
              type="text"
              placeholder="Filter providers…"
              value={search()}
              onInput={(e) => setSearch(e.currentTarget.value)}
              class="w-full bg-transparent text-[12px] text-[var(--text-primary)] placeholder:text-[var(--text-faint)] outline-none selectable-text"
            />
          </div>
        </div>

        <div class="flex-1 overflow-y-auto p-2 space-y-1">
          <For each={providerList()}>
            {(item) => (
              <button
                type="button"
                onClick={() => {
                  setSelectedID(item.id);
                  setFeedback(null);
                }}
                class={`w-full flex items-center justify-between gap-2.5 rounded-xl px-3 py-2 text-left transition-colors cursor-pointer ${
                  selectedID() === item.id
                    ? "bg-[var(--accent-surface)] text-[var(--accent-primary)] font-medium"
                    : "text-[var(--text-secondary)] hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)]"
                }`}
              >
                <ProviderIcon id={item.id} class="w-4 h-4 shrink-0" />
                <div class="min-w-0 flex-1">
                  <div class="truncate text-[12.5px]">{item.name}</div>
                  <div class="truncate text-[10.5px] text-[var(--text-faint)]">
                    {item.hint}
                  </div>
                </div>
                <Show when={item.connected}>
                  <span class="shrink-0 inline-flex items-center gap-1 rounded-full bg-[var(--ok-surface)] px-2 py-0.5 text-[10px] font-medium text-[var(--ok-text)]">
                    <span class="h-1.5 w-1.5 rounded-full bg-[var(--ok)]" />
                    Active
                  </span>
                </Show>
              </button>
            )}
          </For>
        </div>
      </div>

      {/* Right Provider Configuration Form */}
      <form
        onSubmit={onSaveCatalogProvider}
        class="flex-1 flex flex-col justify-between p-6 overflow-y-auto"
      >
        <div class="space-y-4">
          <div class="flex items-center justify-between">
            <div class="flex items-center gap-2.5">
              <ProviderIcon id={activeProvider().id} class="w-5 h-5 shrink-0" />
              <div>
                <h3 class="text-[15px] font-semibold text-[var(--text-primary)]">
                  {activeProvider().name}
                </h3>
                <p class="text-[11.5px] font-mono text-[var(--text-muted)]">
                  Provider ID: {activeProvider().id}
                </p>
              </div>
            </div>
            <Show when={activeProvider().connected}>
              <button
                type="button"
                disabled={saving()}
                onClick={() => void onDisconnect(activeProvider().id)}
                class="inline-flex items-center gap-1.5 rounded-xl border border-[var(--danger-border)] bg-[var(--danger-surface)] px-3 py-1.5 text-[11.5px] font-medium text-[var(--danger-text)] hover:opacity-90 transition-opacity cursor-pointer"
              >
                <Trash2 size={12} />
                Disconnect
              </button>
            </Show>
          </div>

          <div>
            <label class="block text-[11.5px] font-medium text-[var(--text-secondary)] mb-1.5">
              API Key
            </label>
            <input
              type="password"
              placeholder={
                activeProvider().connected
                  ? "•••••••••••••••• (Connected — enter new key to replace)"
                  : `Enter your ${activeProvider().name} API key…`
              }
              value={apiKey()}
              onInput={(e) => setApiKey(e.currentTarget.value)}
              class="w-full rounded-xl border border-[var(--border-strong)] bg-[var(--bg-elevated)] px-3.5 py-2.5 font-mono text-[12.5px] text-[var(--text-primary)] outline-none focus:border-[var(--accent-primary)] selectable-text"
            />
          </div>

          <div>
            <label class="block text-[11.5px] font-medium text-[var(--text-secondary)] mb-1.5">
              Custom Base URL <span class="text-[var(--text-faint)] font-normal">(Optional)</span>
            </label>
            <input
              type="text"
              placeholder="Leave blank for official endpoint, or enter proxy/gateway URL"
              value={baseURL()}
              onInput={(e) => setBaseURL(e.currentTarget.value)}
              class="w-full rounded-xl border border-[var(--border-subtle)] bg-[var(--bg-elevated)] px-3.5 py-2 font-mono text-[12px] text-[var(--text-primary)] outline-none focus:border-[var(--accent-primary)] selectable-text"
            />
          </div>

          <div>
            <label class="block text-[11.5px] font-medium text-[var(--text-secondary)] mb-1.5">
              Register Additional Model ID{" "}
              <span class="text-[var(--text-faint)] font-normal">
                (Optional, e.g. deepseek-v4.1-flash)
              </span>
            </label>
            <input
              type="text"
              placeholder="e.g. deepseek-v4.1-flash"
              value={extraModelID()}
              onInput={(e) => setExtraModelID(e.currentTarget.value)}
              class="w-full rounded-xl border border-[var(--border-subtle)] bg-[var(--bg-elevated)] px-3.5 py-2 font-mono text-[12px] text-[var(--text-primary)] outline-none focus:border-[var(--accent-primary)] selectable-text"
            />
          </div>

          <Show when={feedback()}>
            <div
              class={`rounded-xl border px-3.5 py-2.5 text-[12px] ${
                feedback()!.ok
                  ? "border-[var(--ok-border)] bg-[var(--ok-surface)] text-[var(--ok-text)]"
                  : "border-[var(--danger-border)] bg-[var(--danger-surface)] text-[var(--danger-text)]"
              }`}
            >
              {feedback()!.text}
            </div>
          </Show>
        </div>

        <div class="flex items-center justify-between border-t border-[var(--border-subtle)] pt-4 mt-4">
          <span class="text-[11px] text-[var(--text-faint)]">
            Keys are stored locally in OpenCode's encrypted auth store.
          </span>
          <button
            type="submit"
            disabled={saving()}
            class="inline-flex items-center gap-1.5 rounded-xl bg-[var(--accent-primary)] px-4 py-2 text-[12.5px] font-medium text-white hover:opacity-90 disabled:opacity-50 transition-opacity cursor-pointer"
          >
            <Show when={!saving()} fallback={<LoaderCircle size={14} class="animate-spin" />}>
              <Check size={14} />
            </Show>
            Save & Connect
          </button>
        </div>
      </form>
    </div>
  );
}
