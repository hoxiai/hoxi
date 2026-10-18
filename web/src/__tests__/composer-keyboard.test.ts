import { isComposerIMEEvent } from "../components/timeline/composer-keyboard";
import { assertEqual, describe, it } from "./test-harness";

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
