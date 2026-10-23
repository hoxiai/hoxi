// web/src/components/timeline/ComposerToolsPopover.tsx
import { createEffect, createSignal, onCleanup, Show } from "solid-js";
import { Presence } from "../motion/Presence";
import {
  GitBranch,
  LoaderCircle,
  Paperclip,
  Plus,
  Search,
  Sparkles,
  Workflow,
} from "lucide-solid";
import { t } from "../../i18n";
import { unattendedGapFor } from "../../store/workspace";

export default function ComposerToolsPopover(props: {
  workspaceReady: boolean;
  skillActive?: boolean;
  sandboxMode: "local" | "worktree";
  runtime: string;
  canEnhance: boolean;
  enhancing: boolean;
  onAttach: () => void;
  onOpenSkills: () => void;
  onOpenCodeSearch: () => void;
  onOpenConvoy: () => void;
  onSwitchSandbox: () => void;
  onEnhance: () => void;
}) {
  const [open, setOpen] = createSignal(false);
  let buttonRef: HTMLButtonElement | undefined;
  let menuRef: HTMLDivElement | undefined;

  createEffect(() => {
    if (!open()) return;
    const handlePointerDown = (ev: MouseEvent) => {
      const target = ev.target as Node;
      if (
        menuRef && !menuRef.contains(target) &&
        buttonRef && !buttonRef.contains(target)
      ) {
        setOpen(false);
      }
    };
    const handleKeyDown = (ev: KeyboardEvent) => {
      if (ev.key === "Escape") setOpen(false);
    };
    window.addEventListener("pointerdown", handlePointerDown);
    window.addEventListener("keydown", handleKeyDown);
    onCleanup(() => {
      window.removeEventListener("pointerdown", handlePointerDown);
      window.removeEventListener("keydown", handleKeyDown);
    });
  });

  const run = (action: () => void) => {
    setOpen(false);
    action();
  };

  return (
    <div class="relative inline-block">
      <button
        ref={buttonRef}
        type="button"
        onClick={() => setOpen((v) => !v)}
        aria-expanded={open()}
        class={`inline-flex items-center justify-center rounded-lg p-1.5 transition-colors cursor-pointer ${
          open()
            ? "bg-[var(--accent-surface)] text-[var(--accent-primary)]"
            : "text-[var(--text-muted)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-hover)]"
        }`}
        title={t("timeline.composerToolsTooltip")}
      >
        <Plus
          size={15}
          class={`transition-transform duration-200 ${open() ? "rotate-45" : ""}`}
        />
      </button>

      <Presence when={open()}>
        {(state) => (
        <div
          ref={menuRef}
          data-state={state()}
          class="hoxi-popover absolute bottom-full left-0 mb-2 w-[232px] rounded-2xl border border-[var(--border-strong)] bg-[var(--bg-elevated)] p-1.5 shadow-xl z-50"
          style={{ "--hoxi-origin": "bottom left" }}
        >
          <div class="space-y-0.5">
            <button
              type="button"
              onClick={() => run(props.onAttach)}
              class="w-full flex items-center gap-2.5 rounded-xl px-2.5 py-2 text-left text-[12px] text-[var(--text-secondary)] hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)] transition-colors cursor-pointer"
            >
              <Paperclip size={14} class="shrink-0" />
              <span class="flex-1 min-w-0 truncate">{t("timeline.attachFiles")}</span>
            </button>

            <button
              type="button"
              onClick={() => run(props.onOpenSkills)}
              class="w-full flex items-center gap-2.5 rounded-xl px-2.5 py-2 text-left text-[12px] text-[var(--text-secondary)] hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)] transition-colors cursor-pointer"
            >
              <Sparkles
                size={14}
                class={`shrink-0 ${props.skillActive ? "text-[var(--accent-primary)]" : ""}`}
              />
              <span class="flex-1 min-w-0 truncate">{t("skills.browseSkills")}</span>
            </button>

            <button
              type="button"
              disabled={!props.workspaceReady}
              onClick={() => run(props.onOpenCodeSearch)}
              class="w-full flex items-center gap-2.5 rounded-xl px-2.5 py-2 text-left text-[12px] text-[var(--text-secondary)] hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)] disabled:opacity-40 disabled:cursor-not-allowed transition-colors cursor-pointer"
            >
              <Search size={14} class="shrink-0" />
              <span class="flex-1 min-w-0 truncate">{t("timeline.searchCodebase")}</span>
            </button>

            <button
              type="button"
              disabled={!props.workspaceReady}
              onClick={() => run(props.onOpenConvoy)}
              class="w-full flex items-center gap-2.5 rounded-xl px-2.5 py-2 text-left text-[12px] text-[var(--text-secondary)] hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)] disabled:opacity-40 disabled:cursor-not-allowed transition-colors cursor-pointer"
            >
              <Workflow size={14} class="shrink-0" />
              <span class="flex-1 min-w-0 truncate">{t("timeline.multiAgentConvoy")}</span>
            </button>

            <div class="my-1 border-t border-[var(--border-subtle)]" />

            <button
              type="button"
              disabled={!props.canEnhance || props.enhancing}
              onClick={() => run(props.onEnhance)}
              class="w-full flex items-center gap-2.5 rounded-xl px-2.5 py-2 text-left text-[12px] text-[var(--text-secondary)] hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)] disabled:opacity-40 disabled:cursor-not-allowed transition-colors cursor-pointer"
            >
              <Show
                when={!props.enhancing}
                fallback={<LoaderCircle size={14} class="shrink-0 animate-spin" />}
              >
                <Sparkles size={14} class="shrink-0" />
              </Show>
              <span class="flex-1 min-w-0 truncate">
                {props.enhancing ? t("timeline.enhancingPrompt") : t("timeline.enhancePrompt")}
              </span>
            </button>

            <Show when={unattendedGapFor(props.runtime, props.sandboxMode)}>
              {(gap) => (
                <p class="px-2.5 py-1.5 text-[11px] text-[var(--danger-text)]">
                  {gap() === "runtime" ? t("approval.unattendedRuntimeUnsupported") : t("approval.unattendedNotIsolated")}
                </p>
              )}
            </Show>
            <Show when={props.sandboxMode === "local"}>
              <button
                type="button"
                disabled={!props.workspaceReady}
                onClick={() => run(props.onSwitchSandbox)}
                class="w-full flex items-center gap-2.5 rounded-xl px-2.5 py-2 text-left text-[12px] text-[var(--text-secondary)] hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)] disabled:opacity-40 disabled:cursor-not-allowed transition-colors cursor-pointer"
              >
                <GitBranch size={14} class="shrink-0" />
                <span class="flex-1 min-w-0 truncate">{t("timeline.sandboxEnableWorktree")}</span>
              </button>
            </Show>
          </div>
        </div>
        )}
      </Presence>
    </div>
  );
}
