// web/src/components/timeline/markdown-parser.ts
//
// Stable public entrypoint for markdown parsing. The implementation lives in
// `./markdown/` split by domain (block, inline, streaming) so each file stays
// well under the 800-line limit.
export type {
  AlertKind,
  BlockToken,
  FrontmatterEntry,
  InlineSpan,
  ListItemEntry,
  MarkdownHeadingItem,
  TableAlign,
} from "./markdown/types";

export {
  computeHeadingSlugList,
  extractMarkdownHeadings,
  isLikelyFilePath,
  parseMarkdownBlocks,
  parseNestedBlockquote,
  resolveMarkdownRelativePath,
  resolveMarkdownFileLink,
  slugifyHeading,
  splitTableRow,
  stripFileLineSuffix,
  stripInlineMarkdownFormatting,
} from "./markdown/blocks";

export { isSameInlineSpan, parseInline } from "./markdown/inline";

export {
  advanceStreamingSlice,
  createIncrementalMarkdownParser,
  isSameBlockToken,
  stabilizeBlockToken,
} from "./markdown/stream";
