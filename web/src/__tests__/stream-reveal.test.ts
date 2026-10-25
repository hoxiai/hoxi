import {
  advanceStreamingSlice,
  createIncrementalMarkdownParser,
  parseMarkdownBlocks,
} from "../components/timeline/markdown-parser";
import { createStreamPacer } from "../components/timeline/markdown/reveal";
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

  it("reveals a table row as a whole line", () => {
    const doc = "intro\n| a | b |\n|---|---|\n| 1 | 2 |";
    const rowStart = doc.indexOf("| a");
    assertEqual(advanceStreamingSlice(doc, rowStart, rowStart + 2), doc.indexOf("|---"));
    const lastRow = doc.lastIndexOf("| 1");
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
