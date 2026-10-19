// web/src/components/timeline/RunnerSubagentList.tsx
import { For, Show } from "solid-js";
import { t } from "../../i18n";
import { Icon, Spinner } from "../icons";
import type { RunnerSubagentProgress } from "./tool-card-state";

/** Expanded body of an Antigravity `task` card: one row per dispatched subagent. */
export default function RunnerSubagentList(props: {
  progress: RunnerSubagentProgress;
  running: boolean;
}) {
  return (
    <div class="border-t border-[var(--border-subtle)] bg-[var(--bg-elevated)]/45 p-3 space-y-2.5">
      <For each={props.progress.items}>
        {(item) => (
          <div class="text-[12px] leading-snug">
            <div class="flex items-center gap-2 min-w-0">
              <Show
                when={item.status === "running" && props.running}
                fallback={
                  <span class="shrink-0 text-[var(--text-muted)] inline-flex items-center">
                    <Icon name={item.status === "reported" ? "check-small" : "task"} size="small" />
                  </span>
                }
              >
                <span class="shrink-0 text-[var(--accent-primary)] inline-flex items-center">
                  <Spinner class="w-3 h-3" />
                </span>
              </Show>
              <span class="font-medium text-[var(--text-primary)] truncate">{item.role}</span>
              <Show when={item.type}>
                <span class="font-mono text-[11px] text-[var(--text-faint)] shrink-0">{item.type}</span>
              </Show>
              <span class="ml-auto shrink-0 flex items-center gap-2 font-mono text-[11px] text-[var(--text-faint)]">
                <Show when={item.status === "running" && item.currentTool}>
                  <span class="truncate max-w-[180px]">{item.currentTool}</span>
                </Show>
                <Show when={item.steps > 0}>
                  <span>{t("toolCard.subagentSteps", { count: item.steps })}</span>
                </Show>
                <span>
                  {item.status === "reported"
                    ? t("toolCard.subagentReported")
                    : t("toolCard.subagentWorking")}
                </span>
              </span>
            </div>
            <Show when={item.prompt}>
              <p
                class="mt-1 pl-5 text-[11.5px] text-[var(--text-muted)] line-clamp-2 selectable-text"
                title={item.prompt}
              >
                {item.prompt}
              </p>
            </Show>
          </div>
        )}
      </For>
    </div>
  );
}
