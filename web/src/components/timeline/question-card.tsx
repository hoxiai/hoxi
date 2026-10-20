// web/src/components/timeline/question-card.tsx
import { createMemo, For, Show } from "solid-js";
import { Check, X } from "lucide-solid";
import type { ToolPart } from "../../services/sse-types";
import {
  findQuestionReply,
  resolveQuestionItems,
  type QuestionItemView,
} from "../../store/question-answers";
import { t } from "../../i18n";

/**
 * Dedicated `question` tool body: renders the asked questions with the options
 * list and highlights the answers the user actually picked (or a skipped state),
 * instead of dumping raw input/output JSON.
 */
export default function QuestionToolBody(props: { part: ToolPart }) {
  const reply = createMemo(() => findQuestionReply(props.part.callID));
  const items = createMemo(() => resolveQuestionItems(props.part, reply()));
  const rejected = createMemo(() => Boolean(reply()?.rejected));

  return (
    <div class="p-3 space-y-2.5">
      <Show when={rejected()}>
        <div class="inline-flex items-center gap-1.5 text-[12px] text-[var(--text-muted)]">
          <X size={12} class="text-[var(--warn-text)]" />
          <span>{t("toolCard.questionSkipped")}</span>
        </div>
      </Show>
      <For each={items()}>{(item) => <QuestionRow item={item} />}</For>
    </div>
  );
}

function QuestionRow(props: { item: QuestionItemView }) {
  return (
    <div class="rounded-lg border border-[var(--border-subtle)] bg-[var(--bg-panel)]/60 px-3 py-2.5 space-y-2">
      <div class="flex items-baseline justify-between gap-2">
        <p class="text-[13px] font-medium text-[var(--text-primary)] leading-snug">
          {props.item.question || props.item.header}
        </p>
        <Show when={props.item.multiple}>
          <span class="shrink-0 text-[11px] text-[var(--text-muted)]">
            {t("toolCard.questionMultiSelect")}
          </span>
        </Show>
      </div>

      <Show
        when={props.item.options.length > 0 || props.item.customAnswers.length > 0}
        fallback={
          <div class="text-[12px] text-[var(--text-muted)] italic">
            {t("toolCard.questionUnanswered")}
          </div>
        }
      >
        <div class="space-y-1">
          <For each={props.item.options}>
            {(opt) => (
              <div
                class={`flex items-start gap-2 rounded-md border px-2.5 py-1.5 ${
                  opt.selected
                    ? "border-[var(--accent-primary)] bg-[var(--accent-surface)]/70"
                    : "border-transparent"
                }`}
              >
                <span
                  class={`mt-0.5 flex h-3.5 w-3.5 shrink-0 items-center justify-center rounded ${
                    props.item.multiple ? "rounded-sm" : "rounded-full"
                  } border ${
                    opt.selected
                      ? "border-[var(--accent-primary)] bg-[var(--accent-primary)] text-[var(--accent-foreground)]"
                      : "border-[var(--border-strong)]"
                  }`}
                >
                  <Show when={opt.selected}>
                    <Check size={9} strokeWidth={3} />
                  </Show>
                </span>
                <div class="min-w-0 flex-1">
                  <span
                    class={`text-[12px] ${
                      opt.selected
                        ? "font-medium text-[var(--text-primary)]"
                        : "text-[var(--text-secondary)]"
                    }`}
                  >
                    {opt.label}
                  </span>
                  <Show when={opt.description}>
                    <div class="text-[11px] text-[var(--text-muted)] leading-snug mt-0.5">
                      {opt.description}
                    </div>
                  </Show>
                </div>
                <Show when={opt.selected}>
                  <Check
                    size={12}
                    class="mt-0.5 shrink-0 text-[var(--accent-primary)]"
                    aria-label={t("toolCard.questionSelectedLabel")}
                  />                </Show>
              </div>
            )}
          </For>
          <For each={props.item.customAnswers}>
            {(answer) => (
              <div class="flex items-start gap-2 rounded-md border border-[var(--accent-primary)] bg-[var(--accent-surface)]/70 px-2.5 py-1.5">
                <span class="mt-0.5 flex h-3.5 w-3.5 shrink-0 items-center justify-center rounded-full border border-[var(--accent-primary)] bg-[var(--accent-primary)] text-[var(--accent-foreground)]">
                  <Check size={9} strokeWidth={3} />
                </span>
                <span class="min-w-0 flex-1 text-[12px] font-medium text-[var(--text-primary)] break-words">
                  {answer}
                </span>
              </div>
            )}
          </For>
        </div>
        <Show when={!props.item.answered}>
          <div class="text-[12px] text-[var(--text-muted)] italic">
            {t("toolCard.questionUnanswered")}
          </div>
        </Show>
      </Show>
    </div>
  );
}
