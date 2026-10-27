import { createEffect, onCleanup, onMount } from "solid-js";
import {
  BrowserPointerTracker, browserPoint, observeBrowserZoom, observeBrowserInputCancellation,
  type BrowserAction,
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
}

export default function BrowserPreview(props: Props) {
  let image!: HTMLImageElement;
  let captured: number | null = null;
  const pointer = new BrowserPointerTracker();

  function cancel() {
    const action = pointer.cancel();
    if (action && props.isCurrent) props.onAction(action);
    if (captured !== null && image?.hasPointerCapture(captured)) image.releasePointerCapture(captured);
    captured = null;
  }

  createEffect(() => {
    props.identity;
    // The old connection owns remote cleanup; never release into a new scope.
    pointer.reset();
    if (captured !== null && image?.hasPointerCapture(captured)) image.releasePointerCapture(captured);
    captured = null;
  });

  function point(clientX: number, clientY: number, clamp = false) {
    return browserPoint(image.getBoundingClientRect(), clientX, clientY, { width: props.width, height: props.height }, clamp);
  }

  function handlePointer(event: PointerEvent) {
    if (!props.isCurrent || !event.isPrimary) return;
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
    draggable={false} class="block w-full h-auto rounded-sm select-none touch-none"
    onPointerDown={handlePointer} onPointerMove={handlePointer} onPointerUp={handlePointer}
    onPointerCancel={cancel} onLostPointerCapture={cancel}
    onAuxClick={event => event.preventDefault()}
    onContextMenu={event => event.preventDefault()}
    onWheel={event => {
      event.preventDefault();
      if (event.ctrlKey || !props.isCurrent) return;
      const at = point(event.clientX, event.clientY);
      const unit = event.deltaMode === 1 ? 16 : event.deltaMode === 2 ? props.height : 1;
      if (at) props.onAction({ action: "wheel", ...at, deltaX: event.deltaX * unit, deltaY: event.deltaY * unit });
    }} />;
}
