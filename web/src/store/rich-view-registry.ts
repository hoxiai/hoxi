// web/src/store/rich-view-registry.ts
// Domain-neutral types and resolver registry for structured inline rich views
// (`hoxi.ui/v1`: entity cards, KPI metric strips, media galleries, and receipts).
// Keeping the registry in `store/` avoids circular imports between timeline
// renderers and surface extensions that mount embedded session streams.
import type { ToolPart } from "../services/sse-types";
import type { ContextItemKind, ContextItemRef, ContextSource } from "./context-protocol";

export type RichTone = "neutral" | "accent" | "ok" | "warn" | "danger";

export interface RichMetricItem {
  label: string;
  value: string;
  delta?: string;
  hint?: string;
  tone?: RichTone;
}

export interface RichEntityMetric {
  label: string;
  value: string;
  tone?: RichTone;
}

export interface RichEntityItem {
  id?: string;
  title: string;
  subtitle?: string;
  badge?: string;
  badgeTone?: RichTone;
  thumbnailUrl?: string;
  entityType?: string;
  kind?: ContextItemKind;
  metrics?: RichEntityMetric[];
  tags?: string[];
  url?: string;
  urlLabel?: string;
  ref?: ContextItemRef;
  digest?: string | Record<string, unknown>;
}

export interface RichGalleryImage {
  id?: string;
  src: string;
  alt?: string;
  caption?: string;
  badge?: string;
  badgeTone?: RichTone;
  filename?: string;
  downloadUrl?: string;
}

export interface RichLinkAction {
  label: string;
  url: string;
  tone?: RichTone;
  external?: boolean;
}

export interface RichQrReceipt {
  src: string;
  label?: string;
  caption?: string;
}

export interface StructuredRichViewSpec {
  schemaVersion?: "hoxi.ui/v1";
  title?: string;
  subtitle?: string;
  badge?: string;
  badgeTone?: RichTone;
  note?: string;
  /** When true, promotes this card directly under the collapsed tool group. */
  promote?: boolean;
  layout?: "grid" | "list";
  source?: ContextSource;
  metrics?: RichMetricItem[];
  entities?: RichEntityItem[];
  gallery?: RichGalleryImage[];
  links?: RichLinkAction[];
  qrImage?: RichQrReceipt;
}

export type ToolRichViewResolver = (part: ToolPart) => StructuredRichViewSpec | null;

interface RegisteredResolver {
  id: string;
  priority: number;
  resolve: ToolRichViewResolver;
}

const resolvers: RegisteredResolver[] = [];

/**
 * Registers a tool-to-rich-view adapter (higher priority runs first).
 * Returns an unregister callback for clean lifecycle/test teardown.
 */
export function registerToolRichViewResolver(
  id: string,
  resolve: ToolRichViewResolver,
  priority = 0
): () => void {
  const cleanId = id.trim();
  const idx = resolvers.findIndex((r) => r.id === cleanId);
  const entry: RegisteredResolver = { id: cleanId, priority, resolve };
  if (idx >= 0) {
    resolvers[idx] = entry;
  } else {
    resolvers.push(entry);
  }
  resolvers.sort((a, b) => b.priority - a.priority);
  return () => {
    const removeIdx = resolvers.findIndex((r) => r.id === cleanId && r.resolve === resolve);
    if (removeIdx >= 0) resolvers.splice(removeIdx, 1);
  };
}

export function resolveRegisteredToolRichView(part: ToolPart): StructuredRichViewSpec | null {
  for (const entry of resolvers) {
    try {
      const res = entry.resolve(part);
      if (res) return res;
    } catch {
      /* isolate faulty resolvers */
    }
  }
  return null;
}
