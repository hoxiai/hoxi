import type { ThemedToken } from "shiki";

export type HighlightedCode = { code: string; tokens: ThemedToken[][] };

const MAX_HIGHLIGHT_CACHE = 240;
const highlightCache = new Map<string, HighlightedCode>();

let shikiPromise: Promise<typeof import("shiki")> | undefined;

const LANG_CACHE_ALIASES: Record<string, string> = {
  js: "javascript",
  mjs: "javascript",
  cjs: "javascript",
  ts: "typescript",
  py: "python",
  rb: "ruby",
  rs: "rust",
  sh: "shellscript",
  bash: "shellscript",
  zsh: "shellscript",
  yml: "yaml",
  md: "markdown",
};

function normalizeHighlightLang(lang: string): string {
  const clean = lang.trim().toLowerCase();
  return LANG_CACHE_ALIASES[clean] ?? clean;
}

function buildHighlightCacheKey(code: string, lang: string, theme: "dark" | "light"): string {
  return `${theme}\0${normalizeHighlightLang(lang)}\0${code}`;
}

export function getCachedHighlight(
  code: string,
  language: string,
  theme: "dark" | "light",
): HighlightedCode | undefined {
  const lang = language.trim().toLowerCase();
  if (!code || !lang) return undefined;
  const key = buildHighlightCacheKey(code, lang, theme);
  const hit = highlightCache.get(key);
  if (!hit) return undefined;
  // Refresh LRU order
  highlightCache.delete(key);
  highlightCache.set(key, hit);
  return hit;
}

function setCachedHighlight(
  code: string,
  lang: string,
  theme: "dark" | "light",
  value: HighlightedCode,
) {
  const key = buildHighlightCacheKey(code, lang, theme);
  highlightCache.delete(key);
  highlightCache.set(key, value);
  if (highlightCache.size > MAX_HIGHLIGHT_CACHE) {
    const oldest = highlightCache.keys().next().value;
    if (oldest !== undefined) highlightCache.delete(oldest);
  }
}

export function fileLanguage(path: string): string {
  const name = path.split(/[\\/]/).pop()?.toLowerCase() ?? "";
  if (/^dockerfile(?:\.|$)/.test(name)) return "dockerfile";
  if (name === "makefile") return "make";
  const extension = name.split(".").pop() ?? "";
  const aliases: Record<string, string> = {
    js: "javascript", jsx: "jsx", ts: "typescript", tsx: "tsx",
    mjs: "javascript", cjs: "javascript", py: "python", rb: "ruby",
    rs: "rust", sh: "shellscript", bash: "shellscript", zsh: "shellscript",
    yml: "yaml", md: "markdown", h: "c", m: "objective-c", mm: "objective-cpp",
    go: "go", mod: "gomod", sum: "text", env: "dotenv", conf: "ini",
  };
  return aliases[extension] ?? extension;
}

export async function highlightCode(
  code: string,
  language: string,
  theme: "dark" | "light",
): Promise<HighlightedCode | undefined> {
  const lang = language.trim().toLowerCase();
  // Keep large blocks and unlabelled output cheap while preserving their full text.
  if (!code || code.length > 50_000 || code.split("\n").length > 1_000 ||
      ["", "text", "txt", "plaintext", "plain"].includes(lang)) return;

  const cached = getCachedHighlight(code, lang, theme);
  if (cached) return cached;

  try {
    const shiki = await (shikiPromise ??= import("shiki"));
    if (!Object.hasOwn(shiki.bundledLanguages, lang)) return;
    const result = await shiki.codeToTokens(code, {
      lang: lang as keyof typeof shiki.bundledLanguages,
      theme: theme === "dark" ? "github-dark" : "github-light",
    });
    const entry: HighlightedCode = { code, tokens: result.tokens };
    setCachedHighlight(code, lang, theme, entry);
    return entry;
  } catch {
    // Loading or tokenization failures must never hide the original code.
    shikiPromise = undefined;
    return;
  }
}
