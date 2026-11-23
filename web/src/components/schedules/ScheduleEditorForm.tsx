// web/src/components/schedules/ScheduleEditorForm.tsx
import { createEffect, createMemo, createSignal, For, onCleanup, Show, untrack } from "solid-js";
import { Sparkles, Trash2 } from "lucide-solid";
import { t } from "../../i18n";
import {
  createSchedule,
  deleteSchedule,
  formatScheduleError,
  listSessionsForScheduleDir,
  loadSkillsCatalog,
  normalizeFolderPath,
  previewScheduleTrigger,
  skillsStore,
  updateSchedule,
  workspace,
  type ScheduleItem,
  type ScheduleMisfirePolicy,
  type ScheduleNotifyPolicy,
  type SchedulePreviewResponse,
  type ScheduleTriggerKind,
  type ScheduleTriggerSpec,
} from "../../store/workspace";
import type { SessionInfo } from "../../services/sse-types";
import { formatShortTimestamp, SCHEDULE_TEMPLATES, type ScheduleTemplate } from "./schedule-helpers";

interface ModelOption {
  value: string;
  providerID: string;
  modelID: string;
  label: string;
}

export default function ScheduleEditorForm(props: {
  schedule: ScheduleItem | null;
  defaultDirectory: string;
  defaultProjectID?: string;
  defaultSessionID?: string;
  onSaved: (item: ScheduleItem) => void;
  onDeleted: (id: string) => void;
}) {
  const [name, setName] = createSignal("");
  const [enabled, setEnabled] = createSignal(true);
  const [directory, setDirectory] = createSignal("");
  const [projectID, setProjectID] = createSignal("");
  const [bindingMode, setBindingMode] = createSignal<"fresh" | "bound">("fresh");
  const [sessionID, setSessionID] = createSignal("");
  const [mode, setMode] = createSignal<"build" | "plan">("build");
  const [sandboxMode, setSandboxMode] = createSignal<"local" | "worktree">("local");
  const [commandSandbox, setCommandSandbox] = createSignal(false);
  const [triggerKind, setTriggerKind] = createSignal<ScheduleTriggerKind>("cron");
  const [cronExpr, setCronExpr] = createSignal("0 9 * * 1-5");
  const [intervalMinutes, setIntervalMinutes] = createSignal(30);
  const [misfire, setMisfire] = createSignal<ScheduleMisfirePolicy>("skip");
  const [modelValue, setModelValue] = createSignal("");
  const [skillKey, setSkillKey] = createSignal("");
  const [prompt, setPrompt] = createSignal("");
  const [notifyOn, setNotifyOn] = createSignal<ScheduleNotifyPolicy>("alert_or_error");
  const [injectLastSummary, setInjectLastSummary] = createSignal(true);
  const [preview, setPreview] = createSignal<SchedulePreviewResponse | null>(null);
  const [saving, setSaving] = createSignal(false);
  const [error, setError] = createSignal<string | null>(null);

  const modelOptions = createMemo<ModelOption[]>(() => {
    const out: ModelOption[] = [];
    const seen = new Set<string>();
    const connectedSet = new Set<string>([
      ...(workspace.connectedProviders || []),
      ...Object.keys(workspace.hoxiProviders || {}),
    ]);
    const filterByConnected = connectedSet.size > 0;
    const savedProviderID = props.schedule?.execution.providerID?.trim() || "";
    const savedModelID = props.schedule?.execution.modelID?.trim() || "";
    for (const prov of workspace.providers) {
      if (!prov || prov.id === "hoxi-auto" || prov.id === "hoxi-orchestrator") continue;
      if (filterByConnected && !connectedSet.has(prov.id) && prov.id !== savedProviderID) continue;
      for (const [mid, info] of Object.entries(prov.models || {})) {
        const val = `${prov.id}/${mid}`;
        if (seen.has(val)) continue;
        seen.add(val);
        out.push({
          value: val,
          providerID: prov.id,
          modelID: mid,
          label: `${prov.name || prov.id} / ${info?.name || mid}`,
        });
      }
    }
    if (savedProviderID && savedModelID) {
      const savedVal = `${savedProviderID}/${savedModelID}`;
      if (!seen.has(savedVal)) {
        out.unshift({
          value: savedVal,
          providerID: savedProviderID,
          modelID: savedModelID,
          label: `${savedProviderID} / ${savedModelID}`,
        });
      }
    }
    return out;
  });

  const workspaceDirOptions = createMemo(() => {
    const seen = new Set<string>();
    const list: Array<{ dir: string; projectID: string; label: string }> = [];
    const add = (dir: string, pid: string, label: string) => {
      const clean = dir.trim();
      if (!clean) return;
      const norm = normalizeFolderPath(clean);
      if (seen.has(norm)) return;
      seen.add(norm);
      list.push({ dir: clean, projectID: pid, label });
    };
    for (const ws of workspace.savedWorkspaces) {
      if (ws.hidden) continue;
      for (const f of ws.folders || []) {
        add(f.path, ws.id, `${ws.name} (${f.path})`);
      }
    }
    if (workspace.rootDir) {
      add(workspace.rootDir, workspace.activeProjectID || "", workspace.rootDir);
    }
    if (props.defaultDirectory) {
      add(props.defaultDirectory, props.defaultProjectID || "", props.defaultDirectory);
    }
    if (props.schedule?.directory) {
      add(props.schedule.directory, props.schedule.projectID || "", props.schedule.directory);
    }
    return list;
  });

  const [remoteDirSessions, setRemoteDirSessions] = createSignal<SessionInfo[]>([]);

  const boundSessionOptions = createMemo(() => {
    const targetDir = normalizeFolderPath(directory());
    const useRemote =
      Boolean(targetDir) && targetDir !== normalizeFolderPath(workspace.rootDir);
    const source = useRemote ? remoteDirSessions() : workspace.sessions;
    const list = source.filter((s) => {
      if (s.parentID || s.time?.archived) return false;
      if (!targetDir) return true;
      return !s.directory || normalizeFolderPath(s.directory) === targetDir || (Boolean(projectID()) && s.projectID === projectID());
    });
    const currentBound = sessionID().trim();
    if (currentBound && !list.some((s) => s.id === currentBound)) {
      return [
        {
          id: currentBound,
          title: currentBound,
        },
        ...list,
      ];
    }
    return list;
  });

  // Load sessions for non-active workspace directories when selected in the form.
  let sessionsFetchSeq = 0;
  createEffect(() => {
    const dir = directory().trim();
    const pid = projectID().trim();
    const normDir = normalizeFolderPath(dir);
    if (!normDir || normDir === normalizeFolderPath(workspace.rootDir)) {
      setRemoteDirSessions([]);
      return;
    }
    const seq = ++sessionsFetchSeq;
    void listSessionsForScheduleDir(dir, pid)
      .then((items) => {
        if (seq === sessionsFetchSeq) setRemoteDirSessions(items);
      })
      .catch(() => {
        if (seq === sessionsFetchSeq) setRemoteDirSessions([]);
      });
  });

  // Sync form state ONLY when switching which schedule (or new-schedule target)
  // is being edited, so 10s background polling never clobbers unsaved edits.
  let syncedFormKey = "";
  createEffect(() => {
    const s = props.schedule;
    const nextKey = s
      ? `edit:${s.id}`
      : `new:${props.defaultDirectory || ""}:${props.defaultProjectID || ""}:${props.defaultSessionID || ""}`;
    if (nextKey === syncedFormKey) return;
    syncedFormKey = nextKey;
    setError(null);
    untrack(() => {
      if (s) {
        setName(s.name);
        setEnabled(s.enabled);
        setDirectory(s.directory);
        setProjectID(s.projectID || "");
        setBindingMode(s.sessionID ? "bound" : "fresh");
        setSessionID(s.sessionID || "");
        setMode(s.execution.mode === "plan" ? "plan" : "build");
        setSandboxMode(s.execution.sandboxMode === "worktree" ? "worktree" : "local");
        setCommandSandbox(Boolean(s.execution.commandSandbox));
        setTriggerKind(s.trigger.kind === "interval" ? "interval" : "cron");
        setCronExpr(s.trigger.cron || "0 9 * * 1-5");
        setIntervalMinutes(s.trigger.intervalMinutes || 30);
        setMisfire(s.trigger.misfire === "coalesce" ? "coalesce" : "skip");
        setModelValue(
          s.execution.providerID && s.execution.modelID
            ? `${s.execution.providerID}/${s.execution.modelID}`
            : ""
        );
        setSkillKey(s.skillSelection?.key || s.skillSelection?.name || "");
        setPrompt(s.prompt || "");
        setNotifyOn(s.notifyOn || "alert_or_error");
        setInjectLastSummary(s.injectLastSummary !== false);
      } else {
        setName("");
        setEnabled(true);
        setDirectory(props.defaultDirectory || workspace.rootDir || "");
        setProjectID(props.defaultProjectID || workspace.activeProjectID || "");
        setBindingMode(props.defaultSessionID ? "bound" : "fresh");
        setSessionID(props.defaultSessionID || "");
        setMode("build");
        setSandboxMode("local");
        setCommandSandbox(false);
        setTriggerKind("cron");
        setCronExpr("0 9 * * 1-5");
        setIntervalMinutes(30);
        setMisfire("skip");
        const sel = workspace.selectedModel;
        const defaultModel =
          sel && sel.providerID && sel.modelID && sel.providerID !== "hoxi-auto" && sel.providerID !== "hoxi-orchestrator"
            ? `${sel.providerID}/${sel.modelID}`
            : modelOptions()[0]?.value || "";
        setModelValue(defaultModel);
        setSkillKey("");
        setPrompt("");
        setNotifyOn("alert_or_error");
        setInjectLastSummary(true);
      }
    });
  });

  // Load workspace skills whenever target directory changes.
  createEffect(() => {
    const dir = directory().trim();
    if (dir) {
      void loadSkillsCatalog({ directory: dir, runtime: "native" }).catch(() => {});
    }
  });

  // Debounced live cron/interval preview.
  let previewSeq = 0;
  createEffect(() => {
    const trig: ScheduleTriggerSpec =
      triggerKind() === "interval"
        ? { kind: "interval", intervalMinutes: intervalMinutes(), misfire: misfire() }
        : { kind: "cron", cron: cronExpr().trim(), misfire: misfire() };
    const seq = ++previewSeq;
    const timer = setTimeout(() => {
      void previewScheduleTrigger(trig, 3)
        .then((res) => {
          if (seq === previewSeq) setPreview(res);
        })
        .catch(() => {
          if (seq === previewSeq) setPreview(null);
        });
    }, 220);
    onCleanup(() => clearTimeout(timer));
  });

  const applyTemplate = (tpl: ScheduleTemplate) => {
    setName(tpl.name());
    setPrompt(tpl.prompt());
    setMode(tpl.mode);
    setSandboxMode(tpl.sandboxMode);
    setTriggerKind(tpl.trigger.kind);
    if (tpl.trigger.cron) setCronExpr(tpl.trigger.cron);
    if (tpl.trigger.intervalMinutes) setIntervalMinutes(tpl.trigger.intervalMinutes);
  };

  const applyPreset = (kind: ScheduleTriggerKind, cron: string, interval: number) => {
    setTriggerKind(kind);
    if (kind === "cron") setCronExpr(cron);
    else setIntervalMinutes(interval);
  };

  const onSubmit = async (ev: Event) => {
    ev.preventDefault();
    if (saving()) return;
    setError(null);

    const isBound = bindingMode() === "bound";
    const boundSid = isBound ? sessionID().trim() : "";
    if (isBound && !boundSid) {
      setError(t("schedules.boundSessionRequiredError"));
      return;
    }
    const [provID, ...modelRest] = modelValue().split("/");
    const modID = modelRest.join("/");
    const matchedSkill = skillsStore.skills.find(
      (sk) => sk.key === skillKey() || sk.name === skillKey()
    );
    const skillSelection = skillKey().trim()
      ? { key: matchedSkill?.key || skillKey().trim(), name: matchedSkill?.name || skillKey().trim() }
      : undefined;

    const trigger: ScheduleTriggerSpec =
      triggerKind() === "interval"
        ? { kind: "interval", intervalMinutes: Number(intervalMinutes()) || 30, misfire: misfire() }
        : { kind: "cron", cron: cronExpr().trim(), misfire: misfire() };

    const execution = {
      mode: isBound ? ("" as const) : mode(),
      sandboxMode: sandboxMode(),
      commandSandbox: commandSandbox(),
      runtime: "native",
      providerID: provID?.trim() || "",
      modelID: modID?.trim() || "",
    };

    setSaving(true);
    try {
      if (props.schedule) {
        const updated = await updateSchedule(props.schedule.id, {
          name: name().trim(),
          enabled: enabled(),
          projectID: projectID().trim(),
          directory: directory().trim(),
          sessionID: boundSid,
          trigger,
          execution,
          skillSelection,
          clearSkill: !skillSelection,
          prompt: prompt().trim(),
          injectLastSummary: injectLastSummary(),
          notifyOn: notifyOn(),
        });
        props.onSaved(updated);
      } else {
        const created = await createSchedule({
          name: name().trim(),
          enabled: enabled(),
          projectID: projectID().trim(),
          directory: directory().trim(),
          sessionID: boundSid,
          trigger,
          execution,
          skillSelection,
          prompt: prompt().trim(),
          injectLastSummary: injectLastSummary(),
          notifyOn: notifyOn(),
        });
        props.onSaved(created);
      }
    } catch (err) {
      setError(formatScheduleError(err, "Failed to save schedule"));
    } finally {
      setSaving(false);
    }
  };

  const onDelete = async () => {
    if (!props.schedule || saving()) return;
    if (typeof window !== "undefined" && !window.confirm(t("schedules.deleteConfirm"))) return;
    setSaving(true);
    setError(null);
    try {
      await deleteSchedule(props.schedule.id);
      props.onDeleted(props.schedule.id);
    } catch (err) {
      setError(formatScheduleError(err, "Failed to delete schedule"));
    } finally {
      setSaving(false);
    }
  };

  const inputClass =
    "w-full rounded-lg border border-[var(--border-subtle)] bg-[var(--bg-inset)] px-2.5 py-1.5 text-[12px] text-[var(--text-primary)] placeholder:text-[var(--text-faint)] outline-none focus:border-[var(--accent-primary)]";
  const labelClass = "block text-[11px] font-medium text-[var(--text-secondary)] mb-1";

  return (
    <form onSubmit={(ev) => void onSubmit(ev)} class="space-y-3.5">
      <Show when={!props.schedule}>
        <div>
          <div class="mb-1.5 flex items-center gap-1.5 text-[11px] font-medium text-[var(--text-muted)]">
            <Sparkles size={12} class="text-[var(--accent-primary)]" />
            <span>{t("schedules.templatesTitle")}</span>
          </div>
          <div class="flex flex-wrap gap-1.5">
            <For each={SCHEDULE_TEMPLATES}>
              {(tpl) => (
                <button
                  type="button"
                  onClick={() => applyTemplate(tpl)}
                  class="rounded-lg border border-[var(--border-subtle)] bg-[var(--bg-inset)] px-2.5 py-1 text-[11px] text-[var(--text-secondary)] hover:border-[var(--accent-primary)]/50 hover:text-[var(--text-primary)] cursor-pointer"
                >
                  {tpl.name()}
                </button>
              )}
            </For>
          </div>
        </div>
      </Show>

      <div class="grid grid-cols-1 gap-3 sm:grid-cols-[1fr_auto] sm:items-end">
        <div>
          <label class={labelClass}>{t("schedules.fieldName")}</label>
          <input
            type="text"
            value={name()}
            onInput={(e) => setName(e.currentTarget.value)}
            placeholder={t("schedules.fieldNamePlaceholder")}
            class={inputClass}
          />
        </div>
        <label class="inline-flex items-center gap-2 rounded-lg border border-[var(--border-subtle)] bg-[var(--bg-inset)] px-3 py-1.5 text-[12px] text-[var(--text-primary)] cursor-pointer">
          <input
            type="checkbox"
            checked={enabled()}
            onChange={(e) => setEnabled(e.currentTarget.checked)}
            class="accent-[var(--accent-primary)]"
          />
          <span>{t("schedules.enabledLabel")}</span>
        </label>
      </div>

      <div>
        <label class={labelClass}>{t("schedules.fieldWorkspace")}</label>
        <select
          value={directory()}
          onChange={(e) => {
            const nextDir = e.currentTarget.value;
            setDirectory(nextDir);
            const matched = workspaceDirOptions().find((opt) => opt.dir === nextDir);
            setProjectID(matched?.projectID || "");
          }}
          class={inputClass}
        >
          <For each={workspaceDirOptions()}>
            {(opt) => <option value={opt.dir}>{opt.label}</option>}
          </For>
        </select>
      </div>

      {/* Session binding: fresh session vs bound existing session */}
      <div class="rounded-xl border border-[var(--border-subtle)] bg-[var(--bg-inset)]/40 p-3 space-y-2.5">
        <div class="text-[11px] font-medium text-[var(--text-secondary)]">
          {t("schedules.fieldSessionBinding")}
        </div>
        <div class="grid grid-cols-2 gap-1.5">
          <button
            type="button"
            onClick={() => setBindingMode("fresh")}
            class={`rounded-lg border px-2.5 py-1.5 text-[11.5px] font-medium transition-colors cursor-pointer ${
              bindingMode() === "fresh"
                ? "border-[var(--accent-primary)] bg-[var(--accent-surface)] text-[var(--text-primary)]"
                : "border-[var(--border-subtle)] bg-[var(--bg-panel)] text-[var(--text-muted)] hover:text-[var(--text-primary)]"
            }`}
          >
            {t("schedules.bindingFresh")}
          </button>
          <button
            type="button"
            onClick={() => setBindingMode("bound")}
            class={`rounded-lg border px-2.5 py-1.5 text-[11.5px] font-medium transition-colors cursor-pointer ${
              bindingMode() === "bound"
                ? "border-[var(--accent-primary)] bg-[var(--accent-surface)] text-[var(--text-primary)]"
                : "border-[var(--border-subtle)] bg-[var(--bg-panel)] text-[var(--text-muted)] hover:text-[var(--text-primary)]"
            }`}
          >
            {t("schedules.bindingBound")}
          </button>
        </div>

        <Show
          when={bindingMode() === "bound"}
          fallback={
            <div class="grid grid-cols-1 gap-2.5 sm:grid-cols-2">
              <div>
                <label class={labelClass}>{t("schedules.fieldSandboxMode")}</label>
                <select
                  value={sandboxMode()}
                  onChange={(e) => setSandboxMode(e.currentTarget.value as "local" | "worktree")}
                  class={inputClass}
                >
                  <option value="local">{t("schedules.sandboxLocal")}</option>
                  <option value="worktree">{t("schedules.sandboxWorktree")}</option>
                </select>
              </div>
              <div>
                <label class={labelClass}>{t("schedules.fieldMode")}</label>
                <select
                  value={mode()}
                  onChange={(e) => setMode(e.currentTarget.value as "build" | "plan")}
                  class={inputClass}
                >
                  <option value="build">{t("schedules.modeBuild")}</option>
                  <option value="plan">{t("schedules.modePlan")}</option>
                </select>
              </div>
              <label class="sm:col-span-2 inline-flex items-center gap-2 text-[11.5px] text-[var(--text-secondary)] cursor-pointer">
                <input
                  type="checkbox"
                  checked={commandSandbox()}
                  onChange={(e) => setCommandSandbox(e.currentTarget.checked)}
                  class="accent-[var(--accent-primary)]"
                />
                <span>{t("schedules.fieldCommandSandbox")}</span>
              </label>
            </div>
          }
        >
          <div>
            <label class={labelClass}>{t("schedules.fieldBoundSession")}</label>
            <select
              value={sessionID()}
              onChange={(e) => setSessionID(e.currentTarget.value)}
              class={inputClass}
            >
              <option value="">{t("schedules.boundSessionPlaceholder")}</option>
              <For each={boundSessionOptions()}>
                {(sess) => (
                  <option value={sess.id}>
                    {sess.title || sess.id} ({sess.id.slice(0, 12)})
                  </option>
                )}
              </For>
            </select>
            <p class="mt-1 text-[10.5px] text-[var(--text-faint)]">
              {t("schedules.boundSessionInheritHint")}
            </p>
          </div>
        </Show>
      </div>

      {/* Trigger */}
      <div class="rounded-xl border border-[var(--border-subtle)] bg-[var(--bg-inset)]/40 p-3 space-y-2.5">
        <div class="flex flex-wrap items-center justify-between gap-2">
          <span class="text-[11px] font-medium text-[var(--text-secondary)]">
            {t("schedules.fieldTrigger")}
          </span>
          <div class="flex flex-wrap gap-1">
            <button
              type="button"
              onClick={() => applyPreset("interval", "", 30)}
              class="rounded-md border border-[var(--border-subtle)] bg-[var(--bg-panel)] px-2 py-0.5 text-[10.5px] text-[var(--text-secondary)] hover:text-[var(--text-primary)] cursor-pointer"
            >
              {t("schedules.presetEvery30m")}
            </button>
            <button
              type="button"
              onClick={() => applyPreset("cron", "0 * * * *", 60)}
              class="rounded-md border border-[var(--border-subtle)] bg-[var(--bg-panel)] px-2 py-0.5 text-[10.5px] text-[var(--text-secondary)] hover:text-[var(--text-primary)] cursor-pointer"
            >
              {t("schedules.presetHourly")}
            </button>
            <button
              type="button"
              onClick={() => applyPreset("cron", "0 9 * * *", 1440)}
              class="rounded-md border border-[var(--border-subtle)] bg-[var(--bg-panel)] px-2 py-0.5 text-[10.5px] text-[var(--text-secondary)] hover:text-[var(--text-primary)] cursor-pointer"
            >
              {t("schedules.presetDaily9")}
            </button>
            <button
              type="button"
              onClick={() => applyPreset("cron", "0 9 * * 1-5", 1440)}
              class="rounded-md border border-[var(--border-subtle)] bg-[var(--bg-panel)] px-2 py-0.5 text-[10.5px] text-[var(--text-secondary)] hover:text-[var(--text-primary)] cursor-pointer"
            >
              {t("schedules.presetWeekdays9")}
            </button>
          </div>
        </div>

        <div class="grid grid-cols-1 gap-2.5 sm:grid-cols-3">
          <select
            value={triggerKind()}
            onChange={(e) => setTriggerKind(e.currentTarget.value as ScheduleTriggerKind)}
            class={inputClass}
          >
            <option value="cron">{t("schedules.triggerCron")}</option>
            <option value="interval">{t("schedules.triggerInterval")}</option>
          </select>

          <Show
            when={triggerKind() === "cron"}
            fallback={
              <div class="flex items-center gap-1.5">
                <input
                  type="number"
                  min={5}
                  max={43200}
                  value={intervalMinutes()}
                  onInput={(e) => setIntervalMinutes(Number(e.currentTarget.value))}
                  class={inputClass}
                />
                <span class="shrink-0 text-[11px] text-[var(--text-muted)]">
                  {t("schedules.intervalUnit")}
                </span>
              </div>
            }
          >
            <input
              type="text"
              value={cronExpr()}
              onInput={(e) => setCronExpr(e.currentTarget.value)}
              placeholder={t("schedules.cronPlaceholder")}
              class={`${inputClass} font-mono`}
            />
          </Show>

          <select
            value={misfire()}
            onChange={(e) => setMisfire(e.currentTarget.value as ScheduleMisfirePolicy)}
            class={inputClass}
            title={t("schedules.misfireLabel")}
          >
            <option value="skip">{t("schedules.misfireSkip")}</option>
            <option value="coalesce">{t("schedules.misfireCoalesce")}</option>
          </select>
        </div>

        <Show when={preview()}>
          {(p) => (
            <div class="text-[10.5px]">
              <Show
                when={p().valid}
                fallback={<span class="text-[var(--danger-text)]">{p().error}</span>}
              >
                <span class="text-[var(--text-faint)]">
                  {t("schedules.nextRunsLabel")}:{" "}
                  {(p().nextRuns || []).map((ms) => formatShortTimestamp(ms)).join(" · ")}
                </span>
              </Show>
            </div>
          )}
        </Show>
      </div>

      {/* Model & Skill */}
      <div class="grid grid-cols-1 gap-3 sm:grid-cols-2">
        <div>
          <label class={labelClass}>{t("schedules.fieldModel")}</label>
          <select
            value={modelValue()}
            onChange={(e) => setModelValue(e.currentTarget.value)}
            class={inputClass}
          >
            <option value="">{t("schedules.modelPlaceholder")}</option>
            <For each={modelOptions()}>
              {(opt) => <option value={opt.value}>{opt.label}</option>}
            </For>
          </select>
          <Show when={bindingMode() === "fresh"}>
            <p class="mt-1 text-[10px] text-[var(--text-faint)]">
              {t("schedules.modelRequiredHint")}
            </p>
          </Show>
        </div>

        <div>
          <label class={labelClass}>{t("schedules.fieldSkill")}</label>
          <select
            value={skillKey()}
            onChange={(e) => setSkillKey(e.currentTarget.value)}
            class={inputClass}
          >
            <option value="">{t("schedules.skillNone")}</option>
            <For each={skillsStore.skills}>
              {(sk) => (
                <option value={sk.key}>
                  {sk.name} {sk.sourceLabel ? `(${sk.sourceLabel})` : ""}
                </option>
              )}
            </For>
          </select>
        </div>
      </div>

      {/* Prompt */}
      <div>
        <label class={labelClass}>{t("schedules.fieldPrompt")}</label>
        <textarea
          rows={4}
          value={prompt()}
          onInput={(e) => setPrompt(e.currentTarget.value)}
          placeholder={t("schedules.promptPlaceholder")}
          class={`${inputClass} resize-y leading-relaxed`}
        />
      </div>

      {/* Notification & Delta Summary */}
      <div class="grid grid-cols-1 gap-2.5 sm:grid-cols-2 sm:items-center">
        <div>
          <label class={labelClass}>{t("schedules.fieldNotify")}</label>
          <select
            value={notifyOn()}
            onChange={(e) => setNotifyOn(e.currentTarget.value as ScheduleNotifyPolicy)}
            class={inputClass}
          >
            <option value="alert_or_error">{t("schedules.notifyAlertOrError")}</option>
            <option value="always">{t("schedules.notifyAlways")}</option>
            <option value="never">{t("schedules.notifyNever")}</option>
          </select>
        </div>
        <Show when={bindingMode() === "fresh"}>
          <label class="inline-flex items-center gap-2 pt-3 text-[11.5px] text-[var(--text-secondary)] cursor-pointer">
            <input
              type="checkbox"
              checked={injectLastSummary()}
              onChange={(e) => setInjectLastSummary(e.currentTarget.checked)}
              class="accent-[var(--accent-primary)]"
            />
            <span>{t("schedules.fieldInjectSummary")}</span>
          </label>
        </Show>
      </div>

      <Show when={error()}>
        <div class="rounded-lg border border-[var(--danger-border)]/40 bg-[var(--danger-surface)] px-3 py-2 text-[11.5px] text-[var(--danger-text)]">
          {error()}
        </div>
      </Show>

      <div class="flex items-center justify-between gap-2 border-t border-[var(--border-subtle)] pt-3">
        <Show when={props.schedule} fallback={<span />}>
          <button
            type="button"
            disabled={saving()}
            onClick={() => void onDelete()}
            class="inline-flex items-center gap-1.5 rounded-lg border border-[var(--danger-border)]/40 px-2.5 py-1.5 text-[12px] text-[var(--danger-text)] hover:bg-[var(--danger-surface)] cursor-pointer disabled:opacity-40"
          >
            <Trash2 size={13} />
            <span>{t("schedules.deleteBtn")}</span>
          </button>
        </Show>
        <button
          type="submit"
          disabled={saving()}
          class="inline-flex items-center gap-1.5 rounded-lg bg-[var(--accent-primary)] px-4 py-1.5 text-[12px] font-medium text-white hover:opacity-90 cursor-pointer disabled:opacity-40"
        >
          {saving()
            ? t("schedules.saving")
            : props.schedule
              ? t("schedules.saveBtn")
              : t("schedules.createBtn")}
        </button>
      </div>
    </form>
  );
}
