import { createTranscriptSearchClient, type SearchPattern } from "../../store/workspace";

export function clearTranscriptSearchHighlights() {
  const highlights = (globalThis.CSS as typeof CSS & { highlights?: Map<string, unknown> })?.highlights;
  highlights?.delete("transcript-search-match");
  highlights?.delete("transcript-search-active");
}

/** DOM work is bounded; even highlight matching runs in a terminable worker. */
export function highlightTranscriptSearch(pattern: SearchPattern, turnID: string | null, partID?: string): () => void {
  clearTranscriptSearchHighlights();
  const highlights = (globalThis.CSS as typeof CSS & { highlights?: Map<string, unknown> })?.highlights;
  const HighlightCtor = (globalThis as { Highlight?: new (...ranges: AbstractRange[]) => unknown }).Highlight;
  const root = document.querySelector(".timeline-result-scroll");
  if (!highlights || !HighlightCtor || !root) return () => {};
  const client = createTranscriptSearchClient();
  let cancelled = false;
  const frame = requestAnimationFrame(async () => {
    const nodes: Text[] = [];
    const texts: string[] = [];
    const active = partID ? root.querySelector(`[data-part-id="${CSS.escape(partID)}"]`)
      : turnID ? root.querySelector(`[data-turn-id="${CSS.escape(turnID)}"]`) : null;
    const started = performance.now();
    let chars = 0;
    let visited = 0;
    const seen = new WeakSet<Node>();
    for (const subtree of active ? [active, root] : [root]) {
      const walker = document.createTreeWalker(subtree, NodeFilter.SHOW_TEXT);
      let node: Node | null;
      while ((node = walker.nextNode()) && visited++ < 1000 && chars < 128_000 && performance.now()-started < 4) {
        if (!node.nodeValue || seen.has(node)) continue;
        seen.add(node); nodes.push(node as Text);
        const text = node.nodeValue.slice(0, 128_000-chars);
        texts.push(text); chars += text.length;
      }
    }
    try {
      const matches = await client.ranges(texts, pattern);
      if (cancelled) return;
      const ranges: Range[] = [];
      const activeRanges: Range[] = [];
      for (const [index, start, end] of matches) {
        const text = nodes[index];
        if (!text?.isConnected || !text.nodeValue?.startsWith(texts[index])) continue;
        const range = document.createRange();
        range.setStart(text, start); range.setEnd(text, end);
        (active?.contains(text) ? activeRanges : ranges).push(range);
      }
      highlights.set("transcript-search-match", new HighlightCtor(...ranges));
      highlights.set("transcript-search-active", new HighlightCtor(...activeRanges));
    } catch { /* Search errors are reported by the search controller; highlights are optional. */ }
    finally { client.dispose(); }
  });
  return () => { cancelled = true; cancelAnimationFrame(frame); client.dispose(); clearTranscriptSearchHighlights(); };
}
