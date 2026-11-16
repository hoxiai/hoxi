// web/src/components/layout/UpdateProgressBar.tsx
import { Show } from "solid-js";

/**
 * Update progress bar. A native <progress> is avoided on purpose: assigning an
 * unknown (undefined) value to HTMLProgressElement.value throws in WebKit, and
 * that exception used to abort the install click before any request was sent.
 */
export default function UpdateProgressBar(props: { percent: number | undefined; label: string }) {
  return (
    <div
      role="progressbar"
      aria-label={props.label}
      aria-valuemin={0}
      aria-valuemax={100}
      aria-valuenow={props.percent}
      class="relative block h-1.5 w-full overflow-hidden rounded-full bg-[var(--bg-hover)]"
    >
      <Show
        when={props.percent !== undefined}
        fallback={<div class="absolute inset-0 rounded-full bg-[var(--accent-primary)] opacity-40 animate-pulse" />}
      >
        <div
          class="absolute inset-y-0 left-0 rounded-full bg-[var(--accent-primary)] transition-[width] duration-300"
          style={{ width: `${props.percent ?? 0}%` }}
        />
      </Show>
    </div>
  );
}
