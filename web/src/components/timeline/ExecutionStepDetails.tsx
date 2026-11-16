import { createEffect, createMemo, createSignal, For, Show, untrack } from "solid-js";
import type { ToolPart } from "../../services/sse-types";
import { t } from "../../i18n";
import ToolCard from "./ToolCard";
import { formatToolDisplayName } from "./tool-card-helpers";

export default function ExecutionStepDetails(props: { steps: ToolPart[] }) {
  const [selectedID, setSelectedID] = createSignal(
    (props.steps.findLast((part) => part.state.status === "running" || part.state.status === "pending")
      ?? props.steps[props.steps.length - 1])?.id,
  );
  const selected = createMemo(() => props.steps.find((part) => part.id === selectedID()));
  createEffect(() => {
    const firstID = props.steps[0]?.id;
    if (!firstID) return;
    untrack(() => {
      if (!selected()) setSelectedID(props.steps[props.steps.length - 1]?.id);
    });
  });

  return (
    <section aria-label={t("timeline.executionStepDetails")} class="mt-3 border-t border-[var(--accent-border)] pt-3">
      <div class="flex max-h-36 flex-wrap gap-1.5 overflow-y-auto pb-2">
        <For each={props.steps}>
          {(part, index) => (
            <button
              type="button"
              aria-pressed={selectedID() === part.id}
              onClick={() => setSelectedID(part.id)}
              class={`min-w-0 max-w-full rounded-md border px-2 py-1 text-[11px] cursor-pointer focus-visible:outline focus-visible:outline-[var(--accent-primary)] ${
                selectedID() === part.id
                  ? "border-[var(--accent-border)] bg-[var(--accent-surface)] text-[var(--accent-text)]"
                  : "border-[var(--border-subtle)] text-[var(--text-secondary)] hover:bg-[var(--bg-hover)]"
              }`}
            >
              <span class="block truncate">
                {t("timeline.executionStepNumber", { count: index() + 1 })}
                {" · "}{formatToolDisplayName(part.tool)}
              </span>
            </button>
          )}
        </For>
      </div>
      <Show when={selectedID()} keyed>
        {(_id) => (
          <Show when={selected()}>
            <div class="max-h-[min(28rem,55vh)] overflow-auto px-2">
              <ToolCard part={selected()!} defaultExpanded />
            </div>
          </Show>
        )}
      </Show>
    </section>
  );
}
