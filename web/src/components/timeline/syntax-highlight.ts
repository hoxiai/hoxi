import type { ThemedToken } from "shiki";

export type HighlightedCode = { code: string; tokens: ThemedToken[][] };

function isSameThemedToken(a: ThemedToken, b: ThemedToken): boolean {
  return (
    a === b ||
    (a.content === b.content &&
      a.color === b.color &&
      (a.fontStyle ?? 0) === (b.fontStyle ?? 0))
  );
}

function isSameTokenLine(a: ThemedToken[] | undefined, b: ThemedToken[]): boolean {
  if (!a) return false;
  if (a === b) return true;
  if (a.length !== b.length) return false;
  for (let i = 0; i < a.length; i++) {
    if (!isSameThemedToken(a[i], b[i])) return false;
  }
  return true;
}

/**
 * Reuses unchanged line and token object references across streaming highlight
 * passes so Solid's `<Index>` skips completed lines and prefix tokens.
 */
export function stabilizeHighlightedLines(
  prev: ThemedToken[][] | undefined,
  next: ThemedToken[][],
): ThemedToken[][] {
  if (!prev || prev.length === 0) return next;
  let reusedAll = prev.length === next.length;
  const out = next.map((line, idx) => {
    const prior = prev[idx];
    if (isSameTokenLine(prior, line)) {
      return prior!;
    }
    reusedAll = false;
    if (!prior) return line;
    let reusedLineTokens = prior.length === line.length;
    const stabilizedLine = line.map((tok, tIdx) => {
      const priorTok = prior[tIdx];
      if (priorTok && isSameThemedToken(priorTok, tok)) {
        return priorTok;
      }
      reusedLineTokens = false;
      return tok;
    });
    return reusedLineTokens ? prior : stabilizedLine;
  });
  return reusedAll ? prev : out;
}

const MAX_HIGHLIGHT_CACHE = 240;
const MAX_HIGHLIGHT_CACHE_CHARS = 256 * 1024;
const highlightCache = new Map<string, HighlightedCode>();
let highlightCacheChars = 0;

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

function commonPrefixLength(a: string, b: string): number {
  const limit = Math.min(a.length, b.length);
  let i = 0;
  while (i < limit && a.charCodeAt(i) === b.charCodeAt(i)) i++;
  return i;
}

function isSupersededHighlightCode(existing: string, next: string): boolean {
  if (!existing || !next || existing === next) return false;
  if (next.startsWith(existing) || existing.startsWith(next)) return true;
  const shorter = Math.min(existing.length, next.length);
  if (shorter < 64) return false;
  return commonPrefixLength(existing, next) >= Math.max(64, Math.floor(shorter * 0.8));
}

function deleteCachedHighlightKey(key: string): HighlightedCode | undefined {
  const existing = highlightCache.get(key);
  if (!existing) return undefined;
  highlightCache.delete(key);
  highlightCacheChars = Math.max(0, highlightCacheChars - existing.code.length);
  return existing;
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
  previousCode?: string,
): HighlightedCode {
  const normLang = normalizeHighlightLang(lang);
  const scopePrefix = `${theme}\0${normLang}\0`;
  const key = `${scopePrefix}${code}`;
  let priorTokens: ThemedToken[][] | undefined;

  if (previousCode && previousCode !== code) {
    const prevHit = deleteCachedHighlightKey(`${scopePrefix}${previousCode}`);
    if (prevHit) priorTokens = prevHit.tokens;
  }

  for (const [k, entry] of highlightCache) {
    if (k === key || !k.startsWith(scopePrefix)) continue;
    if (isSupersededHighlightCode(entry.code, code)) {
      priorTokens ??= entry.tokens;
      deleteCachedHighlightKey(k);
    }
  }

  const stabilized: HighlightedCode = priorTokens
    ? { code: value.code, tokens: stabilizeHighlightedLines(priorTokens, value.tokens) }
    : value;

  deleteCachedHighlightKey(key);
  highlightCache.set(key, stabilized);
  highlightCacheChars += code.length;

  while (
    highlightCache.size > MAX_HIGHLIGHT_CACHE ||
    (highlightCache.size > 1 && highlightCacheChars > MAX_HIGHLIGHT_CACHE_CHARS)
  ) {
    const oldest = highlightCache.keys().next().value;
    if (oldest === undefined) break;
    deleteCachedHighlightKey(oldest);
  }
  return stabilized;
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
  previousCode?: string,
): Promise<HighlightedCode | undefined> {
  const lang = language.trim().toLowerCase();
  // Keep large blocks and unlabelled output cheap while preserving their full text.
  if (!code || code.length > 50_000 || code.split("\n").length > 1_000 ||
      ["", "text", "txt", "plaintext", "plain"].includes(lang)) return;

  const cached = getCachedHighlight(code, lang, theme);
  if (cached) {
    if (previousCode && previousCode !== code) {
      deleteCachedHighlightKey(buildHighlightCacheKey(previousCode, lang, theme));
    }
    return cached;
  }

  try {
    const shiki = await (shikiPromise ??= import("shiki"));
    if (!Object.hasOwn(shiki.bundledLanguages, lang)) return;
    const result = await shiki.codeToTokens(code, {
      lang: lang as keyof typeof shiki.bundledLanguages,
      theme: theme === "dark" ? "github-dark" : "github-light",
    });
    const entry: HighlightedCode = { code, tokens: result.tokens };
    return setCachedHighlight(code, lang, theme, entry, previousCode);
  } catch {
    // Loading or tokenization failures must never hide the original code.
    shikiPromise = undefined;
    return;
  }
}
