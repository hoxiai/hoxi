import {
  attachmentActionFeedback,
  attachmentContextMenu,
  clearAttachmentActionFeedback,
  closeAttachmentContextMenu,
  deriveAttachmentFilename,
  openAttachmentContextMenu,
  resolveAttachmentMenuActions,
  showAttachmentActionFeedback,
} from "../store/attachment-context-menu";
import { assertEqual, assertTrue, describe, it } from "./test-harness";

describe("attachment context menu state and resolution", () => {
  it("opens, prevents default, and closes cleanly", () => {
    assertEqual(attachmentContextMenu(), null);
    let prevented = false;
    let stopped = false;
    const evt = {
      clientX: 120,
      clientY: 240,
      preventDefault: () => {
        prevented = true;
      },
      stopPropagation: () => {
        stopped = true;
      },
    } as unknown as MouseEvent;

    openAttachmentContextMenu(evt, {
      kind: "image",
      src: "/api/hoxi/attachments/att_1700_abcdef/content",
      filename: "diagram.png",
    });

    assertEqual(prevented, true);
    assertEqual(stopped, true);
    assertEqual(attachmentContextMenu()?.clientX, 120);
    assertEqual(attachmentContextMenu()?.clientY, 240);
    assertEqual(attachmentContextMenu()?.target.filename, "diagram.png");

    closeAttachmentContextMenu();
    assertEqual(attachmentContextMenu(), null);
  });

  it("ignores blank targets without preventing default", () => {
    let prevented = false;
    const evt = {
      clientX: 10,
      clientY: 20,
      preventDefault: () => {
        prevented = true;
      },
      stopPropagation: () => {},
    } as unknown as MouseEvent;

    openAttachmentContextMenu(evt, { kind: "image", src: "   " });
    assertEqual(prevented, false);
    assertEqual(attachmentContextMenu(), null);
  });

  it("resolves actions for gateway image attachments in desktop vs portal mode", () => {
    const desktop = resolveAttachmentMenuActions(
      {
        kind: "image",
        src: "/api/hoxi/attachments/att_1700_abcdef/content",
        filename: "chart.png",
        onPreview: () => {},
      },
      { portalMode: false }
    );
    assertEqual(desktop.canCopyImage, true);
    assertEqual(desktop.canDownload, true);
    assertEqual(desktop.canPreview, true);
    assertEqual(desktop.canOpenInInspector, true);
    assertEqual(desktop.canOpenExternal, true);
    assertEqual(desktop.externalFilePath, "/api/hoxi/attachments/att_1700_abcdef/content");
    assertEqual(desktop.copyMeta?.kind, "filename");
    assertEqual(desktop.copyMeta?.value, "chart.png");

    const portal = resolveAttachmentMenuActions(
      {
        kind: "image",
        src: "/api/hoxi/attachments/att_1700_abcdef/content",
        filename: "chart.png",
      },
      { portalMode: true }
    );
    assertEqual(portal.canCopyImage, true);
    assertEqual(portal.canDownload, true);
    assertEqual(portal.canOpenInInspector, false);
    assertEqual(portal.canOpenExternal, false);
  });

  it("resolves workspace relative images, remote images, staged videos, and generic files", () => {
    const localImg = resolveAttachmentMenuActions(
      {
        kind: "image",
        src: "data:image/png;base64,AAA",
        localPath: "docs/arch.png",
      },
      { portalMode: false }
    );
    assertEqual(localImg.filename, "arch.png");
    assertEqual(localImg.canOpenExternal, true);
    assertEqual(localImg.externalFilePath, "docs/arch.png");
    assertEqual(localImg.copyMeta?.kind, "path");
    assertEqual(localImg.copyMeta?.value, "docs/arch.png");

    const remoteImg = resolveAttachmentMenuActions(
      {
        kind: "image",
        src: "https://example.com/assets/hero.webp?v=2",
      },
      { portalMode: false }
    );
    assertEqual(remoteImg.filename, "hero.webp");
    assertEqual(remoteImg.canCopyImage, true);
    assertEqual(remoteImg.canOpenExternal, false);
    assertEqual(remoteImg.copyMeta?.kind, "link");
    assertEqual(remoteImg.copyMeta?.value, "https://example.com/assets/hero.webp?v=2");

    const stagedVideo = resolveAttachmentMenuActions(
      {
        kind: "video",
        src: "/api/hoxi/file/content?path=.hoxi%2Fattachments%2Fdemo.mp4&directory=%2Ftmp%2Frepo",
        filename: "demo.mp4",
      },
      { portalMode: false }
    );
    assertEqual(stagedVideo.canCopyImage, false);
    assertEqual(stagedVideo.canDownload, true);
    assertEqual(stagedVideo.canOpenExternal, true);
    assertEqual(stagedVideo.externalFilePath, ".hoxi/attachments/demo.mp4");
    assertEqual(stagedVideo.externalFileDir, "/tmp/repo");
    assertEqual(stagedVideo.copyMeta?.kind, "path");
    assertEqual(stagedVideo.copyMeta?.value, ".hoxi/attachments/demo.mp4");

    const pdfFile = resolveAttachmentMenuActions(
      {
        kind: "file",
        src: "/api/hoxi/attachments/att_1700_abcdef/content",
        filename: "spec.pdf",
        mime: "application/pdf",
      },
      { portalMode: false }
    );
    assertEqual(pdfFile.canCopyImage, false);
    assertEqual(pdfFile.canOpenInInspector, true);
    assertEqual(pdfFile.canOpenExternal, true);
    assertEqual(pdfFile.filename, "spec.pdf");
  });

  it("derives sensible fallback filenames for data URLs, captions, and gateway URLs", () => {
    assertEqual(
      deriveAttachmentFilename({ kind: "image", src: "data:image/webp;base64,AA", mime: "image/webp" }),
      "image.webp"
    );
    assertEqual(
      deriveAttachmentFilename({ kind: "image", src: "data:image/jpeg;base64,AA", filename: "Architecture Diagram" }),
      "Architecture Diagram.jpg"
    );
    assertEqual(
      deriveAttachmentFilename({ kind: "audio", src: "/api/hoxi/attachments/att_1_ab/content", mime: "audio/wav" }),
      "audio.wav"
    );
    assertEqual(
      deriveAttachmentFilename({ kind: "video", src: "/api/hoxi/attachments/att_1_ab/content", mime: "video/webm" }),
      "video.webm"
    );
  });

  it("tracks transient action feedback cleanly", () => {
    clearAttachmentActionFeedback();
    assertEqual(attachmentActionFeedback(), null);
    showAttachmentActionFeedback("Image copied", "ok");
    assertTrue(attachmentActionFeedback() !== null);
    assertEqual(attachmentActionFeedback()?.text, "Image copied");
    assertEqual(attachmentActionFeedback()?.tone, "ok");
    clearAttachmentActionFeedback();
    assertEqual(attachmentActionFeedback(), null);
  });
});
