// web/src/store/file-links.ts
// Ported from Kilo-Org/kilocode (file-path.ts + file-link-validator.ts)
// with Hoxi external-path, Unicode/space, and resolvedPath support.

import { hoxiAPI } from "../services/api";

const KNOWN_EXTENSIONS = new Set([
  // Web & JS/TS
  "ts", "tsx", "js", "jsx", "mjs", "cjs", "mts", "cts", "vue", "svelte", "astro",
  "html", "htm", "css", "scss", "sass", "less", "wasm", "wat",
  // Backend & Systems
  "go", "mod", "sum", "work", "py", "pyi", "rs", "rb", "java", "kt", "kts", "scala", "groovy",
  "c", "h", "cpp", "hpp", "cc", "cxx", "cs", "fs", "fsx", "vb", "swift", "m", "mm", "dart",
  "php", "pl", "pm", "lua", "r", "jl", "ex", "exs", "erl", "hrl", "hs", "ml", "mli",
  "clj", "cljs", "cljc", "edn", "elm", "zig", "nim", "cr", "v", "sv", "vhd", "vhdl",
  "glsl", "wgsl", "hlsl", "metal", "sol", "move", "cairo",
  // Data & Config
  "json", "jsonc", "json5", "yaml", "yml", "toml", "xml", "plist", "ini", "cfg", "conf",
  "env", "properties", "lock", "editorconfig", "gitignore", "gitattributes", "gitmodules",
  "dockerignore", "npmignore", "eslintignore", "prettierignore", "npmrc", "nvmrc",
  "node-version", "python-version", "ruby-version", "tool-versions",
  "prettierrc", "eslintrc", "babelrc", "stylelintrc", "yarnrc",
  "bashrc", "zshrc", "profile", "clang-format", "clang-tidy",
  // Docs & Text
  "md", "mdx", "markdown", "txt", "rst", "adoc", "org", "tex", "bib", "csv", "tsv", "log",
  // Shell & Scripts
  "sh", "bash", "zsh", "fish", "ps1", "psm1", "bat", "cmd", "awk", "sed",
  // Query, Schema & Infra
  "sql", "graphql", "gql", "proto", "prisma", "tf", "tfvars", "hcl", "nix", "bicep",
  "dockerfile", "makefile", "mk", "cmake", "gradle", "podspec", "gemfile", "rakefile",
  // Media & Documents (supported by Inspector preview / external open)
  "svg", "png", "jpg", "jpeg", "gif", "webp", "ico", "bmp", "avif", "pdf",
]);

const EXTENSIONLESS_FILENAMES = new Set([
  "Dockerfile", "Containerfile", "Makefile", "GNUmakefile", "CMakeLists.txt",
  "Gemfile", "Rakefile", "Guardfile", "Podfile", "Fastfile", "Appfile", "Brewfile",
  "Procfile", "Caddyfile", "Vagrantfile", "Justfile", "Taskfile", "Earthfile", "Tiltfile",
  "BUILD", "WORKSPACE",
  "LICENSE", "LICENCE", "COPYING", "NOTICE", "AUTHORS", "CONTRIBUTORS", "CONTRIBUTING",
  "README", "CHANGELOG", "CHANGES", "HISTORY", "SECURITY", "CODEOWNERS",
  "AGENTS", "CLAUDE",
]);

const NON_PATH_SLASH_WORDS = new Set([
  "and/or", "yes/no", "true/false", "on/off", "in/out", "up/down",
  "input/output", "read/write", "client/server", "parent/child",
  "before/after", "start/end", "min/max", "left/right", "key/value",
  "source/target", "request/response", "pub/sub", "async/await",
  "master/slave", "primary/secondary", "active/passive", "open/close",
  "show/hide", "enable/disable", "success/failure", "pass/fail",
  "push/pull", "send/receive", "get/set", "add/remove", "create/update",
  "import/export", "encode/decode", "encrypt/decrypt", "lock/unlock",
  "text/plain", "text/html", "text/css", "text/javascript", "text/csv",
  "text/event-stream", "text/markdown", "text/xml",
  "application/json", "application/xml", "application/pdf",
  "application/octet-stream", "application/x-www-form-urlencoded",
  "multipart/form-data", "image/png", "image/jpeg", "image/gif",
  "image/webp", "image/svg+xml", "audio/mpeg", "video/mp4",
  "w/o", "n/a", "c/o", "b/w", "i/o", "r/w", "ci/cd", "ui/ux", "qa/qc",
  "tcp/ip", "http/https", "ws/sse", "rest/sse",
]);

export interface ExtractedFileSuffix {
  path: string;
  line?: number;
  column?: number;
}

/**
 * Extracts optional `#L10`, `#L10-L20`, `:10`, `:10:5`, or `(10,5)` suffixes from a file reference.
 */
export function extractFileSuffix(raw: string): ExtractedFileSuffix {
  let text = raw.trim();
  if (!text) return { path: "" };

  let line: number | undefined;
  let column: number | undefined;

  // 1. GitHub / Antigravity anchor format: path#L12 or path#L12-L20 or path#L12C5
  const hashMatch = text.match(/^(.*?)#L(\d+)(?:C(\d+))?(?:-L?\d+(?:C\d+)?)?$/i);
  if (hashMatch && hashMatch[1]) {
    text = hashMatch[1];
    line = Number.parseInt(hashMatch[2], 10);
    if (hashMatch[3]) column = Number.parseInt(hashMatch[3], 10);
    return { path: text, line, column };
  }

  // 2. Parenthesized compiler format: path(12) or path(12,5)
  const parenMatch = text.match(/^(.+?)\((\d+)(?:,(\d+))?\)$/);
  if (parenMatch && parenMatch[1] && /[./\\]/.test(parenMatch[1])) {
    text = parenMatch[1];
    line = Number.parseInt(parenMatch[2], 10);
    if (parenMatch[3]) column = Number.parseInt(parenMatch[3], 10);
    return { path: text, line, column };
  }

  // 3. Colon format: path:12 or path:12:5 (preserve Windows drive letter C:\...)
  const colonMatch = text.match(/^(.+?):(\d+)(?::(\d+))?$/);
  if (colonMatch && colonMatch[1] && !/^[A-Za-z]$/.test(colonMatch[1])) {
    text = colonMatch[1];
    line = Number.parseInt(colonMatch[2], 10);
    if (colonMatch[3]) column = Number.parseInt(colonMatch[3], 10);
  }

  return { path: text, line, column };
}

function safeDecodeURIComponent(value: string): string {
  try {
    return decodeURIComponent(value);
  } catch {
    return value;
  }
}

/**
 * Normalizes a raw file reference into a clean path (without `#L10` or `:10:5`),
 * preserving absolute paths (`/Users/...`, `C:/...`), `~/...`, and `../...`.
 */
export function normalizeCandidatePath(raw: string): string {
  let s = raw.trim();
  if (!s) return "";

  if (
    (s.startsWith("`") && s.endsWith("`")) ||
    (s.startsWith('"') && s.endsWith('"')) ||
    (s.startsWith("'") && s.endsWith("'"))
  ) {
    s = s.slice(1, -1).trim();
  }

  if (/^file:\/\//i.test(s)) {
    s = s.replace(/^file:\/\//i, "");
    if (/^localhost\//i.test(s)) {
      s = s.slice("localhost".length);
    }
    if (/^\/[A-Za-z]:[\\/]/.test(s)) {
      s = s.slice(1);
    }
    s = safeDecodeURIComponent(s);
  }

  const { path } = extractFileSuffix(s);
  s = path.replace(/\\/g, "/").trim();
  if (s.startsWith("./")) {
    s = s.slice(2);
  }
  return s;
}

function getFileExtension(cleanPath: string): string {
  const base = cleanPath.split("/").pop() || "";
  if (!base) return "";
  if (base.startsWith(".")) {
    const rest = base.slice(1);
    const dotIdx = rest.lastIndexOf(".");
    return (dotIdx >= 0 ? rest.slice(dotIdx + 1) : rest).toLowerCase();
  }
  const dotIdx = base.lastIndexOf(".");
  if (dotIdx <= 0) return "";
  return base.slice(dotIdx + 1).toLowerCase();
}

function isKnownFileBase(cleanPath: string): boolean {
  const base = cleanPath.split("/").pop() || "";
  if (!base) return false;
  if (EXTENSIONLESS_FILENAMES.has(base)) return true;
  const stem = base.split(".")[0] || "";
  if (stem && EXTENSIONLESS_FILENAMES.has(stem)) return true;
  if (/^\.(?:env|babelrc|eslintrc|prettierrc|stylelintrc|yarnrc)(?:\.[\w.-]+)?$/i.test(base)) {
    return true;
  }
  const ext = getFileExtension(cleanPath);
  return ext !== "" && KNOWN_EXTENSIONS.has(ext);
}

/**
 * High-confidence check that `raw` is a file path with directory structure or explicit path prefix.
 * Supports external paths (`~/.gemini/...`, `/Users/...`, `/tmp/...`), Unicode/Chinese segments,
 * spaces (when inside links or explicit paths), and route segments like `[id]` or `+page.tsx`.
 */
export function looksLikeFilePath(raw: string): boolean {
  const trimmed = raw.trim();
  if (!trimmed || trimmed.length > 512) return false;
  if (/[\r\n\0<>|?*]/.test(trimmed)) return false;

  // Reject web URLs, mailto, data URIs, etc.
  if (/^[a-zA-Z][a-zA-Z0-9+.-]*:\/\//.test(trimmed) && !/^file:\/\//i.test(trimmed)) {
    return false;
  }
  if (/^(mailto|tel|javascript|vbscript|data):/i.test(trimmed)) {
    return false;
  }

  const clean = normalizeCandidatePath(trimmed);
  if (!clean || clean === "/" || clean === "~" || clean === "." || clean === "..") {
    return false;
  }

  const lower = clean.toLowerCase();
  if (NON_PATH_SLASH_WORDS.has(lower)) {
    return false;
  }

  // Reject CLI flag combinations or shell snippets
  if (clean.startsWith("-") || /\s--(?!\S*\/)/.test(clean)) {
    return false;
  }

  const isExplicitFileURL = /^file:\/\//i.test(trimmed);
  const isHomePath = clean.startsWith("~/");
  const isPosixAbs = clean.startsWith("/");
  const isWinAbs = /^[A-Za-z]:\//.test(clean);
  const isRelDot = raw.trim().startsWith("./") || clean.startsWith("../");

  if (isExplicitFileURL || isHomePath || isWinAbs || isRelDot) {
    return clean.split("/").filter(Boolean).length >= 1;
  }

  if (isPosixAbs) {
    // Reject single-segment slash commands like `/compact` or `/plan` unless it has a known extension
    const segments = clean.split("/").filter(Boolean);
    if (segments.length === 0) return false;
    if (segments.length === 1) {
      return isKnownFileBase(clean);
    }
    return true;
  }

  // Relative path with `/`
  if (clean.includes("/")) {
    if (/\s/.test(clean) && !isKnownFileBase(clean)) {
      return false;
    }
    const segments = clean.split("/");
    if (segments.some((seg) => seg === "" || seg === ".")) {
      return false;
    }
    // Either has a known file extension/basename or at least 2 path segments with an extension
    if (isKnownFileBase(clean)) {
      return true;
    }
    const last = segments[segments.length - 1] || "";
    return segments.length >= 2 && /^[a-zA-Z0-9_.\-\u4e00-\u9fa5]+\.[a-zA-Z0-9]{1,10}$/.test(last);
  }

  // Bare extensionless well-known files (Dockerfile, Makefile)
  return EXTENSIONLESS_FILENAMES.has(clean);
}

/**
 * Broader check used for inline `code` spans (Kilo's `looksLikeCandidate`):
 * matches full/relative paths AND bare filenames with known extensions (e.g. `gateway.go`, `package.json`)
 * so they can be verified via `checkFile` against the workspace.
 */
export function looksLikeCandidate(raw: string): boolean {
  const trimmed = raw.trim();
  if (!trimmed || trimmed.length > 512) return false;
  if (looksLikeFilePath(trimmed)) return true;

  // Bare filename with no spaces (e.g. `gateway.go`, `FileTreeView.tsx:42`, `package.json`)
  if (/\s/.test(trimmed)) return false;
  if (/^[a-zA-Z][a-zA-Z0-9+.-]*:/.test(trimmed)) return false;

  const clean = normalizeCandidatePath(trimmed);
  if (!clean || clean.includes("/")) return false;
  // Reject pure numbers or semver-like strings (`v1.2.3`, `127.0.0.1`)
  if (/^v?\d+(\.\d+)+$/i.test(clean)) return false;
  // Reject JS property chains like `window.location` or `console.log` or `err.Error`
  const withoutLeadingDot = clean.startsWith(".") ? clean.slice(1) : clean;
  const dots = withoutLeadingDot.split(".");
  if (dots.length > 2) {
    // Allow `.test.ts`, `.spec.tsx`, `.d.ts`, `.min.js`, `.config.ts`, `.module.css`, `.env.local`
    const secondLast = dots[dots.length - 2].toLowerCase();
    const allowedCompound = new Set([
      "test", "spec", "d", "min", "config", "module", "stories", "pb", "gen", "types", "zh-cn", "en-us",
      "env", "eslintrc", "prettierrc", "babelrc", "stylelintrc", "yarnrc",
    ]);
    if (!allowedCompound.has(secondLast)) return false;
  }

  return isKnownFileBase(clean);
}

/**
 * Extracts a clickable file reference (preserving `:line` or `#Lline` suffix) from a Markdown link `href`.
 * Returns `null` if the `href` is an external web URL or non-file link.
 */
export function extractFilePathFromHref(rawHref: string): string | null {
  const trimmed = rawHref.trim();
  if (!trimmed || trimmed.startsWith("#")) return null;
  if (/^(https?|mailto|tel|javascript|vbscript|data|ws|wss):/i.test(trimmed)) {
    return null;
  }

  if (/^file:\/\//i.test(trimmed)) {
    let rest = trimmed.replace(/^file:\/\//i, "");
    if (/^localhost\//i.test(rest)) {
      rest = rest.slice("localhost".length);
    }
    if (/^\/[A-Za-z]:[\\/]/.test(rest)) {
      rest = rest.slice(1);
    }
    const decoded = safeDecodeURIComponent(rest);
    return decoded || null;
  }

  const decoded = safeDecodeURIComponent(trimmed);
  if (looksLikeFilePath(decoded) || looksLikeCandidate(decoded)) {
    return decoded;
  }
  return null;
}

// ---------------------------------------------------------------------------
// Kilo-style Batched File Validator with Dual Positive/Negative LRU Cache
// ---------------------------------------------------------------------------

const MAX_POSITIVE_CACHE = 5000;
const MAX_NEGATIVE_CACHE = 2000;

const positiveCache = new Map<string, boolean>();
const negativeCache = new Map<string, boolean>();
const resolvedPathCache = new Map<string, string>();
const inflightRequests = new Map<string, Promise<boolean>>();

interface BatchBucket {
  directory: string;
  sessionID?: string;
  candidates: Map<string, Array<(exists: boolean) => void>>;
}

const pendingBuckets = new Map<string, BatchBucket>();
let flushScheduled = false;

function makeCacheKey(directory: string, cleanPath: string): string {
  return `${directory}::${cleanPath}`;
}

function lruSet<V>(map: Map<string, V>, key: string, value: V, maxSize: number): void {
  if (map.has(key)) {
    map.delete(key);
  } else if (map.size >= maxSize) {
    const oldest = map.keys().next().value;
    if (oldest !== undefined) map.delete(oldest);
  }
  map.set(key, value);
}

function lruGet<V>(map: Map<string, V>, key: string): V | undefined {
  if (!map.has(key)) return undefined;
  const val = map.get(key)!;
  map.delete(key);
  map.set(key, val);
  return val;
}

export function clearFileLinkCache(): void {
  positiveCache.clear();
  negativeCache.clear();
  resolvedPathCache.clear();
}

export function getResolvedFilePath(rawCandidate: string, directory = ""): string | undefined {
  const clean = normalizeCandidatePath(rawCandidate);
  if (!clean) return undefined;
  return lruGet(resolvedPathCache, makeCacheKey(directory, clean));
}

function scheduleBatchFlush(): void {
  if (flushScheduled) return;
  flushScheduled = true;
  queueMicrotask(() => {
    flushScheduled = false;
    const buckets = Array.from(pendingBuckets.values());
    pendingBuckets.clear();

    for (const bucket of buckets) {
      const candidateList = Array.from(bucket.candidates.keys());
      if (candidateList.length === 0) continue;

      hoxiAPI
        .validateFiles(candidateList, bucket.directory || undefined, bucket.sessionID)
        .then((res) => {
          const existingSet = new Set(res?.existing || []);
          const resolvedMap = res?.resolved || {};
          for (const [cleanPath, resolvers] of bucket.candidates.entries()) {
            const exists = existingSet.has(cleanPath);
            const cacheKey = makeCacheKey(bucket.directory, cleanPath);
            if (exists) {
              lruSet(positiveCache, cacheKey, true, MAX_POSITIVE_CACHE);
              if (resolvedMap[cleanPath]) {
                lruSet(resolvedPathCache, cacheKey, resolvedMap[cleanPath], MAX_POSITIVE_CACHE);
              }
            } else {
              lruSet(negativeCache, cacheKey, false, MAX_NEGATIVE_CACHE);
            }
            for (const resolve of resolvers) {
              resolve(exists);
            }
          }
        })
        .catch(() => {
          // On transient network error, resolve false without poisoning the negative cache
          for (const resolvers of bucket.candidates.values()) {
            for (const resolve of resolvers) {
              resolve(false);
            }
          }
        });
    }
  });
}

/**
 * Validates whether `rawCandidate` exists on disk (either inside the workspace, via basename
 * fallback, or as an external host path). Collects synchronous calls in the same tick into
 * a single `POST /api/hoxi/file/validate` request.
 */
export function checkFile(
  rawCandidate: string,
  directory = "",
  sessionID?: string
): Promise<boolean> {
  const clean = normalizeCandidatePath(rawCandidate);
  if (!clean) return Promise.resolve(false);

  const cacheKey = makeCacheKey(directory, clean);
  if (lruGet(positiveCache, cacheKey) === true) {
    return Promise.resolve(true);
  }
  if (lruGet(negativeCache, cacheKey) === false) {
    return Promise.resolve(false);
  }

  const inflight = inflightRequests.get(cacheKey);
  if (inflight) return inflight;

  const bucketKey = `${directory}::${sessionID || ""}`;
  let bucket = pendingBuckets.get(bucketKey);
  if (!bucket) {
    bucket = {
      directory,
      sessionID,
      candidates: new Map(),
    };
    pendingBuckets.set(bucketKey, bucket);
  }

  const promise = new Promise<boolean>((resolve) => {
    const list = bucket!.candidates.get(clean) || [];
    list.push(resolve);
    bucket!.candidates.set(clean, list);
  }).finally(() => {
    inflightRequests.delete(cacheKey);
  });

  inflightRequests.set(cacheKey, promise);
  scheduleBatchFlush();
  return promise;
}
