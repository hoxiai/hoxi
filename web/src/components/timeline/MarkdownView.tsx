import { createEffect, createMemo, createSignal, onCleanup } from "solid-js";
import { advanceStreamingSlice, createIncrementalMarkdownParser } from "./markdown-parser";
import MarkdownBlocks from "./MarkdownBlocks";

export {
  advanceStreamingSlice,
  type AlertKind,
  type BlockToken,
  isLikelyFilePath,
  isSameBlockToken,
  type ListItemEntry,
  parseInline,
  parseMarkdownBlocks,
  splitTableRow,
  stabilizeBlockToken,
  type TableAlign,
} from "./markdown-parser";

export default function MarkdownView(props: { content: string; density?: "chat" | "compact" }) {
  const parseIncremental = createIncrementalMarkdownParser();
  const [displayed, setDisplayed] = createSignal(props.content || "");
  let target = props.content || "";
  let current = target;
  let rafId: number | undefined;

  const cancelTick = () => {
    if (rafId !== undefined && typeof cancelAnimationFrame === "function") {
      cancelAnimationFrame(rafId);
      rafId = undefined;
    }
  };

  const tick = () => {
    rafId = undefined;
    if (current === target || !target.startsWith(current)) {
      current = target;
      setDisplayed(current);
      return;
    }
    const remaining = target.length - current.length;
    const rawNext =
      remaining <= 10
        ? target.length
        : current.length + Math.max(10, Math.ceil(remaining * 0.38));
    const nextLen = advanceStreamingSlice(target, current.length, rawNext);
    current = target.slice(0, nextLen);
    setDisplayed(current);
    if (current.length < target.length && typeof requestAnimationFrame === "function") {
      rafId = requestAnimationFrame(tick);
    }
  };

  createEffect(() => {
    const next = props.content || "";
    if (next === target) return;
    const prevTarget = target;
    target = next;
    if (
      !current ||
      !next.startsWith(current) ||
      !next.startsWith(prevTarget) ||
      typeof requestAnimationFrame !== "function"
    ) {
      cancelTick();
      current = next;
      setDisplayed(next);
      return;
    }
    if (next.length - current.length <= 12) {
      cancelTick();
      const nextLen = advanceStreamingSlice(next, current.length, next.length);
      current = next.slice(0, nextLen);
      setDisplayed(current);
      if (current.length < next.length) {
        rafId = requestAnimationFrame(tick);
      }
      return;
    }
    if (rafId === undefined) {
      tick();
    }
  });

  onCleanup(cancelTick);

  const blocks = createMemo(() => parseIncremental(displayed()));

  return (
    <div
      class={`flex flex-col text-[var(--text-primary)] selectable-text ${
        props.density === "compact" ? "gap-y-2 leading-[1.62]" : "gap-y-4 leading-[1.7]"
      }`}
      style={{ "font-size": "var(--app-chat-text-size)" }}
    >
      <MarkdownBlocks blocks={blocks()} density={props.density ?? "chat"} />
    </div>
  );
}
