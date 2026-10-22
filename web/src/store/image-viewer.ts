// web/src/store/image-viewer.ts
// Global full-screen image preview state shared by chat, markdown and inspector surfaces.
import { createSignal } from "solid-js";

export interface ImageViewerPayload {
  src: string;
  alt: string;
  originalLabel?: string;
  onOpenOriginal?: () => void;
}

const [viewer, setViewer] = createSignal<ImageViewerPayload | null>(null);

export const imageViewer = viewer;
export const imageViewerOpen = () => viewer() !== null;

export function openImagePreview(payload: ImageViewerPayload): void {
  const src = (payload.src || "").trim();
  if (!src) return;
  setViewer({
    src,
    alt: payload.alt || "",
    originalLabel: payload.originalLabel,
    onOpenOriginal: payload.onOpenOriginal,
  });
}

export function closeImagePreview(): void {
  setViewer(null);
}
