// web/src/components/share/share-image-context-paint.ts
// Canvas 2D painters for structured quote/reference cards, nested Markdown
// blockquotes/alerts, details panels, and user prompt boxes.
import type { AlertKind } from "../timeline/markdown/types";
import {
  drawWrappedLines,
  fillRoundRect,
  FONT_MONO,
  FONT_SANS,
  makeFont,
  type LayoutBlock,
  type LayoutLabels,
  type ShareContextCardLayout,
  type SharePillLayout,
  type ShareThemePalette,
} from "./share-image-text";

export type NestedBlockPainter = (
  ctx: CanvasRenderingContext2D,
  block: LayoutBlock,
  x: number,
  y: number,
  width: number
) => void;

function drawPill(
  ctx: CanvasRenderingContext2D,
  pill: SharePillLayout,
  x: number,
  centerY: number,
  palette: ShareThemePalette
): void {
  const h = 17;
  const y = centerY - h / 2;
  let bg = palette.pillBg;
  let border = palette.pillBorder;
  let textColor = palette.textSecondary;

  if (pill.tone === "accent" || pill.tone === "op") {
    bg = palette.accentBg;
    border = palette.accentBorder;
    textColor = palette.accent;
  } else if (pill.tone === "pm") {
    bg = palette.alertImportantBg;
    border = palette.importantDot;
    textColor = palette.importantDot;
  } else if (pill.tone === "qa") {
    bg = palette.alertWarnBg;
    border = palette.warnDot;
    textColor = palette.warnDot;
  }

  ctx.fillStyle = bg;
  fillRoundRect(ctx, x, y, pill.width, h, 5);
  ctx.fill();
  ctx.strokeStyle = border;
  ctx.lineWidth = 0.85;
  ctx.stroke();

  ctx.font = makeFont(10.5, pill.mono ? 500 : 600, Boolean(pill.mono));
  ctx.fillStyle = textColor;
  ctx.textBaseline = "middle";
  ctx.fillText(pill.text, x + 6, centerY + 0.5);
}

function drawQuoteMarkIcon(
  ctx: CanvasRenderingContext2D,
  x: number,
  centerY: number,
  color: string
): void {
  ctx.save();
  ctx.fillStyle = color;
  ctx.font = `700 15px Georgia, "Times New Roman", serif`;
  ctx.textBaseline = "middle";
  ctx.fillText("“", x, centerY + 2.5);
  ctx.restore();
}

function drawStructuredKindIcon(
  ctx: CanvasRenderingContext2D,
  kind: "entity" | "snapshot" | "record" | "batch",
  x: number,
  centerY: number,
  color: string
): void {
  ctx.save();
  ctx.strokeStyle = color;
  ctx.lineWidth = 1.25;
  ctx.lineJoin = "round";
  ctx.lineCap = "round";
  if (kind === "entity") {
    ctx.beginPath();
    ctx.ellipse(x + 6, centerY - 3.2, 4.5, 1.8, 0, 0, Math.PI * 2);
    ctx.stroke();
    ctx.beginPath();
    ctx.moveTo(x + 1.5, centerY - 3.2);
    ctx.lineTo(x + 1.5, centerY + 3.2);
    ctx.ellipse(x + 6, centerY + 3.2, 4.5, 1.8, 0, Math.PI, 0, true);
    ctx.lineTo(x + 10.5, centerY - 3.2);
    ctx.stroke();
  } else if (kind === "snapshot") {
    fillRoundRect(ctx, x + 2, centerY - 5, 8, 10, 1.5);
    ctx.stroke();
    ctx.beginPath();
    ctx.moveTo(x + 4, centerY - 1.5);
    ctx.lineTo(x + 8, centerY - 1.5);
    ctx.moveTo(x + 4, centerY + 1.5);
    ctx.lineTo(x + 7.5, centerY + 1.5);
    ctx.stroke();
  } else {
    ctx.beginPath();
    ctx.moveTo(x + 6, centerY - 4.8);
    ctx.lineTo(x + 11, centerY - 2);
    ctx.lineTo(x + 6, centerY + 0.8);
    ctx.lineTo(x + 1, centerY - 2);
    ctx.closePath();
    ctx.stroke();
    ctx.beginPath();
    ctx.moveTo(x + 1.5, centerY + 1.6);
    ctx.lineTo(x + 6, centerY + 4.3);
    ctx.lineTo(x + 10.5, centerY + 1.6);
    ctx.stroke();
  }
  ctx.restore();
}

export function drawContextCard(
  ctx: CanvasRenderingContext2D,
  card: ShareContextCardLayout,
  x: number,
  y: number,
  width: number,
  palette: ShareThemePalette,
  labels: LayoutLabels,
  drawNested: NestedBlockPainter
): void {
  if (card.variant === "quote") {
    ctx.fillStyle = palette.quoteCardBg;
    fillRoundRect(ctx, x, y, width, card.height, 10);
    ctx.fill();

    ctx.save();
    fillRoundRect(ctx, x, y, width, card.height, 10);
    ctx.clip();
    ctx.fillStyle = palette.accent;
    ctx.fillRect(x, y, 3.5, card.height);
    ctx.restore();

    ctx.strokeStyle = palette.quoteCardBorder;
    ctx.lineWidth = 1;
    fillRoundRect(ctx, x, y, width, card.height, 10);
    ctx.stroke();

    const headerMidY = y + 9 + 10;
    let hx = x + 14;
    drawQuoteMarkIcon(ctx, hx, headerMidY, palette.accent);
    hx += 13;
    if (card.sourcePill) {
      drawPill(ctx, card.sourcePill, hx, headerMidY, palette);
      hx += card.sourcePill.width + 6;
    }
    if (card.titleText) {
      ctx.font = makeFont(11.5, 600);
      ctx.fillStyle = palette.textSecondary;
      ctx.textBaseline = "middle";
      ctx.fillText(card.titleText, hx, headerMidY + 0.5);
      hx += ctx.measureText(card.titleText).width + 6;
    }
    for (const pill of card.pills) {
      if (hx + pill.width <= x + width - 10) {
        drawPill(ctx, pill, hx, headerMidY, palette);
        hx += pill.width + 5;
      }
    }

    let cy = y + 9 + 20 + (card.bodyHeight > 0 ? 5 : 0);
    const bodyW = Math.max(120, width - 26);
    for (const b of card.bodyBlocks) {
      drawNested(ctx, b, x + 14, cy, bodyW);
      cy += b.height;
    }
    if (card.omittedLines > 0) {
      ctx.font = makeFont(11, 500);
      ctx.fillStyle = palette.textFaint;
      ctx.textBaseline = "middle";
      ctx.fillText(labels.omittedLines(card.omittedLines), x + 14, cy + 8);
    }
    return;
  }

  const headerH = 30;
  ctx.fillStyle = palette.quoteCardBg;
  fillRoundRect(ctx, x, y, width, card.height, 10);
  ctx.fill();

  ctx.save();
  fillRoundRect(ctx, x, y, width, card.height, 10);
  ctx.clip();
  ctx.fillStyle = palette.quoteHeaderBg;
  ctx.fillRect(x, y, width, headerH);
  if (card.rows.length > 0 || card.extraItemsCount > 0) {
    ctx.strokeStyle = palette.quoteCardBorder;
    ctx.lineWidth = 1;
    ctx.beginPath();
    ctx.moveTo(x, y + headerH);
    ctx.lineTo(x + width, y + headerH);
    ctx.stroke();
  }
  ctx.restore();

  ctx.strokeStyle = palette.quoteCardBorder;
  ctx.lineWidth = 1;
  fillRoundRect(ctx, x, y, width, card.height, 10);
  ctx.stroke();

  const hMidY = y + headerH / 2;
  let hx = x + 11;
  drawStructuredKindIcon(ctx, card.kindIcon, hx, hMidY, palette.accent);
  hx += 17;
  if (card.sourcePill) {
    drawPill(ctx, card.sourcePill, hx, hMidY, palette);
    hx += card.sourcePill.width + 6;
  }
  ctx.font = makeFont(11.5, 600);
  ctx.fillStyle = palette.textPrimary;
  ctx.textBaseline = "middle";
  ctx.fillText(card.headlineText, hx, hMidY + 0.5);
  if (card.headerBadge) {
    drawPill(ctx, card.headerBadge, x + width - 11 - card.headerBadge.width, hMidY, palette);
  }

  let ry = y + headerH;
  const innerW = Math.max(120, width - 22);
  for (let ri = 0; ri < card.rows.length; ri++) {
    const row = card.rows[ri];
    if (ri > 0) {
      ctx.strokeStyle = palette.divider;
      ctx.lineWidth = 1;
      ctx.beginPath();
      ctx.moveTo(x + 1, ry);
      ctx.lineTo(x + width - 1, ry);
      ctx.stroke();
    }
    let cy = ry + 8;
    const textX = x + 11 + (row.indexLabel ? 24 : 0);

    if (row.indexLabel) {
      ctx.fillStyle = palette.pillBg;
      fillRoundRect(ctx, x + 11, cy + 1, 18, 16, 4);
      ctx.fill();
      ctx.strokeStyle = palette.pillBorder;
      ctx.lineWidth = 0.85;
      ctx.stroke();
      ctx.font = `600 10px ${FONT_MONO}`;
      ctx.fillStyle = palette.textMuted;
      ctx.textBaseline = "middle";
      const iw = ctx.measureText(row.indexLabel).width;
      ctx.fillText(row.indexLabel, x + 11 + (18 - iw) / 2, cy + 9.5);
    }

    if (row.titleLines.length > 0) {
      const endY = drawWrappedLines(ctx, row.titleLines, textX, cy, palette);
      if (row.pills.length > 0) {
        if (row.titlePillsInline) {
          let px = textX + row.titleLineWidth + 6;
          for (const pill of row.pills) {
            drawPill(ctx, pill, px, cy + 9, palette);
            px += pill.width + 5;
          }
        } else {
          let px = textX;
          for (const pill of row.pills) {
            drawPill(ctx, pill, px, endY + 11, palette);
            px += pill.width + 5;
          }
          cy = endY + 22;
        }
      }
      if (cy < endY) cy = endY;
    } else if (row.pills.length > 0) {
      let px = textX;
      for (const pill of row.pills) {
        drawPill(ctx, pill, px, cy + 9.5, palette);
        px += pill.width + 5;
      }
      cy += 19;
    } else if (row.indexLabel) {
      cy += 18;
    }

    if (row.subtitleLines.length > 0) {
      if (cy > ry + 8) cy += 3;
      cy = drawWrappedLines(ctx, row.subtitleLines, textX, cy, palette);
    }

    if (row.kvRows.length > 0) {
      if (cy > ry + 8) cy += 6;
      const boxX = x + 11;
      ctx.fillStyle = palette.codeBg;
      fillRoundRect(ctx, boxX, cy, innerW, row.kvBoxHeight, 7);
      ctx.fill();
      ctx.strokeStyle = palette.codeBorder;
      ctx.lineWidth = 1;
      ctx.stroke();

      let ky = cy + 3;
      for (let ki = 0; ki < row.kvRows.length; ki++) {
        const kv = row.kvRows[ki];
        if (ki > 0) {
          ctx.strokeStyle = palette.divider;
          ctx.lineWidth = 1;
          ctx.beginPath();
          ctx.moveTo(boxX + 8, ky);
          ctx.lineTo(boxX + innerW - 8, ky);
          ctx.stroke();
        }
        ctx.font = makeFont(11, 500, true);
        ctx.fillStyle = palette.textMuted;
        ctx.textBaseline = "middle";
        ctx.fillText(kv.key, boxX + 9, ky + 11.5);
        drawWrappedLines(ctx, kv.valueLines, boxX + 9 + row.kvKeyWidth + 8, ky + 3, palette);
        ky += kv.height;
      }
    } else if (row.jsonLines.length > 0) {
      if (cy > ry + 8) cy += 6;
      const boxX = x + 11;
      ctx.fillStyle = palette.codeBg;
      fillRoundRect(ctx, boxX, cy, innerW, row.jsonBoxHeight, 7);
      ctx.fill();
      ctx.strokeStyle = palette.codeBorder;
      ctx.lineWidth = 1;
      ctx.stroke();

      ctx.font = makeFont(11, 400, true);
      ctx.fillStyle = palette.codeText;
      ctx.textBaseline = "middle";
      let jy = cy + 7;
      for (const line of row.jsonLines) {
        ctx.fillText(line, boxX + 9, jy + 8);
        jy += 16.5;
      }
      if (row.omittedLines > 0) {
        ctx.font = makeFont(10.5, 500);
        ctx.fillStyle = palette.textFaint;
        ctx.fillText(labels.omittedLines(row.omittedLines), boxX + 9, jy + 8);
      }
    } else if (row.bodyBlocks.length > 0) {
      if (cy > ry + 8) cy += 6;
      const boxX = x + 11;
      ctx.fillStyle = palette.quoteBg;
      fillRoundRect(ctx, boxX, cy, innerW, row.bodyBlocksHeight, 7);
      ctx.fill();
      ctx.strokeStyle = palette.divider;
      ctx.lineWidth = 1;
      ctx.stroke();

      let by = cy + 7;
      for (const b of row.bodyBlocks) {
        drawNested(ctx, b, boxX + 9, by, Math.max(80, innerW - 18));
        by += b.height;
      }
      if (row.omittedLines > 0) {
        ctx.font = makeFont(10.5, 500);
        ctx.fillStyle = palette.textFaint;
        ctx.textBaseline = "middle";
        ctx.fillText(labels.omittedLines(row.omittedLines), boxX + 9, by + 8);
      }
    }

    ry += row.height;
  }

  if (card.extraItemsCount > 0) {
    ctx.strokeStyle = palette.divider;
    ctx.lineWidth = 1;
    ctx.beginPath();
    ctx.moveTo(x + 1, ry);
    ctx.lineTo(x + width - 1, ry);
    ctx.stroke();
    ctx.font = makeFont(11, 500);
    ctx.fillStyle = palette.textMuted;
    ctx.textBaseline = "middle";
    ctx.fillText(labels.moreItems(card.extraItemsCount), x + 11, ry + 12);
  }
}

function alertVisuals(
  alert: AlertKind | undefined,
  palette: ShareThemePalette
): { bg: string; bar: string; badgeText: string } {
  switch (alert) {
    case "tip":
      return { bg: palette.alertTipBg, bar: palette.okDot, badgeText: palette.okDot };
    case "important":
      return { bg: palette.alertImportantBg, bar: palette.importantDot, badgeText: palette.importantDot };
    case "warning":
      return { bg: palette.alertWarnBg, bar: palette.warnDot, badgeText: palette.warnDot };
    case "caution":
      return { bg: palette.alertCautionBg, bar: palette.errDot, badgeText: palette.errDot };
    case "note":
      return { bg: palette.alertNoteBg, bar: palette.accent, badgeText: palette.accent };
    default:
      return { bg: palette.quoteBg, bar: palette.accent, badgeText: palette.accent };
  }
}

export function drawBlockquoteBlock(
  ctx: CanvasRenderingContext2D,
  block: Extract<LayoutBlock, { kind: "blockquote" }>,
  x: number,
  y: number,
  width: number,
  palette: ShareThemePalette,
  drawNested: NestedBlockPainter
): void {
  const boxH = block.height - 10;
  const vis = alertVisuals(block.alert, palette);

  ctx.fillStyle = vis.bg;
  fillRoundRect(ctx, x, y, width, boxH, 10);
  ctx.fill();

  ctx.save();
  fillRoundRect(ctx, x, y, width, boxH, 10);
  ctx.clip();
  ctx.fillStyle = vis.bar;
  ctx.fillRect(x, y, 3.5, boxH);
  ctx.restore();

  ctx.strokeStyle = palette.quoteCardBorder;
  ctx.lineWidth = 1;
  fillRoundRect(ctx, x, y, width, boxH, 10);
  ctx.stroke();

  let cursorY = y + 10;
  if (block.alert) {
    const label = block.alert.toUpperCase();
    ctx.font = `700 10.5px ${FONT_MONO}`;
    const pillW = Math.ceil(ctx.measureText(label).width + 12);
    ctx.fillStyle = palette.cardBg;
    fillRoundRect(ctx, x + 15, cursorY, pillW, 18, 5);
    ctx.fill();
    ctx.strokeStyle = vis.bar;
    ctx.lineWidth = 0.85;
    ctx.stroke();
    ctx.fillStyle = vis.badgeText;
    ctx.textBaseline = "middle";
    ctx.fillText(label, x + 21, cursorY + 9.5);
    cursorY += 25;
  }

  if (block.blocks && block.blocks.length > 0) {
    const innerW = Math.max(120, width - 28);
    for (const inner of block.blocks) {
      drawNested(ctx, inner, x + 15, cursorY, innerW);
      cursorY += inner.height;
    }
  } else {
    drawWrappedLines(ctx, block.lines, x + 15, cursorY, palette);
  }
}

export function drawDetailsBlock(
  ctx: CanvasRenderingContext2D,
  block: Extract<LayoutBlock, { kind: "details" }>,
  x: number,
  y: number,
  width: number,
  palette: ShareThemePalette,
  drawNested: NestedBlockPainter
): void {
  const boxH = block.height - 10;
  const headerH = Math.max(30, block.summaryLines.length * 19 + 14);

  ctx.fillStyle = palette.quoteBg;
  fillRoundRect(ctx, x, y, width, boxH, 10);
  ctx.fill();

  ctx.save();
  fillRoundRect(ctx, x, y, width, boxH, 10);
  ctx.clip();
  ctx.fillStyle = palette.quoteHeaderBg;
  ctx.fillRect(x, y, width, headerH);
  if (block.blocks.length > 0) {
    ctx.strokeStyle = palette.quoteCardBorder;
    ctx.lineWidth = 1;
    ctx.beginPath();
    ctx.moveTo(x, y + headerH);
    ctx.lineTo(x + width, y + headerH);
    ctx.stroke();
  }
  ctx.restore();

  ctx.strokeStyle = palette.quoteCardBorder;
  ctx.lineWidth = 1;
  fillRoundRect(ctx, x, y, width, boxH, 10);
  ctx.stroke();

  const chevX = x + 13;
  const chevY = y + headerH / 2;
  ctx.save();
  ctx.strokeStyle = palette.textMuted;
  ctx.lineWidth = 1.5;
  ctx.lineCap = "round";
  ctx.lineJoin = "round";
  ctx.beginPath();
  ctx.moveTo(chevX, chevY - 2);
  ctx.lineTo(chevX + 3.5, chevY + 2);
  ctx.lineTo(chevX + 7, chevY - 2);
  ctx.stroke();
  ctx.restore();

  drawWrappedLines(ctx, block.summaryLines, x + 26, y + 7, palette);

  if (block.blocks.length > 0) {
    let cursorY = y + headerH + 9;
    const innerW = Math.max(120, width - 24);
    for (const inner of block.blocks) {
      drawNested(ctx, inner, x + 12, cursorY, innerW);
      cursorY += inner.height;
    }
  }
}

export function drawUserBoxBlock(
  ctx: CanvasRenderingContext2D,
  block: Extract<LayoutBlock, { kind: "user-box" }>,
  x: number,
  y: number,
  width: number,
  palette: ShareThemePalette,
  labels: LayoutLabels,
  drawNested: NestedBlockPainter
): void {
  const boxH = block.height - 4;
  ctx.fillStyle = palette.userBg;
  fillRoundRect(ctx, x, y, width, boxH, 14);
  ctx.fill();
  ctx.strokeStyle = palette.userBorder;
  ctx.lineWidth = 1;
  ctx.stroke();

  const innerX = x + 16;
  const innerW = Math.max(120, width - 32);
  let cursorY = y + 12;

  if (block.collabHeader) {
    const midY = cursorY + 9.5;
    drawPill(ctx, block.collabHeader.roleBadge, innerX, midY, palette);
    if (block.collabHeader.modeBadge) {
      drawPill(ctx, block.collabHeader.modeBadge, innerX + block.collabHeader.roleBadge.width + 6, midY, palette);
    }
    cursorY += 22;
    if (block.collabHeader.titleLines.length > 0) {
      cursorY += 4;
      cursorY = drawWrappedLines(ctx, block.collabHeader.titleLines, innerX, cursorY, palette);
    }
  } else {
    ctx.font = `600 11px ${FONT_SANS}`;
    ctx.fillStyle = palette.accent;
    ctx.textBaseline = "middle";
    ctx.fillText(block.roleLabel.toUpperCase(), innerX, cursorY + 7);
    cursorY += 18;
  }

  if (block.contextCards.length > 0) {
    cursorY += 6;
    for (let i = 0; i < block.contextCards.length; i++) {
      if (i > 0) cursorY += 8;
      const card = block.contextCards[i];
      drawContextCard(ctx, card, innerX, cursorY, innerW, palette, labels, drawNested);
      cursorY += card.height;
    }
  }

  if (block.bodyBlocks.length > 0) {
    cursorY += block.contextCards.length > 0 ? 10 : 4;
    for (const inner of block.bodyBlocks) {
      drawNested(ctx, inner, innerX, cursorY, innerW);
      cursorY += inner.height;
    }
  } else if (block.lines.length > 0) {
    cursorY += block.contextCards.length > 0 ? 10 : 4;
    drawWrappedLines(ctx, block.lines, innerX, cursorY, palette);
  }
}
