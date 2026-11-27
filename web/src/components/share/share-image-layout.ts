// web/src/components/share/share-image-layout.ts
// Block-level Markdown/quote/tool/image layout assembly and GPU-safe pagination
// for local session share PNG cards.
import type { ThemedToken } from "shiki";
import { parseMarkdownBlocks, parseNestedBlockquote } from "../timeline/markdown/blocks";
import { parseInline } from "../timeline/markdown/inline";
import type { BlockToken, InlineSpan } from "../timeline/markdown/types";
import { buildContextCardLayout, buildUserCollabHeader } from "./share-image-context-layout";
import type { ShareDocument, ShareExchange, ShareImageAsset, ShareToolPill } from "./share-image-model";
import {
  inlineSpansToSegments,
  makeFont,
  wrapCodeTokens,
  wrapMarkdownInline,
  wrapStyledSegments,
  type LayoutBlock,
  type LayoutLabels,
  type LoadedImageInfo,
  type PaginatedShareLayout,
  type PositionedPill,
  type ShareContextCardLayout,
  type ShareThemePalette,
  type TextMeasureFn,
} from "./share-image-text";

export const MAX_SINGLE_CANVAS_LOGICAL_HEIGHT = 7000;
export const PAGED_TARGET_LOGICAL_HEIGHT = 2200;
const MAX_VISIBLE_TOOL_PILLS = 12;
const MAX_THINKING_CHARS = 1600;
const MAX_EMBEDDED_IMAGE_HEIGHT = 400;
const MAX_NESTED_BLOCK_DEPTH = 2;

function buildImageBlock(
  caption: string,
  maxWidth: number,
  loaded: LoadedImageInfo | undefined,
  labels: LayoutLabels
): LayoutBlock {
  if (!loaded || loaded.naturalWidth <= 0 || loaded.naturalHeight <= 0) {
    return {
      kind: "image",
      height: 36,
      drawWidth: Math.min(maxWidth, 320),
      drawHeight: 32,
      caption,
      missingLabel: labels.imageUnavailable(caption || "image"),
    };
  }
  const scale = Math.min(
    1,
    maxWidth / loaded.naturalWidth,
    MAX_EMBEDDED_IMAGE_HEIGHT / loaded.naturalHeight
  );
  const drawWidth = Math.max(48, Math.round(loaded.naturalWidth * scale));
  const drawHeight = Math.max(36, Math.round(loaded.naturalHeight * scale));
  const captionH = caption ? 22 : 0;
  return {
    kind: "image",
    height: drawHeight + captionH + 8,
    drawWidth,
    drawHeight,
    caption,
    loaded,
  };
}

function buildToolPillsBlock(
  tools: ShareToolPill[],
  maxWidth: number,
  labels: LayoutLabels,
  measure: TextMeasureFn
): LayoutBlock {
  const pillH = 24;
  const gapX = 6;
  const gapY = 6;
  const labelFont = makeFont(11.5, 600);
  const detailFont = makeFont(11.5, 400, true);

  const visible = tools.slice(0, MAX_VISIBLE_TOOL_PILLS);
  const extraCount = tools.length - visible.length;
  const pills: PositionedPill[] = [];

  let cursorX = 0;
  let cursorY = 0;

  const placePill = (label: string, detail: string, status: ShareToolPill["status"]) => {
    const labelW = measure(label, labelFont);
    const maxDetailW = Math.max(60, maxWidth - labelW - 44);
    let trimmedDetail = detail;
    let detailW = detail ? measure(detail, detailFont) : 0;
    if (detailW > maxDetailW && trimmedDetail) {
      while (trimmedDetail.length > 4 && measure(`${trimmedDetail}…`, detailFont) > maxDetailW) {
        trimmedDetail = trimmedDetail.slice(0, -1);
      }
      trimmedDetail = `${trimmedDetail}…`;
      detailW = measure(trimmedDetail, detailFont);
    }
    const width = Math.min(maxWidth, Math.ceil(22 + labelW + (detailW > 0 ? 6 + detailW : 0) + 10));
    if (cursorX > 0 && cursorX + width > maxWidth) {
      cursorX = 0;
      cursorY += pillH + gapY;
    }
    pills.push({ x: cursorX, y: cursorY, width, height: pillH, label, detail: trimmedDetail, status });
    cursorX += width + gapX;
  };

  for (const tool of visible) {
    placePill(tool.label, tool.detail, tool.status);
  }
  if (extraCount > 0) {
    placePill(labels.moreTools(extraCount), "", "completed");
  }

  return { kind: "tool-pills", height: cursorY + pillH + 6, pills };
}

function computeTableColWidths(
  headers: string[],
  rows: string[][],
  maxWidth: number,
  measure: TextMeasureFn
): number[] {
  const colCount = Math.max(1, headers.length);
  const minCol = Math.min(56, Math.max(24, Math.floor(maxWidth / colCount)));
  const font = makeFont(12.5, 500);
  const ideals = headers.map((h, ci) => {
    let best = measure(h, font) + 20;
    for (const row of rows.slice(0, 12)) {
      const sample = (row[ci] || "").slice(0, 48);
      best = Math.max(best, measure(sample, font) + 20);
    }
    return Math.max(minCol, Math.min(maxWidth * 0.55, best));
  });
  const total = ideals.reduce((a, b) => a + b, 0);
  if (total <= 0) return Array(colCount).fill(Math.floor(maxWidth / colCount));
  const scaled = ideals.map((w) => Math.max(minCol, Math.floor((w / total) * maxWidth)));
  const scaledSum = scaled.reduce((a, b) => a + b, 0);
  if (scaledSum <= maxWidth) return scaled;
  const ratio = maxWidth / scaledSum;
  return scaled.map((w) => Math.max(16, Math.floor(w * ratio)));
}

interface MarkdownBlockStyle {
  bodySize: number;
  bodyLineH: number;
  textColor: string;
  compact: boolean;
  depth: number;
}

function trimTrailingSpacers(blocks: LayoutBlock[]): number {
  while (blocks.length > 0 && blocks[blocks.length - 1].kind === "spacer") {
    blocks.pop();
  }
  return blocks.reduce((sum, b) => sum + b.height, 0);
}

function appendMarkdownBlocks(
  rawMarkdown: string,
  maxWidth: number,
  palette: ShareThemePalette,
  measure: TextMeasureFn,
  highlights: ReadonlyMap<string, ThemedToken[][]>,
  loadedImages: ReadonlyMap<string, LoadedImageInfo>,
  enabledImages: ReadonlyArray<ShareImageAsset>,
  renderedImageIDs: Set<string>,
  svgImages: ReadonlyMap<string, LoadedImageInfo>,
  labels: LayoutLabels,
  out: LayoutBlock[],
  style?: Partial<MarkdownBlockStyle>
): void {
  const blocks: BlockToken[] = parseMarkdownBlocks(rawMarkdown);
  const bodySize = style?.bodySize ?? 14.5;
  const bodyLineH = style?.bodyLineH ?? 23.5;
  const textColor = style?.textColor ?? palette.textSecondary;
  const compact = Boolean(style?.compact);
  const depth = style?.depth ?? 0;
  const paraGap = compact ? 5 : 10;
  const listGap = compact ? 3 : 6;

  const findEnabledMarkdownImage = (src: string): ShareImageAsset | undefined =>
    enabledImages.find((img) => img.src === src || img.displaySrc === src);

  for (const block of blocks) {
    switch (block.type) {
      case "frontmatter":
        break;
      case "hr":
        out.push({ kind: "divider", height: compact ? 13 : 17, marginTop: compact ? 6 : 8 });
        break;
      case "heading": {
        const sizes: Record<1 | 2 | 3 | 4, number> = compact
          ? { 1: 15, 2: 14, 3: 13.5, 4: 13 }
          : { 1: 19, 2: 17, 3: 15.5, 4: 14.5 };
        const lineHs: Record<1 | 2 | 3 | 4, number> = compact
          ? { 1: 21, 2: 20, 3: 19.5, 4: 19 }
          : { 1: 27, 2: 25, 3: 23, 4: 22 };
        const sz = sizes[block.level];
        const lh = lineHs[block.level];
        const lines = wrapMarkdownInline(block.text, maxWidth, sz, lh, 700, palette.textPrimary, palette, measure);
        out.push({
          kind: "heading",
          level: block.level,
          lines,
          height: lines.length * lh + (compact ? 6 : 10),
          keepWithNext: true,
        });
        break;
      }
      case "paragraph": {
        const spans = parseInline(block.text);
        const imageSpans = spans.filter((s): s is Extract<InlineSpan, { type: "image" }> => s.type === "image");
        const nonImageText = spans
          .filter((s) => s.type !== "image")
          .map((s) => ("text" in s ? s.text : ""))
          .join("")
          .trim();

        if (nonImageText) {
          const lines = wrapStyledSegments(
            inlineSpansToSegments(
              spans.filter((s) => s.type !== "image"),
              bodySize,
              400,
              textColor,
              palette
            ),
            maxWidth,
            bodyLineH,
            measure
          );
          out.push({ kind: "paragraph", lines, height: lines.length * bodyLineH + paraGap });
        }
        for (const imgSpan of imageSpans) {
          const asset = findEnabledMarkdownImage(imgSpan.src);
          if (!asset) continue;
          renderedImageIDs.add(asset.id);
          out.push(buildImageBlock(imgSpan.alt || asset.label, maxWidth, loadedImages.get(asset.id), labels));
        }
        break;
      }
      case "list": {
        for (const item of block.items) {
          const indentPx = Math.min(3, Math.floor(item.indent / 2)) * 16;
          const marker = item.kind === "ordered" ? `${item.orderNumber ?? 1}.` : "•";
          const markerX = indentPx + 4;
          const textX = indentPx + (item.kind === "ordered" ? 24 : 18);
          const avail = Math.max(100, maxWidth - textX);
          const lines = wrapMarkdownInline(item.text, avail, bodySize, bodyLineH, 400, textColor, palette, measure);
          out.push({
            kind: "list-item",
            marker,
            markerX,
            textX,
            lines,
            height: lines.length * bodyLineH + listGap,
          });
        }
        out.push({ kind: "spacer", height: compact ? 3 : 4 });
        break;
      }
      case "blockquote": {
        const innerW = Math.max(100, maxWidth - 28);
        const alertExtra = block.alert ? 25 : 0;
        const nestedTokens = depth < MAX_NESTED_BLOCK_DEPTH ? parseNestedBlockquote(block.text) : null;
        if (nestedTokens && nestedTokens.length > 0) {
          const nestedBlocks: LayoutBlock[] = [];
          appendMarkdownBlocks(
            block.text,
            innerW,
            palette,
            measure,
            highlights,
            loadedImages,
            enabledImages,
            renderedImageIDs,
            svgImages,
            labels,
            nestedBlocks,
            {
              bodySize: Math.min(bodySize, 13.5),
              bodyLineH: Math.min(bodyLineH, 21),
              textColor: palette.textSecondary,
              compact: true,
              depth: depth + 1,
            }
          );
          const nestedH = trimTrailingSpacers(nestedBlocks);
          out.push({
            kind: "blockquote",
            alert: block.alert,
            lines: [],
            blocks: nestedBlocks,
            height: Math.max(20, nestedH) + alertExtra + 30,
          });
        } else {
          const quoteSize = Math.min(bodySize, 13.5);
          const quoteLH = Math.min(bodyLineH, 21.5);
          const lines = wrapMarkdownInline(block.text, innerW, quoteSize, quoteLH, 400, palette.textSecondary, palette, measure);
          out.push({
            kind: "blockquote",
            alert: block.alert,
            lines,
            height: lines.length * quoteLH + alertExtra + 30,
          });
        }
        break;
      }
      case "details": {
        const summaryW = Math.max(100, maxWidth - 38);
        const summaryText = block.summary || labels.referencedSingle;
        const summaryLines = wrapMarkdownInline(
          summaryText,
          summaryW,
          13,
          19,
          600,
          palette.textPrimary,
          palette,
          measure
        );
        const headerH = Math.max(30, summaryLines.length * 19 + 14);
        const innerBlocks: LayoutBlock[] = [];
        if (block.body && depth < MAX_NESTED_BLOCK_DEPTH) {
          appendMarkdownBlocks(
            block.body,
            Math.max(100, maxWidth - 24),
            palette,
            measure,
            highlights,
            loadedImages,
            enabledImages,
            renderedImageIDs,
            svgImages,
            labels,
            innerBlocks,
            {
              bodySize: Math.min(bodySize, 13),
              bodyLineH: Math.min(bodyLineH, 20.5),
              textColor: palette.textSecondary,
              compact: true,
              depth: depth + 1,
            }
          );
        } else if (block.body) {
          const lines = wrapMarkdownInline(
            block.body,
            Math.max(100, maxWidth - 24),
            13,
            20.5,
            400,
            palette.textSecondary,
            palette,
            measure
          );
          innerBlocks.push({ kind: "paragraph", lines, height: lines.length * 20.5 + 4 });
        }
        const innerH = trimTrailingSpacers(innerBlocks);
        out.push({
          kind: "details",
          summaryLines,
          blocks: innerBlocks,
          height: headerH + (innerH > 0 ? innerH + 18 : 0) + 10,
        });
        break;
      }
      case "code": {
        if (block.lang.toLowerCase() === "svg") {
          const svgLoaded = svgImages.get(block.code);
          if (svgLoaded) {
            out.push(buildImageBlock("SVG", maxWidth, svgLoaded, labels));
            break;
          }
        }
        const key = `${block.lang.toLowerCase()}\0${block.code}`;
        const wrapped = wrapCodeTokens(block.code, highlights.get(key), Math.max(80, maxWidth - 28), palette, measure);
        const headerH = 28;
        const footerH = wrapped.truncatedLines > 0 ? 22 : 0;
        const codeH = Math.max(1, wrapped.lines.length) * 19.5;
        out.push({
          kind: "code",
          lang: block.lang || "text",
          lines: wrapped.lines,
          truncatedLines: wrapped.truncatedLines,
          height: headerH + codeH + footerH + 30,
        });
        break;
      }
      case "table": {
        const colCount = Math.max(1, block.headers.length);
        const colWidths = computeTableColWidths(block.headers, block.rows, maxWidth, measure);
        const headerCells = block.headers.map((cell, ci) =>
          wrapMarkdownInline(cell, Math.max(40, colWidths[ci] - 16), 12.5, 19, 600, palette.textPrimary, palette, measure)
        );
        const headerH = Math.max(30, ...headerCells.map((c) => c.length * 19 + 14));
        const bodyRows = block.rows.map((row) => {
          const cells = Array.from({ length: colCount }, (_, ci) =>
            wrapMarkdownInline(row[ci] || "", Math.max(40, colWidths[ci] - 16), 12.5, 19, 400, palette.textSecondary, palette, measure)
          );
          const rowH = Math.max(28, ...cells.map((c) => c.length * 19 + 14));
          return { height: rowH, cells };
        });
        const totalTableH = headerH + bodyRows.reduce((sum, r) => sum + r.height, 0) + 12;
        out.push({
          kind: "table",
          colWidths,
          aligns: block.aligns,
          headerRow: { height: headerH, cells: headerCells },
          bodyRows,
          height: totalTableH,
        });
        break;
      }
    }
  }
}

export function paginateLayoutBlocks(
  blocks: LayoutBlock[],
  maxBodyHeight: number,
  firstPageMaxBodyHeight: number
): Array<{ blocks: LayoutBlock[]; bodyHeight: number }> {
  const pages: Array<{ blocks: LayoutBlock[]; bodyHeight: number }> = [];
  let current: LayoutBlock[] = [];
  let currentH = 0;

  const pageLimit = () => (pages.length === 0 ? firstPageMaxBodyHeight : maxBodyHeight);

  const flushPage = () => {
    while (current.length > 0 && (current[current.length - 1].kind === "spacer" || current[current.length - 1].kind === "divider")) {
      currentH -= current.pop()!.height;
    }
    if (current.length > 0) {
      pages.push({ blocks: current, bodyHeight: Math.max(0, currentH) });
    }
    current = [];
    currentH = 0;
  };

  for (let i = 0; i < blocks.length; i++) {
    const block = blocks[i];
    if (current.length === 0 && (block.kind === "spacer" || block.kind === "divider")) {
      continue;
    }
    const nextBlock = blocks[i + 1];
    const reservedH =
      "keepWithNext" in block && block.keepWithNext && nextBlock
        ? block.height + Math.min(nextBlock.height, 140)
        : block.height;

    if (current.length > 0 && currentH + reservedH > pageLimit()) {
      flushPage();
      if (block.kind === "spacer" || block.kind === "divider") continue;
    }
    current.push(block);
    currentH += block.height;
  }

  flushPage();
  if (pages.length === 0) {
    pages.push({ blocks: [], bodyHeight: 0 });
  }
  return pages;
}

export function buildPaginatedShareLayout(params: {
  doc: ShareDocument;
  palette: ShareThemePalette;
  cardWidth: number;
  layoutMode: "single" | "paged";
  showBrandFooter: boolean;
  measure: TextMeasureFn;
  highlights: ReadonlyMap<string, ThemedToken[][]>;
  loadedImages: ReadonlyMap<string, LoadedImageInfo>;
  svgImages: ReadonlyMap<string, LoadedImageInfo>;
  labels: LayoutLabels;
  formattedDate: string;
}): PaginatedShareLayout {
  const { doc, palette, cardWidth, layoutMode, showBrandFooter, measure, highlights, loadedImages, svgImages, labels, formattedDate } = params;
  const outerPadding = cardWidth >= 840 ? 32 : 24;
  const cardPaddingX = cardWidth >= 840 ? 36 : 28;
  const cardPaddingY = 28;
  const contentWidth = cardWidth - outerPadding * 2 - cardPaddingX * 2;

  const titleLines = wrapStyledSegments(
    [{ text: doc.title, font: makeFont(20, 700), color: palette.textPrimary }],
    contentWidth,
    28,
    measure
  ).slice(0, 3);
  const metaChips: string[] = [];
  if (doc.modelID) metaChips.push(doc.modelID);
  if (doc.projectLabel) metaChips.push(doc.projectLabel);
  if (formattedDate) metaChips.push(formattedDate);
  if (doc.exchanges.length > 0) metaChips.push(labels.turnsCount(doc.exchanges.length));
  const headerHeight = titleLines.length * 28 + (metaChips.length > 0 ? 28 : 0) + 26;

  const blocks: LayoutBlock[] = [];
  for (let ei = 0; ei < doc.exchanges.length; ei++) {
    const ex: ShareExchange = doc.exchanges[ei];
    const renderedImageIDs = new Set<string>();

    if (ei > 0) {
      blocks.push({ kind: "divider", height: 28, marginTop: 14 });
    }

    const hasUserContent = Boolean(ex.userText || ex.userContexts.length > 0 || ex.userCollab);
    if (hasUserContent) {
      const userInnerW = Math.max(120, contentWidth - 32);
      const buildNested = (
        md: string,
        width: number,
        st: { bodySize: number; bodyLineH: number; textColor: string; compact: boolean }
      ): LayoutBlock[] => {
        const nested: LayoutBlock[] = [];
        appendMarkdownBlocks(
          md,
          width,
          palette,
          measure,
          highlights,
          loadedImages,
          [],
          renderedImageIDs,
          svgImages,
          labels,
          nested,
          { ...st, depth: 1 }
        );
        return nested;
      };

      const collabHeader = ex.userCollab
        ? buildUserCollabHeader(ex.userCollab, userInnerW, palette, measure, labels)
        : undefined;

      const contextCards: ShareContextCardLayout[] = ex.userContexts.map((ctxBlock) =>
        buildContextCardLayout(ctxBlock, userInnerW, palette, measure, labels, buildNested)
      );

      const userBodyBlocks: LayoutBlock[] = [];
      if (ex.userCollab && ex.userCollab.sections.length > 0) {
        for (const sec of ex.userCollab.sections) {
          const headingLines = wrapStyledSegments(
            [{ text: sec.heading, font: makeFont(12, 600), color: palette.textMuted }],
            userInnerW,
            18,
            measure
          );
          userBodyBlocks.push({
            kind: "heading",
            level: 4,
            lines: headingLines,
            height: headingLines.length * 18 + 3,
            keepWithNext: true,
          });
          appendMarkdownBlocks(
            sec.content,
            userInnerW,
            palette,
            measure,
            highlights,
            loadedImages,
            ex.images,
            renderedImageIDs,
            svgImages,
            labels,
            userBodyBlocks,
            { bodySize: 14.5, bodyLineH: 23, textColor: palette.textPrimary, compact: true, depth: 0 }
          );
        }
      } else if (ex.userText) {
        appendMarkdownBlocks(
          ex.userText,
          userInnerW,
          palette,
          measure,
          highlights,
          loadedImages,
          ex.images,
          renderedImageIDs,
          svgImages,
          labels,
          userBodyBlocks,
          { bodySize: 14.5, bodyLineH: 23, textColor: palette.textPrimary, compact: true, depth: 0 }
        );
      }
      const userBodyH = trimTrailingSpacers(userBodyBlocks);

      let userBoxH = 12; // top pad
      if (collabHeader) {
        userBoxH += 22;
        if (collabHeader.titleLines.length > 0) {
          userBoxH += 4 + collabHeader.titleLines.length * 20.5;
        }
      } else {
        userBoxH += 18;
      }
      if (contextCards.length > 0) {
        userBoxH += 6 + contextCards.reduce((sum, c) => sum + c.height, 0) + (contextCards.length - 1) * 8;
      }
      if (userBodyH > 0) {
        userBoxH += (contextCards.length > 0 ? 10 : 4) + userBodyH;
      }
      userBoxH += 12 + 4; // bottom pad + outer margin

      blocks.push({
        kind: "user-box",
        roleLabel: labels.you,
        collabHeader,
        contextCards,
        bodyBlocks: userBodyBlocks,
        lines: [],
        height: userBoxH,
      });
      blocks.push({ kind: "spacer", height: 10 });
    }

    for (const img of ex.images.filter((item) => item.role === "user" && !renderedImageIDs.has(item.id))) {
      renderedImageIDs.add(img.id);
      blocks.push(buildImageBlock(img.label, contentWidth, loadedImages.get(img.id), labels));
    }

    const hasAssistant = Boolean(
      ex.assistantText ||
      ex.thinkingText ||
      ex.tools.length > 0 ||
      ex.images.some((item) => item.role === "assistant")
    );
    if (hasAssistant) {
      blocks.push({
        kind: "role-header",
        role: "assistant",
        label: labels.assistant,
        modelID: ex.modelID,
        height: 28,
        keepWithNext: true,
      });

      if (ex.thinkingText) {
        const trimmed =
          ex.thinkingText.length > MAX_THINKING_CHARS
            ? `${ex.thinkingText.slice(0, MAX_THINKING_CHARS)}…`
            : ex.thinkingText;
        const lines = wrapStyledSegments(
          [{ text: trimmed, font: makeFont(12.5, 400, false, true), color: palette.textMuted }],
          contentWidth - 28,
          19.5,
          measure
        );
        blocks.push({
          kind: "thinking",
          label: labels.thinking,
          lines,
          height: 28 + lines.length * 19.5 + 20,
        });
        blocks.push({ kind: "spacer", height: 6 });
      }

      if (ex.tools.length > 0) {
        blocks.push(buildToolPillsBlock(ex.tools, contentWidth, labels, measure));
        blocks.push({ kind: "spacer", height: 6 });
      }

      if (ex.assistantText) {
        appendMarkdownBlocks(
          ex.assistantText,
          contentWidth,
          palette,
          measure,
          highlights,
          loadedImages,
          ex.images,
          renderedImageIDs,
          svgImages,
          labels,
          blocks
        );
      }

      for (const img of ex.images.filter((item) => item.role === "assistant" && !renderedImageIDs.has(item.id))) {
        renderedImageIDs.add(img.id);
        blocks.push(buildImageBlock(img.label, contentWidth, loadedImages.get(img.id), labels));
      }
    }
  }

  const totalBodyHeight = blocks.reduce((sum, b) => sum + b.height, 0);
  const footerHeight = showBrandFooter ? 48 : 16;
  const chromeFirstPage = outerPadding * 2 + cardPaddingY * 2 + headerHeight + footerHeight;
  const chromeNextPage = outerPadding * 2 + cardPaddingY * 2 + footerHeight;
  const singleTotalHeight = chromeFirstPage + totalBodyHeight;

  const mustAutoPaginate = layoutMode === "single" && singleTotalHeight > MAX_SINGLE_CANVAS_LOGICAL_HEIGHT;
  const shouldPaginate = layoutMode === "paged" || mustAutoPaginate;

  if (!shouldPaginate) {
    return {
      cardWidth,
      outerPadding,
      cardPaddingX,
      cardPaddingY,
      contentWidth,
      header: { height: headerHeight, titleLines, metaChips },
      footerHeight,
      pages: [{ blocks, bodyHeight: totalBodyHeight, totalHeight: Math.max(240, singleTotalHeight) }],
      autoPaginated: false,
    };
  }

  const targetPageH = mustAutoPaginate ? MAX_SINGLE_CANVAS_LOGICAL_HEIGHT - 400 : PAGED_TARGET_LOGICAL_HEIGHT;
  const firstLimit = Math.max(400, targetPageH - chromeFirstPage);
  const nextLimit = Math.max(400, targetPageH - chromeNextPage);
  const sliced = paginateLayoutBlocks(blocks, nextLimit, firstLimit);
  const finalFooterH = showBrandFooter || sliced.length > 1 ? 48 : 16;

  return {
    cardWidth,
    outerPadding,
    cardPaddingX,
    cardPaddingY,
    contentWidth,
    header: { height: headerHeight, titleLines, metaChips },
    footerHeight: finalFooterH,
    pages: sliced.map((page, idx) => ({
      blocks: page.blocks,
      bodyHeight: page.bodyHeight,
      totalHeight: Math.max(
        240,
        (idx === 0 ? outerPadding * 2 + cardPaddingY * 2 + headerHeight : outerPadding * 2 + cardPaddingY * 2) +
          page.bodyHeight +
          finalFooterH
      ),
    })),
    autoPaginated: mustAutoPaginate,
  };
}
