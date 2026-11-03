import {
  advanceStreamingSlice,
  createIncrementalMarkdownParser,
  inlineCodeRenderer,
  isInlineCodeFileChip,
  parseInline,
  parseMarkdownBlocks,
} from "../components/timeline/markdown-parser";
import { createStreamPacer, holdIncompleteTableRows, initialRevealSlice } from "../components/timeline/markdown/reveal";
import { expandInlineThinkingParts } from "../components/timeline/context-activity-parts";
import { createTailHoldState } from "../components/timeline/tail-hold";
import { assertDeepEqual, assertEqual, assertTrue, describe, it } from "./test-harness";

const FRAME = 1000 / 60;

/** Feeds `chunk` chars every `intervalMs` and records chars revealed per frame. */
function simulate(text: string, chunk: number, intervalMs: number) {
  const pacer = createStreamPacer("", 0);
  const perFrame: number[] = [];
  const slices: string[] = [];
  let sent = 0;
  let nextArrival = 0;
  let lastArrival = 0;
  for (let t = 0; pacer.shown < text.length && t < 60_000; t += FRAME) {
    while (sent < text.length && t >= nextArrival) {
      sent = Math.min(text.length, sent + chunk);
      pacer.push(text.slice(0, sent), t);
      lastArrival = t;
      nextArrival += intervalMs;
    }
    const before = pacer.shown;
    const after = pacer.step(t);
    perFrame.push(after - before);
    slices.push(text.slice(0, after));
    if (pacer.shown >= text.length) return { perFrame, slices, lastArrival, finishedAt: t };
  }
  return { perFrame, slices, lastArrival, finishedAt: Infinity };
}

describe("stream pacer", () => {
  it("reveals steadily between network chunks instead of bursting and stalling", () => {
    const text =
      "\u8fd9\u662f\u4e00\u6bb5\u7528\u4e8e\u9a8c\u8bc1\u5300\u901f\u51fa\u5b57\u7684\u4e2d\u6587\u6587\u672c\uff0c\u8282\u594f\u5e94\u5f53\u8fde\u7eed\u800c\u4e0d\u662f\u4e00\u987f\u4e00\u987f\u5730\u8e66\u51fa\u6765\u3002".repeat(
        12
      );
    const { perFrame } = simulate(text, 25, 100);
    const steady = perFrame.slice(30, perFrame.length - 10);
    const idle = steady.filter((n) => n === 0).length / steady.length;
    assertTrue(idle < 0.2, `idle frame ratio ${idle.toFixed(2)} should stay under 0.2`);
    assertTrue(
      Math.max(...steady) <= 8,
      `max chars per frame ${Math.max(...steady)} should stay small`
    );
  });

  it("never stalls on subsequent frames after snapping past inline tokens", () => {
    const text =
      "\u524d\u7f00 **\u91cd\u70b9\u5185\u5bb9** \u4e0e `web/src/components/timeline/MarkdownView.tsx` \u4ee5\u53ca [\u94fe\u63a5](https://hoxi.ai) \u540e\u7ee7\u7eed\u6d41\u7545\u8f93\u51fa\u666e\u901a\u6587\u5b57\u3002";
    const pacer = createStreamPacer("", 0);
    pacer.push(text, 0);
    let maxConsecutiveZeroFrames = 0;
    let currentZeroRun = 0;
    const slices: string[] = [];
    for (let t = FRAME; pacer.pending && t < 10_000; t += FRAME) {
      const before = pacer.shown;
      const after = pacer.step(t);
      if (after === before) {
        currentZeroRun++;
        maxConsecutiveZeroFrames = Math.max(maxConsecutiveZeroFrames, currentZeroRun);
      } else {
        currentZeroRun = 0;
      }
      slices.push(text.slice(0, after));
    }
    assertTrue(
      maxConsecutiveZeroFrames <= 1,
      `pacer stalled for ${maxConsecutiveZeroFrames} consecutive frames after token snap`
    );
    for (const slice of slices) {
      assertTrue(!/\*\*[^*]*\*$/.test(slice), `half-closed bold: ${slice.slice(-12)}`);
      assertTrue(!/\]\(https?:\/\/[^)]*$/.test(slice), `half-open link: ${slice.slice(-20)}`);
    }
  });

  it("finishes shortly after the last chunk arrives", () => {
    const text = "x".repeat(400);
    const { lastArrival, finishedAt } = simulate(text, 40, 80);
    assertTrue(
      finishedAt - lastArrival <= 160,
      `drained ${finishedAt - lastArrival}ms after last chunk`
    );
  });

  it("catches up quickly on a large backlog", () => {
    const pacer = createStreamPacer("", 0);
    pacer.push("y".repeat(3000), 0);
    let frames = 0;
    for (let t = FRAME; pacer.pending && frames < 200; t += FRAME) {
      pacer.step(t);
      frames++;
    }
    assertTrue(frames <= 60, `3000-char backlog took ${frames} frames`);
  });

  it("rejects content that is not an append so the view can jump", () => {
    const pacer = createStreamPacer("hello world", 0);
    assertTrue(pacer.push("hello world!", 10));
    assertEqual(pacer.push("goodbye", 20), false);
    pacer.reset("goodbye", 20);
    assertEqual(pacer.shown, 7);
    assertEqual(pacer.pending, false);
  });

  it("reveals a table row as a whole line, and a header with its separator and first row", () => {
    const doc = "intro\n| a | b |\n|---|---|\n| 1 | 2 |\n| 3 | 4 |";
    const rowStart = doc.indexOf("| a");
    assertEqual(advanceStreamingSlice(doc, rowStart, rowStart + 2), doc.indexOf("| 3"));
    const lastRow = doc.lastIndexOf("| 3");
    assertEqual(advanceStreamingSlice(doc, lastRow, lastRow + 1), doc.length);
  });
});

describe("incremental markdown streaming", () => {
  it("matches full parsing as content arrives in small network chunks", () => {
    const parse = createIncrementalMarkdownParser();
    const source = Array.from(
      { length: 12 },
      (_, i) =>
        `### Section ${i}\r\n\r\nA paragraph with **bold** and \`code\`.\r\n\r\n- first\r\n- second\r\n\r\n`
    ).join("");
    for (let end = 1; end < source.length; end += 13) {
      const content = source.slice(0, end);
      assertDeepEqual(parse(content), parseMarkdownBlocks(content));
    }
    assertDeepEqual(parse(source), parseMarkdownBlocks(source));
  });

  it("preserves completed paragraphs and list items while the tail grows", () => {
    const parse = createIncrementalMarkdownParser();
    const before = parse("Stable paragraph\n\n- first\n- sec");
    const after = parse("Stable paragraph\n\n- first\n- second");
    assertEqual(after[0], before[0]);
    if (before[1].type !== "list" || after[1].type !== "list") throw new Error("expected list");
    assertEqual(after[1].items[0], before[1].items[0]);
    assertEqual(before[1].items[1].text, "sec");
    assertEqual(after[1].items[1].text, "second");
  });
});

describe("streamed inline code spans", () => {
  type CodeSpan = { type: "code"; text: string; unclosed?: boolean };
  const codeSpans = (text: string) =>
    parseInline(text).filter((span): span is CodeSpan => span.type === "code");

  it("keeps file names on one renderer from the first character to the closing backtick", () => {
    const source = "See `ThoughtBox.tsx` and `web/src/services/sse.ts`, then `https://hoxi.ai`";
    const seen = new Map<number, Set<string>>();
    for (let end = 1; end <= source.length; end++) {
      codeSpans(source.slice(0, end)).forEach((span, index) => {
        const kinds = seen.get(index) ?? new Set<string>();
        kinds.add(inlineCodeRenderer(span));
        seen.set(index, kinds);
      });
    }
    assertDeepEqual([...(seen.get(0) ?? [])], ["code"]);
    assertDeepEqual([...(seen.get(1) ?? [])], ["code"]);
    // A URL only becomes a link once it is closed.
    assertDeepEqual([...(seen.get(2) ?? [])], ["code", "url"]);
  });

  it("shows a file chip only for candidates, preferring a known check result", () => {
    assertEqual(isInlineCodeFileChip("ThoughtBox.tsx", undefined), false);
    assertEqual(isInlineCodeFileChip("ThoughtBox.tsx", true), true);
    assertEqual(isInlineCodeFileChip("web/src/services/sse.ts", undefined), true);
    assertEqual(isInlineCodeFileChip("web/src/services/sse.ts", false), false);
    assertEqual(isInlineCodeFileChip("requestAnimationFrame", true), false);
  });
});

describe("replay mount slice", () => {
  it("mounts with the first visible character so the view never starts empty", () => {
    assertEqual(initialRevealSlice("Hello there"), "H");
    assertEqual(initialRevealSlice("\n\n  Answer: 4"), "\n\n  A");
    assertEqual(initialRevealSlice("**bold** rest"), "**bold**");
    assertEqual(initialRevealSlice("   "), "   ");
    assertEqual(initialRevealSlice(""), "");
  });

  it("paces the rest of the content from the mount slice", () => {
    const content = "The answer is four.";
    const pacer = createStreamPacer(initialRevealSlice(content), 0);
    assertTrue(pacer.push(content, 0), "full content appends to the mount slice");
    let t = 0;
    while (pacer.pending && t < 5_000) pacer.step((t += FRAME));
    assertEqual(pacer.shown, content.length);
  });
});

describe("waiting row hand-off", () => {
  it("keeps the timeline height while the first reply line replaces the waiting row", () => {
    const hold = createTailHoldState();
    // Row shown: list is 1000px including the 61px row.
    hold.measure(1000, 61, true);
    // Row unmounts in the same update that mounts the reply.
    assertEqual(hold.handOff(), 61);
    // Layout: the row is gone, a 45px first line arrived, the spacer is 61px.
    assertEqual(hold.measure(939 + 45 + 61, 0, false), 16);
    // The reply grows past the row's height: the spacer is no longer needed.
    assertEqual(hold.measure(939 + 70 + 16, 0, false), 0);
    assertEqual(hold.measure(939 + 120, 0, false), 0);
  });

  it("never holds space it did not measure and clears on release", () => {
    const hold = createTailHoldState();
    assertEqual(hold.handOff(), 0);
    hold.measure(800, 40, true);
    assertEqual(hold.handOff(), 40);
    assertEqual(hold.release(), 0);
    assertEqual(hold.measure(780, 0, false), 0);
    assertEqual(hold.handOff(), 0);
  });
});

describe("streamed table rows", () => {
  it("holds a row until its line is complete", () => {
    const head = "Files:\n\n| File | Role |\n|---|---|\n| `a.ts` | first |\n";
    assertEqual(holdIncompleteTableRows(`${head}| \`ToolCard.tsx\` | ren`), head);
    assertEqual(holdIncompleteTableRows(`${head}| \`ToolCard.tsx\` | renders |\n`), `${head}| \`ToolCard.tsx\` | renders |\n`);
  });

  it("holds a header until its separator and first row arrive", () => {
    assertEqual(holdIncompleteTableRows("Files:\n\n| File | Role |\n"), "Files:\n\n");
    assertEqual(holdIncompleteTableRows("Files:\n\n| File | Role |\n|--"), "Files:\n\n");
    assertEqual(holdIncompleteTableRows("Files:\n\n| File | Role |\n|---|---|\n"), "Files:\n\n");
    assertEqual(holdIncompleteTableRows("Files:\n\n| File | Role |\n|---|---|\n| a.ts | x"), "Files:\n\n");
    assertEqual(holdIncompleteTableRows("| File |"), "");
  });

  it("leaves text without a pending table untouched", () => {
    for (const text of ["", "Plain answer", "Line one\nline tw", "Done.\n", "| a | b |\n|---|---|\n| 1 | 2 |\n\nAfter"]) {
      assertEqual(holdIncompleteTableRows(text), text);
    }
  });

  it("never regresses held table slice when an indented separator or body row streams in", () => {
    const doc = "Files:\n\n| File | Role |\n  |---|---|\n  | a.ts | main |\n";
    let prev = "";
    for (let i = 1; i <= doc.length; i++) {
      const next = holdIncompleteTableRows(doc.slice(0, i));
      assertTrue(next.startsWith(prev), `held table slice regressed at index ${i}: ${JSON.stringify(prev)} -> ${JSON.stringify(next)}`);
      prev = next;
    }
    assertEqual(prev, doc);
  });

  it("suppresses file chip state while an inline code span is still unclosed", () => {
    assertEqual(isInlineCodeFileChip("web/src/services/sse.ts", undefined, true), false);
    assertEqual(isInlineCodeFileChip("web/src/services/sse.ts", true, true), false);
    assertEqual(isInlineCodeFileChip("web/src/services/sse.ts", undefined, false), true);
  });

  it("holds trailing markdown delimiters after CJK text and punctuation and parses unclosed strikethrough", () => {
    assertDeepEqual(parseInline("\u4e2d\u6587*"), [{ type: "text", text: "\u4e2d\u6587" }]);
    assertDeepEqual(parseInline("\u5bf9\u6bd4\uff1a*"), [{ type: "text", text: "\u5bf9\u6bd4\uff1a" }]);
    assertDeepEqual(parseInline("hello ~"), [{ type: "text", text: "hello " }]);
    assertDeepEqual(parseInline("hello ~~strike"), [
      { type: "text", text: "hello " },
      { type: "strike", text: "strike" },
    ]);
  });

  it("preserves the same reasoning wrapper object reference across pure thinking to inline answer transitions", () => {
    const part: { id: string; sessionID: string; messageID: string; type: "text"; text: string } = {
      id: "part_think_transition",
      sessionID: "ses_1",
      messageID: "msg_1",
      type: "text",
      text: "<think>planning",
    };
    const duringThinking = expandInlineThinkingParts([part]);
    assertEqual(duringThinking.length, 1);
    const reasoningWrapper = duringThinking[0];
    assertEqual(reasoningWrapper.type, "reasoning");

    part.text = "<think>planning</think>\nFinal answer";
    const afterTransition = expandInlineThinkingParts([part]);
    assertEqual(afterTransition.length, 2);
    assertEqual(afterTransition[0], reasoningWrapper);
    assertEqual(afterTransition[1].type, "text");
  });
});


