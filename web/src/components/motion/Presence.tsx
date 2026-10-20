import { createEffect, createSignal, onCleanup, Show, untrack, type Accessor, type JSX } from "solid-js";

export type PresenceState = "enter" | "open" | "exit" | "closed";

const DEFAULT_EXIT_MS = 160;

export function createPresence(
  when: Accessor<boolean>,
  exitMs = DEFAULT_EXIT_MS,
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
      setState("exit");
      exitTimer = setTimeout(() => {
        exitTimer = undefined;
        setMounted(false);
        setState("closed");
      }, exitMs);
    }
  });

  onCleanup(clear);

  return {
    mounted,
    state,
    closing: () => state() === "exit",
  };
}

export function Presence(props: {
  when: boolean;
  exitMs?: number;
  children: (state: Accessor<PresenceState>) => JSX.Element;
}) {
  const presence = createPresence(() => props.when, props.exitMs);
  return (
    <Show when={presence.mounted()}>
      {(_mounted) => props.children(presence.state)}
    </Show>
  );
}
