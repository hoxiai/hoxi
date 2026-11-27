// web/src/components/timeline/svg-sanitizer.ts
// Strict allowlist sanitizer and export utilities for inline ```svg code blocks
// and Mermaid/Chart SVG exports. Never executes scripts or loads external URLs.
import { saveBlobWithFeedback } from "../../store/workspace";

const ALLOWED_SVG_TAGS = new Set([
  "svg", "g", "defs", "symbol", "use", "title", "desc",
  "path", "rect", "circle", "ellipse", "line", "polyline", "polygon",
  "text", "tspan", "textPath",
  "linearGradient", "radialGradient", "stop", "pattern",
  "clipPath", "mask", "marker", "filter",
  "feGaussianBlur", "feOffset", "feMerge", "feMergeNode", "feDropShadow",
  "feColorMatrix", "feBlend", "feComposite", "feFlood", "feMorphology",
  "style",
].map((tag) => tag.toLowerCase()));

const DANGEROUS_ATTR_RE = /^(?:on[a-z]+|xmlns:xlink)$/i;
const URL_ATTRS = new Set(["href", "xlink:href", "src", "action", "formaction"]);
const SAFE_FRAGMENT_RE = /^#[A-Za-z_][\w:.-]*$/;

function sanitizeCssText(css: string): string {
  return css
    .replace(/\/\*[\s\S]*?\*\//g, "")
    .replace(/\\[0-9a-f]{1,6}\s?/gi, "")
    .replace(/@import\b[^;]*;?/gi, "")
    .replace(/expression\s*\([^)]*\)/gi, "")
    .replace(/javascript\s*:/gi, "")
    .replace(/vbscript\s*:/gi, "")
    .replace(/url\s*\(([^)]*)\)/gi, (_full, inner: string) => {
      const cleaned = inner.trim().replace(/^['"]|['"]$/g, "").trim();
      return SAFE_FRAGMENT_RE.test(cleaned) ? `url(${cleaned})` : "none";
    });
}

function sanitizeAttributeValue(name: string, value: string): string | null {
  const lower = name.toLowerCase();
  if (lower.startsWith("on")) return null;
  const trimmed = value.trim();

  if (URL_ATTRS.has(lower)) {
    // Allow internal fragment references (#grad1, #arrow) for gradients/markers/clips only.
    return SAFE_FRAGMENT_RE.test(trimmed) ? trimmed : null;
  }

  if (lower === "style") {
    return sanitizeCssText(trimmed);
  }

  // Strip javascript:/vbscript:/data:text/html and external url(...) from presentation attributes.
  if (/(?:javascript|vbscript)\s*:/i.test(trimmed)) return null;
  if (/data\s*:\s*text\/html/i.test(trimmed)) return null;
  if (/url\s*\(/i.test(trimmed)) {
    return trimmed.replace(/url\s*\(([^)]*)\)/gi, (_full, inner: string) => {
      const cleaned = inner.trim().replace(/^['"]|['"]$/g, "").trim();
      return SAFE_FRAGMENT_RE.test(cleaned) ? `url(${cleaned})` : "none";
    });
  }
  return value;
}

function sanitizeElementTree(node: Element): void {
  const children = Array.from(node.children);
  for (const child of children) {
    const tag = child.localName.toLowerCase();
    if (!ALLOWED_SVG_TAGS.has(tag)) {
      child.remove();
      continue;
    }
    if (tag === "style") {
      child.textContent = sanitizeCssText(child.textContent || "");
    }
    for (const attr of Array.from(child.attributes)) {
      const lower = attr.name.toLowerCase();
      if (lower.startsWith("on") || lower === "srcdoc") {
        child.removeAttribute(attr.name);
        continue;
      }
      if (DANGEROUS_ATTR_RE.test(attr.name) && lower !== "xmlns:xlink") {
        child.removeAttribute(attr.name);
        continue;
      }
      const next = sanitizeAttributeValue(attr.name, attr.value);
      if (next === null) {
        child.removeAttribute(attr.name);
      } else if (next !== attr.value) {
        child.setAttribute(attr.name, next);
      }
    }
    sanitizeElementTree(child);
  }
}

/**
 * Fallback string sanitizer for non-DOM environments (Node unit tests) and pre-filtering.
 * Strips forbidden elements, event handlers, and external/script URLs.
 */
function sanitizeSvgStringFallback(raw: string): string {
  let out = raw
    .replace(/<\?xml[\s\S]*?\?>/gi, "")
    .replace(/<!DOCTYPE[\s\S]*?>/gi, "")
    .replace(/<!--[\s\S]*?-->/g, "");

  // Remove disallowed container elements with their bodies (backref ensures matching close tag).
  const forbiddenTags =
    "script|foreignobject|iframe|object|embed|audio|video|applet|base|link|meta|form|input|textarea|button|animate|animatetransform|animatemotion|set";
  const pairedRe = new RegExp(
    `<\\s*(${forbiddenTags})\\b[^>]*>[\\s\\S]*?<\\s*\\/\\s*\\1\\s*>`,
    "gi"
  );
  // Repeat in case forbidden tags are nested inside each other.
  for (let pass = 0; pass < 3; pass++) {
    const next = out.replace(pairedRe, "");
    if (next === out) break;
    out = next;
  }
  out = out.replace(new RegExp(`<\\s*\\/?\\s*(?:${forbiddenTags})\\b[^>]*\\/?>`, "gi"), "");

  // Sanitize inline <style> blocks.
  out = out.replace(
    /<style\b([^>]*)>([\s\S]*?)<\/style>/gi,
    (_m, attrs: string, body: string) => `<style${attrs}>${sanitizeCssText(body)}</style>`
  );

  // Strip event handler attributes (`onload="..."`, `onclick='...'`, `onerror=foo`).
  out = out.replace(/\s+on[a-z]+\s*=\s*(?:"[^"]*"|'[^']*'|[^\s>]+)/gi, "");

  // Strip non-fragment href/xlink:href/src attributes.
  out = out.replace(
    /\s+((?:xlink:)?href|src)\s*=\s*(?:"([^"]*)"|'([^']*)'|([^\s>]+))/gi,
    (_m, attr: string, d?: string, s?: string, u?: string) => {
      const val = (d ?? s ?? u ?? "").trim();
      return SAFE_FRAGMENT_RE.test(val) ? ` ${attr}="${val}"` : "";
    }
  );

  // Strip external url(...) and javascript: from style / attribute values.
  out = out.replace(/javascript\s*:/gi, "").replace(/vbscript\s*:/gi, "");
  out = out.replace(/url\s*\(([^)]*)\)/gi, (_full, inner: string) => {
    const cleaned = inner.trim().replace(/^['"]|['"]$/g, "").trim();
    return SAFE_FRAGMENT_RE.test(cleaned) ? `url(${cleaned})` : "none";
  });

  const startIdx = out.search(/<svg\b/i);
  const endIdx = out.toLowerCase().lastIndexOf("</svg>");
  if (startIdx < 0 || endIdx < startIdx) return "";
  return out.slice(startIdx, endIdx + 6).trim();
}

export interface SanitizedSvgResult {
  svg: string;
  error?: string;
}

/**
 * Checks whether raw text appears to contain a complete `<svg>...</svg>` root element.
 */
export function hasCompleteSvgMarkup(raw: string): boolean {
  const trimmed = (raw || "").trim();
  return /<svg\b[\s\S]*<\/svg\s*>/i.test(trimmed);
}

/**
 * Strictly sanitizes raw SVG markup for safe inline rendering via `innerHTML` or `data:image/svg+xml`.
 */
export function sanitizeSvgMarkup(raw: string): SanitizedSvgResult {
  const trimmed = (raw || "").trim();
  if (!trimmed || !hasCompleteSvgMarkup(trimmed)) {
    return { svg: "", error: "Incomplete SVG markup" };
  }

  // Always pre-strip dangerous blocks before parsing.
  const preCleaned = sanitizeSvgStringFallback(trimmed);
  if (!preCleaned) {
    return { svg: "", error: "Invalid SVG markup" };
  }

  if (typeof DOMParser === "undefined" || typeof XMLSerializer === "undefined") {
    return { svg: preCleaned };
  }

  try {
    const parser = new DOMParser();
    const doc = parser.parseFromString(preCleaned, "image/svg+xml");
    if (doc.querySelector("parsererror")) {
      return { svg: "", error: "SVG XML parse error" };
    }
    const root = doc.documentElement;
    if (!root || root.localName.toLowerCase() !== "svg") {
      return { svg: "", error: "Root element must be <svg>" };
    }

    // Sanitize root attributes and descendant subtree.
    for (const attr of Array.from(root.attributes)) {
      const next = sanitizeAttributeValue(attr.name, attr.value);
      if (next === null) {
        root.removeAttribute(attr.name);
      } else if (next !== attr.value) {
        root.setAttribute(attr.name, next);
      }
    }
    if (!root.getAttribute("xmlns")) {
      root.setAttribute("xmlns", "http://www.w3.org/2000/svg");
    }
    if (!root.getAttribute("viewBox")) {
      const w = parseFloat(root.getAttribute("width") || "");
      const h = parseFloat(root.getAttribute("height") || "");
      if (Number.isFinite(w) && w > 0 && Number.isFinite(h) && h > 0) {
        root.setAttribute("viewBox", `0 0 ${w} ${h}`);
      }
    }

    sanitizeElementTree(root);
    const serialized = new XMLSerializer().serializeToString(root);
    return { svg: serialized };
  } catch {
    return { svg: "", error: "SVG sanitization failed" };
  }
}

export function svgToDataURL(svg: string): string {
  return `data:image/svg+xml;charset=utf-8,${encodeURIComponent(svg)}`;
}

export function downloadSvgFile(svg: string, filename = "diagram.svg"): void {
  if (!svg || typeof document === "undefined") return;
  const blob = new Blob([svg], { type: "image/svg+xml;charset=utf-8" });
  void saveBlobWithFeedback(blob, filename.endsWith(".svg") ? filename : `${filename}.svg`);
}

function ensureSvgExplicitDimensions(svg: string): {
  svg: string;
  width?: number;
  height?: number;
} {
  const openTagMatch = svg.match(/<svg\b[^>]*>/i);
  if (!openTagMatch) return { svg };
  const openTag = openTagMatch[0];
  const viewBoxMatch = openTag.match(/\bviewBox\s*=\s*["']\s*[+-]?[\d.]+\s+[+-]?[\d.]+\s+([\d.]+)\s+([\d.]+)\s*["']/i);
  const vbW = viewBoxMatch ? parseFloat(viewBoxMatch[1]) : NaN;
  const vbH = viewBoxMatch ? parseFloat(viewBoxMatch[2]) : NaN;

  const widthMatch = openTag.match(/\bwidth\s*=\s*["']\s*([^"']+)\s*["']/i);
  const heightMatch = openTag.match(/\bheight\s*=\s*["']\s*([^"']+)\s*["']/i);
  const hasPixelWidth = Boolean(widthMatch && /^\d+(?:\.\d+)?(?:px)?$/i.test(widthMatch[1].trim()));
  const hasPixelHeight = Boolean(heightMatch && /^\d+(?:\.\d+)?(?:px)?$/i.test(heightMatch[1].trim()));

  if (hasPixelWidth && hasPixelHeight) {
    return {
      svg,
      width: parseFloat(widthMatch![1]),
      height: parseFloat(heightMatch![1]),
    };
  }
  if (!Number.isFinite(vbW) || vbW <= 0 || !Number.isFinite(vbH) || vbH <= 0) {
    return { svg };
  }

  let updatedTag = openTag;
  if (!hasPixelWidth) {
    updatedTag = widthMatch
      ? updatedTag.replace(/\bwidth\s*=\s*["'][^"']*["']/i, `width="${vbW}"`)
      : updatedTag.replace(/<svg\b/i, `<svg width="${vbW}"`);
  }
  if (!hasPixelHeight) {
    updatedTag = heightMatch
      ? updatedTag.replace(/\bheight\s*=\s*["'][^"']*["']/i, `height="${vbH}"`)
      : updatedTag.replace(/<svg\b/i, `<svg height="${vbH}"`);
  }
  return {
    svg: svg.slice(0, openTagMatch.index!) + updatedTag + svg.slice(openTagMatch.index! + openTag.length),
    width: vbW,
    height: vbH,
  };
}

export async function downloadSvgAsPng(
  svg: string,
  filename = "diagram.png",
  backgroundColor?: string
): Promise<boolean> {
  if (!svg || typeof document === "undefined" || typeof Image === "undefined") return false;
  const sized = ensureSvgExplicitDimensions(svg);
  return new Promise<boolean>((resolve) => {
    const img = new Image();
    img.onload = () => {
      try {
        const scale = 2;
        const baseW = sized.width || img.naturalWidth || 800;
        const baseH = sized.height || img.naturalHeight || 500;
        const w = Math.max(1, Math.round(baseW * scale));
        const h = Math.max(1, Math.round(baseH * scale));
        const canvas = document.createElement("canvas");
        canvas.width = w;
        canvas.height = h;
        const ctx = canvas.getContext("2d");
        if (!ctx) {
          resolve(false);
          return;
        }
        if (backgroundColor) {
          ctx.fillStyle = backgroundColor;
          ctx.fillRect(0, 0, w, h);
        }
        ctx.drawImage(img, 0, 0, w, h);
        canvas.toBlob((blob) => {
          if (!blob) {
            resolve(false);
            return;
          }
          void saveBlobWithFeedback(blob, filename.endsWith(".png") ? filename : `${filename}.png`)
            .then(() => resolve(true));
        }, "image/png");
      } catch {
        resolve(false);
      }
    };
    img.onerror = () => resolve(false);
    img.src = svgToDataURL(sized.svg);
  });
}
