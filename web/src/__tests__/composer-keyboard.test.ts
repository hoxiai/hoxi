import { isComposerIMEEvent } from "../components/timeline/composer-keyboard";
import {
  applyAutocompleteToken,
  findAutocompleteToken,
} from "../components/timeline/composer-autocomplete";
import {
  classifyAttachmentKind,
  createImageAttachments,
  MAX_ATTACHMENT_BYTES,
  MAX_ATTACHMENTS,
} from "../components/timeline/composer-hooks";
import { guessFetchedModelDraft } from "../components/models/CustomProviderModelSection";
import { assertDeepEqual, assertEqual, describe, it } from "./test-harness";

describe("Composer IME keyboard guard", () => {
  const enter = { key: "Enter", keyCode: 13, isComposing: false };

  it("leaves composition keys to the IME before any composer shortcuts", () => {
    for (const key of ["Enter", " ", "Tab", "ArrowUp", "ArrowDown", "Escape"]) {
      assertEqual(isComposerIMEEvent({ ...enter, key }, true), true);
      assertEqual(isComposerIMEEvent({ ...enter, key, isComposing: true }, false), true);
    }
  });

  it("recognizes WebKit's commit Enter even after compositionend", () => {
    assertEqual(isComposerIMEEvent({ ...enter, keyCode: 229 }, false), true);
    assertEqual(isComposerIMEEvent({ ...enter, key: "Process" }, false), true);
  });

  it("allows the next ordinary Enter and other keys after composition finishes", () => {
    assertEqual(isComposerIMEEvent(enter, true), true);
    assertEqual(isComposerIMEEvent(enter, false), false);
    for (const key of ["Tab", "ArrowUp", "ArrowDown", "Escape"]) {
      assertEqual(isComposerIMEEvent({ ...enter, key }, false), false);
    }
  });
});

describe("Composer cursor-aware autocomplete token", () => {
  it("detects @mention tokens both at the end and in the middle of a sentence", () => {
    const endToken = findAutocompleteToken("check @src/app", 14);
    assertDeepEqual(endToken, {
      mode: "file",
      query: "src/app",
      start: 6,
      end: 14,
    });

    const midSentence = "please review @comp before shipping";
    // Cursor right after "@comp" (offset 19)
    const midToken = findAutocompleteToken(midSentence, 19);
    assertDeepEqual(midToken, {
      mode: "file",
      query: "comp",
      start: 14,
      end: 19,
    });

    const applied = applyAutocompleteToken(midSentence, midToken!, "@src/components/Composer.tsx ");
    assertEqual(applied.text, "please review @src/components/Composer.tsx before shipping");
    assertEqual(applied.cursor, 43);
  });

  it("detects /command tokens only at the start of input", () => {
    const cmdToken = findAutocompleteToken("/rev extra args", 4);
    assertDeepEqual(cmdToken, {
      mode: "command",
      query: "rev",
      start: 0,
      end: 4,
    });

    const applied = applyAutocompleteToken("/rev extra args", cmdToken!, "/review ");
    assertEqual(applied.text, "/review extra args");
    assertEqual(applied.cursor, 8);

    // Mid-sentence slash is not a slash command
    assertEqual(findAutocompleteToken("check path /usr/local", 15), null);
  });
});

describe("Composer image paste & custom model vision inference", () => {
  it("defaults chat and custom models to supporting image and reasoning while excluding utility endpoints", () => {
    for (const modelID of [
      "claude-opus-4-6",
      "MiniMax-M2.7",
      "my-company-internal-agent-v1",
      "custom-gateway-model",
    ]) {
      const draft = guessFetchedModelDraft({ id: modelID, name: modelID });
      assertEqual(draft.image, true);
      assertEqual(draft.reasoning, true);
    }

    const embed = guessFetchedModelDraft({
      id: "text-embedding-3-small",
      name: "text-embedding-3-small",
    });
    assertEqual(embed.image, false);
    assertEqual(embed.reasoning, false);
  });

  it("extracts inline data:image HTML clipboard paste and passes ordinary text through", () => {
    const attach = createImageAttachments();
    let prevented = false;
    const htmlPasteEvt = {
      preventDefault: () => {
        prevented = true;
      },
      clipboardData: {
        items: [],
        files: [],
        getData: (type: string) =>
          type === "text/html"
            ? '<img src="data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk+M9QDwADhgGAWjR9awAAAABJRU5ErkJggg==" />'
            : "",
      },
    } as unknown as ClipboardEvent;

    assertEqual(attach.handleClipboardPaste(htmlPasteEvt), true);
    assertEqual(prevented, true);
    assertEqual(attach.images().length, 1);
    assertEqual(attach.images()[0]?.mime, "image/png");

    let textPrevented = false;
    const textPasteEvt = {
      preventDefault: () => {
        textPrevented = true;
      },
      clipboardData: {
        items: [],
        files: [],
        getData: (type: string) => (type === "text/plain" ? "ordinary prompt text" : ""),
      },
    } as unknown as ClipboardEvent;

    assertEqual(attach.handleClipboardPaste(textPasteEvt), false);
    assertEqual(textPrevented, false);
  });

  it("classifies images, PDFs, spreadsheets, documents, text/code files and rejects unsupported binaries", () => {
    assertEqual(classifyAttachmentKind("shot.png", "image/png"), "image");
    assertEqual(classifyAttachmentKind("photo.heic", ""), "image");
    assertEqual(classifyAttachmentKind("spec.pdf", "application/pdf"), "pdf");
    assertEqual(classifyAttachmentKind("budget.xlsx", ""), "spreadsheet");
    assertEqual(classifyAttachmentKind("report.xls", "application/vnd.ms-excel"), "spreadsheet");
    assertEqual(classifyAttachmentKind("metrics.csv", "text/csv"), "spreadsheet");
    assertEqual(classifyAttachmentKind("data.tsv", ""), "spreadsheet");
    assertEqual(classifyAttachmentKind("sheet.ods", ""), "spreadsheet");
    assertEqual(classifyAttachmentKind("proposal.docx", ""), "document");
    assertEqual(classifyAttachmentKind("roadmap.pptx", ""), "document");
    assertEqual(classifyAttachmentKind("notes.txt", "text/plain"), "text");
    assertEqual(classifyAttachmentKind("main.go", ""), "text");
    assertEqual(classifyAttachmentKind("archive.zip", "application/octet-stream"), null);
  });

  it("surfaces clear notices for unsupported files, oversized files, and count limits", () => {
    const attach = createImageAttachments();

    // Unsupported binary file
    attach.addFile({
      name: "installer.dmg",
      type: "application/x-apple-diskimage",
      size: 1024,
    } as File);
    assertEqual(attach.images().length, 0);
    assertEqual(Boolean(attach.notice()?.includes("installer.dmg")), true);

    // Oversized file
    attach.clearNotice();
    attach.addFile({
      name: "huge.pdf",
      type: "application/pdf",
      size: MAX_ATTACHMENT_BYTES + 1024,
    } as File);
    assertEqual(attach.images().length, 0);
    assertEqual(Boolean(attach.notice()?.includes("huge.pdf")), true);

    // Count limit via HTML paste
    for (let i = 0; i < MAX_ATTACHMENTS + 2; i++) {
      attach.handleClipboardPaste({
        preventDefault: () => {},
        clipboardData: {
          items: [],
          files: [],
          getData: (type: string) =>
            type === "text/html"
              ? '<img src="data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk+M9QDwADhgGAWjR9awAAAABJRU5ErkJggg==" />'
              : "",
        },
      } as unknown as ClipboardEvent);
    }
    assertEqual(attach.images().length, MAX_ATTACHMENTS);
    assertEqual(Boolean(attach.notice()?.includes(String(MAX_ATTACHMENTS))), true);
  });
});


