// web/src/store/browser-input.ts
import type { BrowserAction, BrowserPointerButton } from "../services/browser";

type Point = { x: number; y: number };
type Mergeable = "move" | "scroll" | "zoom" | "type";

// Inputs that only matter in aggregate wait for the next animation frame or
// the in-flight request: a pointer move keeps only its latest position, wheel
// deltas add up and inserted text concatenates. Anything else is sent at once,
// after the buffer drains, so a release always follows the final move.
export class BrowserInputBuffer {
  private move: BrowserAction | null = null;
  private scroll: (Required<Pick<BrowserAction, "x" | "y" | "deltaX" | "deltaY">> & { action: "scroll" | "wheel" }) | null = null;
  private zoom: BrowserAction | null = null;
  private text = "";
  private order: Mergeable[] = [];

  add(action: BrowserAction): boolean {
    if (action.action === "pointer" && action.phase === "move") {
      if (!this.move) this.order.push("move");
      this.move = action;
    } else if (action.action === "scroll" || action.action === "wheel") {
      if (this.scroll) {
        this.scroll.x = action.x ?? this.scroll.x;
        this.scroll.y = action.y ?? this.scroll.y;
        this.scroll.deltaX += action.deltaX ?? 0;
        this.scroll.deltaY += action.deltaY ?? 0;
      } else {
        this.order.push("scroll");
        this.scroll = { action: action.action, x: action.x ?? 0, y: action.y ?? 0, deltaX: action.deltaX ?? 0, deltaY: action.deltaY ?? 0 };
      }
    } else if (action.action === "zoom") {
      if (!this.zoom) this.order.push("zoom");
      this.zoom = { ...action, scaleFactor: Math.max(.25, Math.min(4, (this.zoom?.scaleFactor ?? 1) * (action.scaleFactor ?? 1))) };
    } else if (action.action === "type") {
      const text = action.text ?? "";
      if (text && !this.text) this.order.push("type");
      this.text += text;
    } else {
      return false;
    }
    return true;
  }

  // Returns buffered inputs in the order each kind first arrived.
  drain(): BrowserAction[] {
    const actions: BrowserAction[] = [];
    for (const kind of this.order) {
      if (kind === "move" && this.move) actions.push(this.move);
      if (kind === "scroll" && this.scroll) actions.push(this.scroll);
      if (kind === "zoom" && this.zoom) actions.push(this.zoom);
      if (kind === "type" && this.text) actions.push({ action: "type", text: this.text });
    }
    this.move = this.scroll = null;
    this.zoom = null;
    this.text = "";
    this.order = [];
    return actions;
  }
}

// DOM MouseEvent.button numbering, and the MouseEvent.buttons bits Chrome also uses.
const BUTTON_NAMES: Record<number, BrowserPointerButton> = { 0: "left", 1: "middle", 2: "right" };
const BUTTON_BITS: Record<BrowserPointerButton, number> = { left: 1, right: 2, middle: 4 };
const MULTI_CLICK_MS = 500;
const MULTI_CLICK_DISTANCE = 4;

export interface BrowserPointerEvent {
  type: "pointerdown" | "pointermove" | "pointerup";
  button: number;
  buttons: number;
  altKey: boolean;
  ctrlKey: boolean;
  metaKey: boolean;
  shiftKey: boolean;
}

// CDP modifier bits: Alt=1, Ctrl=2, Meta=4, Shift=8.
export function browserPointerModifiers(event: Pick<BrowserPointerEvent, "altKey" | "ctrlKey" | "metaKey" | "shiftKey">): number {
  return (event.altKey ? 1 : 0) | (event.ctrlKey ? 2 : 0) | (event.metaKey ? 4 : 0) | (event.shiftKey ? 8 : 0);
}

// Turns pointer events on the preview into remote presses, moves and releases.
// Pointer events carry no click count, so repeated presses are counted here.
// Every press it reports is later released or cancelled.
export class BrowserPointerTracker {
  private held = 0;
  private last: { button: BrowserPointerButton; x: number; y: number; time: number; count: number } | null = null;

  constructor(private readonly now: () => number = () => performance.now()) {}

  get pressed(): boolean {
    return this.held !== 0;
  }

  handle(event: BrowserPointerEvent, point: Point): BrowserAction[] {
    const modifiers = browserPointerModifiers(event);
    const actions: BrowserAction[] = [];
    if (event.type === "pointermove") actions.push({ action: "pointer", phase: "move", ...point, modifiers });
    const button = BUTTON_NAMES[event.button];
    let changed: BrowserAction | null = null;
    if (event.type === "pointerdown" && button) {
      changed = this.press(button, point, modifiers);
    } else if (event.type === "pointermove" && button) {
      // A chorded button went down or up while another stayed held.
      changed = event.buttons & BUTTON_BITS[button] ? this.press(button, point, modifiers) : this.release(button, point, modifiers);
    }
    if (changed) actions.push(changed);
    if (event.type === "pointerup") {
      // pointerup fires when the last button lifts; release all still held.
      for (const name of ["left", "right", "middle"] as const) {
        if (!(event.buttons & BUTTON_BITS[name])) {
          const up = this.release(name, point, modifiers);
          if (up) actions.push(up);
        }
      }
    }
    return actions;
  }

  // Forgets held buttons and returns the request that releases them remotely.
  cancel(): BrowserAction | null {
    if (!this.held) return null;
    this.held = 0;
    return { action: "pointer", phase: "cancel" };
  }

  reset(): void {
    this.held = 0;
    this.last = null;
  }

  private press(button: BrowserPointerButton, point: Point, modifiers: number): BrowserAction | null {
    if (this.held & BUTTON_BITS[button]) return null;
    this.held |= BUTTON_BITS[button];
    const time = this.now();
    const last = this.last;
    const repeat = last && last.button === button && time - last.time <= MULTI_CLICK_MS &&
      Math.abs(point.x - last.x) <= MULTI_CLICK_DISTANCE && Math.abs(point.y - last.y) <= MULTI_CLICK_DISTANCE;
    const count = repeat ? Math.min(last.count + 1, 3) : 1;
    this.last = { button, ...point, time, count };
    return { action: "pointer", phase: "down", ...point, button, clickCount: count, modifiers };
  }

  private release(button: BrowserPointerButton, point: Point, modifiers: number): BrowserAction | null {
    if (!(this.held & BUTTON_BITS[button])) return null;
    this.held &= ~BUTTON_BITS[button];
    const clickCount = this.last?.button === button ? this.last.count : 1;
    return { action: "pointer", phase: "up", ...point, button, clickCount, modifiers };
  }
}
