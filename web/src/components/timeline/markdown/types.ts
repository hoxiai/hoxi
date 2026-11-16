// web/src/components/timeline/markdown/types.ts

export type TableAlign = "left" | "center" | "right";
export type AlertKind = "note" | "tip" | "important" | "warning" | "caution";

export interface ListItemEntry {
  text: string;
  indent: number;
  kind: "bullet" | "ordered";
  orderNumber?: number;
}

export interface FrontmatterEntry {
  key: string;
  value: string;
}

export interface MarkdownHeadingItem {
  level: 1 | 2 | 3 | 4;
  text: string;
  slug: string;
}

export type BlockToken =
  | { type: "frontmatter"; entries: FrontmatterEntry[] }
  | { type: "code"; lang: string; code: string }
  | { type: "heading"; level: 1 | 2 | 3 | 4; text: string }
  | { type: "list"; ordered: boolean; start: number; items: ListItemEntry[] }
  | { type: "blockquote"; alert?: AlertKind; text: string }
  | { type: "table"; headers: string[]; aligns: TableAlign[]; rows: string[][] }
  | { type: "hr" }
  | { type: "paragraph"; text: string };

export type InlineSpan =
  | { type: "text"; text: string }
  | { type: "br" }
  | { type: "code"; text: string; unclosed?: boolean }
  | { type: "bold"; text: string }
  | { type: "italic"; text: string }
  | { type: "strike"; text: string }
  | { type: "link"; text: string; href: string }
  | { type: "image"; alt: string; src: string; title?: string };
