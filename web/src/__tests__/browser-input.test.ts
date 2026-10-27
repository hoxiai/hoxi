import { browserPoint } from "../services/browser";
import { BrowserInputBuffer, BrowserPointerTracker, browserPointerModifiers, type BrowserPointerEvent } from "../store/browser-input";
import { assertDeepEqual, assertEqual, describe, it } from "./test-harness";

const keys = { altKey: false, ctrlKey: false, metaKey: false, shiftKey: false };
const down = (button = 0, buttons = 1): BrowserPointerEvent => ({ type: "pointerdown", button, buttons, ...keys });
const move = (buttons = 0, button = -1): BrowserPointerEvent => ({ type: "pointermove", button, buttons, ...keys });
const up = (button = 0, buttons = 0): BrowserPointerEvent => ({ type: "pointerup", button, buttons, ...keys });

describe("Browser preview input", () => {
  it("keeps only the latest pointer move and drains it before the next press", () => {
    const buffer = new BrowserInputBuffer();
    for (const x of [1, 2, 3]) assertEqual(buffer.add({ action: "pointer", phase: "move", x, y: x }), true);
    assertEqual(buffer.add({ action: "pointer", phase: "down", x: 3, y: 3, button: "left", clickCount: 1 }), false);
    assertDeepEqual(buffer.drain(), [{ action: "pointer", phase: "move", x: 3, y: 3 }]);
    assertDeepEqual(buffer.drain(), []);
  });

  it("merges wheel deltas and text while preserving arrival order across kinds", () => {
    const buffer = new BrowserInputBuffer();
    buffer.add({ action: "type", text: "he" });
    buffer.add({ action: "scroll", x: 5, y: 6, deltaX: 1, deltaY: 10 });
    buffer.add({ action: "pointer", phase: "move", x: 7, y: 8 });
    buffer.add({ action: "type", text: "llo" });
    buffer.add({ action: "scroll", x: 9, y: 9, deltaY: 5 });
    buffer.add({ action: "type", text: "" });
    assertDeepEqual(buffer.drain(), [
      { action: "type", text: "hello" },
      { action: "scroll", x: 9, y: 9, deltaX: 1, deltaY: 15 },
      { action: "pointer", phase: "move", x: 7, y: 8 },
    ]);
    buffer.add({ action: "type", text: "" });
    assertDeepEqual(buffer.drain(), []);
    for (const action of [{ action: "key", key: "Enter" }, { action: "pointer", phase: "cancel" }, { action: "click", x: 1, y: 1 }] as const) {
      assertEqual(buffer.add(action), false, action.action);
    }
  });

  it("reports hover, drag and release with the button held during the drag", () => {
    const pointer = new BrowserPointerTracker(() => 0);
    assertDeepEqual(pointer.handle(move(), { x: 1, y: 1 }), [{ action: "pointer", phase: "move", x: 1, y: 1, modifiers: 0 }]);
    assertDeepEqual(pointer.handle({ ...down(), shiftKey: true }, { x: 2, y: 2 }),
      [{ action: "pointer", phase: "down", x: 2, y: 2, button: "left", clickCount: 1, modifiers: 8 }]);
    assertEqual(pointer.pressed, true);
    assertDeepEqual(pointer.handle(move(1), { x: 40, y: 2 }), [{ action: "pointer", phase: "move", x: 40, y: 2, modifiers: 0 }]);
    assertDeepEqual(pointer.handle(up(), { x: 50, y: 2 }),
      [{ action: "pointer", phase: "up", x: 50, y: 2, button: "left", clickCount: 1, modifiers: 0 }]);
    assertEqual(pointer.pressed, false);
    assertDeepEqual(pointer.handle(up(), { x: 50, y: 2 }), [], "release without a press");
    assertEqual(pointer.cancel(), null);
  });

  it("counts double and triple clicks only for quick presses in one place", () => {
    let now = 0;
    const pointer = new BrowserPointerTracker(() => now);
    const click = (x: number, button = 0) => {
      const pressed = pointer.handle(down(button, button === 2 ? 2 : 1), { x, y: 0 })[0];
      const released = pointer.handle(up(button), { x, y: 0 })[0];
      assertEqual(pressed.clickCount, released.clickCount);
      return pressed.clickCount;
    };
    assertEqual(click(10), 1);
    now = 200; assertEqual(click(12), 2);
    now = 400; assertEqual(click(10), 3);
    now = 600; assertEqual(click(10), 3, "count stays at triple");
    now = 1200; assertEqual(click(10), 1, "too slow");
    now = 1300; assertEqual(click(30), 1, "moved too far");
    now = 1400; assertEqual(click(30, 2), 1, "another button");
    const right = pointer.handle(down(2, 2), { x: 30, y: 0 });
    assertEqual(right[0].button, "right");
    assertEqual(right[0].clickCount, 2);
  });

  it("follows chorded buttons and releases every held button on pointerup", () => {
    const pointer = new BrowserPointerTracker(() => 0);
    pointer.handle(down(), { x: 1, y: 1 });
    assertDeepEqual(pointer.handle(move(3, 2), { x: 2, y: 2 }).map(a => [a.phase, a.button]), [["move", undefined], ["down", "right"]]);
    assertDeepEqual(pointer.handle(move(2, 0), { x: 3, y: 3 }).map(a => [a.phase, a.button]), [["move", undefined], ["up", "left"]]);
    pointer.handle(move(6, 1), { x: 3, y: 3 });
    assertDeepEqual(pointer.handle(up(2, 0), { x: 4, y: 4 }).map(a => [a.phase, a.button]), [["up", "right"], ["up", "middle"]]);
    assertEqual(pointer.pressed, false);
  });

  it("cancels a held press exactly once and ignores its late release", () => {
    const pointer = new BrowserPointerTracker(() => 0);
    pointer.handle(down(), { x: 1, y: 1 });
    assertDeepEqual(pointer.cancel(), { action: "pointer", phase: "cancel" });
    assertEqual(pointer.cancel(), null);
    assertDeepEqual(pointer.handle(up(), { x: 1, y: 1 }), []);
    assertDeepEqual(pointer.handle(move(1), { x: 2, y: 2 }).map(a => a.phase), ["move"], "no implicit press");
  });

  it("encodes modifier keys as CDP bits", () => {
    assertEqual(browserPointerModifiers(keys), 0);
    assertEqual(browserPointerModifiers({ altKey: true, ctrlKey: true, metaKey: true, shiftKey: true }), 15);
    assertEqual(browserPointerModifiers({ ...keys, metaKey: true }), 4);
  });

  it("clamps captured drags outside the preview to the viewport edge", () => {
    const bounds = { left: 100, top: 50, width: 640, height: 400 };
    const viewport = { width: 1280, height: 800 };
    assertEqual(browserPoint(bounds, 20, 900, viewport), null);
    assertDeepEqual(browserPoint(bounds, 20, 900, viewport, true), { x: 0, y: 799 });
    assertDeepEqual(browserPoint(bounds, 2000, -40, viewport, true), { x: 1279, y: 0 });
    assertDeepEqual(browserPoint(bounds, 420, 250, viewport, true), { x: 640, y: 400 });
    assertEqual(browserPoint(bounds, Number.NaN, 250, viewport, true), null);
  });
});
