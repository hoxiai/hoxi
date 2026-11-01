// web/src/components/timeline/tail-hold.ts
import { type Accessor, createRenderEffect, createSignal, on, onCleanup } from "solid-js";

/**
 * Height bookkeeping for the hand-off from the waiting row ("Thinking…") to
 * the first streamed output. The row unmounts in the same update that mounts
 * the reply, but the reply starts one line tall. Without a hold the timeline
 * shrinks, the bottom-pinned scroll position is clamped down, and everything
 * jumps back up a frame later as the reply grows: a visible blink.
 *
 * The spacer starts at the row's height and shrinks as the new output grows,
 * so the timeline never gets shorter than it was while the row was shown.
 */
export function createTailHoldState() {
  let held = 0; // list height without the spacer while the row was shown
  let rowHeight = 0; // the row's height, margins included
  let spacer = 0;
  return {
    get spacer() {
      return spacer;
    },
    /** Records a layout pass. `listHeight` includes the current spacer. */
    measure(listHeight: number, rowHeightNow: number, waiting: boolean): number {
      const natural = listHeight - spacer;
      if (waiting) {
        held = natural;
        rowHeight = rowHeightNow;
        spacer = 0;
      } else if (spacer > 0) {
        spacer = Math.max(0, held - natural);
      }
      return spacer;
    },
    /** The row just went away while the turn keeps running. */
    handOff(): number {
      spacer = held > 0 ? rowHeight : 0;
      return spacer;
    },
    release(): number {
      held = 0;
      rowHeight = 0;
      spacer = 0;
      return spacer;
    },
  };
}

/**
 * Wires `createTailHoldState` to the timeline list and the waiting row.
 * Render `spacer()` px of empty space right after the row.
 */
export function createTailHeightHold(options: {
  sessionID: Accessor<string | null>;
  waiting: Accessor<boolean>;
  busy: Accessor<boolean>;
}) {
  const state = createTailHoldState();
  const [spacer, setSpacer] = createSignal(0);
  let list: HTMLElement | undefined;
  let row: HTMLElement | undefined;

  const measure = () => {
    if (!list) return;
    setSpacer(state.measure(list.offsetHeight, row?.offsetHeight ?? 0, options.waiting()));
  };
  const observer = typeof ResizeObserver === "function" ? new ResizeObserver(measure) : undefined;
  onCleanup(() => observer?.disconnect());

  // A render effect, so the spacer lands in the same DOM update that removes
  // the row, before anything reads layout and clamps the scroll position.
  createRenderEffect(
    on([options.waiting, options.sessionID, options.busy], (next, prev) => {
      if (!prev) return;
      const [waiting, sessionID, busy] = next;
      if (sessionID !== prev[1] || !busy || waiting) {
        setSpacer(state.release());
      } else if (prev[0]) {
        setSpacer(state.handOff());
      }
    })
  );

  return {
    spacer,
    list: (el: HTMLElement) => {
      list = el;
      observer?.observe(el);
    },
    row: (el: HTMLElement) => {
      row = el;
      observer?.observe(el);
    },
  };
}
