// web/src/__tests__/desktop-window.test.ts
import {
  closeDesktopWindow,
  isMacPlatform,
  isWailsDesktopHost,
  isWindowsDesktopHost,
  isWindowsPlatform,
  setDesktopWindowTheme,
} from "../services/desktop-window";
import {
  parseCSSColorRGB,
  resolveDesktopCanvasRGB,
  syncDesktopWindowThemeFromDOM,
  syncFramelessResizeBoundary,
} from "../store/desktop-window";
import { shouldToggleWindowMaximiseOnDoubleClick } from "../components/layout/WindowControls";
import { assertDeepEqual, assertEqual, assertTrue, describe, it } from "./test-harness";

function makeWin(overrides: Record<string, unknown> = {}): Window {
  return {
    location: { protocol: "http:", hostname: "wails.localhost" },
    ...overrides,
  } as unknown as Window;
}

function makeNav(platform: string, userAgent = "", uaDataPlatform?: string): Navigator {
  return {
    platform,
    userAgent,
    ...(uaDataPlatform ? { userAgentData: { platform: uaDataPlatform } } : {}),
  } as unknown as Navigator;
}

describe("desktop window platform & frameless helpers", () => {
  it("detects Windows vs macOS platforms without confusing Darwin with Win", () => {
    assertEqual(isWindowsPlatform(makeNav("Win32")), true);
    assertEqual(isWindowsPlatform(makeNav("Win64")), true);
    assertEqual(isWindowsPlatform(makeNav("", "Mozilla/5.0 (Windows NT 10.0; Win64; x64)")), true);
    assertEqual(isWindowsPlatform(makeNav("", "", "Windows")), true);

    // Crucial: "Darwin" contains "win" as a substring and must never match Windows.
    assertEqual(isWindowsPlatform(makeNav("Darwin")), false);
    assertEqual(isWindowsPlatform(makeNav("MacIntel", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7)")), false);
    assertEqual(isWindowsPlatform(makeNav("Linux x86_64")), false);

    assertEqual(isMacPlatform(makeNav("MacIntel")), true);
    assertEqual(isMacPlatform(makeNav("Darwin")), true);
    assertEqual(isMacPlatform(makeNav("Win32")), false);
    assertEqual(isMacPlatform(makeNav("Linux x86_64")), false);
  });

  it("enables Windows window controls only inside a Windows Wails desktop host", () => {
    const winWails = makeWin({ location: { protocol: "http:", hostname: "wails.localhost" }, runtime: {} });
    const winBrowser = makeWin({ location: { protocol: "http:", hostname: "127.0.0.1" } });
    const macWails = makeWin({ location: { protocol: "wails:", hostname: "wails" }, runtime: {} });
    const winDevWails = makeWin({ location: { protocol: "http:", hostname: "localhost" }, wails: { flags: {} } });

    assertEqual(isWailsDesktopHost(winWails), true);
    assertEqual(isWailsDesktopHost(winBrowser), false);
    assertEqual(isWailsDesktopHost(winDevWails), true);

    assertEqual(isWindowsDesktopHost(winWails, makeNav("Win32")), true);
    assertEqual(isWindowsDesktopHost(winDevWails, makeNav("Win32")), true);
    assertEqual(isWindowsDesktopHost(winBrowser, makeNav("Win32")), false);
    assertEqual(isWindowsDesktopHost(macWails, makeNav("MacIntel")), false);
  });

  it("disables Wails DOM resize border while maximised and restores it in normal state", () => {
    const flags = { enableResize: true, resizeEdge: "ne-resize" as string | undefined, defaultCursor: "" };
    const win = makeWin({ wails: { flags } });
    const nav = makeNav("Win32");
    const doc = { documentElement: { style: { cursor: "ne-resize" } } } as unknown as Document;

    syncFramelessResizeBoundary(true, false, win, nav, doc);
    assertEqual(flags.enableResize, false);
    assertEqual(flags.resizeEdge, undefined);
    assertEqual(doc.documentElement.style.cursor, "");

    syncFramelessResizeBoundary(false, false, win, nav, doc);
    assertEqual(flags.enableResize, true);

    // Fullscreen keeps edge resize disabled.
    flags.resizeEdge = "n-resize";
    syncFramelessResizeBoundary(false, true, win, nav, doc);
    assertEqual(flags.enableResize, false);
    assertEqual(flags.resizeEdge, undefined);
  });

  it("parses CSS canvas colors, prefers active surface theme, and syncs DWM theme only on Windows", () => {
    assertDeepEqual(parseCSSColorRGB("#f4f7fb"), [244, 247, 251]);
    assertDeepEqual(parseCSSColorRGB("#0b1220"), [11, 18, 32]);
    assertDeepEqual(parseCSSColorRGB("#abc"), [170, 187, 204]);
    assertDeepEqual(parseCSSColorRGB("rgb(15, 23, 42)"), [15, 23, 42]);
    assertEqual(parseCSSColorRGB("not-a-color"), null);

    const calls: string[] = [];
    const runtime = {
      WindowSetDarkTheme: () => calls.push("dark"),
      WindowSetLightTheme: () => calls.push("light"),
      WindowSetSystemDefaultTheme: () => calls.push("system"),
      WindowSetBackgroundColour: (r: number, g: number, b: number, a: number) =>
        calls.push(`bg:${r},${g},${b},${a}`),
    };
    const rootEl = { __bg: "#f4f7fb" };
    const workEl = { __bg: "#edf6f9" };
    const doc = {
      documentElement: rootEl,
      querySelector: (sel: string) => (sel === "[data-surface]" ? workEl : null),
    } as unknown as Document;
    const win = makeWin({
      runtime,
      getComputedStyle: (el: { __bg?: string }) => ({
        getPropertyValue: (prop: string) => (prop === "--bg-canvas" ? el.__bg || "" : ""),
      }),
    });

    assertDeepEqual(resolveDesktopCanvasRGB("light", doc, win), [237, 246, 249]);

    syncDesktopWindowThemeFromDOM("light", "system", win, makeNav("Win32"), doc);
    assertDeepEqual(calls, ["system", "bg:237,246,249,255"]);

    calls.length = 0;
    setDesktopWindowTheme("dark", "dark", [11, 18, 32], win, makeNav("Win32"));
    assertDeepEqual(calls, ["dark", "bg:11,18,32,255"]);

    // Must be a no-op on macOS so macOS appearance is untouched.
    calls.length = 0;
    setDesktopWindowTheme("dark", "dark", [11, 18, 32], win, makeNav("MacIntel"));
    assertDeepEqual(calls, []);
  });

  it("closes via local desktop bridge and falls back to WindowHide (never Quit)", async () => {
    const origFetch = globalThis.fetch;
    let hidden = 0;
    let quit = 0;
    const win = makeWin({
      runtime: {
        WindowHide: () => hidden++,
        Quit: () => quit++,
      },
    });

    try {
      let calledPath = "";
      let calledMethod = "";
      globalThis.fetch = (async (input: RequestInfo | URL, init?: RequestInit) => {
        calledPath = String(input);
        calledMethod = init?.method || "GET";
        return new Response(JSON.stringify({ ok: true }), {
          status: 200,
          headers: { "Content-Type": "application/json" },
        });
      }) as typeof fetch;

      await closeDesktopWindow(win);
      assertTrue(calledPath.endsWith("/api/hoxi/desktop/window/close"), `unexpected path: ${calledPath}`);
      assertEqual(calledMethod, "POST");
      assertEqual(hidden, 0);
      assertEqual(quit, 0);

      // Network failure falls back to hiding to tray, never quitting the app.
      globalThis.fetch = (async () => {
        throw new Error("bridge offline");
      }) as typeof fetch;

      await closeDesktopWindow(win);
      assertEqual(hidden, 1);
      assertEqual(quit, 0);

      // Hung bridge times out promptly and still falls back to WindowHide.
      globalThis.fetch = ((_input: RequestInfo | URL, init?: RequestInit) =>
        new Promise((_resolve, reject) => {
          init?.signal?.addEventListener("abort", () => reject(new Error("aborted")), { once: true });
        })) as typeof fetch;

      await closeDesktopWindow(win, 25);
      assertEqual(hidden, 2);
      assertEqual(quit, 0);
    } finally {
      globalThis.fetch = origFetch;
    }
  });

  it("only toggles maximize on double-clicking genuine draggable header regions on Windows", () => {
    const win = makeWin({
      runtime: {},
      getComputedStyle: (el: { __drag?: string }) => ({
        getPropertyValue: (prop: string) => (prop === "--wails-draggable" ? el.__drag || "" : ""),
      }),
    });
    const winNav = makeNav("Win32");
    const macNav = makeNav("MacIntel");

    const dragHeader = {
      nodeType: 1,
      __drag: "drag",
      closest: () => null,
    } as unknown as EventTarget;
    const toolbarButton = {
      nodeType: 1,
      __drag: "no-drag",
      closest: () => ({}),
    } as unknown as EventTarget;
    const noDragText = {
      nodeType: 3,
      parentElement: {
        nodeType: 1,
        __drag: "no-drag",
        closest: () => null,
      },
    } as unknown as EventTarget;
    const titleText = {
      nodeType: 3,
      parentElement: {
        nodeType: 1,
        __drag: "drag",
        closest: () => null,
      },
    } as unknown as EventTarget;

    assertEqual(shouldToggleWindowMaximiseOnDoubleClick(dragHeader, win, winNav), true);
    assertEqual(shouldToggleWindowMaximiseOnDoubleClick(titleText, win, winNav), true);
    assertEqual(shouldToggleWindowMaximiseOnDoubleClick(toolbarButton, win, winNav), false);
    assertEqual(shouldToggleWindowMaximiseOnDoubleClick(noDragText, win, winNav), false);
    // macOS handles native titlebar double-click via AppKit.
    assertEqual(shouldToggleWindowMaximiseOnDoubleClick(dragHeader, win, macNav), false);
  });
});
