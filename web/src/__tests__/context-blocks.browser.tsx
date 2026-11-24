import { createSignal } from "solid-js";
import { render } from "solid-js/web";
import { ComposerContextChips } from "../components/timeline/ComposerContextChips";
import { UserContextBlocks } from "../components/timeline/UserContextBlocks";
import {
  parseHoxiContextPrompt,
  serializeContextEnvelopes,
  type ComposerContextAttachment,
  type ContextEnvelope,
} from "../store/context-protocol";
import { assertEqual, assertTrue } from "./test-harness";

const tick = () => new Promise<void>((resolve) => setTimeout(resolve, 0));

export async function runBrowserTest(): Promise<string[]> {
  const host = document.createElement("div");
  document.body.append(host);
  let dispose: (() => void) | undefined;

  try {
    // Scenario 1: UserContextBlocks renders a light quote bar + collapsible batch entity card
    const batchEnvelope: ContextEnvelope = {
      version: 1,
      source: { appId: "1688", appName: "1688 Sourcing", tabId: "catalog", tabTitle: "Catalog" },
      items: [
        {
          kind: "entity",
          entityType: "product",
          title: "Heavyweight Cotton Hoodie",
          subtitle: "CNY 38 · MOQ 2",
          badge: "Pending",
          ref: {
            id: "78239102",
            resolve: { mcpTool: "1688_analysis_dossier", mcpArgs: { offerId: "78239102" } },
          },
          digest: { priceCny: 38, moq: 2 },
        },
        {
          kind: "entity",
          entityType: "product",
          title: "Waffle Knit Long Sleeve",
          subtitle: "CNY 45 · 48h ship",
          ref: { id: "99100231", resolve: { servicePath: "v1/catalog/products/99100231" } },
        },
      ],
    };
    const quoteEnvelope: ContextEnvelope = {
      version: 1,
      source: { appId: "ashare", appName: "Equity Research" },
      items: [
        {
          kind: "quote",
          title: "Report Excerpt",
          content: "Line 1 with <item> and ]]> marker\nLine 2 valuation\nLine 3 long risk summary that triggers clamp and expand toggle",
        },
      ],
    };
    const serialized = serializeContextEnvelopes([quoteEnvelope, batchEnvelope], "Compare both offers");
    const parsed = parseHoxiContextPrompt(serialized);
    assertEqual(parsed.hasContext, true);
    assertEqual(parsed.bodyText, "Compare both offers");

    dispose = render(
      () => <UserContextBlocks envelopes={parsed.envelopes} partId="browser-test-1" />,
      host,
    );
    await tick();

    assertTrue(!host.textContent!.includes("<hoxi-context"), "raw XML tags must never render in DOM");
    const quoteBlock = host.querySelector('[data-context-kind="quote"]');
    assertTrue(Boolean(quoteBlock), "single quote must render lightweight quote block");
    assertTrue(quoteBlock!.textContent!.includes("Line 1 with <item> and ]]> marker"), "CDATA text must decode cleanly");

    const cardBlock = host.querySelector('[data-context-kind="batch"]');
    assertTrue(Boolean(cardBlock), "batch entity envelope must render collapsible card");
    assertTrue(!cardBlock!.textContent!.includes("1688_analysis_dossier"), "batch card must default to collapsed");

    const toggleBtn = cardBlock!.querySelector<HTMLButtonElement>("button[aria-expanded]");
    assertTrue(Boolean(toggleBtn), "batch card must expose aria-expanded button");
    assertEqual(toggleBtn!.getAttribute("aria-expanded"), "false");
    toggleBtn!.click();
    await tick();
    assertEqual(toggleBtn!.getAttribute("aria-expanded"), "true");
    assertTrue(cardBlock!.textContent!.includes("1688_analysis_dossier"), "expanding card reveals resolve tool hint");
    assertTrue(cardBlock!.textContent!.includes('"priceCny": 38'), "expanding card formats JSON digest");
    assertTrue(cardBlock!.textContent!.includes("product:78239102"), "expanding card shows entity ref badge");

    toggleBtn!.click();
    await tick();
    assertEqual(toggleBtn!.getAttribute("aria-expanded"), "false");
    assertTrue(!cardBlock!.textContent!.includes("1688_analysis_dossier"), "collapsing card hides expanded details");

    dispose();
    dispose = undefined;

    // Scenario 2: ComposerContextChips renders interactive pills, opens preview dialog, and supports per-item & whole removal
    const [contexts, setContexts] = createSignal<ComposerContextAttachment[]>([
      { id: "ctx_batch", source: batchEnvelope.source, items: [...batchEnvelope.items] },
      { id: "ctx_quote", source: quoteEnvelope.source, items: [...quoteEnvelope.items] },
    ]);
    dispose = render(
      () => (
        <ComposerContextChips
          contexts={contexts()}
          onRemoveContext={(id) => setContexts((prev) => prev.filter((c) => c.id !== id))}
          onRemoveContextItem={(attId, idx) =>
            setContexts((prev) =>
              prev.flatMap((c) => {
                if (c.id !== attId) return [c];
                const nextItems = c.items.filter((_, i) => i !== idx);
                return nextItems.length > 0 ? [{ ...c, items: nextItems }] : [];
              }),
            )
          }
        />
      ),
      host,
    );
    await tick();

    assertEqual(host.querySelectorAll("[data-composer-context-chip]").length, 2, "two context chips render above composer");
    const batchChip = host.querySelector<HTMLElement>('[data-composer-context-chip="ctx_batch"]');
    assertTrue(Boolean(batchChip), "batch chip present");
    batchChip!.querySelector<HTMLButtonElement>("button")!.click();
    await tick();

    const previewDialog = host.querySelector<HTMLElement>('[data-composer-context-preview="ctx_batch"]');
    assertTrue(Boolean(previewDialog), "clicking chip opens preview popover");
    assertTrue(previewDialog!.textContent!.includes("Heavyweight Cotton Hoodie"), "preview popover shows first entity");
    assertTrue(previewDialog!.textContent!.includes("Waffle Knit Long Sleeve"), "preview popover shows second entity");

    // Remove first item from inside preview popover (list item buttons live inside the scrollable body)
    const bodyButtons = [...previewDialog!.querySelectorAll<HTMLButtonElement>(".divide-y button")];
    assertTrue(bodyButtons.length === 2, "preview popover exposes per-item remove buttons");
    bodyButtons[0].click();
    await tick();
    assertEqual(contexts()[0].items.length, 1, "removing single item updates attachment in place");

    // Remove entire quote chip via chip close button
    const quoteChip = host.querySelector<HTMLElement>('[data-composer-context-chip="ctx_quote"]');
    const quoteButtons = quoteChip!.querySelectorAll<HTMLButtonElement>("button");
    quoteButtons[quoteButtons.length - 1].click();
    await tick();
    assertEqual(host.querySelectorAll("[data-composer-context-chip]").length, 1, "removing chip unmounts it cleanly");

    return [
      "UserContextBlocks decodes CDATA quotes and defaults structured cards to collapsed",
      "UserContextBlocks toggles aria-expanded and formats entity digest + resolve hints on click",
      "ComposerContextChips previews batch items in a dialog and supports per-item and whole-chip removal",
    ];
  } finally {
    dispose?.();
    host.remove();
  }
}
