import { createRoot, createSignal } from "solid-js";
import { reconcile, unwrap } from "solid-js/store";
import { isComposerIMEEvent } from "../components/timeline/composer-keyboard";
import {
  applyAutocompleteToken,
  findAutocompleteToken,
} from "../components/timeline/composer-autocomplete";
import {
  classifyAttachmentKind,
  createImageAttachments,
  createPasteCollapse,
  createPromptHistory,
  createSessionDraftSync,
  MAX_ATTACHMENT_BYTES,
  MAX_ATTACHMENTS,
} from "../components/timeline/composer-hooks";
import { guessFetchedModelDraft } from "../components/models/custom-endpoint-helpers";
import { hoxiAPI } from "../services/api";
import {
  bindComposerQueuedEditBridge,
  cancelQueuedEditInComposer,
  drainNextQueuedPrompt,
  editQueuedPrompt,
  enqueueSessionPrompt,
  forgetPromptQueueState,
  getComposerSkill,
  initializeSessionAI,
  selectComposerSkill,
  setWorkspace,
  submitSessionPrompt,
  workspace,
} from "../store/workspace";
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
  it("detects @mention tokens at the end, in the middle of a sentence, and adjacent to CJK text", () => {
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

    // Adjacent to CJK text without spaces before or after
    const cjkMention = "\u8bf7\u68c0\u67e5@changes\u7136\u540e\u63d0\u4ea4";
    assertDeepEqual(findAutocompleteToken(cjkMention, 11), {
      mode: "file",
      query: "changes",
      start: 3,
      end: 11,
    });
  });

  it("detects /skill and /command tokens at any position while ignoring URLs and multi-segment paths", () => {
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

    // Mid-sentence slash after whitespace
    assertDeepEqual(findAutocompleteToken("please use /tools to inspect", 17), {
      mode: "command",
      query: "tools",
      start: 11,
      end: 17,
    });

    // Slash adjacent to CJK text without spaces
    assertDeepEqual(findAutocompleteToken("\u5e2e\u6211\u7528/skill-authoring\u4f18\u5316", 19), {
      mode: "command",
      query: "skill-authoring",
      start: 3,
      end: 19,
    });

    // Multi-segment file paths and URLs are not slash commands
    assertEqual(findAutocompleteToken("check path /usr/local", 15), null);
    assertEqual(findAutocompleteToken("check path /usr/local", 21), null);
    assertEqual(findAutocompleteToken("visit https://hoxi.ai", 14), null);
    assertEqual(findAutocompleteToken("run ./build.sh", 6), null);
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

describe("Queued prompt re-editing & prompt history safety", () => {
  it("does not overwrite non-empty edited text on ArrowUp or clear restored drafts on ArrowDown", () => {
    createRoot((dispose) => {
      try {
        const [sid] = createSignal<string | null>("ses_hist_guard");
        const history = createPromptHistory(sid);
        history.recordPrompt("Earlier prompt");
        history.recordPrompt("Queued prompt original");

        // While editing non-empty text (not browsing history), ArrowUp must not replace it
        assertEqual(
          history.navigateHistory("up", "Queued prompt edited", 0),
          null
        );
        assertEqual(
          history.navigateHistory("up", "Queued prompt edited", 10),
          null
        );

        // Browsing from an empty composer works, and resetHistoryCursor prevents a stale
        // ArrowDown from wiping a newly restored queued prompt back to ""
        assertEqual(history.navigateHistory("up", "", 0), "Queued prompt original");
        history.resetHistoryCursor();
        assertEqual(
          history.navigateHistory("down", "Restored queued prompt", 22),
          null
        );
      } finally {
        dispose();
      }
    });
  });

  it("preserves queued prompts across re-edits, Escape cancel, stashed drafts, drain races and session switches", async () => {
    const saved = structuredClone(unwrap(workspace));
    const savedSend = hoxiAPI.sendPromptAsync;
    const sid = "ses_queue_reedit";
    const other = "ses_queue_other";
    try {
      const modelA = { providerID: "openai", modelID: "gpt-5" };
      const modelSpecial = { providerID: "anthropic", modelID: "claude-opus-4-6" };
      setWorkspace("providers", [
        { id: "openai", name: "OpenAI", models: { "gpt-5": { id: "gpt-5" } } },
        { id: "anthropic", name: "Anthropic", models: { "claude-opus-4-6": { id: "claude-opus-4-6" } } },
      ]);
      setWorkspace("sessions", [
        { id: sid, title: "Queue Edit", directory: "/proj", time: { created: 1, updated: 1 } },
        { id: other, title: "Other", directory: "/proj", time: { created: 1, updated: 1 } },
      ]);
      setWorkspace("rootDir", "/proj");
      setWorkspace("activeSessionID", sid);
      setWorkspace("statusBySession", sid, { type: "busy" });
      initializeSessionAI(sid, { model: modelA, agent: "build", effort: "medium" });

      await createRoot(async (dispose) => {
        const [text, setText] = createSignal("");
        const [cursor, setCursor] = createSignal(0);
        const sessionID = () => workspace.activeSessionID;
        const imgAttach = createImageAttachments(sessionID, () => workspace.rootDir);
        const pasteCollapse = createPasteCollapse();
        const promptHistory = createPromptHistory(sessionID);
        const draftSync = createSessionDraftSync({
          sessionID,
          directory: () => workspace.rootDir,
          text,
          setText,
          imgAttach,
          pasteCollapse,
          onAfterRestore: () => {},
        });
        const bridge = bindComposerQueuedEditBridge({
          sessionID,
          getText: text,
          setText,
          setCursorOffset: setCursor,
          getExpandedText: () => pasteCollapse.expandIntoPrompt(text()),
          getPromptAttachments: () => imgAttach.toPromptAttachments(),
          getImages: () => imgAttach.images(),
          replaceImages: (items) => imgAttach.replaceImages(items),
          restoreFromAttachments: (atts) => imgAttach.restoreFromAttachments(atts),
          getPastes: () => pasteCollapse.pastes(),
          replacePastes: (items) => pasteCollapse.replacePastes(items),
          getSelectedSkill: () => getComposerSkill(sessionID()) ?? null,
          selectSkill: (id, sk, dir) => selectComposerSkill(id, sk, undefined, dir),
          resetHistoryCursor: () => promptHistory.resetHistoryCursor(),
          focusEnd: (len) => setCursor(len),
          onDrain: (id) => void drainNextQueuedPrompt(id),
        });
        try {
          const q1 = enqueueSessionPrompt(sid, "First queued task", {
            model: modelSpecial,
            effort: "high",
            system: "Keep strict types",
          });
          const q2 = enqueueSessionPrompt(sid, "Second queued task");

          // User has an unsent draft in the composer before clicking Edit on q1
          setText("Unsent scratch note");
          editQueuedPrompt(sid, q1.id);
          bridge.syncDraftEdit();
          assertEqual(text(), "First queued task");
          assertEqual(cursor(), "First queued task".length);
          assertEqual(workspace.queuedPromptsBySession[sid].length, 1);
          assertEqual(workspace.queuedPromptsBySession[sid][0].id, q2.id);

          // Turn finishes while user is still editing q1 (at index 0): q2 must NOT jump ahead
          const dispatched: Array<{ id: string; text: string }> = [];
          hoxiAPI.sendPromptAsync = async (id, promptText) => {
            dispatched.push({ id, text: promptText });
          };
          setWorkspace("statusBySession", sid, { type: "idle" });
          await drainNextQueuedPrompt(sid);
          assertEqual(dispatched.length, 0);
          assertEqual(workspace.queuedPromptsBySession[sid].length, 1);

          // Put session back to busy and switch directly to editing q2 after modifying q1:
          // q1 (with the user's edits) is restored to index 0 instead of being lost
          setWorkspace("statusBySession", sid, { type: "busy" });
          setText("First queued task (edited)");
          editQueuedPrompt(sid, q2.id);
          bridge.syncDraftEdit();
          assertEqual(text(), "Second queued task");
          assertEqual(workspace.queuedPromptsBySession[sid].length, 1);
          assertEqual(workspace.queuedPromptsBySession[sid][0].id, q1.id);
          assertEqual(workspace.queuedPromptsBySession[sid][0].text, "First queued task (edited)");

          // Pressing Escape cancels editing q2, restores q2 at index 1, and restores the stashed scratch note
          assertEqual(cancelQueuedEditInComposer(sid), true);
          assertEqual(text(), "Unsent scratch note");
          assertEqual(workspace.queuedPromptsBySession[sid].length, 2);
          assertEqual(workspace.queuedPromptsBySession[sid][0].id, q1.id);
          assertEqual(workspace.queuedPromptsBySession[sid][1].id, q2.id);
          assertEqual(workspace.queuedPromptsBySession[sid][1].text, "Second queued task");

          // Edit q1 again and submit while busy: q1 returns to index 0 (not appended to the end)
          // and keeps its original ID and system prompt, while the stashed scratch note returns
          editQueuedPrompt(sid, q1.id);
          bridge.syncDraftEdit();
          setText("First queued task (final)");
          const stashed = bridge.popStashedBeforeSend(sid);
          bridge.applyAfterClear(sid, "/proj", true, stashed);
          await submitSessionPrompt("First queued task (final)", sid, { directory: "/proj" });
          assertEqual(text(), "Unsent scratch note");
          assertEqual(workspace.queuedPromptsBySession[sid].length, 2);
          assertEqual(workspace.queuedPromptsBySession[sid][0].id, q1.id);
          assertEqual(workspace.queuedPromptsBySession[sid][0].text, "First queued task (final)");
          assertEqual(workspace.queuedPromptsBySession[sid][0].system, "Keep strict types");
          assertEqual(workspace.queuedPromptsBySession[sid][1].id, q2.id);

          // Switching sessions mid-edit automatically flushes the edited queued prompt back to its slot
          editQueuedPrompt(sid, q2.id);
          bridge.syncDraftEdit();
          setText("Second queued task (switched away)");
          setWorkspace("activeSessionID", other);
          draftSync.sync();
          assertEqual(workspace.queuedPromptsBySession[sid].length, 2);
          assertEqual(workspace.queuedPromptsBySession[sid][1].id, q2.id);
          assertEqual(workspace.queuedPromptsBySession[sid][1].text, "Second queued task (switched away)");
        } finally {
          bridge.dispose();
          dispose();
        }
      });
    } finally {
      forgetPromptQueueState([sid, other]);
      hoxiAPI.sendPromptAsync = savedSend;
      setWorkspace(reconcile(saved));
    }
  });
});



