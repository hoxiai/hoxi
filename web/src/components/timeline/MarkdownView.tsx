import { createMemo } from "solid-js";
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
  const blocks = createMemo(() => parseIncremental(props.content || ""));

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
