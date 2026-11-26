// web/src/__tests__/display-layer.test.ts
import { assertTrue as assert, assertEqual, describe, it } from "./test-harness";
import {
  computeNiceTicks,
  extractChartFromMarkdownTable,
  formatChartNumber,
  markdownTableToCsv,
  markdownTableToMarkdown,
  parseChartSpec,
  parseNumericCell,
} from "../components/timeline/chart-spec";
import {
  hasCompleteSvgMarkup,
  sanitizeSvgMarkup,
  svgToDataURL,
} from "../components/timeline/svg-sanitizer";
import {
  parseShellOutputRuns,
  processCarriageReturnsPreservingAnsi,
} from "../components/timeline/ansi-output";
import {
  createIncrementalMarkdownParser,
  isSameBlockToken,
  parseMarkdownBlocks,
} from "../components/timeline/markdown-parser";
import { tryParseStructuredJson } from "../components/timeline/JsonTreeView";
import { diffLineTone } from "../components/timeline/CodeBlockCard";
import {
  findMatchingLineIndices,
  formatLineRangeLabel,
} from "../components/inspector/FileSourceCodeView";
import { extractToolErrorBadge, extractToolErrorText } from "../components/timeline/ToolOutputView";
import { isJsonPath } from "../components/inspector/file-tree-helpers";
import {
  canvasPreview,
  canvasPreviewHistory,
  clearCanvasPreview,
  isChartCodeBlockLang,
  isSvgCodeBlockLang,
  openCanvasPreview,
  selectCanvasPreview,
} from "../store/document-viewer";
import { workspace } from "../store/workspace-state";

describe("display layer — declarative chart spec & table visualization", () => {
  it("parses canonical Hoxi chart JSON", () => {
    const spec = parseChartSpec(
      JSON.stringify({
        type: "line",
        title: "Latency P95",
        unit: "ms",
        labels: ["Mon", "Tue", "Wed"],
        series: [
          { name: "Before", values: [120, 115, 130], color: "#ef4444" },
          { name: "After", values: [42, 39, 45], color: "javascript:alert(1)" },
        ],
      })
    );
    assert(spec !== null, "expected parsed chart spec");
    assertEqual(spec!.type, "line");
    assertEqual(spec!.title, "Latency P95");
    assertEqual(spec!.unit, "ms");
    assertEqual(spec!.series.length, 2);
    assertEqual(spec!.series[0].color, "#ef4444");
    assertEqual(spec!.series[1].color, undefined);
  });

  it("parses Chart.js style datasets and flat object arrays", () => {
    const chartJs = parseChartSpec(
      JSON.stringify({
        type: "doughnut",
        data: {
          labels: ["Go", "TS", "Rust"],
          datasets: [{ label: "LOC", data: [60, 30, 10] }],
        },
        options: { plugins: { title: { text: "Languages" } } },
      })
    );
    assert(chartJs !== null, "expected Chart.js schema to parse");
    assertEqual(chartJs!.type, "donut");
    assertEqual(chartJs!.title, "Languages");
    assertEqual(chartJs!.series[0].values[0], 60);

    const flat = parseChartSpec(
      JSON.stringify({
        type: "horizontal-bar",
        data: [
          { stage: "Build", ms: 420 },
          { stage: "Test", ms: 980 },
        ],
      })
    );
    assert(flat !== null, "expected object array schema to parse");
    assertEqual(flat!.type, "horizontal-bar");
    assertEqual(flat!.labels[1], "Test");
    assertEqual(flat!.series[0].name, "ms");
    assertEqual(flat!.series[0].values[1], 980);
  });

  it("rejects invalid or non-numeric chart JSON cleanly", () => {
    assertEqual(parseChartSpec("not json"), null);
    assertEqual(parseChartSpec(JSON.stringify({ labels: ["A"], series: [] })), null);
    assertEqual(parseChartSpec(JSON.stringify({ labels: ["A"], values: ["foo", "bar"] })), null);
  });

  it("extracts visualizable charts from numeric Markdown tables and skips prose tables", () => {
    assertEqual(parseNumericCell("$1,250.5")?.value, 1250.5);
    assertEqual(parseNumericCell("$1,250.5")?.unit, "$");
    assertEqual(parseNumericCell("**88.4%**")?.value, 88.4);
    assertEqual(parseNumericCell("**88.4%**")?.unit, "%");

    const temporal = extractChartFromMarkdownTable(
      ["Month", "Revenue", "Cost"],
      [
        ["2026-01", "$12,000", "$8,000"],
        ["2026-02", "$15,500", "$9,100"],
        ["2026-03", "$19,200", "$9,800"],
      ]
    );
    assert(temporal !== null, "expected numeric table to produce chart spec");
    assertEqual(temporal!.type, "line");
    assertEqual(temporal!.unit, "$");
    assertEqual(temporal!.series.length, 2);

    const prose = extractChartFromMarkdownTable(
      ["Component", "Role"],
      [
        ["Gateway", "Routes requests"],
        ["Runner", "Manages agent sessions"],
      ]
    );
    assertEqual(prose, null);

    // Currency + magnitude suffixes fold scale cleanly instead of dropping M/K.
    assertEqual(parseNumericCell("$1.2M")?.value, 1_200_000);
    assertEqual(parseNumericCell("$1.2M")?.unit, "$");
    assertEqual(parseNumericCell("$500K")?.value, 500_000);
    assert(parseNumericCell("$1.2M")!.comparableValue > parseNumericCell("$500K")!.comparableValue, "$1.2M > $500K");

    // Mixed units in the same dimensional family normalize to a common base unit.
    const mixedTime = extractChartFromMarkdownTable(
      ["Step", "Latency"],
      [
        ["Cache", "100ms"],
        ["Cold", "2s"],
      ]
    );
    assert(mixedTime !== null, "mixed ms/s table extracts chart");
    assertEqual(mixedTime!.unit, "ms");
    assertEqual(mixedTime!.series[0].values[0], 100);
    assertEqual(mixedTime!.series[0].values[1], 2000);

    const mixedBytes = extractChartFromMarkdownTable(
      ["Asset", "Size"],
      [
        ["Icon", "800KB"],
        ["Bundle", "1.5MB"],
      ]
    );
    assert(mixedBytes !== null, "mixed KB/MB table extracts chart");
    assertEqual(mixedBytes!.unit, "KB");
    assertEqual(mixedBytes!.series[0].values[0], 800);
    assertEqual(mixedBytes!.series[0].values[1], 1536);

    const mixedScale = extractChartFromMarkdownTable(
      ["Item", "Count"],
      [
        ["Small", "500"],
        ["Large", "1.5K"],
      ]
    );
    assert(mixedScale !== null, "mixed bare/K table extracts chart");
    assertEqual(mixedScale!.series[0].values[0], 500);
    assertEqual(mixedScale!.series[0].values[1], 1500);
  });

  it("formats chart numbers, nice ticks, CSV, and Markdown exports", () => {
    assertEqual(formatChartNumber(15000, "$"), "$15K");
    assertEqual(formatChartNumber(42.5, "%"), "42.5%");
    const ticks = computeNiceTicks(0, 93, 4);
    assert(ticks[0] === 0 && ticks[ticks.length - 1] >= 93, "ticks must cover data range");

    const csv = markdownTableToCsv(["Name", "Note"], [["A", 'Hello, "world"']]);
    assertEqual(csv, 'Name,Note\nA,"Hello, ""world"""');

    const md = markdownTableToMarkdown(["Col"], [["a|b"]]);
    assert(md.includes("a\\|b"), "pipe must be escaped in Markdown table export");
  });
});

describe("display layer — inline SVG sanitization", () => {
  it("strips scripts, foreignObject, event handlers, and external URLs while keeping safe vector elements", () => {
    const malicious = `
      <svg viewBox="0 0 100 100" onload="alert(1)">
        <defs><linearGradient id="g1"><stop offset="0%" stop-color="#3b82f6"/></linearGradient></defs>
        <style>@import url("https://evil.test/x.css"); .a { fill: url(#g1); background: u/*x*/rl(https://evil.test/bg.png); border-image: url('https://evil.test/unclosed); }</style>
        <script>alert("xss")</script>
        <foreignObject><iframe src="https://evil.test"></iframe></foreignObject>
        <rect class="a" x="0" y="0" width="100" height="100" onclick="steal()" fill="url(#g1)" />
        <a href="javascript:alert(2)"><circle cx="50" cy="50" r="20" /></a>
        <use xlink:href="https://evil.test/sprite.svg#x" />
        <use href="#g1" />
      </svg>
    `;
    assert(hasCompleteSvgMarkup(malicious), "complete svg detected");
    const res = sanitizeSvgMarkup(malicious);
    assert(res.svg.includes("<svg"), "root svg preserved");
    assert(res.svg.includes("url(#g1)"), "internal fragment reference preserved");
    assert(!/<script/i.test(res.svg), "script stripped");
    assert(!/foreignobject/i.test(res.svg), "foreignObject stripped");
    assert(!/<iframe/i.test(res.svg), "iframe stripped");
    assert(!/\bonload\b/i.test(res.svg), "onload stripped");
    assert(!/\bonclick\b/i.test(res.svg), "onclick stripped");
    assert(!/javascript:/i.test(res.svg), "javascript URL stripped");
    assert(!/evil\.test/i.test(res.svg), "external URL stripped");
    assert(svgToDataURL(res.svg).startsWith("data:image/svg+xml;charset=utf-8,"), "data URL encoded");
  });

  it("rejects incomplete SVG during streaming", () => {
    assertEqual(hasCompleteSvgMarkup("<svg viewBox='0 0 10 10'><rect"), false);
    const res = sanitizeSvgMarkup("<svg viewBox='0 0 10 10'><rect");
    assertEqual(res.svg, "");
  });
});

describe("display layer — shell ANSI coloring & clickable file:line refs", () => {
  it("preserves SGR colors while resolving carriage returns and extracting file:line refs", () => {
    const raw =
      "progress 50%\r\u001b[31;1mFAIL\u001b[0m internal/gateway/server.go:142:9 check failed\n" +
      "see https://example.com/app.js:8080 for docs";
    const preserved = processCarriageReturnsPreservingAnsi(raw);
    assert(!preserved.includes("progress 50%"), "carriage return overwrite resolved");
    assert(preserved.includes("\u001b[31;1m"), "SGR color preserved");

    const runs = parseShellOutputRuns(raw);
    const failRun = runs.find((r) => r.text === "FAIL");
    assert(Boolean(failRun?.style?.fg && failRun?.style?.bold), "FAIL styled with red bold");

    const fileRun = runs.find((r) => r.fileRef?.path === "internal/gateway/server.go");
    assert(Boolean(fileRun), "workspace file:line detected");
    assertEqual(fileRun?.fileRef?.line, 142);

    const urlFalsePositive = runs.find((r) => r.fileRef && r.text.includes("example.com"));
    assertEqual(urlFalsePositive, undefined);
  });

  it("applies subtle semantic PASS/FAIL coloring on monochrome output", () => {
    const runs = parseShellOutputRuns("--- PASS: TestOK (0.01s)\n--- FAIL: TestBad (0.02s)");
    assertEqual(runs[0].style?.fg, "#3fb950");
    const failRun = runs.find((r) => r.text.startsWith("--- FAIL:"));
    assertEqual(failRun?.style?.fg, "#ff7b72");
  });
});

describe("display layer — Markdown <details> blocks & structured JSON detection", () => {
  it("parses collapsible <details><summary> blocks including fenced code inside", () => {
    const md = [
      "Intro paragraph.",
      "<details open>",
      "<summary>Show **logs**</summary>",
      "",
      "```go",
      "// </details> inside code fence must not close outer block",
      "fmt.Println(1)",
      "```",
      "</details>",
      "After.",
    ].join("\n");

    const blocks = parseMarkdownBlocks(md);
    assertEqual(blocks.length, 3);
    assertEqual(blocks[0].type, "paragraph");
    assertEqual(blocks[1].type, "details");
    const det = blocks[1] as Extract<(typeof blocks)[number], { type: "details" }>;
    assertEqual(det.summary, "Show **logs**");
    assertEqual(det.open, true);
    assert(det.body.includes("fmt.Println(1)"), "body preserves code fence");
    assertEqual(blocks[2].type, "paragraph");

    assert(isSameBlockToken(det, { ...det }), "isSameBlockToken matches details");
    const inc = createIncrementalMarkdownParser();
    const streamed = inc(md);
    assertEqual(streamed.length, 3);
    assertEqual(streamed[1].type, "details");

    // Nested single-line <details> must not prematurely close outer <details>.
    const nestedMd = [
      "<details>",
      "<summary>Outer</summary>",
      "<details><summary>Inner</summary>Inside</details>",
      "Still outer",
      "</details>",
      "Done",
    ].join("\n");
    const nestedBlocks = parseMarkdownBlocks(nestedMd);
    assertEqual(nestedBlocks.length, 2);
    assertEqual(nestedBlocks[0].type, "details");
    const outer = nestedBlocks[0] as Extract<(typeof nestedBlocks)[number], { type: "details" }>;
    assert(outer.body.includes("Still outer"), "outer details stays open across single-line nested details");
  });

  it("detects structured JSON objects/arrays while rejecting primitives", () => {
    assert(tryParseStructuredJson('{"ok":true,"items":[1,2]}') !== null, "object detected");
    assert(tryParseStructuredJson('[1,2,3]') !== null, "array detected");
    assertEqual(tryParseStructuredJson('"plain string"'), null);
    assertEqual(tryParseStructuredJson("42"), null);
    assertEqual(tryParseStructuredJson("{invalid"), null);
  });
});

describe("display layer — code block language detection & side canvas history", () => {
  it("recognizes svg and chart fenced block languages", () => {
    assert(isSvgCodeBlockLang("svg"), "svg");
    assert(isSvgCodeBlockLang("SVG"), "SVG");
    assert(!isSvgCodeBlockLang("html"), "html is not svg");
    assert(isChartCodeBlockLang("chart"), "chart");
    assert(isChartCodeBlockLang("hoxi-chart"), "hoxi-chart");
    assert(isChartCodeBlockLang("chartjs"), "chartjs");
    assert(!isChartCodeBlockLang("json"), "json is not chart");
  });

  it("docks HTML/SVG versions in Inspector canvas and supports version switching", () => {
    clearCanvasPreview();
    openCanvasPreview({ kind: "html", title: "Demo v1", content: "<h1>v1</h1>", sessionID: "ses_1" });
    assertEqual(workspace.inspectorOpen, true);
    assertEqual(workspace.inspectorTab, "preview");
    const firstId = canvasPreview()?.id;
    assert(Boolean(firstId), "first canvas entry created");

    openCanvasPreview({ kind: "html", title: "Demo v2", content: "<h1>v2</h1>", sessionID: "ses_1" });
    assertEqual(canvasPreviewHistory().length, 2);
    assertEqual(canvasPreview()?.title, "Demo v2");

    // Re-opening identical content reuses existing entry instead of duplicating.
    openCanvasPreview({ kind: "html", title: "Demo v1", content: "<h1>v1</h1>", sessionID: "ses_1" });
    assertEqual(canvasPreviewHistory().length, 2);
    assertEqual(canvasPreview()?.id, firstId);

    selectCanvasPreview(canvasPreviewHistory()[1].id);
    assertEqual(canvasPreview()?.title, "Demo v2");
    clearCanvasPreview();
  });
});

describe("display layer — code diff tinting, in-file search, line quote, and tool error badge", () => {
  it("classifies diff code lines without misclassifying +++/--- headers", () => {
    assertEqual(diffLineTone("@@ -10,4 +10,6 @@"), "hunk");
    assertEqual(diffLineTone("+const a = 1;"), "add");
    assertEqual(diffLineTone("-const a = 0;"), "del");
    assertEqual(diffLineTone("+++ b/file.ts"), null);
    assertEqual(diffLineTone("--- a/file.ts"), null);
    assertEqual(diffLineTone(" context line"), null);
  });

  it("finds 1-based matching lines case-insensitively and formats line ranges", () => {
    const lines = ["package main", "func Hello() {", "  println(\"hello\")", "}"];
    const matches = findMatchingLineIndices(lines, "HELLO");
    assertEqual(matches.join(","), "2,3");
    assertEqual(formatLineRangeLabel(10, 10), "10");
    assertEqual(formatLineRangeLabel(24, 10), "10-24");
    assertEqual(isJsonPath("hoxi.json"), true);
    assertEqual(isJsonPath("main.go"), false);
  });

  it("extracts concise failure badge for collapsed failed ToolCards", () => {
    const shellErr = {
      id: "prt_1",
      sessionID: "ses_1",
      messageID: "msg_1",
      type: "tool" as const,
      callID: "call_1",
      tool: "bash",
      state: { status: "error" as const, input: {}, error: "command failed", time: { start: 1, end: 2 } },
    };
    assertEqual(extractToolErrorBadge(shellErr, 2), "exit 2");
    assertEqual(extractToolErrorBadge(shellErr), "command failed");
    assertEqual(extractToolErrorText(shellErr), "command failed");

    const hookTimeoutMsg =
      'tool call denied by pre-tool hook: timed out waiting for another active session in this workspace to finish modifying "collab.ts"';
    const editHookErr = {
      ...shellErr,
      tool: "edit_file",
      state: { status: "error" as const, input: { Path: "work/web/store/collab.ts" }, error: hookTimeoutMsg, time: { start: 1, end: 2 } },
    };
    const hookBadge = extractToolErrorBadge(editHookErr);
    assert(hookBadge.startsWith("timed out waiting for another active"), `expected stripped pre-tool hook reason, got: ${hookBadge}`);
    assertEqual(hookBadge.length, 44);
    assertEqual(extractToolErrorText(editHookErr), hookTimeoutMsg);

    const stackedPrefixErr = {
      ...editHookErr,
      state: { status: "error" as const, input: {}, error: "pre-tool hook denied: error: target string not found", time: { start: 1, end: 2 } },
    };
    assertEqual(extractToolErrorBadge(stackedPrefixErr), "target string not found");

    const barePrefixErr = {
      ...editHookErr,
      state: { status: "error" as const, input: {}, error: "tool call denied by pre-tool hook: ", time: { start: 1, end: 2 } },
    };
    assertEqual(extractToolErrorBadge(barePrefixErr), "tool call denied by pre-tool hook:");

    const okPart = {
      ...shellErr,
      state: { status: "completed" as const, input: {}, output: "ok", title: "", metadata: {}, time: { start: 1, end: 2 } },
    };
    assertEqual(extractToolErrorBadge(okPart, 1), "");
    assertEqual(extractToolErrorText(okPart), "");
  });

  it("linkifies plain tool output paths without semantic PASS/FAIL coloring when semanticColors is false", () => {
    const runs = parseShellOutputRuns("ok web/src/app.tsx:42 match", { semanticColors: false });
    assertEqual(runs[0].style, undefined);
    const link = runs.find((r) => r.fileRef?.path === "web/src/app.tsx");
    assertEqual(link?.fileRef?.line, 42);
  });
});
