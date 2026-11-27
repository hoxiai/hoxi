// web/src/__tests__/share-image-model.test.ts
import {
  buildShareDocument,
  groupTurnsIntoExchanges,
  redactShareSecrets,
  resolveInitialExchangeSelection,
  shortenLocalPaths,
  type ShareTurnInput,
} from "../components/share/share-image-model";
import {
  buildPaginatedShareLayout,
  MAX_SINGLE_CANVAS_LOGICAL_HEIGHT,
  paginateLayoutBlocks,
} from "../components/share/share-image-layout";
import { LIGHT_PALETTE, type LayoutBlock, type LayoutLabels } from "../components/share/share-image-text";
import { serializeContextEnvelopes } from "../store/context-protocol";
import { formatSavedDisplayPath, sanitizeShareImageFilename } from "../store/share-image";
import { assertEqual, assertTrue, describe, it } from "./test-harness";

const TEST_LABELS: LayoutLabels = {
  you: "You",
  assistant: "Hoxi",
  thinking: "Thinking",
  quoted: "Quoted",
  referencedItems: (n) => `${n} items referenced`,
  referencedSingle: "Referenced context",
  moreItems: (n) => `+${n} more items`,
  omittedLines: (n) => `${n} lines omitted`,
  collabRolePM: "Product Manager",
  collabRoleQA: "QA Report",
  collabRoleOP: "Operations",
  moreTools: (n) => `+${n} more`,
  truncatedCodeLines: (n) => `${n} omitted`,
  imageUnavailable: (label) => `Unavailable: ${label}`,
  turnsCount: (n) => `${n} turns`,
};

const fixedMeasure = (text: string) => text.length * 8;

describe("share image model & privacy", () => {
  it("redacts secrets idempotently and counts matches", () => {
    const sample = [
      "Authorization: Bearer eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9",
      'api_key="sk-ant-1234567890abcdef"',
      "postgres://admin:supersecret@db.internal:5432/app",
      "curl --token ghp_1234567890abcdef123456 https://api.github.com",
      "-----BEGIN RSA PRIVATE KEY-----\nMIIEpAIBAAKCAQEA\n-----END RSA PRIVATE KEY-----",
      "bypass = true",
    ].join("\n");

    const first = redactShareSecrets(sample);
    assertTrue(first.count >= 5, `expected at least 5 redactions, got ${first.count}`);
    assertTrue(!first.text.includes("eyJhbGci"), "bearer token leaked");
    assertTrue(!first.text.includes("sk-ant-1234567890abcdef"), "anthropic key leaked");
    assertTrue(!first.text.includes("supersecret"), "url password leaked");
    assertTrue(!first.text.includes("ghp_1234567890abcdef123456"), "github token leaked");
    assertTrue(!first.text.includes("MIIEpAIBAAKCAQEA"), "private key leaked");
    assertTrue(first.text.includes("postgres://admin:[redacted]@db.internal:5432/app"), "url host should stay readable");
    assertTrue(first.text.includes("bypass = true"), "non-secret word 'bypass' must stay intact");

    const second = redactShareSecrets(first.text);
    assertEqual(second.text, first.text);
    assertEqual(second.count, 0);
  });

  it("shortens workspace, worktree, and home directory paths", () => {
    const raw =
      "Check /Users/alice/code/myrepo/.hoxi/worktrees/wt-1/src/app.ts, /Users/alice/code/myrepo/README.md, " +
      "/home/bob/.config/app.json, and C:\\Users\\carol\\project\\main.go";
    const shortened = shortenLocalPaths(raw, "/Users/alice/code/myrepo/.hoxi/worktrees/wt-1");
    assertTrue(shortened.includes("<workspace>/src/app.ts"), `worktree path not shortened: ${shortened}`);
    assertTrue(shortened.includes("<workspace>/README.md"), `repo root not shortened: ${shortened}`);
    assertTrue(shortened.includes("~/.config/app.json"), `linux home not shortened: ${shortened}`);
    assertTrue(shortened.includes("~\\project\\main.go"), `windows home not shortened: ${shortened}`);
    assertTrue(!shortened.includes("alice") && !shortened.includes("bob") && !shortened.includes("carol"), "usernames leaked");
  });

  it("groups multi-step assistant turns, filters todo tools, and collects images", () => {
    const turns: ShareTurnInput[] = [
      {
        info: { id: "msg_u1", sessionID: "ses_1", role: "user", time: { created: 1000 } },
        parts: [
          { id: "p1", sessionID: "ses_1", messageID: "msg_u1", type: "text", text: "Fix bug in /Users/alice/app/main.ts" },
          {
            id: "p2",
            sessionID: "ses_1",
            messageID: "msg_u1",
            type: "file",
            mime: "image/png",
            filename: "bug.png",
            url: "/api/hoxi/attachments/att_1/content",
          },
        ],
      },
      {
        info: { id: "msg_a1", sessionID: "ses_1", role: "assistant", modelID: "claude-sonnet-4", time: { created: 1100 } },
        parts: [
          { id: "p3", sessionID: "ses_1", messageID: "msg_a1", type: "reasoning", text: "Let me check main.ts", time: { start: 1100, end: 1120 } },
          {
            id: "p4",
            sessionID: "ses_1",
            messageID: "msg_a1",
            type: "tool",
            callID: "c1",
            tool: "read_file",
            state: {
              status: "completed",
              input: { filePath: "/Users/alice/app/main.ts" },
              output: "ok",
              title: "",
              metadata: {},
              time: { start: 1100, end: 1150 },
            },
          },
          {
            id: "p5",
            sessionID: "ses_1",
            messageID: "msg_a1",
            type: "tool",
            callID: "c2",
            tool: "todowrite",
            state: {
              status: "completed",
              input: {},
              output: "",
              title: "",
              metadata: {},
              time: { start: 1150, end: 1160 },
            },
          },
        ],
      },
      {
        info: { id: "msg_a2", sessionID: "ses_1", role: "assistant", modelID: "claude-sonnet-4", time: { created: 1200 } },
        parts: [
          {
            id: "p6",
            sessionID: "ses_1",
            messageID: "msg_a2",
            type: "text",
            text: "Done! Here is the diagram:\n\n![arch](docs/arch.png)",
          },
        ],
      },
    ];

    const grouped = groupTurnsIntoExchanges(turns, {
      hideLocalPaths: true,
      redactSecrets: true,
      workspaceDir: "/Users/alice/app",
    });

    assertEqual(grouped.exchanges.length, 1);
    const ex = grouped.exchanges[0];
    assertEqual(ex.messageIDs.length, 3);
    assertEqual(ex.userText, "Fix bug in <workspace>/main.ts");
    assertEqual(ex.tools.length, 1);
    assertEqual(ex.tools[0].detail, "main.ts");
    assertEqual(ex.thinkingText, "Let me check main.ts");
    assertEqual(ex.images.length, 2);
    assertEqual(ex.images[0].sourceKind, "attachment");
    assertEqual(ex.images[1].sourceKind, "markdown");
    assertEqual(ex.images[1].src, "docs/arch.png");

    const focusSel = resolveInitialExchangeSelection(grouped.exchanges, "msg_a2");
    assertTrue(focusSel.has(ex.id), "focusMessageID should select its exchange");

    const doc = buildShareDocument(
      { id: "ses_1", title: "Fix sk-ant-secretkey123456", time: { created: 1000, updated: 1200 }, directory: "/Users/alice/app" },
      "ses_1",
      turns,
      {
        selectedExchangeIDs: focusSel,
        disabledImageIDs: new Set([ex.images[0].id]),
        includeTools: true,
        includeThinking: false,
        includeImages: true,
        hideLocalPaths: true,
        redactSecrets: true,
      }
    );
    assertTrue(!doc.title.includes("sk-ant-secretkey123456"), "session title must be redacted");
    assertEqual(doc.redactionCount, 1);
    assertEqual(doc.exchanges[0].thinkingText, "");
    assertEqual(doc.exchanges[0].images.length, 1);
    assertEqual(doc.exchanges[0].images[0].src, "docs/arch.png");
  });

  it("structures hoxi-context quotes, entity cards, collab prompts, and nested blockquotes without raw tags", () => {
    const quotedPrompt = serializeContextEnvelopes(
      [
        {
          version: 1,
          source: { appId: "timeline", appName: "Hoxi" },
          items: [
            {
              kind: "quote",
              title: "Quoted from conversation",
              content: "### Findings\n- Check `/Users/alice/repo/main.go`\n- Token: `ghp_1234567890abcdef123456`",
            },
          ],
        },
        {
          version: 1,
          source: { appId: "shopark", appName: "Shopark" },
          items: [
            {
              kind: "entity",
              entityType: "product",
              title: "Trail Jacket",
              badge: "In Stock",
              ref: { id: "SKU-42" },
              digest: { price: "$129", margin: "44%", api_key: "sk-proj-secretvalue1234" },
            },
            {
              kind: "snapshot",
              title: "Raw Config",
              content: JSON.stringify({ nested: { a: 1, b: [2, 3] }, ok: true }),
            },
          ],
        },
      ],
      "Please compare these two points."
    );

    const collabPrompt =
      `<untrusted_pm_request author="Alice" mode="build" title="Improve checkout">\n` +
      `### \u95ee\u9898\u63cf\u8ff0\nCart totals lag behind.\n\n` +
      `### \u590d\u73b0\u6b65\u9aa4\n1. Add two items\n2. Change qty\n</untrusted_pm_request>`;

    const turns: ShareTurnInput[] = [
      {
        info: { id: "u1", sessionID: "ses_q", role: "user", time: { created: 1000 } },
        parts: [{ id: "p1", sessionID: "ses_q", messageID: "u1", type: "text", text: quotedPrompt }],
      },
      {
        info: { id: "a1", sessionID: "ses_q", role: "assistant", time: { created: 1100 } },
        parts: [
          {
            id: "p2",
            sessionID: "ses_q",
            messageID: "a1",
            type: "text",
            text:
              "> [!WARNING]\n> ### Key Caveats\n> - First caveat\n> - Second caveat\n\n" +
              "<details>\n<summary>Extra Notes</summary>\n\n- Detail item A\n- Detail item B\n\n</details>",
          },
        ],
      },
      {
        info: { id: "u2", sessionID: "ses_q", role: "user", time: { created: 2000 } },
        parts: [{ id: "p3", sessionID: "ses_q", messageID: "u2", type: "text", text: collabPrompt }],
      },
    ];

    const doc = buildShareDocument(
      { id: "ses_q", title: "Quoted Session", time: { created: 1000, updated: 2000 }, directory: "/Users/alice/repo" },
      "ses_q",
      turns,
      {
        includeTools: true,
        includeThinking: false,
        includeImages: true,
        hideLocalPaths: true,
        redactSecrets: true,
      }
    );

    assertEqual(doc.exchanges.length, 2);
    const ex1 = doc.exchanges[0];
    assertEqual(ex1.userText, "Please compare these two points.");
    assertTrue(!ex1.userText.includes("<details>"), "userText must not leak raw <details> tags");
    assertTrue(!ex1.userPreview.includes("<details>"), "userPreview must not leak raw <details> tags");
    assertEqual(ex1.userContexts.length, 2);
    assertEqual(ex1.userContexts[0].isSingleQuote, true);
    assertTrue(ex1.userContexts[0].items[0].body?.includes("<workspace>/main.go") === true, "local path inside quote should shorten");
    assertTrue(!ex1.userContexts[0].items[0].body?.includes("ghp_1234567890abcdef123456"), "secret inside quote must be redacted");
    assertEqual(ex1.userContexts[1].isSingleQuote, false);
    assertEqual(ex1.userContexts[1].items.length, 2);
    const kvPairs = ex1.userContexts[1].items[0].kvPairs ?? [];
    assertEqual(kvPairs.length, 3);
    assertTrue(kvPairs.every((kv) => !kv.value.includes("sk-proj-secretvalue1234")), "secret inside kv digest must be redacted");
    assertTrue(doc.redactionCount >= 2, `expected >= 2 redactions across quotes, got ${doc.redactionCount}`);

    const ex2 = doc.exchanges[1];
    assertEqual(ex2.userCollab?.role, "pm");
    assertEqual(ex2.userCollab?.author, "Alice");
    assertEqual(ex2.userCollab?.sections.length, 2);

    const layout = buildPaginatedShareLayout({
      doc,
      palette: LIGHT_PALETTE,
      cardWidth: 680,
      layoutMode: "single",
      showBrandFooter: true,
      measure: fixedMeasure,
      highlights: new Map(),
      loadedImages: new Map(),
      svgImages: new Map(),
      labels: TEST_LABELS,
      formattedDate: "2026-11-27",
    });

    const pageBlocks = layout.pages[0].blocks;
    const userBoxes = pageBlocks.filter((b): b is Extract<LayoutBlock, { kind: "user-box" }> => b.kind === "user-box");
    assertEqual(userBoxes.length, 2);
    assertEqual(userBoxes[0].contextCards.length, 2);
    assertEqual(userBoxes[0].contextCards[0].variant, "quote");
    if (userBoxes[0].contextCards[0].variant === "quote") {
      assertTrue(
        userBoxes[0].contextCards[0].bodyBlocks.some((b) => b.kind === "heading") &&
          userBoxes[0].contextCards[0].bodyBlocks.some((b) => b.kind === "list-item"),
        "quoted markdown should parse into structured heading and list blocks"
      );
    }
    assertEqual(userBoxes[0].contextCards[1].variant, "structured");
    if (userBoxes[0].contextCards[1].variant === "structured") {
      assertEqual(userBoxes[0].contextCards[1].rows[0].kvRows.length, 3);
      assertTrue(userBoxes[0].contextCards[1].rows[1].jsonLines.length > 0, "nested json snapshot should format as mono lines");
    }
    assertEqual(userBoxes[1].collabHeader?.role, "pm");
    assertTrue(userBoxes[1].bodyBlocks.some((b) => b.kind === "heading"), "collab sections should render as structured headings");

    const bqBlock = pageBlocks.find((b): b is Extract<LayoutBlock, { kind: "blockquote" }> => b.kind === "blockquote");
    assertTrue(Boolean(bqBlock), "expected blockquote block");
    assertEqual(bqBlock?.alert, "warning");
    assertTrue(Boolean(bqBlock?.blocks && bqBlock.blocks.some((b) => b.kind === "list-item")), "nested blockquote should keep structured list items");

    const detailsBlock = pageBlocks.find((b): b is Extract<LayoutBlock, { kind: "details" }> => b.kind === "details");
    assertTrue(Boolean(detailsBlock), "expected dedicated details block");
    assertTrue(Boolean(detailsBlock?.blocks.some((b) => b.kind === "list-item")), "details block should keep structured inner blocks");
  });

  it("extracts parenthesized markdown image filenames, scopes redaction count to selected exchanges, and clamps wide tables", () => {
    const turns: ShareTurnInput[] = [
      {
        info: { id: "u1", sessionID: "ses_2", role: "user", time: { created: 1000 } },
        parts: [{ id: "p1", sessionID: "ses_2", messageID: "u1", type: "text", text: "Secret: ghp_1234567890abcdef123456" }],
      },
      {
        info: { id: "a1", sessionID: "ses_2", role: "assistant", time: { created: 1050 } },
        parts: [{ id: "p2", sessionID: "ses_2", messageID: "a1", type: "text", text: "Noted" }],
      },
      {
        info: { id: "u2", sessionID: "ses_2", role: "user", time: { created: 2000 } },
        parts: [{ id: "p3", sessionID: "ses_2", messageID: "u2", type: "text", text: "Show table & screenshot" }],
      },
      {
        info: { id: "a2", sessionID: "ses_2", role: "assistant", time: { created: 2050 } },
        parts: [
          {
            id: "p4",
            sessionID: "ses_2",
            messageID: "a2",
            type: "text",
            text:
              "![shot](docs/image(1).png)\n\n" +
              "| C1 | C2 | C3 | C4 | C5 | C6 | C7 | C8 | C9 | C10 | C11 | C12 |\n" +
              "| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |\n" +
              "| a | b | c | d | e | f | g | h | i | j | k | l |",
          },
        ],
      },
    ];

    const doc = buildShareDocument(
      { id: "ses_2", title: "Clean title", time: { created: 1000, updated: 2050 } },
      "ses_2",
      turns,
      {
        selectedExchangeIDs: new Set(["u2"]),
        includeTools: true,
        includeThinking: false,
        includeImages: true,
        hideLocalPaths: true,
        redactSecrets: true,
      }
    );
    assertEqual(doc.redactionCount, 0);
    assertEqual(doc.exchanges[0].images.length, 1);
    assertEqual(doc.exchanges[0].images[0].src, "docs/image(1).png");

    const layout = buildPaginatedShareLayout({
      doc,
      palette: LIGHT_PALETTE,
      cardWidth: 680,
      layoutMode: "single",
      showBrandFooter: true,
      measure: fixedMeasure,
      highlights: new Map(),
      loadedImages: new Map(),
      svgImages: new Map(),
      labels: TEST_LABELS,
      formattedDate: "2026-11-25",
    });
    const tableBlock = layout.pages[0].blocks.find((b) => b.kind === "table");
    assertTrue(Boolean(tableBlock), "expected table block in layout");
    if (tableBlock && tableBlock.kind === "table") {
      const sumW = tableBlock.colWidths.reduce((a, b) => a + b, 0);
      assertTrue(sumW <= layout.contentWidth, `table width ${sumW} exceeded contentWidth ${layout.contentWidth}`);
    }
  });

  it("paginates blocks cleanly without stranding keepWithNext headers and enforces GPU height cap", () => {
    const blocks: LayoutBlock[] = [
      { kind: "paragraph", height: 120, lines: [] },
      { kind: "heading", level: 2, height: 40, lines: [], keepWithNext: true },
      { kind: "paragraph", height: 120, lines: [] },
      { kind: "paragraph", height: 120, lines: [] },
    ];
    const pages = paginateLayoutBlocks(blocks, 220, 220);
    assertEqual(pages.length, 3);
    assertEqual(pages[0].blocks.length, 1);
    assertEqual(pages[1].blocks[0].kind, "heading");
    assertEqual(pages[1].blocks[1].kind, "paragraph");

    const hugeTurns: ShareTurnInput[] = Array.from({ length: 40 }, (_, idx) => ({
      info: { id: `m_${idx}`, sessionID: "ses_big", role: idx % 2 === 0 ? "user" : "assistant", time: { created: 1000 + idx } },
      parts: [
        {
          id: `p_${idx}`,
          sessionID: "ses_big",
          messageID: `m_${idx}`,
          type: "text",
          text: Array.from({ length: 12 }, (__, j) => `Line ${idx}-${j}: detailed explanation about architecture and testing`).join("\n\n"),
        },
      ],
    }));

    const doc = buildShareDocument(undefined, "ses_big", hugeTurns, {
      selectedExchangeIDs: resolveInitialExchangeSelection(
        groupTurnsIntoExchanges(hugeTurns, { hideLocalPaths: true, redactSecrets: true }).exchanges
      ),
      includeTools: true,
      includeThinking: false,
      includeImages: false,
      hideLocalPaths: true,
      redactSecrets: true,
    });
    assertEqual(doc.exchanges.length, 20);
    const layout = buildPaginatedShareLayout({
      doc,
      palette: LIGHT_PALETTE,
      cardWidth: 680,
      layoutMode: "single",
      showBrandFooter: true,
      measure: fixedMeasure,
      highlights: new Map(),
      loadedImages: new Map(),
      svgImages: new Map(),
      labels: TEST_LABELS,
      formattedDate: "2026-11-25",
    });

    assertEqual(layout.autoPaginated, true);
    assertTrue(layout.pages.length > 1, "huge document should auto-split into multiple pages");
    for (const page of layout.pages) {
      assertTrue(
        page.totalHeight <= MAX_SINGLE_CANVAS_LOGICAL_HEIGHT,
        `page height ${page.totalHeight} exceeded GPU cap ${MAX_SINGLE_CANVAS_LOGICAL_HEIGHT}`
      );
    }
  });

  it("formats saved display paths and sanitizes filenames cleanly", () => {
    assertEqual(formatSavedDisplayPath("/Users/alice/Downloads/card.png"), "~/Downloads/card.png");
    assertEqual(formatSavedDisplayPath("C:\\Users\\bob\\Downloads\\card.png"), "~\\Downloads\\card.png");
    assertEqual(sanitizeShareImageFilename("..foo/bar: baz?"), "foo-bar-baz");
    assertEqual(sanitizeShareImageFilename("   "), "hoxi-session");
  });
});
