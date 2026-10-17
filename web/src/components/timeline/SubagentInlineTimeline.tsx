// web/src/components/timeline/SubagentInlineTimeline.tsx
import { createEffect, createMemo, For, Show } from "solid-js";
import type { MessagePart } from "../../services/sse-types";
import {
  loadSessionMessages,
  openFileInInspector,
  selectSession,
  workspace,
} from "../../store/workspace";
import { FileIcon, Icon, Spinner } from "../icons";
import {
  extractCommand,
  extractDescription,
  extractTargetFile,
  getFilename,
  kiloToolIconName,
  type ToolPart,
} from "./tool-card-helpers";

/**
 * Ported from kilocode TaskToolExpanded.tsx:
 * Renders a live compact scrollable list of a child subagent's tool calls
 * directly inside the parent `task` ToolCard.
 */
export default function SubagentInlineTimeline(props: {
  childSessionID: string;
  parentRunning: boolean;
}) {
  // Fetch child session messages on mount and when parent finishes
  createEffect(() => {
    const id = props.childSessionID;
    const running = props.parentRunning;
    if (!id) return;
    void loadSessionMessages(id);
    if (running) {
      const timer = setInterval(() => {
        void loadSessionMessages(id);
      }, 1500);
      return () => clearInterval(timer);
    }
  });

  const childToolParts = createMemo<ToolPart[]>(() => {
    const turns = workspace.messagesBySession[props.childSessionID] ?? [];
    const tools: ToolPart[] = [];
    for (const turn of turns) {
      for (const part of turn.parts ?? []) {
        if (part.type === "tool") {
          tools.push(part as ToolPart);
        }
      }
    }
    return tools.slice(-12);
  });

  return (
    <Show when={childToolParts().length > 0}>
      <div class="border-t border-[var(--border-subtle)] bg-[var(--bg-inset)]/65 px-3 py-2 space-y-1 max-h-44 overflow-y-auto">
        <div class="flex items-center justify-between text-[10.5px] font-semibold uppercase tracking-wider text-[var(--text-faint)] mb-1">
          <span>Subagent Live Activity ({childToolParts().length})</span>
          <button
            type="button"
            onClick={(e) => {
              e.stopPropagation();
              void selectSession(props.childSessionID);
            }}
            class="text-[var(--accent-primary)] hover:underline font-normal normal-case cursor-pointer"
          >
            Open child session →
          </button>
        </div>

        <For each={childToolParts()}>
          {(toolPart) => {
            const running = () =>
              toolPart.state.status === "pending" || toolPart.state.status === "running";
            const file = () => extractTargetFile(toolPart);
            const desc = () =>
              extractDescription(toolPart) || extractCommand(toolPart) || toolPart.tool;

            return (
              <div class="flex items-center justify-between gap-2 rounded-md bg-[var(--bg-panel)]/80 px-2 py-1 text-[11.5px]">
                <div class="flex items-center gap-1.5 min-w-0 flex-1">
                  <Show
                    when={!running()}
                    fallback={
                      <span class="text-[var(--accent-primary)] shrink-0 inline-flex">
                        <Spinner class="w-3 h-3" />
                      </span>
                    }
                  >
                    <Icon
                      name={
                        toolPart.state.status === "error"
                          ? "circle-ban-sign"
                          : kiloToolIconName(toolPart.tool)
                      }
                      size="small"
                      class={
                        toolPart.state.status === "error"
                          ? "text-[var(--danger-text)] shrink-0"
                          : "text-[var(--text-muted)] shrink-0"
                      }
                    />
                  </Show>

                  <span class="font-mono text-[11px] text-[var(--text-secondary)] shrink-0">
                    {toolPart.tool}
                  </span>

                  <Show
                    when={file()}
                    fallback={
                      <span class="text-[11px] text-[var(--text-muted)] truncate">
                        {desc()}
                      </span>
                    }
                  >
                    {(f) => (
                      <button
                        type="button"
                        onClick={(e) => {
                          e.stopPropagation();
                          openFileInInspector(f());
                        }}
                        class="inline-flex items-center gap-1 font-mono text-[11px] text-[var(--text-primary)] hover:text-[var(--accent-primary)] truncate cursor-pointer"
                      >
                        <FileIcon node={{ path: f(), type: "file" }} class="w-3 h-3 shrink-0" />
                        <span class="truncate">{getFilename(f())}</span>
                      </button>
                    )}
                  </Show>
                </div>

                <span
                  class={`font-mono text-[10px] shrink-0 ${
                    toolPart.state.status === "completed"
                      ? "text-[var(--ok-text)]"
                      : toolPart.state.status === "error"
                        ? "text-[var(--danger-text)]"
                        : "text-[var(--accent-primary)]"
                  }`}
                >
                  {toolPart.state.status}
                </span>
              </div>
            );
          }}
        </For>
      </div>
    </Show>
  );
}
