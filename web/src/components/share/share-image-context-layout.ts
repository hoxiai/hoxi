// web/src/components/share/share-image-context-layout.ts
// Structured layout builders for hoxi-context v1 quote/reference cards
// and PM/QA/OP collaboration prompt headers on shareable PNG cards.
import type { ShareCollabInfo, ShareContextBlock, ShareContextItem } from "./share-image-model";
import {
  makeFont,
  truncateTextToWidth,
  wrapMarkdownInline,
  wrapStyledSegments,
  type LayoutBlock,
  type LayoutLabels,
  type ShareContextCardLayout,
  type ShareContextKvRowLayout,
  type ShareContextRowLayout,
  type SharePillLayout,
  type ShareThemePalette,
  type ShareUserCollabHeader,
  type TextMeasureFn,
} from "./share-image-text";

const MAX_CONTEXT_ITEMS_RENDERED = 8;
const MAX_QUOTE_SOURCE_LINES = 26;
const MAX_JSON_PREVIEW_LINES = 14;
const MAX_STRUCTURED_BODY_LINES = 16;

export type NestedBlockBuilder = (
  markdown: string,
  maxWidth: number,
  style: { bodySize: number; bodyLineH: number; textColor: string; compact: boolean }
) => LayoutBlock[];

function clipSourceLines(text: string, maxLines: number): { clipped: string; omitted: number } {
  const normalized = text.replace(/\r\n/g, "\n").trim();
  if (!normalized) return { clipped: "", omitted: 0 };
  const rawLines = normalized.split("\n");
  if (rawLines.length <= maxLines) {
    return { clipped: normalized, omitted: 0 };
  }
  return {
    clipped: rawLines.slice(0, maxLines).join("\n"),
    omitted: rawLines.length - maxLines,
  };
}

function wrapPlainMonoLines(
  text: string,
  maxWidth: number,
  maxLines: number,
  measure: TextMeasureFn
): { lines: string[]; omitted: number } {
  const font = makeFont(11, 400, true);
  const rawLines = text.replace(/\r\n/g, "\n").split("\n");
  const out: string[] = [];
  let omitted = 0;

  for (let i = 0; i < rawLines.length; i++) {
    const expanded = rawLines[i].replace(/\t/g, "  ");
    if (out.length >= maxLines) {
      omitted += rawLines.length - i;
      break;
    }
    if (measure(expanded, font) <= maxWidth) {
      out.push(expanded);
      continue;
    }
    let buf = "";
    for (const ch of Array.from(expanded)) {
      if (buf && measure(buf + ch, font) > maxWidth) {
        out.push(buf);
        buf = ch;
        if (out.length >= maxLines) {
          omitted += rawLines.length - i;
          buf = "";
          break;
        }
      } else {
        buf += ch;
      }
    }
    if (buf && out.length < maxLines) out.push(buf);
  }
  return { lines: out, omitted };
}

function makePill(
  text: string,
  maxWidth: number,
  measure: TextMeasureFn,
  opts?: { mono?: boolean; tone?: SharePillLayout["tone"] }
): SharePillLayout | undefined {
  const mono = Boolean(opts?.mono);
  const font = makeFont(10.5, mono ? 500 : 600, mono);
  const clean = truncateTextToWidth(text, font, Math.max(24, maxWidth - 12), measure);
  if (!clean) return undefined;
  return {
    text: clean,
    width: Math.ceil(measure(clean, font) + 12),
    mono,
    tone: opts?.tone ?? "neutral",
  };
}

function measurePillRowWidth(pills: SharePillLayout[]): number {
  if (pills.length === 0) return 0;
  return pills.reduce((sum, p) => sum + p.width, 0) + (pills.length - 1) * 5;
}

function sumBlocksHeight(blocks: LayoutBlock[]): number {
  let end = blocks.length;
  while (end > 0 && blocks[end - 1].kind === "spacer") end--;
  blocks.length = end;
  return blocks.reduce((sum, b) => sum + b.height, 0);
}

export function buildUserCollabHeader(
  collab: ShareCollabInfo,
  maxWidth: number,
  palette: ShareThemePalette,
  measure: TextMeasureFn,
  labels: LayoutLabels
): ShareUserCollabHeader {
  const baseRole =
    collab.role === "pm"
      ? labels.collabRolePM
      : collab.role === "op"
        ? labels.collabRoleOP
        : labels.collabRoleQA;
  const roleText = collab.author && collab.author !== baseRole ? `${baseRole} · ${collab.author}` : baseRole;
  const roleBadge =
    makePill(roleText, Math.min(260, maxWidth * 0.65), measure, { tone: collab.role }) ?? {
      text: baseRole,
      width: 64,
      tone: collab.role,
    };
  const modeBadge = collab.mode
    ? makePill(collab.mode.toUpperCase(), 80, measure, { mono: true, tone: "neutral" })
    : undefined;
  const titleLines = collab.title
    ? wrapMarkdownInline(collab.title, maxWidth, 14, 20.5, 600, palette.textPrimary, palette, measure)
    : [];
  return { role: collab.role, roleBadge, modeBadge, titleLines };
}

function buildSingleQuoteCard(
  block: ShareContextBlock,
  maxWidth: number,
  palette: ShareThemePalette,
  measure: TextMeasureFn,
  labels: LayoutLabels,
  buildNested: NestedBlockBuilder
): ShareContextCardLayout {
  const item = block.items[0];
  const innerW = Math.max(120, maxWidth - 26);

  const sourcePill = block.sourceLabel
    ? makePill(block.sourceLabel, Math.min(140, innerW * 0.35), measure, { tone: "neutral" })
    : undefined;

  const pills: SharePillLayout[] = [];
  if (item.badge) {
    const p = makePill(item.badge, 120, measure, { tone: "accent" });
    if (p) pills.push(p);
  }
  if (item.refLabel) {
    const p = makePill(item.refLabel, 130, measure, { mono: true, tone: "neutral" });
    if (p) pills.push(p);
  }

  const reservedForHeader =
    17 + (sourcePill ? sourcePill.width + 6 : 0) + (pills.length > 0 ? measurePillRowWidth(pills) + 8 : 0);
  const titleAvail = Math.max(48, innerW - reservedForHeader);
  const titleFont = makeFont(11.5, 600);
  const rawTitle = item.title || item.subtitle || labels.quoted;
  const titleText = truncateTextToWidth(rawTitle, titleFont, titleAvail, measure);

  let bodyBlocks: LayoutBlock[] = [];
  let bodyHeight = 0;
  let omittedLines = 0;
  const rawBody = item.body || (item.title && item.subtitle ? item.subtitle : "");
  if (rawBody) {
    const { clipped, omitted } = clipSourceLines(rawBody, MAX_QUOTE_SOURCE_LINES);
    omittedLines = omitted;
    bodyBlocks = buildNested(clipped, innerW, {
      bodySize: 12.5,
      bodyLineH: 19.5,
      textColor: palette.textSecondary,
      compact: true,
    });
    bodyHeight = sumBlocksHeight(bodyBlocks);
  }

  const headerH = 20;
  const bodyGap = bodyHeight > 0 ? 5 : 0;
  const omittedH = omittedLines > 0 ? 18 : 0;
  const height = 9 + headerH + bodyGap + bodyHeight + omittedH + 8;

  return {
    variant: "quote",
    height,
    sourcePill,
    titleText,
    pills,
    bodyBlocks,
    bodyHeight,
    omittedLines,
  };
}

function buildStructuredRow(
  item: ShareContextItem,
  idx: number,
  total: number,
  maxWidth: number,
  palette: ShareThemePalette,
  measure: TextMeasureFn,
  buildNested: NestedBlockBuilder
): ShareContextRowLayout {
  const innerW = Math.max(120, maxWidth - 22);
  const indexLabel = total > 1 ? String(idx + 1) : undefined;
  const indexOffset = indexLabel ? 24 : 0;
  const headAvail = Math.max(80, innerW - indexOffset);

  const pills: SharePillLayout[] = [];
  if (item.badge) {
    const p = makePill(item.badge, 130, measure, { tone: "accent" });
    if (p) pills.push(p);
  }
  if (item.refLabel) {
    const p = makePill(item.refLabel, 150, measure, { mono: true, tone: "neutral" });
    if (p) pills.push(p);
  }

  const titleText = item.title || "";
  const titleLines = titleText
    ? wrapMarkdownInline(titleText, headAvail, 12, 18, 600, palette.textPrimary, palette, measure)
    : [];
  const titleLineWidth =
    titleLines.length === 1 ? titleLines[0].runs.reduce((sum, r) => sum + r.width, 0) : headAvail;
  const pillsW = measurePillRowWidth(pills);
  const titlePillsInline =
    pills.length > 0 &&
    (titleLines.length === 0 || (titleLines.length === 1 && titleLineWidth + 6 + pillsW <= headAvail));

  const subtitleLines = item.subtitle
    ? wrapMarkdownInline(item.subtitle, headAvail, 11.5, 17, 400, palette.textMuted, palette, measure)
    : [];

  let kvKeyWidth = 0;
  const kvRows: ShareContextKvRowLayout[] = [];
  let kvBoxHeight = 0;
  let jsonLines: string[] = [];
  let jsonBoxHeight = 0;
  let bodyBlocks: LayoutBlock[] = [];
  let bodyBlocksHeight = 0;
  let omittedLines = 0;

  if (item.kvPairs && item.kvPairs.length > 0) {
    const keyFont = makeFont(11, 500, true);
    const maxKeyBudget = Math.max(56, Math.floor((innerW - 18) * 0.36));
    for (const pair of item.kvPairs) {
      kvKeyWidth = Math.max(kvKeyWidth, Math.ceil(measure(pair.key, keyFont) + 12));
    }
    kvKeyWidth = Math.min(maxKeyBudget, Math.max(48, kvKeyWidth));
    const valW = Math.max(72, innerW - 18 - kvKeyWidth - 8);
    let innerH = 0;
    for (const pair of item.kvPairs) {
      const valueLines = wrapStyledSegments(
        [{ text: pair.value, font: makeFont(11.5, 500), color: palette.textPrimary }],
        valW,
        17,
        measure
      );
      const rowH = Math.max(22, valueLines.length * 17 + 6);
      kvRows.push({ key: truncateTextToWidth(pair.key, keyFont, kvKeyWidth - 6, measure), valueLines, height: rowH });
      innerH += rowH;
    }
    kvBoxHeight = innerH + 6;
  } else if (item.body && item.isJson) {
    const wrapped = wrapPlainMonoLines(item.body, Math.max(80, innerW - 18), MAX_JSON_PREVIEW_LINES, measure);
    jsonLines = wrapped.lines;
    omittedLines = wrapped.omitted;
    jsonBoxHeight = jsonLines.length * 16.5 + (omittedLines > 0 ? 16 : 0) + 14;
  } else if (item.body) {
    const { clipped, omitted } = clipSourceLines(item.body, MAX_STRUCTURED_BODY_LINES);
    omittedLines = omitted;
    bodyBlocks = buildNested(clipped, Math.max(80, innerW - 18), {
      bodySize: 12,
      bodyLineH: 18.5,
      textColor: palette.textSecondary,
      compact: true,
    });
    const innerH = sumBlocksHeight(bodyBlocks);
    bodyBlocksHeight = innerH + (omittedLines > 0 ? 16 : 0) + 14;
  }

  let h = 8;
  const headTextH = titleLines.length * 18;
  if (headTextH > 0) {
    h += headTextH;
    if (pills.length > 0 && !titlePillsInline) h += 22;
  } else if (pills.length > 0) {
    h += 19;
  } else if (indexLabel) {
    h += 18;
  }
  if (subtitleLines.length > 0) {
    h += (h > 8 ? 3 : 0) + subtitleLines.length * 17;
  }
  const detailH = kvBoxHeight || jsonBoxHeight || bodyBlocksHeight;
  if (detailH > 0) {
    h += (h > 8 ? 6 : 0) + detailH;
  }
  h += 8;

  return {
    height: h,
    indexLabel,
    titleLines,
    titlePillsInline,
    titleLineWidth,
    pills,
    subtitleLines,
    kvKeyWidth,
    kvRows,
    kvBoxHeight,
    jsonLines,
    jsonBoxHeight,
    bodyBlocks,
    bodyBlocksHeight,
    omittedLines,
  };
}

export function buildContextCardLayout(
  block: ShareContextBlock,
  maxWidth: number,
  palette: ShareThemePalette,
  measure: TextMeasureFn,
  labels: LayoutLabels,
  buildNested: NestedBlockBuilder
): ShareContextCardLayout {
  if (block.isSingleQuote) {
    return buildSingleQuoteCard(block, maxWidth, palette, measure, labels, buildNested);
  }

  const total = block.items.length;
  const visible = block.items.slice(0, MAX_CONTEXT_ITEMS_RENDERED);
  const extraItemsCount = Math.max(0, total - visible.length);
  const first = visible[0];

  const kindIcon: "entity" | "snapshot" | "record" | "batch" =
    total > 1 ? "batch" : first?.kind === "entity" ? "entity" : first?.kind === "snapshot" ? "snapshot" : "record";

  const sourcePill = block.sourceLabel
    ? makePill(block.sourceLabel, Math.min(130, maxWidth * 0.3), measure, { tone: "neutral" })
    : undefined;

  const headerBadge =
    total === 1 && first?.badge
      ? makePill(first.badge, 110, measure, { tone: "accent" })
      : total > 1
        ? makePill(`${total}`, 44, measure, { mono: true, tone: "accent" })
        : undefined;

  const headlineRaw =
    total === 1
      ? first?.title || first?.subtitle || labels.referencedSingle
      : labels.referencedItems(total);

  const headerReserved =
    22 + 18 + (sourcePill ? sourcePill.width + 6 : 0) + (headerBadge ? headerBadge.width + 8 : 0);
  const headlineText = truncateTextToWidth(
    headlineRaw,
    makeFont(11.5, 600),
    Math.max(60, maxWidth - headerReserved),
    measure
  );

  const singleHeaderOnly =
    total === 1 &&
    Boolean(first?.title) &&
    first.title === headlineRaw &&
    !first.subtitle &&
    !first.body &&
    !first.kvPairs?.length &&
    !first.refLabel;

  const rows = singleHeaderOnly
    ? []
    : visible.map((item, idx) => {
        const trimmedItem: ShareContextItem =
          total === 1 &&
          item.title &&
          item.title === headlineRaw &&
          (item.subtitle || item.body || item.kvPairs?.length || item.refLabel)
            ? { ...item, title: undefined, badge: undefined }
            : item;
        return buildStructuredRow(trimmedItem, idx, total, maxWidth, palette, measure, buildNested);
      });

  const headerH = 30;
  const rowsH = rows.reduce((sum, r) => sum + r.height, 0);
  const extraH = extraItemsCount > 0 ? 24 : 0;

  return {
    variant: "structured",
    height: headerH + rowsH + extraH,
    kindIcon,
    sourcePill,
    headlineText,
    headerBadge,
    rows,
    extraItemsCount,
  };
}
