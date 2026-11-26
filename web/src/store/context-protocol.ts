// web/src/store/context-protocol.ts
// Standard structured context-attachment protocol (hoxi-context v1) separating
// human-readable display metadata, on-demand entity references, and LLM payloads.
import {
  extractCollabPromptTitleFallback,
  formatCollabPromptForClipboard,
  formatCollabPromptForExport,
} from "./collab-prompt";

export type ContextItemKind = "quote" | "entity" | "record" | "snapshot";

export interface ContextItemResolveSpec {
  servicePath?: string;
  mcpTool?: string;
  mcpArgs?: Record<string, unknown>;
}

export interface ContextItemRef {
  id: string;
  uri?: string;
  resolve?: ContextItemResolveSpec;
}

export interface ContextItem {
  kind: ContextItemKind;
  title?: string;
  subtitle?: string;
  badge?: string;
  entityType?: string;
  ref?: ContextItemRef;
  digest?: string | Record<string, unknown>;
  content?: string;
}

export interface ContextSource {
  appId?: string;
  appName?: string;
  tabId?: string;
  tabTitle?: string;
}

export interface ContextEnvelope {
  version: 1;
  source?: ContextSource;
  items: ContextItem[];
}

export interface ComposerContextAttachment {
  id: string;
  source?: ContextSource;
  items: ContextItem[];
}

export interface ParsedContextPrompt {
  envelopes: ContextEnvelope[];
  bodyText: string;
  hasContext: boolean;
}

export interface NormalizeContextOptions {
  maxItemChars?: number;
  maxTotalChars?: number;
}

export const DEFAULT_CONTEXT_MAX_ITEM_CHARS = 4000;
export const DEFAULT_CONTEXT_MAX_TOTAL_CHARS = 16000;
export const MAX_CONTEXT_ITEMS_PER_ENVELOPE = 50;

const VALID_KINDS = new Set<ContextItemKind>(["quote", "entity", "record", "snapshot"]);
const CONTEXT_OPEN_TAG = "<hoxi-context";
const CONTEXT_CLOSE_TAG = "</hoxi-context>";
const ITEM_OPEN_TAG = "<item";
const ITEM_CLOSE_TAG = "</item>";
const CDATA_START = "<![CDATA[";
const CDATA_END = "]]>";

function isPlainRecord(value: unknown): value is Record<string, unknown> {
  return Boolean(value) && typeof value === "object" && !Array.isArray(value);
}

function cleanStr(value: unknown, maxLen = 512): string | undefined {
  if (typeof value !== "string") return undefined;
  const trimmed = value.trim();
  if (!trimmed) return undefined;
  return trimmed.length > maxLen ? trimmed.slice(0, maxLen) : trimmed;
}

function escapeXmlAttr(value: string): string {
  return value
    .replace(/&/g, "&amp;")
    .replace(/"/g, "&quot;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;")
    .replace(/\r/g, "&#13;")
    .replace(/\n/g, "&#10;")
    .replace(/\t/g, "&#9;");
}

function unescapeXmlAttr(value: string): string {
  return value.replace(/&(quot|lt|gt|amp|#10|#13|#9);/g, (_, ent: string) => {
    switch (ent) {
      case "quot":
        return '"';
      case "lt":
        return "<";
      case "gt":
        return ">";
      case "amp":
        return "&";
      case "#10":
        return "\n";
      case "#13":
        return "\r";
      case "#9":
        return "\t";
      default:
        return `&${ent};`;
    }
  });
}

function tryParseJsonStructure(raw: string): Record<string, unknown> | unknown[] | null {
  const trimmed = raw.trim();
  if (!trimmed) return null;
  const first = trimmed[0];
  const last = trimmed[trimmed.length - 1];
  if (!((first === "{" && last === "}") || (first === "[" && last === "]"))) return null;
  try {
    const parsed: unknown = JSON.parse(trimmed);
    if (parsed && typeof parsed === "object") {
      return parsed as Record<string, unknown> | unknown[];
    }
  } catch {
    return null;
  }
  return null;
}

function inferContextKind(
  rawKind: unknown,
  hasRefOrEntity: boolean,
  hasTitle: boolean,
  bodyText: string,
  hasObjectDigest: boolean,
  totalItemsHint: number
): ContextItemKind {
  if (typeof rawKind === "string" && VALID_KINDS.has(rawKind as ContextItemKind)) {
    return rawKind as ContextItemKind;
  }
  if (hasRefOrEntity) return "entity";
  const parsedJson = hasObjectDigest ? null : tryParseJsonStructure(bodyText);
  if (hasObjectDigest || parsedJson !== null) {
    const keyCount = parsedJson && !Array.isArray(parsedJson) ? Object.keys(parsedJson).length : 0;
    if (totalItemsHint <= 1 && (bodyText.length >= 360 || keyCount >= 4)) {
      return "snapshot";
    }
    return "record";
  }
  return hasTitle ? "record" : "quote";
}

function normalizeResolveSpec(raw: unknown): ContextItemResolveSpec | undefined {
  if (typeof raw === "string") {
    const trimmed = raw.trim();
    if (!trimmed) return undefined;
    if (trimmed.startsWith("service:")) {
      const p = cleanStr(trimmed.slice("service:".length), 512);
      return p ? { servicePath: p } : undefined;
    }
    if (trimmed.startsWith("mcp:")) {
      const tool = cleanStr(trimmed.slice("mcp:".length), 160);
      return tool ? { mcpTool: tool } : undefined;
    }
    return undefined;
  }
  if (!isPlainRecord(raw)) return undefined;
  const servicePath = cleanStr(raw.servicePath, 512);
  const mcpTool = cleanStr(raw.mcpTool, 160);
  const mcpArgs = isPlainRecord(raw.mcpArgs) ? raw.mcpArgs : undefined;
  if (!servicePath && !mcpTool) return undefined;
  return {
    ...(servicePath ? { servicePath } : {}),
    ...(mcpTool ? { mcpTool } : {}),
    ...(mcpTool && mcpArgs ? { mcpArgs } : {}),
  };
}

function normalizeRef(raw: unknown): ContextItemRef | undefined {
  if (!isPlainRecord(raw)) return undefined;
  const id = cleanStr(raw.id, 256);
  const uri = cleanStr(raw.uri, 512);
  const resolve = normalizeResolveSpec(raw.resolve);
  if (!id && !uri && !resolve) return undefined;
  return {
    id: id || uri || "",
    ...(uri ? { uri } : {}),
    ...(resolve ? { resolve } : {}),
  };
}

export function normalizeContextSource(raw: unknown): ContextSource | undefined {
  if (!isPlainRecord(raw)) return undefined;
  const appId = cleanStr(raw.appId, 128);
  const appName = cleanStr(raw.appName, 128);
  const tabId = cleanStr(raw.tabId, 128);
  const tabTitle = cleanStr(raw.tabTitle, 128);
  if (!appId && !appName && !tabId && !tabTitle) return undefined;
  return {
    ...(appId ? { appId } : {}),
    ...(appName ? { appName } : {}),
    ...(tabId ? { tabId } : {}),
    ...(tabTitle ? { tabTitle } : {}),
  };
}

export function normalizeContextItem(
  raw: unknown,
  maxItemChars = DEFAULT_CONTEXT_MAX_ITEM_CHARS,
  totalItemsHint = 1
): ContextItem | null {
  const limit = Math.max(64, Math.trunc(maxItemChars));
  if (typeof raw === "string") {
    const content = raw.trim();
    if (!content) return null;
    return {
      kind: inferContextKind(undefined, false, false, content, false, totalItemsHint),
      content: content.length > limit ? content.slice(0, limit) : content,
    };
  }
  if (!isPlainRecord(raw)) return null;
  const title = cleanStr(raw.title, 240);
  const subtitle = cleanStr(raw.subtitle, 320);
  const badge = cleanStr(raw.badge, 64);
  const entityType = cleanStr(raw.entityType, 64);
  const ref = normalizeRef(raw.ref);

  let digest: string | Record<string, unknown> | undefined;
  let digestText = "";
  if (isPlainRecord(raw.digest)) {
    const serialized = JSON.stringify(raw.digest);
    if (serialized.length <= limit) {
      digest = raw.digest;
      digestText = serialized;
    } else {
      digest = serialized.slice(0, limit);
      digestText = digest;
    }
  } else if (typeof raw.digest === "string") {
    const trimmed = raw.digest.trim();
    if (trimmed) {
      digest = trimmed.length > limit ? trimmed.slice(0, limit) : trimmed;
      digestText = digest;
    }
  }

  let content: string | undefined;
  if (typeof raw.content === "string") {
    const trimmed = raw.content.trim();
    if (trimmed) {
      content = trimmed.length > limit ? trimmed.slice(0, limit) : trimmed;
    }
  } else if (isPlainRecord(raw.content) || Array.isArray(raw.content)) {
    const serialized = JSON.stringify(raw.content);
    if (serialized) {
      content = serialized.length > limit ? serialized.slice(0, limit) : serialized;
    }
  }

  if (!title && !subtitle && !content && !digestText && !ref?.id && !ref?.uri) {
    return null;
  }

  const kind = inferContextKind(
    raw.kind,
    Boolean(ref?.id || entityType),
    Boolean(title),
    content || digestText,
    isPlainRecord(digest),
    totalItemsHint
  );

  return {
    kind,
    ...(title ? { title } : {}),
    ...(subtitle ? { subtitle } : {}),
    ...(badge ? { badge } : {}),
    ...(entityType ? { entityType } : {}),
    ...(ref ? { ref } : {}),
    ...(digest !== undefined ? { digest } : {}),
    ...(content ? { content } : {}),
  };
}

export function normalizeContextItems(
  rawItems: unknown,
  options?: NormalizeContextOptions
): ContextItem[] {
  if (!Array.isArray(rawItems)) return [];
  const maxItem = Math.max(64, Math.trunc(options?.maxItemChars ?? DEFAULT_CONTEXT_MAX_ITEM_CHARS));
  const maxTotal = Math.max(maxItem, Math.trunc(options?.maxTotalChars ?? DEFAULT_CONTEXT_MAX_TOTAL_CHARS));
  const hintCount = rawItems.length;
  const out: ContextItem[] = [];
  let usedChars = 0;

  for (const raw of rawItems) {
    if (out.length >= MAX_CONTEXT_ITEMS_PER_ENVELOPE) break;
    const item = normalizeContextItem(raw, maxItem, hintCount);
    if (!item) continue;
    const body = getContextItemBodyText(item);
    const remaining = maxTotal - usedChars;
    if (remaining <= 0 && body.length > 0 && out.length > 0) break;
    if (body.length > remaining && remaining > 0) {
      const clipped = body.slice(0, remaining);
      if (item.content !== undefined) {
        item.content = clipped;
      } else if (item.digest !== undefined) {
        item.digest = clipped;
      }
    }
    usedChars += getContextItemBodyText(item).length + (item.title?.length ?? 0) + (item.subtitle?.length ?? 0);
    out.push(item);
  }

  return out;
}

export function getContextItemBodyText(item: ContextItem): string {
  if (typeof item.content === "string" && item.content.trim()) return item.content;
  if (typeof item.digest === "string" && item.digest.trim()) return item.digest;
  if (isPlainRecord(item.digest)) {
    try {
      return JSON.stringify(item.digest);
    } catch {
      return "";
    }
  }
  return "";
}

function encodeItemBody(body: string): string {
  if (!body) return "";
  const needsCdata =
    body.includes(ITEM_CLOSE_TAG) ||
    body.includes(CONTEXT_CLOSE_TAG) ||
    body.includes(CONTEXT_OPEN_TAG) ||
    body.includes(ITEM_OPEN_TAG) ||
    body.includes(CDATA_START);
  if (!needsCdata) return body;
  return `${CDATA_START}${body.replace(/\]\]>/g, "]]]]><![CDATA[>")}${CDATA_END}`;
}

function serializeAttr(name: string, value?: string): string {
  if (!value) return "";
  return ` ${name}="${escapeXmlAttr(value)}"`;
}

export function serializeContextEnvelope(envelope: ContextEnvelope): string {
  if (!envelope.items.length) return "";
  const src = envelope.source;
  const openAttrs =
    ` v="1"` +
    serializeAttr("source", src?.appId) +
    serializeAttr("source-name", src?.appName) +
    serializeAttr("tab", src?.tabId) +
    serializeAttr("tab-title", src?.tabTitle);

  const lines: string[] = [`${CONTEXT_OPEN_TAG}${openAttrs}>`];
  for (const item of envelope.items) {
    const resolve = item.ref?.resolve;
    let resolveArgsAttr = "";
    if (resolve?.mcpTool && resolve.mcpArgs && Object.keys(resolve.mcpArgs).length > 0) {
      try {
        resolveArgsAttr = serializeAttr("resolve-args", JSON.stringify(resolve.mcpArgs));
      } catch {
        resolveArgsAttr = "";
      }
    }
    const attrs =
      serializeAttr("kind", item.kind) +
      serializeAttr("type", item.entityType) +
      serializeAttr("id", item.ref?.id) +
      serializeAttr("uri", item.ref?.uri) +
      serializeAttr("title", item.title) +
      serializeAttr("subtitle", item.subtitle) +
      serializeAttr("badge", item.badge) +
      serializeAttr("resolve-service", resolve?.servicePath) +
      serializeAttr("resolve-tool", resolve?.mcpTool) +
      resolveArgsAttr;
    const body = getContextItemBodyText(item);
    if (!body) {
      lines.push(`  ${ITEM_OPEN_TAG}${attrs} />`);
    } else {
      lines.push(`  ${ITEM_OPEN_TAG}${attrs}>\n${encodeItemBody(body)}\n  ${ITEM_CLOSE_TAG}`);
    }
  }
  lines.push(CONTEXT_CLOSE_TAG);
  return lines.join("\n");
}

export function serializeContextEnvelopes(
  envelopes: ContextEnvelope[],
  instruction?: string
): string {
  const blocks = envelopes.map(serializeContextEnvelope).filter(Boolean);
  const cleanInst = (instruction ?? "").trim();
  if (blocks.length === 0) return cleanInst;
  const joined = blocks.join("\n\n");
  return cleanInst ? `${joined}\n\n${cleanInst}` : joined;
}

function parseXmlAttrs(rawAttrs: string): Record<string, string> {
  const attrs: Record<string, string> = {};
  let i = 0;
  const n = rawAttrs.length;
  while (i < n) {
    while (i < n && /\s/.test(rawAttrs[i])) i++;
    if (i >= n) break;
    const keyStart = i;
    while (i < n && /[A-Za-z0-9_:-]/.test(rawAttrs[i])) i++;
    const key = rawAttrs.slice(keyStart, i);
    while (i < n && /\s/.test(rawAttrs[i])) i++;
    if (!key || i >= n || rawAttrs[i] !== "=") {
      while (i < n && !/\s/.test(rawAttrs[i])) i++;
      continue;
    }
    i++; // skip '='
    while (i < n && /\s/.test(rawAttrs[i])) i++;
    if (i >= n) break;
    const quote = rawAttrs[i];
    if (quote !== '"' && quote !== "'") {
      const valStart = i;
      while (i < n && !/\s/.test(rawAttrs[i])) i++;
      attrs[key] = unescapeXmlAttr(rawAttrs.slice(valStart, i));
      continue;
    }
    i++; // skip opening quote
    const valStart = i;
    while (i < n && rawAttrs[i] !== quote) i++;
    attrs[key] = unescapeXmlAttr(rawAttrs.slice(valStart, i));
    if (i < n && rawAttrs[i] === quote) i++;
  }
  return attrs;
}

function findTagEnd(src: string, startIndex: number): number {
  let quote: string | null = null;
  for (let i = startIndex; i < src.length; i++) {
    const ch = src[i];
    if (quote) {
      if (ch === quote) quote = null;
    } else if (ch === '"' || ch === "'") {
      quote = ch;
    } else if (ch === ">") {
      return i;
    }
  }
  return -1;
}

function decodeCdataSegments(rawBody: string): string {
  let trimmed = rawBody;
  if (trimmed.startsWith("\n")) trimmed = trimmed.slice(1);
  if (trimmed.endsWith("\n  ")) trimmed = trimmed.slice(0, -3);
  else if (trimmed.endsWith("\n")) trimmed = trimmed.slice(0, -1);

  if (!trimmed.startsWith(CDATA_START)) return trimmed;
  let out = "";
  let pos = 0;
  while (pos < trimmed.length) {
    if (!trimmed.startsWith(CDATA_START, pos)) {
      return trimmed;
    }
    const endIdx = trimmed.indexOf(CDATA_END, pos + CDATA_START.length);
    if (endIdx < 0) return trimmed;
    out += trimmed.slice(pos + CDATA_START.length, endIdx);
    pos = endIdx + CDATA_END.length;
  }
  return out;
}

function findItemCloseOutsideCdata(src: string, fromIndex: number): number {
  let pos = fromIndex;
  while (pos < src.length) {
    const nextClose = src.indexOf(ITEM_CLOSE_TAG, pos);
    if (nextClose < 0) return -1;
    const nextCdata = src.indexOf(CDATA_START, pos);
    if (nextCdata >= 0 && nextCdata < nextClose) {
      const cdataEnd = src.indexOf(CDATA_END, nextCdata + CDATA_START.length);
      if (cdataEnd < 0) return -1;
      pos = cdataEnd + CDATA_END.length;
      continue;
    }
    return nextClose;
  }
  return -1;
}

function findContextCloseOutsideCdata(src: string, fromIndex: number): number {
  let pos = fromIndex;
  while (pos < src.length) {
    const nextClose = src.indexOf(CONTEXT_CLOSE_TAG, pos);
    if (nextClose < 0) return -1;
    const nextCdata = src.indexOf(CDATA_START, pos);
    if (nextCdata >= 0 && nextCdata < nextClose) {
      const cdataEnd = src.indexOf(CDATA_END, nextCdata + CDATA_START.length);
      if (cdataEnd < 0) return -1;
      pos = cdataEnd + CDATA_END.length;
      continue;
    }
    return nextClose;
  }
  return -1;
}

function parseEnvelopeInnerItems(inner: string): ContextItem[] {
  const items: ContextItem[] = [];
  let pos = 0;
  while (pos < inner.length) {
    const itemStart = inner.indexOf(ITEM_OPEN_TAG, pos);
    if (itemStart < 0) break;
    const afterTagName = itemStart + ITEM_OPEN_TAG.length;
    const nextChar = inner[afterTagName];
    if (nextChar && !/[\s/>]/.test(nextChar)) {
      pos = afterTagName;
      continue;
    }
    const tagEnd = findTagEnd(inner, afterTagName);
    if (tagEnd < 0) break;
    const selfClosing = inner[tagEnd - 1] === "/";
    const attrSlice = inner.slice(afterTagName, selfClosing ? tagEnd - 1 : tagEnd);
    const attrs = parseXmlAttrs(attrSlice);

    let body = "";
    if (selfClosing) {
      pos = tagEnd + 1;
    } else {
      const closeIdx = findItemCloseOutsideCdata(inner, tagEnd + 1);
      if (closeIdx < 0) break;
      body = decodeCdataSegments(inner.slice(tagEnd + 1, closeIdx));
      pos = closeIdx + ITEM_CLOSE_TAG.length;
    }

    let resolveArgs: Record<string, unknown> | undefined;
    if (attrs["resolve-args"]) {
      const parsed = tryParseJsonStructure(attrs["resolve-args"]);
      if (parsed && !Array.isArray(parsed)) resolveArgs = parsed;
    }
    const resolve: ContextItemResolveSpec | undefined =
      attrs["resolve-service"] || attrs["resolve-tool"]
        ? {
            ...(attrs["resolve-service"] ? { servicePath: attrs["resolve-service"] } : {}),
            ...(attrs["resolve-tool"] ? { mcpTool: attrs["resolve-tool"] } : {}),
            ...(attrs["resolve-tool"] && resolveArgs ? { mcpArgs: resolveArgs } : {}),
          }
        : undefined;
    const ref: ContextItemRef | undefined =
      attrs.id || attrs.uri || resolve
        ? {
            id: attrs.id || attrs.uri || "",
            ...(attrs.uri ? { uri: attrs.uri } : {}),
            ...(resolve ? { resolve } : {}),
          }
        : undefined;

    const kind = inferContextKind(
      attrs.kind,
      Boolean(ref?.id || attrs.type),
      Boolean(attrs.title),
      body,
      false,
      2
    );
    const item: ContextItem = {
      kind,
      ...(attrs.title ? { title: attrs.title } : {}),
      ...(attrs.subtitle ? { subtitle: attrs.subtitle } : {}),
      ...(attrs.badge ? { badge: attrs.badge } : {}),
      ...(attrs.type ? { entityType: attrs.type } : {}),
      ...(ref ? { ref } : {}),
      ...(body ? { content: body } : {}),
    };
    if (item.title || item.subtitle || item.content || item.ref?.id || item.ref?.uri) {
      items.push(item);
    }
  }
  return items;
}

export function parseHoxiContextPrompt(raw: string): ParsedContextPrompt {
  if (!raw || !raw.includes(CONTEXT_OPEN_TAG)) {
    return { envelopes: [], bodyText: raw || "", hasContext: false };
  }
  const envelopes: ContextEnvelope[] = [];
  const textSegments: string[] = [];
  let cursor = 0;

  while (cursor < raw.length) {
    const startIdx = raw.indexOf(CONTEXT_OPEN_TAG, cursor);
    if (startIdx < 0) {
      textSegments.push(raw.slice(cursor));
      break;
    }
    const afterName = startIdx + CONTEXT_OPEN_TAG.length;
    const nextCh = raw[afterName];
    if (nextCh && !/[\s>]/.test(nextCh)) {
      textSegments.push(raw.slice(cursor, afterName));
      cursor = afterName;
      continue;
    }
    const openEnd = findTagEnd(raw, afterName);
    if (openEnd < 0) {
      textSegments.push(raw.slice(cursor));
      break;
    }
    const closeIdx = findContextCloseOutsideCdata(raw, openEnd + 1);
    if (closeIdx < 0) {
      textSegments.push(raw.slice(cursor));
      break;
    }
    const attrs = parseXmlAttrs(raw.slice(afterName, openEnd));
    const inner = raw.slice(openEnd + 1, closeIdx);
    const items = parseEnvelopeInnerItems(inner);
    if (items.length === 0) {
      // Malformed/empty block: preserve verbatim so user text is never lost.
      textSegments.push(raw.slice(cursor, closeIdx + CONTEXT_CLOSE_TAG.length));
      cursor = closeIdx + CONTEXT_CLOSE_TAG.length;
      continue;
    }
    if (startIdx > cursor) {
      textSegments.push(raw.slice(cursor, startIdx));
    }
    const source = normalizeContextSource({
      appId: attrs.source,
      appName: attrs["source-name"],
      tabId: attrs.tab,
      tabTitle: attrs["tab-title"],
    });
    envelopes.push({
      version: 1,
      ...(source ? { source } : {}),
      items,
    });
    cursor = closeIdx + CONTEXT_CLOSE_TAG.length;
  }

  if (envelopes.length === 0) {
    return { envelopes: [], bodyText: raw, hasContext: false };
  }
  const bodyText = textSegments
    .map((s) => s.trim())
    .filter(Boolean)
    .join("\n\n");
  return { envelopes, bodyText, hasContext: true };
}

export function extractContextTitleFallback(rawPrompt: string): string {
  const collabTitle = extractCollabPromptTitleFallback(rawPrompt);
  if (collabTitle) return collabTitle;
  const parsed = parseHoxiContextPrompt(rawPrompt);
  if (!parsed.hasContext) return rawPrompt;
  if (parsed.bodyText.trim()) return parsed.bodyText;
  const first = parsed.envelopes[0]?.items[0];
  if (!first) return rawPrompt;
  return first.title || first.subtitle || getContextItemBodyText(first) || rawPrompt;
}

export function estimateContextEnvelopeChars(envelope: Pick<ContextEnvelope, "items">): number {
  let total = 0;
  for (const item of envelope.items) {
    total +=
      (item.title?.length ?? 0) +
      (item.subtitle?.length ?? 0) +
      getContextItemBodyText(item).length;
  }
  return total;
}

export function formatContextSizeLabel(chars: number): string {
  if (chars < 1000) return `${chars} B`;
  return `${(chars / 1024).toFixed(1)} KB`;
}

export function deriveContextItemPreview(item: ContextItem, maxLen = 84): string {
  if (item.subtitle) return item.subtitle;
  const body = getContextItemBodyText(item).replace(/\s+/g, " ").trim();
  if (!body) return item.ref?.uri || item.ref?.id || "";
  return body.length > maxLen ? `${body.slice(0, maxLen)}…` : body;
}

export function prettyFormatContextBody(raw: string): { text: string; isJson: boolean } {
  const parsed = tryParseJsonStructure(raw);
  if (parsed !== null) {
    try {
      return { text: JSON.stringify(parsed, null, 2), isJson: true };
    } catch {
      return { text: raw, isJson: false };
    }
  }
  return { text: raw, isJson: false };
}

export function formatContextPromptForExport(raw: string): string {
  const collabExport = formatCollabPromptForExport(raw);
  if (collabExport !== null) return collabExport;
  const parsed = parseHoxiContextPrompt(raw);
  if (!parsed.hasContext) return raw;
  const sections: string[] = [];
  for (const env of parsed.envelopes) {
    const srcLabel = env.source?.appName || env.source?.appId || "Context";
    const summary = `${srcLabel} (${env.items.length})`;
    const itemLines = env.items.map((item, idx) => {
      const head = [
        env.items.length > 1 ? `${idx + 1}.` : "-",
        item.title ? `**${item.title}**` : "",
        item.badge ? `\`${item.badge}\`` : "",
        item.subtitle ? `— ${item.subtitle}` : "",
      ]
        .filter(Boolean)
        .join(" ");
      const body = getContextItemBodyText(item);
      if (!body) return head;
      const formatted = prettyFormatContextBody(body);
      const fence = formatted.isJson ? "json" : "";
      const safeBody = formatted.text.replace(/```/g, "`\u200b``");
      return `${head}\n\n\`\`\`${fence}\n${safeBody}\n\`\`\``;
    });
    sections.push(`<details>\n<summary>${summary}</summary>\n\n${itemLines.join("\n\n")}\n\n</details>`);
  }
  if (parsed.bodyText) sections.push(parsed.bodyText);
  return sections.join("\n\n");
}

export function formatContextPromptForClipboard(raw: string): string {
  const collabClip = formatCollabPromptForClipboard(raw);
  if (collabClip !== null) return collabClip;
  const parsed = parseHoxiContextPrompt(raw);
  if (!parsed.hasContext) return raw;
  const sections: string[] = [];
  for (const env of parsed.envelopes) {
    const src = env.source?.appName || env.source?.appId;
    const itemLines = env.items.map((item) => {
      const head = [src ? `[${src}]` : "", item.title, item.badge ? `(${item.badge})` : "", item.subtitle]
        .filter(Boolean)
        .join(" ");
      const body = getContextItemBodyText(item);
      if (!body) return head;
      return head ? `${head}\n${body}` : body;
    });
    if (itemLines.length > 0) sections.push(itemLines.join("\n\n"));
  }
  if (parsed.bodyText) sections.push(parsed.bodyText);
  return sections.join("\n\n");
}
