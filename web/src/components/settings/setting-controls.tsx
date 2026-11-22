// web/src/components/settings/setting-controls.tsx
// Shared building blocks for the settings tabs. They live apart from
// SettingsModal so the tabs it renders do not import their own parent.
import { createEffect, createSignal, For, type JSX, onCleanup, Show } from "solid-js";
import { Dynamic, Portal } from "solid-js/web";
import { CircleHelp } from "lucide-solid";

export interface HelpTipCoords {
  x: number;
  y: number;
  above: boolean;
}

interface ActiveHelpTipState extends HelpTipCoords {
  anchor: HTMLElement;
}

const [activeHelpTip, setActiveHelpTip] = createSignal<ActiveHelpTipState | null>(null);

export function computeHelpTipCoords(
  rect: { left: number; width: number; top: number; bottom: number },
  vw: number,
  vh: number
): HelpTipCoords {
  const tipWidth = 280;
  const edgePad = 12;
  const gap = 8;
  const half = Math.min(tipWidth, Math.max(120, vw - edgePad * 2)) / 2;
  const rawX = rect.left + rect.width / 2;
  const x = Math.min(vw - edgePad - half, Math.max(edgePad + half, rawX));
  const above = rect.top >= 120 || rect.top > vh - rect.bottom;
  const y = above ? vh - rect.top + gap : rect.bottom + gap;
  return { x, y, above };
}

function measureHelpTip(anchor: HTMLElement): ActiveHelpTipState {
  const rect = anchor.getBoundingClientRect();
  const vw = typeof window !== "undefined" ? window.innerWidth : 1024;
  const vh = typeof window !== "undefined" ? window.innerHeight : 768;
  return { anchor, ...computeHelpTipCoords(rect, vw, vh) };
}

export function HelpTip(props: { text: string; size?: number }) {
  let anchorRef: HTMLSpanElement | undefined;

  const isOpen = () => Boolean(anchorRef && activeHelpTip()?.anchor === anchorRef);

  const openTip = () => {
    if (!anchorRef || !props.text) return;
    setActiveHelpTip(measureHelpTip(anchorRef));
  };

  const closeTip = () => {
    if (anchorRef && activeHelpTip()?.anchor === anchorRef) {
      setActiveHelpTip(null);
    }
  };

  createEffect(() => {
    if (!isOpen() || typeof window === "undefined") return;
    const handleViewportChange = () => closeTip();
    window.addEventListener("scroll", handleViewportChange, true);
    window.addEventListener("resize", handleViewportChange);
    onCleanup(() => {
      window.removeEventListener("scroll", handleViewportChange, true);
      window.removeEventListener("resize", handleViewportChange);
    });
  });

  onCleanup(() => closeTip());

  return (
    <Show when={props.text}>
      <span
        ref={anchorRef}
        role="img"
        aria-label={props.text}
        onMouseEnter={openTip}
        onMouseLeave={closeTip}
        onClick={(e) => {
          e.preventDefault();
          e.stopPropagation();
          if (isOpen()) closeTip();
          else openTip();
        }}
        class="relative inline-flex items-center shrink-0 text-[var(--text-faint)] hover:text-[var(--text-secondary)] transition-colors cursor-help"
      >
        <CircleHelp size={props.size ?? 14} />
        <Show when={isOpen() ? activeHelpTip() : null}>
          {(pos) => (
            <Portal>
              <span
                role="tooltip"
                style={{
                  left: `${pos().x}px`,
                  ...(pos().above ? { bottom: `${pos().y}px` } : { top: `${pos().y}px` }),
                  transform: "translateX(-50%)",
                }}
                class="pointer-events-none fixed z-[90] w-[280px] max-w-[calc(100vw-24px)] rounded-xl border border-[var(--border-strong)] bg-[var(--bg-panel)] px-3.5 py-2.5 text-left font-sans text-[12.5px] font-normal normal-case tracking-normal leading-relaxed whitespace-pre-line text-[var(--text-secondary)] shadow-xl"
              >
                {props.text}
              </span>
            </Portal>
          )}
        </Show>
      </span>
    </Show>
  );
}

export function SettingGroup(props: {
  title?: string;
  tip?: string;
  action?: JSX.Element;
  children: JSX.Element;
}) {
  return (
    <section class="space-y-2.5">
      <Show when={props.title || props.action}>
        <div class="flex items-center justify-between px-1">
          <div class="flex items-center gap-2">
            <Show when={props.title}>
              <h3 class="text-[13px] font-semibold text-[var(--text-secondary)]">
                {props.title}
              </h3>
            </Show>
            <Show when={props.tip}>
              <HelpTip text={props.tip!} />
            </Show>
          </div>
          <Show when={props.action}>{props.action}</Show>
        </div>
      </Show>
      <div class="rounded-2xl border border-[var(--border-subtle)] bg-[var(--bg-inset)]/35 divide-y divide-[var(--border-subtle)]/60">
        {props.children}
      </div>
    </section>
  );
}

export function SettingRow(props: {
  label: string;
  tip?: string;
  icon?: any;
  children: JSX.Element;
}) {
  return (
    <div class="flex items-center justify-between gap-6 px-5 py-4">
      <div class="flex items-center gap-2.5 min-w-0">
        <Show when={props.icon}>
          <Dynamic component={props.icon} size={16} class="text-[var(--text-muted)] shrink-0" />
        </Show>
        <span class="text-[15px] font-medium text-[var(--text-primary)] truncate">
          {props.label}
        </span>
        <Show when={props.tip}>
          <HelpTip text={props.tip!} />
        </Show>
      </div>
      <div class="flex items-center gap-2.5 shrink-0">{props.children}</div>
    </div>
  );
}

export function SegmentedPills<T extends string>(props: {
  value: T;
  options: Array<{ id: T; label: () => string; tip?: () => string; icon?: any }>;
  onChange: (val: T) => void;
}) {
  return (
    <div class="inline-flex items-center gap-0.5 rounded-xl border border-[var(--border-subtle)] bg-[var(--bg-inset)] p-1">
      <For each={props.options}>
        {(opt) => {
          const IconComp = opt.icon;
          const active = () => props.value === opt.id;
          return (
            <button
              type="button"
              onClick={() => props.onChange(opt.id)}
              title={opt.tip ? opt.tip() : undefined}
              class={`hoxi-press inline-flex items-center gap-1.5 rounded-lg px-3.5 py-1.5 text-[13px] font-medium cursor-pointer ${
                active()
                  ? "bg-[var(--bg-panel)] text-[var(--accent-primary)] shadow-2xs"
                  : "text-[var(--text-muted)] hover:text-[var(--text-primary)]"
              }`}
            >
              <Show when={IconComp}>
                <IconComp size={14} />
              </Show>
              <span>{opt.label()}</span>
            </button>
          );
        }}
      </For>
    </div>
  );
}

export function ToggleSwitch(props: {
  checked: boolean;
  disabled?: boolean;
  onChange: (next: boolean) => void;
}) {
  return (
    <button
      type="button"
      role="switch"
      aria-checked={props.checked}
      disabled={props.disabled}
      onClick={() => {
        if (!props.disabled) props.onChange(!props.checked);
      }}
      class={`relative inline-flex h-6 w-11 shrink-0 cursor-pointer rounded-full border-2 border-transparent transition-colors duration-200 ease-in-out disabled:opacity-50 disabled:cursor-not-allowed ${
        props.checked ? "bg-[var(--accent-primary)]" : "bg-[var(--border-strong)]"
      }`}
    >
      <span
        class={`pointer-events-none inline-block h-5 w-5 transform rounded-full ${props.checked ? "bg-[var(--accent-foreground)]" : "bg-white"} shadow-xs transition duration-200 ease-in-out ${
          props.checked ? "translate-x-5" : "translate-x-0"
        }`}
      />
    </button>
  );
}
