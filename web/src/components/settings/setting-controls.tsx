// web/src/components/settings/setting-controls.tsx
// Shared building blocks for the settings tabs. They live apart from
// SettingsModal so the tabs it renders do not import their own parent.
import { For, type JSX, Show } from "solid-js";
import { CircleHelp } from "lucide-solid";

export function HelpTip(props: { text: string }) {
  return (
    <span class="relative inline-flex items-center group/tip shrink-0">
      <CircleHelp
        size={15}
        class="text-[var(--text-faint)] hover:text-[var(--text-secondary)] transition-colors cursor-help"
      />
      <span class="pointer-events-none invisible opacity-0 group-hover/tip:visible group-hover/tip:opacity-100 transition-all duration-150 absolute left-1/2 -translate-x-1/2 bottom-full mb-2.5 z-50 w-68 rounded-xl border border-[var(--border-strong)] bg-[var(--bg-panel)] px-3.5 py-2.5 text-[13px] font-normal leading-relaxed text-[var(--text-secondary)] shadow-xl">
        {props.text}
      </span>
    </span>
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
  const IconComp = props.icon;
  return (
    <div class="flex items-center justify-between gap-6 px-5 py-4">
      <div class="flex items-center gap-2.5 min-w-0">
        <Show when={IconComp}>
          <IconComp size={16} class="text-[var(--text-muted)] shrink-0" />
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
  onChange: (next: boolean) => void;
}) {
  return (
    <button
      type="button"
      role="switch"
      aria-checked={props.checked}
      onClick={() => props.onChange(!props.checked)}
      class={`relative inline-flex h-6 w-11 shrink-0 cursor-pointer rounded-full border-2 border-transparent transition-colors duration-200 ease-in-out ${
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
