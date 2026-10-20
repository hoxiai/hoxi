// web/src/components/timeline/markdown-parser.ts
//
// Stable public entrypoint for markdown parsing. The implementation lives in
// `./markdown/` split by domain (block, inline, streaming) so each file stays
// well under the 800-line limit.
export type {
  AlertKind,
  BlockToken,
  InlineSpan,
  ListItemEntry,
  TableAlign,
} from "./markdown/types";

export {
  isLikelyFilePath,
  parseMarkdownBlocks,
  parseNestedBlockquote,
  splitTableRow,
  stripFileLineSuffix,
} from "./markdown/blocks";

export { isSameInlineSpan, parseInline } from "./markdown/inline";

export {
  createIncrementalMarkdownParser,
  isSameBlockToken,
  stabilizeBlockToken,
} from "./markdown/stream";
