// web/src/components/timeline/ModeSwitcher.tsx
import { createEffect, createMemo, createSignal, For, onCleanup, Show } from "solid-js";
import {
  Bot,
  Check,
  ChevronDown,
  Compass,
  Hammer,
  ShieldAlert,
  Sparkles,
  Wrench,
} from "lucide-solid";
import { setActiveAgent, workspace } from "../../store/workspace";
import type { AgentInfo } from "../../services/api";
import { t } from "../../i18n";

function formatAgentLabel(agentOrName: AgentInfo | string): string {
  const name = typeof agentOrName === "string" ? agentOrName : agentOrName.name;
  if (!name) return "Agent";
  const lower = name.toLowerCase();
  if (lower === "build") return t("timeline.buildMode");
  if (lower === "plan") return t("timeline.planMode");
  return name
    .split(/[-_]/)
    .map((w) => w.charAt(0).toUpperCase() + w.slice(1))
    .join(" ");
}

function modeIcon(name: string) {
  const lower = name.toLowerCase();
  if (lower === "build") return <Hammer size={12} class="shrink-0" />;
  if (lower === "plan") return <Compass size={12} class="shrink-0" />;
  if (lower === "debugger" || lower === "fix") return <Wrench size={12} class="shrink-0" />;
  return <Sparkles size={12} class="shrink-0" />;
}

function modeBadge(name: string): string | null {
  const lower = name.toLowerCase();
  if (lower === "build") return t("timeline.buildMode");
  if (lower === "plan") return t("timeline.planMode");
  return null;
}

export default function ModeSwitcher() {
  const [open, setOpen] = createSignal(false);
  let buttonRef: HTMLButtonElement | undefined;
  let menuRef: HTMLDivElement | undefined;

  const agents = createMemo(() => workspace.agents || []);
  const currentMode = () => workspace.activeMode || "build";
  const currentAgent = createMemo(() =>
    agents().find((a) => a.name.toLowerCase() === currentMode().toLowerCase())
  );

  // Click outside listener
  createEffect(() => {
    if (!open()) return;
    const handleClick = (e: MouseEvent) => {
      const target = e.target as Node;
      if (
        menuRef &&
        !menuRef.contains(target) &&
        buttonRef &&
        !buttonRef.contains(target)
      ) {
        setOpen(false);
      }
    };
    window.addEventListener("pointerdown", handleClick);
    onCleanup(() => window.removeEventListener("pointerdown", handleClick));
  });

  const onSelectMode = (name: string) => {
    setActiveAgent(name);
    setOpen(false);
  };

  const cycleMode = () => {
    const list = agents();
    if (list.length <= 1) return;
    const currentIndex = list.findIndex((a) => a.name === currentMode());
    const nextIndex = (currentIndex + 1) % list.length;
    setActiveAgent(list[nextIndex].name);
  };

  return (
    <div class="relative inline-block">
      <button
        ref={buttonRef}
        type="button"
        onClick={() => setOpen((v) => !v)}
        class={`inline-flex items-center gap-1.5 rounded-lg px-2 py-1 text-[12px] font-medium transition-colors cursor-pointer select-none ${
          open()
            ? "bg-[var(--accent-surface)] text-[var(--accent-primary)]"
            : currentMode() === "plan"
              ? "bg-[var(--violet-surface)] text-[var(--violet-text)] hover:brightness-105"
              : "text-[var(--text-secondary)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-hover)]"
        }`}
        title={t("timeline.modeTooltip", { mode: currentMode() })}
      >
        <span class="text-[var(--accent-primary)]">
          {modeIcon(currentMode())}
        </span>
        <span>
          {formatAgentLabel(currentAgent() || currentMode())}
        </span>
        <ChevronDown
          size={11}
          class={`text-[var(--text-muted)] transition-transform duration-200 ${
            open() ? "rotate-180" : ""
          }`}
        />
      </button>

      {/* Floating Mode Switcher Popover */}
      <Show when={open()}>
        <div
          ref={menuRef}
          class="absolute bottom-full left-0 mb-2 w-[270px] rounded-2xl border border-[var(--border-strong)] bg-[var(--bg-elevated)] p-1.5 shadow-2xl z-50 animate-in fade-in zoom-in-95 duration-100"
        >
          <div class="px-2.5 py-1.5 flex items-center justify-between border-b border-[var(--border-subtle)] mb-1">
            <span class="text-[10px] font-semibold uppercase tracking-wider text-[var(--text-faint)]">
              {t("timeline.agentModes")}
            </span>
            <span class="text-[10.5px] text-[var(--text-muted)] font-mono">
              {t("timeline.agentsAvailable", { count: agents().length })}
            </span>
          </div>

          <div class="space-y-1">
            <For each={agents()}>
              {(agent) => {
                const isSelected = () =>
                  agent.name.toLowerCase() === currentMode().toLowerCase();
                const badge = modeBadge(agent.name);

                return (
                  <button
                    type="button"
                    onClick={() => onSelectMode(agent.name)}
                    class={`w-full flex items-start gap-2.5 rounded-xl px-2.5 py-2 text-left transition-colors cursor-pointer ${
                      isSelected()
                        ? "bg-[var(--accent-surface)] text-[var(--text-primary)]"
                        : "text-[var(--text-secondary)] hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)]"
                    }`}
                  >
                    <div
                      class={`mt-0.5 p-1 rounded-lg shrink-0 ${
                        isSelected()
                          ? "bg-[var(--accent-primary)] text-white"
                          : "bg-[var(--bg-inset)] text-[var(--text-muted)]"
                      }`}
                    >
                      {modeIcon(agent.name)}
                    </div>

                    <div class="flex-1 min-w-0">
                      <div class="flex items-center justify-between gap-1.5">
                        <span
                          class={`text-[12px] font-medium ${
                            isSelected() ? "text-[var(--accent-primary)] font-semibold" : ""
                          }`}
                        >
                          {formatAgentLabel(agent)}
                        </span>
                        <Show when={badge}>
                          <span
                            class={`rounded px-1.5 py-[1px] text-[9.5px] font-medium uppercase tracking-wider ${
                              agent.name === "plan"
                                ? "bg-[var(--violet-surface)] text-[var(--violet-text)] border border-[var(--violet-border)]"
                                : "bg-[var(--bg-inset)] text-[var(--text-muted)] border border-[var(--border-subtle)]"
                            }`}
                          >
                            {badge}
                          </span>
                        </Show>
                      </div>

                      <Show when={agent.description}>
                        <p class="mt-0.5 text-[11px] leading-snug text-[var(--text-muted)] line-clamp-2">
                          {agent.description}
                        </p>
                      </Show>
                    </div>

                    <Show when={isSelected()}>
                      <Check size={14} class="shrink-0 text-[var(--accent-primary)] mt-1" />
                    </Show>
                  </button>
                );
              }}
            </For>
          </div>
        </div>
      </Show>
    </div>
  );
}
