import { createEffect, createMemo, createSignal, onCleanup } from "solid-js";
import { createIncrementalMarkdownParser } from "./markdown-parser";
import MarkdownBlocks from "./MarkdownBlocks";

export {
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
    if (remaining <= 10) {
      current = target;
      setDisplayed(current);
      return;
    }
    const step = Math.max(10, Math.ceil(remaining * 0.38));
    let nextLen = Math.min(target.length, current.length + step);
    const code = target.charCodeAt(nextLen - 1);
    if (code >= 0xd800 && code <= 0xdbff && nextLen < target.length) {
      nextLen++;
    }
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
      typeof requestAnimationFrame !== "function" ||
      next.length - current.length <= 12
    ) {
      cancelTick();
      current = next;
      setDisplayed(next);
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
