// web/src/components/approval/ApprovalGateCard.tsx
import { createMemo, createSignal, For, Show } from "solid-js";
import {
  Check,
  ChevronDown,
  ChevronUp,
  ExternalLink,
  LoaderCircle,
  ShieldAlert,
  SlidersHorizontal,
  Terminal,
  X,
  Zap,
} from "lucide-solid";
import type { PermissionRequest, SnapshotFileDiff } from "../../services/sse-types";
import { hoxiAPI } from "../../services/api";
import {
  openDiffInInspector,
  sessionDirectory,
  workspace,
} from "../../store/workspace";
import { DiffChanges, FileIcon } from "../icons";
import { FileDiffBlock } from "../inspector/PierreDiffViewer";
import { locale, t } from "../../i18n";

interface ExtractedPermissionDiff {
  file: string;
  patch?: string;
  diff?: SnapshotFileDiff;
  additions: number;
  deletions: number;
}

/**
 * Ported from kilocode permission-diff-utils.ts:
 * Extracts structured file diffs (`filediff`, `files[]`, or raw `diff`) from a PermissionRequest.
 */
function extractPermissionDiffs(request: PermissionRequest): ExtractedPermissionDiff[] {
  const meta = request.metadata ?? {};

  // 1. Direct `filediff` object (patch or before/after)
  if (meta.filediff && typeof meta.filediff === "object") {
    const fd = meta.filediff as Record<string, any>;
    const file =
      typeof fd.file === "string"
        ? fd.file
        : typeof fd.filePath === "string"
          ? fd.filePath
          : typeof meta.filepath === "string"
            ? meta.filepath
            : "patch";
    const additions = typeof fd.additions === "number" ? fd.additions : 0;
    const deletions = typeof fd.deletions === "number" ? fd.deletions : 0;
    if (typeof fd.patch === "string" && fd.patch.trim()) {
      return [{ file, patch: fd.patch, additions, deletions }];
    }
    if (typeof fd.before === "string" || typeof fd.after === "string") {
      return [
        {
          file,
          diff: {
            file,
            before: typeof fd.before === "string" ? fd.before : "",
            after: typeof fd.after === "string" ? fd.after : "",
            additions,
            deletions,
          },
          additions,
          deletions,
        },
      ];
    }
  }

  // 2. Multi-file `files` array (apply_patch / multiedit)
  if (Array.isArray(meta.files)) {
    const list: ExtractedPermissionDiff[] = [];
    for (const raw of meta.files) {
      if (!raw || typeof raw !== "object") continue;
      const item = raw as Record<string, any>;
      const file =
        typeof item.relativePath === "string"
          ? item.relativePath
          : typeof item.filePath === "string"
            ? item.filePath
            : typeof item.file === "string"
              ? item.file
              : "";
      if (!file) continue;
      const additions = typeof item.additions === "number" ? item.additions : 0;
      const deletions = typeof item.deletions === "number" ? item.deletions : 0;
      if (typeof item.patch === "string" && item.patch.trim()) {
        list.push({ file, patch: item.patch, additions, deletions });
      } else if (typeof item.before === "string" || typeof item.after === "string") {
        list.push({
          file,
          diff: {
            file,
            before: typeof item.before === "string" ? item.before : "",
            after: typeof item.after === "string" ? item.after : "",
            additions,
            deletions,
          },
          additions,
          deletions,
        });
      }
    }
    if (list.length > 0) return list;
  }

  // 3. Raw `diff` string
  if (typeof meta.diff === "string" && meta.diff.trim()) {
    const file =
      typeof meta.filepath === "string"
        ? meta.filepath
        : typeof meta.filePath === "string"
          ? meta.filePath
          : request.patterns?.[0] || "patch";
    return [{ file, patch: meta.diff, additions: 0, deletions: 0 }];
  }

  return [];
}

/**
 * Derives hierarchical command/file rules from `request.always`, `metadata.rules`, or bash tokens.
 */
function deriveHierarchicalRules(request: PermissionRequest, commandStr: string): string[] {
  const metaRules = Array.isArray(request.metadata?.rules)
    ? (request.metadata.rules as string[])
    : [];
  const alwaysRules = Array.isArray(request.always) ? request.always : [];
  const combined = [...new Set([...metaRules, ...alwaysRules].filter(Boolean))];
  if (combined.length > 0) return combined;

  if (commandStr) {
    const tokens = commandStr.trim().split(/\s+/);
    const derived: string[] = [];
    if (tokens.length >= 2) derived.push(`${tokens[0]} ${tokens[1]} *`);
    if (tokens.length >= 1 && tokens[0]) derived.push(`${tokens[0]} *`);
    return derived;
  }
  return [];
}

/**
 * Ported from kilocode PermissionDock.tsx & PermissionDiff.tsx:
 * Supports command preview, multi-file PermissionDiff cards, hierarchical rule toggles,
 * and Reject-with-Feedback steering.
 */
export default function ApprovalGateCard(props: { request: PermissionRequest }) {
  const [busy, setBusy] = createSignal<null | "once" | "always" | "reject">(null);
  const [rejecting, setRejecting] = createSignal(false);
  const [feedback, setFeedback] = createSignal("");
  const [rulesExpanded, setRulesExpanded] = createSignal(false);
  const [selectedRules, setSelectedRules] = createSignal<Record<string, "allow" | "deny">>({});
  const [error, setError] = createSignal<string | null>(null);

  const command = createMemo(() => {
    const meta = props.request.metadata ?? {};
    if (typeof meta.command === "string" && meta.command.trim()) return meta.command.trim();
    if (props.request.permission === "bash" && props.request.patterns?.length) {
      return props.request.patterns.join("\n");
    }
    return "";
  });

  const diffs = createMemo(() => extractPermissionDiffs(props.request));
  const rules = createMemo(() => deriveHierarchicalRules(props.request, command()));

  const toggleRuleDecision = (rule: string, decision: "allow" | "deny") => {
    setSelectedRules((prev) => {
      const next = { ...prev };
      if (next[rule] === decision) {
        delete next[rule];
      } else {
        next[rule] = decision;
      }
      return next;
    });
  };

  const persistSelectedRules = async () => {
    const entries = Object.entries(selectedRules());
    if (entries.length === 0) return;
    try {
      const toolName = props.request.permission || "bash";
      const cfg = await hoxiAPI.getGlobalConfig().catch(() => ({} as Record<string, any>));
      const existingPerm = (cfg.permission && typeof cfg.permission === "object" ? cfg.permission : {}) as Record<string, any>;
      const currentToolPerm =
        existingPerm[toolName] && typeof existingPerm[toolName] === "object"
          ? { ...existingPerm[toolName] }
          : {};
      for (const [pattern, dec] of entries) {
        currentToolPerm[pattern] = dec;
      }
      await hoxiAPI.updateGlobalConfig({
        permission: {
          ...existingPerm,
          [toolName]: currentToolPerm,
        },
      });
    } catch (err) {
      console.warn("failed to persist custom permission rules:", err);
    }
  };

  const reply = async (mode: "once" | "always" | "reject") => {
    if (busy()) return;
    setBusy(mode);
    setError(null);
    const dir = sessionDirectory(props.request.sessionID);
    try {
      if (Object.keys(selectedRules()).length > 0) {
        await persistSelectedRules();
      }
      await hoxiAPI.replyPermission(props.request.id, mode, dir);
      // If rejecting with steering feedback, send the feedback prompt right after
      const note = feedback().trim();
      if (mode === "reject" && note && props.request.sessionID) {
        await hoxiAPI.sendPromptAsync(props.request.sessionID, note, {
          agent: workspace.activeMode,
          model: workspace.selectedModel || undefined,
          variant: workspace.reasoningEffort || undefined,
          directory: dir,
        });
      }
    } catch (err) {
      console.error("permission reply failed:", err);
      setError(String(err));
      setBusy(null);
    }
  };

  return (
    <div class="rounded-xl border border-[var(--warn-border)] bg-[var(--warn-surface)] overflow-hidden shadow-sm">
      <div class="flex items-center gap-2 px-3.5 py-2 border-b border-[var(--warn-border)]">
        <ShieldAlert size={14} class="text-[var(--warn-text)] shrink-0" />
        <span class="text-[12px] font-semibold text-[var(--warn-text)]">
          {t("approval.title")}
        </span>
        <Show when={props.request.metadata?.description}>
          <span class="truncate text-[11px] text-[var(--text-secondary)]">
            · {String(props.request.metadata?.description)}
          </span>
        </Show>
        <span class="ml-auto rounded-md bg-[var(--bg-panel)]/70 px-2 py-0.5 font-mono text-[10.5px] text-[var(--warn-text)]">
          {props.request.permission || "tool"}
        </span>
      </div>

      <div class="px-3.5 py-2.5 space-y-2.5 max-h-[290px] overflow-y-auto">
        <Show
          when={command()}
          fallback={
            <Show when={props.request.patterns?.length && diffs().length === 0}>
              <div class="space-y-1">
                <For each={props.request.patterns}>
                  {(pattern) => (
                    <pre class="overflow-auto rounded-lg bg-[var(--bg-code)] px-2.5 py-1.5 font-mono text-[11px] leading-relaxed text-[var(--text-primary)] selectable-text whitespace-pre-wrap break-all">
                      {pattern}
                    </pre>
                  )}
                </For>
              </div>
            </Show>
          }
        >
          <div class="flex items-start gap-2 rounded-lg bg-[var(--bg-code)] px-3 py-2 font-mono text-[11.5px] text-[var(--text-primary)]">
            <Terminal size={13} class="mt-0.5 text-[var(--accent-primary)] shrink-0" />
            <pre class="whitespace-pre-wrap break-all selectable-text">{command()}</pre>
          </div>
        </Show>

        {/* Kilo PermissionDiff: Structured File Diff Preview Cards */}
        <Show when={diffs().length > 0}>
          <div class="space-y-2">
            <For each={diffs()}>
              {(item) => (
                <div class="overflow-hidden rounded-lg border border-[var(--border-subtle)] bg-[var(--bg-panel)]">
                  <div class="flex items-center justify-between gap-2 border-b border-[var(--border-subtle)] bg-[var(--bg-inset)] px-2.5 py-1.5 text-[11px]">
                    <div class="flex items-center gap-1.5 min-w-0">
                      <FileIcon
                        node={{ path: item.file, type: "file" }}
                        class="w-3.5 h-3.5 shrink-0"
                      />
                      <span class="truncate font-mono text-[var(--text-primary)]">
                        {item.file}
                      </span>
                    </div>
                    <div class="flex items-center gap-2 shrink-0">
                      <Show when={item.additions > 0 || item.deletions > 0}>
                        <DiffChanges
                          changes={{ additions: item.additions, deletions: item.deletions }}
                        />
                      </Show>
                      <button
                        type="button"
                        onClick={() => openDiffInInspector(item.file)}
                        class="inline-flex items-center gap-1 rounded px-1.5 py-0.5 text-[10.5px] text-[var(--text-muted)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-hover)] cursor-pointer"
                        title={t("approval.openDiffInInspector")}
                      >
                        <ExternalLink size={11} />
                      </button>
                    </div>
                  </div>
                  <FileDiffBlock
                    patch={item.patch}
                    diff={item.diff}
                    diffStyle="unified"
                    maxHeight={160}
                  />
                </div>
              )}
            </For>
          </div>
        </Show>

        {/* Kilo Hierarchical Rule Toggles */}
        <Show when={rules().length > 0}>
          <div class="rounded-lg border border-[var(--border-subtle)] bg-[var(--bg-panel)]/70 px-2.5 py-1.5">
            <button
              type="button"
              onClick={() => setRulesExpanded((v) => !v)}
              class="flex w-full items-center justify-between text-[11px] text-[var(--text-secondary)] hover:text-[var(--text-primary)] cursor-pointer"
            >
              <span class="inline-flex items-center gap-1.5 font-medium">
                <SlidersHorizontal size={11} class="text-[var(--accent-primary)]" />
                <span>
                  {t("approval.autoApproveRules", { count: rules().length })}
                </span>
              </span>
              {rulesExpanded() ? <ChevronUp size={12} /> : <ChevronDown size={12} />}
            </button>

            <Show when={rulesExpanded()}>
              <div class="mt-1.5 space-y-1 border-t border-[var(--border-subtle)] pt-1.5">
                <For each={rules()}>
                  {(rule) => {
                    const dec = () => selectedRules()[rule];
                    return (
                      <div class="flex items-center justify-between gap-2 text-[11px]">
                        <code class="truncate font-mono text-[var(--text-primary)] bg-[var(--bg-inset)] px-1.5 py-0.5 rounded">
                          {rule}
                        </code>
                        <div class="flex items-center gap-1 shrink-0">
                          <button
                            type="button"
                            onClick={() => toggleRuleDecision(rule, "allow")}
                            class={`rounded px-2 py-0.5 text-[10px] font-medium transition-colors cursor-pointer ${
                              dec() === "allow"
                                ? "bg-[var(--ok-surface)] text-[var(--ok-text)] border border-[var(--ok-border)]"
                                : "text-[var(--text-muted)] hover:bg-[var(--bg-hover)]"
                            }`}
                          >
                            {t("approval.ruleAlwaysAllow")}
                          </button>
                          <button
                            type="button"
                            onClick={() => toggleRuleDecision(rule, "deny")}
                            class={`rounded px-2 py-0.5 text-[10px] font-medium transition-colors cursor-pointer ${
                              dec() === "deny"
                                ? "bg-[var(--danger-surface)] text-[var(--danger-text)] border border-[var(--danger-border)]"
                                : "text-[var(--text-muted)] hover:bg-[var(--bg-hover)]"
                            }`}
                          >
                            {t("approval.ruleAlwaysDeny")}
                          </button>
                        </div>
                      </div>
                    );
                  }}
                </For>
              </div>
            </Show>
          </div>
        </Show>

        <Show when={rejecting()}>
          <div class="space-y-1">
            <textarea
              rows={2}
              value={feedback()}
              onInput={(ev) => setFeedback(ev.currentTarget.value)}
              onKeyDown={(ev) => {
                if (ev.key === "Escape") {
                  ev.preventDefault();
                  setRejecting(false);
                } else if (ev.key === "Enter" && !ev.shiftKey && !ev.isComposing) {
                  ev.preventDefault();
                  void reply("reject");
                }
              }}
              placeholder={t("approval.rejectFeedbackPlaceholder")}
              class="w-full rounded-lg border border-[var(--border-strong)] bg-[var(--bg-panel)] px-2.5 py-1.5 text-[12px] text-[var(--text-primary)] placeholder:text-[var(--text-faint)] outline-none"
            />
          </div>
        </Show>

        <Show when={error()}>
          <div class="text-[11px] text-[var(--danger-text)] font-mono">{error()}</div>
        </Show>
      </div>

      <div class="flex flex-wrap items-center gap-2 px-3.5 py-2 border-t border-[var(--warn-border)] bg-[var(--bg-panel)]/40">
        <Show
          when={rejecting()}
          fallback={
            <>
              <button
                type="button"
                class="inline-flex items-center gap-1.5 rounded-lg border border-[var(--ok-border)] bg-[var(--ok-surface)] hover:opacity-90 px-3 py-1 text-[11.5px] font-medium text-[var(--ok-text)] disabled:opacity-50 transition-opacity cursor-pointer"
                disabled={!!busy()}
                onClick={() => void reply("once")}
              >
                <Show when={busy() !== "once"} fallback={<LoaderCircle size={12} class="animate-spin" />}>
                  <Check size={12} />
                </Show>
                {t("approval.allowOnce")}
              </button>
              <button
                type="button"
                class="inline-flex items-center gap-1.5 rounded-lg border border-[var(--accent-border)] bg-[var(--accent-surface)] hover:opacity-90 px-3 py-1 text-[11.5px] font-medium text-[var(--accent-primary)] disabled:opacity-50 transition-opacity cursor-pointer"
                disabled={!!busy()}
                onClick={() => void reply("always")}
              >
                <Show when={busy() !== "always"} fallback={<LoaderCircle size={12} class="animate-spin" />}>
                  <Zap size={12} />
                </Show>
                {t("approval.allowAlways")}
              </button>
              <button
                type="button"
                class="inline-flex items-center gap-1.5 rounded-lg border border-[var(--danger-border)] bg-[var(--danger-surface)] hover:opacity-90 px-3 py-1 text-[11.5px] font-medium text-[var(--danger-text)] disabled:opacity-50 transition-opacity cursor-pointer"
                disabled={!!busy()}
                onClick={() => setRejecting(true)}
              >
                <X size={12} />
                {t("approval.reject")}…
              </button>
            </>
          }
        >
          <button
            type="button"
            class="inline-flex items-center gap-1.5 rounded-lg bg-[var(--status-danger)] text-white hover:opacity-90 px-3 py-1 text-[11.5px] font-medium disabled:opacity-50 transition-opacity cursor-pointer"
            disabled={!!busy()}
            onClick={() => void reply("reject")}
          >
            <Show when={busy() !== "reject"} fallback={<LoaderCircle size={12} class="animate-spin" />}>
              <X size={12} />
            </Show>
            {feedback().trim()
              ? t("approval.rejectWithFeedback")
              : t("approval.reject")}
          </button>
          <button
            type="button"
            class="inline-flex items-center rounded-lg px-2.5 py-1 text-[11.5px] text-[var(--text-secondary)] hover:bg-[var(--bg-hover)] cursor-pointer"
            disabled={!!busy()}
            onClick={() => setRejecting(false)}
          >
            {t("common.cancel")}
          </button>
        </Show>
      </div>
    </div>
  );
}
