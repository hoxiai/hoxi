// web/src/__tests__/rich-views.test.ts
import { assertEqual, assertTrue as assert, describe, it } from "./test-harness";
import type { MessagePart, ToolPart } from "../services/sse-types";
import {
  registerToolRichViewResolver,
  type StructuredRichViewSpec,
} from "../store/rich-view-registry";
import {
  parseHoxiContextPrompt,
  serializeContextEnvelopes,
} from "../store/context-protocol";
import {
  isRichCardsCodeBlockLang,
  isRichMetricsCodeBlockLang,
} from "../store/document-viewer";
import {
  buildContextEnvelopeFromEntities,
  buildContextEnvelopeFromImages,
  buildContextEnvelopeFromMetric,
  extractExplicitToolUiView,
  extractMarkdownParagraphGallery,
  extractSingleMarkdownImage,
  normalizeStructuredRichView,
  parseRichMarkdownBlock,
  replaceRichFencesForReadableExport,
  resolveToolRichView,
  richEntitiesToTable,
  sanitizeRichLinkURL,
  sanitizeRichMediaURL,
} from "../components/timeline/rich-view-spec";
import {
  groupAssistantParts,
  stabilizeTurnRenderItems,
} from "../components/timeline/context-activity-turns";

function makeToolPart(
  id: string,
  tool: string,
  output: string,
  metadata?: Record<string, unknown>,
  attachments?: Array<{ url: string; mime: string; filename?: string }>
): ToolPart {
  return {
    id,
    callID: `call_${id}`,
    sessionID: "ses_rich_1",
    messageID: "msg_rich_1",
    type: "tool",
    tool,
    state: {
      status: "completed",
      input: {},
      output,
      title: tool,
      metadata: metadata ?? {},
      time: { start: 100, end: 200 },
      ...(attachments ? { attachments } : {}),
    },
  };
}

describe("rich views — URL sanitization, schema bounds & Markdown parsers", () => {
  it("sanitizes link and media URLs strictly", () => {
    assertEqual(sanitizeRichLinkURL("https://example.com/p/1"), "https://example.com/p/1");
    assertEqual(sanitizeRichLinkURL("http://127.0.0.1:8080/preview"), "http://127.0.0.1:8080/preview");
    assertEqual(sanitizeRichLinkURL("javascript:alert(1)"), undefined);
    assertEqual(sanitizeRichLinkURL("data:text/html,<script>1</script>"), undefined);
    assertEqual(sanitizeRichLinkURL("file:///etc/passwd"), undefined);
    assertEqual(sanitizeRichLinkURL("https://example.com/\nfoo"), undefined);

    assertEqual(sanitizeRichMediaURL("https://cdn.example.com/a.webp"), "https://cdn.example.com/a.webp");
    assertEqual(sanitizeRichMediaURL("/api/hoxi/attachments/att_1"), "/api/hoxi/attachments/att_1");
    assertEqual(sanitizeRichMediaURL("data:image/png;base64,iVBORw0KGgo="), "data:image/png;base64,iVBORw0KGgo=");
    assertEqual(sanitizeRichMediaURL("assets/hero.png"), "assets/hero.png");
    assertEqual(sanitizeRichMediaURL("../secret.png"), undefined);
    assertEqual(sanitizeRichMediaURL("javascript:alert(1)"), undefined);
    assertEqual(sanitizeRichMediaURL("data:text/html;base64,PHNjcmlwdD4="), undefined);
  });

  it("parses hoxi-cards and hoxi-metrics fenced blocks and enforces safe bounds", () => {
    assert(isRichCardsCodeBlockLang("hoxi-cards"), "hoxi-cards recognized");
    assert(isRichCardsCodeBlockLang("cards"), "cards recognized");
    assert(isRichMetricsCodeBlockLang("hoxi-metrics"), "hoxi-metrics recognized");
    assert(isRichMetricsCodeBlockLang("metrics"), "metrics recognized");

    const cardsSpec = parseRichMarkdownBlock(
      "hoxi-cards",
      JSON.stringify({
        title: "Top Picks",
        badge: "Verified",
        badgeTone: "success",
        entities: [
          {
            id: "sku-1",
            title: "Cotton Hoodie",
            subtitle: "420g heavyweight",
            badge: "Ready",
            badgeTone: "ok",
            thumbnailUrl: "https://cdn.example.com/hoodie.jpg",
            url: "javascript:alert(1)",
            metrics: [
              { label: "Price", value: "$39.90", tone: "primary" },
              { label: "Margin", value: "42%", tone: "positive" },
            ],
            tags: ["Bestseller"],
          },
        ],
      })
    );
    assert(cardsSpec !== null, "cards block parsed");
    assertEqual(cardsSpec!.title, "Top Picks");
    assertEqual(cardsSpec!.badgeTone, "ok");
    assertEqual(cardsSpec!.entities?.length, 1);
    assertEqual(cardsSpec!.entities?.[0].url, undefined, "unsafe entity URL stripped");
    assertEqual(cardsSpec!.entities?.[0].metrics?.[0].tone, "accent");
    assertEqual(cardsSpec!.entities?.[0].metrics?.[1].tone, "ok");

    const table = richEntitiesToTable(cardsSpec!.entities!);
    assert(table !== null, "entities convert to table");
    assertEqual(table!.headers.includes("Price"), true);
    assertEqual(table!.rows[0][0], "Cotton Hoodie");

    const metricArray = parseRichMarkdownBlock(
      "hoxi-metrics",
      JSON.stringify([
        { label: "Revenue", value: "$12,400", delta: "+18%", tone: "ok" },
        { label: "ROAS", value: "3.4x", tone: "accent" },
      ])
    );
    assert(metricArray !== null, "metric array shorthand parsed");
    assertEqual(metricArray!.metrics?.length, 2);

    // Reject malformed or empty JSON
    assertEqual(parseRichMarkdownBlock("hoxi-cards", "{bad json"), null);
    assertEqual(normalizeStructuredRichView({ title: "Empty only" }), null);

    // Enforce bounded item counts
    const huge = normalizeStructuredRichView({
      entities: Array.from({ length: 40 }, (_, i) => ({ title: `Item ${i + 1}` })),
      metrics: Array.from({ length: 20 }, (_, i) => ({ label: `M${i}`, value: String(i) })),
    });
    assertEqual(huge?.entities?.length, 24);
    assertEqual(huge?.metrics?.length, 12);
  });

  it("detects multi-image and single-image Markdown paragraphs cleanly", () => {
    const gallery = extractMarkdownParagraphGallery(
      "![Front](https://cdn.example.com/1.png)\n![Back](https://cdn.example.com/2.png)"
    );
    assert(gallery !== null, "two consecutive images form gallery");
    assertEqual(gallery!.length, 2);
    assertEqual(gallery![0].alt, "Front");

    assertEqual(
      extractMarkdownParagraphGallery("Compare ![Front](https://cdn.example.com/1.png) and ![Back](https://cdn.example.com/2.png)"),
      null,
      "prose mixed with images stays normal paragraph"
    );
    assertEqual(
      extractMarkdownParagraphGallery("![Bad](javascript:alert(1)) ![Ok](https://cdn.example.com/2.png)"),
      null,
      "unsafe image URL rejected"
    );

    const single = extractSingleMarkdownImage("![Main](assets/main.png)");
    assert(single !== null && single.src === "assets/main.png", "single image paragraph extracted");
    assertEqual(extractSingleMarkdownImage("Text ![Main](assets/main.png)"), null);
  });
});

describe("rich views — tool resolution, promotion, gallery grouping & hoxi-context round-trip", () => {
  it("prioritizes explicit uiView over registered resolvers and promotes up to 2 cards", () => {
    const unregister = registerToolRichViewResolver(
      "test-fallback-resolver",
      (part) => {
        if (part.tool !== "custom_lookup") return null;
        return {
          schemaVersion: "hoxi.ui/v1",
          title: "Fallback View",
          promote: true,
          metrics: [{ label: "Count", value: "9" }],
        };
      },
      5
    );
    try {
      const explicitPart = makeToolPart(
        "t_exp",
        "custom_lookup",
        JSON.stringify({
          uiView: {
            title: "Explicit View",
            promote: true,
            metrics: [{ label: "Score", value: "98" }],
          },
        })
      );
      const fallbackPart = makeToolPart("t_fb", "custom_lookup", JSON.stringify({ raw: true }));
      const thirdPart = makeToolPart(
        "t_3",
        "other_tool",
        JSON.stringify({
          uiView: { title: "Third Promoted", promote: true, metrics: [{ label: "X", value: "3" }] },
        })
      );
      const nonPromoted = makeToolPart(
        "t_np",
        "non_promoted",
        JSON.stringify({
          uiView: { title: "Inline Only", promote: false, metrics: [{ label: "Y", value: "1" }] },
        })
      );

      assertEqual(resolveToolRichView(explicitPart)?.title, "Explicit View");
      assertEqual(resolveToolRichView(fallbackPart)?.title, "Fallback View");
      assertEqual(extractExplicitToolUiView(nonPromoted.state.status === "completed" ? nonPromoted.state.output : "")?.promote, false);

      const grouped = groupAssistantParts([explicitPart, fallbackPart, thirdPart, nonPromoted]);
      const richItems = grouped.filter((it) => it.kind === "rich-view");
      assertEqual(richItems.length, 2, "at most 2 promoted rich views per activity group");
      if (richItems[0].kind === "rich-view" && richItems[1].kind === "rich-view") {
        assertEqual(richItems[0].spec.title, "Fallback View");
        assertEqual(richItems[1].spec.title, "Third Promoted");
      }

      const stabilized = stabilizeTurnRenderItems(grouped, groupAssistantParts([explicitPart, fallbackPart, thirdPart, nonPromoted]));
      assert(stabilized === grouped, "stabilizeTurnRenderItems preserves identity for rich-view items");
    } finally {
      unregister();
    }
  });

  it("collapses >= 2 consecutive image file parts into a media-gallery while keeping single image as single", () => {
    const imgTool = makeToolPart(
      "t_img",
      "hoxi_image",
      "done",
      {},
      [
        { url: "/api/hoxi/attachments/img_1", mime: "image/png", filename: "v1.png" },
        { url: "/api/hoxi/attachments/img_2", mime: "image/png", filename: "v2.png" },
      ]
    );
    const grouped = groupAssistantParts([imgTool]);
    assertEqual(grouped.length, 2);
    assertEqual(grouped[0].kind, "activity-group");
    assertEqual(grouped[1].kind, "media-gallery");
    if (grouped[1].kind === "media-gallery") {
      assertEqual(grouped[1].parts.length, 2);
    }

    const singleFile: MessagePart = {
      id: "f_one",
      sessionID: "ses_rich_1",
      messageID: "msg_rich_1",
      type: "file",
      mime: "image/png",
      filename: "solo.png",
      url: "/api/hoxi/attachments/solo",
    };
    const soloGrouped = groupAssistantParts([singleFile]);
    assertEqual(soloGrouped.length, 1);
    assertEqual(soloGrouped[0].kind, "single");
  });

  it("round-trips entity, gallery, and metric selections through hoxi-context v1 and formats readable exports", () => {
    const entityEnv = buildContextEnvelopeFromEntities(
      [
        {
          id: "p-42",
          title: "Insulated Bottle",
          subtitle: "750ml",
          badge: "Ready",
          entityType: "product",
          metrics: [{ label: "Price", value: "$24.90" }],
          ref: { id: "p-42", uri: "hoxi://catalog/p-42", resolve: { mcpTool: "catalog_get" } },
        },
      ],
      { appId: "catalog", appName: "Catalog" }
    );
    const serialized = `${serializeContextEnvelopes([entityEnv])}\n\nCompare margins`;
    const parsed = parseHoxiContextPrompt(serialized);
    assert(parsed.hasContext, "serialized envelope parses back");
    assertEqual(parsed.envelopes[0].items[0].title, "Insulated Bottle");
    assertEqual(parsed.envelopes[0].items[0].ref?.resolve?.mcpTool, "catalog_get");
    assertEqual(parsed.bodyText, "Compare margins");

    const imgEnv = buildContextEnvelopeFromImages([
      { id: "img-a", src: "https://cdn.example.com/a.png", alt: "Hero A" },
    ]);
    assertEqual(imgEnv.items[0].entityType, "image");
    assertEqual(imgEnv.items[0].title, "Hero A");

    const metricEnv = buildContextEnvelopeFromMetric(
      { label: "7d Revenue", value: "$4,820", delta: "+12%" },
      "Store Overview"
    );
    assertEqual(metricEnv.items[0].kind, "snapshot");
    assertEqual(metricEnv.items[0].title, "Store Overview · 7d Revenue");

    const md = [
      "Summary:",
      "```hoxi-cards",
      JSON.stringify({
        title: "Candidates",
        entities: [{ title: "Item A", badge: "Top", metrics: [{ label: "Price", value: "$19" }] }],
      }),
      "```",
    ].join("\n");
    const exported = replaceRichFencesForReadableExport(md);
    assert(!exported.includes("```hoxi-cards"), "rich fence converted for export");
    assert(exported.includes("**Candidates**") && exported.includes("**Item A** [Top]"), "readable export preserves key info");
  });
});
