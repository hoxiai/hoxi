import { createRoot } from "solid-js";
import { createBrowserClipboard, browserExternalURL } from "../store/browser-clipboard";
import { readDesktopClipboard, writeDesktopClipboard } from "../services/desktop-events";
import type { BrowserAction, BrowserActionResult } from "../services/browser";
import { assertDeepEqual, assertEqual, describe, it } from "./test-harness";

const page: BrowserActionResult = { tabID: "one", url: "https://example.com/", title: "", tabs: [], manual: true };
const point = { x: 10, y: 20, clientX: 100, clientY: 200 };

function fixture() {
  let identity = "session-one:tab-one", current = true, disposed = () => {};
  const reads: string[] = [], writes: string[] = [], opened: string[] = [], errors: string[] = [], actions: BrowserAction[] = [];
  let reply = async (_action: BrowserAction): Promise<BrowserActionResult> => ({ ...page, clipboardText: "Selected text 🙂",
    context: { canCopy: true, editable: true, linkURL: "https://example.com/link" } });
  let read = async () => "Mac clipboard 🙂";
  const clipboard = createRoot(dispose => {
    disposed = dispose;
    return createBrowserClipboard({ identity: () => identity, isCurrent: () => current,
      request: async action => { actions.push(action); return reply(action); }, send: action => { actions.push(action); },
      pageURL: () => page.url, onError: message => { errors.push(message); } }, {
      read: () => { reads.push("explicit read"); return read(); }, write: async text => { writes.push(text); }, open: url => { opened.push(url); },
    });
  });
  return { clipboard, reads, writes, opened, errors, actions, dispose: () => disposed(),
    switchPage: () => { identity = "session-two:tab-two"; }, stop: () => { current = false; },
    reply: (fn: typeof reply) => { reply = fn; }, read: (fn: typeof read) => { read = fn; } };
}

describe("Browser clipboard", () => {
  it("only accepts HTTP(S) links without credentials for external opening", () => {
    assertEqual(browserExternalURL("https://example.com/path"), "https://example.com/path");
    for (const url of ["javascript:alert(1)", "file:///private", "https://user:pass@example.com", "data:text/plain,abc", "example.com", "about:blank"]) {
      assertEqual(browserExternalURL(url), null);
    }
  });

  it("reads the system clipboard only after explicit paste and targets the menu position", async () => {
    const f = fixture();
    try {
      await f.clipboard.openMenu(point);
      assertEqual(f.reads.length, 0);
      assertEqual(f.writes.length, 0);
      await f.clipboard.paste();
      assertDeepEqual(f.reads, ["explicit read"]);
      assertDeepEqual(f.actions.at(-1), { action: "paste", x: 10, y: 20, text: "Mac clipboard 🙂" });
      assertEqual(f.clipboard.menu(), null);
    } finally { f.dispose(); }
  });

  it("copies the remote selection or link to the OS clipboard and validates external links", async () => {
    const f = fixture();
    try {
      await f.clipboard.copySelection();
      await f.clipboard.openMenu(point); await f.clipboard.copyLink();
      await f.clipboard.openMenu(point); f.clipboard.openExternal(true);
      assertDeepEqual(f.writes, ["Selected text 🙂", "https://example.com/link"]);
      assertDeepEqual(f.opened, ["https://example.com/link"]);
      assertEqual(f.reads.length, 0);
    } finally { f.dispose(); }
  });

  it("drops a selection reply after switching sessions instead of replacing the OS clipboard", async () => {
    const f = fixture();
    let finish!: (value: BrowserActionResult) => void;
    f.reply(() => new Promise(resolve => { finish = resolve; }));
    try {
      const pending = f.clipboard.copySelection();
      f.switchPage(); finish({ ...page, clipboardText: "old session selection" }); await pending;
      assertDeepEqual(f.writes, []);
    } finally { f.dispose(); }
  });

  it("drops a clipboard read after tab/session changes and after disposal", async () => {
    for (const change of ["switch", "dispose"] as const) {
      const f = fixture();
      let finish!: (text: string) => void;
      try {
        await f.clipboard.openMenu(point);
        f.read(() => new Promise(resolve => { finish = resolve; }));
        const pending = f.clipboard.paste();
        if (change === "switch") f.switchPage(); else f.dispose();
        finish("must not reach another page"); await pending;
        assertEqual(f.actions.filter(action => action.action === "paste").length, 0);
      } finally { f.dispose(); }
    }
  });

  it("keeps newer menus when an older context response arrives late", async () => {
    const f = fixture();
    let finish!: (value: BrowserActionResult) => void;
    f.reply(() => new Promise(resolve => { finish = resolve; }));
    try {
      const first = f.clipboard.openMenu(point);
      f.reply(async () => ({ ...page, context: { canCopy: false, editable: true, linkURL: "https://new.example/" } }));
      await f.clipboard.openMenu({ ...point, x: 30 });
      finish({ ...page, context: { canCopy: true, editable: false, linkURL: "https://old.example/" } }); await first;
      assertEqual(f.clipboard.menu()?.context?.linkURL, "https://new.example/");
    } finally { f.dispose(); }
  });

  it("does not copy protected, empty or oversized text, or paste without an editable target", async () => {
    const f = fixture();
    try {
      for (const result of [{ clipboardError: "protected" as const }, { clipboardText: "" }, { clipboardText: "x".repeat(65537) }]) {
        f.reply(async () => ({ ...page, ...result })); await f.clipboard.copySelection();
      }
      f.reply(async () => ({ ...page, context: { canCopy: false, editable: false, linkURL: "" } }));
      await f.clipboard.openMenu(point); await f.clipboard.paste();
      assertDeepEqual(f.writes, []); assertDeepEqual(f.reads, []);
      assertEqual(f.errors.length, 2);
    } finally { f.dispose(); }
  });

  it("uses native Wails clipboard APIs and reports failed writes", async () => {
    const original = Object.getOwnPropertyDescriptor(globalThis, "window");
    const writes: string[] = [];
    let accepted = true;
    Object.defineProperty(globalThis, "window", { configurable: true, value: { runtime: {
      ClipboardGetText: async () => "native read", ClipboardSetText: async (text: string) => { writes.push(text); return accepted; },
    } } });
    try {
      assertEqual(await readDesktopClipboard(), "native read");
      await writeDesktopClipboard("native write");
      accepted = false;
      let failed = false;
      try { await writeDesktopClipboard("rejected"); } catch { failed = true; }
      assertEqual(failed, true); assertDeepEqual(writes, ["native write", "rejected"]);
    } finally {
      if (original) Object.defineProperty(globalThis, "window", original); else Reflect.deleteProperty(globalThis, "window");
    }
  });
});
