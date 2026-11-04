import { createEffect, createSignal, on, onCleanup, Show, untrack, type Accessor, type JSX } from "solid-js";

export type PresenceState = "enter" | "open" | "exit" | "closed";

const DEFAULT_EXIT_MS = 160;
const DEFERRED_OPEN_FALLBACK_MS = 50;

export function createPresence(
  when: Accessor<boolean>,
  exitMs: number | Accessor<number | undefined> = DEFAULT_EXIT_MS,
): {
  mounted: Accessor<boolean>;
  state: Accessor<PresenceState>;
  closing: Accessor<boolean>;
} {
  const [mounted, setMounted] = createSignal(when());
  const [state, setState] = createSignal<PresenceState>(when() ? "enter" : "closed");
  let exitTimer: ReturnType<typeof setTimeout> | undefined;
  let frame = 0;

  const clear = () => {
    if (exitTimer !== undefined) {
      clearTimeout(exitTimer);
      exitTimer = undefined;
    }
    if (frame) {
      cancelAnimationFrame(frame);
      frame = 0;
    }
  };

  const resolveExitMs = () => {
    const raw = typeof exitMs === "function" ? exitMs() : exitMs;
    return raw ?? DEFAULT_EXIT_MS;
  };

  createEffect(() => {
    const open = when();
    clear();

    if (open) {
      setMounted(true);
      setState("enter");
      frame = requestAnimationFrame(() => {
        frame = requestAnimationFrame(() => {
          frame = 0;
          setState("open");
        });
      });
      return;
    }

    if (untrack(() => mounted())) {
      const duration = resolveExitMs();
      if (duration <= 0) {
        setMounted(false);
        setState("closed");
        return;
      }
      setState("exit");
      exitTimer = setTimeout(() => {
        exitTimer = undefined;
        setMounted(false);
        setState("closed");
      }, duration);
    }
  });

  onCleanup(clear);

  return {
    mounted,
    state,
    closing: () => state() === "exit",
  };
}

/**
 * The open state a collapsible body should render with. A body that mounts in
 * the same update that opens it has no closed style to transition from, so it
 * would jump straight to full height. When `animate()` is true, an opening is
 * applied two frames later, after the body has rendered closed once. Closing
 * and non-animated changes apply at once; the initial value never animates.
 */
export function createDeferredOpen(
  open: Accessor<boolean>,
  animate: Accessor<boolean> = () => true,
): Accessor<boolean> {
  const [visual, setVisual] = createSignal(untrack(open));
  let frame = 0;
  let fallback: ReturnType<typeof setTimeout> | undefined;
  const cancel = () => {
    if (frame) cancelAnimationFrame(frame);
    frame = 0;
    if (fallback !== undefined) clearTimeout(fallback);
    fallback = undefined;
  };
  const reveal = () => {
    cancel();
    setVisual(true);
  };

  createEffect(
    on(
      open,
      (next) => {
        cancel();
        if (!next || !untrack(animate) || typeof requestAnimationFrame !== "function") {
          setVisual(next);
          return;
        }
        frame = requestAnimationFrame(() => {
          frame = requestAnimationFrame(reveal);
        });
        // Throttled or paused frames (a hidden window) must not keep it shut.
        fallback = setTimeout(reveal, DEFERRED_OPEN_FALLBACK_MS);
      },
      { defer: true },
    ),
  );

  onCleanup(cancel);
  return visual;
}

export function Presence(props: {
  when: boolean;
  exitMs?: number;
  children: (state: Accessor<PresenceState>) => JSX.Element;
}) {
  const presence = createPresence(() => props.when, () => props.exitMs);
  return (
    <Show when={presence.mounted()}>
      {(_mounted) => props.children(presence.state)}
    </Show>
  );
}
