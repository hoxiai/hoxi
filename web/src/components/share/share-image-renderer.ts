// web/src/components/share/share-image-renderer.ts
// High-DPI Canvas 2D painter, Shiki/image preloader, clipboard image copy,
// and PNG download helper for local Hoxi session share cards.
import type { ThemedToken } from "shiki";
import { loadMarkdownImage, resolveMediaPlaybackURL } from "../../store/workspace";
import { parseMarkdownBlocks } from "../timeline/markdown/blocks";
import { createSyntaxHighlightClient } from "../timeline/syntax-highlight-client";
import { sanitizeSvgMarkup, svgToDataURL } from "../timeline/svg-sanitizer";
import { buildPaginatedShareLayout } from "./share-image-layout";
import {
  DARK_PALETTE,
  drawHoxiMatrixMark,
  FONT_MONO,
  FONT_SANS,
  LIGHT_PALETTE,
  type LayoutBlock,
  type LayoutLabels,
  type LoadedImageInfo,
  type ShareThemePalette,
  type WrappedLine,
} from "./share-image-text";
import type { ShareDocument, ShareImageAsset } from "./share-image-model";

export interface RenderedSharePage {
  index: number;
  total: number;
  width: number;
  height: number;
  blob: Blob;
  objectURL: string;
}

export interface RenderedShareResult {
  pages: RenderedSharePage[];
  autoPaginated: boolean;
  dispose: () => void;
}

export interface RenderShareImageParams {
  doc: ShareDocument;
  theme: "light" | "dark";
  widthOption: "standard" | "wide";
  layoutMode: "single" | "paged";
  showBrandFooter: boolean;
  workspaceDir: string;
  labels: LayoutLabels;
  formattedDate: string;
  signal?: AbortSignal;
}

const RENDER_SCALE = 2;
const IMAGE_LOAD_TIMEOUT_MS = 6000;

function loadImageElement(src: string, crossOrigin?: string): Promise<LoadedImageInfo | null> {
  return new Promise((resolve) => {
    const img = new Image();
    if (crossOrigin) img.crossOrigin = crossOrigin;
    let settled = false;
    const done = (result: LoadedImageInfo | null) => {
      if (settled) return;
      settled = true;
      clearTimeout(timer);
      resolve(result);
    };
    const timer = setTimeout(() => done(null), IMAGE_LOAD_TIMEOUT_MS);
    img.onload = () =>
      done(
        img.naturalWidth > 0 && img.naturalHeight > 0
          ? { image: img, naturalWidth: img.naturalWidth, naturalHeight: img.naturalHeight }
          : null
      );
    img.onerror = () => done(null);
    img.src = src;
  });
}

async function fetchAsObjectURLImage(url: string): Promise<LoadedImageInfo | null> {
  try {
    const resp = await fetch(url);
    if (!resp.ok) return null;
    const blob = await resp.blob();
    if (!blob.type.startsWith("image/") && blob.size === 0) return null;
    const objectURL = URL.createObjectURL(blob);
    try {
      return await loadImageElement(objectURL);
    } finally {
      URL.revokeObjectURL(objectURL);
    }
  } catch {
    return null;
  }
}

async function resolveAssetImage(
  asset: ShareImageAsset,
  workspaceDir: string,
  sessionID: string
): Promise<LoadedImageInfo | null> {
  if (asset.sourceKind === "data") {
    return loadImageElement(asset.src);
  }
  if (asset.sourceKind === "attachment") {
    // Prefer same-origin fetch via Wails AssetServer so canvas never taints.
    const sameOrigin = await fetchAsObjectURLImage(asset.src);
    if (sameOrigin) return sameOrigin;
    const loopback = resolveMediaPlaybackURL(asset.src);
    if (loopback && loopback !== asset.src) {
      return (await fetchAsObjectURLImage(loopback)) ?? loadImageElement(loopback, "anonymous");
    }
    return null;
  }
  if (asset.sourceKind === "markdown") {
    const dataURL = await loadMarkdownImage(asset.src, workspaceDir, sessionID);
    if (!dataURL) return null;
    return loadImageElement(dataURL);
  }
  // Remote http(s) images: try CORS-safe fetch first; never taint the canvas.
  return (await fetchAsObjectURLImage(asset.src)) ?? loadImageElement(asset.src, "anonymous");
}

function collectCodeBlocks(doc: ShareDocument): Array<{ lang: string; code: string }> {
  const seen = new Set<string>();
  const out: Array<{ lang: string; code: string }> = [];
  for (const ex of doc.exchanges) {
    if (!ex.assistantText) continue;
    for (const block of parseMarkdownBlocks(ex.assistantText)) {
      if (block.type !== "code") continue;
      const lang = block.lang.trim().toLowerCase();
      const key = `${lang}\0${block.code}`;
      if (seen.has(key)) continue;
      seen.add(key);
      out.push({ lang, code: block.code });
    }
  }
  return out;
}

async function preloadHighlights(
  blocks: Array<{ lang: string; code: string }>,
  theme: "light" | "dark",
  signal?: AbortSignal
): Promise<Map<string, ThemedToken[][]>> {
  const map = new Map<string, ThemedToken[][]>();
  const highlightable = blocks.filter((b) => b.lang && b.lang !== "text" && b.lang !== "svg");
  if (highlightable.length === 0) return map;

  const client = createSyntaxHighlightClient();
  try {
    for (const block of highlightable) {
      if (signal?.aborted) break;
      const res = await client.highlight(block.code, block.lang, theme);
      if (res?.tokens) {
        map.set(`${block.lang}\0${block.code}`, res.tokens);
      }
    }
  } finally {
    client.dispose();
  }
  return map;
}

async function preloadSvgBlocks(
  blocks: Array<{ lang: string; code: string }>,
  signal?: AbortSignal
): Promise<Map<string, LoadedImageInfo>> {
  const map = new Map<string, LoadedImageInfo>();
  for (const block of blocks) {
    if (signal?.aborted) break;
    if (block.lang !== "svg") continue;
    const clean = sanitizeSvgMarkup(block.code);
    if (!clean.svg) continue;
    const loaded = await loadImageElement(svgToDataURL(clean.svg));
    if (loaded) map.set(block.code, loaded);
  }
  return map;
}

// ---------------------------------------------------------------------------
// Canvas 2D drawing primitives
// ---------------------------------------------------------------------------

function fillRoundRect(
  ctx: CanvasRenderingContext2D,
  x: number,
  y: number,
  w: number,
  h: number,
  r: number
) {
  const radius = Math.min(r, w / 2, h / 2);
  ctx.beginPath();
  ctx.moveTo(x + radius, y);
  ctx.arcTo(x + w, y, x + w, y + h, radius);
  ctx.arcTo(x + w, y + h, x, y + h, radius);
  ctx.arcTo(x, y + h, x, y, radius);
  ctx.arcTo(x, y, x + w, y, radius);
  ctx.closePath();
}

function drawWrappedLines(
  ctx: CanvasRenderingContext2D,
  lines: WrappedLine[],
  startX: number,
  startY: number,
  palette: ShareThemePalette,
  align: "left" | "center" | "right" = "left",
  boxWidth = 0
): number {
  let y = startY;
  ctx.textBaseline = "middle";
  for (const line of lines) {
    const lineW = line.runs.reduce((s, r) => s + r.width, 0);
    let x = startX;
    if (align === "center" && boxWidth > 0) x = startX + Math.max(0, (boxWidth - lineW) / 2);
    else if (align === "right" && boxWidth > 0) x = startX + Math.max(0, boxWidth - lineW);

    const midY = y + line.height / 2;
    for (const run of line.runs) {
      if (run.inlineCode) {
        ctx.fillStyle = palette.inlineCodeBg;
        fillRoundRect(ctx, x, midY - 9.5, run.width, 19, 5);
        ctx.fill();
        ctx.font = run.font;
        ctx.fillStyle = run.color;
        ctx.fillText(run.text, x + 4, midY + 0.5);
      } else {
        ctx.font = run.font;
        ctx.fillStyle = run.color;
        ctx.fillText(run.text, x, midY);
        if (run.strike) {
          ctx.strokeStyle = run.color;
          ctx.lineWidth = 1;
          ctx.beginPath();
          ctx.moveTo(x, midY);
          ctx.lineTo(x + run.width, midY);
          ctx.stroke();
        }
        if (run.underline) {
          ctx.strokeStyle = run.color;
          ctx.lineWidth = 1;
          ctx.beginPath();
          ctx.moveTo(x, midY + 7);
          ctx.lineTo(x + run.width, midY + 7);
          ctx.stroke();
        }
      }
      x += run.width;
    }
    y += line.height;
  }
  return y;
}

function drawBlock(
  ctx: CanvasRenderingContext2D,
  block: LayoutBlock,
  x: number,
  y: number,
  contentWidth: number,
  palette: ShareThemePalette,
  labels: LayoutLabels
) {
  switch (block.kind) {
    case "spacer":
      return;
    case "divider": {
      ctx.strokeStyle = palette.divider;
      ctx.lineWidth = 1;
      ctx.beginPath();
      ctx.moveTo(x, y + block.marginTop);
      ctx.lineTo(x + contentWidth, y + block.marginTop);
      ctx.stroke();
      return;
    }
    case "role-header": {
      const dotY = y + 12;
      drawHoxiMatrixMark(ctx, x - 2, dotY - 9, 18, palette);
      ctx.font = `600 12.5px ${FONT_SANS}`;
      ctx.fillStyle = palette.textPrimary;
      ctx.textBaseline = "middle";
      ctx.fillText(block.label, x + 22, dotY);
      if (block.modelID) {
        const labelW = ctx.measureText(block.label).width;
        const modelX = x + 22 + labelW + 8;
        const maxPillW = Math.max(0, x + contentWidth - modelX);
        if (maxPillW > 32) {
          ctx.font = `500 11px ${FONT_MONO}`;
          let modelText = block.modelID;
          while (modelText.length > 4 && ctx.measureText(modelText).width + 12 > maxPillW) {
            modelText = `${modelText.slice(0, -2)}…`;
          }
          const modelW = ctx.measureText(modelText).width;
          ctx.fillStyle = palette.pillBg;
          fillRoundRect(ctx, modelX, dotY - 9, Math.min(maxPillW, modelW + 12), 18, 5);
          ctx.fill();
          ctx.strokeStyle = palette.pillBorder;
          ctx.lineWidth = 1;
          ctx.stroke();
          ctx.fillStyle = palette.textMuted;
          ctx.fillText(modelText, modelX + 6, dotY + 0.5);
        }
      }
      return;
    }
    case "user-box": {
      const boxH = block.height - 4;
      ctx.fillStyle = palette.userBg;
      fillRoundRect(ctx, x, y, contentWidth, boxH, 14);
      ctx.fill();
      ctx.strokeStyle = palette.userBorder;
      ctx.lineWidth = 1;
      ctx.stroke();

      ctx.font = `600 11px ${FONT_SANS}`;
      ctx.fillStyle = palette.accent;
      ctx.textBaseline = "middle";
      ctx.fillText(block.roleLabel.toUpperCase(), x + 16, y + 18);
      drawWrappedLines(ctx, block.lines, x + 16, y + 30, palette);
      return;
    }
    case "heading":
      drawWrappedLines(ctx, block.lines, x, y + 4, palette);
      return;
    case "paragraph":
      drawWrappedLines(ctx, block.lines, x + (block.indentX ?? 0), y + 2, palette);
      return;
    case "list-item": {
      ctx.font = `600 14px ${FONT_SANS}`;
      ctx.fillStyle = palette.textMuted;
      ctx.textBaseline = "middle";
      ctx.fillText(block.marker, x + block.markerX, y + 12);
      drawWrappedLines(ctx, block.lines, x + block.textX, y, palette);
      return;
    }
    case "blockquote": {
      const boxH = block.height - 8;
      ctx.fillStyle = palette.quoteBg;
      fillRoundRect(ctx, x, y, contentWidth, boxH, 10);
      ctx.fill();
      ctx.fillStyle = block.alert === "caution" || block.alert === "warning" ? palette.warnDot : palette.accent;
      fillRoundRect(ctx, x, y, 3.5, boxH, 2);
      ctx.fill();
      let textY = y + 8;
      if (block.alert) {
        ctx.font = `700 11px ${FONT_SANS}`;
        ctx.fillStyle = palette.accent;
        ctx.textBaseline = "middle";
        ctx.fillText(block.alert.toUpperCase(), x + 14, textY + 8);
        textY += 20;
      }
      drawWrappedLines(ctx, block.lines, x + 14, textY, palette);
      return;
    }
    case "thinking": {
      const boxH = block.height - 6;
      ctx.fillStyle = palette.thinkingBg;
      fillRoundRect(ctx, x, y, contentWidth, boxH, 10);
      ctx.fill();
      ctx.strokeStyle = palette.thinkingBorder;
      ctx.lineWidth = 1;
      ctx.stroke();
      ctx.font = `600 11px ${FONT_SANS}`;
      ctx.fillStyle = palette.textFaint;
      ctx.textBaseline = "middle";
      ctx.fillText(block.label, x + 14, y + 16);
      drawWrappedLines(ctx, block.lines, x + 14, y + 28, palette);
      return;
    }
    case "tool-pills": {
      ctx.textBaseline = "middle";
      for (const pill of block.pills) {
        const px = x + pill.x;
        const py = y + pill.y;
        ctx.fillStyle = palette.pillBg;
        fillRoundRect(ctx, px, py, pill.width, pill.height, 7);
        ctx.fill();
        ctx.strokeStyle = palette.pillBorder;
        ctx.lineWidth = 1;
        ctx.stroke();

        const dotColor =
          pill.status === "error" ? palette.errDot : pill.status === "running" || pill.status === "pending" ? palette.warnDot : palette.okDot;
        ctx.fillStyle = dotColor;
        ctx.beginPath();
        ctx.arc(px + 10, py + pill.height / 2, 3, 0, Math.PI * 2);
        ctx.fill();

        ctx.font = `600 11.5px ${FONT_SANS}`;
        ctx.fillStyle = palette.textSecondary;
        ctx.fillText(pill.label, px + 18, py + pill.height / 2 + 0.5);
        if (pill.detail) {
          const lw = ctx.measureText(pill.label).width;
          ctx.font = `400 11.5px ${FONT_MONO}`;
          ctx.fillStyle = palette.textMuted;
          ctx.fillText(pill.detail, px + 18 + lw + 6, py + pill.height / 2 + 0.5);
        }
      }
      return;
    }
    case "code": {
      const boxH = block.height - 10;
      const headerH = 28;
      ctx.fillStyle = palette.codeBg;
      fillRoundRect(ctx, x, y, contentWidth, boxH, 12);
      ctx.fill();

      // Code header strip
      ctx.save();
      fillRoundRect(ctx, x, y, contentWidth, boxH, 12);
      ctx.clip();
      ctx.fillStyle = palette.codeHeaderBg;
      ctx.fillRect(x, y, contentWidth, headerH);
      ctx.strokeStyle = palette.codeBorder;
      ctx.lineWidth = 1;
      ctx.beginPath();
      ctx.moveTo(x, y + headerH);
      ctx.lineTo(x + contentWidth, y + headerH);
      ctx.stroke();
      ctx.restore();

      ctx.strokeStyle = palette.codeBorder;
      ctx.lineWidth = 1;
      fillRoundRect(ctx, x, y, contentWidth, boxH, 12);
      ctx.stroke();

      ctx.font = `600 11px ${FONT_MONO}`;
      ctx.fillStyle = palette.textMuted;
      ctx.textBaseline = "middle";
      ctx.fillText(block.lang, x + 14, y + headerH / 2);

      let cy = y + headerH + 10;
      for (const line of block.lines) {
        let cx = x + 14;
        for (const tok of line.tokens) {
          const style = tok.italic ? "italic " : "";
          const weight = tok.bold ? 600 : 400;
          ctx.font = `${style}${weight} 12.5px ${FONT_MONO}`;
          ctx.fillStyle = tok.color;
          ctx.fillText(tok.text, cx, cy + 9.5);
          cx += ctx.measureText(tok.text).width;
        }
        cy += 19.5;
      }
      if (block.truncatedLines > 0) {
        ctx.font = `500 11px ${FONT_SANS}`;
        ctx.fillStyle = palette.textFaint;
        ctx.fillText(labels.truncatedCodeLines(block.truncatedLines), x + 14, cy + 8);
      }
      return;
    }
    case "table": {
      const totalW = block.colWidths.reduce((a, b) => a + b, 0);
      const totalH = block.headerRow.height + block.bodyRows.reduce((s, r) => s + r.height, 0);
      ctx.save();
      fillRoundRect(ctx, x, y, totalW, totalH, 10);
      ctx.clip();
      ctx.fillStyle = palette.tableHeaderBg;
      ctx.fillRect(x, y, totalW, block.headerRow.height);

      let ry = y;
      const drawRow = (row: { height: number; cells: WrappedLine[][] }, isHeader: boolean) => {
        let cx = x;
        for (let ci = 0; ci < block.colWidths.length; ci++) {
          const cw = block.colWidths[ci];
          const cellLines = row.cells[ci] ?? [];
          drawWrappedLines(ctx, cellLines, cx + 8, ry + 7, palette, block.aligns[ci] || "left", cw - 16);
          if (ci > 0) {
            ctx.strokeStyle = palette.tableBorder;
            ctx.lineWidth = 1;
            ctx.beginPath();
            ctx.moveTo(cx, ry);
            ctx.lineTo(cx, ry + row.height);
            ctx.stroke();
          }
          cx += cw;
        }
        if (!isHeader) {
          ctx.strokeStyle = palette.tableBorder;
          ctx.lineWidth = 1;
          ctx.beginPath();
          ctx.moveTo(x, ry);
          ctx.lineTo(x + totalW, ry);
          ctx.stroke();
        }
        ry += row.height;
      };

      drawRow(block.headerRow, true);
      for (const row of block.bodyRows) drawRow(row, false);
      ctx.restore();

      ctx.strokeStyle = palette.tableBorder;
      ctx.lineWidth = 1;
      fillRoundRect(ctx, x, y, totalW, totalH, 10);
      ctx.stroke();
      return;
    }
    case "image": {
      if (!block.loaded) {
        ctx.fillStyle = palette.pillBg;
        fillRoundRect(ctx, x, y, block.drawWidth, block.drawHeight, 8);
        ctx.fill();
        ctx.strokeStyle = palette.pillBorder;
        ctx.lineWidth = 1;
        ctx.stroke();
        ctx.font = `500 12px ${FONT_SANS}`;
        ctx.fillStyle = palette.textMuted;
        ctx.textBaseline = "middle";
        ctx.fillText(block.missingLabel || block.caption, x + 12, y + block.drawHeight / 2);
        return;
      }
      ctx.save();
      fillRoundRect(ctx, x, y, block.drawWidth, block.drawHeight, 12);
      ctx.clip();
      ctx.drawImage(block.loaded.image, x, y, block.drawWidth, block.drawHeight);
      ctx.restore();

      ctx.strokeStyle = palette.cardBorder;
      ctx.lineWidth = 1;
      fillRoundRect(ctx, x, y, block.drawWidth, block.drawHeight, 12);
      ctx.stroke();

      if (block.caption) {
        ctx.font = `400 11.5px ${FONT_SANS}`;
        ctx.fillStyle = palette.textFaint;
        ctx.textBaseline = "middle";
        ctx.fillText(block.caption, x + 2, y + block.drawHeight + 13);
      }
      return;
    }
  }
}

function canvasToPngBlob(canvas: HTMLCanvasElement): Promise<Blob> {
  return new Promise((resolve, reject) => {
    canvas.toBlob((blob) => {
      if (blob) resolve(blob);
      else reject(new Error("Canvas PNG encoding failed"));
    }, "image/png");
  });
}

// ---------------------------------------------------------------------------
// Public entry: render ShareDocument into high-DPI PNG page(s)
// ---------------------------------------------------------------------------

export async function renderShareDocumentToImages(
  params: RenderShareImageParams
): Promise<RenderedShareResult> {
  const { doc, theme, widthOption, layoutMode, showBrandFooter, workspaceDir, labels, formattedDate, signal } = params;
  const palette: ShareThemePalette = theme === "dark" ? DARK_PALETTE : LIGHT_PALETTE;
  const cardWidth = widthOption === "wide" ? 920 : 680;

  const codeBlocks = collectCodeBlocks(doc);
  const enabledAssets = doc.exchanges.flatMap((ex) => ex.images);

  // Preload Shiki highlights, inline SVG diagrams, and embedded images in parallel
  const [highlights, svgImages, imageEntries] = await Promise.all([
    preloadHighlights(codeBlocks, theme, signal),
    preloadSvgBlocks(codeBlocks, signal),
    Promise.all(
      enabledAssets.map(async (asset) => {
        if (signal?.aborted) return [asset.id, null] as const;
        return [asset.id, await resolveAssetImage(asset, workspaceDir, doc.sessionID)] as const;
      })
    ),
  ]);

  if (signal?.aborted) {
    throw new DOMException("Aborted", "AbortError");
  }

  const loadedImages = new Map<string, LoadedImageInfo>();
  for (const [id, info] of imageEntries) {
    if (info) loadedImages.set(id, info);
  }

  // Scratch canvas strictly for exact font width measurement
  const measureCanvas = document.createElement("canvas");
  const measureCtx = measureCanvas.getContext("2d");
  if (!measureCtx) throw new Error("Canvas 2D context unavailable");
  const measure = (text: string, font: string): number => {
    measureCtx.font = font;
    return measureCtx.measureText(text).width;
  };

  const layout = buildPaginatedShareLayout({
    doc,
    palette,
    cardWidth,
    layoutMode,
    showBrandFooter,
    measure,
    highlights,
    loadedImages,
    svgImages,
    labels,
    formattedDate,
  });

  const renderedPages: RenderedSharePage[] = [];
  const totalPages = layout.pages.length;

  try {
    for (let pageIdx = 0; pageIdx < totalPages; pageIdx++) {
      if (signal?.aborted) throw new DOMException("Aborted", "AbortError");
      const page = layout.pages[pageIdx];
      const logicalW = layout.cardWidth;
      const logicalH = Math.ceil(page.totalHeight);

      const canvas = document.createElement("canvas");
      canvas.width = Math.round(logicalW * RENDER_SCALE);
      canvas.height = Math.round(logicalH * RENDER_SCALE);
      const ctx = canvas.getContext("2d");
      if (!ctx) throw new Error("Canvas 2D context unavailable");

      ctx.scale(RENDER_SCALE, RENDER_SCALE);

      // Outer backdrop
      ctx.fillStyle = palette.outerBg;
      ctx.fillRect(0, 0, logicalW, logicalH);

      // Elevated inner card
      const cardX = layout.outerPadding;
      const cardY = layout.outerPadding;
      const cardW = logicalW - layout.outerPadding * 2;
      const cardH = logicalH - layout.outerPadding * 2;

      ctx.save();
      ctx.shadowColor = palette.cardShadow;
      ctx.shadowBlur = 24;
      ctx.shadowOffsetX = 0;
      ctx.shadowOffsetY = 8;
      ctx.fillStyle = palette.cardBg;
      fillRoundRect(ctx, cardX, cardY, cardW, cardH, 20);
      ctx.fill();
      ctx.restore();

      ctx.strokeStyle = palette.cardBorder;
      ctx.lineWidth = 1;
      fillRoundRect(ctx, cardX, cardY, cardW, cardH, 20);
      ctx.stroke();

      const innerX = cardX + layout.cardPaddingX;
      let cursorY = cardY + layout.cardPaddingY;

      // Page 1 header
      if (pageIdx === 0) {
        cursorY = drawWrappedLines(ctx, layout.header.titleLines, innerX, cursorY, palette);
        if (layout.header.metaChips.length > 0) {
          cursorY += 6;
          let mx = innerX;
          const maxMetaX = innerX + layout.contentWidth;
          ctx.font = `500 11.5px ${FONT_SANS}`;
          ctx.textBaseline = "middle";
          for (let ci = 0; ci < layout.header.metaChips.length; ci++) {
            const chip = layout.header.metaChips[ci];
            const sepW = ci > 0 ? 12 : 0;
            const cw = ctx.measureText(chip).width;
            if (mx + sepW + cw > maxMetaX) break;
            if (ci > 0) {
              ctx.fillStyle = palette.textFaint;
              ctx.fillText("·", mx, cursorY + 10);
              mx += 12;
            }
            ctx.fillStyle = ci === 0 ? palette.textSecondary : palette.textMuted;
            ctx.fillText(chip, mx, cursorY + 10);
            mx += cw + 8;
          }
          cursorY += 22;
        }
        cursorY += 10;
        ctx.strokeStyle = palette.divider;
        ctx.lineWidth = 1;
        ctx.beginPath();
        ctx.moveTo(innerX, cursorY);
        ctx.lineTo(innerX + layout.contentWidth, cursorY);
        ctx.stroke();
        cursorY += 16;
      }

      // Page content blocks
      for (const block of page.blocks) {
        drawBlock(ctx, block, innerX, cursorY, layout.contentWidth, palette, labels);
        cursorY += block.height;
      }

      // Brand footer / page counter
      if (showBrandFooter || totalPages > 1) {
        const footerTop = cardY + cardH - layout.cardPaddingY - layout.footerHeight + 16;
        ctx.strokeStyle = palette.divider;
        ctx.lineWidth = 1;
        ctx.beginPath();
        ctx.moveTo(innerX, footerTop);
        ctx.lineTo(innerX + layout.contentWidth, footerTop);
        ctx.stroke();

        const footerMidY = footerTop + 20;
        ctx.textBaseline = "middle";
        if (showBrandFooter) {
          drawHoxiMatrixMark(ctx, innerX - 2, footerMidY - 10, 20, palette);
          ctx.font = `500 12px ${FONT_MONO}`;
          ctx.fillStyle = palette.textMuted;
          ctx.fillText("hoxi.ai", innerX + 24, footerMidY + 0.5);
        }
        if (totalPages > 1) {
          const counter = `${pageIdx + 1} / ${totalPages}`;
          ctx.font = `500 11.5px ${FONT_MONO}`;
          ctx.fillStyle = palette.textMuted;
          const tw = ctx.measureText(counter).width;
          ctx.fillText(counter, innerX + layout.contentWidth - tw, footerMidY);
        }
      }

      const blob = await canvasToPngBlob(canvas);
      const objectURL = URL.createObjectURL(blob);
      renderedPages.push({
        index: pageIdx,
        total: totalPages,
        width: canvas.width,
        height: canvas.height,
        blob,
        objectURL,
      });
    }
  } catch (err) {
    for (const page of renderedPages) URL.revokeObjectURL(page.objectURL);
    throw err;
  }

  return {
    pages: renderedPages,
    autoPaginated: layout.autoPaginated,
    dispose: () => {
      for (const page of renderedPages) URL.revokeObjectURL(page.objectURL);
    },
  };
}

export async function copyPngBlobToClipboard(blob: Blob): Promise<boolean> {
  if (
    typeof navigator === "undefined" ||
    !navigator.clipboard?.write ||
    typeof ClipboardItem === "undefined"
  ) {
    return false;
  }
  try {
    await navigator.clipboard.write([new ClipboardItem({ "image/png": blob })]);
    return true;
  } catch {
    return false;
  }
}

export function safeShareFilename(title: string): string {
  const cleaned = title.trim().replace(/[\\/:*?"<>|]+/g, "-").replace(/\s+/g, "-");
  return cleaned.slice(0, 72) || "hoxi-session";
}

export function downloadSharePages(pages: RenderedSharePage[], baseName: string): void {
  const stem = safeShareFilename(baseName);
  pages.forEach((page, idx) => {
    const filename = pages.length > 1 ? `${stem}-${idx + 1}.png` : `${stem}.png`;
    const trigger = () => {
      const url = URL.createObjectURL(page.blob);
      const link = document.createElement("a");
      link.href = url;
      link.download = filename;
      document.body.appendChild(link);
      link.click();
      link.remove();
      setTimeout(() => URL.revokeObjectURL(url), 60_000);
    };
    if (idx === 0) trigger();
    else setTimeout(trigger, idx * 150);
  });
}
