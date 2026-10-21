// web/src/components/models/SubagentSettingsTab.tsx
import { createMemo, createSignal, For, onMount, Show } from "solid-js";
import {
  Bot,
  Check,
  ChevronDown,
  ChevronUp,
  LoaderCircle,
  Pencil,
  Plus,
  ShieldAlert,
  Trash2,
  Users,
} from "lucide-solid";
import {
  BUILTIN_SUBAGENT_IDS,
  createSubagent,
  deleteSubagent,
  effectiveAllowedSubagents,
  fetchGlobalConfig,
  legacySubagentModel,
  loadAgents,
  migrateLegacySubagentModel,
  refreshSubagentCatalog,
  readDelegationPolicy,
  readGlobalTaskPolicy,
  readSubagents,
  setDelegationPolicy,
  setGlobalTaskPolicy,
  updateSubagent,
  workspace,
  type DelegationPolicy,
  type SubagentEntry,
  type TaskPermissionMode,
} from "../../store/workspace";
import { t } from "../../i18n";
import { getConnectedProviderModelGroups } from "./model-selector-utils";
import ModelRolePicker from "./ModelRolePicker";

type SaveState = { id: string; ok: boolean } | null;

const GLOBAL_POLICIES: Array<TaskPermissionMode | "inherit"> = ["inherit", "allow", "ask", "deny"];

function delegationSelectValue(policy: DelegationPolicy): string {
  if (policy.kind === "all") return policy.action;
  return policy.kind;
}

function globalPolicyLabel(policy: TaskPermissionMode | "inherit"): string {
  if (policy === "inherit") return t("models.subagentsPolicyInherit");
  if (policy === "allow") return t("models.subagentsPolicyAllow");
  if (policy === "ask") return t("models.subagentsPolicyAsk");
  return t("models.subagentsPolicyDeny");
}

export default function SubagentSettingsTab() {
  const [savingId, setSavingId] = createSignal<string | null>(null);
  const [feedback, setFeedback] = createSignal<SaveState>(null);
  const [expandedPrompts, setExpandedPrompts] = createSignal<Record<string, boolean>>({});
  const [legacyDismissed, setLegacyDismissed] = createSignal(false);
  const [editingId, setEditingId] = createSignal<string | null>(null);
  const [editDesc, setEditDesc] = createSignal("");
  const [editPrompt, setEditPrompt] = createSignal("");

  const [newId, setNewId] = createSignal("");
  const [newDesc, setNewDesc] = createSignal("");
  const [newPrompt, setNewPrompt] = createSignal("");
  const [newModel, setNewModel] = createSignal("");
  const [newReadOnly, setNewReadOnly] = createSignal(false);
  const [creating, setCreating] = createSignal(false);

  const connectedGroups = createMemo(() => getConnectedProviderModelGroups());
  const subagents = createMemo(() => readSubagents(workspace.globalConfig));
  const subagentIDs = createMemo(() => subagents().map((entry) => entry.id));
  const primaryAgents = createMemo(() => workspace.agents.filter((agent) => agent.name));
  const globalPolicy = createMemo(() => readGlobalTaskPolicy(workspace.globalConfig));
  const legacyModel = createMemo(() => legacySubagentModel(workspace.globalConfig));

  onMount(async () => {
    try {
      await fetchGlobalConfig();
      await loadAgents(workspace.rootDir || undefined);
      await refreshSubagentCatalog();
    } catch {
      /* keep whatever the store already holds */
    }
  });

  const markSaved = (id: string, ok: boolean) => {
    setFeedback({ id, ok });
    setTimeout(() => {
      if (feedback()?.id === id) setFeedback(null);
    }, 2000);
  };

  const saveEntry = async (id: string, patch: Parameters<typeof updateSubagent>[1]) => {
    setSavingId(id);
    try {
      await updateSubagent(id, patch);
      markSaved(id, true);
    } catch {
      markSaved(id, false);
    } finally {
      setSavingId(null);
    }
  };

  const onGlobalPolicy = async (policy: TaskPermissionMode | "inherit") => {
    setSavingId("global");
    try {
      await setGlobalTaskPolicy(policy);
      markSaved("global", true);
    } catch {
      markSaved("global", false);
    } finally {
      setSavingId(null);
    }
  };

  const onDelegation = async (primaryID: string, policy: DelegationPolicy) => {
    setSavingId(primaryID);
    try {
      await setDelegationPolicy(primaryID, policy);
      markSaved(primaryID, true);
    } catch {
      markSaved(primaryID, false);
    } finally {
      setSavingId(null);
    }
  };

  const onToggleAllowlist = async (primaryID: string, subagentID: string) => {
    const policy = readDelegationPolicy(primaryID, workspace.globalConfig);
    const allow = policy.kind === "allowlist" ? [...policy.allow] : [];
    const next = allow.includes(subagentID)
      ? allow.filter((id) => id !== subagentID)
      : [...allow, subagentID];
    await onDelegation(primaryID, { kind: "allowlist", allow: next });
  };

  const onMigrateLegacy = async () => {
    setSavingId("legacy");
    try {
      await migrateLegacySubagentModel();
      markSaved("legacy", true);
    } catch {
      markSaved("legacy", false);
    } finally {
      setSavingId(null);
    }
  };

  const onAdd = async (event: Event) => {
    event.preventDefault();
    const id = newId()
      .trim()
      .toLowerCase()
      .replace(/[^a-z0-9_-]/g, "-");
    const prompt = newPrompt().trim();
    if (!id || !prompt || creating()) return;
    if ((BUILTIN_SUBAGENT_IDS as readonly string[]).includes(id) || subagentIDs().includes(id)) return;
    setCreating(true);
    try {
      await createSubagent({
        id,
        description: newDesc().trim() || id,
        prompt,
        model: newModel() || undefined,
        readOnly: newReadOnly(),
      });
      setNewId("");
      setNewDesc("");
      setNewPrompt("");
      setNewModel("");
      setNewReadOnly(false);
      markSaved("new", true);
    } catch {
      markSaved("new", false);
    } finally {
      setCreating(false);
    }
  };

  const onDelete = async (entry: SubagentEntry) => {
    if (entry.builtin) return;
    setSavingId(entry.id);
    try {
      await deleteSubagent(entry.id);
      markSaved(entry.id, true);
    } catch {
      markSaved(entry.id, false);
    } finally {
      setSavingId(null);
    }
  };

  const togglePrompt = (id: string) =>
    setExpandedPrompts((prev) => ({ ...prev, [id]: !prev[id] }));

  const startEdit = (entry: SubagentEntry) => {
    setEditingId(entry.id);
    setEditDesc(entry.description);
    setEditPrompt(entry.prompt);
  };

  const saveEdit = async (id: string) => {
    const prompt = editPrompt().trim();
    if (!prompt) return;
    await saveEntry(id, {
      description: editDesc().trim() || null,
      prompt,
    });
    setEditingId(null);
  };

  const saveIcon = (id: string) => (
    <span class="w-4 flex items-center justify-center">
      <Show when={savingId() === id}>
        <LoaderCircle size={13} class="animate-spin text-[var(--accent-primary)]" />
      </Show>
      <Show when={savingId() !== id && feedback()?.id === id}>
        <Show
          when={feedback()?.ok}
          fallback={<ShieldAlert size={13} class="text-rose-400" />}
        >
          <Check size={13} class="text-[var(--ok)]" />
        </Show>
      </Show>
    </span>
  );

  return (
    <div class="flex-1 overflow-y-auto p-6 sm:p-8">
      <div class="mx-auto w-full max-w-4xl space-y-6">
        <div class="flex items-center gap-3 rounded-2xl border border-[var(--border-subtle)] bg-[var(--bg-elevated)]/50 px-4 py-3">
          <div class="flex h-8 w-8 shrink-0 items-center justify-center rounded-xl bg-[var(--accent-surface)] text-[var(--accent-primary)]">
            <Users size={16} />
          </div>
          <div>
            <h3 class="text-[13px] font-semibold text-[var(--text-primary)]">
              {t("models.subagentsTitle")}
            </h3>
            <p class="text-[12px] text-[var(--text-secondary)]">
              {t("models.subagentsSubtitle")}
            </p>
          </div>
        </div>

        <Show when={legacyModel() && !legacyDismissed()}>
          <div class="rounded-2xl border border-[var(--warning-border,var(--border-strong))] bg-[var(--bg-inset)] px-4 py-3">
            <div class="flex items-start justify-between gap-3">
              <div class="min-w-0 space-y-1">
                <div class="text-[13px] font-semibold text-[var(--text-primary)]">
                  {t("models.subagentsLegacyTitle")}
                </div>
                <p class="text-[12px] text-[var(--text-muted)]">
                  {t("models.subagentsLegacyDesc")}
                </p>
                <p class="font-mono text-[11px] text-[var(--text-faint)]">{legacyModel()}</p>
              </div>
              <div class="flex shrink-0 items-center gap-1.5">
                <button
                  type="button"
                  onClick={() => void onMigrateLegacy()}
                  disabled={savingId() === "legacy"}
                  class="rounded-xl bg-[var(--accent-primary)] px-3 py-1.5 text-[12px] font-medium text-[var(--accent-foreground)] hover:opacity-90 disabled:opacity-50 cursor-pointer"
                >
                  {t("models.subagentsLegacyMigrate")}
                </button>
                <button
                  type="button"
                  onClick={() => setLegacyDismissed(true)}
                  class="rounded-xl px-2.5 py-1.5 text-[12px] text-[var(--text-secondary)] hover:bg-[var(--bg-hover)] cursor-pointer"
                >
                  {t("models.subagentsLegacyDismiss")}
                </button>
              </div>
            </div>
          </div>
        </Show>

        <div class="space-y-3">
          <div class="flex items-center justify-between gap-3">
            <div>
              <h4 class="text-[13px] font-semibold text-[var(--text-primary)]">
                {t("models.subagentsDelegationTitle")}
              </h4>
              <p class="text-[12px] text-[var(--text-muted)]">
                {t("models.subagentsDelegationDesc")}
              </p>
            </div>
            {saveIcon("global")}
          </div>

          <div class="rounded-2xl border border-[var(--border-subtle)] bg-[var(--bg-elevated)]/40 px-4 py-3">
            <div class="flex items-center justify-between gap-3">
              <span class="text-[12px] font-medium text-[var(--text-primary)]">
                {t("models.subagentsGlobalPolicy")}
              </span>
              <div class="inline-flex rounded-lg border border-[var(--border-subtle)] bg-[var(--bg-panel)] p-0.5">
                <For each={GLOBAL_POLICIES}>
                  {(policy) => (
                    <button
                      type="button"
                      onClick={() => void onGlobalPolicy(policy)}
                      class={`rounded-md px-2.5 py-1 text-[11px] font-medium cursor-pointer ${
                        globalPolicy() === policy
                          ? "bg-[var(--accent-surface)] text-[var(--accent-primary)]"
                          : "text-[var(--text-muted)] hover:text-[var(--text-primary)]"
                      }`}
                    >
                      {globalPolicyLabel(policy)}
                    </button>
                  )}
                </For>
              </div>
            </div>
          </div>

          <div class="space-y-2">
            <div>
              <h5 class="text-[12px] font-semibold text-[var(--text-primary)]">
                {t("models.subagentsPerAgentTitle")}
              </h5>
              <p class="text-[11px] text-[var(--text-muted)]">
                {t("models.subagentsPerAgentDesc")}
              </p>
            </div>

            <For each={primaryAgents()}>
              {(agent) => {
                const policy = createMemo(() => readDelegationPolicy(agent.name, workspace.globalConfig));
                const allowed = createMemo(() =>
                  effectiveAllowedSubagents(agent.name, subagentIDs(), workspace.globalConfig)
                );
                return (
                  <div class="rounded-2xl border border-[var(--border-subtle)] bg-[var(--bg-elevated)]/40 px-4 py-3 space-y-2.5">
                    <div class="flex items-center justify-between gap-3">
                      <div class="flex items-center gap-2 min-w-0">
                        <Bot size={14} class="shrink-0 text-[var(--accent-primary)]" />
                        <span class="truncate text-[12px] font-semibold text-[var(--text-primary)]">
                          {agent.name}
                        </span>
                        <Show when={allowed()}>
                          <span class="shrink-0 rounded-full bg-[var(--bg-inset)] px-1.5 py-0.5 text-[10px] text-[var(--text-muted)]">
                            {t("models.subagentsAllowlist")}
                          </span>
                        </Show>
                      </div>
                      <div class="flex shrink-0 items-center gap-1.5">
                        <select
                          value={delegationSelectValue(policy())}
                          onChange={(event) => {
                            const kind = event.currentTarget.value;
                            if (kind === "inherit") void onDelegation(agent.name, { kind: "inherit" });
                            else if (kind === "allowlist") void onDelegation(agent.name, { kind: "allowlist", allow: [] });
                            else void onDelegation(agent.name, { kind: "all", action: kind as TaskPermissionMode });
                          }}
                          class="rounded-lg border border-[var(--border-strong)] bg-[var(--bg-panel)] px-2 py-1 text-[11px] text-[var(--text-primary)] outline-none cursor-pointer"
                        >
                          <option value="inherit">{t("models.subagentsFollowGlobal")}</option>
                          <option value="allow">{t("models.subagentsAllowAll")}</option>
                          <option value="ask">{t("models.subagentsPolicyAsk")}</option>
                          <option value="allowlist">{t("models.subagentsAllowlist")}</option>
                          <option value="deny">{t("models.subagentsPolicyDeny")}</option>
                        </select>
                        {saveIcon(agent.name)}
                      </div>
                    </div>

                    <Show when={policy().kind === "allowlist"}>
                      <div class="space-y-1.5 border-t border-[var(--border-subtle)] pt-2">
                        <div class="text-[11px] text-[var(--text-muted)]">
                          {t("models.subagentsAllowlistHint")}
                        </div>
                        <div class="flex flex-wrap gap-1.5">
                          <For each={subagents()}>
                            {(entry) => {
                              const checked = () =>
                                policy().kind === "allowlist" &&
                                (policy() as { allow: string[] }).allow.includes(entry.id);
                              return (
                                <label class="inline-flex items-center gap-1.5 rounded-lg border border-[var(--border-subtle)] bg-[var(--bg-panel)] px-2 py-1 text-[11px] text-[var(--text-secondary)] cursor-pointer select-none">
                                  <input
                                    type="checkbox"
                                    checked={checked()}
                                    onChange={() => void onToggleAllowlist(agent.name, entry.id)}
                                    class="rounded accent-[var(--accent-primary)] cursor-pointer"
                                  />
                                  <span class="font-mono">{entry.id}</span>
                                </label>
                              );
                            }}
                          </For>
                        </div>
                        <Show when={(policy() as { allow?: string[] }).allow?.length === 0}>
                          <div class="text-[11px] text-rose-400">
                            {t("models.subagentsNoAllowlist")}
                          </div>
                        </Show>
                      </div>
                    </Show>
                  </div>
                );
              }}
            </For>
          </div>
        </div>

        <div class="border-t border-[var(--border-subtle)] pt-5 space-y-3">
          <div>
            <h4 class="text-[13px] font-semibold text-[var(--text-primary)]">
              {t("models.subagentsRosterTitle")}
            </h4>
            <p class="text-[12px] text-[var(--text-muted)]">
              {t("models.subagentsRosterDesc")}
            </p>
          </div>

          <Show
            when={subagents().length > 0}
            fallback={
              <div class="rounded-xl border border-[var(--border-subtle)] bg-[var(--bg-inset)] px-3.5 py-3 text-[12px] text-[var(--text-muted)]">
                {t("models.subagentsNoAgents")}
              </div>
            }
          >
            <For each={subagents()}>
              {(entry) => {
                const promptOpen = () => Boolean(expandedPrompts()[entry.id]);
                return (
                  <div class="rounded-2xl border border-[var(--border-subtle)] bg-[var(--bg-elevated)]/40 px-4 py-3 space-y-2.5">
                    <div class="flex flex-wrap items-center justify-between gap-2">
                      <div class="flex min-w-0 items-center gap-2">
                        <span class="truncate font-mono text-[12px] font-semibold text-[var(--text-primary)]">
                          {entry.id}
                        </span>
                        <span class="shrink-0 rounded-full bg-[var(--accent-surface)] px-1.5 py-0.5 text-[10px] font-medium text-[var(--accent-primary)]">
                          {entry.builtin
                            ? t("models.subagentsBuiltinBadge")
                            : t("models.subagentsCustomBadge")}
                        </span>
                        <Show when={entry.readOnly}>
                          <span class="shrink-0 rounded-full bg-[var(--bg-inset)] px-1.5 py-0.5 text-[10px] text-[var(--text-muted)]">
                            {t("models.subagentsReadOnlyLabel")}
                          </span>
                        </Show>
                        <Show when={entry.disabled}>
                          <span class="shrink-0 rounded-full bg-rose-500/10 px-1.5 py-0.5 text-[10px] text-rose-400">
                            {t("models.subagentsDisabledLabel")}
                          </span>
                        </Show>
                      </div>
                      <div class="flex shrink-0 items-center gap-1.5">
                        {saveIcon(entry.id)}
                        <Show when={!entry.builtin}>
                          <button
                            type="button"
                            onClick={() => startEdit(entry)}
                            class="rounded-lg p-1.5 text-[var(--text-faint)] hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)] cursor-pointer"
                            title={t("models.agentEditMode")}
                          >
                            <Pencil size={13} />
                          </button>
                          <button
                            type="button"
                            onClick={() => void onDelete(entry)}
                            class="rounded-lg p-1.5 text-[var(--text-faint)] hover:bg-rose-500/10 hover:text-rose-400 cursor-pointer"
                            title={t("models.subagentsDelete")}
                          >
                            <Trash2 size={13} />
                          </button>
                        </Show>
                      </div>
                    </div>

                    <Show when={entry.description}>
                      <p class="text-[12px] text-[var(--text-muted)]">{entry.description}</p>
                    </Show>

                    <div class="grid grid-cols-1 gap-3 sm:grid-cols-2">
                      <div class="space-y-1">
                        <span class="text-[11px] font-medium uppercase tracking-wider text-[var(--text-faint)]">
                          {t("models.subagentsModelLabel")}
                        </span>
                        <ModelRolePicker
                          value={entry.model}
                          groups={connectedGroups()}
                          allowEmpty
                          emptyLabel={t("models.subagentsFollowPrimary")}
                          triggerClass="w-full"
                          onChange={(value) => void saveEntry(entry.id, { model: value || null })}
                        />
                      </div>

                      <div class="space-y-1">
                        <span class="text-[11px] font-medium uppercase tracking-wider text-[var(--text-faint)]">
                          {t("models.subagentsStepsLabel")}
                        </span>
                        <div class="flex items-center gap-2">
                          <input
                            type="number"
                            min={0}
                            max={200}
                            value={entry.steps || ""}
                            placeholder={t("models.subagentsStepsUnlimited")}
                            onChange={(event) => {
                              const raw = Number(event.currentTarget.value);
                              const steps = Number.isFinite(raw) && raw > 0 ? Math.min(200, Math.floor(raw)) : 0;
                              void saveEntry(entry.id, { steps: steps || null });
                            }}
                            class="w-28 rounded-xl border border-[var(--border-strong)] bg-[var(--bg-panel)] px-2.5 py-1.5 text-[12px] text-[var(--text-primary)] outline-none focus:border-[var(--accent-primary)]"
                          />
                          <span class="text-[11px] text-[var(--text-muted)]">
                            {t("models.subagentsStepsHint")}
                          </span>
                        </div>
                      </div>
                    </div>

                    <div class="flex flex-wrap items-center gap-3 border-t border-[var(--border-subtle)] pt-2">
                      <label class="inline-flex items-center gap-1.5 text-[11px] text-[var(--text-secondary)] cursor-pointer select-none">
                        <input
                          type="checkbox"
                          checked={entry.readOnly}
                          onChange={(event) =>
                            void saveEntry(entry.id, { readOnly: event.currentTarget.checked })
                          }
                          class="rounded accent-[var(--accent-primary)] cursor-pointer"
                        />
                        <span>{t("models.subagentsReadOnlyLabel")}</span>
                        <span class="text-[var(--text-faint)]">· {t("models.subagentsReadOnlyHint")}</span>
                      </label>

                      <label class="inline-flex items-center gap-1.5 text-[11px] text-[var(--text-secondary)] cursor-pointer select-none">
                        <input
                          type="checkbox"
                          checked={entry.hidden}
                          onChange={(event) =>
                            void saveEntry(entry.id, { hidden: event.currentTarget.checked })
                          }
                          class="rounded accent-[var(--accent-primary)] cursor-pointer"
                        />
                        <span>{t("models.subagentsHiddenLabel")}</span>
                      </label>

                      <label class="inline-flex items-center gap-1.5 text-[11px] text-[var(--text-secondary)] cursor-pointer select-none">
                        <input
                          type="checkbox"
                          checked={entry.disabled}
                          onChange={(event) =>
                            void saveEntry(entry.id, { disable: event.currentTarget.checked })
                          }
                          class="rounded accent-[var(--accent-primary)] cursor-pointer"
                        />
                        <span>{t("models.subagentsDisabledLabel")}</span>
                      </label>

                      <Show when={entry.prompt && editingId() !== entry.id}>
                        <button
                          type="button"
                          onClick={() => togglePrompt(entry.id)}
                          class="ml-auto inline-flex items-center gap-1 text-[11px] font-medium text-[var(--text-muted)] hover:text-[var(--text-primary)] cursor-pointer select-none"
                        >
                          <span>
                            {promptOpen()
                              ? t("models.subagentsHidePrompt")
                              : t("models.subagentsShowPrompt")}
                          </span>
                          <Show when={promptOpen()} fallback={<ChevronDown size={11} />}>
                            <ChevronUp size={11} />
                          </Show>
                        </button>
                      </Show>
                    </div>

                    <Show when={editingId() === entry.id}>
                      <div class="space-y-2 border-t border-[var(--border-subtle)] pt-2">
                        <input
                          type="text"
                          value={editDesc()}
                          placeholder={t("models.subagentsDescLabel")}
                          onInput={(event) => setEditDesc(event.currentTarget.value)}
                          class="w-full rounded-xl border border-[var(--border-strong)] bg-[var(--bg-panel)] px-3 py-1.5 text-[12px] text-[var(--text-primary)] outline-none focus:border-[var(--accent-primary)]"
                        />
                        <textarea
                          rows={5}
                          value={editPrompt()}
                          onInput={(event) => setEditPrompt(event.currentTarget.value)}
                          class="w-full rounded-xl border border-[var(--border-strong)] bg-[var(--bg-panel)] px-3 py-2 font-mono text-[11px] text-[var(--text-primary)] outline-none focus:border-[var(--accent-primary)]"
                        />
                        <div class="flex items-center justify-end gap-2">
                          <button
                            type="button"
                            onClick={() => setEditingId(null)}
                            class="rounded-xl px-3 py-1 text-[12px] font-medium text-[var(--text-secondary)] hover:bg-[var(--bg-hover)] cursor-pointer"
                          >
                            {t("models.cancelEdit")}
                          </button>
                          <button
                            type="button"
                            onClick={() => void saveEdit(entry.id)}
                            class="inline-flex items-center gap-1 rounded-xl bg-[var(--accent-primary)] px-3.5 py-1 text-[12px] font-medium text-[var(--accent-foreground)] hover:opacity-90 cursor-pointer"
                          >
                            <Check size={12} />
                            {t("models.saveAgentChanges")}
                          </button>
                        </div>
                      </div>
                    </Show>

                    <Show when={promptOpen() && entry.prompt && editingId() !== entry.id}>
                      <pre class="max-h-48 overflow-y-auto rounded-xl bg-[var(--bg-inset)] p-2.5 font-mono text-[11px] whitespace-pre-wrap text-[var(--text-secondary)]">
                        {entry.prompt}
                      </pre>
                    </Show>
                  </div>
                );
              }}
            </For>
          </Show>
        </div>

        <div class="rounded-2xl border border-[var(--border-subtle)] bg-[var(--bg-elevated)]/40 p-5 space-y-4">
          <div>
            <h4 class="text-[13px] font-semibold text-[var(--text-primary)]">
              {t("models.subagentsAddTitle")}
            </h4>
            <p class="text-[12px] text-[var(--text-muted)]">
              {t("models.subagentsAddSubtitle")}
            </p>
          </div>

          <form onSubmit={(event) => void onAdd(event)} class="space-y-3">
            <div class="grid grid-cols-1 gap-3 sm:grid-cols-2">
              <div>
                <label class="mb-1 block text-[11px] font-medium text-[var(--text-secondary)]">
                  {t("models.agentIdPlaceholder")}
                </label>
                <input
                  type="text"
                  required
                  placeholder="e.g. security-review"
                  value={newId()}
                  onInput={(event) => setNewId(event.currentTarget.value)}
                  class="w-full rounded-xl border border-[var(--border-strong)] bg-[var(--bg-panel)] px-3 py-1.5 text-[12px] text-[var(--text-primary)] outline-none focus:border-[var(--accent-primary)]"
                />
              </div>
              <div>
                <label class="mb-1 block text-[11px] font-medium text-[var(--text-secondary)]">
                  {t("models.subagentsModelLabel")}
                </label>
                <ModelRolePicker
                  value={newModel()}
                  groups={connectedGroups()}
                  allowEmpty
                  emptyLabel={t("models.subagentsFollowPrimary")}
                  triggerClass="w-full"
                  onChange={(value) => setNewModel(value)}
                />
              </div>
            </div>

            <div>
              <label class="mb-1 block text-[11px] font-medium text-[var(--text-secondary)]">
                {t("models.subagentsDescLabel")}
              </label>
              <input
                type="text"
                placeholder={t("models.subagentsDescPlaceholder")}
                value={newDesc()}
                onInput={(event) => setNewDesc(event.currentTarget.value)}
                class="w-full rounded-xl border border-[var(--border-strong)] bg-[var(--bg-panel)] px-3 py-1.5 text-[12px] text-[var(--text-primary)] outline-none focus:border-[var(--accent-primary)]"
              />
            </div>

            <div>
              <label class="mb-1 block text-[11px] font-medium text-[var(--text-secondary)]">
                {t("models.subagentsPromptLabel")}
              </label>
              <textarea
                required
                rows={3}
                placeholder={t("models.subagentsPromptPlaceholder")}
                value={newPrompt()}
                onInput={(event) => setNewPrompt(event.currentTarget.value)}
                class="w-full rounded-xl border border-[var(--border-strong)] bg-[var(--bg-panel)] px-3 py-2 font-mono text-[11px] text-[var(--text-primary)] outline-none focus:border-[var(--accent-primary)]"
              />
            </div>

            <div class="flex items-center justify-between pt-1">
              <label class="inline-flex items-center gap-1.5 text-[11px] text-[var(--text-secondary)] cursor-pointer select-none">
                <input
                  type="checkbox"
                  checked={newReadOnly()}
                  onChange={(event) => setNewReadOnly(event.currentTarget.checked)}
                  class="rounded accent-[var(--accent-primary)] cursor-pointer"
                />
                <span>{t("models.subagentsReadOnlyLabel")}</span>
              </label>

              <button
                type="submit"
                disabled={creating() || !newId().trim() || !newPrompt().trim()}
                class="inline-flex items-center gap-1.5 rounded-xl bg-[var(--accent-primary)] px-4 py-1.5 text-[12px] font-medium text-[var(--accent-foreground)] hover:opacity-90 disabled:opacity-50 transition-opacity cursor-pointer"
              >
                <Show when={creating()} fallback={<Plus size={13} />}>
                  <LoaderCircle size={13} class="animate-spin" />
                </Show>
                {t("models.subagentsAddButton")}
              </button>
            </div>
          </form>
        </div>

        <div class="space-y-1.5 rounded-2xl border border-[var(--border-subtle)] bg-[var(--bg-inset)] px-4 py-3 text-[11px] text-[var(--text-muted)]">
          <div>· {t("models.subagentsWorktreeNote")}</div>
          <div>· {t("models.subagentsIsolatedNote")}</div>
          <div>· {t("models.subagentsReadOnlyNote")}</div>
        </div>
      </div>
    </div>
  );
}
