// web/src/components/timeline/CollabPromptCard.tsx
// Structured, high-signal display for remote collaborator turns (PM / QA) inside UserTurnItem.
// Keeps the collaborator's actual input prominent while tucking prompt-injection guardrails,
// restricted directories, and legacy boilerplate into an on-demand collapsible drawer.
import { createMemo, createSignal, For, Show } from "solid-js";
import {
  AlertTriangle,
  Bug,
  ChevronDown,
  GitBranch,
  Lock,
  MessageSquareText,
  ShieldCheck,
  Sparkles,
} from "lucide-solid";
import type { ParsedCollabPrompt } from "../../store/workspace";
import { t } from "../../i18n";
import { Icon } from "../icons";

const COLLAB_BODY_FOLD_LINES = 10;
const COLLAB_BODY_FOLD_CHARS = 600;
const MAX_COLLAB_EXPANDED_STATES = 512;

// Survives session-switch remounts while remaining bounded (FIFO).
const expandedCollabGuardrails = new Set<string>();
const expandedCollabBodies = new Set<string>();

function rememberKey(set: Set<string>, key: string, open: boolean) {
  if (open) {
    set.delete(key);
    if (set.size >= MAX_COLLAB_EXPANDED_STATES) {
      const oldest = set.values().next().value;
      if (oldest !== undefined) set.delete(oldest);
    }
    set.add(key);
  } else {
    set.delete(key);
  }
}

export function CollabPromptCard(props: {
  parsed: ParsedCollabPrompt;
  partId: string;
  forceExpand?: boolean;
}) {
  const [guardrailsOpen, setGuardrailsSignal] = createSignal(
    expandedCollabGuardrails.has(props.partId)
  );
  const [bodyExpanded, setBodyExpandedSignal] = createSignal(
    expandedCollabBodies.has(props.partId)
  );

  const toggleGuardrails = () => {
    const next = !guardrailsOpen();
    rememberKey(expandedCollabGuardrails, props.partId, next);
    setGuardrailsSignal(next);
  };

  const toggleBody = () => {
    const next = !bodyExpanded();
    rememberKey(expandedCollabBodies, props.partId, next);
    setBodyExpandedSignal(next);
  };

  const isPM = () => props.parsed.role === "pm";
  const isDirectLocal = () => props.parsed.workspace === "local";
  const isBodyExpanded = createMemo(() => bodyExpanded() || Boolean(props.forceExpand));

  const roleLabel = createMemo(() => {
    const base = isPM() ? t("timeline.collabRolePM") : t("timeline.collabRoleQA");
    const author = props.parsed.author?.trim();
    if (!author || author === base) return base;
    return `${base} · ${author}`;
  });

  const modeLabel = createMemo(() => {
    if (props.parsed.mode === "plan") return t("timeline.collabModePlan");
    if (isDirectLocal()) return t("timeline.collabModeBuildDirect");
    if (props.parsed.workspace === "worktree") return t("timeline.collabModeBuildWorktree");
    if (props.parsed.mode === "build") return t("timeline.collabModeBuild");
    return "";
  });

  const showTitleRow = createMemo(() => {
    const title = props.parsed.title?.trim();
    if (!title) return false;
    const body = props.parsed.bodyText.trim();
    if (props.parsed.stage === "followup" && body) return false;
    return title !== body;
  });

  const lineCount = createMemo(() => {
    const text = props.parsed.bodyText || "";
    let count = 1;
    for (let i = 0; i < text.length; i++) {
      if (text.charCodeAt(i) === 10) count++;
    }
    return count;
  });

  const isCollapsible = createMemo(
    () =>
      props.parsed.sections.length === 0 &&
      (lineCount() > COLLAB_BODY_FOLD_LINES ||
        props.parsed.bodyText.length > COLLAB_BODY_FOLD_CHARS)
  );

  return (
    <div data-collab-role={props.parsed.role} class="space-y-2">
      {/* Compact collaborator identity & safety header */}
      <div class="flex flex-wrap items-center gap-1.5 pb-1.5 border-b border-[var(--border-subtle)]/75">
        <span
          class={`inline-flex items-center gap-1 rounded-md border px-1.5 py-0.5 text-[11px] font-semibold leading-none ${
            isPM()
              ? "border-[var(--violet-border)] bg-[var(--bg-panel)]/80 text-[var(--violet-text)]"
              : "border-[var(--warn-border)] bg-[var(--bg-panel)]/80 text-[var(--warn-text)]"
          }`}
        >
          <Show when={isPM()} fallback={<Bug size={11} class="shrink-0" />}>
            <Sparkles size={11} class="shrink-0" />
          </Show>
          <span class="max-w-[200px] truncate">{roleLabel()}</span>
        </span>

        <Show when={modeLabel()}>
          <span
            class={`inline-flex items-center gap-1 rounded-md border px-1.5 py-0.5 text-[10.5px] font-medium leading-none ${
              isDirectLocal()
                ? "border-[var(--warn-border)]/80 bg-[var(--warn-surface)]/80 text-[var(--warn-text)]"
                : "border-[var(--border-subtle)] bg-[var(--bg-panel)]/75 text-[var(--text-secondary)]"
            }`}
          >
            <Show
              when={isDirectLocal()}
              fallback={
                <Show when={props.parsed.workspace === "worktree"}>
                  <GitBranch size={10} class="shrink-0" />
                </Show>
              }
            >
              <AlertTriangle size={10} class="shrink-0" />
            </Show>
            <span>{modeLabel()}</span>
          </span>
        </Show>

        <Show when={props.parsed.deniedPaths.length > 0}>
          <span
            title={`${t("timeline.collabDeniedPathsLabel")}: ${props.parsed.deniedPaths.join(", ")}`}
            class="inline-flex items-center gap-1 rounded-md border border-[var(--border-subtle)] bg-[var(--bg-panel)]/70 px-1.5 py-0.5 font-mono text-[10px] text-[var(--text-muted)] leading-none"
          >
            <Lock size={9.5} class="shrink-0" />
            <span>{props.parsed.deniedPaths.length}</span>
          </span>
        </Show>

        <button
          type="button"
          onClick={toggleGuardrails}
          aria-expanded={guardrailsOpen()}
          class="ml-auto inline-flex items-center gap-1 rounded-md px-1.5 py-0.5 text-[10.5px] font-medium text-[var(--text-muted)] hover:bg-[var(--bg-panel)]/75 hover:text-[var(--text-primary)] transition-colors cursor-pointer select-none"
        >
          <ShieldCheck size={11} class="shrink-0 text-[var(--ok-text)]" />
          <span>{t("timeline.collabGuardrailsBtn")}</span>
          <ChevronDown
            size={10.5}
            class={`transition-transform duration-150 ${guardrailsOpen() ? "rotate-180" : ""}`}
          />
        </button>
      </div>

      {/* Collapsible guardrails, restricted paths, and legacy prompt details */}
      <Show when={guardrailsOpen()}>
        <div class="rounded-xl border border-[var(--border-subtle)]/85 bg-[var(--bg-panel)]/85 px-2.5 py-2 text-[11.5px] leading-relaxed text-[var(--text-secondary)] space-y-1.5">
          <div class="flex items-center gap-1.5 font-medium text-[var(--text-primary)]">
            <ShieldCheck size={12} class="shrink-0 text-[var(--ok-text)]" />
            <span>{t("timeline.collabGuardrailsTitle")}</span>
          </div>
          <p class="text-[var(--text-secondary)]">
            {t("timeline.collabGuardrailsDefaultSummary")}
          </p>
          <Show when={props.parsed.deniedPaths.length > 0}>
            <div class="flex flex-wrap items-center gap-1 pt-0.5">
              <span class="text-[11px] font-medium text-[var(--text-muted)]">
                {t("timeline.collabDeniedPathsLabel")}:
              </span>
              <For each={props.parsed.deniedPaths}>
                {(p) => (
                  <span class="rounded border border-[var(--border-subtle)] bg-[var(--bg-inset)] px-1.5 py-0.5 font-mono text-[10.5px] text-[var(--text-secondary)]">
                    {p}
                  </span>
                )}
              </For>
            </div>
          </Show>
          <Show when={props.parsed.developerFeedback}>
            <div class="rounded-lg border border-[var(--border-subtle)]/75 bg-[var(--bg-inset)]/70 px-2 py-1.5 space-y-0.5">
              <div class="text-[10.5px] font-medium text-[var(--text-muted)]">
                {t("timeline.collabDevFeedbackLabel")}
              </div>
              <div class="whitespace-pre-wrap break-words text-[11.5px] text-[var(--text-primary)] selectable-text">
                {props.parsed.developerFeedback}
              </div>
            </div>
          </Show>
          <Show when={props.parsed.guardrailNotice}>
            <div class="space-y-1 pt-0.5">
              <div class="text-[10.5px] font-medium text-[var(--text-muted)]">
                {t("timeline.collabLegacyPromptLabel")}
              </div>
              <div class="max-h-40 overflow-y-auto rounded-lg border border-[var(--border-subtle)]/70 bg-[var(--bg-inset)]/65 px-2 py-1.5 whitespace-pre-wrap break-words text-[11px] leading-relaxed text-[var(--text-muted)] selectable-text">
                {props.parsed.guardrailNotice}
              </div>
            </div>
          </Show>
        </div>
      </Show>

      {/* Optional compact linked title when different from message body */}
      <Show when={showTitleRow()}>
        <div class="inline-flex max-w-full items-center gap-1.5 rounded-lg border border-[var(--border-subtle)]/70 bg-[var(--bg-panel)]/65 px-2 py-1 text-[11.5px] text-[var(--text-secondary)]">
          <MessageSquareText size={11} class="shrink-0 text-[var(--text-muted)]" />
          <span class="shrink-0 text-[var(--text-muted)]">
            {t("timeline.collabPromptTitleLabel")}:
          </span>
          <span class="truncate font-medium text-[var(--text-primary)] selectable-text">
            {props.parsed.title}
          </span>
        </div>
      </Show>

      {/* Main collaborator message body */}
      <Show
        when={props.parsed.sections.length > 0}
        fallback={
          <Show when={Boolean(props.parsed.bodyText)}>
            <div>
              <div
                class={`whitespace-pre-wrap break-words leading-[1.65] text-[var(--text-primary)] selectable-text ${
                  isCollapsible() && !isBodyExpanded()
                    ? "max-h-[196px] overflow-hidden [mask-image:linear-gradient(to_bottom,black_65%,transparent_100%)]"
                    : ""
                }`}
                style={{ "font-size": "var(--app-chat-text-size)" }}
              >
                {props.parsed.bodyText}
              </div>
              <Show when={isCollapsible() && !props.forceExpand}>
                <button
                  type="button"
                  onClick={toggleBody}
                  class="mt-1 inline-flex items-center gap-1 rounded px-1.5 py-0.5 text-[12px] font-medium text-[var(--text-muted)] hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)] transition-colors cursor-pointer select-none"
                >
                  <Icon
                    name="chevron-down"
                    size="small"
                    class={`transition-transform duration-150 ${bodyExpanded() ? "rotate-180" : ""}`}
                  />
                  <span>
                    {bodyExpanded()
                      ? t("timeline.collapsePrompt")
                      : lineCount() > COLLAB_BODY_FOLD_LINES
                        ? t("timeline.expandPromptLines", { count: lineCount() })
                        : t("timeline.expandPrompt")}
                  </span>
                </button>
              </Show>
            </div>
          </Show>
        }
      >
        <div class="space-y-2">
          <For each={props.parsed.sections}>
            {(sec) => (
              <div class="space-y-0.5">
                <div class="text-[11px] font-semibold text-[var(--text-muted)]">
                  {sec.heading}
                </div>
                <div
                  class="whitespace-pre-wrap break-words leading-[1.6] text-[var(--text-primary)] selectable-text"
                  style={{ "font-size": "var(--app-chat-text-size)" }}
                >
                  {sec.content}
                </div>
              </div>
            )}
          </For>
        </div>
      </Show>
    </div>
  );
}
