// web/src/components/approval/ApprovalGateCard.tsx
import { createEffect, createMemo, createSignal, For, onCleanup, Show } from "solid-js";
import {
  Check,
  ChevronDown,
  ChevronUp,
  Clock,
  ExternalLink,
  LoaderCircle,
  ShieldAlert,
  SlidersHorizontal,
  Terminal,
  X,
  Zap,
} from "lucide-solid";
import type { PermissionRequest } from "../../services/sse-types";
import {
  appSettings,
  isDestructivePermissionRequest,
  grantableClasses,
  grantClassLabel,
  isPermissionGoneError,
  isRunnerPermissionID,
  markPermissionResolved,
  openChangedFileInInspector,
  replyPermissionRequest,
  setApprovalMode,
  type PermissionReplyMode,
} from "../../store/workspace";
import { DiffChanges, FileIcon } from "../icons";
import FileDiffBlock from "../inspector/LazyFileDiffBlock";
import { t } from "../../i18n";
import {
  deriveHierarchicalRules,
  extractPermissionDiffs,
  isPersistableRule,
  rulesForReply,
} from "./approval-helpers";

/**
 * Ported from kilocode PermissionDock.tsx & PermissionDiff.tsx:
 * Supports command preview, multi-file PermissionDiff cards, hierarchical rule toggles,
 * and Reject-with-Feedback steering.
 */
export default function ApprovalGateCard(props: { request: PermissionRequest }) {
  const [busy, setBusy] = createSignal<null | "once" | "always" | "reject" | "class">(null);
  const [rejecting, setRejecting] = createSignal(false);
  const [feedback, setFeedback] = createSignal("");
  const [rulesExpanded, setRulesExpanded] = createSignal(false);
  const [selectedRules, setSelectedRules] = createSignal<Record<string, "allow" | "deny">>({});
  const [error, setError] = createSignal<string | null>(null);
  const [nowMs, setNowMs] = createSignal(Date.now());

  createEffect(() => {
    const exp = Number(props.request.expiresAt ?? 0);
    if (!exp) return;
    setNowMs(Date.now());
    const timer = window.setInterval(() => setNowMs(Date.now()), 1000);
    onCleanup(() => window.clearInterval(timer));
  });

  const remainingSeconds = createMemo(() => {
    const exp = Number(props.request.expiresAt ?? 0);
    if (!exp) return null;
    return Math.max(0, Math.ceil((exp - nowMs()) / 1000));
  });

  const command = createMemo(() => {
    const meta = props.request.metadata ?? {};
    if (typeof meta.command === "string" && meta.command.trim()) return meta.command.trim();
    if (props.request.permission === "bash" && props.request.patterns?.length) {
      return props.request.patterns.join("\n");
    }
    return "";
  });

  const toolArgsText = createMemo(() => {
    const args = props.request.metadata?.args;
    if (args === undefined || args === null) return "";
    if (typeof args === "string") return args.trim();
    try {
      return JSON.stringify(args, null, 2);
    } catch {
      return String(args);
    }
  });

  const diffs = createMemo(() => extractPermissionDiffs(props.request));
  const rules = createMemo(() => deriveHierarchicalRules(props.request, command()));
  const isDestructive = createMemo(() => isDestructivePermissionRequest(props.request));
  const isRunner = createMemo(() => isRunnerPermissionID(props.request.id));
  const sessionClasses = createMemo(() => grantableClasses(props.request));

  const grantSessionClass = async () => {
    if (busy()) return;
    setBusy("class");
    setError(null);
    try {
      await replyPermissionRequest(props.request, "once", { grantClass: true });
    } catch (err) {
      if (isPermissionGoneError(err)) {
        markPermissionResolved(props.request.id);
        return;
      }
      console.error("session grant failed:", err);
      setError(String(err));
    } finally {
      setBusy(null);
    }
  };

  const alwaysButtonLabel = createMemo(() => {
    if (isRunner()) {
      if (props.request.permission === "tool") return t("approval.allowSessionToolCall");
      if (props.request.permission === "bash") return t("approval.allowSessionCommand");
    }
    return t("approval.allowAlways");
  });

  const runnerScopeHint = createMemo(() => {
    if (!isRunner() || isDestructive()) return "";
    if (props.request.permission === "tool") return t("approval.sessionGrantScopeTool");
    if (props.request.permission === "bash") return t("approval.sessionGrantScopeCommand");
    return "";
  });

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

  /**
   * The rule list is the write path: picking a rule settles the pending request
   * in one go, so a selection never dead-ends when the outer Always button is
   * hidden. Deny rules are persisted as picked (fail closed, except the bare
   * `*` catch-all). Allow rules are only persisted when exact: a saved glob
   * like `git *` would let a destructive variant (`git push --force`) skip the
   * gate, so a wildcard prefix only settles this request through the
   * session-scoped "always" reply. Destructive requests can only pick a deny
   * rule (AGENTS 2.2.6).
   */
  const applyRuleDecision = (rule: string, decision: "allow" | "deny") => {
    if (busy()) return;
    if (decision === "allow" && isDestructive()) return;
    toggleRuleDecision(rule, decision);
    void reply(
      decision === "allow" ? "always" : "reject",
      isPersistableRule(rule, decision) ? { [rule]: decision } : {}
    );
  };

  const reply = async (
    mode: PermissionReplyMode,
    rulesOverride?: Record<string, "allow" | "deny">
  ) => {
    if (busy()) return;
    setBusy(mode);
    setError(null);
    try {
      await replyPermissionRequest(props.request, mode, {
        rules:
          rulesOverride ??
          rulesForReply(props.request, mode, isDestructive(), selectedRules()),
        note: feedback(),
      });
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
        <div class="ml-auto flex items-center gap-1.5 shrink-0">
          <Show when={remainingSeconds() !== null}>
            <span class="inline-flex items-center gap-1 rounded-md border border-[var(--warn-border)] bg-[var(--bg-panel)]/70 px-1.5 py-0.5 font-mono text-[10.5px] text-[var(--warn-text)]">
              <Clock size={10} />
              {t("approval.expiresIn", { seconds: remainingSeconds() ?? 0 })}
            </span>
          </Show>
          <span class="rounded-md bg-[var(--bg-panel)]/70 px-2 py-0.5 font-mono text-[11px] text-[var(--warn-text)]">
            {props.request.permission || "tool"}
          </span>
        </div>
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

        <Show when={toolArgsText()}>
          <div class="space-y-1">
            <div class="text-[10.5px] font-medium text-[var(--text-muted)]">
              {t("approval.toolArgsLabel")}
            </div>
            <pre
              class="max-h-[120px] overflow-auto rounded-lg bg-[var(--bg-code)] px-2.5 py-1.5 font-mono leading-relaxed text-[var(--text-primary)] selectable-text whitespace-pre-wrap break-all"
              style={{ "font-size": "var(--app-code-text-size)" }}
            >
              {toolArgsText()}
            </pre>
          </div>
        </Show>

        <Show when={runnerScopeHint()}>
          <div class="text-[11px] leading-relaxed text-[var(--text-muted)]">
            {runnerScopeHint()}
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
                <p class="pb-1 text-[11px] leading-relaxed text-[var(--text-muted)]">
                  {t("approval.ruleScopeHint")}
                </p>
                <For each={rules()}>
                  {(rule) => {
                    const dec = () => selectedRules()[rule];
                    return (
                      <div class="flex flex-wrap items-center justify-between gap-2 text-[12px]">
                        <code class="truncate font-mono text-[var(--text-primary)] bg-[var(--bg-inset)] px-1.5 py-0.5 rounded">
                          {rule}
                        </code>
                        <div class="flex items-center gap-1 shrink-0">
                          <Show when={!isDestructive()}>
                            <button
                              type="button"
                              disabled={!!busy()}
                              onClick={() => applyRuleDecision(rule, "allow")}
                              class={`rounded px-2 py-0.5 text-[12px] font-medium transition-colors cursor-pointer disabled:opacity-50 ${
                                dec() === "allow"
                                  ? "bg-[var(--ok-surface)] text-[var(--ok-text)] border border-[var(--ok-border)]"
                                  : "text-[var(--text-muted)] hover:bg-[var(--bg-hover)]"
                              }`}
                            >
                              {t("approval.ruleAlwaysAllow")}
                            </button>
                          </Show>
                          <button
                            type="button"
                            disabled={!!busy()}
                            onClick={() => applyRuleDecision(rule, "deny")}
                            class={`rounded px-2 py-0.5 text-[12px] font-medium transition-colors cursor-pointer disabled:opacity-50 ${
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
              <Show when={sessionClasses().length > 0}>
                <button
                  type="button"
                  class="inline-flex items-center gap-1.5 rounded-lg border border-[var(--accent-border)] bg-[var(--accent-surface)] hover:opacity-90 px-3 py-1 text-[12px] font-medium text-[var(--accent-primary)] disabled:opacity-50 transition-opacity cursor-pointer"
                  disabled={!!busy()}
                  title={t("approval.sessionClassScope", {
                    classes: sessionClasses().map(grantClassLabel).join(t("approval.grantClassSeparator")),
                  })}
                  onClick={() => void grantSessionClass()}
                >
                  <Show when={busy() !== "class"} fallback={<LoaderCircle size={12} class="animate-spin" />}>
                    <Zap size={12} />
                  </Show>
                  {t("approval.allowSessionClass")}
                </button>
              </Show>
              <Show when={!isDestructive()}>
                <button
                  type="button"
                  class="inline-flex items-center gap-1.5 rounded-lg border border-[var(--accent-border)] bg-[var(--accent-surface)] hover:opacity-90 px-3 py-1 text-[12px] font-medium text-[var(--accent-primary)] disabled:opacity-50 transition-opacity cursor-pointer"
                  disabled={!!busy()}
                  onClick={() => void reply("always")}
                >
                  <Show when={busy() !== "always"} fallback={<LoaderCircle size={12} class="animate-spin" />}>
                    <Zap size={12} />
                  </Show>
                  {alwaysButtonLabel()}
                </button>
              </Show>
              <button
                type="button"
                class="inline-flex items-center gap-1.5 rounded-lg border border-[var(--danger-border)] bg-[var(--danger-surface)] hover:opacity-90 px-3 py-1 text-[12px] font-medium text-[var(--danger-text)] disabled:opacity-50 transition-opacity cursor-pointer"
                disabled={!!busy()}
                onClick={() => setRejecting(true)}
              >
                <X size={12} />
                {t("approval.reject")}…
              </button>
              <Show when={!isDestructive() && appSettings.approvalMode !== "auto" && appSettings.approvalMode !== "trusted"}>
                <button
                  type="button"
                  class="ml-auto inline-flex items-center gap-1 rounded-lg px-2 py-1 text-[11px] text-[var(--text-secondary)] hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)] transition-colors cursor-pointer"
                  disabled={!!busy()}
                  onClick={() =>
                    void setApprovalMode("auto").catch((err) => {
                      console.warn("Failed to set approval mode:", err);
                    })
                  }
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
