import { createEffect, createSignal, on, onCleanup, onMount } from "solid-js";

export function createTranscriptControls(sessionID: () => string | null) {
  const [visible, setVisible] = createSignal(false);
  let previousTop = 0;
  let upwardDistance = 0;
  let intentUntil = 0;
  let dragging = false;
  let timer: ReturnType<typeof setTimeout> | undefined;
  const reset = () => {
    clearTimeout(timer);
    setVisible(false);
    upwardDistance = 0;
    intentUntil = 0;
  };
  createEffect(on(sessionID, reset));
  const endPointer = () => { dragging = false; intentUntil = Date.now() + 300; };
  onMount(() => {
    window.addEventListener("pointerup", endPointer);
    window.addEventListener("pointercancel", endPointer);
    onCleanup(() => {
      window.removeEventListener("pointerup", endPointer);
      window.removeEventListener("pointercancel", endPointer);
    });
  });
  onCleanup(() => clearTimeout(timer));
  return {
    visible,
    markIntent: () => { intentUntil = Date.now() + 1000; },
    startPointer: () => { dragging = true; intentUntil = Date.now() + 1000; },
    endPointer,
    onScroll: (top: number) => {
      const delta = previousTop - top;
      previousTop = top;
      if (!dragging && Date.now() > intentUntil) return;
      if (delta < 0) {
        clearTimeout(timer);
        setVisible(false);
        upwardDistance = 0;
      } else if (delta > 0) {
        upwardDistance += delta;
        if (upwardDistance >= 24) setVisible(true);
        clearTimeout(timer);
        timer = setTimeout(() => { setVisible(false); upwardDistance = 0; }, 2000);
      }
    },
  };
}
