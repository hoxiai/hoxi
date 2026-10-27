import { test } from "node:test";
import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import { browserClipboard } from "./clipboard.js";

test("Chrome Link and isolated Chrome use the same fixed clipboard script", async () => {
  const page = await readFile(new URL("../../internal/browser/clipboard_page.js", import.meta.url), "utf8");
  const extension = await readFile(new URL("./clipboard.js", import.meta.url), "utf8");
  assert.equal(extension.replace("export function browserClipboard(", "function browserClipboard("), page);
});

test("clipboard context is bounded and never reads password fields or OS clipboard", () => {
  const oldDocument = globalThis.document;
  const doc = { activeElement: null, baseURI: "https://example.com", getSelection: () => "selected page words",
    defaultView: { getComputedStyle: () => ({ webkitTextSecurity: "none" }) } };
  const el = (localName, props = {}) => ({ localName, ownerDocument: doc, getRootNode: () => doc, ...props });
  const body = el("body");
  const input = el("input", { type: "text", value: "selected input", selectionStart: 0, selectionEnd: 8 });
  const password = el("input", { type: "password" });
  Object.defineProperty(password, "value", { get() { assert.fail("password value accessed"); } });
  let target = body;
  doc.elementFromPoint = () => target;
  globalThis.document = doc;
  try {
    doc.activeElement = input;
    assert.deepEqual(browserClipboard("copy"), { clipboardText: "selected" });
    const anchor = el("a", { href: "https://example.com/path" });
    target = el("span", { parentElement: anchor });
    assert.deepEqual(browserClipboard("context", 1, 2), { context: { canCopy: true, editable: false, linkURL: "https://example.com/path" } });
    for (const href of ["javascript:alert(1)", "file:///tmp/private", "https://user:pass@example.com", "data:text/plain,secret"]) {
      anchor.href = href;
      assert.equal(browserClipboard("context", 1, 2).context.linkURL, "");
    }
    doc.activeElement = password; target = password;
    assert.deepEqual(browserClipboard("copy"), { clipboardError: "protected" });
    assert.deepEqual(browserClipboard("context", 1, 2).context, { canCopy: false, editable: true, linkURL: "" });
    doc.activeElement = null; target = el("iframe");
    assert.deepEqual(browserClipboard("paste", 1, 2), { clipboardError: "notEditable" });
    doc.activeElement = input; input.value = "x".repeat(65537); input.selectionEnd = 65537;
    assert.deepEqual(browserClipboard("copy"), { clipboardError: "tooLarge" });
    doc.activeElement = body;
    assert.deepEqual(browserClipboard("copy"), { clipboardText: "selected page words" });
    target = el("textarea", { readOnly: true });
    assert.deepEqual(browserClipboard("paste", 1, 2), { clipboardError: "notEditable" });
  } finally { globalThis.document = oldDocument; }
});
