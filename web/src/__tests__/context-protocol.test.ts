import { createRoot, createSignal } from "solid-js";
import {
  extractContextTitleFallback,
  formatContextPromptForClipboard,
  formatContextPromptForExport,
  normalizeContextItems,
  parseHoxiContextPrompt,
  prettyFormatContextBody,
  serializeContextEnvelopes,
  type ContextEnvelope,
} from "../store/context-protocol";
import { extractLocalFallbackTitle } from "../store/session-title";
import {
  composerDraftKey,
  createContextAttachments,
  createPasteCollapse,
  createSessionDraftSync,
  sessionDrafts,
} from "../components/timeline/composer-drafts";
import { assertEqual, assertTrue, describe, it } from "./test-harness";

describe("Structured Context Reference Protocol (hoxi-context v1)", () => {
  it("round-trips entity references, digests, resolve pointers, and instructions losslessly", () => {
    const envelope: ContextEnvelope = {
      version: 1,
      source: { appId: "catalog", appName: "Catalog Studio", tabId: "list", tabTitle: "Products" },
      items: [
        {
          kind: "entity",
          entityType: "product",
          title: 'Heavy "Cotton" Hoodie & Tee',
          subtitle: "¥38~45 · MOQ 2 · 30d 1200",
          badge: "Pending",
          ref: {
            id: "78239102",
            uri: "hoxi://catalog/product/78239102",
            resolve: { servicePath: "v1/catalog/products/78239102" },
          },
          digest: { priceCny: 38, moq: 2, sales30d: 1200 },
        },
        {
          kind: "entity",
          entityType: "option",
          title: "NVDA 06/20 $140C",
          ref: {
            id: "NVDA-20260620-140C",
            resolve: { mcpTool: "option.get_contract", mcpArgs: { symbol: "NVDA", strike: 140 } },
          },
        },
      ],
    };

    const wire = serializeContextEnvelopes([envelope], "Compare margin & risk across these items.");
    assertTrue(wire.includes('<hoxi-context v="1" source="catalog" source-name="Catalog Studio"'));
    assertTrue(wire.includes('resolve-service="v1/catalog/products/78239102"'));
    assertTrue(wire.includes('resolve-tool="option.get_contract"'));

    const parsed = parseHoxiContextPrompt(wire);
    assertEqual(parsed.hasContext, true);
    assertEqual(parsed.bodyText, "Compare margin & risk across these items.");
    assertEqual(parsed.envelopes.length, 1);
    assertEqual(parsed.envelopes[0].source?.appName, "Catalog Studio");
    assertEqual(parsed.envelopes[0].items.length, 2);

    const first = parsed.envelopes[0].items[0];
    assertEqual(first.kind, "entity");
    assertEqual(first.entityType, "product");
    assertEqual(first.title, 'Heavy "Cotton" Hoodie & Tee');
    assertEqual(first.subtitle, "¥38~45 · MOQ 2 · 30d 1200");
    assertEqual(first.badge, "Pending");
    assertEqual(first.ref?.id, "78239102");
    assertEqual(first.ref?.uri, "hoxi://catalog/product/78239102");
    assertEqual(first.ref?.resolve?.servicePath, "v1/catalog/products/78239102");
    assertEqual(first.content, '{"priceCny":38,"moq":2,"sales30d":1200}');

    const second = parsed.envelopes[0].items[1];
    assertEqual(second.ref?.resolve?.mcpTool, "option.get_contract");
    assertEqual(second.ref?.resolve?.mcpArgs?.strike, 140);
  });

  it("safely CDATA-wraps and round-trips bodies containing closing XML tags or CDATA markers", () => {
    const hostileBody = 'Example </item> and </hoxi-context> and ]]> marker <item kind="fake">';
    const wire = serializeContextEnvelopes(
      [{ version: 1, items: [{ kind: "quote", title: "XML Snippet", content: hostileBody }] }],
      "Explain this snippet"
    );
    assertTrue(wire.includes("<![CDATA["));
    const parsed = parseHoxiContextPrompt(wire);
    assertEqual(parsed.hasContext, true);
    assertEqual(parsed.envelopes.length, 1);
    assertEqual(parsed.envelopes[0].items[0].content, hostileBody);
    assertEqual(parsed.bodyText, "Explain this snippet");
  });

  it("preserves malformed or non-context text verbatim and infers kinds for legacy inputs", () => {
    const malformed = '<hoxi-context v="1">broken unclosed text';
    const parsedMalformed = parseHoxiContextPrompt(malformed);
    assertEqual(parsedMalformed.hasContext, false);
    assertEqual(parsedMalformed.bodyText, malformed);

    const legacyItems = normalizeContextItems([
      "Just a quoted sentence",
      { title: "Workbench Snapshot", content: JSON.stringify({ a: 1, b: 2, c: 3, d: 4 }) },
    ]);
    assertEqual(legacyItems.length, 2);
    assertEqual(legacyItems[0].kind, "quote");
    assertEqual(legacyItems[1].kind, "record");

    const singleSnapshot = normalizeContextItems([
      { title: "Workbench Snapshot", content: JSON.stringify({ a: 1, b: 2, c: 3, d: 4 }) },
    ]);
    assertEqual(singleSnapshot[0].kind, "snapshot");
  });

  it("extracts clean session titles and Markdown exports without leaking XML envelope tags", () => {
    const withInst = serializeContextEnvelopes(
      [{ version: 1, source: { appName: "Scanner" }, items: [{ kind: "entity", title: "Alpha", ref: { id: "A1" } }] }],
      "Analyze Alpha breakout probability"
    );
    assertEqual(extractLocalFallbackTitle(withInst, "en-US"), "Analyze Alpha breakout probability");

    const contextOnly = serializeContextEnvelopes([
      { version: 1, items: [{ kind: "entity", title: "NVDA 06/20 $140C", subtitle: "IV 52%", ref: { id: "140C" } }] },
    ]);
    assertEqual(extractContextTitleFallback(contextOnly), "NVDA 06/20 $140C");
    assertEqual(extractLocalFallbackTitle(contextOnly, "en-US"), "NVDA 06/20 $140C");

    const exported = formatContextPromptForExport(withInst);
    assertTrue(exported.includes("<details>"));
    assertTrue(exported.includes("Scanner (1)"));
    assertTrue(exported.includes("Analyze Alpha breakout probability"));
    assertTrue(!exported.includes("<hoxi-context"));

    const clip = formatContextPromptForClipboard(withInst);
    assertTrue(clip.includes("[Scanner] Alpha"));
    assertTrue(clip.includes("Analyze Alpha breakout probability"));
    assertTrue(!clip.includes("<hoxi-context"));

    const pretty = prettyFormatContextBody('{"k":1}');
    assertEqual(pretty.isJson, true);
    assertTrue(pretty.text.includes('\n  "k": 1\n'));
  });

  it("persists composer context attachments across session switches and failed-send restoration", () => {
    createRoot((dispose) => {
      try {
        const [sid, setSid] = createSignal<string | null>("ses_ctx_a");
        const [dir] = createSignal("/tmp/ctx-test");
        const [text, setText] = createSignal("");
        const [images, setImages] = createSignal<Array<{ id: string; filename: string; mime: string; dataUrl: string }>>([]);
        const [notice, setNotice] = createSignal<string | null>(null);
        const pasteCollapse = createPasteCollapse();
        const contextAttach = createContextAttachments();

        const sync = createSessionDraftSync({
          sessionID: sid,
          directory: dir,
          text,
          setText,
          imgAttach: {
            images,
            notice,
            setNotice,
            replaceImages: setImages,
            toPromptAttachments: () => [],
          },
          pasteCollapse,
          contextAttach,
          onAfterRestore: () => {},
        });

        contextAttach.addContext({
          version: 1,
          source: { appId: "demo", appName: "Demo" },
          items: [
            { kind: "entity", title: "Item 1", ref: { id: "1" } },
            { kind: "entity", title: "Item 2", ref: { id: "2" } },
          ],
        });
        setText("Compare both");

        // Switch away -> draft saved including contexts
        setSid("ses_ctx_b");
        sync.sync();
        assertEqual(contextAttach.contexts().length, 0);
        assertEqual(text(), "");

        // Switch back -> contexts restored
        setSid("ses_ctx_a");
        sync.sync();
        assertEqual(contextAttach.contexts().length, 1);
        assertEqual(contextAttach.contexts()[0].items.length, 2);
        assertEqual(text(), "Compare both");

        // Remove single item inside attachment
        const attId = contextAttach.contexts()[0].id;
        contextAttach.removeContextItem(attId, 0);
        assertEqual(contextAttach.contexts()[0].items.length, 1);
        assertEqual(contextAttach.contexts()[0].items[0].title, "Item 2");

        // Serialize & extract round-trip
        const serialized = contextAttach.serializeIntoPrompt(text());
        assertTrue(serialized.includes("<hoxi-context"));
        assertTrue(serialized.endsWith("\n\nCompare both"));

        const extractedBody = contextAttach.extractFromPromptText(serialized, false);
        assertEqual(extractedBody, "Compare both");
        assertEqual(contextAttach.contexts().length, 1);
        assertEqual(contextAttach.contexts()[0].items[0].title, "Item 2");

        // Restore failed draft preserves contexts
        const savedContexts = contextAttach.contexts();
        contextAttach.clearContexts();
        setText("");
        sync.restoreFailedDraft(
          { text: "Compare both", images: [], pastes: [], contexts: savedContexts },
          "ses_ctx_a",
          "/tmp/ctx-test",
          "Network error"
        );
        assertEqual(contextAttach.contexts().length, 1);
        assertEqual(notice(), "Network error");

        sessionDrafts.delete(composerDraftKey("ses_ctx_a", "/tmp/ctx-test"));
        sessionDrafts.delete(composerDraftKey("ses_ctx_b", "/tmp/ctx-test"));
      } finally {
        dispose();
      }
    });
  });
});
