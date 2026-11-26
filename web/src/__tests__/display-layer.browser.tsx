// web/src/__tests__/display-layer.browser.tsx
import { render } from "solid-js/web";
import { t } from "../i18n";
import MarkdownBlocks from "../components/timeline/MarkdownBlocks";
import MarkdownView from "../components/timeline/MarkdownView";
import { parseMarkdownBlocks } from "../components/timeline/markdown-parser";
import ShellOutputView from "../components/timeline/ShellOutputView";
import JsonTreeView from "../components/timeline/JsonTreeView";
import ToolOutputView from "../components/timeline/ToolOutputView";
import FileSourceCodeView from "../components/inspector/FileSourceCodeView";
import { assertEqual, assertTrue } from "./test-harness";

const tick = () => new Promise<void>((resolve) => setTimeout(resolve, 0));

export async function runBrowserTest(): Promise<string[]> {
  const host = document.createElement("div");
  document.body.append(host);
  let dispose: (() => void) | undefined;

  try {
    // Scenario 1: MarkdownBlocks renders chart, sanitized SVG, interactive sortable/visualizable table, and <details>
    const md = [
      "```chart",
      JSON.stringify({
        type: "bar",
        title: "Build Duration",
        unit: "s",
        labels: ["Go", "Web", "Bundle"],
        series: [{ name: "Seconds", values: [12, 8, 5] }],
      }),
      "```",
      "",
      "```svg",
      '<svg viewBox="0 0 80 40" onload="window.__xss=1"><style>body { background-color: rgb(255, 0, 123) !important; }</style><script>window.__xss=2</script><rect width="80" height="40" fill="#3b82f6"/></svg>',
      "```",
      "",
      "| Service | Latency |",
      "| --- | ---: |",
      "| Cold | 2s |",
      "| Auth | 12ms |",
      "| API | 100ms |",
      "",
      "<details>",
      "<summary>Trace details</summary>",
      "",
      "Hidden diagnostic body.",
      "</details>",
    ].join("\n");

    const blocks = parseMarkdownBlocks(md);
    dispose = render(() => <MarkdownBlocks blocks={blocks} />, host);
    await tick();

    // Chart card
    const chartCard = host.querySelector<HTMLElement>('[data-hoxi-chart-card="true"]');
    assertTrue(Boolean(chartCard), "chart block renders ChartBlockCard");
    assertTrue(Boolean(chartCard!.querySelector("svg")), "chart renders interactive SVG");
    assertTrue(chartCard!.textContent!.includes("Build Duration"), "chart displays title");

    // Sanitized SVG card (isolated inside <img> data URL so <style> never leaks to host DOM)
    const svgCard = host.querySelector<HTMLElement>('[data-hoxi-svg-card="true"]');
    assertTrue(Boolean(svgCard), "svg block renders SvgBlockCard");
    const renderedImg = svgCard!.querySelector<HTMLImageElement>('img[data-hoxi-svg-img="true"]');
    assertTrue(Boolean(renderedImg), "sanitized SVG rendered via isolated <img>");
    const decodedSvg = decodeURIComponent(renderedImg!.src.replace(/^data:image\/svg\+xml;charset=utf-8,/, ""));
    assertTrue(!/<script/i.test(decodedSvg), "script tag stripped from SVG payload");
    assertTrue(!/\bonload\b/i.test(decodedSvg), "onload handler stripped from SVG payload");
    assertEqual((window as unknown as { __xss?: unknown }).__xss, undefined, "no XSS script executed");
    assertTrue(
      getComputedStyle(document.body).backgroundColor !== "rgb(255, 0, 123)",
      "SVG <style> does not leak into host document"
    );

    // Interactive Markdown table: unit-aware sort (12ms < 100ms < 2s) + visualize as chart
    const tableCard = host.querySelector<HTMLElement>('[data-hoxi-table-card="true"]');
    assertTrue(Boolean(tableCard), "GFM table renders MarkdownTableCard");
    const headers = tableCard!.querySelectorAll<HTMLTableCellElement>("thead th");
    assertEqual(headers.length, 2, "two table headers rendered");
    headers[1].click(); // sort ascending by comparable latency: 12ms, 100ms, 2s
    await tick();
    const sortedRows = Array.from(tableCard!.querySelectorAll<HTMLTableRowElement>("tbody tr"));
    assertEqual(sortedRows[0]?.querySelector("td")?.textContent?.trim(), "Auth", "12ms sorts first");
    assertEqual(sortedRows[1]?.querySelector("td")?.textContent?.trim(), "API", "100ms sorts second");
    assertEqual(sortedRows[2]?.querySelector("td")?.textContent?.trim(), "Cold", "2s sorts after 100ms");

    const chartToggleBtn = Array.from(tableCard!.querySelectorAll<HTMLButtonElement>("button")).find(
      (btn) => btn.textContent?.includes(t("timeline.tableVisualizeChart"))
    );
    assertTrue(Boolean(chartToggleBtn), "numeric table exposes inline chart toggle");
    chartToggleBtn!.click();
    await tick();
    assertTrue(Boolean(tableCard!.querySelector("svg[role='img']")), "clicking Visualize renders table as SVG chart");

    // Collapsible <details>
    const detailsEl = host.querySelector("details");
    assertTrue(Boolean(detailsEl), "details block renders native <details>");
    assertTrue(detailsEl!.textContent!.includes("Trace details"), "summary text rendered");
    assertTrue(detailsEl!.textContent!.includes("Hidden diagnostic body."), "nested body rendered");

    dispose();
    dispose = undefined;

    // Scenario 2: ShellOutputView ANSI colors + clickable file:line + deep JsonTreeView Expand All + ToolOutputView
    const rawShell = "\u001b[31mFAIL\u001b[0m internal/gateway/server.go:142 test boom";
    const jsonData = { status: "ok", nested: { deep: { leaf: "deep-leaf-value" } } };
    dispose = render(
      () => (
        <div>
          <ShellOutputView rawOutput={rawShell} plainOutput="FAIL internal/gateway/server.go:142 test boom" />
          <JsonTreeView data={jsonData} rawText={JSON.stringify(jsonData, null, 2)} />
          <ToolOutputView text="web/src/store/inspector.ts:88 match line" />
        </div>
      ),
      host
    );
    await tick();

    const preButtons = host.querySelectorAll<HTMLButtonElement>("pre button");
    assertTrue(preButtons.length >= 2, "shell and generic tool file:line render clickable buttons");
    assertEqual(preButtons[0]!.textContent?.trim(), "internal/gateway/server.go:142");
    assertEqual(preButtons[1]!.textContent?.trim(), "web/src/store/inspector.ts:88");

    const treeTextBefore = host.textContent || "";
    assertTrue(treeTextBefore.includes("status:") && treeTextBefore.includes('"ok"'), "JsonTreeView renders root keys");
    assertTrue(!treeTextBefore.includes("deep-leaf-value"), "deep JSON leaf starts collapsed");
    const expandAllBtn = Array.from(host.querySelectorAll<HTMLButtonElement>("button")).find((btn) =>
      btn.textContent?.includes(t("toolCard.jsonExpandAll"))
    );
    assertTrue(Boolean(expandAllBtn), "JsonTreeView renders Expand All button");
    expandAllBtn!.click();
    await tick();
    assertTrue((host.textContent || "").includes("deep-leaf-value"), "single Expand All reveals nested depth >= 2");

    dispose();
    dispose = undefined;

    // Scenario 3: Long code block auto-collapses & diff block renders semantic tones
    const longLines = Array.from({ length: 35 }, (_, i) => `const line${i + 1} = ${i + 1};`).join("\n");
    const codeMd = [
      "```ts",
      longLines,
      "```",
      "",
      "```diff",
      "@@ -1,2 +1,2 @@",
      "-old line",
      "+new line",
      "```",
    ].join("\n");
    dispose = render(() => <MarkdownBlocks blocks={parseMarkdownBlocks(codeMd)} />, host);
    await tick();

    const collapsedCard = host.querySelector<HTMLElement>('[data-code-collapsed="true"]');
    assertTrue(Boolean(collapsedCard), "code block >28 lines auto-collapses when not streaming");
    assertTrue(!collapsedCard!.textContent!.includes("const line35"), "collapsed block hides tail lines");
    const expandBtn = collapsedCard!.querySelector<HTMLButtonElement>('[data-code-expand-btn="true"]');
    assertTrue(Boolean(expandBtn), "collapsed block shows expand footer button");
    expandBtn!.click();
    await tick();
    assertEqual(collapsedCard!.getAttribute("data-code-collapsed"), "false", "clicking expand reveals all lines");
    assertTrue(collapsedCard!.textContent!.includes("const line35"), "expanded block displays full code");

    assertTrue(Boolean(host.querySelector('[data-diff-tone="hunk"]')), "diff hunk line tinted");
    assertTrue(Boolean(host.querySelector('[data-diff-tone="del"]')), "diff deletion line tinted");
    assertTrue(Boolean(host.querySelector('[data-diff-tone="add"]')), "diff addition line tinted");

    dispose();
    dispose = undefined;

    // Scenario 4: FileSourceCodeView delegated line-number selection -> hoxi-context v1 quote event + capture Escape isolation
    const srcLines = ["alpha()", "beta()", "gamma()", "delta()"];
    let capturedEvent: CustomEvent | null = null;
    let bubbleEscapeTriggered = false;
    const onFill = (ev: Event) => {
      capturedEvent = ev as CustomEvent;
    };
    const onBubbleEscape = (ev: KeyboardEvent) => {
      if (ev.key === "Escape" && !ev.defaultPrevented) {
        bubbleEscapeTriggered = true;
      }
    };
    window.addEventListener("keydown", onBubbleEscape);
    window.addEventListener("hoxi:composer-fill", onFill);
    try {
      dispose = render(
        () => (
          <FileSourceCodeView
            filePath="src/demo.ts"
            sourceLines={srcLines}
            visibleLines={srcLines}
            highlighted={undefined}
            changedLines={new Set()}
            highlightedLine={null}
            searchOpen={false}
            onCloseSearch={() => {}}
            onEnsureLineVisible={() => {}}
            scrollContainer={() => undefined}
          />
        ),
        host
      );
      await tick();

      const gutter2 = host.querySelector<HTMLElement>('[data-line-gutter="2"]');
      const gutter3 = host.querySelector<HTMLElement>('[data-line-gutter="3"]');
      assertTrue(Boolean(gutter2 && gutter3), "line gutters render");
      gutter2!.click();
      gutter3!.dispatchEvent(new MouseEvent("click", { bubbles: true, shiftKey: true }));
      await tick();

      // Pressing Escape on the focused document clears line selection in capture phase
      // before reaching MessageStream's global window bubble Escape handler.
      document.body.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true, cancelable: true }));
      await tick();
      assertEqual(bubbleEscapeTriggered, false, "Escape clearing selection does not leak to global bubble handler");

      // Re-select and dispatch Quote L2-3
      gutter2!.click();
      gutter3!.dispatchEvent(new MouseEvent("click", { bubbles: true, shiftKey: true }));
      await tick();

      const quoteBtn = Array.from(host.querySelectorAll<HTMLButtonElement>("button")).find((btn) =>
        btn.textContent?.includes("L2-3")
      );
      assertTrue(Boolean(quoteBtn), "Shift+click line range displays Quote L2-3 pill");
      quoteBtn!.click();
      await tick();
      assertTrue(Boolean(capturedEvent), "clicking Quote dispatches hoxi:composer-fill");
      const detail = (capturedEvent as unknown as CustomEvent).detail;
      assertEqual(detail.contexts?.[0]?.items?.[0]?.title, "src/demo.ts:L2-3");
      assertEqual(detail.contexts?.[0]?.items?.[0]?.content, "beta()\ngamma()");
    } finally {
      window.removeEventListener("keydown", onBubbleEscape);
      window.removeEventListener("hoxi:composer-fill", onFill);
    }

    dispose();
    dispose = undefined;

    // Scenario 5: Multi-image Markdown gallery + A/B compare, hoxi-cards reverse hoxi-context handoff, and ToolOutputView Card/JSON toggle
    const tinyPngA =
      "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg==";
    const tinyPngB =
      "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk+M9QDwADhgGAWjR9awAAAABJRU5ErkJggg==";
    const richMd = [
      `![Before](${tinyPngA})`,
      "",
      `![After](${tinyPngB})`,
      "",
      "```hoxi-cards",
      JSON.stringify({
        title: "Candidate Comparison",
        entities: [
          {
            id: "cand-1",
            title: "Heavyweight Hoodie",
            badge: "Top Pick",
            badgeTone: "ok",
            metrics: [
              { label: "Cost", value: "$8.20" },
              { label: "Margin", value: "44%", tone: "ok" },
            ],
            ref: { id: "cand-1", uri: "hoxi://demo/cand-1" },
          },
          {
            id: "cand-2",
            title: "Lightweight Tee",
            badge: "Draft",
            metrics: [{ label: "Cost", value: "$3.90" }],
          },
        ],
      }),
      "```",
    ].join("\n");

    let richContextEvent: CustomEvent | null = null;
    const onRichFill = (ev: Event) => {
      richContextEvent = ev as CustomEvent;
    };
    window.addEventListener("hoxi:composer-fill", onRichFill);
    try {
      const toolRichSpec = {
        schemaVersion: "hoxi.ui/v1" as const,
        title: "Pulse Snapshot",
        metrics: [{ label: "Orders", value: "48", tone: "ok" as const }],
      };
      dispose = render(
        () => (
          <div>
            <MarkdownBlocks blocks={parseMarkdownBlocks(richMd)} />
            <ToolOutputView
              richView={toolRichSpec}
              text={JSON.stringify({ orders: 48, note: "raw-json-marker" })}
            />
          </div>
        ),
        host
      );
      await tick();

      const galleryEl = host.querySelector<HTMLElement>('[data-hoxi-media-gallery="true"]');
      assertTrue(Boolean(galleryEl), "consecutive image paragraphs render MediaGalleryView");
      assertEqual(
        galleryEl!.querySelectorAll('[data-hoxi-gallery-tile="true"]').length,
        2,
        "gallery renders both image tiles"
      );
      const compareBtn = Array.from(galleryEl!.querySelectorAll<HTMLButtonElement>("button")).find(
        (btn) => btn.textContent?.includes(t("timeline.galleryCompareBtn"))
      );
      assertTrue(Boolean(compareBtn), "2-image gallery exposes Compare A/B button");
      compareBtn!.click();
      await tick();
      assertTrue(
        Boolean(galleryEl!.querySelector('[data-hoxi-gallery-compare="true"]')),
        "clicking Compare A/B opens split slider comparison"
      );

      const richCardBlock = host.querySelector<HTMLElement>('[data-hoxi-rich-block-card="true"]');
      assertTrue(Boolean(richCardBlock), "hoxi-cards block renders RichBlockCard");
      const firstEntity = richCardBlock!.querySelector<HTMLElement>('[data-hoxi-entity-card="true"]');
      assertTrue(Boolean(firstEntity), "entity cards render inside RichBlockCard");
      const selectBtn = firstEntity!.querySelector<HTMLButtonElement>("button[aria-pressed]");
      assertTrue(Boolean(selectBtn), "entity card exposes checkbox button");
      selectBtn!.click();
      await tick();
      const sendSelectedBtn = richCardBlock!.querySelector<HTMLButtonElement>(
        '[data-hoxi-rich-send-selected="true"]'
      );
      assertTrue(Boolean(sendSelectedBtn), "selecting an entity shows Send to Chat action");
      sendSelectedBtn!.click();
      await tick();
      assertTrue(Boolean(richContextEvent), "Send to Chat dispatches hoxi:composer-fill");
      const richDetail = (richContextEvent as unknown as CustomEvent).detail;
      assertEqual(richDetail.contexts?.[0]?.items?.[0]?.title, "Heavyweight Hoodie");
      assertEqual(richDetail.contexts?.[0]?.items?.[0]?.ref?.uri, "hoxi://demo/cand-1");

      // ToolOutputView defaults to Card view and toggles to JSON tree
      assertTrue(host.textContent!.includes("Pulse Snapshot"), "ToolOutputView renders rich card by default");
      assertTrue(!host.textContent!.includes("raw-json-marker"), "raw JSON hidden while Card tab active");
      const jsonTabBtn = Array.from(host.querySelectorAll<HTMLButtonElement>("button")).find(
        (btn) => btn.textContent?.trim() === t("toolCard.jsonViewTab")
      );
      assertTrue(Boolean(jsonTabBtn), "ToolOutputView renders JSON toggle button");
      jsonTabBtn!.click();
      await tick();
      assertTrue(host.textContent!.includes("raw-json-marker"), "switching to JSON tab reveals JsonTreeView");
    } finally {
      window.removeEventListener("hoxi:composer-fill", onRichFill);
    }

    dispose?.();
    dispose = undefined;

    // Scenario 6: Portal-mode MarkdownView renders Chart + Rich cards cleanly while hiding desktop Source/Send-to-Chat actions and blocking desktop-only thumbnails
    const portalMd = [
      "```chart",
      JSON.stringify({
        type: "bar",
        title: "PM Funnel",
        labels: ["Visit", "Pay"],
        series: [{ name: "Users", values: [100, 28] }],
      }),
      "```",
      "",
      "```hoxi-cards",
      JSON.stringify({
        title: "Option Comparison",
        entities: [
          {
            title: "Plan A",
            subtitle: "Fast ship",
            thumbnailUrl: "/api/hoxi/file/content?path=.env",
            metrics: [{ label: "ETA", value: "2d" }],
          },
          {
            title: "Plan B",
            subtitle: "Safe portal asset",
            thumbnailUrl: "/api/hoxi/collab/portal/attachments/att_1?token=tok",
            metrics: [{ label: "ETA", value: "4d" }],
          },
        ],
      }),
      "```",
    ].join("\n");

    dispose = render(() => <MarkdownView content={portalMd} density="chat" portal />, host);
    await tick();

    const portalChart = host.querySelector<HTMLElement>('[data-hoxi-chart-card="true"]');
    assertTrue(Boolean(portalChart), "portal MarkdownView renders ChartBlockCard");
    assertTrue(Boolean(portalChart!.querySelector("svg")), "portal chart renders interactive SVG");
    const portalChartSourceBtn = Array.from(portalChart!.querySelectorAll<HTMLButtonElement>("button")).find(
      (btn) => btn.textContent?.trim() === t("inspector.files.sourceMode")
    );
    assertEqual(portalChartSourceBtn, undefined, "portal ChartBlockCard hides Source tab");

    const portalRich = host.querySelector<HTMLElement>('[data-hoxi-rich-block-card="true"]');
    assertTrue(Boolean(portalRich), "portal MarkdownView renders RichBlockCard");
    const portalRichSourceBtn = Array.from(portalRich!.querySelectorAll<HTMLButtonElement>("button")).find(
      (btn) => btn.textContent?.trim() === t("inspector.files.sourceMode")
    );
    assertEqual(portalRichSourceBtn, undefined, "portal RichBlockCard hides Source tab");
    assertEqual(
      portalRich!.querySelector('button[aria-pressed]'),
      null,
      "portal StructuredRichView disables desktop entity selection"
    );
    const portalRichImgs = Array.from(portalRich!.querySelectorAll<HTMLImageElement>("img")).map((el) =>
      el.getAttribute("src") || ""
    );
    assertTrue(
      !portalRichImgs.some((src) => src.includes("/api/hoxi/file/content")),
      "portal StructuredRichView blocks desktop /api/hoxi/file/content thumbnails"
    );
    assertTrue(
      portalRichImgs.some((src) => src.includes("/api/hoxi/collab/portal/attachments/att_1")),
      "portal StructuredRichView preserves safe portal attachment thumbnails"
    );
    // Portal mode must hide raw source Copy buttons on Chart and Rich cards
    const portalChartCopyBtn = portalChart!.querySelector(`button[title="${t("common.copy")}"]`);
    const portalRichCopyBtn = portalRich!.querySelector(`button[title="${t("common.copy")}"]`);
    assertEqual(portalChartCopyBtn, null, "portal ChartBlockCard hides raw-source Copy button");
    assertEqual(portalRichCopyBtn, null, "portal RichBlockCard hides raw-source Copy button");

    // Portal mode must NEVER expose raw <pre><code> DSL/JSON/SVG source during pending or failed visual renders
    dispose();
    const portalFallbackMd = [
      "```mermaid",
      "flowchart LR",
      "  SECRET_DSL_NODE --> NEXT_NODE",
      "```",
      "",
      "```chart",
      "{invalid_chart_json_secret}",
      "```",
      "",
      "```hoxi-cards",
      "{invalid_cards_json_secret}",
      "```",
      "",
      "```svg",
      "<script>SECRET_SVG_PAYLOAD</script>",
      "```",
    ].join("\n");
    dispose = render(() => <MarkdownView content={portalFallbackMd} density="chat" portal />, host);
    await tick();

    assertEqual(host.querySelectorAll("pre code").length, 0, "portal visual fallbacks never render raw <pre><code>");
    assertTrue(
      !host.textContent?.includes("SECRET_DSL_NODE") &&
        !host.textContent?.includes("invalid_chart_json_secret") &&
        !host.textContent?.includes("invalid_cards_json_secret") &&
        !host.textContent?.includes("SECRET_SVG_PAYLOAD"),
      "portal visual fallbacks never leak raw DSL, JSON, or SVG source into DOM"
    );
    assertEqual(
      host.querySelector(`button[title="${t("common.copy")}"]`),
      null,
      "portal visual cards hide Copy buttons across mermaid/chart/rich/svg"
    );

    return [
      "ChartBlockCard renders interactive SVG bar chart from declarative JSON",
      "SvgBlockCard sanitizes <script> and onload in real browser DOM",
      "MarkdownTableCard sorts numerically and visualizes tables as inline SVG charts",
      "MarkdownBlocks renders native collapsible <details><summary>",
      "ShellOutputView + ToolOutputView linkify file:line and JsonTreeView renders structured JSON",
      "CodeBlockCard auto-collapses long code blocks and tints diff blocks",
      "FileSourceCodeView line-range click quotes exact lines into hoxi-context v1",
      "MediaGalleryView renders auto-grouped images with A/B slider, RichBlockCard sends hoxi-context v1, and ToolOutputView toggles Card/JSON",
      "Portal MarkdownView renders charts and rich cards while hiding desktop Source/Send-to-Chat controls and blocking desktop-only thumbnails",
    ];
  } finally {
    dispose?.();
    host.remove();
  }
}
