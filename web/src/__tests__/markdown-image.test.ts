import {
  advanceStreamingSlice,
  parseInline,
  parseMarkdownBlocks,
  parseNestedBlockquote,
} from "../components/timeline/markdown-parser";
import { liveReasoningTicker } from "../components/timeline/ThoughtBox";
import { isSameTextRevealStream } from "../components/icons/TextShimmer";
import { assertDeepEqual, assertEqual, assertTrue, describe, it } from "./test-harness";

describe("markdown images", () => {
  it("parses remote images without touching surrounding text", () => {
    assertDeepEqual(parseInline("See ![arch](https://hoxi.ai/a.png) now"), [
      { type: "text", text: "See " },
      { type: "image", alt: "arch", src: "https://hoxi.ai/a.png" },
      { type: "text", text: " now" },
    ]);
  });

  it("parses optional image titles", () => {
    assertDeepEqual(parseInline('![arch](https://hoxi.ai/a.png "Diagram")'), [
      {
        type: "image",
        alt: "arch",
        src: "https://hoxi.ai/a.png",
        title: "Diagram",
      },
    ]);
  });

  it("keeps workspace-relative and data image sources", () => {
    assertDeepEqual(parseInline("![shot](docs/shot.png)"), [
      { type: "image", alt: "shot", src: "docs/shot.png" },
    ]);
    const data = "data:image/png;base64,iVBORw0KGgo=";
    assertDeepEqual(parseInline(`![dot](${data})`), [
      { type: "image", alt: "dot", src: data },
    ]);
  });

  it("does not render broken or unsafe images", () => {
    assertDeepEqual(parseInline("![arch](https://hoxi.ai/a.png"), [
      { type: "text", text: "![arch](https://hoxi.ai/a.png" },
    ]);
    assertDeepEqual(parseInline("![x](javascript:alert(1))"), [
      { type: "text", text: "![x](javascript:alert(1))" },
    ]);
  });
});

describe("blockquote nested blocks", () => {
  it("recurses into lists inside blockquotes", () => {
    const blocks = parseNestedBlockquote("- one\n- two");
    assertEqual(blocks?.length, 1);
    assertEqual(blocks?.[0].type, "list");
  });

  it("recurses into fenced code inside blockquotes", () => {
    const blocks = parseNestedBlockquote("```sh\nls -la\n```");
    assertDeepEqual(blocks, [{ type: "code", lang: "sh", code: "ls -la" }]);
  });

  it("keeps simple quotes on the cheap inline path", () => {
    assertEqual(parseNestedBlockquote("just a note"), null);
    assertTrue(parseNestedBlockquote("   ") === null);
  });
});

describe("markdown inline streaming & backtracking guard", () => {
  it("parses unclosed bold, links, and images in linear time without backtracking stalls", () => {
    const unclosedAsciiBold = `**${"a".repeat(120)}`;
    const cjkBody = "\u8fd9\u662f\u4e00\u53e5\u5c1a\u672a\u95ed\u5408\u7684\u4e2d\u6587\u52a0\u7c97\u6587\u672c".repeat(3);
    const unclosedChineseBold = `**${cjkBody}`;
    const unclosedMultiLineBold = `**${"b".repeat(120)}\nsecond line after unclosed bold`;
    const unclosedCodeInBold = `**${"`code` ".repeat(30)}tail without close\nnext line`;
    const unclosedUnderscore = `__${"u".repeat(120)}`;
    const unclosedLink = `[Docs](https://hoxi.ai/${"p".repeat(120)}`;
    const unclosedImage = `![diagram](https://hoxi.ai/${"i".repeat(120)}`;

    const start = performance.now();
    assertDeepEqual(parseInline(unclosedAsciiBold), [
      { type: "bold", text: "a".repeat(120) },
    ]);
    assertDeepEqual(parseInline(unclosedChineseBold), [
      {
        type: "bold",
        text: cjkBody,
      },
    ]);
    assertDeepEqual(parseInline(unclosedMultiLineBold), [
      { type: "text", text: unclosedMultiLineBold },
    ]);
    assertEqual(parseInline(unclosedCodeInBold).length > 0, true);
    assertDeepEqual(parseInline(unclosedUnderscore), [
      { type: "text", text: unclosedUnderscore },
    ]);
    assertDeepEqual(parseInline(unclosedLink), [
      { type: "link", text: "Docs", href: `https://hoxi.ai/${"p".repeat(120)}` },
    ]);
    assertDeepEqual(parseInline(unclosedImage), [
      { type: "text", text: unclosedImage },
    ]);
    const elapsedMs = performance.now() - start;
    assertTrue(
      elapsedMs < 50,
      `Expected unclosed streaming inline markdown to parse in <50ms, took ${elapsedMs.toFixed(2)}ms`
    );
  });

  it("preserves code spans, single asterisks, and balanced parens inside closed tokens", () => {
    assertDeepEqual(parseInline("**bold with `a**b` and * star**"), [
      { type: "bold", text: "bold with `a**b` and * star" },
    ]);
    assertDeepEqual(parseInline("__under with `x__y` and _ single__"), [
      { type: "bold", text: "under with `x__y` and _ single" },
    ]);
    assertDeepEqual(
      parseInline("[`api]v2` docs](https://en.wikipedia.org/wiki/Markdown_(markup_language))"),
      [
        {
          type: "link",
          text: "`api]v2` docs",
          href: "https://en.wikipedia.org/wiki/Markdown_(markup_language)",
        },
      ]
    );
  });

  it("stabilizes unclosed bracket links, compound file extensions, and pending heading prefixes while streaming", () => {
    // Hold lone trailing `[` and `[` + backtick so brackets never flash before link label starts
    assertDeepEqual(parseInline("See ["), [{ type: "text", text: "See " }]);
    assertDeepEqual(parseInline("See [`"), [{ type: "text", text: "See " }]);

    // Keep `[`parseInline` -> `[`parseInline`] -> `[`parseInline`](file://...) as a single link span throughout
    for (const step of [
      "See [`parse",
      "See [`parseInline`",
      "See [`parseInline`]",
      "See [`parseInline`](",
      "See [`parseInline`](file:///Users/test/inline.ts#L92)",
    ]) {
      const spans = parseInline(step);
      assertEqual(spans.length, 2, `Expected 2 spans for ${step}`);
      assertDeepEqual(spans[0], { type: "text", text: "See " });
      assertEqual(spans[1].type, "link");
    }

    // Keep `[inline.ts` -> `[inline.ts]` -> `[inline.ts](file://...)` as a link span without bracket flashes
    for (const step of ["See [inline.ts", "See [inline.ts]", "See [inline.ts](file:///a/inline.ts)"]) {
      const spans = parseInline(step);
      assertEqual(spans.length, 2, `Expected 2 spans for ${step}`);
      assertEqual(spans[1].type, "link");
    }

    // Do not misparse array indexing `arr[idx` as a link
    assertDeepEqual(parseInline("Check arr[idx"), [{ type: "text", text: "Check arr[idx" }]);

    // Bare paths with longer extensions (.tsx, .jsx, .cpp, .mdx, .jsonc) must not truncate at shorter prefixes
    assertDeepEqual(parseInline("Edit web/src/App.tsx and src/main.cpp and docs/guide.mdx"), [
      { type: "text", text: "Edit " },
      { type: "code", text: "web/src/App.tsx" },
      { type: "text", text: " and " },
      { type: "code", text: "src/main.cpp" },
      { type: "text", text: " and " },
      { type: "code", text: "docs/guide.mdx" },
    ]);

    // Hold bare `#`, `##`, `###` on tail line until trailing space confirms final heading level
    assertDeepEqual(parseMarkdownBlocks("#"), []);
    assertDeepEqual(parseMarkdownBlocks("##"), []);
    assertDeepEqual(parseMarkdownBlocks("###"), []);
    assertDeepEqual(parseMarkdownBlocks("### "), [{ type: "heading", level: 3, text: "" }]);
    assertDeepEqual(parseMarkdownBlocks("### Title"), [{ type: "heading", level: 3, text: "Title" }]);
  });

  it("snaps streaming pacer slices across complete inline tokens and keeps reasoning ticker prefix-continuous", () => {
    const linkDoc = "Check [inline.ts](file:///Users/test/web/src/inline.ts#L23) next";
    const snappedLink = advanceStreamingSlice(linkDoc, 6, 18);
    assertEqual(
      linkDoc.slice(0, snappedLink),
      "Check [inline.ts](file:///Users/test/web/src/inline.ts#L23)"
    );

    const codeDoc = "Inspect `web/src/components/timeline/MarkdownInline.tsx` carefully";
    const snappedCode = advanceStreamingSlice(codeDoc, 8, 20);
    assertEqual(
      codeDoc.slice(0, snappedCode),
      "Inspect `web/src/components/timeline/MarkdownInline.tsx`"
    );

    // Hold trailing `]` of `[label]` for one frame, then flush on the next frame if no `(` arrived
    const bracketTail = "See [MarkdownLabel]";
    const heldOnce = advanceStreamingSlice(bracketTail, 4, bracketTail.length);
    assertEqual(heldOnce, bracketTail.length - 1);
    const flushedNext = advanceStreamingSlice(bracketTail, heldOnce, bracketTail.length);
    assertEqual(flushedNext, bracketTail.length);

    // Streaming reasoning ticker across backtick & link closure must preserve TextReveal stream continuity
    const reasoningSteps = [
      "Checking `inline",
      "Checking `inline.ts",
      "Checking `inline.ts`",
      "Checking `inline.ts` and [MarkdownInline.tsx",
      "Checking `inline.ts` and [MarkdownInline.tsx](",
      "Checking `inline.ts` and [MarkdownInline.tsx](file:///Users/test/MarkdownInline.tsx",
      "Checking `inline.ts` and [MarkdownInline.tsx](file:///Users/test/MarkdownInline.tsx)",
    ];
    let prevTicker = liveReasoningTicker(reasoningSteps[0], true);
    for (let i = 1; i < reasoningSteps.length; i++) {
      const nextTicker = liveReasoningTicker(reasoningSteps[i], true);
      assertTrue(
        isSameTextRevealStream(prevTicker, nextTicker),
        `Expected continuous stream from "${prevTicker}" to "${nextTicker}"`
      );
      prevTicker = nextTicker;
    }
  });
});
