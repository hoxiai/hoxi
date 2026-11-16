import { createEffect, onCleanup, onMount } from "solid-js";
import {
  BrowserPointerTracker, browserPoint, observeBrowserZoom, observeBrowserInputCancellation,
  type BrowserAction, type BrowserMenuPoint,
} from "../../store/workspace";
import { t } from "../../i18n";

interface Props {
  image: string;
  width: number;
  height: number;
  identity: string;
  isCurrent: boolean;
  onAction: (action: BrowserAction) => void;
  onKeyboardFocus: () => void;
  onContextMenu: (point: BrowserMenuPoint) => void;
  scale?: "fit" | "0.5" | "1" | "1.5";
}

export default function BrowserPreview(props: Props) {
  let image!: HTMLImageElement;
  let captured: number | null = null;
  let hostContextPress = false;
  const pointer = new BrowserPointerTracker();

  function cancel() {
    // A scope or tab change resets the tracker first, so this only releases
    // a press made on the page still shown, even while its frame is replaced.
    const action = pointer.cancel();
    if (action) props.onAction(action);
    if (captured !== null && image?.hasPointerCapture(captured)) image.releasePointerCapture(captured);
    captured = null;
    hostContextPress = false;
  }

  createEffect(() => {
    // The old connection owns remote cleanup; never release into a new scope.
    if (!pointer.bind(props.identity)) return;
    hostContextPress = false;
    if (captured !== null && image?.hasPointerCapture(captured)) image.releasePointerCapture(captured);
    captured = null;
  });

  function point(clientX: number, clientY: number, clamp = false) {
    return browserPoint(image.getBoundingClientRect(), clientX, clientY, { width: props.width, height: props.height }, clamp);
  }

  function handlePointer(event: PointerEvent) {
    // Keep remote selection/focus intact while opening the host menu. Shift
    // preserves access to the page's own right-button interactions.
    if (event.type === "pointerdown" && !pointer.pressed && !event.shiftKey && (event.button === 2 || (event.button === 0 && event.ctrlKey))) {
      hostContextPress = true;
      event.preventDefault(); return;
    }
    if (hostContextPress && !pointer.pressed) {
      if (event.type === "pointerup") hostContextPress = false;
      event.preventDefault(); return;
    }
    if (!event.isPrimary || !pointer.accepts(event.type as "pointerdown" | "pointermove" | "pointerup", props.isCurrent)) return;
    const at = point(event.clientX, event.clientY, pointer.pressed);
    if (!at) return;
    if (event.type === "pointerdown") {
      event.preventDefault();
      if (event.button === 3 || event.button === 4) {
        props.onAction({ action: event.button === 3 ? "back" : "forward" });
        return;
      }
      image.setPointerCapture(event.pointerId);
      captured = event.pointerId;
      props.onKeyboardFocus();
    }
    for (const action of pointer.handle(event as PointerEvent & { type: "pointerdown" | "pointermove" | "pointerup" }, at)) props.onAction(action);
    if (event.type === "pointerup" && !pointer.pressed && captured !== null) {
      if (image.hasPointerCapture(captured)) image.releasePointerCapture(captured);
      captured = null;
    }
  }

  onMount(() => {
    const stopZoom = observeBrowserZoom(image, (scaleFactor, x, y) => {
      if (!props.isCurrent || pointer.pressed) return;
      const bounds = image.getBoundingClientRect();
      const at = point(Number.isFinite(x) && x >= bounds.left && x <= bounds.right ? x : bounds.left + bounds.width / 2,
        Number.isFinite(y) && y >= bounds.top && y <= bounds.bottom ? y : bounds.top + bounds.height / 2);
      if (at && scaleFactor !== 1) props.onAction({ action: "zoom", ...at, scaleFactor });
    });
    const stopCancellation = observeBrowserInputCancellation(cancel);
    onCleanup(() => { stopZoom(); stopCancellation(); cancel(); });
  });

  return <img ref={image} src={props.image} alt={t("inspector.browser.viewport")} title={t("inspector.browser.gestureHint")}
    draggable={false} class="block h-auto rounded-sm select-none touch-none max-w-none"
    style={{ width: !props.scale || props.scale === "fit" ? "100%" : `${props.width * Number(props.scale)}px` }}
    onPointerDown={handlePointer} onPointerMove={handlePointer} onPointerUp={handlePointer}
    onPointerCancel={cancel} onLostPointerCapture={cancel}
    onAuxClick={event => event.preventDefault()}
    onContextMenu={event => {
      event.preventDefault();
      if (event.shiftKey || !props.isCurrent) return;
      const at = point(event.clientX, event.clientY);
      if (at) { cancel(); props.onContextMenu({ ...at, clientX: event.clientX, clientY: event.clientY }); }
    }}
    onWheel={event => {
      event.preventDefault();
      if (event.ctrlKey || !props.isCurrent) return;
      const at = point(event.clientX, event.clientY);
      const unit = event.deltaMode === 1 ? 16 : event.deltaMode === 2 ? props.height : 1;
      if (at) props.onAction({ action: "wheel", ...at, deltaX: event.deltaX * unit, deltaY: event.deltaY * unit });
    }} />;
}
