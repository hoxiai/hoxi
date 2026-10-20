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
