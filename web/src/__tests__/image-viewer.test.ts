import {
  closeImagePreview,
  imageViewer,
  imageViewerOpen,
  openImagePreview,
} from "../store/image-viewer";
import {
  closeDocumentPreview,
  documentViewer,
  documentViewerOpen,
  isHtmlPreviewCodeBlockLang,
  isHtmlPreviewPath,
  isMermaidCodeBlockLang,
  isRichDocumentPreviewPath,
  openDocumentPreview,
} from "../store/document-viewer";
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

describe("document & mermaid preview state", () => {
  it("classifies rich document paths, HTML previews, and Mermaid blocks", () => {
    assertEqual(isRichDocumentPreviewPath("report.pdf"), true);
    assertEqual(isRichDocumentPreviewPath("data/sales.xlsx"), true);
    assertEqual(isRichDocumentPreviewPath("metrics.csv"), true);
    assertEqual(isRichDocumentPreviewPath("spec.docx"), true);
    assertEqual(isRichDocumentPreviewPath("README.md"), true);
    assertEqual(isRichDocumentPreviewPath("preview.html"), true);
    assertEqual(isRichDocumentPreviewPath("src/app.ts"), false);
    assertEqual(isHtmlPreviewPath("index.html"), true);
    assertEqual(isHtmlPreviewPath("main.go"), false);
    assertEqual(isMermaidCodeBlockLang("mermaid"), true);
    assertEqual(isMermaidCodeBlockLang("MMD"), true);
    assertEqual(isMermaidCodeBlockLang("typescript"), false);
    assertEqual(isHtmlPreviewCodeBlockLang("html"), true);
    assertEqual(isHtmlPreviewCodeBlockLang("HTM"), true);
    assertEqual(isHtmlPreviewCodeBlockLang("css"), false);
  });

  it("opens file and HTML payloads and closes cleanly", () => {
    assertEqual(documentViewerOpen(), false);
    openDocumentPreview({ kind: "file", path: "   " });
    assertEqual(documentViewerOpen(), false);

    openDocumentPreview({ kind: "file", path: "docs/report.pdf" });
    assertEqual(documentViewerOpen(), true);
    assertEqual(documentViewer()?.kind, "file");
    assertEqual(documentViewer()?.title, "docs/report.pdf");

    openDocumentPreview({ kind: "html", title: "Demo", html: "<h1>Hi</h1>" });
    assertEqual(documentViewer()?.kind, "html");
    assertEqual(documentViewer()?.title, "Demo");

    closeDocumentPreview();
    assertEqual(documentViewerOpen(), false);
    assertTrue(documentViewer() === null);
  });
});
