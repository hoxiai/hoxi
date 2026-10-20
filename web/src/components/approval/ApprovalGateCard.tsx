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
import type { PermissionRequest } from "../../services/sse-types";
import { hoxiAPI, isRunnerPermissionID } from "../../services/api";
import {
  appSettings,
  isDestructivePermissionRequest,
  isPermissionGoneError,
  markPermissionResolved,
  openChangedFileInInspector,
  sessionDirectory,
  sessionAIConfig,
  setApprovalMode,
  workspace,
} from "../../store/workspace";
import { DiffChanges, FileIcon } from "../icons";
import FileDiffBlock from "../inspector/LazyFileDiffBlock";
import { t } from "../../i18n";
import { deriveHierarchicalRules, extractPermissionDiffs } from "./approval-helpers";

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
  const isDestructive = createMemo(() => isDestructivePermissionRequest(props.request));

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
    // Runner (agy) requests: rules live in the Go gate, and the agy turn is
    // still running, so the note travels with the reply as the deny reason.
    const runner = isRunnerPermissionID(props.request.id);
    try {
      if (!runner && Object.keys(selectedRules()).length > 0) {
        await persistSelectedRules();
      }
      const note = feedback().trim();
      await hoxiAPI.replyPermission(props.request.id, mode, dir, runner && mode === "reject" ? note : undefined);
      markPermissionResolved(props.request.id);
      // If rejecting with steering feedback, send the feedback prompt right after
      if (!runner && mode === "reject" && note && props.request.sessionID) {
        await hoxiAPI.sendPromptAsync(props.request.sessionID, note, {
          agent: sessionAIConfig(props.request.sessionID).agent,
          model: sessionAIConfig(props.request.sessionID).model || undefined,
          variant: sessionAIConfig(props.request.sessionID).effort || undefined,
          directory: dir,
        });
      }
    } catch (err) {
      if (isPermissionGoneError(err)) {
        markPermissionResolved(props.request.id);
        return;
      }
      console.error("permission reply failed:", err);
      setError(String(err));
    } finally {
      setBusy(null);
    }
  };

  return (
    <div class="hoxi-item-pop rounded-xl border border-[var(--warn-border)] bg-[var(--warn-surface)] overflow-hidden shadow-sm">
      <div class="flex items-center gap-2 px-3.5 py-2 border-b border-[var(--warn-border)]">
        <ShieldAlert size={14} class="text-[var(--warn-text)] shrink-0" />
        <span class="text-[12px] font-semibold text-[var(--warn-text)]">
          {t("approval.title")}
        </span>
        <Show when={isDestructive()}>
          <span class="rounded-md border border-[var(--danger-border)] bg-[var(--danger-surface)] px-1.5 py-0.5 text-[11px] font-semibold text-[var(--danger-text)]">
            {t("approval.highRiskBadge")}
          </span>
        </Show>
        <Show when={props.request.metadata?.description}>
          <span class="truncate text-[11px] text-[var(--text-secondary)]">
            · {String(props.request.metadata?.description)}
          </span>
        </Show>
        <span class="ml-auto rounded-md bg-[var(--bg-panel)]/70 px-2 py-0.5 font-mono text-[11px] text-[var(--warn-text)]">
          {props.request.permission || "tool"}
        </span>
      </div>

      <div class="px-3.5 py-2.5 space-y-2.5 max-h-[290px] overflow-y-auto">
        <Show when={props.request.permission === "doom_loop"}>
          <div class="text-[12px] leading-relaxed text-[var(--warn-text)]">
            {t("approval.doomLoopNotice", {
              tool: String(props.request.metadata?.tool ?? props.request.patterns?.[0] ?? ""),
            })}
          </div>
        </Show>
        <Show
          when={command()}
          fallback={
            <Show when={props.request.patterns?.length && diffs().length === 0}>
              <div class="space-y-1">
                <For each={props.request.patterns}>
                  {(pattern) => (
                    <pre class="overflow-auto rounded-lg bg-[var(--bg-code)] px-2.5 py-1.5 font-mono leading-relaxed text-[var(--text-primary)] selectable-text whitespace-pre-wrap break-all" style={{ "font-size": "var(--app-code-text-size)" }}>
                      {pattern}
                    </pre>
                  )}
                </For>
              </div>
            </Show>
          }
        >
          <div class="flex items-start gap-2 rounded-lg bg-[var(--bg-code)] px-3 py-2 font-mono text-[var(--text-primary)]" style={{ "font-size": "var(--app-code-text-size)" }}>
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
                         onClick={() => openChangedFileInInspector(item)}
                        class="inline-flex items-center gap-1 rounded px-1.5 py-0.5 text-[11px] text-[var(--text-muted)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-hover)] cursor-pointer"
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

        {/* Kilo Hierarchical Rule Toggles (OpenCode config only; agy uses session "always") */}
        <Show when={rules().length > 0 && !isRunnerPermissionID(props.request.id)}>
          <div class="rounded-lg border border-[var(--border-subtle)] bg-[var(--bg-panel)]/70 px-2.5 py-1.5">
            <button
              type="button"
              onClick={() => setRulesExpanded((v) => !v)}
              class="flex w-full items-center justify-between text-[12px] text-[var(--text-secondary)] hover:text-[var(--text-primary)] cursor-pointer"
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
                      <div class="flex flex-wrap items-center justify-between gap-2 text-[12px]">
                        <code class="truncate font-mono text-[var(--text-primary)] bg-[var(--bg-inset)] px-1.5 py-0.5 rounded">
                          {rule}
                        </code>
                        <div class="flex items-center gap-1 shrink-0">
                          <button
                            type="button"
                            onClick={() => toggleRuleDecision(rule, "allow")}
                            class={`rounded px-2 py-0.5 text-[12px] font-medium transition-colors cursor-pointer ${
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
                            class={`rounded px-2 py-0.5 text-[12px] font-medium transition-colors cursor-pointer ${
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
                class="inline-flex items-center gap-1.5 rounded-lg border border-[var(--ok-border)] bg-[var(--ok-surface)] hover:opacity-90 px-3 py-1 text-[12px] font-medium text-[var(--ok-text)] disabled:opacity-50 transition-opacity cursor-pointer"
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
                class="inline-flex items-center gap-1.5 rounded-lg border border-[var(--accent-border)] bg-[var(--accent-surface)] hover:opacity-90 px-3 py-1 text-[12px] font-medium text-[var(--accent-primary)] disabled:opacity-50 transition-opacity cursor-pointer"
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
                class="inline-flex items-center gap-1.5 rounded-lg border border-[var(--danger-border)] bg-[var(--danger-surface)] hover:opacity-90 px-3 py-1 text-[12px] font-medium text-[var(--danger-text)] disabled:opacity-50 transition-opacity cursor-pointer"
                disabled={!!busy()}
                onClick={() => setRejecting(true)}
              >
                <X size={12} />
                {t("approval.reject")}…
              </button>
              <Show when={!isDestructive() && appSettings.approvalMode !== "auto"}>
                <button
                  type="button"
                  class="ml-auto inline-flex items-center gap-1 rounded-lg px-2 py-1 text-[11px] text-[var(--text-secondary)] hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)] transition-colors cursor-pointer"
                  disabled={!!busy()}
                  onClick={() => setApprovalMode("auto")}
                >
                  <Zap size={11} class="text-[var(--ok-text)]" />
                  {t("approval.switchToAutoApprove")}
                </button>
              </Show>
            </>
          }
        >
          <button
            type="button"
            class="inline-flex items-center gap-1.5 rounded-lg bg-[var(--status-danger)] text-white hover:opacity-90 px-3 py-1 text-[12px] font-medium disabled:opacity-50 transition-opacity cursor-pointer"
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
            class="inline-flex items-center rounded-lg px-2.5 py-1 text-[12px] text-[var(--text-secondary)] hover:bg-[var(--bg-hover)] cursor-pointer"
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
