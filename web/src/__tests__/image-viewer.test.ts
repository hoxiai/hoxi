import {
  closeImagePreview,
  imageViewer,
  imageViewerOpen,
  openImagePreview,
} from "../store/image-viewer";
import { assertEqual, assertTrue, describe, it } from "./test-harness";

describe("image preview overlay state", () => {
  it("opens with the given source and closes again", () => {
    assertEqual(imageViewerOpen(), false);
    openImagePreview({ src: "data:image/png;base64,AAA", alt: "shot" });
    assertEqual(imageViewerOpen(), true);
    assertEqual(imageViewer()?.src, "data:image/png;base64,AAA");
    assertEqual(imageViewer()?.alt, "shot");
    closeImagePreview();
    assertEqual(imageViewerOpen(), false);
    assertTrue(imageViewer() === null);
  });

  it("ignores blank sources", () => {
    openImagePreview({ src: "   ", alt: "empty" });
    assertEqual(imageViewerOpen(), false);
  });

  it("keeps the original-open affordance for inspector-backed images", () => {
    let opened = 0;
    openImagePreview({
      src: "data:image/png;base64,BBB",
      alt: "local",
      originalLabel: "Open file in Inspector",
      onOpenOriginal: () => opened++,
    });
    imageViewer()?.onOpenOriginal?.();
    assertEqual(opened, 1);
    assertEqual(imageViewer()?.originalLabel, "Open file in Inspector");
    closeImagePreview();
  });
});
