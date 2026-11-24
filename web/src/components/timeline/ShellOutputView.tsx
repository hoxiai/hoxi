// web/src/components/timeline/ShellOutputView.tsx
// Renders Shell ToolCard output with ANSI SGR color styling and clickable
// workspace `path:line` error/stack references that jump straight into Inspector.
import { createMemo, For, Show } from "solid-js";
import { t } from "../../i18n";
import { isTesterPortalMode, openFileInInspector } from "../../store/workspace";
import { parseShellOutputRuns, type AnsiStyle } from "./ansi-output";

const MAX_RICH_RENDER_CHARS = 32 * 1024;

function styleObj(style?: AnsiStyle) {
  if (!style) return undefined;
  return {
    color: style.fg,
    "background-color": style.bg,
    "font-weight": style.bold ? "600" : undefined,
    opacity: style.dim ? "0.68" : undefined,
    "font-style": style.italic ? "italic" : undefined,
    "text-decoration": style.underline ? "underline" : undefined,
  };
}

export default function ShellOutputView(props: {
  rawOutput: string;
  plainOutput: string;
  setRef?: (el: HTMLPreElement | undefined) => void;
}) {
  const useRich = createMemo(() => (props.rawOutput || "").length <= MAX_RICH_RENDER_CHARS);
  const runs = createMemo(() =>
    useRich() ? parseShellOutputRuns(props.rawOutput) : []
  );

  return (
    <pre
      ref={props.setRef}
      class="max-h-[260px] overflow-auto px-3.5 py-2.5 text-[12px] leading-[1.55] text-[#e6edf3] whitespace-pre-wrap break-all selectable-text"
    >
      <Show when={useRich()} fallback={props.plainOutput}>
        <For each={runs()}>
          {(run) => (
            <Show
              when={run.fileRef && !isTesterPortalMode()}
              fallback={
                <Show when={run.style} fallback={run.text}>
                  <span style={styleObj(run.style)}>{run.text}</span>
                </Show>
              }
            >
              <button
                type="button"
                onClick={(ev) => {
                  ev.stopPropagation();
                  openFileInInspector(run.fileRef!.path, run.fileRef!.line);
                }}
                title={t("toolCard.openFileLineTooltip", {
                  file: run.fileRef!.path,
                  line: run.fileRef!.line,
                })}
                style={styleObj(run.style)}
                class="inline font-mono text-[#79c0ff] underline decoration-[#79c0ff]/45 underline-offset-2 hover:text-[#a5d6ff] hover:decoration-[#a5d6ff] cursor-pointer"
              >
                {run.text}
              </button>
            </Show>
          )}
        </For>
      </Show>
    </pre>
  );
}
