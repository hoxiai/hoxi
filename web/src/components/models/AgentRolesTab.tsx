// web/src/components/models/AgentRolesTab.tsx
import { createMemo, createSignal, For, onMount, Show } from "solid-js";
import {
  Bot,
  Bug,
  Check,
  ChevronDown,
  ChevronUp,
  Compass,
  Hammer,
  LoaderCircle,
  MessageCircleQuestion,
  Pencil,
  Plus,
  RotateCcw,
  Sparkles,
  Trash2,
} from "lucide-solid";
import {
  fetchGlobalConfig,
  loadAgents,
  updateGlobalModelConfig,
  workspace,
} from "../../store/workspace";
import { t } from "../../i18n";
import { getConnectedProviderModelGroups } from "./model-selector-utils";

function agentIcon(name: string) {
  const lower = name.toLowerCase();
  if (lower === "build") return <Hammer size={16} class="shrink-0" />;
  if (lower === "plan") return <Compass size={16} class="shrink-0" />;
  if (lower === "ask") return <MessageCircleQuestion size={16} class="shrink-0" />;
  if (lower === "debug") return <Bug size={16} class="shrink-0" />;
  return <Sparkles size={16} class="shrink-0" />;
}

// Hoxi-owned agents injected by the runtime overlay; they cannot be removed.
const BUILTIN_MODE_IDS = new Set(["build", "plan", "ask", "debug", "compaction"]);

export default function AgentRolesTab() {
  const [savingKey, setSavingKey] = createSignal<string | null>(null);
  const [savedKey, setSavedKey] = createSignal<string | null>(null);

  // New Custom Agent Form State
  const [newAgentId, setNewAgentId] = createSignal("");
  const [newAgentDesc, setNewAgentDesc] = createSignal("");
  const [newAgentModel, setNewAgentModel] = createSignal("");
  const [newAgentPrompt, setNewAgentPrompt] = createSignal("");
  const [creating, setCreating] = createSignal(false);
  const [formFeedback, setFormFeedback] = createSignal<{ ok: boolean; text: string } | null>(null);

  // Editing existing custom agent
  const [editingAgentId, setEditingAgentId] = createSignal<string | null>(null);
  const [editDesc, setEditDesc] = createSignal("");
  const [editPrompt, setEditPrompt] = createSignal("");

  // Expanded prompt visibility toggles
  const [expandedPrompts, setExpandedPrompts] = createSignal<Record<string, boolean>>({});

  const connectedGroups = createMemo(() => getConnectedProviderModelGroups());

  onMount(async () => {
    try {
      await loadAgents(workspace.rootDir || undefined);
    } catch {
      /* ignore */
    }
  });

  const togglePromptExpanded = (id: string) => {
    setExpandedPrompts((prev) => ({ ...prev, [id]: !prev[id] }));
  };

  const applyAgentModel = async (agentName: string, modelKey: string) => {
    setSavingKey(agentName);
    setSavedKey(null);
    try {
      await updateGlobalModelConfig({
        agent: {
          [agentName]: { model: modelKey || null },
        },
      });
      setSavedKey(agentName);
      setTimeout(() => {
        if (savedKey() === agentName) setSavedKey(null);
      }, 1500);
    } finally {
      setSavingKey(null);
    }
  };

  const onAddCustomAgent = async (ev: Event) => {
    ev.preventDefault();
    const id = newAgentId()
      .trim()
      .toLowerCase()
      .replace(/[^a-z0-9_-]/g, "-");
    const prompt = newAgentPrompt().trim();
    if (!id || id === "build" || id === "plan" || !prompt || creating()) return;
    setCreating(true);
    setFormFeedback(null);
    try {
      const cfg = await fetchGlobalConfig();
      const existing = cfg?.agent?.[id] ?? {};
      const entry: Record<string, any> = {
        ...existing,
        mode: "primary",
        description: newAgentDesc().trim() || `Custom ${id} mode`,
        prompt,
      };
      if (newAgentModel()) {
        entry.model = newAgentModel();
      }
      await updateGlobalModelConfig({ agent: { [id]: entry } });
      setNewAgentId("");
      setNewAgentDesc("");
      setNewAgentModel("");
      setNewAgentPrompt("");
      await loadAgents(workspace.rootDir || undefined);
      setFormFeedback({ ok: true, text: t("models.agentSaved", { id }) });
      setTimeout(() => setFormFeedback(null), 3000);
    } catch (err: any) {
      setFormFeedback({ ok: false, text: err?.message || t("models.agentSaveFailed") });
    } finally {
      setCreating(false);
    }
  };

  const onRemoveCustomAgent = async (id: string) => {
    if (savingKey() === `del:${id}`) return;
    setSavingKey(`del:${id}`);
    try {
      // PATCH deep-merges; null is the only way to drop a key from .hoxi/models.json.
      await updateGlobalModelConfig({ agent: { [id]: null } });
      await loadAgents(workspace.rootDir || undefined);
    } catch {
      /* ignore */
    } finally {
      setSavingKey(null);
    }
  };

  const startEditAgent = (id: string, curDesc: string, curPrompt: string) => {
    setEditingAgentId(id);
    setEditDesc(curDesc);
    setEditPrompt(curPrompt);
  };

  const saveEditAgent = async (id: string) => {
    setSavingKey(`edit:${id}`);
    try {
      const cfg = await fetchGlobalConfig();
      const existing = cfg?.agent?.[id] ?? {};
      const entry = {
        ...existing,
        description: editDesc().trim() || existing.description,
        prompt: editPrompt().trim() || existing.prompt,
      };
      await updateGlobalModelConfig({ agent: { [id]: entry } });
      await loadAgents(workspace.rootDir || undefined);
      setEditingAgentId(null);
    } finally {
      setSavingKey(null);
    }
  };

  // Merge built-in agents and custom agents
  const agentsList = createMemo(() => {
    const list: Array<{
      id: string;
      name: string;
      isBuiltin: boolean;
      description: string;
      prompt?: string;
      model: string;
    }> = [];

    // Built-in Build
    list.push({
      id: "build",
      name: t("timeline.buildMode"),
      isBuiltin: true,
      description: t("models.agentRoleBuildDesc"),
      model: workspace.globalConfig?.agent?.build?.model || "",
      prompt: workspace.globalConfig?.agent?.build?.prompt,
    });

    // Built-in Plan
    list.push({
      id: "plan",
      name: t("timeline.planMode"),
      isBuiltin: true,
      description: t("models.agentRolePlanDesc"),
      model: workspace.globalConfig?.agent?.plan?.model || "",
      prompt: workspace.globalConfig?.agent?.plan?.prompt,
    });

    // Built-in Ask (read-only) and Debug
    list.push({
      id: "ask",
      name: t("timeline.askMode"),
      isBuiltin: true,
      description: t("models.agentRoleAskDesc"),
      model: workspace.globalConfig?.agent?.ask?.model || "",
    });
    list.push({
      id: "debug",
      name: t("timeline.debugMode"),
      isBuiltin: true,
      description: t("models.agentRoleDebugDesc"),
      model: workspace.globalConfig?.agent?.debug?.model || "",
    });

    // Custom Agents from globalConfig.agent
    const cfgAgent = workspace.globalConfig?.agent || {};
    for (const [key, val] of Object.entries(cfgAgent)) {
      if (BUILTIN_MODE_IDS.has(key)) continue;
      if (val && typeof val === "object") {
        const item = val as Record<string, any>;
        list.push({
          id: key,
          name: key,
          isBuiltin: false,
          description: item.description || t("models.agentRoleCustomDesc"),
          model: typeof item.model === "string" ? item.model : "",
          prompt: typeof item.prompt === "string" ? item.prompt : "",
        });
      }
    }

    return list;
  });

  return (
    <div class="flex-1 overflow-y-auto p-6 sm:p-8">
      <div class="mx-auto w-full max-w-4xl space-y-6">
        {/* Header */}
        <div class="flex items-center gap-3 rounded-2xl border border-[var(--border-subtle)] bg-[var(--bg-elevated)]/50 px-4 py-3">
          <div class="flex h-8 w-8 shrink-0 items-center justify-center rounded-xl bg-[var(--accent-surface)] text-[var(--accent-primary)]">
            <Bot size={16} />
          </div>
          <div>
            <h3 class="text-[13px] font-semibold text-[var(--text-primary)]">
              {t("models.agentsTitle")}
            </h3>
            <p class="text-[12px] text-[var(--text-secondary)]">
              {t("models.agentsSubtitle")}
            </p>
          </div>
        </div>

        {/* Agent Cards List */}
        <div class="space-y-3">
          <For each={agentsList()}>
            {(agent) => {
              const isEditing = () => editingAgentId() === agent.id;
              const isPromptOpen = () => Boolean(expandedPrompts()[agent.id]);

              return (
                <div class="rounded-2xl border border-[var(--border-subtle)] bg-[var(--bg-elevated)]/40 p-4 transition-colors">
                  <div class="flex flex-col sm:flex-row sm:items-center justify-between gap-3">
                    <div class="flex items-start gap-3 min-w-0">
                      <div class="flex h-8 w-8 shrink-0 items-center justify-center rounded-xl bg-[var(--bg-panel)] text-[var(--accent-primary)] border border-[var(--border-subtle)] mt-0.5">
                        {agentIcon(agent.id)}
                      </div>
                      <div class="min-w-0 space-y-0.5">
                        <div class="flex items-center gap-2 flex-wrap">
                          <span class="text-[13px] font-semibold capitalize text-[var(--text-primary)]">
                            {agent.name}
                          </span>
                          <span class="rounded font-mono text-[11px] px-1.5 py-0.5 bg-[var(--bg-inset)] text-[var(--text-muted)]">
                            {agent.id}
                          </span>
                          <span
                            class={`rounded-full px-2 py-0.5 text-[11px] font-medium ${
                              agent.isBuiltin
                                ? "bg-[var(--accent-surface)] text-[var(--accent-primary)]"
                                : "bg-emerald-500/10 text-emerald-400"
                            }`}
                          >
                            {agent.isBuiltin ? t("models.builtinBadge") : t("models.customBadge")}
                          </span>
                        </div>
                        <Show when={!isEditing()}>
                          <p class="text-[12px] text-[var(--text-muted)] leading-relaxed">
                            {agent.description}
                          </p>
                        </Show>
                      </div>
                    </div>

                    {/* Dedicated Model Selector */}
                    <div class="flex items-center gap-2 shrink-0 self-end sm:self-auto">
                      <div class="flex flex-col items-end">
                        <span class="text-[11px] font-medium uppercase tracking-wider text-[var(--text-faint)] mb-1">
                          {t("models.agentDedicatedModelLabel")}
                        </span>
                        <div class="flex items-center gap-1.5">
                          <select
                            value={agent.model}
                            onChange={(e) => void applyAgentModel(agent.id, e.currentTarget.value)}
                            class="w-[240px] rounded-xl border border-[var(--border-strong)] bg-[var(--bg-panel)] px-3 py-1.5 text-[12px] font-medium text-[var(--text-primary)] outline-none focus:border-[var(--accent-primary)] cursor-pointer"
                          >
                            <option value="">{t("models.agentModelFollowDefault")}</option>
                            <For each={connectedGroups()}>
                              {(grp) => (
                                <optgroup label={`${grp.providerName} (${grp.providerID})`}>
                                  <For each={grp.models}>
                                    {(m) => (
                                      <option value={m.fullKey}>
                                        {m.name} ({m.id})
                                        {m.free ? ` · [${t("models.freeModel")}]` : ""}
                                      </option>
                                    )}
                                  </For>
                                </optgroup>
                              )}
                            </For>
                          </select>

                          <Show when={agent.model}>
                            <button
                              type="button"
                              onClick={() => void applyAgentModel(agent.id, "")}
                              class="rounded-lg p-1.5 text-[var(--text-faint)] hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)] transition-colors cursor-pointer"
                              title={t("models.agentModelFollowDefault")}
                            >
                              <RotateCcw size={13} />
                            </button>
                          </Show>

                          <div class="w-4 flex items-center justify-center">
                            <Show when={savingKey() === agent.id}>
                              <LoaderCircle size={13} class="animate-spin text-[var(--accent-primary)]" />
                            </Show>
                            <Show when={savedKey() === agent.id}>
                              <Check size={13} class="text-[var(--ok)]" />
                            </Show>
                          </div>
                        </div>
                      </div>

                      {/* Custom agent actions (edit, delete) */}
                      <Show when={!agent.isBuiltin}>
                        <div class="flex items-center gap-1 pl-1">
                          <Show when={!isEditing()}>
                            <button
                              type="button"
                              onClick={() => startEditAgent(agent.id, agent.description, agent.prompt || "")}
                              class="rounded-lg p-1.5 text-[var(--text-faint)] hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)] cursor-pointer"
                              title={t("models.agentEditMode")}
                            >
                              <Pencil size={13} />
                            </button>
                          </Show>
                          <button
                            type="button"
                            onClick={() => void onRemoveCustomAgent(agent.id)}
                            class="rounded-lg p-1.5 text-[var(--text-faint)] hover:bg-rose-500/10 hover:text-rose-400 cursor-pointer"
                            title={t("models.removeAgentTooltip")}
                          >
                            <Trash2 size={13} />
                          </button>
                        </div>
                      </Show>
                    </div>
                  </div>

                  {/* Inline editing for custom agent */}
                  <Show when={isEditing()}>
                    <div class="mt-3 pt-3 border-t border-[var(--border-subtle)] space-y-2.5">
                      <div>
                        <label class="block text-[11px] font-medium text-[var(--text-secondary)] mb-1">
                          {t("models.agentDescPlaceholder")}
                        </label>
                        <input
                          type="text"
                          value={editDesc()}
                          onInput={(e) => setEditDesc(e.currentTarget.value)}
                          class="w-full rounded-xl border border-[var(--border-strong)] bg-[var(--bg-panel)] px-3 py-1.5 text-[12px] text-[var(--text-primary)] outline-none focus:border-[var(--accent-primary)]"
                        />
                      </div>
                      <div>
                        <label class="block text-[11px] font-medium text-[var(--text-secondary)] mb-1">
                          {t("models.agentPromptLabel")}
                        </label>
                        <textarea
                          rows={4}
                          value={editPrompt()}
                          onInput={(e) => setEditPrompt(e.currentTarget.value)}
                          class="w-full rounded-xl border border-[var(--border-strong)] bg-[var(--bg-panel)] px-3 py-2 font-mono text-[11px] text-[var(--text-primary)] outline-none focus:border-[var(--accent-primary)]"
                        />
                      </div>
                      <div class="flex items-center justify-end gap-2">
                        <button
                          type="button"
                          onClick={() => setEditingAgentId(null)}
                          class="rounded-xl px-3 py-1 text-[12px] font-medium text-[var(--text-secondary)] hover:bg-[var(--bg-hover)] cursor-pointer"
                        >
                          {t("models.cancelEdit")}
                        </button>
                        <button
                          type="button"
                          onClick={() => void saveEditAgent(agent.id)}
                          class="inline-flex items-center gap-1 rounded-xl bg-[var(--accent-primary)] px-3.5 py-1 text-[12px] font-medium text-[var(--accent-foreground)] hover:opacity-90 cursor-pointer"
                        >
                          <Check size={12} />
                          {t("models.saveAgentChanges")}
                        </button>
                      </div>
                    </div>
                  </Show>

                  {/* Prompt Preview / Expansion Toggle */}
                  <Show when={!isEditing() && agent.prompt}>
                    <div class="mt-2 pt-2 border-t border-[var(--border-subtle)]">
                      <button
                        type="button"
                        onClick={() => togglePromptExpanded(agent.id)}
                        class="inline-flex items-center gap-1.5 text-[11px] font-medium text-[var(--text-muted)] hover:text-[var(--text-primary)] cursor-pointer select-none"
                      >
                        <span>{t("models.agentPromptLabel")}</span>
                        <Show when={isPromptOpen()} fallback={<ChevronDown size={11} />}>
                          <ChevronUp size={11} />
                        </Show>
                      </button>

                      <Show when={isPromptOpen()}>
                        <pre class="mt-1.5 max-h-48 overflow-y-auto rounded-xl bg-[var(--bg-inset)] p-2.5 font-mono text-[11px] text-[var(--text-secondary)] whitespace-pre-wrap">
                          {agent.prompt}
                        </pre>
                      </Show>
                    </div>
                  </Show>
                </div>
              );
            }}
          </For>
        </div>

        {/* Add Custom Agent Section */}
        <div class="rounded-2xl border border-[var(--border-subtle)] bg-[var(--bg-elevated)]/40 p-5 space-y-4">
          <div>
            <h4 class="text-[13px] font-semibold text-[var(--text-primary)]">
              {t("models.customAgentsTitle")}
            </h4>
            <p class="text-[12px] text-[var(--text-muted)]">
              {t("models.customAgentsSubtitle")}
            </p>
          </div>

          <form onSubmit={(e) => void onAddCustomAgent(e)} class="space-y-3">
            <div class="grid grid-cols-1 sm:grid-cols-2 gap-3">
              <div>
                <label class="block text-[11px] font-medium text-[var(--text-secondary)] mb-1">
                  {t("models.agentIdPlaceholder")}
                </label>
                <input
                  type="text"
                  required
                  placeholder="e.g. reviewer"
                  value={newAgentId()}
                  onInput={(e) => setNewAgentId(e.currentTarget.value)}
                  class="w-full rounded-xl border border-[var(--border-strong)] bg-[var(--bg-panel)] px-3 py-1.5 text-[12px] text-[var(--text-primary)] outline-none focus:border-[var(--accent-primary)]"
                />
              </div>

              <div>
                <label class="block text-[11px] font-medium text-[var(--text-secondary)] mb-1">
                  {t("models.agentDedicatedModelLabel")}
                </label>
                <select
                  value={newAgentModel()}
                  onChange={(e) => setNewAgentModel(e.currentTarget.value)}
                  class="w-full rounded-xl border border-[var(--border-strong)] bg-[var(--bg-panel)] px-3 py-1.5 text-[12px] font-medium text-[var(--text-primary)] outline-none focus:border-[var(--accent-primary)]"
                >
                  <option value="">{t("models.agentModelFollowDefault")}</option>
                  <For each={connectedGroups()}>
                    {(grp) => (
                      <optgroup label={`${grp.providerName} (${grp.providerID})`}>
                        <For each={grp.models}>
                          {(m) => (
                            <option value={m.fullKey}>
                              {m.name} ({m.id}){m.free ? ` · [${t("models.freeModel")}]` : ""}
                            </option>
                          )}
                        </For>
                      </optgroup>
                    )}
                  </For>
                </select>
              </div>
            </div>

            <div>
              <label class="block text-[11px] font-medium text-[var(--text-secondary)] mb-1">
                {t("models.agentDescPlaceholder")}
              </label>
              <input
                type="text"
                placeholder="e.g. Code reviewer and security auditor"
                value={newAgentDesc()}
                onInput={(e) => setNewAgentDesc(e.currentTarget.value)}
                class="w-full rounded-xl border border-[var(--border-strong)] bg-[var(--bg-panel)] px-3 py-1.5 text-[12px] text-[var(--text-primary)] outline-none focus:border-[var(--accent-primary)]"
              />
            </div>

            <div>
              <label class="block text-[11px] font-medium text-[var(--text-secondary)] mb-1">
                {t("models.agentPromptLabel")}
              </label>
              <textarea
                required
                rows={3}
                placeholder={t("models.agentPromptPlaceholder")}
                value={newAgentPrompt()}
                onInput={(e) => setNewAgentPrompt(e.currentTarget.value)}
                class="w-full rounded-xl border border-[var(--border-strong)] bg-[var(--bg-panel)] px-3 py-2 font-mono text-[11px] text-[var(--text-primary)] outline-none focus:border-[var(--accent-primary)]"
              />
            </div>

            <div class="flex items-center justify-between pt-1">
              <Show when={formFeedback()}>
                {(fb) => (
                  <span
                    class={`text-[12px] font-medium ${
                      fb().ok ? "text-[var(--ok)]" : "text-rose-400"
                    }`}
                  >
                    {fb().text}
                  </span>
                )}
              </Show>
              <Show when={!formFeedback()}>
                <span />
              </Show>

              <button
                type="submit"
                disabled={creating() || !newAgentId().trim() || !newAgentPrompt().trim()}
                class="inline-flex items-center gap-1.5 rounded-xl bg-[var(--accent-primary)] px-4 py-1.5 text-[12px] font-medium text-[var(--accent-foreground)] hover:opacity-90 disabled:opacity-50 transition-opacity cursor-pointer"
              >
                <Show when={creating()} fallback={<Plus size={13} />}>
                  <LoaderCircle size={13} class="animate-spin" />
                </Show>
                {t("models.addAgentMode")}
              </button>
            </div>
          </form>
        </div>
      </div>
    </div>
  );
}
