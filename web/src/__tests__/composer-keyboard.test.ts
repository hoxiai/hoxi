import { isComposerIMEEvent } from "../components/timeline/composer-keyboard";
import {
  applyAutocompleteToken,
  findAutocompleteToken,
} from "../components/timeline/composer-autocomplete";
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

