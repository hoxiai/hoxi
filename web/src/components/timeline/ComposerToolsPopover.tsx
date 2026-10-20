// web/src/components/timeline/ComposerToolsPopover.tsx
import { createEffect, createSignal, onCleanup, Show } from "solid-js";
import { Paperclip, Plus, Search, Sparkles, Workflow } from "lucide-solid";
import { t } from "../../i18n";

export default function ComposerToolsPopover(props: {
  workspaceReady: boolean;
  skillActive?: boolean;
  onAttach: () => void;
  onOpenSkills: () => void;
  onOpenCodeSearch: () => void;
  onOpenConvoy: () => void;
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

      <Show when={open()}>
        <div
          ref={menuRef}
          class="absolute bottom-full left-0 mb-2 w-[232px] rounded-2xl border border-[var(--border-strong)] bg-[var(--bg-elevated)] p-1.5 shadow-2xl z-50 animate-in fade-in zoom-in-95 duration-100"
        >
          <div class="px-2.5 py-1.5 border-b border-[var(--border-subtle)] mb-1">
            <span class="text-[10px] font-semibold uppercase tracking-wider text-[var(--text-faint)]">
              {t("timeline.composerTools")}
            </span>
          </div>

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
          </div>
        </div>
      </Show>
    </div>
  );
}
