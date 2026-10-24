import {
  advanceStreamingSlice,
  parseInline,
  parseMarkdownBlocks,
} from "../components/timeline/markdown-parser";
import {
  createStreamPacer,
  locateInlineSpans,
  locateTailInlines,
  pruneRevealChunks,
  revealLevel,
  revealSegments,
} from "../components/timeline/markdown/reveal";
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
    const text = "\u8fd9\u662f\u4e00\u6bb5\u7528\u4e8e\u9a8c\u8bc1\u5300\u901f\u51fa\u5b57\u7684\u4e2d\u6587\u6587\u672c\uff0c\u8282\u594f\u5e94\u5f53\u8fde\u7eed\u800c\u4e0d\u662f\u4e00\u987f\u4e00\u987f\u5730\u8e66\u51fa\u6765\u3002".repeat(12);
    const { perFrame } = simulate(text, 25, 100);
    // Skip warm-up; the old pacer idled on ~50% of frames at this rate.
    const steady = perFrame.slice(30, perFrame.length - 10);
    const idle = steady.filter((n) => n === 0).length / steady.length;
    assertTrue(idle < 0.2, `idle frame ratio ${idle.toFixed(2)} should stay under 0.2`);
    assertTrue(Math.max(...steady) <= 8, `max chars per frame ${Math.max(...steady)} should stay small`);
  });

  it("finishes shortly after the last chunk arrives", () => {
    const text = "x".repeat(400);
    const { lastArrival, finishedAt } = simulate(text, 40, 80);
    assertTrue(finishedAt - lastArrival <= 160, `drained ${finishedAt - lastArrival}ms after last chunk`);
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

  it("never stops inside a markdown token that has fully arrived", () => {
    const text = "\u524d\u7f00 **\u91cd\u70b9\u5185\u5bb9** \u4e0e `src/app.ts` \u4ee5\u53ca [\u94fe\u63a5](https://hoxi.ai) \u7ed3\u5c3e\u3002".repeat(3);
    const pacer = createStreamPacer("", 0);
    pacer.push(text, 0);
    const slices: string[] = [];
    for (let t = FRAME; pacer.pending && t < 10_000; t += FRAME) {
      slices.push(text.slice(0, pacer.step(t)));
    }
    for (const slice of slices) {
      assertTrue(!/\*\*[^*]*\*$/.test(slice), `half-closed bold: ${slice.slice(-12)}`);
      assertTrue(!/\]\(https?:\/\/[^)]*$/.test(slice), `half-open link: ${slice.slice(-20)}`);
    }
  });

  it("rejects content that is not an append so the view can jump", () => {
    const pacer = createStreamPacer("hello world", 0);
    assertTrue(pacer.push("hello world!", 10));
    assertEqual(pacer.push("goodbye", 20), false);
    pacer.reset("goodbye", 20);
    assertEqual(pacer.shown, 7);
    assertEqual(pacer.pending, false);
  });
});

describe("stream reveal trail", () => {
  const chunks = [
    { start: 16, end: 19, at: 100 },
    { start: 19, end: 21, at: 116 },
  ];

  it("steps freshly revealed text through the trail levels until it settles", () => {
    assertEqual(revealLevel(0, 720, 9), 0);
    assertEqual(revealLevel(100, 720, 9), 1);
    assertEqual(revealLevel(719, 720, 9), 8);
    assertEqual(revealLevel(720, 720, 9), -1);
  });

  it("maps fresh chunks onto a text node and merges equal levels", () => {
    // "Hello world" starts at source offset 10; "wor" and "ld" are fresh.
    assertDeepEqual(revealSegments(chunks, 10, 11, 120, 720), [{ from: 6, to: 11, level: 0 }]);
    assertDeepEqual(revealSegments(chunks, 10, 11, 190, 720), [
      { from: 6, to: 9, level: 1 },
      { from: 9, to: 11, level: 0 },
    ]);
  });

  it("leaves settled or unrelated text unpainted", () => {
    assertDeepEqual(revealSegments(chunks, 0, 7, 120, 720), []);
    assertDeepEqual(revealSegments(chunks, -1, 11, 120, 720), []);
    assertDeepEqual(revealSegments(chunks, 10, 11, 900, 720), []);
  });

  it("prunes chunks whose trail has finished", () => {
    assertEqual(pruneRevealChunks(chunks, 200, 280), chunks);
    assertDeepEqual(pruneRevealChunks(chunks, 390, 280), [chunks[1]]);
  });

  it("reveals a table row as a whole line", () => {
    const doc = "intro\n| a | b |\n|---|---|\n| 1 | 2 |";
    const rowStart = doc.indexOf("| a");
    assertEqual(advanceStreamingSlice(doc, rowStart, rowStart + 2), doc.indexOf("|---"));
    const lastRow = doc.lastIndexOf("| 1");
    assertEqual(advanceStreamingSlice(doc, lastRow, lastRow + 1), doc.length);
  });
});

describe("stream reveal offsets", () => {
  it("locates trailing paragraphs and list items without matching later text", () => {
    const source = "foo\n\nfoo\n\n- alpha\n- beta";
    const offsets = locateTailInlines(source, parseMarkdownBlocks(source), 2, 2);
    assertEqual(offsets.get("1"), 5);
    assertEqual(offsets.get("2:0"), source.indexOf("alpha"));
    assertEqual(offsets.get("2:1"), source.indexOf("beta"));
    assertEqual(offsets.has("0"), false);
  });

  it("maps inline spans to their visible text offsets", () => {
    const source = "a `b` **c** [d](https://x.y) e";
    assertDeepEqual(locateInlineSpans(source, parseInline(source)), [0, 3, 5, 8, 11, 13, 28]);
  });
});
