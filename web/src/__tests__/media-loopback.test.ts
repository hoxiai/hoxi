// web/src/__tests__/media-loopback.test.ts
import {
  getLoopbackHTTPBase,
  resolveDesktopLoopbackURL,
  setGatewayToken,
  setLoopbackHTTPBase,
} from "../services/http";
import { hoxiAPI } from "../services/api";
import { assertEqual, describe, it } from "./test-harness";

function withMockLocation<T>(loc: { protocol: string; hostname: string }, fn: () => T): T {
  const g = globalThis as { window?: { location?: { protocol: string; hostname: string } } };
  const prevWindow = g.window;
  g.window = { location: loc };
  try {
    return fn();
  } finally {
    if (prevWindow === undefined) {
      delete g.window;
    } else {
      g.window = prevWindow;
    }
  }
}

describe("Desktop loopback URL resolution for media staging and playback", () => {
  it("rewrites relative /api paths to loopback HTTP only inside Wails desktop WebViews", () => {
    setLoopbackHTTPBase("ws://127.0.0.1:5420/");
    assertEqual(getLoopbackHTTPBase(), "http://127.0.0.1:5420");

    // macOS Wails custom scheme -> rewritten to loopback HTTP
    withMockLocation({ protocol: "wails:", hostname: "wails" }, () => {
      assertEqual(
        resolveDesktopLoopbackURL("/api/hoxi/file/content?path=inputs%2Fdemo.mp4&raw=1"),
        "http://127.0.0.1:5420/api/hoxi/file/content?path=inputs%2Fdemo.mp4&raw=1"
      );
      // blob:, data:, and external URLs stay untouched
      assertEqual(resolveDesktopLoopbackURL("blob:wails://wails/123"), "blob:wails://wails/123");
      assertEqual(resolveDesktopLoopbackURL("data:video/mp4;base64,AA=="), "data:video/mp4;base64,AA==");
      assertEqual(resolveDesktopLoopbackURL("https://example.com/v.mp4"), "https://example.com/v.mp4");
    });

    // Windows Wails WebView2 host -> rewritten to loopback HTTP
    withMockLocation({ protocol: "http:", hostname: "wails.localhost" }, () => {
      assertEqual(
        resolveDesktopLoopbackURL("/api/hoxi/file/stage?filename=demo.mp4"),
        "http://127.0.0.1:5420/api/hoxi/file/stage?filename=demo.mp4"
      );
    });

    // Browser localhost, LAN, and remote tunnel -> keep relative URL untouched
    withMockLocation({ protocol: "http:", hostname: "127.0.0.1" }, () => {
      assertEqual(
        resolveDesktopLoopbackURL("/api/hoxi/file/content?path=a.mp4&raw=1"),
        "/api/hoxi/file/content?path=a.mp4&raw=1"
      );
    });
    withMockLocation({ protocol: "https:", hostname: "demo.trycloudflare.com" }, () => {
      assertEqual(
        resolveDesktopLoopbackURL("/api/hoxi/file/content?path=a.mp4&raw=1"),
        "/api/hoxi/file/content?path=a.mp4&raw=1"
      );
    });

    setLoopbackHTTPBase("");
  });

  it("stages workspace files via loopback HTTP on Wails desktop with expectedSize and token", async () => {
    setLoopbackHTTPBase("ws://127.0.0.1:5420");
    setGatewayToken("tok_stage_test");
    const prevFetch = globalThis.fetch;
    const calls: Array<{ url: string; method?: string; token?: string; bodySize?: number }> = [];
    globalThis.fetch = (async (input: RequestInfo | URL, init?: RequestInit) => {
      const headers = (init?.headers || {}) as Record<string, string>;
      const body = init?.body as { size?: number } | undefined;
      calls.push({
        url: String(input),
        method: init?.method,
        token: headers["X-Hoxi-Token"],
        bodySize: body?.size,
      });
      return {
        ok: true,
        json: async () => ({
          ok: true,
          path: "inputs/demo.mp4",
          filename: "demo.mp4",
          mime: "video/mp4",
          url: "/api/hoxi/file/content?path=inputs%2Fdemo.mp4&directory=%2Ftmp%2Fws&raw=1",
          size: 12,
        }),
      } as unknown as Response;
    }) as typeof fetch;

    try {
      const fakeFile = new File([new Uint8Array(12)], "demo.mp4", { type: "video/mp4" });
      const res = await withMockLocation({ protocol: "wails:", hostname: "wails" }, () =>
        hoxiAPI.stageWorkspaceFile("/tmp/ws", fakeFile)
      );
      assertEqual(res.ok, true);
      assertEqual(calls.length, 1);
      assertEqual(
        calls[0].url.startsWith("http://127.0.0.1:5420/api/hoxi/file/stage?"),
        true
      );
      assertEqual(calls[0].url.includes("expectedSize=12"), true);
      assertEqual(calls[0].token, "tok_stage_test");
      assertEqual(calls[0].bodySize, 12);
    } finally {
      globalThis.fetch = prevFetch;
      setLoopbackHTTPBase("");
      setGatewayToken("");
    }
  });
});
