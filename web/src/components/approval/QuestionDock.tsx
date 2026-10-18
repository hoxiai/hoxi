// web/src/components/approval/QuestionDock.tsx
import { createMemo, createSignal, For, Show } from "solid-js";
import { Check, HelpCircle, LoaderCircle, MessageSquarePlus, X } from "lucide-solid";
import type { QuestionInfo, QuestionRequest } from "../../services/sse-types";
import { hoxiAPI } from "../../services/api";
import { sessionDirectory, setWorkspace } from "../../store/workspace";
import { locale, t } from "../../i18n";

/**
 * Ported from kilocode QuestionDock.tsx:
 * Renders interactive single/multi-choice questions (`question.asked`)
 * inline above the Composer with multi-tab navigation and custom write-in support.
 */
export default function QuestionDock(props: { request: QuestionRequest }) {
  const questions = createMemo<QuestionInfo[]>(() => props.request.questions ?? []);
  const [activeTab, setActiveTab] = createSignal(0);
  const [selectedByIdx, setSelectedByIdx] = createSignal<Record<number, string[]>>({});
  const [customByIdx, setCustomByIdx] = createSignal<Record<number, string>>({});
  const [showCustomByIdx, setShowCustomByIdx] = createSignal<Record<number, boolean>>({});
  const [sending, setSending] = createSignal(false);
  const [rejecting, setRejecting] = createSignal(false);
  const [error, setError] = createSignal<string | null>(null);

  const currentQuestion = createMemo<QuestionInfo | undefined>(
    () => questions()[activeTab()]
  );

  const selectedFor = (idx: number): string[] => selectedByIdx()[idx] ?? [];
  const customFor = (idx: number): string => customByIdx()[idx] ?? "";
  const isCustomOpen = (idx: number): boolean => !!showCustomByIdx()[idx];

  const toggleOption = (idx: number, label: string, multiple?: boolean) => {
    setSelectedByIdx((prev) => {
      const cur = prev[idx] ?? [];
      if (multiple) {
        const next = cur.includes(label)
          ? cur.filter((item) => item !== label)
          : [...cur, label];
        return { ...prev, [idx]: next };
      }
      return { ...prev, [idx]: cur.includes(label) ? [] : [label] };
    });
  };

  const buildAnswers = (): string[][] => {
    return questions().map((_, idx) => {
      const picks = [...selectedFor(idx)];
      const customText = customFor(idx).trim();
      if (customText && !picks.includes(customText)) {
        picks.push(customText);
      }
      return picks;
    });
  };

  const canSubmit = createMemo(() => {
    const qs = questions();
    if (qs.length === 0) return false;
    return buildAnswers().every((ans) => ans.length > 0);
  });

  const removeFromStore = () => {
    setWorkspace("pendingQuestions", (prev) =>
      prev.filter((q) => q.id !== props.request.id)
    );
  };

  const onSubmit = async () => {
    if (sending() || rejecting() || !canSubmit()) return;
    setSending(true);
    setError(null);
    const dir = sessionDirectory(props.request.sessionID);
    try {
      await hoxiAPI.replyQuestion(props.request.id, buildAnswers(), dir);
      removeFromStore();
    } catch (err) {
      console.error("replyQuestion failed:", err);
      setError(String(err));
      setSending(false);
    }
  };

  const onDismiss = async () => {
    if (sending() || rejecting()) return;
    setRejecting(true);
    setError(null);
    const dir = sessionDirectory(props.request.sessionID);
    try {
      await hoxiAPI.rejectQuestion(props.request.id, dir);
      removeFromStore();
    } catch (err) {
      console.error("rejectQuestion failed:", err);
      setError(String(err));
      setRejecting(false);
    }
  };

  // Auto-advance to next unanswered tab on single-choice click when multiple questions exist
  const onOptionClick = (idx: number, label: string, q: QuestionInfo) => {
    toggleOption(idx, label, q.multiple);
    if (!q.multiple && questions().length > 1 && idx < questions().length - 1) {
      setTimeout(() => setActiveTab(idx + 1), 140);
    }
  };

  return (
    <div class="rounded-xl border border-[var(--accent-border)] bg-[var(--bg-elevated)] overflow-hidden shadow-md">
      {/* Header */}
      <div class="flex items-center justify-between gap-2 px-3.5 py-2 border-b border-[var(--border-subtle)] bg-[var(--accent-surface)]/45">
        <div class="flex items-center gap-2 min-w-0">
          <HelpCircle size={14} class="text-[var(--accent-primary)] shrink-0" />
          <span class="text-[12px] font-semibold text-[var(--text-primary)] truncate">
            {t("approval.questionTitle")}
          </span>
        </div>

        {/* Multi-question step tabs */}
        <Show when={questions().length > 1}>
          <div class="flex items-center gap-1">
            <For each={questions()}>
              {(q, idx) => {
                const answered = () =>
                  selectedFor(idx()).length > 0 || customFor(idx()).trim().length > 0;
                return (
                  <button
                    type="button"
                    onClick={() => setActiveTab(idx())}
                    class={`px-2 py-0.5 rounded-md text-[11px] font-mono transition-colors cursor-pointer ${
                      activeTab() === idx()
                        ? "bg-[var(--accent-primary)] text-[var(--accent-foreground)] font-semibold"
                        : answered()
                          ? "bg-[var(--ok-surface)] text-[var(--ok-text)]"
                          : "bg-[var(--bg-panel)] text-[var(--text-muted)] hover:text-[var(--text-primary)]"
                    }`}
                  >
                    {idx() + 1}. {q.header || `Q${idx() + 1}`}
                  </button>
                );
              }}
            </For>
          </div>
        </Show>

        <button
          type="button"
          onClick={() => void onDismiss()}
          disabled={sending() || rejecting()}
          class="text-[var(--text-muted)] hover:text-[var(--text-primary)] p-0.5 rounded cursor-pointer"
          title={t("approval.skipQuestionTooltip")}
        >
          <X size={13} />
        </button>
      </div>

      {/* Body */}
      <Show when={currentQuestion()}>
        {(q) => {
          const idx = () => activeTab();
          const picks = () => selectedFor(idx());
          return (
            <div class="px-3.5 py-3 space-y-2.5">
              <div class="flex items-baseline justify-between gap-2">
                <p class="text-[12.5px] font-medium text-[var(--text-primary)] leading-snug">
                  {q().question}
                </p>
                <Show when={q().multiple}>
                  <span class="shrink-0 text-[10.5px] text-[var(--text-muted)]">
                    {t("approval.multiSelect")}
                  </span>
                </Show>
              </div>

              {/* Options list */}
              <div class="grid grid-cols-1 gap-1.5 max-h-[210px] overflow-y-auto">
                <For each={q().options}>
                  {(opt) => {
                    const isPicked = () => picks().includes(opt.label);
                    return (
                      <button
                        type="button"
                        onClick={() => onOptionClick(idx(), opt.label, q())}
                        class={`flex items-start gap-2.5 rounded-lg border px-3 py-2 text-left transition-all cursor-pointer ${
                          isPicked()
                            ? "border-[var(--accent-primary)] bg-[var(--accent-surface)]/70 text-[var(--text-primary)]"
                            : "border-[var(--border-subtle)] bg-[var(--bg-panel)] hover:bg-[var(--bg-hover)] text-[var(--text-secondary)]"
                        }`}
                      >
                        <span
                          class={`mt-0.5 flex h-4 w-4 shrink-0 items-center justify-center rounded ${
                            q().multiple ? "rounded-sm" : "rounded-full"
                          } border ${
                            isPicked()
                              ? "border-[var(--accent-primary)] bg-[var(--accent-primary)] text-[var(--accent-foreground)]"
                              : "border-[var(--border-strong)] bg-transparent"
                          }`}
                        >
                          <Show when={isPicked()}>
                            <Check size={10} strokeWidth={3} />
                          </Show>
                        </span>
                        <div class="min-w-0 flex-1">
                          <div class="text-[12px] font-medium text-[var(--text-primary)]">
                            {opt.label}
                          </div>
                          <Show when={opt.description}>
                            <div class="text-[11px] text-[var(--text-muted)] leading-snug mt-0.5">
                              {opt.description}
                            </div>
                          </Show>
                        </div>
                      </button>
                    );
                  }}
                </For>
              </div>

              {/* Custom write-in answer (enabled by default unless custom === false) */}
              <Show when={q().custom !== false}>
                <Show
                  when={isCustomOpen(idx()) || customFor(idx()).length > 0}
                  fallback={
                    <button
                      type="button"
                      onClick={() =>
                        setShowCustomByIdx((prev) => ({ ...prev, [idx()]: true }))
                      }
                      class="inline-flex items-center gap-1.5 text-[11.5px] text-[var(--accent-primary)] hover:underline cursor-pointer"
                    >
                      <MessageSquarePlus size={12} />
                      <span>
                        {t("approval.customAnswerLabel")}
                      </span>
                    </button>
                  }
                >
                  <input
                    type="text"
                    value={customFor(idx())}
                    onInput={(ev) =>
                      setCustomByIdx((prev) => ({
                        ...prev,
                        [idx()]: ev.currentTarget.value,
                      }))
                    }
                    onKeyDown={(ev) => {
                      if (ev.key === "Enter" && !ev.isComposing && canSubmit()) {
                        ev.preventDefault();
                        void onSubmit();
                      }
                    }}
                    placeholder={t("approval.customAnswerPlaceholder")}
                    class="w-full rounded-lg border border-[var(--border-strong)] bg-[var(--bg-panel)] px-2.5 py-1.5 text-[12px] text-[var(--text-primary)] placeholder:text-[var(--text-faint)] outline-none"
                  />
                </Show>
              </Show>

              <Show when={error()}>
                <div class="text-[11px] text-[var(--danger-text)] font-mono">{error()}</div>
              </Show>
            </div>
          );
        }}
      </Show>

      {/* Footer Actions */}
      <div class="flex items-center justify-between gap-2 px-3.5 py-2 border-t border-[var(--border-subtle)] bg-[var(--bg-panel)]/60">
        <span class="text-[11px] text-[var(--text-muted)]">
          {questions().length > 1
            ? `${activeTab() + 1} / ${questions().length}`
            : t("approval.confirmHint")}
        </span>

        <div class="flex items-center gap-2">
          <button
            type="button"
            onClick={() => void onDismiss()}
            disabled={sending() || rejecting()}
            class="rounded-lg px-2.5 py-1 text-[11.5px] text-[var(--text-secondary)] hover:bg-[var(--bg-hover)] disabled:opacity-50 cursor-pointer"
          >
            {t("approval.skipQuestion")}
          </button>
          <button
            type="button"
            onClick={() => void onSubmit()}
            disabled={!canSubmit() || sending() || rejecting()}
            class="inline-flex items-center gap-1.5 rounded-lg bg-[var(--accent-primary)] px-3 py-1 text-[11.5px] font-medium text-[var(--accent-foreground)] hover:opacity-90 disabled:opacity-40 transition-opacity cursor-pointer"
          >
            <Show when={!sending()} fallback={<LoaderCircle size={12} class="animate-spin" />}>
              <Check size={12} />
            </Show>
            <span>{t("approval.submitAnswer")}</span>
          </button>
        </div>
      </div>
    </div>
  );
}
