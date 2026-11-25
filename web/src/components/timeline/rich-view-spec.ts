// web/src/components/timeline/rich-view-spec.ts
// Pure parser, sanitizer, hoxi-context v1 reverse-handoff builders, Markdown
// multi-image gallery detector, and export fallbacks for hoxi.ui/v1 rich views.
import type { ToolPart } from "../../services/sse-types";
import {
  resolveRegisteredToolRichView,
  type ContextEnvelope,
  type ContextItem,
  type ContextItemKind,
  type ContextItemRef,
  type ContextSource,
  type RichEntityItem,
  type RichEntityMetric,
  type RichGalleryImage,
  type RichLinkAction,
  type RichMetricItem,
  type RichQrReceipt,
  type RichTone,
  type StructuredRichViewSpec,
} from "../../store/workspace";
import { parseInline } from "./markdown-parser";

const MAX_RICH_METRICS = 12;
const MAX_RICH_ENTITIES = 24;
const MAX_RICH_ENTITY_METRICS = 6;
const MAX_RICH_ENTITY_TAGS = 6;
const MAX_RICH_GALLERY = 24;
const MAX_RICH_LINKS = 6;
const CONTROL_CHAR_RE = /[\u0000-\u001f\u007f]/;
const VALID_TONES = new Set<RichTone>(["neutral", "accent", "ok", "warn", "danger"]);
const VALID_CONTEXT_KINDS = new Set<ContextItemKind>(["quote", "entity", "record", "snapshot"]);

function isRecord(v: unknown): v is Record<string, unknown> {
  return Boolean(v) && typeof v === "object" && !Array.isArray(v);
}

function cleanText(v: unknown, maxLen = 200): string | undefined {
  if (typeof v === "number" && Number.isFinite(v)) return String(v);
  if (typeof v !== "string") return undefined;
  const trimmed = v.replace(/\s+/g, " ").trim();
  if (!trimmed) return undefined;
  return trimmed.length > maxLen ? `${trimmed.slice(0, maxLen - 1)}…` : trimmed;
}

export function normalizeRichTone(raw: unknown): RichTone | undefined {
  if (typeof raw !== "string") return undefined;
  const lower = raw.trim().toLowerCase();
  if (lower === "success" || lower === "positive" || lower === "green") return "ok";
  if (lower === "warning" || lower === "amber" || lower === "yellow") return "warn";
  if (lower === "error" || lower === "negative" || lower === "red" || lower === "critical") return "danger";
  if (lower === "primary" || lower === "info" || lower === "blue") return "accent";
  return VALID_TONES.has(lower as RichTone) ? (lower as RichTone) : undefined;
}

/**
 * Allows only safe http(s) navigation URLs; rejects javascript:/data:/file: and control chars.
 */
export function sanitizeRichLinkURL(raw: unknown): string | undefined {
  if (typeof raw !== "string") return undefined;
  const trimmed = raw.trim();
  if (!trimmed || trimmed.length > 2048 || CONTROL_CHAR_RE.test(trimmed)) return undefined;
  if (!/^https?:\/\/[^\s<>"]+$/i.test(trimmed)) return undefined;
  return trimmed;
}

/**
 * Allows safe image URLs: http(s), gateway attachment/file routes, raster/svg data URLs,
 * or clean workspace-relative paths without traversal or unsafe schemes.
 */
export function sanitizeRichMediaURL(raw: unknown): string | undefined {
  if (typeof raw !== "string") return undefined;
  const trimmed = raw.trim();
  if (!trimmed || trimmed.length > 512_000 || CONTROL_CHAR_RE.test(trimmed)) return undefined;
  if (/^https?:\/\/[^\s<>"]+$/i.test(trimmed)) return trimmed;
  if (/^\/api\/hoxi\/(?:attachments|file\/content|[a-z0-9-]+\/portal\/attachments)/i.test(trimmed)) {
    return trimmed;
  }
  if (/^data:image\/(?:png|jpe?g|gif|webp|avif|svg\+xml)(?:;charset=[^;,]+)?;(?:base64|utf8),/i.test(trimmed)) {
    return trimmed;
  }
  if (/^data:image\/svg\+xml,/i.test(trimmed)) return trimmed;
  if (/^[a-z][a-z0-9+.-]*:/i.test(trimmed)) return undefined;
  if (trimmed.startsWith("//") || trimmed.includes("..")) return undefined;
  return trimmed;
}

function normalizeSource(raw: unknown): ContextSource | undefined {
  if (!isRecord(raw)) return undefined;
  const appId = cleanText(raw.appId, 64);
  const appName = cleanText(raw.appName, 80);
  const tabId = cleanText(raw.tabId, 64);
  const tabTitle = cleanText(raw.tabTitle, 80);
  if (!appId && !appName && !tabId && !tabTitle) return undefined;
  return {
    ...(appId ? { appId } : {}),
    ...(appName ? { appName } : {}),
    ...(tabId ? { tabId } : {}),
    ...(tabTitle ? { tabTitle } : {}),
  };
}

function normalizeRef(raw: unknown, fallbackId?: string): ContextItemRef | undefined {
  if (isRecord(raw)) {
    const id = cleanText(raw.id, 160) || fallbackId;
    if (!id) return undefined;
    const uri = cleanText(raw.uri, 512);
    let resolve: ContextItemRef["resolve"];
    if (isRecord(raw.resolve)) {
      const mcpTool = cleanText(raw.resolve.mcpTool, 160);
      const servicePath = cleanText(raw.resolve.servicePath, 512);
      const mcpArgs = isRecord(raw.resolve.mcpArgs) ? raw.resolve.mcpArgs : undefined;
      if (mcpTool || servicePath) {
        resolve = {
          ...(mcpTool ? { mcpTool } : {}),
          ...(servicePath ? { servicePath } : {}),
          ...(mcpTool && mcpArgs ? { mcpArgs } : {}),
        };
      }
    }
    return { id, ...(uri ? { uri } : {}), ...(resolve ? { resolve } : {}) };
  }
  return fallbackId ? { id: fallbackId } : undefined;
}

function normalizeMetricList(raw: unknown): RichMetricItem[] {
  if (!Array.isArray(raw)) return [];
  const out: RichMetricItem[] = [];
  for (const entry of raw.slice(0, MAX_RICH_METRICS)) {
    if (!isRecord(entry)) continue;
    const label = cleanText(entry.label ?? entry.name ?? entry.title, 80);
    const value = cleanText(entry.value ?? entry.displayValue, 80);
    if (!label || !value) continue;
    const delta = cleanText(entry.delta ?? entry.change, 48);
    const hint = cleanText(entry.hint ?? entry.subtitle ?? entry.note, 120);
    const tone = normalizeRichTone(entry.tone ?? entry.status);
    out.push({
      label,
      value,
      ...(delta ? { delta } : {}),
      ...(hint ? { hint } : {}),
      ...(tone ? { tone } : {}),
    });
  }
  return out;
}

function normalizeEntityMetrics(raw: unknown): RichEntityMetric[] {
  if (!Array.isArray(raw)) return [];
  const out: RichEntityMetric[] = [];
  for (const entry of raw.slice(0, MAX_RICH_ENTITY_METRICS)) {
    if (!isRecord(entry)) continue;
    const label = cleanText(entry.label ?? entry.name ?? entry.key, 48);
    const value = cleanText(entry.value, 64);
    if (!label || !value) continue;
    const tone = normalizeRichTone(entry.tone ?? entry.status);
    out.push({ label, value, ...(tone ? { tone } : {}) });
  }
  return out;
}

function normalizeEntityList(raw: unknown): RichEntityItem[] {
  if (!Array.isArray(raw)) return [];
  const out: RichEntityItem[] = [];
  for (const entry of raw.slice(0, MAX_RICH_ENTITIES)) {
    if (!isRecord(entry)) continue;
    const title = cleanText(entry.title ?? entry.name ?? entry.label, 160);
    if (!title) continue;
    const id = cleanText(entry.id ?? entry.key, 128);
    const subtitle = cleanText(entry.subtitle ?? entry.summary ?? entry.description, 200);
    const badge = cleanText(entry.badge ?? entry.status, 48);
    const badgeTone = normalizeRichTone(entry.badgeTone ?? entry.tone);
    const thumbnailUrl = sanitizeRichMediaURL(
      entry.thumbnailUrl ?? entry.imageUrl ?? entry.image ?? entry.thumb
    );
    const entityType = cleanText(entry.entityType ?? entry.type, 48);
    const rawKind = typeof entry.kind === "string" ? entry.kind.trim().toLowerCase() : "";
    const kind = VALID_CONTEXT_KINDS.has(rawKind as ContextItemKind)
      ? (rawKind as ContextItemKind)
      : undefined;
    const metrics = normalizeEntityMetrics(entry.metrics ?? entry.fields);
    const tags = Array.isArray(entry.tags)
      ? entry.tags
          .map((t) => cleanText(t, 36))
          .filter((t): t is string => Boolean(t))
          .slice(0, MAX_RICH_ENTITY_TAGS)
      : [];
    const url = sanitizeRichLinkURL(entry.url ?? entry.href);
    const urlLabel = cleanText(entry.urlLabel, 48);
    const ref = normalizeRef(entry.ref, id);
    const digest =
      typeof entry.digest === "string"
        ? cleanText(entry.digest, 600)
        : isRecord(entry.digest)
          ? entry.digest
          : undefined;
    out.push({
      ...(id ? { id } : {}),
      title,
      ...(subtitle ? { subtitle } : {}),
      ...(badge ? { badge } : {}),
      ...(badgeTone ? { badgeTone } : {}),
      ...(thumbnailUrl ? { thumbnailUrl } : {}),
      ...(entityType ? { entityType } : {}),
      ...(kind ? { kind } : {}),
      ...(metrics.length > 0 ? { metrics } : {}),
      ...(tags.length > 0 ? { tags } : {}),
      ...(url ? { url } : {}),
      ...(urlLabel ? { urlLabel } : {}),
      ...(ref ? { ref } : {}),
      ...(digest !== undefined ? { digest } : {}),
    });
  }
  return out;
}

function normalizeGalleryList(raw: unknown): RichGalleryImage[] {
  if (!Array.isArray(raw)) return [];
  const out: RichGalleryImage[] = [];
  for (const entry of raw.slice(0, MAX_RICH_GALLERY)) {
    if (typeof entry === "string") {
      const src = sanitizeRichMediaURL(entry);
      if (src) out.push({ src });
      continue;
    }
    if (!isRecord(entry)) continue;
    const src = sanitizeRichMediaURL(entry.src ?? entry.url ?? entry.imageUrl);
    if (!src) continue;
    const id = cleanText(entry.id, 96);
    const alt = cleanText(entry.alt ?? entry.title, 140);
    const caption = cleanText(entry.caption ?? entry.subtitle, 160);
    const badge = cleanText(entry.badge, 40);
    const badgeTone = normalizeRichTone(entry.badgeTone ?? entry.tone);
    const filename = cleanText(entry.filename, 140);
    const downloadUrl = sanitizeRichMediaURL(entry.downloadUrl);
    out.push({
      ...(id ? { id } : {}),
      src,
      ...(alt ? { alt } : {}),
      ...(caption ? { caption } : {}),
      ...(badge ? { badge } : {}),
      ...(badgeTone ? { badgeTone } : {}),
      ...(filename ? { filename } : {}),
      ...(downloadUrl ? { downloadUrl } : {}),
    });
  }
  return out;
}

function normalizeLinks(raw: unknown): RichLinkAction[] {
  if (!Array.isArray(raw)) return [];
  const out: RichLinkAction[] = [];
  for (const entry of raw.slice(0, MAX_RICH_LINKS)) {
    if (!isRecord(entry)) continue;
    const label = cleanText(entry.label ?? entry.title, 64);
    const url = sanitizeRichLinkURL(entry.url ?? entry.href);
    if (!label || !url) continue;
    const tone = normalizeRichTone(entry.tone);
    out.push({
      label,
      url,
      ...(tone ? { tone } : {}),
      ...(typeof entry.external === "boolean" ? { external: entry.external } : {}),
    });
  }
  return out;
}

function normalizeQrReceipt(raw: unknown): RichQrReceipt | undefined {
  if (!isRecord(raw)) return undefined;
  const src = sanitizeRichMediaURL(raw.src ?? raw.imageDataUrl ?? raw.imageUrl);
  if (!src) return undefined;
  const label = cleanText(raw.label ?? raw.title, 80);
  const caption = cleanText(raw.caption ?? raw.subtitle, 140);
  return { src, ...(label ? { label } : {}), ...(caption ? { caption } : {}) };
}

export function hasRichViewContent(spec: StructuredRichViewSpec | null | undefined): boolean {
  if (!spec) return false;
  return Boolean(
    (spec.metrics && spec.metrics.length > 0) ||
      (spec.entities && spec.entities.length > 0) ||
      (spec.gallery && spec.gallery.length > 0) ||
      (spec.links && spec.links.length > 0) ||
      spec.qrImage
  );
}

/**
 * Validates and normalizes an explicit `hoxi.ui/v1` rich view object.
 */
export function normalizeStructuredRichView(raw: unknown): StructuredRichViewSpec | null {
  if (!isRecord(raw)) return null;
  const metrics = normalizeMetricList(raw.metrics);
  const entities = normalizeEntityList(raw.entities ?? raw.cards ?? raw.items);
  const gallery = normalizeGalleryList(raw.gallery ?? raw.images);
  const links = normalizeLinks(raw.links);
  const qrImage = normalizeQrReceipt(raw.qrImage ?? raw.qrcode);
  const spec: StructuredRichViewSpec = {
    schemaVersion: "hoxi.ui/v1",
    ...(cleanText(raw.title, 140) ? { title: cleanText(raw.title, 140) } : {}),
    ...(cleanText(raw.subtitle, 220) ? { subtitle: cleanText(raw.subtitle, 220) } : {}),
    ...(cleanText(raw.badge, 48) ? { badge: cleanText(raw.badge, 48) } : {}),
    ...(normalizeRichTone(raw.badgeTone ?? raw.tone) ? { badgeTone: normalizeRichTone(raw.badgeTone ?? raw.tone) } : {}),
    ...(cleanText(raw.note, 220) ? { note: cleanText(raw.note, 220) } : {}),
    ...(typeof raw.promote === "boolean" ? { promote: raw.promote } : {}),
    ...(raw.layout === "list" || raw.layout === "grid" ? { layout: raw.layout } : {}),
    ...(normalizeSource(raw.source) ? { source: normalizeSource(raw.source) } : {}),
    ...(metrics.length > 0 ? { metrics } : {}),
    ...(entities.length > 0 ? { entities } : {}),
    ...(gallery.length > 0 ? { gallery } : {}),
    ...(links.length > 0 ? { links } : {}),
    ...(qrImage ? { qrImage } : {}),
  };
  return hasRichViewContent(spec) ? spec : null;
}

function tryParseJson(raw: string): Record<string, unknown> | null {
  const trimmed = (raw || "").trim();
  if (!trimmed || trimmed.length > 512_000) return null;
  if (trimmed[0] === "{" && trimmed[trimmed.length - 1] === "}") {
    try {
      const p: unknown = JSON.parse(trimmed);
      return isRecord(p) ? p : null;
    } catch {
      /* fall through */
    }
  }
  const first = trimmed.indexOf("{");
  const last = trimmed.lastIndexOf("}");
  if (first >= 0 && last > first) {
    try {
      const p: unknown = JSON.parse(trimmed.slice(first, last + 1));
      return isRecord(p) ? p : null;
    } catch {
      return null;
    }
  }
  return null;
}

function unwrapStructuredPayload(
  rawOutput: string,
  metadata?: Record<string, unknown>
): Record<string, unknown> | null {
  if (metadata && isRecord(metadata.structuredContent)) {
    return metadata.structuredContent;
  }
  const outer = tryParseJson(rawOutput);
  if (!outer) return null;
  if (isRecord(outer.structuredContent)) return outer.structuredContent;
  if (isRecord(outer.result)) {
    if (isRecord(outer.result.structuredContent)) return outer.result.structuredContent;
    if (Array.isArray(outer.result.content)) {
      for (const item of outer.result.content) {
        if (isRecord(item) && item.type === "text" && typeof item.text === "string") {
          const inner = tryParseJson(item.text);
          if (inner) return inner;
        }
      }
    }
    return outer.result;
  }
  if (Array.isArray(outer.content)) {
    for (const item of outer.content) {
      if (isRecord(item) && item.type === "text" && typeof item.text === "string") {
        const inner = tryParseJson(item.text);
        if (inner) return inner;
      }
    }
  }
  return outer;
}

/**
 * Extracts an explicit `uiView` (or `hoxi.ui/v1` root payload) from a completed tool result.
 */
export function extractExplicitToolUiView(
  rawOutput: string,
  metadata?: Record<string, unknown>
): StructuredRichViewSpec | null {
  if (metadata && isRecord(metadata.uiView)) {
    const fromMeta = normalizeStructuredRichView(metadata.uiView);
    if (fromMeta) return fromMeta;
  }
  const payload = unwrapStructuredPayload(rawOutput, metadata);
  if (!payload) return null;
  if (isRecord(payload.uiView)) {
    const fromField = normalizeStructuredRichView(payload.uiView);
    if (fromField) return fromField;
  }
  if (payload.schemaVersion === "hoxi.ui/v1") {
    return normalizeStructuredRichView(payload);
  }
  return null;
}

const resolvedToolRichViewCache = new WeakMap<object, StructuredRichViewSpec | null>();

/**
 * Resolves the structured rich view for a completed tool part by checking explicit
 * `hoxi.ui/v1` `uiView` metadata/output first, then registered extension resolvers.
 */
export function resolveToolRichView(part: ToolPart): StructuredRichViewSpec | null {
  if (part.state.status !== "completed") return null;
  if (resolvedToolRichViewCache.has(part.state)) {
    return resolvedToolRichViewCache.get(part.state) ?? null;
  }
  const explicit = extractExplicitToolUiView(
    part.state.output || "",
    part.state.metadata as Record<string, unknown> | undefined
  );
  const resolved = explicit ?? resolveRegisteredToolRichView(part);
  resolvedToolRichViewCache.set(part.state, resolved);
  return resolved;
}

/**
 * Parses a declarative Markdown fenced block: ```hoxi-cards / ```cards / ```hoxi-metrics / ```metrics.
 */
export function parseRichMarkdownBlock(
  lang: string,
  code: string
): StructuredRichViewSpec | null {
  const trimmed = (code || "").trim();
  if (!trimmed || trimmed.length > 256_000) return null;
  let parsed: unknown;
  try {
    parsed = JSON.parse(trimmed);
  } catch {
    return null;
  }
  const cleanLang = (lang || "").trim().toLowerCase();
  const isMetricsOnly = cleanLang === "hoxi-metrics" || cleanLang === "metrics";

  if (Array.isArray(parsed)) {
    if (isMetricsOnly) {
      const metrics = normalizeMetricList(parsed);
      return metrics.length > 0 ? { schemaVersion: "hoxi.ui/v1", metrics } : null;
    }
    const entities = normalizeEntityList(parsed);
    return entities.length > 0 ? { schemaVersion: "hoxi.ui/v1", entities } : null;
  }

  if (!isRecord(parsed)) return null;
  if (isMetricsOnly) {
    const metrics = normalizeMetricList(parsed.metrics ?? parsed.items);
    if (metrics.length === 0) return null;
    return {
      schemaVersion: "hoxi.ui/v1",
      ...(cleanText(parsed.title, 140) ? { title: cleanText(parsed.title, 140) } : {}),
      ...(cleanText(parsed.subtitle, 220) ? { subtitle: cleanText(parsed.subtitle, 220) } : {}),
      ...(cleanText(parsed.badge, 48) ? { badge: cleanText(parsed.badge, 48) } : {}),
      ...(normalizeRichTone(parsed.badgeTone ?? parsed.tone)
        ? { badgeTone: normalizeRichTone(parsed.badgeTone ?? parsed.tone) }
        : {}),
      ...(cleanText(parsed.note, 220) ? { note: cleanText(parsed.note, 220) } : {}),
      metrics,
    };
  }
  return normalizeStructuredRichView(parsed);
}

/**
 * Extracts a single standalone Markdown image when a paragraph consists strictly
 * of one complete `![alt](src)` span (ignoring surrounding whitespace).
 */
export function extractSingleMarkdownImage(text: string): RichGalleryImage | null {
  if (!text || !text.includes("![")) return null;
  const spans = parseInline(text, { streaming: false });
  let found: RichGalleryImage | null = null;
  for (const span of spans) {
    if (span.type === "image") {
      if (found) return null;
      const src = sanitizeRichMediaURL(span.src);
      if (!src) return null;
      found = {
        src,
        ...(span.alt ? { alt: span.alt } : {}),
        ...(span.title ? { caption: span.title } : {}),
      };
    } else if (span.type === "br") {
      continue;
    } else if (span.type === "text" && /^\s*$/.test(span.text)) {
      continue;
    } else {
      return null;
    }
  }
  return found;
}

/**
 * Detects a Markdown paragraph consisting strictly of >= 2 images (separated only by
 * whitespace or line breaks) so it renders as an interactive responsive gallery instead
 * of stacking tall vertical images down the timeline.
 */
export function extractMarkdownParagraphGallery(text: string): RichGalleryImage[] | null {
  if (!text || !text.includes("![")) return null;
  const spans = parseInline(text, { streaming: false });
  const images: RichGalleryImage[] = [];
  for (const span of spans) {
    if (span.type === "image") {
      const src = sanitizeRichMediaURL(span.src);
      if (!src) return null;
      images.push({
        src,
        ...(span.alt ? { alt: span.alt } : {}),
        ...(span.title ? { caption: span.title } : {}),
      });
    } else if (span.type === "br") {
      continue;
    } else if (span.type === "text" && /^\s*$/.test(span.text)) {
      continue;
    } else {
      return null;
    }
  }
  return images.length >= 2 ? images : null;
}

export function buildContextEnvelopeFromEntities(
  items: RichEntityItem[],
  source?: ContextSource
): ContextEnvelope {
  const ctxItems: ContextItem[] = items.map((item) => {
    const metricDigest: Record<string, string> = {};
    for (const m of item.metrics || []) {
      metricDigest[m.label] = m.value;
    }
    const digest =
      item.digest !== undefined
        ? item.digest
        : Object.keys(metricDigest).length > 0
          ? metricDigest
          : undefined;
    const subtitleParts = [
      item.subtitle,
      ...(item.metrics || []).slice(0, 3).map((m) => `${m.label}: ${m.value}`),
    ].filter(Boolean);
    return {
      kind: item.kind ?? (item.ref || item.entityType ? "entity" : "record"),
      ...(item.entityType ? { entityType: item.entityType } : {}),
      title: item.title,
      ...(subtitleParts.length > 0 ? { subtitle: subtitleParts.join(" · ") } : {}),
      ...(item.badge ? { badge: item.badge } : {}),
      ...(item.ref ? { ref: item.ref } : item.id ? { ref: { id: item.id } } : {}),
      ...(digest !== undefined ? { digest } : {}),
    };
  });
  return {
    version: 1,
    ...(source ? { source } : {}),
    items: ctxItems,
  };
}

export function buildContextEnvelopeFromImages(
  images: RichGalleryImage[],
  source?: ContextSource
): ContextEnvelope {
  return {
    version: 1,
    ...(source ? { source } : {}),
    items: images.map((img, idx) => ({
      kind: "entity" as const,
      entityType: "image",
      title: img.alt || img.filename || img.caption || `Image ${idx + 1}`,
      ...(img.caption && img.caption !== img.alt ? { subtitle: img.caption } : {}),
      ...(img.badge ? { badge: img.badge } : {}),
      ref: { id: img.id || img.filename || `img-${idx + 1}` },
      digest: {
        ...(img.filename ? { filename: img.filename } : {}),
        ...(img.src && !img.src.startsWith("data:") ? { src: img.src } : {}),
        ...(img.caption ? { caption: img.caption } : {}),
      },
    })),
  };
}

export function buildContextEnvelopeFromMetric(
  metric: RichMetricItem,
  specTitle?: string,
  source?: ContextSource
): ContextEnvelope {
  const title = specTitle ? `${specTitle} · ${metric.label}` : metric.label;
  const subtitle = [metric.value, metric.delta, metric.hint].filter(Boolean).join(" · ");
  return {
    version: 1,
    ...(source ? { source } : {}),
    items: [
      {
        kind: "snapshot",
        entityType: "metric",
        title,
        subtitle,
        ...(metric.delta ? { badge: metric.delta } : {}),
        digest: {
          label: metric.label,
          value: metric.value,
          ...(metric.delta ? { delta: metric.delta } : {}),
          ...(metric.hint ? { hint: metric.hint } : {}),
        },
      },
    ],
  };
}

export function richEntitiesToTable(
  entities: RichEntityItem[]
): { headers: string[]; rows: string[][] } | null {
  if (!entities || entities.length === 0) return null;
  const metricLabels: string[] = [];
  const seen = new Set<string>();
  for (const item of entities) {
    for (const m of item.metrics || []) {
      if (!seen.has(m.label)) {
        seen.add(m.label);
        metricLabels.push(m.label);
      }
    }
  }
  const hasBadge = entities.some((e) => Boolean(e.badge));
  const hasSubtitle = entities.some((e) => Boolean(e.subtitle));
  const headers = [
    "Title",
    ...(hasBadge ? ["Status"] : []),
    ...metricLabels.slice(0, 5),
    ...(hasSubtitle ? ["Summary"] : []),
  ];
  const rows = entities.map((item) => {
    const byLabel = new Map((item.metrics || []).map((m) => [m.label, m.value]));
    return [
      item.title,
      ...(hasBadge ? [item.badge || "—"] : []),
      ...metricLabels.slice(0, 5).map((lbl) => byLabel.get(lbl) || "—"),
      ...(hasSubtitle ? [item.subtitle || "—"] : []),
    ];
  });
  return { headers, rows };
}

/**
 * Formats a StructuredRichViewSpec into clean human-readable Markdown so session
 * exports and share-image previews never expose raw JSON walls.
 */
export function formatRichViewToReadableMarkdown(spec: StructuredRichViewSpec): string {
  const sections: string[] = [];
  if (spec.title) {
    const head = spec.badge ? `**${spec.title}** (${spec.badge})` : `**${spec.title}**`;
    sections.push(spec.subtitle ? `${head}\n${spec.subtitle}` : head);
  }
  if (spec.metrics && spec.metrics.length > 0) {
    const metricLines = spec.metrics.map((m) => {
      const extra = [m.delta, m.hint].filter(Boolean).join(" · ");
      return `- **${m.label}**: ${m.value}${extra ? ` (${extra})` : ""}`;
    });
    sections.push(metricLines.join("\n"));
  }
  if (spec.entities && spec.entities.length > 0) {
    const entityLines = spec.entities.map((e, idx) => {
      const badge = e.badge ? ` [${e.badge}]` : "";
      const metrics = (e.metrics || []).map((m) => `${m.label}: ${m.value}`).join(" · ");
      const detail = [metrics, e.subtitle].filter(Boolean).join(" — ");
      return `${idx + 1}. **${e.title}**${badge}${detail ? ` — ${detail}` : ""}`;
    });
    sections.push(entityLines.join("\n"));
  }
  if (spec.links && spec.links.length > 0) {
    sections.push(spec.links.map((l) => `- [${l.label}](${l.url})`).join("\n"));
  }
  if (spec.note) sections.push(`_${spec.note}_`);
  return sections.join("\n\n").trim();
}

const RICH_FENCE_RE = /```(hoxi-cards|cards|hoxi-metrics|metrics)\s*\n([\s\S]*?)```/gi;

/**
 * Replaces ```hoxi-cards / ```hoxi-metrics JSON fences in Markdown text with readable
 * Markdown lists for clipboard/export/share cards while leaving invalid blocks intact.
 */
export function replaceRichFencesForReadableExport(markdown: string): string {
  if (!markdown || !markdown.includes("```")) return markdown;
  return markdown.replace(RICH_FENCE_RE, (full, lang: string, body: string) => {
    const spec = parseRichMarkdownBlock(lang, body);
    if (!spec) return full;
    const readable = formatRichViewToReadableMarkdown(spec);
    return readable || full;
  });
}
