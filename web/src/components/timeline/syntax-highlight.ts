import type { ThemedToken } from "shiki";

export type HighlightedCode = { code: string; tokens: ThemedToken[][] };

export type HighlightWorkerRequest = {
  type: "highlight"; clientID: number; requestID: number;
  code: string; language: string; theme: "dark" | "light";
} | { type: "cancel"; clientID: number };
export interface HighlightWorkerResponse {
  clientID: number;
  requestID: number;
  result?: HighlightedCode;
}

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
  for (let i = a.length - 1; i >= 0; i--) {
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

type ShikiModule = typeof import("shiki");
type ShikiHighlighter = Awaited<ReturnType<ShikiModule["getSingletonHighlighter"]>>;
type ShikiGrammarState = NonNullable<ReturnType<ShikiHighlighter["codeToTokens"]>["grammarState"]>;

type HighlightCheckpoint = {
  scopePrefix: string;
  prefixCode: string;
  tokens: ThemedToken[][];
  grammarState: ShikiGrammarState;
  inlineSafe: boolean;
};

const MAX_CHECKPOINT_CACHE = 16;
const MAX_CHECKPOINT_CACHE_CHARS = 256 * 1024;
const checkpointCache = new Map<string, HighlightCheckpoint>();
let checkpointCacheChars = 0;
const semicolonProtoCache = new Map<string, ThemedToken | null>();

let shikiPromise: Promise<ShikiModule> | undefined;
let shikiModule: ShikiModule | undefined;
let shikiHighlighter: ShikiHighlighter | undefined;
const loadedShikiLangs = new Set<string>();
const loadedShikiThemes = new Set<string>();

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

function deleteCheckpointKey(key: string): void {
  const existing = checkpointCache.get(key);
  if (!existing) return;
  checkpointCache.delete(key);
  checkpointCacheChars = Math.max(0, checkpointCacheChars - existing.prefixCode.length);
}

function findCheckpoint(scopePrefix: string, code: string): HighlightCheckpoint | undefined {
  let bestKey: string | undefined;
  let bestEntry: HighlightCheckpoint | undefined;
  for (const [k, entry] of checkpointCache) {
    if (entry.scopePrefix !== scopePrefix) continue;
    const pLen = entry.prefixCode.length;
    if (
      code.length > pLen &&
      (code.charCodeAt(pLen) === 10 || entry.inlineSafe) &&
      code.startsWith(entry.prefixCode)
    ) {
      if (!bestEntry || pLen > bestEntry.prefixCode.length) {
        bestKey = k;
        bestEntry = entry;
      }
    }
  }
  if (bestKey && bestEntry) {
    checkpointCache.delete(bestKey);
    checkpointCache.set(bestKey, bestEntry);
  }
  return bestEntry;
}

function saveCheckpoint(
  scopePrefix: string,
  prefixCode: string,
  tokens: ThemedToken[][],
  grammarState: ShikiGrammarState | undefined,
  inlineSafe = false,
): void {
  if (!grammarState || !prefixCode) return;
  const key = `${scopePrefix}${prefixCode}`;
  for (const [k, entry] of checkpointCache) {
    if (entry.scopePrefix !== scopePrefix || k === key) continue;
    if (isSupersededHighlightCode(entry.prefixCode, prefixCode)) {
      deleteCheckpointKey(k);
    }
  }
  deleteCheckpointKey(key);
  checkpointCache.set(key, { scopePrefix, prefixCode, tokens, grammarState, inlineSafe });
  checkpointCacheChars += prefixCode.length;
  while (
    checkpointCache.size > MAX_CHECKPOINT_CACHE ||
    (checkpointCache.size > 1 && checkpointCacheChars > MAX_CHECKPOINT_CACHE_CHARS)
  ) {
    const oldest = checkpointCache.keys().next().value;
    if (oldest === undefined) break;
    deleteCheckpointKey(oldest);
  }
}

function getSemicolonProto(
  hl: ShikiHighlighter,
  typedLang: Parameters<ShikiHighlighter["codeToTokens"]>[1]["lang"],
  shikiTheme: "github-dark" | "github-light",
  scopePrefix: string,
): ThemedToken | null {
  if (semicolonProtoCache.has(scopePrefix)) {
    return semicolonProtoCache.get(scopePrefix) ?? null;
  }
  const res = hl.codeToTokens(";", { lang: typedLang, theme: shikiTheme });
  const tok = res.tokens[0]?.[0] ?? null;
  semicolonProtoCache.set(scopePrefix, tok);
  return tok;
}

function isInlineSafeCheckpoint(
  hl: ShikiHighlighter,
  typedLang: Parameters<ShikiHighlighter["codeToTokens"]>[1]["lang"],
  shikiTheme: "github-dark" | "github-light",
  scopePrefix: string,
  prefixCode: string,
  tokens: ThemedToken[][],
  grammarState: ShikiGrammarState | undefined,
): boolean {
  if (!grammarState || !prefixCode.endsWith(";") || tokens.length === 0) return false;
  const scopes = (grammarState as { getScopes?: () => string[] }).getScopes?.();
  if (!scopes || scopes.length !== 1) return false;
  const lastLine = tokens[tokens.length - 1];
  if (!lastLine || lastLine.length === 0) return false;
  const lastTok = lastLine[lastLine.length - 1];
  if (!/^[\s}\])]*;$/.test(lastTok.content)) {
    return false;
  }
  const proto = getSemicolonProto(hl, typedLang, shikiTheme, scopePrefix);
  if (!proto) return false;
  return (
    lastTok.color === proto.color &&
    (lastTok.fontStyle ?? 0) === (proto.fontStyle ?? 0)
  );
}

function mergeInlineTokens(
  prefixLines: ThemedToken[][],
  tailLines: ThemedToken[][],
): ThemedToken[][] {
  if (prefixLines.length === 0) return tailLines;
  if (tailLines.length === 0) return prefixLines;
  const out = prefixLines.slice(0, -1);
  const lastPrefixLine = prefixLines[prefixLines.length - 1];
  const firstTailLine = tailLines[0];
  let mergedLine: ThemedToken[];
  if (lastPrefixLine.length > 0 && firstTailLine.length > 0) {
    const lastTok = lastPrefixLine[lastPrefixLine.length - 1];
    const firstTok = firstTailLine[0];
    if (
      lastTok.color === firstTok.color &&
      (lastTok.fontStyle ?? 0) === (firstTok.fontStyle ?? 0)
    ) {
      mergedLine = lastPrefixLine
        .slice(0, -1)
        .concat(
          {
            ...lastTok,
            content: lastTok.content + firstTok.content,
          },
          firstTailLine.slice(1),
        );
    } else {
      mergedLine = lastPrefixLine.concat(firstTailLine);
    }
  } else {
    mergedLine = lastPrefixLine.concat(firstTailLine);
  }
  out.push(mergedLine);
  for (let i = 1; i < tailLines.length; i++) {
    out.push(tailLines[i]);
  }
  return out;
}

function shiftTokenOffsets(lines: ThemedToken[][], offsetDelta: number): ThemedToken[][] {
  if (offsetDelta === 0) return lines;
  for (let i = 0; i < lines.length; i++) {
    const line = lines[i];
    for (let j = 0; j < line.length; j++) {
      line[j].offset += offsetDelta;
    }
  }
  return lines;
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

  const exactCp = checkpointCache.get(`${scopePrefix}${code}`);
  if (exactCp) {
    exactCp.tokens = stabilized.tokens;
  } else {
    const cp = findCheckpoint(scopePrefix, code);
    if (cp) {
      if (code.charCodeAt(cp.prefixCode.length) === 10 && cp.tokens.length <= stabilized.tokens.length) {
        cp.tokens = stabilized.tokens.slice(0, cp.tokens.length);
      } else if (cp.inlineSafe && cp.tokens.length === 1 && stabilized.tokens.length >= 1) {
        const prefixTokCount = cp.tokens[0].length;
        if (
          prefixTokCount <= stabilized.tokens[0].length &&
          stabilized.tokens[0][prefixTokCount - 1]?.content === cp.tokens[0][prefixTokCount - 1]?.content
        ) {
          cp.tokens = [stabilized.tokens[0].slice(0, prefixTokCount)];
        }
      }
    }
  }

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

export function isWithinHighlightLimits(code: string, lang: string): boolean {
  if (!code || code.length > 50_000 || ["", "text", "txt", "plaintext", "plain"].includes(lang)) {
    return false;
  }
  let lines = 1;
  for (let i = 0; i < code.length; i++) {
    if (code.charCodeAt(i) === 10 && ++lines > 1_000) return false;
  }
  return true;
}

function tokenizeWithCheckpoints(
  hl: ShikiHighlighter,
  code: string,
  lang: string,
  shikiTheme: "github-dark" | "github-light",
  scopePrefix: string,
): ThemedToken[][] {
  const typedLang = lang as Parameters<ShikiHighlighter["codeToTokens"]>[1]["lang"];
  const lastNewline = code.lastIndexOf("\n");
  const cp = findCheckpoint(scopePrefix, code);
  if (cp) {
    if (code.charCodeAt(cp.prefixCode.length) === 10) {
      const baseOffset = cp.prefixCode.length + 1;
      if (lastNewline > cp.prefixCode.length && code.charCodeAt(lastNewline - 1) !== 13) {
        const midCode = code.slice(baseOffset, lastNewline);
        const midRes = hl.codeToTokens(midCode, {
          lang: typedLang,
          theme: shikiTheme,
          grammarState: cp.grammarState,
        });
        const midTokens = cp.tokens.concat(shiftTokenOffsets(midRes.tokens, baseOffset));
        saveCheckpoint(scopePrefix, code.slice(0, lastNewline), midTokens, midRes.grammarState);
        const tailCode = code.slice(lastNewline + 1);
        const tailRes = hl.codeToTokens(tailCode, {
          lang: typedLang,
          theme: shikiTheme,
          grammarState: midRes.grammarState,
        });
        const out = midTokens.concat(shiftTokenOffsets(tailRes.tokens, lastNewline + 1));
        if (
          !code.endsWith("\r") &&
          isInlineSafeCheckpoint(hl, typedLang, shikiTheme, scopePrefix, code, out, tailRes.grammarState)
        ) {
          saveCheckpoint(scopePrefix, code, out, tailRes.grammarState, true);
        }
        return out;
      }
      const tailCode = code.slice(baseOffset);
      const tailRes = hl.codeToTokens(tailCode, {
        lang: typedLang,
        theme: shikiTheme,
        grammarState: cp.grammarState,
      });
      const out = cp.tokens.concat(shiftTokenOffsets(tailRes.tokens, baseOffset));
      if (
        !code.endsWith("\r") &&
        isInlineSafeCheckpoint(hl, typedLang, shikiTheme, scopePrefix, code, out, tailRes.grammarState)
      ) {
        saveCheckpoint(scopePrefix, code, out, tailRes.grammarState, true);
      }
      return out;
    }

    const baseOffset = cp.prefixCode.length;
    if (lastNewline >= baseOffset && code.charCodeAt(lastNewline - 1) !== 13) {
      const midCode = code.slice(baseOffset, lastNewline);
      const midRes = hl.codeToTokens(midCode, {
        lang: typedLang,
        theme: shikiTheme,
        grammarState: cp.grammarState,
      });
      const midTokens = mergeInlineTokens(cp.tokens, shiftTokenOffsets(midRes.tokens, baseOffset));
      saveCheckpoint(scopePrefix, code.slice(0, lastNewline), midTokens, midRes.grammarState);
      const tailCode = code.slice(lastNewline + 1);
      const tailRes = hl.codeToTokens(tailCode, {
        lang: typedLang,
        theme: shikiTheme,
        grammarState: midRes.grammarState,
      });
      return midTokens.concat(shiftTokenOffsets(tailRes.tokens, lastNewline + 1));
    }

    const lastSemi = code.lastIndexOf(";", code.length - 2);
    if (lastSemi >= baseOffset + 128) {
      const midCode = code.slice(baseOffset, lastSemi + 1);
      const midRes = hl.codeToTokens(midCode, {
        lang: typedLang,
        theme: shikiTheme,
        grammarState: cp.grammarState,
      });
      const midPrefix = code.slice(0, lastSemi + 1);
      const midTokens = mergeInlineTokens(cp.tokens, shiftTokenOffsets(midRes.tokens, baseOffset));
      if (
        isInlineSafeCheckpoint(
          hl,
          typedLang,
          shikiTheme,
          scopePrefix,
          midPrefix,
          midTokens,
          midRes.grammarState,
        )
      ) {
        saveCheckpoint(scopePrefix, midPrefix, midTokens, midRes.grammarState, true);
        const tailCode = code.slice(lastSemi + 1);
        const tailRes = hl.codeToTokens(tailCode, {
          lang: typedLang,
          theme: shikiTheme,
          grammarState: midRes.grammarState,
        });
        return mergeInlineTokens(midTokens, shiftTokenOffsets(tailRes.tokens, lastSemi + 1));
      }
    }

    const tailCode = code.slice(baseOffset);
    const tailRes = hl.codeToTokens(tailCode, {
      lang: typedLang,
      theme: shikiTheme,
      grammarState: cp.grammarState,
    });
    const out = mergeInlineTokens(cp.tokens, shiftTokenOffsets(tailRes.tokens, baseOffset));
    if (
      !code.endsWith("\r") &&
      isInlineSafeCheckpoint(hl, typedLang, shikiTheme, scopePrefix, code, out, tailRes.grammarState)
    ) {
      saveCheckpoint(scopePrefix, code, out, tailRes.grammarState, true);
    }
    return out;
  }

  if (lastNewline > 0 && code.charCodeAt(lastNewline - 1) !== 13) {
    const prefixCode = code.slice(0, lastNewline);
    const prefRes = hl.codeToTokens(prefixCode, { lang: typedLang, theme: shikiTheme });
    saveCheckpoint(scopePrefix, prefixCode, prefRes.tokens, prefRes.grammarState);
    const tailCode = code.slice(lastNewline + 1);
    const tailRes = hl.codeToTokens(tailCode, {
      lang: typedLang,
      theme: shikiTheme,
      grammarState: prefRes.grammarState,
    });
    const out = prefRes.tokens.concat(shiftTokenOffsets(tailRes.tokens, lastNewline + 1));
    if (
      !code.endsWith("\r") &&
      isInlineSafeCheckpoint(hl, typedLang, shikiTheme, scopePrefix, code, out, tailRes.grammarState)
    ) {
      saveCheckpoint(scopePrefix, code, out, tailRes.grammarState, true);
    }
    return out;
  }

  const lastSemi = code.length > 256 && !code.endsWith(";") ? code.lastIndexOf(";", code.length - 2) : -1;
  if (lastSemi > 128) {
    const prefixCode = code.slice(0, lastSemi + 1);
    const prefRes = hl.codeToTokens(prefixCode, { lang: typedLang, theme: shikiTheme });
    if (
      isInlineSafeCheckpoint(
        hl,
        typedLang,
        shikiTheme,
        scopePrefix,
        prefixCode,
        prefRes.tokens,
        prefRes.grammarState,
      )
    ) {
      saveCheckpoint(scopePrefix, prefixCode, prefRes.tokens, prefRes.grammarState, true);
      const tailCode = code.slice(lastSemi + 1);
      const tailRes = hl.codeToTokens(tailCode, {
        lang: typedLang,
        theme: shikiTheme,
        grammarState: prefRes.grammarState,
      });
      return mergeInlineTokens(prefRes.tokens, shiftTokenOffsets(tailRes.tokens, lastSemi + 1));
    }
  }

  const fullRes = hl.codeToTokens(code, { lang: typedLang, theme: shikiTheme });
  if (!code.endsWith("\r")) {
    const inlineSafe = isInlineSafeCheckpoint(
      hl,
      typedLang,
      shikiTheme,
      scopePrefix,
      code,
      fullRes.tokens,
      fullRes.grammarState,
    );
    saveCheckpoint(scopePrefix, code, fullRes.tokens, fullRes.grammarState, inlineSafe);
  }
  return fullRes.tokens;
}

export function highlightCodeSync(
  code: string,
  language: string,
  theme: "dark" | "light",
  previousCode?: string,
): HighlightedCode | undefined {
  const lang = language.trim().toLowerCase();
  if (!isWithinHighlightLimits(code, lang)) return undefined;

  const cached = getCachedHighlight(code, lang, theme);
  if (cached) {
    if (previousCode && previousCode !== code) {
      deleteCachedHighlightKey(buildHighlightCacheKey(previousCode, lang, theme));
    }
    return cached;
  }

  const shikiTheme = theme === "dark" ? "github-dark" : "github-light";
  if (
    !shikiHighlighter ||
    !shikiModule ||
    !Object.hasOwn(shikiModule.bundledLanguages, lang) ||
    !loadedShikiLangs.has(lang) ||
    !loadedShikiThemes.has(shikiTheme)
  ) {
    return undefined;
  }

  const normLang = normalizeHighlightLang(lang);
  const scopePrefix = `${theme}\0${normLang}\0`;
  const cp = findCheckpoint(scopePrefix, code);
  if (!cp && code.length > 1024) {
    return undefined;
  }
  if (cp && code.length - cp.prefixCode.length > 4096) {
    return undefined;
  }

  try {
    const tokens = tokenizeWithCheckpoints(shikiHighlighter, code, lang, shikiTheme, scopePrefix);
    return setCachedHighlight(code, lang, theme, { code, tokens }, previousCode);
  } catch {
    return undefined;
  }
}

export async function highlightCode(
  code: string,
  language: string,
  theme: "dark" | "light",
  previousCode?: string,
): Promise<HighlightedCode | undefined> {
  const lang = language.trim().toLowerCase();
  // Keep large blocks and unlabelled output cheap while preserving their full text.
  if (!isWithinHighlightLimits(code, lang)) return;

  const cached = getCachedHighlight(code, lang, theme);
  if (cached) {
    if (previousCode && previousCode !== code) {
      deleteCachedHighlightKey(buildHighlightCacheKey(previousCode, lang, theme));
    }
    return cached;
  }

  try {
    const shiki = await (shikiPromise ??= import("shiki"));
    shikiModule = shiki;
    if (!Object.hasOwn(shiki.bundledLanguages, lang)) return;
    const shikiTheme = theme === "dark" ? "github-dark" : "github-light";
    if (!shikiHighlighter || !loadedShikiLangs.has(lang) || !loadedShikiThemes.has(shikiTheme)) {
      const hl = await shiki.getSingletonHighlighter({
        langs: [lang as keyof typeof shiki.bundledLanguages],
        themes: [shikiTheme],
      });
      shikiHighlighter = hl;
      for (const l of hl.getLoadedLanguages()) loadedShikiLangs.add(l);
      loadedShikiLangs.add(lang);
      for (const t of hl.getLoadedThemes()) loadedShikiThemes.add(t);
      loadedShikiThemes.add(shikiTheme);
    }
    const normLang = normalizeHighlightLang(lang);
    const scopePrefix = `${theme}\0${normLang}\0`;
    const tokens = tokenizeWithCheckpoints(shikiHighlighter, code, lang, shikiTheme, scopePrefix);
    const entry: HighlightedCode = { code, tokens };
    return setCachedHighlight(code, lang, theme, entry, previousCode);
  } catch {
    // Loading or tokenization failures must never hide the original code.
    shikiPromise = undefined;
    return;
  }
}
