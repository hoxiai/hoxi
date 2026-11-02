import { advanceStreamingSlice } from "../components/timeline/markdown/stream";
import { parseInline } from "../components/timeline/markdown/inline";
import { assertDeepEqual, assertEqual, assertTrue, describe, it } from "./test-harness";

describe("long inline text performance", () => {
  it("preserves long unbroken text without retrying a path at every character", () => {
    for (const text of ["\u4efb\u52a1\u6b63\u5728\u8fde\u7eed\u8f93\u51fa\u6587\u672c".repeat(3300), "a".repeat(32768), "segment/".repeat(4096)]) {
      const start = performance.now();
      const spans = parseInline(text);
      const elapsed = performance.now() - start;
      assertDeepEqual(spans, [{ type: "text", text }]);
      // The old path alternative took hundreds of ms to seconds at this size.
      // Leave ample headroom for slower CI hosts; the bounded scan takes a few ms.
      assertTrue(elapsed < 100, `32K inline text blocked for ${elapsed.toFixed(1)}ms`);
    }
  });

  it("keeps syntax precedence and paths immediately after syntax tokens", () => {
    assertDeepEqual(parseInline("src/a.ts **bold**src/b.ts `src/c.ts` [docs](src/d.ts) ~/src/e.ts:12"), [
      { type: "code", text: "src/a.ts" }, { type: "text", text: " " },
      { type: "bold", text: "bold" }, { type: "code", text: "src/b.ts" },
      { type: "text", text: " " }, { type: "code", text: "src/c.ts" },
      { type: "text", text: " " }, { type: "link", text: "docs", href: "src/d.ts" },
      { type: "text", text: " " }, { type: "code", text: "~/src/e.ts:12" },
    ]);
    assertDeepEqual(parseInline("https://example.test/src/a.ts and **bold**"), [
      { type: "link", text: "https://example.test/src/a.ts", href: "https://example.test/src/a.ts" },
      { type: "text", text: " and " }, { type: "bold", text: "bold" },
    ]);
    assertDeepEqual(parseInline("\u4e2d\u6587~/src/a.ts"), [
      { type: "text", text: "\u4e2d\u6587" }, { type: "code", text: "~/src/a.ts" },
    ]);
    assertDeepEqual(parseInline("~~ok~~a__bold__src/a.ts"), [
      { type: "strike", text: "ok" }, { type: "text", text: "a__bold__src/a.ts" },
    ]);
  });

  it("paces long plain/code lines without quadratic token-boundary matching", () => {
    for (const line of ["\u4efb\u52a1\u6b63\u5728\u8fde\u7eed\u8f93\u51fa\u6587\u672c".repeat(3300), "ABCDEFGHIJKLMNOPQRSTUVWXYZ".repeat(1261)]) {
      const code = `\`\`\`text\n${line}\n\`\`\``;
      const start = performance.now();
      for (const end of [10, 1000, 16000, 32000]) {
        assertEqual(advanceStreamingSlice(code, 8, end), end);
      }
      const elapsed = performance.now() - start;
      assertTrue(elapsed < 100, `32K stream slicing blocked for ${elapsed.toFixed(1)}ms`);
    }
  });

  it("still snaps across file paths and inline syntax after earlier tokens", () => {
    const source = "src/first.ts **bold**src/second.ts and [docs](https://example.test)";
    for (const token of ["src/first.ts", "**bold**", "src/second.ts", "[docs](https://example.test)"]) {
      const index = source.indexOf(token);
      assertEqual(advanceStreamingSlice(source, index, index + 2), index + token.length);
    }
  });
});
