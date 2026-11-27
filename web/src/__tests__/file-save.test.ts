// web/src/__tests__/file-save.test.ts
import { hoxiAPI } from "../services/api";
import {
  dismissSavedFileToast,
  saveBlobWithFeedback,
  savedFileToast,
} from "../store/file-save";
import { assertEqual, assertTrue, describe, it } from "./test-harness";

describe("Unified file save store", () => {
  it("uses native desktop save dialog, remembers path, and emits toast", async () => {
    const originalSaveFile = hoxiAPI.saveFile;
    let seenRequest: Parameters<typeof hoxiAPI.saveFile>[0] | null = null;
    dismissSavedFileToast();
    try {
      hoxiAPI.saveFile = async (input) => {
        seenRequest = input;
        return {
          supported: true,
          saved: true,
          canceled: false,
          path: "/Users/alice/Downloads/report.md",
          directory: "/Users/alice/Downloads",
        };
      };

      const blob = new Blob(["# Summary\n"], { type: "text/markdown" });
      const res = await saveBlobWithFeedback(blob, "report.md", {
        dialogTitle: "Save Markdown",
      });

      assertEqual(res.status, "saved");
      if (res.status === "saved" && res.mode === "native") {
        assertEqual(res.path, "/Users/alice/Downloads/report.md");
        assertEqual(res.directory, "/Users/alice/Downloads");
      } else {
        throw new Error("expected native save result");
      }
      assertTrue(seenRequest !== null, "saveFile API must be invoked");
      assertEqual(seenRequest!.filename, "report.md");
      assertEqual(seenRequest!.title, "Save Markdown");
      assertTrue(seenRequest!.dataUrl.startsWith("data:text/markdown;base64,"));

      const toast = savedFileToast();
      assertTrue(toast !== null, "toast must be shown after native save");
      assertEqual(toast!.result.status, "saved");
      if (toast!.result.status === "saved" && toast!.result.mode === "native") {
        assertEqual(toast!.result.path, "/Users/alice/Downloads/report.md");
      }
    } finally {
      hoxiAPI.saveFile = originalSaveFile;
      dismissSavedFileToast();
    }
  });

  it("returns canceled without showing a toast when user cancels native dialog", async () => {
    const originalSaveFile = hoxiAPI.saveFile;
    dismissSavedFileToast();
    try {
      hoxiAPI.saveFile = async () => ({
        supported: true,
        saved: false,
        canceled: true,
        path: "",
      });

      const res = await saveBlobWithFeedback(
        new Blob(["{}"], { type: "application/json" }),
        "session.json"
      );
      assertEqual(res.status, "canceled");
      assertEqual(savedFileToast(), null);
    } finally {
      hoxiAPI.saveFile = originalSaveFile;
      dismissSavedFileToast();
    }
  });

  it("reports protected destination errors without silently falling back to browser download", async () => {
    const originalSaveFile = hoxiAPI.saveFile;
    dismissSavedFileToast();
    try {
      hoxiAPI.saveFile = async () => {
        throw new Error('HTTP 403: {"error":"selected destination is protected"}');
      };

      const res = await saveBlobWithFeedback(
        new Blob(["hello"], { type: "text/plain" }),
        "notes.txt"
      );
      assertEqual(res.status, "error");
      if (res.status === "error") {
        assertEqual(res.error, "selected destination is protected");
      }
      const toast = savedFileToast();
      assertTrue(toast !== null, "error toast must be shown");
      assertEqual(toast!.result.status, "error");
    } finally {
      hoxiAPI.saveFile = originalSaveFile;
      dismissSavedFileToast();
    }
  });

  it("falls back to browser download when native save is unavailable", async () => {
    const originalSaveFile = hoxiAPI.saveFile;
    const originalDocument = (globalThis as { document?: unknown }).document;
    const originalCreateObjectURL = URL.createObjectURL;
    const originalRevokeObjectURL = URL.revokeObjectURL;
    let clickedDownload = "";
    dismissSavedFileToast();
    try {
      hoxiAPI.saveFile = async () => ({ supported: false });
      URL.createObjectURL = () => "blob:file-save-fallback";
      URL.revokeObjectURL = () => {};
      (globalThis as { document?: unknown }).document = {
        createElement: () => ({
          href: "",
          download: "",
          click() {
            clickedDownload = this.download;
          },
          remove() {},
        }),
        body: {
          appendChild() {},
        },
      };

      const res = await saveBlobWithFeedback(
        new Blob(["<svg></svg>"], { type: "image/svg+xml" }),
        "diagram.svg"
      );
      assertEqual(res.status, "saved");
      if (res.status === "saved" && res.mode === "browser") {
        assertEqual(res.filename, "diagram.svg");
      } else {
        throw new Error("expected browser download fallback");
      }
      assertEqual(clickedDownload, "diagram.svg");
      const toast = savedFileToast();
      assertTrue(toast !== null && toast.result.status === "saved" && toast.result.mode === "browser");
    } finally {
      hoxiAPI.saveFile = originalSaveFile;
      URL.createObjectURL = originalCreateObjectURL;
      URL.revokeObjectURL = originalRevokeObjectURL;
      if (originalDocument === undefined) {
        delete (globalThis as { document?: unknown }).document;
      } else {
        (globalThis as { document?: unknown }).document = originalDocument;
      }
      dismissSavedFileToast();
    }
  });

  it("ignores a concurrent save click while a native save dialog is already open", async () => {
    const originalSaveFile = hoxiAPI.saveFile;
    let calls = 0;
    let releaseFirst!: (value: Awaited<ReturnType<typeof hoxiAPI.saveFile>>) => void;
    dismissSavedFileToast();
    try {
      hoxiAPI.saveFile = () => {
        calls++;
        return new Promise((resolve) => {
          releaseFirst = resolve;
        });
      };

      const first = saveBlobWithFeedback(
        new Blob(["# First"], { type: "text/markdown" }),
        "first.md"
      );
      // Let blobToDataURL resolve and enter hoxiAPI.saveFile.
      await new Promise((r) => setTimeout(r, 0));
      const second = await saveBlobWithFeedback(
        new Blob(["# Second"], { type: "text/markdown" }),
        "second.md"
      );
      assertEqual(second.status, "canceled");
      assertEqual(calls, 1);
      assertEqual(savedFileToast(), null);

      releaseFirst({
        supported: true,
        saved: true,
        canceled: false,
        path: "/Users/alice/Downloads/first.md",
        directory: "/Users/alice/Downloads",
      });
      const firstResult = await first;
      assertEqual(firstResult.status, "saved");
    } finally {
      hoxiAPI.saveFile = originalSaveFile;
      dismissSavedFileToast();
    }
  });
});
