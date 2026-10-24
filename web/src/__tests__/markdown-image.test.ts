import {
  parseInline,
  parseNestedBlockquote,
} from "../components/timeline/markdown-parser";
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
});
