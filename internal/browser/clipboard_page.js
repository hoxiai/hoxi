// Fixed page-side operations: never receives code or accesses the OS clipboard.
function browserClipboard(mode, x, y) {
  const limit = 65536;
  function active(root) {
    let el = root.activeElement;
    while (el?.shadowRoot?.activeElement) el = el.shadowRoot.activeElement;
    if (el?.localName === "iframe" || el?.localName === "frame") {
      try { return el.contentDocument ? active(el.contentDocument) : null; } catch { return null; }
    }
    return el;
  }
  function at(root, px, py) {
    let el = root.elementFromPoint(px, py);
    for (let depth = 0; el?.shadowRoot && depth < 32; depth++) {
      const inner = el.shadowRoot.elementFromPoint(px, py);
      if (!inner || inner === el) break;
      el = inner;
    }
    if (el?.localName === "iframe" || el?.localName === "frame") {
      // Frame transforms and cross-origin documents require frame-local CDP;
      // fail closed rather than read/paste into the surrounding document.
      return null;
    }
    return el;
  }
  const focused = active(document);
  let target = mode === "copy" ? focused : at(document, x, y);
  if (target?.isContentEditable) {
    while (target.parentElement?.isContentEditable) target = target.parentElement;
  }
  const protectedField = el => el?.localName === "input" && el.type === "password" ||
    Boolean(el && el.ownerDocument.defaultView.getComputedStyle(el).webkitTextSecurity &&
      el.ownerDocument.defaultView.getComputedStyle(el).webkitTextSecurity !== "none");
  const editable = el => Boolean(el && !el.disabled && !el.readOnly && (
    el.localName === "textarea" || el.localName === "input" &&
    ["text", "search", "email", "url", "tel", "password", "number"].includes(el.type) || el.isContentEditable));
  function selection() {
    if (!focused || protectedField(focused)) return "";
    if (["input", "textarea"].includes(focused.localName)) {
      const start = focused.selectionStart, end = focused.selectionEnd;
      return typeof start === "number" && typeof end === "number" ? focused.value.slice(start, end) : "";
    }
    const root = focused.getRootNode();
    return String((typeof root.getSelection === "function" ? root.getSelection() : focused.ownerDocument.getSelection()) || "");
  }
  if (mode === "copy") {
    if (!focused || protectedField(focused)) return { clipboardError: "protected" };
    const text = selection();
    if (text.length > limit) return { clipboardError: "tooLarge" };
    return { clipboardText: text };
  }
  if (mode === "paste") {
    if (!editable(target)) return { clipboardError: "notEditable" };
    if (focused !== target) target.focus({ preventScroll: true });
    if (active(document) !== target) return { clipboardError: "notEditable" };
    return {};
  }
  if (mode !== "context") throw new Error("Invalid clipboard operation");
  let anchor = target;
  for (let depth = 0; anchor && depth < 64; depth++) {
    if (anchor.localName === "a" && anchor.href) break;
    anchor = anchor.parentElement || anchor.getRootNode()?.host;
  }
  let linkURL = "";
  if (anchor?.href) {
    try {
      const url = new URL(anchor.href, anchor.ownerDocument.baseURI);
      if (["http:", "https:"].includes(url.protocol) && !url.username && !url.password && url.href.length <= 8192) linkURL = url.href;
    } catch { /* Unusable links are omitted. */ }
  }
  return { context: { canCopy: !protectedField(target) && Boolean(selection()), editable: editable(target), linkURL } };
}
