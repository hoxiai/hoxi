import { createEffect, createMemo, createSignal, onCleanup } from "solid-js";
import { openExternalBrowserURL, readDesktopClipboard, writeDesktopClipboard } from "../services/desktop-events";
import type { BrowserAction, BrowserActionResult, BrowserContext } from "../services/browser";
import { t } from "../i18n";

export interface BrowserMenuPoint { x: number; y: number; clientX: number; clientY: number }
export interface BrowserMenu extends BrowserMenuPoint {
  identity: string;
  pageURL: string;
  context?: BrowserContext;
}

interface Options {
  identity: () => string;
  isCurrent: () => boolean;
  request: (action: BrowserAction) => Promise<BrowserActionResult | undefined>;
  send: (action: BrowserAction) => void;
  pageURL: () => string;
  onError: (message: string) => void;
}

export function browserExternalURL(raw: string): string | null {
  try {
    const url = new URL(raw);
    return raw.length <= 8192 && ["http:", "https:"].includes(url.protocol) && !url.username && !url.password ? url.href : null;
  } catch { return null; }
}

export function createBrowserClipboard(options: Options, io = {
  read: readDesktopClipboard, write: writeDesktopClipboard, open: openExternalBrowserURL,
}) {
  const [menu, setMenu] = createSignal<BrowserMenu | null>(null);
  const [notice, setNotice] = createSignal("");
  let disposed = false;
  let generation = 0;
  let timer: ReturnType<typeof setTimeout> | undefined;
  const pageIdentity = createMemo(options.identity);
  const live = (identity: string) => !disposed && Boolean(identity) && options.identity() === identity && options.isCurrent();
  createEffect(() => {
    const identity = pageIdentity();
    setMenu(previous => previous?.identity === identity ? previous : null);
    setNotice("");
  });
  onCleanup(() => { disposed = true; clearTimeout(timer); });

  function inform(message: string) {
    setNotice(message);
    clearTimeout(timer);
    timer = setTimeout(() => setNotice(""), 2500);
  }

  function check(result: BrowserActionResult) {
    if (result.clipboardError) {
      options.onError(t(result.clipboardError === "protected" ? "inspector.browser.copyProtected" :
        result.clipboardError === "tooLarge" ? "inspector.browser.clipboardTooLarge" : "inspector.browser.pasteNotEditable"));
      return false;
    }
    return true;
  }

  async function openMenu(point: BrowserMenuPoint) {
    if (!options.isCurrent()) return;
    const opened: BrowserMenu = { ...point, identity: options.identity(), pageURL: options.pageURL() };
    setMenu(opened);
    const result = await options.request({ action: "context", x: point.x, y: point.y });
    if (menu() !== opened || !live(opened.identity)) return;
    if (!result?.context) { setMenu(null); return; }
    setMenu({ ...opened, context: result.context });
  }

  async function copySelection() {
    const identity = options.identity(), request = ++generation;
    setMenu(null);
    if (!live(identity)) return;
    const result = await options.request({ action: "copy" });
    if (request !== generation || !live(identity) || !result || !check(result)) return;
    if (!result.clipboardText) { inform(t("inspector.browser.noSelection")); return; }
    if (typeof result.clipboardText !== "string" || result.clipboardText.length > 65536) {
      options.onError(t("inspector.browser.clipboardTooLarge")); return;
    }
    try {
      await io.write(result.clipboardText);
      if (live(identity)) inform(t("inspector.browser.copied"));
    } catch { if (live(identity)) options.onError(t("inspector.browser.clipboardUnavailable")); }
  }

  async function copyLink() {
    const opened = menu();
    setMenu(null);
    const url = browserExternalURL(opened?.context?.linkURL || "");
    if (!opened || !url || !live(opened.identity)) return;
    try {
      await io.write(url);
      if (live(opened.identity)) inform(t("inspector.browser.copied"));
    } catch { if (live(opened.identity)) options.onError(t("inspector.browser.clipboardUnavailable")); }
  }

  async function paste() {
    const opened = menu();
    setMenu(null);
    if (!opened?.context?.editable || !live(opened.identity)) return;
    try {
      // Read during this click, not when opening a menu or polling the page.
      const text = await io.read();
      if (!live(opened.identity)) return;
      if (text.length > 65536) { options.onError(t("inspector.browser.clipboardTooLarge")); return; }
      if (!text) { inform(t("inspector.browser.clipboardEmpty")); return; }
      const result = await options.request({ action: "paste", x: opened.x, y: opened.y, text });
      if (live(opened.identity) && result) check(result);
    } catch { if (live(opened.identity)) options.onError(t("inspector.browser.clipboardUnavailable")); }
  }

  function openExternal(link = false) {
    const opened = menu();
    setMenu(null);
    const url = browserExternalURL((link ? opened?.context?.linkURL : opened?.pageURL) || "");
    if (opened && url && live(opened.identity)) io.open(url);
  }

  function showPageMenu() {
    const opened = menu();
    setMenu(null);
    if (!opened || !live(opened.identity)) return;
    options.send({ action: "pointer", phase: "down", button: "right", clickCount: 1, x: opened.x, y: opened.y });
    options.send({ action: "pointer", phase: "up", button: "right", clickCount: 1, x: opened.x, y: opened.y });
  }

  return { menu, notice, openMenu, closeMenu: () => setMenu(null), copySelection, copyLink, paste, openExternal, showPageMenu };
}
